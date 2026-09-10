// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// src/runner_http/handlers.rs. See README.md for the bounded migration scope.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

// Authenticate verifies a Bearer credential with host expiry, revocation and
// credential-kind rules. It must not return a Principal from unverified claims.
type Authenticate func(context.Context, string) (Principal, error)

// HTTPHandler serves only the original Runner polling routes. Constructing a
// handler does not mount routes into sub2api; the host auth adapter is required.
type HTTPHandler struct {
	registry     *Registry
	authenticate Authenticate
	maxBodyBytes int64
}

// NewHTTPHandler rejects missing auth and unbounded body configuration.
// MaxBodyBytes limits the complete request body, separately from shell limits.
func NewHTTPHandler(registry *Registry, authenticate Authenticate, maxBodyBytes int64) (*HTTPHandler, error) {
	if registry == nil || authenticate == nil || maxBodyBytes <= 0 {
		return nil, fmt.Errorf("registry, authenticator and positive HTTP body limit are required")
	}
	return &HTTPHandler{registry: registry, authenticate: authenticate, maxBodyBytes: maxBodyBytes}, nil
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/shell/agent/"
	action := strings.TrimPrefix(r.URL.Path, prefix)
	if r.URL.Path != prefix+action || (action != "register" && action != "poll" && action != "result" && action != "offline") {
		writeError(w, http.StatusNotFound, "unknown Runner endpoint")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "Runner endpoint requires POST")
		return
	}
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 {
		unauthorized(w)
		return
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		unauthorized(w)
		return
	}
	principal, err := h.authenticate(r.Context(), parts[1])
	if err != nil {
		unauthorized(w)
		return
	}
	bodyReader := http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	defer bodyReader.Close()
	data, err := io.ReadAll(bodyReader)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, http.StatusRequestEntityTooLarge, "Runner request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "cannot read Runner request body")
		}
		return
	}
	switch action {
	case "register":
		body, err := protocol.ReadRegisterRequest(data)
		if err != nil {
			writeDecodeError(w)
			return
		}
		view, err := h.registry.Register(principal, body)
		if err != nil {
			writeRegistryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.RunnerRegisterResponse{Success: true, Client: &view})
	case "poll":
		body, err := protocol.ReadPollPayload(data)
		if err != nil {
			writeDecodeError(w)
			return
		}
		request, err := h.registry.Poll(principal, body)
		if err != nil {
			writeRegistryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.RunnerPollResponse{Success: true, Request: request})
	case "result":
		body, err := protocol.ReadResultPayload(data)
		if err != nil {
			writeDecodeError(w)
			return
		}
		if err = h.registry.Complete(principal, body); err != nil {
			writeRegistryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.RunnerResultResponse{Success: true})
	case "offline":
		body, err := protocol.ReadOfflineRequest(data)
		if err != nil {
			writeDecodeError(w)
			return
		}
		if err = h.registry.Offline(principal, body); err != nil {
			writeRegistryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.RunnerOfflineResponse{Success: true})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, "Runner authentication required")
}
func writeDecodeError(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "invalid Runner JSON payload")
}
func writeRegistryError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, ErrForbidden) {
		status = http.StatusForbidden
	}
	if errors.Is(err, ErrClosed) {
		status = http.StatusServiceUnavailable
	}
	writeError(w, status, err.Error())
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, protocol.RunnerResultResponse{Success: false, Error: &message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "Runner response encoding failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// The waiter has already settled; a broken socket cannot authorize redelivery.
	_, _ = w.Write(body)
}
