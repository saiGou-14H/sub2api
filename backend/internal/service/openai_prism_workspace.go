package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const OpenAIPrismProjectTTL = 7 * 24 * time.Hour
const prismProjectLockTTL = 15 * time.Minute

// PrismProjectStore is an optional extension of GatewayCache. Ownership and
// private upstream identifiers share one record; a missing record never falls
// back to trusting a caller-supplied upstream project identifier.
type PrismProjectStore interface {
	GetPrismProject(context.Context, string) ([]byte, error)
	SetPrismProject(context.Context, string, []byte, time.Duration) error
	SavePrismProjectWithLease(context.Context, string, string, []byte, time.Duration) (bool, error)
	TryAcquirePrismProjectLock(context.Context, string, string, time.Duration) (bool, error)
	RefreshPrismProjectLock(context.Context, string, string, time.Duration) (bool, error)
	ReleasePrismProjectLock(context.Context, string, string) error
}

type PrismWorkspaceError struct {
	Status  int
	Message string
}

func (e *PrismWorkspaceError) Error() string { return e.Message }

func prismWorkspaceError(status int, message string) error {
	return &PrismWorkspaceError{Status: status, Message: message}
}

// OpenAIPrismProject is private service state. Only Public may be serialized
// into a gateway response. Sandbox credentials and provider IDs stay server-side.
type OpenAIPrismProject struct {
	Handle         string                      `json:"-"`
	Title          string                      `json:"-"`
	Model          string                      `json:"-"`
	APIKeyID       int64                       `json:"-"`
	UserID         int64                       `json:"-"`
	GroupID        int64                       `json:"-"`
	AccountID      int64                       `json:"-"`
	CredentialHash string                      `json:"-"`
	State          OpenAIPrismSessionState     `json:"-"`
	Files          map[string]PrismProjectFile `json:"-"`
	CreatedAt      time.Time                   `json:"-"`
	ExpiresAt      time.Time                   `json:"-"`
	lease          *prismProjectLease
}

type PrismProjectFile struct {
	NodeID   string
	BlobUUID string
	FileName string
	Path     string
}

// The explicit wire type prevents accidental serialization of the live lease.
type prismProjectRecord struct {
	Handle         string
	Title          string
	Model          string
	APIKeyID       int64
	UserID         int64
	GroupID        int64
	AccountID      int64
	CredentialHash string
	State          OpenAIPrismSessionState
	Files          map[string]PrismProjectFile
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type prismProjectLease struct {
	owner  string
	ctx    context.Context
	cancel context.CancelFunc
	lost   atomic.Bool
}

// PrismProjectLeaseContext lets the upstream request stop when renewal loses
// the lease. Persisting state additionally checks ownership atomically.
func PrismProjectLeaseContext(ctx context.Context, project *OpenAIPrismProject) context.Context {
	if project != nil && project.lease != nil {
		return project.lease.ctx
	}
	return ctx
}

func encodePrismProject(project *OpenAIPrismProject) ([]byte, error) {
	return json.Marshal(prismProjectRecord{Handle: project.Handle, Title: project.Title, Model: project.Model, APIKeyID: project.APIKeyID, UserID: project.UserID, GroupID: project.GroupID, AccountID: project.AccountID, CredentialHash: project.CredentialHash, State: project.State, Files: project.Files, CreatedAt: project.CreatedAt, ExpiresAt: project.ExpiresAt})
}

func (p *OpenAIPrismProject) Public() map[string]any {
	return map[string]any{"id": p.Handle, "object": "prism.project", "title": p.Title, "model": p.Model, "created_at": p.CreatedAt.Unix(), "expires_at": p.ExpiresAt.Unix()}
}

type prismProjectContextKey struct{}

func WithPrismProject(ctx context.Context, project *OpenAIPrismProject) context.Context {
	return context.WithValue(ctx, prismProjectContextKey{}, project)
}

func PrismProjectFromContext(ctx context.Context) *OpenAIPrismProject {
	if ctx == nil {
		return nil
	}
	project, _ := ctx.Value(prismProjectContextKey{}).(*OpenAIPrismProject)
	return project
}

type prismWorkspaceEntry struct {
	payload   []byte
	expiresAt time.Time
	transport *OpenAIPrismTransport
	gate      chan struct{}
}

type prismWorkspaceRuntime struct {
	mu      sync.Mutex
	entries map[string]*prismWorkspaceEntry
}

func (s *OpenAIGatewayService) prismWorkspaceRuntime() *prismWorkspaceRuntime {
	s.prismWorkspaceOnce.Do(func() { s.prismWorkspace = &prismWorkspaceRuntime{entries: make(map[string]*prismWorkspaceEntry)} })
	return s.prismWorkspace
}

func (s *OpenAIGatewayService) prismProjectStore() PrismProjectStore {
	if s == nil {
		return nil
	}
	store, _ := s.cache.(PrismProjectStore)
	return store
}

func validPrismProjectHandle(handle string) bool {
	if !strings.HasPrefix(handle, "prism_proj_") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(handle, "prism_proj_"))
	return err == nil
}

