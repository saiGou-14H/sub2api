package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type prismWorkspaceTestStore struct {
	stubGatewayCache
	mu     sync.Mutex
	values map[string][]byte
	owners map[string]string
	fail   bool
}

func (s *prismWorkspaceTestStore) GetPrismProject(_ context.Context, handle string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return nil, errors.New("cache unavailable")
	}
	return append([]byte(nil), s.values[handle]...), nil
}
func (s *prismWorkspaceTestStore) SetPrismProject(_ context.Context, handle string, payload []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("cache unavailable")
	}
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	if _, exists := s.values[handle]; exists {
		return errors.New("project already exists")
	}
	s.values[handle] = append([]byte(nil), payload...)
	return nil
}
func (s *prismWorkspaceTestStore) SavePrismProjectWithLease(_ context.Context, handle, owner string, payload []byte, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return false, errors.New("cache unavailable")
	}
	if s.owners[handle] != owner || s.values[handle] == nil {
		return false, nil
	}
	s.values[handle] = append([]byte(nil), payload...)
	return true, nil
}
func (s *prismWorkspaceTestStore) TryAcquirePrismProjectLock(_ context.Context, handle, owner string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return false, errors.New("cache unavailable")
	}
	if s.owners == nil {
		s.owners = make(map[string]string)
	}
	if s.owners[handle] != "" {
		return false, nil
	}
	s.owners[handle] = owner
	return true, nil
}
func (s *prismWorkspaceTestStore) RefreshPrismProjectLock(_ context.Context, handle, owner string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return false, errors.New("cache unavailable")
	}
	return s.owners[handle] == owner, nil
}
func (s *prismWorkspaceTestStore) ReleasePrismProjectLock(_ context.Context, handle, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[handle] == owner {
		delete(s.owners, handle)
	}
	return nil
}

func prismWorkspaceFixture(t *testing.T, shared *prismWorkspaceTestStore) (*OpenAIGatewayService, *APIKey, *OpenAIPrismProject, *stubOpenAIAccountRepo) {
	t.Helper()
	groupID := int64(31)
	account := *prismGatewayTestAccount()
	account.Status, account.Schedulable, account.GroupIDs = StatusActive, true, []int64{groupID}
	repo := &stubOpenAIAccountRepo{accounts: []Account{account}}
	s := &OpenAIGatewayService{accountRepo: repo}
	if shared != nil {
		s.cache = shared
	}
	key := &APIKey{ID: 21, UserID: 11, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI}}
	project := &OpenAIPrismProject{Handle: "prism_proj_" + uuid.NewString(), Title: "Example", Model: OpenAIPrismDefaultModel, APIKeyID: key.ID, UserID: key.UserID, GroupID: groupID, AccountID: account.ID, CredentialHash: prismProjectCredentialHash(&account), State: OpenAIPrismSessionState{ProjectID: "private-project", ConversationID: "private-conversation", SandboxURL: "https://prism.openai.com/s/sandboxes/proxy", SandboxToken: "private-sandbox", Cookies: []*http.Cookie{{Name: "prism_session_token", Value: "rotated-cookie"}}}, CreatedAt: time.Now()}
	require.NoError(t, s.savePrismProject(context.Background(), project))
	return s, key, project, repo
}

