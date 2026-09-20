package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// OpenAIPrismProgress describes work executed by Prism's own sandbox. These
// observations are not client function calls and require no client tool result.
type OpenAIPrismProgress struct {
	TranscriptCursor   int                     `json:"transcript_cursor"`
	LineCount          int                     `json:"line_count"`
	ToolCalls          []OpenAIPrismNativeTool `json:"tool_calls"`
	ReasoningSummaries []string                `json:"reasoning_summaries"`
}

type OpenAIPrismNativeTool struct {
	Name      string  `json:"name"`
	CallID    string  `json:"call_id"`
	LineIndex int     `json:"line_index"`
	Status    *string `json:"status"`
}

type prismLiveProgress struct {
	TranscriptCursor   int                     `json:"transcriptCursor"`
	LineCount          int                     `json:"lineCount"`
	ToolCalls          []OpenAIPrismNativeTool `json:"toolCalls"`
	ReasoningSummaries []json.RawMessage       `json:"reasoningSummaries"`
}

// File changes are available only through the authorized workspace API. Keep
// captured file fields while dropping future internal/debug metadata. Unlike
// error-message sanitization, this preserves complete file diffs and binaries.
func prismNormalizeDeltaFiles(raw json.RawMessage, secrets []string) json.RawMessage {
	var files []map[string]json.RawMessage
	result := make([]map[string]json.RawMessage, 0)
	if json.Unmarshal(raw, &files) != nil {
		return json.RawMessage(`[]`)
	}
	for _, file := range files {
		filtered := make(map[string]json.RawMessage)
		for key, value := range file {
			switch key {
			case "file_path", "status", "diff", "diff_error", "binary_body_b64", "binary_fetch_error", "binary_mime_type":
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					filtered[key] = json.RawMessage(`null`)
					continue
				}
				var text string
				if json.Unmarshal(value, &text) == nil {
					filtered[key] = prismMustJSON(prismReplaceSecrets(text, secrets))
				}
			case "diff_truncated", "base_render_hash_mismatch":
				var flag bool
				if json.Unmarshal(value, &flag) == nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					filtered[key] = prismMustJSON(flag)
				}
			case "binary_bytes", "binary_fetch_status":
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					filtered[key] = json.RawMessage(`null`)
					continue
				}
				var number int64
				if json.Unmarshal(value, &number) == nil {
					filtered[key] = prismMustJSON(number)
				}
			}
		}
		if len(filtered) > 0 {
			result = append(result, filtered)
		}
	}
	return prismMustJSON(result)
}

func (p *prismLiveProgress) public(secrets []string) OpenAIPrismProgress {
	result := OpenAIPrismProgress{TranscriptCursor: p.TranscriptCursor, LineCount: p.LineCount, ToolCalls: make([]OpenAIPrismNativeTool, 0, len(p.ToolCalls)), ReasoningSummaries: []string{}}
	for _, tool := range p.ToolCalls {
		tool.Name, tool.CallID = prismRedact(tool.Name, secrets), prismRedact(tool.CallID, secrets)
		if tool.Status != nil {
			status := prismRedact(*tool.Status, secrets)
			tool.Status = &status
		}
		result.ToolCalls = append(result.ToolCalls, tool)
	}
	// The capture contains an empty list. Accept string summaries without
	// exposing unknown nested fields or raw event previews from future versions.
	for _, raw := range p.ReasoningSummaries {
		var summary string
		if json.Unmarshal(raw, &summary) == nil {
			result.ReasoningSummaries = append(result.ReasoningSummaries, prismRedact(summary, secrets))
		}
	}
	return result
}

func (t *OpenAIPrismTransport) ProjectCreate(ctx context.Context, account *Account, token, title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "sub2api"
	}
	data, _, err := t.doJSON(ctx, account, token, http.MethodPost, OpenAIPrismProjectPath, map[string]any{"project_uuid": uuid.NewString(), "title": title, "file_uuids": []string{}})
	if err != nil {
		return "", err
	}
	var project prismProject
	if json.Unmarshal(data, &project) != nil || strings.TrimSpace(project.UUID) == "" {
		return "", errors.New("prism project creation returned no uuid")
	}
	return project.UUID, nil
}

