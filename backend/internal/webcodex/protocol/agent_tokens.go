// SPDX-License-Identifier: Apache-2.0
// Managed Runner credential DTOs adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// src/runner_tokens_http/routes.rs. Management authority is supplied by the host.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
)

// AgentTokenScopes preserves serde Vec<String>, including rejection of null elements.
type AgentTokenScopes []string

func (s *AgentTokenScopes) UnmarshalJSON(b []byte) error {
	var elements []json.RawMessage
	if err := json.Unmarshal(b, &elements); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return errors.New("scopes cannot be null")
	}
	out := make(AgentTokenScopes, 0, len(elements))
	for _, element := range elements {
		if bytes.Equal(bytes.TrimSpace(element), []byte("null")) {
			return errors.New("scopes cannot contain null")
		}
		var value string
		if err := json.Unmarshal(element, &value); err != nil {
			return err
		}
		out = append(out, value)
	}
	*s = out
	return nil
}

// CreateAgentTokenRequest preserves omitted/null scopes versus explicit empty scopes.
// Username carries the canonical host owner, never a mutable profile name.
type CreateAgentTokenRequest struct {
	Username  string            `json:"username" wire:"required"`
	ClientID  string            `json:"client_id" wire:"required"`
	Name      *string           `json:"name"`
	Scopes    *AgentTokenScopes `json:"scopes"`
	ExpiresAt *int64            `json:"expires_at"`
}

func (r *CreateAgentTokenRequest) UnmarshalJSON(b []byte) error {
	var next CreateAgentTokenRequest
	if err := decodeObject(b, &next, false); err != nil {
		return err
	}
	*r = next
	return nil
}

// RegisterAgentTokenHashRequest is closed and never accepts plaintext credentials.
type RegisterAgentTokenHashRequest struct {
	Username    string           `json:"username" wire:"required"`
	ClientID    string           `json:"client_id" wire:"required"`
	Name        *string          `json:"name"`
	TokenHash   string           `json:"token_hash" wire:"required"`
	TokenPrefix string           `json:"token_prefix" wire:"required"`
	Scopes      AgentTokenScopes `json:"scopes"`
	ExpiresAt   *int64           `json:"expires_at"`
}

func (r *RegisterAgentTokenHashRequest) UnmarshalJSON(b []byte) error {
	var next RegisterAgentTokenHashRequest
	if err := decodeObject(b, &next, true); err != nil {
		return err
	}
	*r = next
	return nil
}

// ListAgentTokensRequest selects one canonical host owner.
type ListAgentTokensRequest struct {
	Username string `json:"username" wire:"required"`
}

func (r *ListAgentTokensRequest) UnmarshalJSON(b []byte) error {
	var next ListAgentTokensRequest
	if err := decodeObject(b, &next, false); err != nil {
		return err
	}
	*r = next
	return nil
}

// RevokeAgentTokenRequest retains the original credential ID and target fields.
type RevokeAgentTokenRequest struct {
	Username string `json:"username" wire:"required"`
	TokenID  string `json:"token_id" wire:"required"`
}

func (r *RevokeAgentTokenRequest) UnmarshalJSON(b []byte) error {
	var next RevokeAgentTokenRequest
	if err := decodeObject(b, &next, false); err != nil {
		return err
	}
	*r = next
	return nil
}