func TestPrismWorkspaceOwnerGroupAndCredentialIsolation(t *testing.T) {
	s, key, project, repo := prismWorkspaceFixture(t, nil)
	ctx := context.Background()
	loaded, err := s.LoadPrismProject(ctx, key, project.Handle)
	require.NoError(t, err)
	require.Equal(t, project.State.ProjectID, loaded.State.ProjectID)
	other := *key
	other.ID++
	_, err = s.LoadPrismProject(ctx, &other, project.Handle)
	require.Error(t, err)
	other = *key
	other.UserID++
	_, err = s.LoadPrismProject(ctx, &other, project.Handle)
	require.Error(t, err)
	other = *key
	other.Group = &Group{ID: *key.GroupID, Platform: PlatformAnthropic}
	_, err = s.LoadPrismProject(ctx, &other, project.Handle)
	require.Error(t, err)
	repo.accounts[0].GroupIDs = []int64{999}
	_, err = s.LoadPrismProject(ctx, key, project.Handle)
	require.Error(t, err)
	repo.accounts[0].GroupIDs = []int64{project.GroupID}
	repo.accounts[0].Credentials["access_token"] = "rotated-credential"
	_, err = s.LoadPrismProject(ctx, key, project.Handle)
	require.Error(t, err)
	var publicErr *PrismWorkspaceError
	require.ErrorAs(t, err, &publicErr)
	require.Equal(t, http.StatusConflict, publicErr.Status)
}

func TestPrismWorkspaceSyncStatusPreservesPathsAndRedactsPrivateValues(t *testing.T) {
	s, key, project, _ := prismWorkspaceFixture(t, nil)
	ctx := context.Background()
	unlock, err := s.LockPrismProject(ctx, project)
	require.NoError(t, err)
	state := project.State
	state.DeltaSync = OpenAIPrismDeltaSync{Status: "partial", Files: []OpenAIPrismDeltaFileSync{
		{Path: "chapters/main.tex", Status: "synced"},
		{Path: "rotated-cookie.png", Status: "unsynced", Reason: "unsupported_binary"},
	}}
	require.NoError(t, s.SavePrismProjectState(ctx, project, state))
	unlock()
	upstreamCalls := 0
	s.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
		return NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(*http.Request) (*http.Response, error) {
			upstreamCalls++
			return nil, errors.New("sync-status must use the saved result")
		}), OpenAIPrismTransportOptions{})
	}
	result, err := s.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "sync-status"})
	require.NoError(t, err)
	require.Equal(t, "application/json", result.ContentType)
	require.Zero(t, upstreamCalls)
	var syncStatus OpenAIPrismDeltaSync
	require.NoError(t, json.Unmarshal(result.Body, &syncStatus))
	require.Equal(t, "partial", syncStatus.Status)
	require.Equal(t, []OpenAIPrismDeltaFileSync{{Path: "chapters/main.tex", Status: "synced"}, {Path: "[redacted].png", Status: "unsynced", Reason: "unsupported_binary"}}, syncStatus.Files)
	require.NotContains(t, string(result.Body), "private-project")
	require.NotContains(t, string(result.Body), "rotated-cookie")
	other := *key
	other.ID++
	_, err = s.OperatePrismProject(ctx, &other, project.Handle, PrismProjectOperation{Name: "sync-status"})
	require.Error(t, err)
}

func TestPrismWorkspaceCrossInstanceLeaseRefreshesStateAndFailsClosed(t *testing.T) {
	store := &prismWorkspaceTestStore{}
	first, key, project, repo := prismWorkspaceFixture(t, store)
	second := &OpenAIGatewayService{cache: store, accountRepo: repo}
	ctx := context.Background()
	stale, err := second.LoadPrismProject(ctx, key, project.Handle)
	require.NoError(t, err)
	release, err := first.LockPrismProject(ctx, project)
	require.NoError(t, err)
	_, err = second.LockPrismProject(ctx, stale)
	require.Error(t, err)
	state := project.State
	state.ResponseID = "updated-private-response"
	require.NoError(t, first.SavePrismProjectState(ctx, project, state))
	release()
	release, err = second.LockPrismProject(ctx, stale)
	require.NoError(t, err)
	require.Equal(t, "updated-private-response", stale.State.ResponseID)
	release()
	store.fail = true
	_, err = first.LoadPrismProject(ctx, key, project.Handle)
	require.Error(t, err, "Redis failure must not expose a local cached project")
}

