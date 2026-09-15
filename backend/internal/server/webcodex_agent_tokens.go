// SPDX-License-Identifier: Apache-2.0
// Original routes/validation/responses: WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// src/runner_tokens_http/{routes,responses}.rs and src/auth/pat.rs.
package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// webCodexAgentTokens is reached only through the host's panel JWT middleware.
// Its User repository revalidates immutable identity and current role/status.
type webCodexAgentTokens struct {
	keys         *repository.WebCodexAPIKeyRepository
	users        service.UserRepository
	maxBodyBytes int64
}

func (r *WebCodexRunner) mountManagement(router *gin.Engine, jwt middleware.JWTAuthMiddleware, backendGuard, panelLimit gin.HandlerFunc, audit middleware.AuditLogMiddleware) {
	if r == nil || r.tokens == nil {
		return
	}
	if jwt == nil || backendGuard == nil || panelLimit == nil || audit == nil {
		panic("WebCodex credential management requires host panel middleware")
	}
	group := router.Group("/api/agent-tokens", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}, gin.HandlerFunc(jwt), backendGuard, panelLimit, gin.HandlerFunc(audit))
	group.POST("/create", r.tokens.create)
	group.POST("/register_hash", r.tokens.registerHash)
	group.POST("/list", r.tokens.list)
	group.POST("/revoke", r.tokens.revoke)
}

func agentTokenError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"status": status, "error": message})
}
func (h *webCodexAgentTokens) body(c *gin.Context, out any) bool {
	c.Header("Cache-Control", "no-store")
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			agentTokenError(c, 413, "request body too large")
		} else {
			agentTokenError(c, 400, "invalid request body")
		}
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		// Decode errors can contain supplied secret values or unknown-field names.
		agentTokenError(c, 400, "invalid request body")
		return false
	}
	return true
}

// Original username field is retained, with the existing canonical host owner
// encoding. Profile usernames and JWT role claims never select another user.
func (h *webCodexAgentTokens) authorize(c *gin.Context, username string) (*service.User, int64, string, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		agentTokenError(c, 401, "host user authentication required")
		return nil, 0, "", false
	}
	caller, err := h.users.GetByID(c.Request.Context(), subject.UserID)
	if err != nil && !errors.Is(err, service.ErrUserNotFound) {
		agentTokenError(c, 500, "host user lookup unavailable")
		return nil, 0, "", false
	}
	if caller == nil || caller.ID != subject.UserID || caller.DeletedAt != nil || !caller.IsActive() {
		agentTokenError(c, 401, "current host user is unavailable")
		return nil, 0, "", false
	}
	username = strings.TrimSpace(username)
	id, err := strconv.ParseInt(strings.TrimPrefix(username, "sub2api_user_"), 10, 64)
	canonical, ownerErr := runner.HostUserOwner(id)
	if err != nil || ownerErr != nil || canonical != username {
		agentTokenError(c, 400, "username must be a canonical sub2api_user_<positive int64>")
		return nil, 0, "", false
	}
	if caller.ID != id && !caller.IsAdmin() {
		agentTokenError(c, 403, "caller may only manage their own resources")
		return nil, 0, "", false
	}
	return caller, id, canonical, true
}

// Resolve only after action-specific validation, preserving the original 400
// response for malformed input before a missing-target 404 lookup.
func (h *webCodexAgentTokens) target(c *gin.Context, caller *service.User, id int64, issuing bool) (*service.User, bool) {
	user := caller
	if caller.ID != id {
		var err error
		user, err = h.users.GetByID(c.Request.Context(), id)
		if err != nil && !errors.Is(err, service.ErrUserNotFound) {
			agentTokenError(c, 500, "host user lookup unavailable")
			return nil, false
		}
	}
	if user == nil || user.ID != id || user.DeletedAt != nil {
		agentTokenError(c, 404, "user not found")
		return nil, false
	}
	// Original admin list/revoke can manage a disabled target; issuance cannot.
	if issuing && !user.IsActive() {
		agentTokenError(c, 403, "user is disabled")
		return nil, false
	}
	return user, true
}

