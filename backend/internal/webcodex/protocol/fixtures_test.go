// SPDX-License-Identifier: Apache-2.0
// Public API fixtures for the WebCodex fixed-source protocol adaptation.
package protocol_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestPublicJSONFixtures(t *testing.T) {
	var table struct {
		SchemaVersion int    `json:"schema_version"`
		SourceCommit  string `json:"source_commit"`
		Requests      []struct {
			Name       string          `json:"name"`
			Request    json.RawMessage `json:"request"`
			Wire       string          `json:"wire"`
			Invocation string          `json:"invocation"`
			Kind       string          `json:"kind"`
		} `json:"requests"`
		Registrations []struct {
			Name      string          `json:"name"`
			Request   json.RawMessage `json:"request"`
			Wire      string          `json:"wire"`
			Admission string          `json:"admission"`
		} `json:"registrations"`
	}
	b, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &table); err != nil {
		t.Fatal(err)
	}
	if table.SchemaVersion != 1 || table.SourceCommit != "97ad66949a859174911c2f6da2ff1063be98bfa9" {
		t.Fatal("unexpected fixture provenance/schema")
	}
	for _, c := range table.Requests {
		t.Run(c.Name, func(t *testing.T) {
			r, err := protocol.ReadRequest(c.Request)
			if c.Wire == "reject" {
				if err == nil {
					t.Fatal("wire rejection expected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			inv, err := r.DecodeInvocation()
			switch c.Invocation {
			case "accept":
				if err != nil {
					t.Fatal(err)
				}
				if inv.Operation.WireKind() != c.Kind {
					t.Fatalf("kind: %s", inv.Operation.WireKind())
				}
			case "invalid":
				if err == nil || errors.Is(err, protocol.ErrUnsupported) || errors.Is(err, protocol.ErrUnknownKind) {
					t.Fatalf("invalid payload expected: %v", err)
				}
			case "unsupported":
				if !errors.Is(err, protocol.ErrUnsupported) {
					t.Fatalf("unsupported expected: %v", err)
				}
			case "unknown":
				if !errors.Is(err, protocol.ErrUnknownKind) {
					t.Fatalf("unknown kind expected: %v", err)
				}
			default:
				t.Fatal("unknown fixture invocation outcome")
			}
		})
	}
	for _, c := range table.Registrations {
		t.Run(c.Name, func(t *testing.T) {
			r, err := protocol.ReadRegisterRequest(c.Request)
			if c.Wire == "reject" {
				if err == nil {
					t.Fatal("wire rejection expected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			err = protocol.ValidateRegistration(r)
			if err == nil {
				err = protocol.ValidateGenerationCapabilities(r.Capabilities)
			}
			if (err == nil) != (c.Admission == "accept") {
				t.Fatalf("admission %s: %v", c.Admission, err)
			}
		})
	}
}
