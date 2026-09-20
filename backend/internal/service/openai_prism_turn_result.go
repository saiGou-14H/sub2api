package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// State is an opaque object, not a client-generated cursor. In particular an
// absent/null state on a synchronous completion does not authorize another poll.
func prismValidTurnState(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && len(object) > 0
}

// Both start and status can return a terminal result. A completed start must
// follow the same validation and output handling as a completed poll.
func prismTurnResult(turn prismStatusResponse, path string) (json.RawMessage, error) {
	if prismStatusFailed(turn.Status) || (turn.Response != nil && prismStatusFailed(turn.Response.Status)) {
		return nil, prismTurnFailure(turn, path)
	}
	if !prismStatusCompleted(turn.Status) {
		return nil, nil
	}
	if turn.Response == nil || !strings.EqualFold(turn.Response.Status, "success") {
		return nil, errors.New("prism completed response missing successful result")
	}
	candidate := bytes.TrimSpace(turn.Response.Payload)
	if len(candidate) < 2 || candidate[0] != '{' || candidate[len(candidate)-1] != '}' || !json.Valid(candidate) {
		return nil, errors.New("prism completed response payload is not a JSON object")
	}
	var completed struct {
		Output json.RawMessage `json:"output"`
	}
	_ = json.Unmarshal(candidate, &completed)
	output := bytes.TrimSpace(completed.Output)
	if len(output) < 2 || output[0] != '[' {
		return nil, errors.New("prism completed response payload missing output array")
	}
	return append(json.RawMessage(nil), candidate...), nil
}

// The private error payload can include full request headers and sandbox URLs.
// Expose only a bounded status and a fixed classification, never its debug body.
func prismTurnFailure(turn prismStatusResponse, path string) error {
	code, message := http.StatusBadGateway, "prism turn failed"
	if turn.Response != nil {
		var failure struct {
			HTTPStatus        int `json:"httpStatus"`
			CodexRequestDebug struct {
				Error struct {
					BodyText string `json:"bodyText"`
				} `json:"error"`
			} `json:"codexRequestDebug"`
		}
		if json.Unmarshal(turn.Response.Payload, &failure) == nil {
			if failure.HTTPStatus >= 400 && failure.HTTPStatus <= 599 {
				code = failure.HTTPStatus
			}
			var detail struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(failure.CodexRequestDebug.Error.BodyText), &detail) == nil &&
				strings.Contains(strings.ToLower(detail.Error.Message), "unsupported assistant model") {
				code = http.StatusBadRequest
				message = "Unsupported assistant model: configure this Prism account's model mapping to a model supported by Prism"
			}
		}
	}
	return &OpenAIPrismHTTPError{StatusCode: code, Path: path, Message: message}
}