func agentTokenClient(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("allowed_client_id cannot be empty")
	}
	if utf8.RuneCountInString(value) > 80 {
		return "", errors.New("allowed_client_id is too long; maximum is 80 characters")
	}
	for _, ch := range value {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
			return "", errors.New("allowed_client_id may only contain ASCII letters, digits, '-', '_', and '.'")
		}
	}
	return value, nil
}
func agentTokenName(value *string, fallback string) (string, error) {
	name := ""
	if value != nil {
		name = strings.TrimSpace(*value)
	}
	if name == "" {
		name = fallback
	}
	if utf8.RuneCountInString(name) > 128 {
		return "", errors.New("token name is too long")
	}
	return name, nil
}
func defaultAgentTokenScopes() []string {
	return []string{"agent:register", "agent:poll", "agent:result", "agent:job_update"}
}
func agentTokenScopes(raw []string) ([]string, error) {
	scopes := make([]string, 0, len(raw))
	seen := make(map[string]bool)
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		switch s {
		case "agent:register", "agent:poll", "agent:result", "agent:job_update":
		default:
			return nil, errors.New("agent tokens may only carry agent:* transport scopes")
		}
		if !seen[s] {
			scopes = append(scopes, s)
			seen[s] = true
		}
	}
	return scopes, nil
}
func agentTokenPrefix(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "wc_agent_") {
		return "", errors.New("token_prefix must start with wc_agent_")
	}
	if len(value) <= len("wc_agent_") || len(value) > 32 {
		return "", errors.New("token_prefix length is invalid")
	}
	for _, ch := range value {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return "", errors.New("token_prefix contains invalid characters")
		}
	}
	return value, nil
}
func agentTokenHash(raw string) (string, error) {
	value := strings.TrimPrefix(strings.TrimSpace(raw), "sha256:")
	if len(value) != 64 {
		return "", errors.New("token_hash must be sha256:<64 hex> or bare 64 hex")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", errors.New("token_hash must be sha256:<64 hex> or bare 64 hex")
	}
	return strings.ToLower(value), nil
}
func agentTokenFields(c *gin.Context, client string, name *string, fallback string, rawScopes []string, expires *int64) (string, string, []string, bool) {
	client, err := agentTokenClient(client)
	if err != nil {
		agentTokenError(c, 400, err.Error())
		return "", "", nil, false
	}
	nameValue, err := agentTokenName(name, fallback)
	if err != nil {
		agentTokenError(c, 400, err.Error())
		return "", "", nil, false
	}
	scopes, err := agentTokenScopes(rawScopes)
	if err != nil {
		agentTokenError(c, 400, err.Error())
		return "", "", nil, false
	}
	if expires != nil && *expires <= time.Now().Unix() {
		agentTokenError(c, 400, "expires_at must be in the future")
		return "", "", nil, false
	}
	return client, nameValue, scopes, true
}
func (h *webCodexAgentTokens) create(c *gin.Context) {
	var body protocol.CreateAgentTokenRequest
	if !h.body(c, &body) {
		return
	}
	caller, targetID, owner, ok := h.authorize(c, body.Username)
	if !ok {
		return
	}
	scopes := defaultAgentTokenScopes()
	if body.Scopes != nil {
		scopes = []string(*body.Scopes)
	}
	client, name, scopes, ok := agentTokenFields(c, body.ClientID, body.Name, "default", scopes, body.ExpiresAt)
	if !ok {
		return
	}
	user, ok := h.target(c, caller, targetID, true)
	if !ok {
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		agentTokenError(c, 500, "credential generation unavailable")
		return
	}
	token := "wc_agent_" + hex.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	id, err := uuid.NewRandom()
	if err != nil {
		agentTokenError(c, 500, "credential generation unavailable")
		return
	}
	key := &repository.WebCodexAPIKey{ID: id.String(), UserID: user.ID, Name: name, KeyHash: hex.EncodeToString(hash[:]), KeyPrefix: token[:16], CreatedAt: time.Now().Unix(), Scopes: strings.Join(scopes, " "), ExpiresAt: body.ExpiresAt, Kind: "agent", AllowedClientID: &client}
	if err := h.keys.Insert(c.Request.Context(), key); err != nil {
		agentTokenError(c, 500, "credential storage unavailable")
		return
	}
	c.JSON(200, gin.H{"success": true, "token": token, "token_prefix": key.KeyPrefix, "token_id": key.ID, "name": key.Name, "kind": "agent", "username": owner, "user_id": owner, "allowed_client_id": client, "scopes": scopes, "created_at": key.CreatedAt, "expires_at": key.ExpiresAt})
}
func (h *webCodexAgentTokens) registerHash(c *gin.Context) {
	var body protocol.RegisterAgentTokenHashRequest
	if !h.body(c, &body) {
		return
	}
	caller, targetID, _, ok := h.authorize(c, body.Username)
	if !ok {
		return
	}
	scopes := []string(body.Scopes)
	if len(scopes) == 0 {
		scopes = defaultAgentTokenScopes()
	}
	client, name, scopes, ok := agentTokenFields(c, body.ClientID, body.Name, strings.TrimSpace(body.ClientID), scopes, body.ExpiresAt)
	if !ok {
		return
	}
	hash, err := agentTokenHash(body.TokenHash)
	if err != nil {
		agentTokenError(c, 400, err.Error())
		return
	}
	prefix, err := agentTokenPrefix(body.TokenPrefix)
	if err != nil {
		agentTokenError(c, 400, err.Error())
		return
	}
	user, ok := h.target(c, caller, targetID, true)
	if !ok {
		return
	}
	id, err := uuid.NewRandom()
	if err != nil {
		agentTokenError(c, 500, "credential generation unavailable")
		return
	}
	key := &repository.WebCodexAPIKey{ID: id.String(), UserID: user.ID, Name: name, KeyHash: hash, KeyPrefix: prefix, CreatedAt: time.Now().Unix(), Scopes: strings.Join(scopes, " "), ExpiresAt: body.ExpiresAt, Kind: "agent", AllowedClientID: &client}
	if err := h.keys.Insert(c.Request.Context(), key); err != nil {
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23505" {
			agentTokenError(c, 409, "token hash already exists")
		} else {
			agentTokenError(c, 500, "credential storage unavailable")
		}
		return
	}
	c.JSON(200, gin.H{"success": true, "token": gin.H{"id": key.ID, "name": key.Name, "token_prefix": key.KeyPrefix, "allowed_client_id": key.AllowedClientID, "scopes": scopes, "created_at": key.CreatedAt, "expires_at": key.ExpiresAt}})
}
func agentTokenSummary(key *repository.WebCodexAPIKey) gin.H {
	owner, _ := runner.HostUserOwner(key.UserID)
	scopes := strings.Fields(key.Scopes)
	if scopes == nil {
		scopes = []string{}
	}
	return gin.H{"id": key.ID, "user_id": owner, "name": key.Name, "token_prefix": key.KeyPrefix, "kind": key.Kind, "allowed_client_id": key.AllowedClientID, "scopes": scopes, "created_at": key.CreatedAt, "last_used_at": key.LastUsedAt, "expires_at": key.ExpiresAt, "revoked_at": key.RevokedAt}
}
func (h *webCodexAgentTokens) list(c *gin.Context) {
	var body protocol.ListAgentTokensRequest
	if !h.body(c, &body) {
		return
	}
	caller, targetID, owner, ok := h.authorize(c, body.Username)
	if !ok {
		return
	}
	user, ok := h.target(c, caller, targetID, false)
	if !ok {
		return
	}
	keys, err := h.keys.ListAgentByUser(c.Request.Context(), user.ID)
	if err != nil {
		agentTokenError(c, 500, "credential storage unavailable")
		return
	}
	tokens := make([]gin.H, 0, len(keys))
	for _, key := range keys {
		tokens = append(tokens, agentTokenSummary(key))
	}
	c.JSON(200, gin.H{"success": true, "username": owner, "user_id": owner, "tokens": tokens, "count": len(tokens)})
}
func (h *webCodexAgentTokens) revoke(c *gin.Context) {
	var body protocol.RevokeAgentTokenRequest
	if !h.body(c, &body) {
		return
	}
	caller, targetID, _, ok := h.authorize(c, body.Username)
	if !ok {
		return
	}
	id := strings.TrimSpace(body.TokenID)
	if id == "" {
		agentTokenError(c, 400, "token_id cannot be empty")
		return
	}
	user, ok := h.target(c, caller, targetID, false)
	if !ok {
		return
	}
	key, err := h.keys.RevokeAgentByUser(c.Request.Context(), user.ID, id, time.Now().Unix())
	if err != nil {
		agentTokenError(c, 500, "credential storage unavailable")
		return
	}
	if key == nil {
		owner, kind, found, err := h.keys.AgentTokenIdentity(c.Request.Context(), id)
		switch {
		case err != nil:
			agentTokenError(c, 500, "credential storage unavailable")
		case !found:
			agentTokenError(c, 404, "token not found")
		case owner != user.ID:
			agentTokenError(c, 403, "token does not belong to the specified user")
		case kind != "agent":
			agentTokenError(c, 400, "token is not an agent token")
		default:
			agentTokenError(c, 409, "token changed during revocation")
		}
		return
	}
	c.JSON(200, gin.H{"success": true, "token": agentTokenSummary(key)})
}
