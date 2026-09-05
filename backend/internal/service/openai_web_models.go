package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIWebModelCatalogExtraKey stores the last successful per-account Web
// entitlement response. It contains only model ids and timestamps, never auth
// material or the raw upstream response.
const OpenAIWebModelCatalogExtraKey = "openai_web_model_catalog"

const openAIWebModelCatalogTTL = 5 * time.Minute

type OpenAIWebModelCatalogSnapshot struct {
	Models           []string `json:"models"`
	WorkModeModels   []string `json:"work_mode_models,omitempty"`
	DefaultModelSlug string   `json:"default_model_slug,omitempty"`
	SyncedAt         string   `json:"synced_at"`
}

func normalizeOpenAIWebModelIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values)+1)
	models := make([]string, 0, len(values)+1)
	for _, value := range values {
		model, ok := NormalizeOpenAIWebModel(value)
		if !ok {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	// The browser's Default selector is a gateway capability even when the
	// authenticated manifest lists only concrete slugs.
	if _, exists := seen[OpenAIWebTestModel]; !exists {
		models = append([]string{OpenAIWebTestModel}, models...)
	}
	return models
}

func normalizeOpenAIWebWorkModeModelIDs(values []string) []string {
	models := normalizeOpenAIWebModelIDs(values)
	result := make([]string, 0, len(models))
	for _, model := range models {
		if model != OpenAIWebTestModel {
			result = append(result, model)
		}
	}
	return result
}

func (a *Account) SetOpenAIWebModelCatalogSnapshot(snapshot OpenAIWebModelCatalogSnapshot) {
	if a == nil {
		return
	}
	snapshot.Models = normalizeOpenAIWebModelIDs(snapshot.Models)
	snapshot.WorkModeModels = normalizeOpenAIWebWorkModeModelIDs(snapshot.WorkModeModels)
	if a.Extra == nil {
		a.Extra = make(map[string]any)
	}
	a.Extra[OpenAIWebModelCatalogExtraKey] = snapshot
}

// GetOpenAIWebModelCatalog returns the last successful model snapshot. The
// boolean distinguishes a real snapshot from the conservative auto fallback.
func (a *Account) GetOpenAIWebModelCatalog() ([]string, bool) {
	if a == nil || a.Extra == nil {
		return []string{OpenAIWebTestModel}, false
	}
	raw, exists := a.Extra[OpenAIWebModelCatalogExtraKey]
	if !exists || raw == nil {
		return []string{OpenAIWebTestModel}, false
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return []string{OpenAIWebTestModel}, false
	}
	var snapshot OpenAIWebModelCatalogSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil || len(snapshot.Models) == 0 {
		return []string{OpenAIWebTestModel}, false
	}
	models := normalizeOpenAIWebModelIDs(snapshot.Models)
	if len(models) == 0 {
		return []string{OpenAIWebTestModel}, false
	}
	return models, true
}

func (a *Account) OpenAIWebModelCatalogSnapshot() *OpenAIWebModelCatalogSnapshot {
	if a == nil || a.Extra == nil {
		return nil
	}
	raw, exists := a.Extra[OpenAIWebModelCatalogExtraKey]
	if !exists || raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot OpenAIWebModelCatalogSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil || len(snapshot.Models) == 0 {
		return nil
	}
	snapshot.Models = normalizeOpenAIWebModelIDs(snapshot.Models)
	snapshot.WorkModeModels = normalizeOpenAIWebWorkModeModelIDs(snapshot.WorkModeModels)
	return &snapshot
}

func (a *Account) OpenAIWebModelCatalogFresh(now time.Time) bool {
	snapshot := a.OpenAIWebModelCatalogSnapshot()
	if snapshot == nil || strings.TrimSpace(snapshot.SyncedAt) == "" {
		return false
	}
	syncedAt, err := time.Parse(time.RFC3339, snapshot.SyncedAt)
	return err == nil && now.Sub(syncedAt) < openAIWebModelCatalogTTL
}

// IsOpenAIWebWorkModeModel reports whether the selected Web model needs the
// Plus work-mode request contract. Catalog metadata is authoritative when it
// identifies the model; the -wm suffix keeps older persisted snapshots and
// newly introduced work-mode slugs compatible until the next discovery.
func (a *Account) IsOpenAIWebWorkModeModel(requestedModel string) bool {
	model, ok := NormalizeOpenAIWebModel(requestedModel)
	if !ok {
		return false
	}
	snapshot := a.OpenAIWebModelCatalogSnapshot()
	if model == OpenAIWebTestModel && snapshot != nil {
		if defaultModel, valid := NormalizeOpenAIWebModel(snapshot.DefaultModelSlug); valid && defaultModel != OpenAIWebTestModel {
			model = defaultModel
		}
	}
	if snapshot != nil {
		for _, workModeModel := range snapshot.WorkModeModels {
			if model == workModeModel {
				return true
			}
		}
	}
	return strings.HasSuffix(model, "-wm")
}

// DiscoverOpenAIWebModelCatalog performs the authenticated browser manifest
// request. It intentionally reuses the same browser session and headers as a
// normal conversation, but does not run a sentinel challenge.
func (t *OpenAIWebTransport) DiscoverOpenAIWebModelCatalog(ctx context.Context, account *Account, token string) (*OpenAIWebModelCatalogSnapshot, error) {
	if t == nil {
		return nil, errors.New("ChatGPT web transport is nil")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("access token is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !t.options.SkipBootstrap {
		if _, err := t.Bootstrap(ctx, account, token); err != nil {
			return nil, err
		}
	}
	path := OpenAIWebModelsPath
	headers, err := t.commonHeaders(ctx, account, token, OpenAIWebModelsRoute)
	if err != nil {
		return nil, err
	}
	headers.Set("Accept", "application/json")
	resp, err := t.request(ctx, http.MethodGet, path, token, account, nil, headers)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("ChatGPT web model list returned no response")
	}
	raw, readErr := readAndCloseWebBody(resp, 8<<20)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, webHTTPError(path, resp.StatusCode, raw, token)
	}
	if readErr != nil {
		return nil, fmt.Errorf("ChatGPT web model list response: %w", readErr)
	}
	var payload struct {
		Models []struct {
			Slug            string `json:"slug"`
			IsWorkModeModel bool   `json:"is_work_mode_model"`
		} `json:"models"`
		DefaultModelSlug string `json:"default_model_slug"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, errors.New("ChatGPT web model list response is invalid")
	}
	models := make([]string, 0, len(payload.Models))
	workModeModels := make([]string, 0)
	for _, item := range payload.Models {
		if model := strings.TrimSpace(item.Slug); model != "" {
			models = append(models, model)
			if item.IsWorkModeModel {
				workModeModels = append(workModeModels, model)
			}
		}
	}
	models = normalizeOpenAIWebModelIDs(models)
	workModeModels = normalizeOpenAIWebWorkModeModelIDs(workModeModels)
	if len(models) == 0 {
		return nil, errors.New("ChatGPT web model list returned no supported models")
	}
	defaultModel, _ := NormalizeOpenAIWebModel(payload.DefaultModelSlug)
	if defaultModel == "" {
		defaultModel = OpenAIWebTestModel
	}
	return &OpenAIWebModelCatalogSnapshot{
		Models:           models,
		WorkModeModels:   workModeModels,
		DefaultModelSlug: defaultModel,
		SyncedAt:         time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// SyncOpenAIWebModelCatalog discovers and persists one account's current Web
// capability list. A caller can keep using the supplied account immediately;
// repository persistence is best-effort only after a successful discovery.
func (s *AccountTestService) SyncOpenAIWebModelCatalog(ctx context.Context, account *Account) (*OpenAIWebModelCatalogSnapshot, error) {
	if s == nil || account == nil {
		return nil, errors.New("account test service and account are required")
	}
	credentialAccount := account
	if account.IsCredentialShadow() && s.accountRepo != nil {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		credentialAccount = resolved
	}
	token := strings.TrimSpace(credentialAccount.GetOpenAIAccessToken())
	if token == "" {
		return nil, errors.New("no OpenAI access token is available")
	}
	transport := s.newOpenAIWebTransport()
	snapshot, err := transport.DiscoverOpenAIWebModelCatalog(ctx, credentialAccount, token)
	if err != nil {
		return nil, err
	}
	account.SetOpenAIWebModelCatalogSnapshot(*snapshot)
	if s.accountRepo != nil && account.ID > 0 {
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{OpenAIWebModelCatalogExtraKey: *snapshot}); err != nil {
			return nil, fmt.Errorf("save ChatGPT web model catalog: %w", err)
		}
	}
	return snapshot, nil
}

func refreshOpenAIWebModelCatalog(ctx context.Context, repo AccountRepository, upstream HTTPUpstream, account *Account) {
	if account == nil || !account.IsOpenAIWebTransport() || upstream == nil || account.OpenAIWebModelCatalogFresh(time.Now()) {
		return
	}
	token := strings.TrimSpace(account.GetOpenAIAccessToken())
	if token == "" {
		return
	}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{})
	snapshot, err := transport.DiscoverOpenAIWebModelCatalog(ctx, account, token)
	if err != nil || snapshot == nil {
		return
	}
	account.SetOpenAIWebModelCatalogSnapshot(*snapshot)
	if repo != nil && account.ID > 0 {
		_ = repo.UpdateExtra(ctx, account.ID, map[string]any{OpenAIWebModelCatalogExtraKey: *snapshot})
	}
}

func openAIWebModelSet(account Account) []string {
	models, _ := account.GetOpenAIWebModelCatalog()
	return models
}