func TestPrismWorkspaceBoundAccountAdmissionAndPrivateSerialization(t *testing.T) {
	s, key, project, repo := prismWorkspaceFixture(t, nil)
	ctx := context.Background()
	selection, err := s.SelectBoundPrismProjectAccount(ctx, key.GroupID, project, "", nil, OpenAIEndpointCapabilityResponses, false)
	require.NoError(t, err, "file operations have no model and must still pass normal account admission")
	require.Equal(t, project.AccountID, selection.Account.ID)
	selection.ReleaseFunc()
	repo.accounts[0].Schedulable = false
	_, err = s.SelectBoundPrismProjectAccount(ctx, key.GroupID, project, "", nil, OpenAIEndpointCapabilityResponses, false)
	require.Error(t, err)
	serialized, err := json.Marshal(project)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(serialized))
	serialized, err = json.Marshal(project.Public())
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "private-")
}

func TestPrismWorkspacePublicPayloadRedactsNestedSecretsPreservesContent(t *testing.T) {
	_, _, project, repo := prismWorkspaceFixture(t, nil)
	project.Files = map[string]PrismProjectFile{"prism_file_public": {NodeID: "private-node", BlobUUID: "private-file"}}
	body := []byte(`{"status":"done","projectId":"private-project","fileUuid":"private-file","items":[{"uuid":"another-provider-id","content":"full text private-project private-sandbox access-secret rotated-cookie","metadata":{"authorization":"secret","private_secret":"never expose","url":"https://private.invalid/file"}}],"binary_bytes":9007199254740993}`)
	result := prismWorkspacePublicJSON(body, project, &repo.accounts[0])
	for _, forbidden := range []string{"private-project", "private-file", "private-sandbox", "access-secret", "rotated-cookie", "another-provider-id", "never expose", "private.invalid"} {
		require.NotContains(t, string(result), forbidden)
	}
	require.Contains(t, string(result), project.Handle)
	require.Contains(t, string(result), "prism_file_public")
	require.Contains(t, string(result), "full text")
	require.Contains(t, string(result), "9007199254740993")
}

func TestPrismWorkspaceUploadUsesReturnedFileUUIDAndPDFStaysBinary(t *testing.T) {
	s, key, project, _ := prismWorkspaceFixture(t, nil)
	var thumbnailBody string
	var requestedNodeID string
	s.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
		transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/project-files/upload":
				require.Equal(t, "private-project", req.Header.Get("x-prism-project-id"))
				requestedNodeID = req.Header.Get("x-prism-file-id")
				return prismTestJSON(`{"fileUuid":"provider-returned-file","sedimentFileId":"private-storage"}`), nil
			case "/api/projects/private-project/thumbnail":
				data, _ := io.ReadAll(req.Body)
				thumbnailBody = string(data)
				return prismTestJSON(`{"updated":true}`), nil
			case "/s/sandboxes/proxy/render-status":
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/pdf"}}, Body: io.NopCloser(strings.NewReader("%PDF-1.7\x00binary"))}, nil
			default:
				return nil, errors.New("unexpected workspace request")
			}
		}), OpenAIPrismTransportOptions{})
		transport.attachFile = func(_ context.Context, _ *Account, _, projectID, nodeID, fileName string) (string, error) {
			require.Equal(t, "private-project", projectID)
			require.Equal(t, requestedNodeID, nodeID)
			require.NotEqual(t, "provider-returned-file", nodeID)
			return fileName, nil
		}
		return transport
	}
	upload, err := s.OperatePrismProject(context.Background(), key, project.Handle, PrismProjectOperation{Name: "upload", FileName: "document.tex", ContentType: "text/plain", Data: []byte("document")})
	require.NoError(t, err)
	var file struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(upload.Body, &file))
	require.True(t, strings.HasPrefix(file.ID, "prism_file_"))
	require.NotContains(t, string(upload.Body), "provider-returned-file")
	require.Contains(t, string(upload.Body), `"project_path":"document.tex"`)
	_, err = s.OperatePrismProject(context.Background(), key, project.Handle, PrismProjectOperation{Name: "thumbnail", FileID: file.ID})
	require.NoError(t, err)
	require.JSONEq(t, `{"thumbnail_uuid":"provider-returned-file"}`, thumbnailBody)
	pdf, err := s.OperatePrismProject(context.Background(), key, project.Handle, PrismProjectOperation{Name: "pdf"})
	require.NoError(t, err)
	require.Equal(t, "application/pdf", pdf.ContentType)
	require.Equal(t, []byte("%PDF-1.7\x00binary"), pdf.Body)
}

