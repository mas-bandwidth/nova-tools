package records

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func stellaFixture(t *testing.T) (*Object, Allowlists) {
	t.Helper()
	raw, err := os.ReadFile("testdata/valid/antigravity_request.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseStrict(raw, nil, "envelope")
	if err != nil {
		t.Fatal(err)
	}
	bv, _ := v.(*Object).Get("body")
	b := bv.(*Object)
	uv, _ := b.Get("raw_usage")
	return b, Allowlists{RawUsageFields: append([]string(nil), uv.(*Object).Keys()...), ReceiptFields: []string{"turn_id"}}
}

func TestStellaMissingSupportedUsageEntryIsRefused(t *testing.T) {
	body, allow := stellaFixture(t)
	validator := NewValidator(allow)
	raw, _, err := Seal(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = validator.ValidateEnvelope(raw); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	uv, _ := body.Get("raw_usage")
	usage := uv.(*Object)
	delete(usage.vals, "input")
	var keys []string
	for _, k := range usage.keys {
		if k != "input" {
			keys = append(keys, k)
		}
	}
	usage.keys = keys
	raw, _, err = Seal(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = validator.ValidateEnvelope(raw); err == nil {
		t.Fatal("accepted omission of a supported usage field; contract requires an explicit absent entry")
	}
}

func TestStellaUnsupportedKeyCannotEnterDiagnostic(t *testing.T) {
	body, allow := stellaFixture(t)
	sentinel := "SYNTHETIC_PRIVATE_PATH_SENTINEL\nFORGED_REPORT"
	body.set(sentinel, "synthetic")
	raw, _, err := Seal(body)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewValidator(allow).ValidateEnvelope(raw)
	if err == nil {
		t.Fatal("unexpected acceptance")
	}
	if strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "\n") {
		t.Fatal("unsupported source member name entered the diagnostic, including its newline")
	}
}

func TestStellaCanonicalAPICannotEmitInvalidUTF8(t *testing.T) {
	got, err := Canonicalize(string([]byte{0xff}))
	if err == nil && !utf8.Valid(got) {
		t.Fatal("canonical API emitted invalid UTF-8 instead of refusing")
	}
}

func TestAntigravity129Join134Validator(t *testing.T) {
	// 1. Explicit per-source mapping allowlist for Antigravity synthetic source
	allow := Allowlists{
		RawUsageFields: []string{
			"cache_read_tokens",
			"input_tokens",
			"output_tokens",
			"thinking_output_tokens",
			"total_tokens",
		},
		ReceiptFields: []string{"idx"},
	}
	validator := NewValidator(allow)

	// 2. Actual 129 synthetic turn 1 envelope (all present)
	// Body kind is turn, source.kind is antigravity, bench is studio,
	// mapping_id is content-addressed sha256.
	rawTurn1 := []byte(`{
		"body": {
			"schema": "nova.tokens.observation/2",
			"source": {
				"kind": "antigravity",
				"producer_version": "2.12.2",
				"namespace": "antigravity",
				"session_id": "conv-synth-001",
				"event_key": ["conv-synth-001", "1"]
			},
			"kind": "turn",
			"revision": {
				"native": null,
				"supersedes": [],
				"basis": "source_order"
			},
			"time": {
				"occurred_at": "2026-09-12T12:00:01.100200Z",
				"start": null,
				"end": null,
				"basis": "response_observation"
			},
			"origin": {
				"friend": "emma",
				"bench": "studio",
				"basis": "owner_binding",
				"binding_id": null
			},
			"model": {
				"id": "gemini-2.5-pro",
				"basis": "harness_reported"
			},
			"repository": {
				"id": "mas-bandwidth/emma",
				"basis": "source_binding",
				"policy_id": null,
				"touched": []
			},
			"raw_usage": {
				"cache_read_tokens": {
					"presence": "present",
					"value": "45000",
					"number_kind": "integer",
					"unit": "tokens",
					"reason": null
				},
				"input_tokens": {
					"presence": "present",
					"value": "1200",
					"number_kind": "integer",
					"unit": "tokens",
					"reason": null
				},
				"output_tokens": {
					"presence": "present",
					"value": "500",
					"number_kind": "integer",
					"unit": "tokens",
					"reason": null
				},
				"thinking_output_tokens": {
					"presence": "present",
					"value": "120",
					"number_kind": "integer",
					"unit": "tokens",
					"reason": null
				},
				"total_tokens": {
					"presence": "present",
					"value": "128000",
					"number_kind": "integer",
					"unit": "tokens",
					"reason": null
				}
			},
			"model_usage": [],
			"mapping_id": "sha256:1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b",
			"receipt": {
				"idx": "1"
			}
		}
	}`)

	v1, err := parseStrict(rawTurn1, nil, "envelope")
	if err != nil {
		t.Fatalf("parse rawTurn1: %v", err)
	}
	bodyObj1 := v1.(*Object).vals["body"].(*Object)
	sealedTurn1, expectedID1, err := Seal(bodyObj1)
	if err != nil {
		t.Fatalf("seal turn 1: %v", err)
	}

	env1, err := validator.ValidateEnvelope(sealedTurn1)
	if err != nil {
		t.Fatalf("validation failed for 129 turn 1: %v", err)
	}
	if env1.ID != expectedID1 {
		t.Errorf("ID mismatch: got %s, want %s", env1.ID, expectedID1)
	}
	if env1.Observation.Source.Kind != "antigravity" {
		t.Errorf("source.kind mismatch: got %s, want antigravity", env1.Observation.Source.Kind)
	}
	if env1.Observation.Kind != "turn" {
		t.Errorf("body.kind mismatch: got %s, want turn", env1.Observation.Kind)
	}
	if env1.Observation.Origin.Bench == nil || *env1.Observation.Origin.Bench != "studio" {
		t.Errorf("origin.bench mismatch: got %v, want studio", env1.Observation.Origin.Bench)
	}

	// 3. Actual 129 synthetic turn 2 envelope with omitted_from_wire
	rawTurn2 := []byte(`{
		"body": {
			"schema": "nova.tokens.observation/2",
			"source": {
				"kind": "antigravity",
				"producer_version": "2.12.2",
				"namespace": "antigravity",
				"session_id": "conv-synth-001",
				"event_key": ["conv-synth-001", "2"]
			},
			"kind": "turn",
			"revision": {
				"native": null,
				"supersedes": [],
				"basis": "source_order"
			},
			"time": {
				"occurred_at": "2026-09-12T12:01:15.300400Z",
				"start": null,
				"end": null,
				"basis": "response_observation"
			},
			"origin": {
				"friend": "emma",
				"bench": "studio",
				"basis": "owner_binding",
				"binding_id": null
			},
			"model": {
				"id": "gemini-2.5-pro",
				"basis": "harness_reported"
			},
			"repository": {
				"id": "mas-bandwidth/emma",
				"basis": "source_binding",
				"policy_id": null,
				"touched": []
			},
			"raw_usage": {
				"cache_read_tokens": {
					"presence": "absent",
					"value": null,
					"number_kind": "integer",
					"unit": "tokens",
					"reason": "omitted_from_wire"
				},
				"input_tokens": {
					"presence": "present",
					"value": "800",
					"number_kind": "integer",
					"unit": "tokens",
					"reason": null
				},
				"output_tokens": {
					"presence": "absent",
					"value": null,
					"number_kind": "integer",
					"unit": "tokens",
					"reason": "omitted_from_wire"
				},
				"thinking_output_tokens": {
					"presence": "absent",
					"value": null,
					"number_kind": "integer",
					"unit": "tokens",
					"reason": "omitted_from_wire"
				},
				"total_tokens": {
					"presence": "absent",
					"value": null,
					"number_kind": "integer",
					"unit": "tokens",
					"reason": "omitted_from_wire"
				}
			},
			"model_usage": [],
			"mapping_id": "sha256:1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b",
			"receipt": {
				"idx": "2"
			}
		}
	}`)

	v2, err := parseStrict(rawTurn2, nil, "envelope")
	if err != nil {
		t.Fatalf("parse rawTurn2: %v", err)
	}
	bodyObj2 := v2.(*Object).vals["body"].(*Object)
	sealedTurn2, expectedID2, err := Seal(bodyObj2)
	if err != nil {
		t.Fatalf("seal turn 2: %v", err)
	}

	env2, err := validator.ValidateEnvelope(sealedTurn2)
	if err != nil {
		t.Fatalf("validation failed for 129 turn 2: %v", err)
	}
	if env2.ID != expectedID2 {
		t.Errorf("ID mismatch: got %s, want %s", env2.ID, expectedID2)
	}
	cacheRead := env2.Observation.RawUsage["cache_read_tokens"]
	if cacheRead.Presence != "absent" || cacheRead.Reason == nil || *cacheRead.Reason != "omitted_from_wire" {
		t.Errorf("expected absent with omitted_from_wire, got %+v", cacheRead)
	}
}