func prismProjectCredentialHash(account *Account) string {
	material, _ := json.Marshal([]string{prismAccessToken(account), prismCredential(account, "prism_session_token"), prismCredential(account, "prism_cookie")})
	hash := sha256.Sum256(material)
	return hex.EncodeToString(hash[:])
}

func validatePrismIdentity(key *APIKey) error {
	if key == nil || key.ID <= 0 || key.UserID <= 0 {
		return prismWorkspaceError(http.StatusUnauthorized, "Invalid API key")
	}
	if key.GroupID == nil || key.Group == nil || key.Group.ID != *key.GroupID || key.Group.Platform != PlatformOpenAI {
		return prismWorkspaceError(http.StatusForbidden, "Prism projects require an OpenAI group")
	}
	return nil
}

func (s *OpenAIGatewayService) savePrismProject(ctx context.Context, project *OpenAIPrismProject) error {
	if project.lease != nil && (project.lease.lost.Load() || project.lease.ctx.Err() != nil) {
		return prismWorkspaceError(http.StatusConflict, "Prism project lease expired; retry later")
	}
	project.ExpiresAt = time.Now().Add(OpenAIPrismProjectTTL)
	payload, err := encodePrismProject(project)
	if err != nil {
		return err
	}
	if store := s.prismProjectStore(); store != nil {
		if project.lease != nil {
			saved, err := store.SavePrismProjectWithLease(ctx, project.Handle, project.lease.owner, payload, OpenAIPrismProjectTTL)
			if err != nil || !saved {
				project.lease.lost.Store(true)
				project.lease.cancel()
				return prismWorkspaceError(http.StatusConflict, "Prism project lease expired; retry later")
			}
		} else if err := store.SetPrismProject(ctx, project.Handle, payload, OpenAIPrismProjectTTL); err != nil {
			return err
		}
	}
	runtime := s.prismWorkspaceRuntime()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	entry := runtime.entries[project.Handle]
	if entry != nil && len(entry.payload) > 0 && project.lease == nil {
		return prismWorkspaceError(http.StatusConflict, "Prism project update requires a lease")
	}
	if project.lease != nil && (entry == nil || !entry.expiresAt.After(time.Now())) {
		return prismWorkspaceError(http.StatusConflict, "Prism project expired during the operation")
	}
	if entry == nil {
		if len(runtime.entries) >= 1024 {
			for handle, candidate := range runtime.entries {
				if !candidate.expiresAt.After(time.Now()) && len(candidate.gate) == 0 {
					delete(runtime.entries, handle)
				}
			}
			if len(runtime.entries) >= 1024 {
				return prismWorkspaceError(http.StatusServiceUnavailable, "Prism project capacity reached")
			}
		}
		entry = &prismWorkspaceEntry{gate: make(chan struct{}, 1)}
		runtime.entries[project.Handle] = entry
	}
	entry.payload, entry.expiresAt = append([]byte(nil), payload...), project.ExpiresAt
	return nil
}