func TestPrismWorkspaceUploadMountFailureDoesNotPublishFile(t *testing.T) {
	s, key, project, _ := prismWorkspaceFixture(t, nil)
	s.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
		transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "/api/project-files/upload", req.URL.Path)
			return prismTestJSON(`{"fileUuid":"blob-uploaded"}`), nil
		}), OpenAIPrismTransportOptions{})
		transport.attachFile = func(context.Context, *Account, string, string, string, string) (string, error) {
			return "", errors.New("document sync failed")
		}
		return transport
	}
	result, err := s.OperatePrismProject(context.Background(), key, project.Handle, PrismProjectOperation{Name: "upload", FileName: "document.tex", ContentType: "text/plain", Data: []byte("document")})
	require.ErrorContains(t, err, "document sync failed")
	require.Nil(t, result)
	loaded, err := s.LoadPrismProject(context.Background(), key, project.Handle)
	require.NoError(t, err)
	require.Empty(t, loaded.Files)
}

func TestPrismWorkspaceRejectsInvalidFilenameBeforeUploading(t *testing.T) {
	for _, name := range []string{"", ".", "..", "nested/file.tex", `nested\file.tex`, `C:\file.tex`, "file\n.tex", "file\x00.tex", strings.Repeat("a", 256)} {
		t.Run(name, func(t *testing.T) {
			s, key, project, _ := prismWorkspaceFixture(t, nil)
			upstreamCalls := 0
			s.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
				return NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(*http.Request) (*http.Response, error) {
					upstreamCalls++
					return prismTestJSON(`{"fileUuid":"must-not-upload"}`), nil
				}), OpenAIPrismTransportOptions{})
			}
			result, err := s.OperatePrismProject(context.Background(), key, project.Handle, PrismProjectOperation{Name: "upload", FileName: name, ContentType: "text/plain", Data: []byte("document")})
			var invalid *PrismWorkspaceError
			require.ErrorAs(t, err, &invalid)
			require.Equal(t, http.StatusBadRequest, invalid.Status)
			require.Nil(t, result)
			require.Zero(t, upstreamCalls, "invalid filename must not leave an uploaded blob")
		})
	}
}

func TestPrismWorkspaceUploadDoesNotReplayAfterMountAuthorizationFailure(t *testing.T) {
	s, key, project, _ := prismWorkspaceFixture(t, nil)
	uploads := 0
	s.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
		transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "/api/project-files/upload", req.URL.Path)
			uploads++
			return prismTestJSON(`{"fileUuid":"blob-uploaded"}`), nil
		}), OpenAIPrismTransportOptions{})
		transport.attachFile = func(context.Context, *Account, string, string, string, string) (string, error) {
			return "", &OpenAIPrismHTTPError{StatusCode: http.StatusForbidden, Path: "/s/sandboxes/proxy/wait-for-sync", Message: "authorization expired"}
		}
		return transport
	}
	result, err := s.OperatePrismProject(context.Background(), key, project.Handle, PrismProjectOperation{Name: "upload", FileName: "document.tex", ContentType: "text/plain", Data: []byte("document")})
	var started *OpenAIPrismStartedError
	require.ErrorAs(t, err, &started)
	require.Nil(t, result)
	require.Equal(t, 1, uploads)
}