// PrepareProject creates or checks a project, provisions its sandbox and syncs
// project files. Bootstrap authorizations are sent only to the sandbox proxy.
func (t *OpenAIPrismTransport) PrepareProject(ctx context.Context, account *Account, token, projectID, title string) (OpenAIPrismSessionState, error) {
	if _, err := t.authUserID(ctx, account, token); err != nil {
		return OpenAIPrismSessionState{}, err
	}
	var err error
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID, err = t.ProjectCreate(ctx, account, token, title)
		if err != nil {
			return OpenAIPrismSessionState{}, err
		}
	}
	if _, err := t.ProjectAccess(ctx, account, token, projectID); err != nil {
		return OpenAIPrismSessionState{}, err
	}
	sandboxURL, sandboxToken, err := t.bootstrap(ctx, account, token)
	if err != nil {
		return OpenAIPrismSessionState{}, err
	}
	if err := t.prepareSandbox(ctx, account, token, projectID, sandboxToken); err != nil {
		return OpenAIPrismSessionState{}, err
	}
	state := OpenAIPrismSessionState{ProjectID: projectID, ConversationID: prismConversationID(""), SandboxURL: sandboxURL, SandboxToken: sandboxToken, Cookies: t.currentCookies(account, token)}
	t.stateMu.Lock()
	t.state = state
	t.stateMu.Unlock()
	return t.OpenAIPrismSessionState(), nil
}

func (t *OpenAIPrismTransport) sandboxJSON(ctx context.Context, account *Account, token, sandboxToken, method, path string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	data, _, _, err := t.doRaw(ctx, account, token, method, path, bytes.NewReader(encoded), "application/json", sandboxToken, nil, prismJSONSecrets(encoded)...)
	return data, err
}

func (t *OpenAIPrismTransport) prepareSandbox(ctx context.Context, account *Account, token, projectID, sandboxToken string) error {
	data, _, err := t.doJSON(ctx, account, token, http.MethodPost, "/api/projects/"+url.PathEscape(projectID)+"/sandbox/resources-token", map[string]any{"sandbox_session_id": nil, "sandbox_token": sandboxToken})
	if err != nil {
		return err
	}
	var resource struct {
		AccessToken string `json:"access_token"`
		BaseURL     string `json:"resources_base_url"`
	}
	if json.Unmarshal(data, &resource) != nil || resource.AccessToken == "" || resource.BaseURL == "" {
		return errors.New("prism resource authorization is incomplete")
	}
	if err := t.validateAuthorizationURL(resource.BaseURL, false); err != nil {
		return err
	}
	data, err = t.sandboxJSON(ctx, account, token, sandboxToken, http.MethodPost, "/s/sandboxes/proxy/resources-token", map[string]string{"token": resource.AccessToken, "resourceBaseUrl": resource.BaseURL, "projectId": projectID})
	if err != nil {
		return err
	}
	var accepted struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &accepted) != nil || accepted.Status != "success" {
		return errors.New("prism sandbox rejected resource authorization")
	}
	data, _, err = t.doJSON(ctx, account, token, http.MethodPost, "/api/y", map[string]string{"docId": projectID})
	if err != nil {
		return err
	}
	var document struct {
		URL           string `json:"url"`
		BaseURL       string `json:"baseUrl"`
		DocID         string `json:"docId"`
		Token         string `json:"token"`
		Authorization string `json:"authorization"`
	}
	if json.Unmarshal(data, &document) != nil || document.DocID != projectID || document.Token == "" || document.Authorization == "" {
		return errors.New("prism document authorization is incomplete")
	}
	if err := t.validateAuthorizationURL(document.URL, true); err != nil {
		return err
	}
	if err := t.validateAuthorizationURL(document.BaseURL, false); err != nil {
		return err
	}
	data, err = t.sandboxJSON(ctx, account, token, sandboxToken, http.MethodPost, "/s/sandboxes/proxy/token", document)
	if err != nil {
		return err
	}
	var synced struct {
		Success bool `json:"success"`
	}
	if json.Unmarshal(data, &synced) != nil || !synced.Success {
		return errors.New("prism sandbox rejected document authorization")
	}
	data, err = t.WaitForSync(ctx, account, token, sandboxToken, 10000)
	if err != nil {
		return err
	}
	var readiness struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &readiness) != nil || readiness.Status != "synced" {
		return errors.New("prism sandbox did not synchronize project files")
	}
	return nil
}