func (s *OpenAIGatewayService) readPrismProject(ctx context.Context, handle string) (*OpenAIPrismProject, error) {
	if !validPrismProjectHandle(handle) {
		return nil, prismWorkspaceError(http.StatusNotFound, "Prism project not found")
	}
	var payload []byte
	if store := s.prismProjectStore(); store != nil {
		var err error
		payload, err = store.GetPrismProject(ctx, handle)
		if err != nil {
			return nil, prismWorkspaceError(http.StatusServiceUnavailable, "Prism project storage unavailable")
		}
	} else {
		runtime := s.prismWorkspaceRuntime()
		runtime.mu.Lock()
		if entry := runtime.entries[handle]; entry != nil && entry.expiresAt.After(time.Now()) {
			payload = append([]byte(nil), entry.payload...)
		}
		runtime.mu.Unlock()
	}
	var record prismProjectRecord
	if len(payload) == 0 || json.Unmarshal(payload, &record) != nil || record.Handle != handle || !record.ExpiresAt.After(time.Now()) {
		return nil, prismWorkspaceError(http.StatusNotFound, "Prism project not found")
	}
	project := OpenAIPrismProject{Handle: record.Handle, Title: record.Title, Model: record.Model, APIKeyID: record.APIKeyID, UserID: record.UserID, GroupID: record.GroupID, AccountID: record.AccountID, CredentialHash: record.CredentialHash, State: record.State, Files: record.Files, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt}
	return &project, nil
}

func (s *OpenAIGatewayService) prismProjectAccount(ctx context.Context, project *OpenAIPrismProject) (*Account, error) {
	if s == nil || s.accountRepo == nil || project == nil {
		return nil, prismWorkspaceError(http.StatusServiceUnavailable, "Prism account unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, project.AccountID)
	if err != nil || account == nil || !account.IsOpenAIPrismTransport() || !openAIStickyAccountMatchesGroup(account, &project.GroupID) {
		return nil, prismWorkspaceError(http.StatusNotFound, "Prism project not found")
	}
	credentials, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil || credentials == nil || prismProjectCredentialHash(credentials) != project.CredentialHash {
		return nil, prismWorkspaceError(http.StatusConflict, "Prism project credentials changed; create a new project")
	}
	return account, nil
}

func (s *OpenAIGatewayService) LoadPrismProject(ctx context.Context, key *APIKey, handle string) (*OpenAIPrismProject, error) {
	if err := validatePrismIdentity(key); err != nil {
		return nil, err
	}
	project, err := s.readPrismProject(ctx, strings.TrimSpace(handle))
	if err != nil {
		return nil, err
	}
	if project.APIKeyID != key.ID || project.UserID != key.UserID || project.GroupID != *key.GroupID {
		return nil, prismWorkspaceError(http.StatusNotFound, "Prism project not found")
	}
	if _, err := s.prismProjectAccount(ctx, project); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *OpenAIGatewayService) ResolvePrismProjectAccount(ctx context.Context, key *APIKey, handle string) (*Account, error) {
	project, err := s.LoadPrismProject(ctx, key, handle)
	if err != nil {
		return nil, err
	}
	return s.prismProjectAccount(ctx, project)
}

// LockPrismProject serializes chat, uploads and rendering across processes. A
// cache outage is an error, not permission to use only the local lock.
func (s *OpenAIGatewayService) LockPrismProject(ctx context.Context, project *OpenAIPrismProject) (func(), error) {
	if project == nil || !validPrismProjectHandle(project.Handle) {
		return nil, prismWorkspaceError(http.StatusNotFound, "Prism project not found")
	}
	runtime := s.prismWorkspaceRuntime()
	runtime.mu.Lock()
	entry := runtime.entries[project.Handle]
	if entry == nil {
		entry = &prismWorkspaceEntry{gate: make(chan struct{}, 1), expiresAt: project.ExpiresAt}
		runtime.entries[project.Handle] = entry
	}
	runtime.mu.Unlock()
	select {
	case entry.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	store := s.prismProjectStore()
	owner := uuid.NewString()
	if store != nil {
		claimed, err := store.TryAcquirePrismProjectLock(ctx, project.Handle, owner, prismProjectLockTTL)
		if err != nil || !claimed {
			<-entry.gate
			return nil, prismWorkspaceError(http.StatusConflict, "Prism project is busy; retry later")
		}
	}
	// Reload after claiming the lease so a turn waiting on another instance
	// cannot overwrite newer conversation state with its pre-lock snapshot.
	latest, err := s.readPrismProject(ctx, project.Handle)
	if err == nil && (latest.APIKeyID != project.APIKeyID || latest.UserID != project.UserID || latest.GroupID != project.GroupID || latest.AccountID != project.AccountID || latest.CredentialHash != project.CredentialHash || latest.State.ProjectID != project.State.ProjectID) {
		err = prismWorkspaceError(http.StatusConflict, "Prism project identity changed")
	}
	if err == nil {
		_, err = s.prismProjectAccount(ctx, latest)
	}
	if err != nil {
		if store != nil {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = store.ReleasePrismProjectLock(releaseCtx, project.Handle, owner)
			cancel()
		}
		<-entry.gate
		return nil, err
	}
	*project = *latest
	// Rebuild the transport after loading the shared record: its cookie jar may
	// predate a successful rotation performed by another gateway instance.
	runtime.mu.Lock()
	entry.transport = nil
	entry.expiresAt = latest.ExpiresAt
	runtime.mu.Unlock()
	leaseCtx, cancelLease := context.WithTimeout(ctx, 14*time.Minute)
	lease := &prismProjectLease{owner: owner, ctx: leaseCtx, cancel: cancelLease}
	project.lease = lease
	handle := project.Handle
	done := make(chan struct{})
	if store != nil {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-lease.ctx.Done():
					return
				case <-ticker.C:
					renewCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					refreshed := refreshPrismProjectLease(renewCtx, store, handle, lease)
					cancel()
					if !refreshed {
						return
					}
				}
			}
		}()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			lease.lost.Store(true)
			lease.cancel()
			if store != nil {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = store.ReleasePrismProjectLock(releaseCtx, project.Handle, owner)
				cancel()
			}
			<-entry.gate
		})
	}, nil
}

