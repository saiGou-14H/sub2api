// SPDX-License-Identifier: Apache-2.0
package protocol_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func packageListFixture(t *testing.T) skillReadFixture {
	t.Helper()
	b, err := os.ReadFile("testdata/skill-packages-list.json")
	if err != nil {
		t.Fatal(err)
	}
	// The independent listing fixture uses the same language-neutral schema.
	var f skillReadFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.SchemaVersion != 1 || f.SourceCommit != "97ad66949a859174911c2f6da2ff1063be98bfa9" || f.Provenance == "" {
		t.Fatal("listing fixture provenance changed")
	}
	return f
}

func TestSkillPackagesListSourceFixtures(t *testing.T) {
	f := packageListFixture(t)
	seen := map[string]bool{}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if seen[c.Name] {
				t.Fatal("duplicate case name")
			}
			seen[c.Name] = true
			wire, err := json.Marshal(mergeSkillFields(f.BaseRequest, c.Set, c.Omit))
			if err != nil {
				t.Fatal(err)
			}
			if c.RawWire != "" {
				wire = []byte(c.RawWire)
			}
			r, err := protocol.ReadRequest(wire)
			if c.Outcome == "wire_reject" {
				if err == nil {
					t.Fatal("wire rejection expected")
				}
				if _, err := protocol.DecodeRequest(wire); err == nil {
					t.Fatal("public decoder admitted rejected wire")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.ExpectedWireKind != "" && r.Kind != c.ExpectedWireKind {
				t.Fatalf("wire kind %q", r.Kind)
			}
			i, err := r.DecodeInvocation()
			switch c.Outcome {
			case "invalid":
				if err == nil || errors.Is(err, protocol.ErrUnsupported) || errors.Is(err, protocol.ErrUnknownKind) {
					t.Fatalf("canonical invalid expected: %v", err)
				}
				return
			case "unknown":
				if !errors.Is(err, protocol.ErrUnknownKind) {
					t.Fatalf("unknown sync kind: %v", err)
				}
				if _, err := r.DecodeJobInvocation(); !errors.Is(err, protocol.ErrUnknownKind) {
					t.Fatalf("unknown Job kind: %v", err)
				}
				return
			case "accept":
				if err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unknown outcome %q", c.Outcome)
			}
			if _, ok := i.Operation.(protocol.FileOperation); !ok {
				t.Fatalf("non-file operation %T", i.Operation)
			}
			expected := map[string]any{
				"metadata":  mergeSkillFields(f.BaseInvocation["metadata"], c.ExpectedMetadata, nil),
				"operation": mergeSkillFields(f.BaseInvocation["operation"], c.ExpectedOperation, nil),
			}
			if got, want := losslessSkillJSON(t, skillCanonical(i)), losslessSkillJSON(t, expected); !reflect.DeepEqual(got, want) {
				t.Fatalf("canonical fields:\ngot %#v\nwant %#v", got, want)
			}
			round, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := protocol.ReadRequest(round)
			if err != nil || !reflect.DeepEqual(restored, r) {
				t.Fatalf("request roundtrip changed opaque/wide/nullable fields: %v", err)
			}
			public, err := protocol.DecodeRequest(wire)
			if err != nil || !reflect.DeepEqual(public, i) {
				t.Fatalf("public decoder differs: %v", err)
			}
			if _, err := r.DecodeJobInvocation(); !errors.Is(err, protocol.ErrUnsupported) {
				t.Fatalf("sync kind in Job decoder: %v", err)
			}
			if _, err := protocol.DecodeJobRequest(wire); !errors.Is(err, protocol.ErrUnsupported) {
				t.Fatalf("sync wire in Job decoder: %v", err)
			}
		})
	}
}

func TestSkillPackagesListRemainingDeferredKinds(t *testing.T) {
	f := packageListFixture(t)
	if len(f.DeferredFileKinds) != 15 {
		t.Fatal("remaining File coverage changed")
	}
	seen := map[string]bool{}
	for _, kind := range f.DeferredFileKinds {
		t.Run(kind, func(t *testing.T) {
			if seen[kind] {
				t.Fatal("duplicate deferred kind")
			}
			seen[kind] = true
			k, err := json.Marshal(kind)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(mergeSkillFields(f.BaseRequest, map[string]json.RawMessage{"kind": k}, nil))
			if err != nil {
				t.Fatal(err)
			}
			r, err := protocol.ReadRequest(b)
			if err != nil {
				t.Fatal(err)
			}
			inv, err := r.DecodeInvocation()
			// Preserve the historical fixture; these synchronous File kinds were added later.
			if kind == "file_project_overview" || kind == "file_write_project_file" {
				if err != nil || inv.Operation.WireKind() != kind {
					t.Fatalf("newly supported File kind: %v", err)
				}
			} else if !errors.Is(err, protocol.ErrUnsupported) {
				t.Fatalf("sync: %v", err)
			}
			if _, err := r.DecodeJobInvocation(); !errors.Is(err, protocol.ErrUnsupported) {
				t.Fatalf("Job: %v", err)
			}
		})
	}
}

func TestSkillPackagesListCanonicalOwnership(t *testing.T) {
	f := packageListFixture(t)
	b, err := json.Marshal(f.BaseRequest)
	if err != nil {
		t.Fatal(err)
	}
	r, err := protocol.ReadRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	max := uint64(18446744073709551615)
	r.MaxBytes = &max
	i, err := r.DecodeInvocation()
	if err != nil {
		t.Fatal(err)
	}
	before := losslessSkillJSON(t, skillCanonical(i))
	*r.Cwd, *r.Path, *r.Content, *r.MaxBytes = "/other", "other", "changed", 0
	if !reflect.DeepEqual(before, losslessSkillJSON(t, skillCanonical(i))) {
		t.Fatal("wire mutation retargeted canonical listing")
	}
	if reflect.TypeFor[protocol.RunnerRequest]().NumField() != 27 || reflect.TypeFor[protocol.FilePayload]().NumField() != 9 {
		t.Fatal("original field set expanded")
	}
}