func TestPrismWorkspaceCreateSelectsOnlyPrismAndEnforcesModelAllowlist(t *testing.T) {
	s, key, _, repo := prismWorkspaceFixture(t, nil)
	codex := repo.accounts[0]
	codex.ID = 1
	codex.Priority = -100
	codex.Extra = nil
	repo.accounts = append(repo.accounts, codex)
	upstream := &prismTestUpstream{}
	s.openAIPrismTransportFactory = func() *OpenAIPrismTransport {
		return NewOpenAIPrismTransportFromUpstream(upstream, OpenAIPrismTransportOptions{})
	}
	project, err := s.CreatePrismProject(context.Background(), key, PrismProjectCreateRequest{Title: "Public title"})
	require.NoError(t, err)
	require.Equal(t, repo.accounts[0].ID, project.AccountID)
	require.True(t, strings.HasPrefix(project.Handle, "prism_proj_"))
	key.Group.ModelAllowlist = GroupModelAllowlist{Enabled: true, Models: []string{"other-model"}}
	before := len(upstream.requests)
	_, err = s.CreatePrismProject(context.Background(), key, PrismProjectCreateRequest{})
	require.Error(t, err)
	require.Len(t, upstream.requests, before)
}

func TestPrismWorkspaceLostLeaseCannotOverwriteSuccessorState(t *testing.T) {
	store := &prismWorkspaceTestStore{}
	first, key, project, repo := prismWorkspaceFixture(t, store)
	second := &OpenAIGatewayService{cache: store, accountRepo: repo}
	ctx := context.Background()
	oldRelease, err := first.LockPrismProject(ctx, project)
	require.NoError(t, err)
	defer oldRelease()
	// Simulate lease loss without assuming timeouts or sleeping in the test.
	store.mu.Lock()
	delete(store.owners, project.Handle)
	store.mu.Unlock()
	current, err := second.LoadPrismProject(ctx, key, project.Handle)
	require.NoError(t, err)
	newRelease, err := second.LockPrismProject(ctx, current)
	require.NoError(t, err)
	defer newRelease()
	newState := current.State
	newState.ResponseID = "successor-response"
	require.NoError(t, second.SavePrismProjectState(ctx, current, newState))
	oldState := project.State
	oldState.ResponseID = "stale-response"
	require.Error(t, first.SavePrismProjectState(ctx, project, oldState))
	require.Error(t, PrismProjectLeaseContext(ctx, project).Err())
	loaded, err := second.LoadPrismProject(ctx, key, project.Handle)
	require.NoError(t, err)
	require.Equal(t, "successor-response", loaded.State.ResponseID)
	oldRelease()
	require.True(t, refreshPrismProjectLease(ctx, store, current.Handle, current.lease), "old release cannot delete successor lease")
}

func TestPrismWorkspaceLeaseRenewFailureCancelsAndRejectsStateWrite(t *testing.T) {
	store := &prismWorkspaceTestStore{}
	s, _, project, _ := prismWorkspaceFixture(t, store)
	ctx := context.Background()
	release, err := s.LockPrismProject(ctx, project)
	require.NoError(t, err)
	defer release()
	store.mu.Lock()
	store.fail = true
	store.mu.Unlock()
	require.False(t, refreshPrismProjectLease(ctx, store, project.Handle, project.lease))
	require.Error(t, PrismProjectLeaseContext(ctx, project).Err())
	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	require.Error(t, s.SavePrismProjectState(ctx, project, project.State), "restored Redis does not revive a lost lease")
}

func TestPrismWorkspaceLockRechecksAuthorizationAfterWait(t *testing.T) {
	s, _, project, repo := prismWorkspaceFixture(t, nil)
	repo.accounts[0].GroupIDs = []int64{999}
	_, err := s.LockPrismProject(context.Background(), project)
	require.Error(t, err, "a preloaded project cannot outlive account removal from its group")
}