func refreshPrismProjectLease(ctx context.Context, store PrismProjectStore, handle string, lease *prismProjectLease) bool {
	refreshed, err := store.RefreshPrismProjectLock(ctx, handle, lease.owner, prismProjectLockTTL)
	if err != nil || !refreshed {
		lease.lost.Store(true)
		lease.cancel()
		return false
	}
	return true
}

func (s *OpenAIGatewayService) PrismProjectTransport(project *OpenAIPrismProject) *OpenAIPrismTransport {
	runtime := s.prismWorkspaceRuntime()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	entry := runtime.entries[project.Handle]
	if entry == nil {
		entry = &prismWorkspaceEntry{gate: make(chan struct{}, 1), expiresAt: project.ExpiresAt}
		runtime.entries[project.Handle] = entry
	}
	if entry.transport == nil {
		entry.transport = s.newOpenAIPrismTransport()
	}
	return entry.transport
}

func (s *OpenAIGatewayService) SavePrismProjectState(ctx context.Context, project *OpenAIPrismProject, state OpenAIPrismSessionState) error {
	if project == nil || project.lease == nil || state.ProjectID == "" || state.ProjectID != project.State.ProjectID {
		return prismWorkspaceError(http.StatusConflict, "Prism project state mismatch")
	}
	project.State = state
	return s.savePrismProject(ctx, project)
}

// RefreshPrismProjectSandbox is called only while holding the project lock.
// It reauthorizes this project's files without changing its conversation chain.
func (s *OpenAIGatewayService) RefreshPrismProjectSandbox(ctx context.Context, project *OpenAIPrismProject) error {
	ctx = PrismProjectLeaseContext(ctx, project)
	account, err := s.prismProjectAccount(ctx, project)
	if err != nil {
		return err
	}
	credentials, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return err
	}
	transportAccount := *account
	transportAccount.Credentials = credentials.Credentials
	transport := s.PrismProjectTransport(project)
	transport.RestoreCookies(&transportAccount, prismAccessToken(credentials), project.State.Cookies)
	state, err := transport.PrepareProject(ctx, &transportAccount, prismAccessToken(credentials), project.State.ProjectID, project.Title)
	if err != nil {
		return err
	}
	project.State.SandboxURL, project.State.SandboxToken, project.State.Cookies = state.SandboxURL, state.SandboxToken, state.Cookies
	return s.savePrismProject(ctx, project)
}