func (t *OpenAIPrismTransport) validateAuthorizationURL(value string, websocket bool) error {
	base, baseErr := url.Parse(t.baseURL())
	target, targetErr := url.Parse(value)
	if baseErr != nil || targetErr != nil || base.Host == "" || target.Host == "" {
		return errors.New("prism authorization URL is not a same-origin endpoint")
	}
	expectedScheme := base.Scheme
	if websocket {
		if expectedScheme == "https" {
			expectedScheme = "wss"
		} else {
			expectedScheme = "ws"
		}
	}
	if baseErr != nil || targetErr != nil || target.Host == "" || !strings.EqualFold(base.Host, target.Host) || target.Scheme != expectedScheme || target.User != nil || target.Fragment != "" || target.RawQuery != "" {
		return errors.New("prism authorization URL is not a same-origin endpoint")
	}
	return nil
}

func (t *OpenAIPrismTransport) ConversationHistory(ctx context.Context, account *Account, token, projectID, conversationID string) ([]byte, error) {
	userID, err := t.authUserID(ctx, account, token)
	if err != nil {
		return nil, err
	}
	data, _, err := t.doJSON(ctx, account, token, http.MethodPost, OpenAIPrismConversationHistoryPath, map[string]any{"conversationId": conversationID, "order": "desc", "limit": 50, "userId": userID, "projectId": projectID})
	return data, err
}

func (t *OpenAIPrismTransport) sandboxRead(ctx context.Context, account *Account, token, sandboxToken, path string) ([]byte, http.Header, error) {
	data, _, headers, err := t.doRaw(ctx, account, token, http.MethodGet, path, nil, "", sandboxToken, nil)
	return data, headers, err
}

func (t *OpenAIPrismTransport) GetLogs(ctx context.Context, account *Account, token, sandboxToken string) ([]byte, http.Header, error) {
	return t.sandboxRead(ctx, account, token, sandboxToken, "/s/sandboxes/proxy/logs")
}

func (t *OpenAIPrismTransport) SyncTex(ctx context.Context, account *Account, token, sandboxToken string) ([]byte, http.Header, error) {
	return t.sandboxRead(ctx, account, token, sandboxToken, "/s/sandboxes/proxy/synctex")
}

func (t *OpenAIPrismTransport) Heartbeat(ctx context.Context, account *Account, token, sandboxToken string) ([]byte, http.Header, error) {
	return t.sandboxRead(ctx, account, token, sandboxToken, "/s/sandboxes/proxy/heartbeat")
}

func (t *OpenAIPrismTransport) WaitForSync(ctx context.Context, account *Account, token, sandboxToken string, waitMS int) ([]byte, error) {
	if waitMS < 1 || waitMS > 30000 {
		waitMS = 10000
	}
	data, _, err := t.sandboxRead(ctx, account, token, sandboxToken, "/s/sandboxes/proxy/wait-for-sync?wait_ms="+strconv.Itoa(waitMS))
	return data, err
}

func (t *OpenAIPrismTransport) WordCount(ctx context.Context, account *Account, token, sandboxToken, mainDocument string, includeBib bool) ([]byte, error) {
	return t.sandboxJSON(ctx, account, token, sandboxToken, http.MethodPost, "/s/sandboxes/proxy/word-count", map[string]any{"mainDocument": mainDocument, "includeBib": includeBib})
}

func (t *OpenAIPrismTransport) LatestRender(ctx context.Context, account *Account, token, projectID, mainDocument string) ([]byte, http.Header, error) {
	data, _, headers, err := t.doRaw(ctx, account, token, http.MethodGet, "/api/projects/"+url.PathEscape(projectID)+"/render-results/latest?main_document="+url.QueryEscape(mainDocument), nil, "", "", nil)
	return data, headers, err
}

func (t *OpenAIPrismTransport) VersionHistory(ctx context.Context, account *Account, token, projectID string, page, pageSize int) ([]byte, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	data, _, err := t.doJSON(ctx, account, token, http.MethodGet, "/api/projects/"+url.PathEscape(projectID)+"/version-history?page="+strconv.Itoa(page)+"&pageSize="+strconv.Itoa(pageSize), nil)
	return data, err
}