func (s *OpenAIGatewayService) SelectBoundPrismProjectAccount(ctx context.Context, groupID *int64, project *OpenAIPrismProject, requestedModel string, excludedIDs map[int64]struct{}, capability OpenAIEndpointCapability, requireCompact bool) (*AccountSelectionResult, error) {
	if project == nil || groupID == nil || *groupID != project.GroupID {
		return nil, ErrNoAvailableAccounts
	}
	if _, excluded := excludedIDs[project.AccountID]; excluded {
		return nil, ErrNoAvailableAccounts
	}
	account, err := s.prismProjectAccount(ctx, project)
	if err != nil {
		return nil, err
	}
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	account = s.recheckSelectedOpenAIAccountFromDB(ctx, account, groupID, PlatformOpenAI, requestedModel, requireCompact, capability)
	if account == nil || s.checkChannelPricingRestriction(ctx, groupID, requestedModel) || (s.needsUpstreamChannelRestrictionCheck(ctx, groupID) && s.isUpstreamModelRestrictedByChannel(ctx, *groupID, account, requestedModel, requireCompact)) {
		return nil, ErrNoAvailableAccounts
	}
	result, err := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Acquired {
		return nil, ErrNoAvailableAccounts
	}
	return s.newAcquiredSelectionResult(ctx, account, result.ReleaseFunc)
}

type PrismProjectCreateRequest struct {
	Title string `json:"title"`
	Model string `json:"model"`
}

func (s *OpenAIGatewayService) CreatePrismProject(ctx context.Context, key *APIKey, request PrismProjectCreateRequest) (*OpenAIPrismProject, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := validatePrismIdentity(key); err != nil {
		return nil, err
	}
	request.Title = strings.TrimSpace(request.Title)
	if request.Title == "" {
		request.Title = "Untitled project"
	}
	if len(request.Title) > 200 {
		return nil, prismWorkspaceError(http.StatusBadRequest, "Project title is too long")
	}
	request.Model = strings.TrimSpace(request.Model)
	if request.Model == "" {
		request.Model = OpenAIPrismDefaultModel
	}
	if _, valid := NormalizeOpenAIPrismModel(request.Model); !valid {
		return nil, prismWorkspaceError(http.StatusBadRequest, "Invalid Prism model")
	}
	if !key.Group.ModelAllowlist.Allows(request.Model) {
		return nil, prismWorkspaceError(http.StatusForbidden, "Model is not allowed for this group")
	}
	accounts, err := s.listSchedulableAccounts(ctx, key.GroupID, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	excluded := make(map[int64]struct{})
	for i := range accounts {
		if !accounts[i].IsOpenAIPrismTransport() {
			excluded[accounts[i].ID] = struct{}{}
		}
	}
	selection, _, err := s.SelectAccountWithSchedulerForCapability(WithOpenAIProfitControlSuppressed(ctx), key.GroupID, "", "", request.Model, excluded, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, false, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	if selection == nil || selection.Account == nil {
		return nil, ErrNoAvailableAccounts
	}
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	account := selection.Account
	if !account.IsOpenAIPrismTransport() || !openAIStickyAccountMatchesGroup(account, key.GroupID) {
		return nil, ErrNoAvailableAccounts
	}
	if !selection.Acquired {
		acquired, err := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
		if err != nil {
			return nil, err
		}
		if acquired == nil || !acquired.Acquired {
			return nil, ErrNoAvailableAccounts
		}
		if acquired.ReleaseFunc != nil {
			defer acquired.ReleaseFunc()
		}
	}
	account = s.recheckSelectedOpenAIAccountFromDB(WithOpenAIProfitControlSuppressed(ctx), account, key.GroupID, PlatformOpenAI, request.Model, false, OpenAIEndpointCapabilityResponses)
	if account == nil || !account.IsOpenAIPrismTransport() || !openAIStickyAccountMatchesGroup(account, key.GroupID) {
		return nil, ErrNoAvailableAccounts
	}
	credentials, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, err
	}
	transportAccount := *account
	transportAccount.Credentials = credentials.Credentials
	transport := s.newOpenAIPrismTransport()
	state, err := transport.PrepareProject(ctx, &transportAccount, prismAccessToken(credentials), "", request.Title)
	if err != nil {
		s.prismWorkspaceAccountError(ctx, account, err)
		return nil, err
	}
	project := &OpenAIPrismProject{Handle: "prism_proj_" + uuid.NewString(), Title: request.Title, Model: request.Model, APIKeyID: key.ID, UserID: key.UserID, GroupID: *key.GroupID, AccountID: account.ID, CredentialHash: prismProjectCredentialHash(credentials), State: state, Files: make(map[string]PrismProjectFile), CreatedAt: time.Now()}
	if err := s.savePrismProject(ctx, project); err != nil {
		return nil, err
	}
	runtime := s.prismWorkspaceRuntime()
	runtime.mu.Lock()
	runtime.entries[project.Handle].transport = transport
	runtime.mu.Unlock()
	return project, nil
}

type PrismProjectOperation struct {
	Name                  string
	MainDocument          string `json:"main_document"`
	ClientStateVector     string `json:"client_state_vector"`
	ClientDeleteSetUpdate string `json:"client_delete_set_update"`
	FileID                string `json:"file_id"`
	FileName              string `json:"-"`
	ContentType           string `json:"-"`
	Data                  []byte `json:"-"`
	IncludeBibliography   bool   `json:"include_bibliography"`
	Page                  int    `json:"page"`
	PageSize              int    `json:"page_size"`
	WaitMS                int    `json:"wait_ms"`
}

type PrismProjectResult struct {
	Body        []byte
	ContentType string
}

func (s *OpenAIGatewayService) prismWorkspaceAccountError(ctx context.Context, account *Account, err error) {
	var upstream *OpenAIPrismHTTPError
	if errors.As(err, &upstream) && s.rateLimitService != nil {
		s.rateLimitService.HandleUpstreamError(ctx, account, upstream.StatusCode, upstream.Headers, prismErrorBody(upstream))
	}
}

func (s *OpenAIGatewayService) OperatePrismProject(ctx context.Context, key *APIKey, handle string, request PrismProjectOperation) (*PrismProjectResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	result, err := s.operatePrismProject(ctx, key, handle, request)
	var started *OpenAIPrismStartedError
	if errors.As(err, &started) {
		return result, err
	}
	var upstream *OpenAIPrismHTTPError
	if !errors.As(err, &upstream) || !strings.HasPrefix(upstream.Path, "/s/sandboxes/proxy/") || (upstream.StatusCode != http.StatusUnauthorized && upstream.StatusCode != http.StatusForbidden && upstream.StatusCode != http.StatusGone) {
		return result, err
	}
	project, loadErr := s.LoadPrismProject(ctx, key, handle)
	if loadErr != nil {
		return nil, loadErr
	}
	unlock, lockErr := s.LockPrismProject(ctx, project)
	if lockErr != nil {
		return nil, lockErr
	}
	selection, selectErr := s.SelectBoundPrismProjectAccount(WithOpenAIProfitControlSuppressed(PrismProjectLeaseContext(ctx, project)), key.GroupID, project, "", nil, OpenAIEndpointCapabilityResponses, false)
	if selectErr != nil {
		unlock()
		return nil, selectErr
	}
	refreshErr := s.RefreshPrismProjectSandbox(ctx, project)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	unlock()
	if refreshErr != nil {
		return nil, refreshErr
	}
	return s.operatePrismProject(ctx, key, handle, request)
}

func (s *OpenAIGatewayService) operatePrismProject(ctx context.Context, key *APIKey, handle string, request PrismProjectOperation) (*PrismProjectResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if request.MainDocument == "" {
		request.MainDocument = "main.tex"
	}
	if len(request.MainDocument) > 255 || strings.ContainsAny(request.MainDocument, "\\\r\n\x00") || strings.HasPrefix(request.MainDocument, "/") {
		return nil, prismWorkspaceError(http.StatusBadRequest, "Invalid main document")
	}
	for _, part := range strings.Split(request.MainDocument, "/") {
		if part == ".." {
			return nil, prismWorkspaceError(http.StatusBadRequest, "Invalid main document")
		}
	}
	project, err := s.LoadPrismProject(ctx, key, handle)
	if err != nil {
		return nil, err
	}
	unlock, err := s.LockPrismProject(ctx, project)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ctx = PrismProjectLeaseContext(ctx, project)
	selection, err := s.SelectBoundPrismProjectAccount(WithOpenAIProfitControlSuppressed(ctx), key.GroupID, project, "", nil, OpenAIEndpointCapabilityResponses, false)
	if err != nil {
		return nil, err
	}
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	account := selection.Account
	credentials, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, err
	}
	transportAccount := *account
	transportAccount.Credentials = credentials.Credentials
	transport := s.PrismProjectTransport(project)
	token := prismAccessToken(credentials)
	state := project.State
	transport.RestoreCookies(&transportAccount, token, state.Cookies)
	var body []byte
	var headers http.Header
	switch request.Name {
	case "access":
		_, err = transport.ProjectAccess(ctx, &transportAccount, token, state.ProjectID)
		body, _ = json.Marshal(project.Public())
	case "history":
		body, err = transport.ConversationHistory(ctx, &transportAccount, token, state.ProjectID, state.ConversationID)
	case "upload":
		if len(request.Data) == 0 || len(request.Data) > 32<<20 || request.FileName == "" || request.FileName == "." || request.FileName == ".." || len(request.FileName) > 255 || strings.ContainsAny(request.FileName, "/\\\r\n\x00") {
			return nil, prismWorkspaceError(http.StatusBadRequest, "Invalid Prism file upload")
		}
		if len(project.Files) >= 1024 {
			return nil, prismWorkspaceError(http.StatusBadRequest, "Prism project file limit reached")
		}
		fileID := uuid.NewString()
		var uploaded []byte
		uploaded, err = transport.UploadProjectFile(ctx, &transportAccount, token, state.ProjectID, fileID, request.FileName, request.ContentType, request.Data)
		if err == nil {
			var result struct {
				FileUUID string `json:"fileUuid"`
			}
			if json.Unmarshal(uploaded, &result) != nil || strings.TrimSpace(result.FileUUID) == "" {
				return nil, prismWorkspaceError(http.StatusBadGateway, "Prism upload returned no file identifier")
			}
			projectPath, attachErr := transport.AttachProjectFile(ctx, &transportAccount, token, state.ProjectID, fileID, request.FileName, state.SandboxToken)
			if attachErr != nil {
				s.prismWorkspaceAccountError(ctx, account, attachErr)
				// The blob upload has already succeeded. Retrying the complete
				// operation could create a duplicate document node.
				return nil, &OpenAIPrismStartedError{Err: attachErr}
			}
			handle := "prism_file_" + uuid.NewString()
			if project.Files == nil {
				project.Files = make(map[string]PrismProjectFile)
			}
			project.Files[handle] = PrismProjectFile{NodeID: fileID, BlobUUID: result.FileUUID, FileName: request.FileName, Path: projectPath}
			body, _ = json.Marshal(map[string]any{"id": handle, "object": "prism.file", "filename": request.FileName, "bytes": len(request.Data), "project_id": project.Handle, "project_path": projectPath})
		}
	case "thumbnail":
		file, exists := project.Files[request.FileID]
		if !exists || file.BlobUUID == "" {
			return nil, prismWorkspaceError(http.StatusNotFound, "Prism file not found")
		}
		_, err = transport.SetProjectThumbnail(ctx, &transportAccount, token, state.ProjectID, file.BlobUUID)
		body = []byte(`{"updated":true}`)
	case "render":
		body, err = transport.RenderSandbox(ctx, &transportAccount, token, state.SandboxToken, request.MainDocument, request.ClientStateVector, request.ClientDeleteSetUpdate)
	case "render-status", "pdf":
		body, headers, err = transport.RenderStatus(ctx, &transportAccount, token, state.SandboxToken, "")
	case "logs":
		body, headers, err = transport.GetLogs(ctx, &transportAccount, token, state.SandboxToken)
	case "synctex":
		body, headers, err = transport.SyncTex(ctx, &transportAccount, token, state.SandboxToken)
	case "word-count":
		body, err = transport.WordCount(ctx, &transportAccount, token, state.SandboxToken, request.MainDocument, request.IncludeBibliography)
	case "latest-render":
		body, headers, err = transport.LatestRender(ctx, &transportAccount, token, state.ProjectID, request.MainDocument)
	case "version-history":
		body, err = transport.VersionHistory(ctx, &transportAccount, token, state.ProjectID, request.Page, request.PageSize)
	case "heartbeat":
		body, headers, err = transport.Heartbeat(ctx, &transportAccount, token, state.SandboxToken)
	case "wait-for-sync":
		body, err = transport.WaitForSync(ctx, &transportAccount, token, state.SandboxToken, request.WaitMS)
	case "delta-files":
		body = state.DeltaFiles
		if len(body) == 0 {
			body = []byte(`[]`)
		}
	case "sync-status":
		syncStatus := prismPublicDeltaSync(state)
		syncStatus.Files = append([]OpenAIPrismDeltaFileSync{}, syncStatus.Files...)
		for index := range syncStatus.Files {
			syncStatus.Files[index].Path = string(prismWorkspaceRedact([]byte(syncStatus.Files[index].Path), project, credentials))
			syncStatus.Files[index].Reason = string(prismWorkspaceRedact([]byte(syncStatus.Files[index].Reason), project, credentials))
		}
		body, _ = json.Marshal(syncStatus)
	default:
		return nil, prismWorkspaceError(http.StatusNotFound, "Unknown Prism project operation")
	}
	if err != nil {
		var upstream *OpenAIPrismHTTPError
		if !errors.As(err, &upstream) || !strings.HasPrefix(upstream.Path, "/s/sandboxes/proxy/") || (upstream.StatusCode != http.StatusUnauthorized && upstream.StatusCode != http.StatusForbidden && upstream.StatusCode != http.StatusGone) {
			s.prismWorkspaceAccountError(ctx, account, err)
		}
		return nil, err
	}
	project.State.Cookies = transport.currentCookies(&transportAccount, token)
	if err := s.savePrismProject(ctx, project); err != nil {
		return nil, err
	}
	contentType := headers.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/pdf") || strings.HasPrefix(string(body), "%PDF-") {
		return &PrismProjectResult{Body: body, ContentType: "application/pdf"}, nil
	}
	if request.Name == "synctex" && !json.Valid(body) {
		return &PrismProjectResult{Body: prismWorkspaceRedact(body, project, credentials), ContentType: "application/octet-stream"}, nil
	}
	if !json.Valid(body) {
		body, _ = json.Marshal(map[string]string{"text": string(prismWorkspaceRedact(body, project, credentials))})
	} else if request.Name != "sync-status" {
		// sync-status is an explicitly typed public result. Its file paths
		// are redacted above; the generic upstream filter drops all path keys.
		body = prismWorkspacePublicJSON(body, project, credentials)
	}
	return &PrismProjectResult{Body: body, ContentType: "application/json"}, nil
}

func prismWorkspaceRedact(body []byte, project *OpenAIPrismProject, credentials *Account) []byte {
	text := string(body)
	secrets := append(prismSensitiveValues(credentials, prismAccessToken(credentials)), project.State.SandboxToken, project.State.SandboxURL, project.State.ConversationID, project.State.ResponseID)
	for _, cookie := range project.State.Cookies {
		if cookie != nil {
			secrets = append(secrets, cookie.Value)
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	if project.State.ProjectID != "" {
		text = strings.ReplaceAll(text, project.State.ProjectID, project.Handle)
	}
	for handle, file := range project.Files {
		if file.NodeID != "" {
			text = strings.ReplaceAll(text, file.NodeID, handle)
		}
		if file.BlobUUID != "" {
			text = strings.ReplaceAll(text, file.BlobUUID, handle)
		}
	}
	return []byte(text)
}

func prismWorkspacePublicJSON(body []byte, project *OpenAIPrismProject, credentials *Account) []byte {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return []byte(`{}`)
	}
	var clean func(any) any
	clean = func(v any) any {
		switch item := v.(type) {
		case map[string]any:
			out := make(map[string]any)
			for key, child := range item {
				normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
				if strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "cookie") || strings.Contains(normalized, "authorization") || strings.Contains(normalized, "sandbox") || strings.Contains(normalized, "url") || normalized == "href" || normalized == "location" || normalized == "userid" || normalized == "accountid" || normalized == "conversationid" || normalized == "responseid" || normalized == "codexlistensnapshot" {
					continue
				}
				if strings.HasSuffix(normalized, "path") && normalized != "filepath" && normalized != "projectpath" {
					continue
				}
				if strings.HasSuffix(normalized, "id") {
					id, ok := child.(string)
					id = string(prismWorkspaceRedact([]byte(id), project, credentials))
					if !ok || (!strings.HasPrefix(id, "prism_proj_") && !strings.HasPrefix(id, "prism_file_")) {
						continue
					}
				}
				if _, err := uuid.Parse(key); err == nil {
					continue
				}
				out[key] = clean(child)
			}
			return out
		case []any:
			for i := range item {
				item[i] = clean(item[i])
			}
			return item
		case string:
			if strings.HasPrefix(item, "http://") || strings.HasPrefix(item, "https://") {
				return "[redacted]"
			}
			return string(prismWorkspaceRedact([]byte(item), project, credentials))
		default:
			return v
		}
	}
	public, err := json.Marshal(clean(value))
	if err != nil {
		return []byte(`{"error":"invalid upstream data"}`)
	}
	return public
}
