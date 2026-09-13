package records

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealedCodexMappingValidates(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "codex", "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	v := NewValidator(Allowlists{})
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("sealed codex mapping refused: %v", err)
	}
	if env.Mapping == nil {
		t.Fatal("expected env.Mapping to be set")
	}
	if env.Mapping.Name != "codex-desktop-responses" {
		t.Errorf("got name %q, want codex-desktop-responses", env.Mapping.Name)
	}
	if env.Mapping.IdentityRule.SourceKind != "codex_desktop" {
		t.Errorf("got source_kind %q, want codex_desktop", env.Mapping.IdentityRule.SourceKind)
	}
	if len(env.Mapping.FieldRules) != 6 {
		t.Errorf("got %d field rules, want 6", len(env.Mapping.FieldRules))
	}
	if env.ID != "sha256:34a1189b7cf58e352f2dd4da8ea4e1076894a933bb9e3b1391082b3ccc2a2c7d" {
		t.Errorf("got ID %s, want sha256:34a1189b...", env.ID)
	}

	// Reseal byte-identity test
	resealedRaw, resealedID, err := SealMapping(*env.Mapping)
	if err != nil {
		t.Fatalf("SealMapping failed: %v", err)
	}
	if resealedID != env.ID {
		t.Errorf("resealed ID %s != env.ID %s", resealedID, env.ID)
	}
	trimmedRaw := bytes.TrimSpace(raw)
	if !bytes.Equal(resealedRaw, trimmedRaw) {
		t.Errorf("resealed bytes differ from sealed fixture:\ngot:  %s\nwant: %s", resealedRaw, trimmedRaw)
	}
}

func TestSealedGrokMappingValidates(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "grok", "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	v := NewValidator(Allowlists{})
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("sealed grok mapping refused: %v", err)
	}
	if env.Mapping == nil {
		t.Fatal("expected env.Mapping to be set")
	}
	if env.Mapping.Name != "grok-turn-export" {
		t.Errorf("got name %q, want grok-turn-export", env.Mapping.Name)
	}
	if env.Mapping.IdentityRule.SourceKind != "grok" {
		t.Errorf("got source_kind %q, want grok", env.Mapping.IdentityRule.SourceKind)
	}
	if len(env.Mapping.FieldRules) != 9 {
		t.Errorf("got %d field rules, want 9", len(env.Mapping.FieldRules))
	}
	if env.ID != "sha256:00b0ebe373b6f79f0ca1f029246273d75f84517e57a2fb99c5cd486b2dcb526c" {
		t.Errorf("got ID %s, want sha256:00b0ebe3...", env.ID)
	}

	// Reseal byte-identity test
	resealedRaw, resealedID, err := SealMapping(*env.Mapping)
	if err != nil {
		t.Fatalf("SealMapping failed: %v", err)
	}
	if resealedID != env.ID {
		t.Errorf("resealed ID %s != env.ID %s", resealedID, env.ID)
	}
	trimmedRaw := bytes.TrimSpace(raw)
	if !bytes.Equal(resealedRaw, trimmedRaw) {
		t.Errorf("resealed bytes differ from sealed fixture:\ngot:  %s\nwant: %s", resealedRaw, trimmedRaw)
	}
}

func TestPositiveCoverageRecordValidates(t *testing.T) {
	c := Coverage{
		Schema:         SchemaCoverage,
		ScopeID:        "nova.codex-desktop.responses",
		SourceIDs:      []string{"nova.codex-desktop.responses"},
		Interval:       CoverageInterval{Start: "2026-09-12T00:00:00Z", End: "2026-09-13T00:00:00Z"},
		Status:         "complete_within_scope",
		Reasons:        []CoverageReason{},
		CollectedAt:    "2026-09-13T01:00:00Z",
		CollectorBuild: "codex-desktop@0.154.0 build=abc123",
		MappingIDs:     []string{"sha256:34a1189b7cf58e352f2dd4da8ea4e1076894a933bb9e3b1391082b3ccc2a2c7d"},
		Shards: []ShardRef{
			{
				ShardID:     "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				RecordCount: "2",
				InlineIDs: []string{
					"sha256:2222222222222222222222222222222222222222222222222222222222222222",
					"sha256:3333333333333333333333333333333333333333333333333333333333333333",
				},
			},
		},
		Predecessors: []string{},
		Counts: CoverageCounts{
			SourceCandidates: "2",
			RecordsEmitted:   "2",
			Observations:     "2",
			Conflicts:        "0",
			Gaps:             "0",
		},
	}

	sealedBytes, id, err := SealCoverage(c)
	if err != nil {
		t.Fatalf("SealCoverage failed: %v", err)
	}

	v := NewValidator(Allowlists{})
	env, err := v.ValidateEnvelope(sealedBytes)
	if err != nil {
		t.Fatalf("ValidateEnvelope refused sealed coverage: %v", err)
	}
	if env.Coverage == nil {
		t.Fatal("expected env.Coverage to be populated")
	}
	if env.ID != id {
		t.Errorf("env.ID %s != id %s", env.ID, id)
	}
	if env.Coverage.ScopeID != "nova.codex-desktop.responses" {
		t.Errorf("got scope_id %q", env.Coverage.ScopeID)
	}

	// Also test inventory_file branch
	invCID := "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	cInv := c
	cInv.Shards = []ShardRef{
		{
			ShardID:       "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			RecordCount:   "2",
			InventoryFile: &invCID,
		},
	}
	sealedInvBytes, invID, err := SealCoverage(cInv)
	if err != nil {
		t.Fatalf("SealCoverage with inventory_file failed: %v", err)
	}
	envInv, err := v.ValidateEnvelope(sealedInvBytes)
	if err != nil {
		t.Fatalf("ValidateEnvelope refused inventory_file coverage: %v", err)
	}
	if envInv.Coverage == nil || envInv.Coverage.Shards[0].InventoryFile == nil || *envInv.Coverage.Shards[0].InventoryFile != invCID {
		t.Errorf("unexpected envInv coverage: %+v", envInv.Coverage)
	}
	if envInv.ID != invID {
		t.Errorf("envInv.ID %s != invID %s", envInv.ID, invID)
	}
}

func TestCoverageAdversarialCases(t *testing.T) {
	validCov := Coverage{
		Schema:         SchemaCoverage,
		ScopeID:        "nova.test.responses",
		SourceIDs:      []string{"nova.test.responses"},
		Interval:       CoverageInterval{Start: "2026-09-12T00:00:00Z", End: "2026-09-13T00:00:00Z"},
		Status:         "complete_within_scope",
		Reasons:        []CoverageReason{},
		CollectedAt:    "2026-09-13T01:00:00Z",
		CollectorBuild: "test@1.0.0 build=123",
		MappingIDs:     []string{},
		Shards: []ShardRef{
			{
				ShardID:     "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				RecordCount: "1",
				InlineIDs:   []string{"sha256:2222222222222222222222222222222222222222222222222222222222222222"},
			},
		},
		Predecessors: []string{},
		Counts: CoverageCounts{
			SourceCandidates: "1",
			RecordsEmitted:   "1",
			Observations:     "1",
			Conflicts:        "0",
			Gaps:             "0",
		},
	}

	v := NewValidator(Allowlists{})

	// 1. Shard count mismatch
	t.Run("shard_count_mismatch", func(t *testing.T) {
		bad := validCov
		bad.Shards = []ShardRef{
			{
				ShardID:     "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				RecordCount: "2",
				InlineIDs:   []string{"sha256:2222222222222222222222222222222222222222222222222222222222222222"},
			},
		}
		body, _ := bad.Body()
		raw, _, _ := Seal(body)
		_, err := v.ValidateEnvelope(raw)
		if err == nil || !strings.Contains(err.Error(), "refused: shard_reference at body.shards[0].record_count") {
			t.Errorf("want shard_reference, got %v", err)
		}
	})

	// 2. Reason source not in source_ids
	t.Run("reason_source_not_in_scope", func(t *testing.T) {
		bad := validCov
		unlisted := "nova.unlisted.responses"
		bad.Reasons = []CoverageReason{
			{Code: "source_unavailable", Source: &unlisted},
		}
		bad.Counts.Gaps = "1"
		body, _ := bad.Body()
		raw, _, _ := Seal(body)
		_, err := v.ValidateEnvelope(raw)
		if err == nil || !strings.Contains(err.Error(), "refused: coverage_reason_source at body.reasons[0].source") {
			t.Errorf("want coverage_reason_source, got %v", err)
		}
	})

	// 3. Status unknown
	t.Run("status_unknown", func(t *testing.T) {
		bad := validCov
		bad.Status = "complete"
		body, _ := bad.Body()
		raw, _, _ := Seal(body)
		_, err := v.ValidateEnvelope(raw)
		if err == nil || !strings.Contains(err.Error(), "refused: unknown_enum at body.status") {
			t.Errorf("want unknown_enum, got %v", err)
		}
	})

	// 4. Friend underscore
	t.Run("friend_underscore", func(t *testing.T) {
		bad := validCov
		und := "_"
		bad.CollectorFriend = &und
		body, _ := bad.Body()
		raw, _, _ := Seal(body)
		_, err := v.ValidateEnvelope(raw)
		if err == nil || !strings.Contains(err.Error(), "refused: label_syntax at body.collector_friend") {
			t.Errorf("want label_syntax, got %v", err)
		}
	})

	// 5. Shard both inline and file
	t.Run("shard_both", func(t *testing.T) {
		inv := "sha256:3333333333333333333333333333333333333333333333333333333333333333"
		bad := validCov
		bad.Shards = []ShardRef{
			{
				ShardID:       "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				RecordCount:   "1",
				InlineIDs:     []string{"sha256:2222222222222222222222222222222222222222222222222222222222222222"},
				InventoryFile: &inv,
			},
		}
		body, _ := bad.Body()
		raw, _, _ := Seal(body)
		_, err := v.ValidateEnvelope(raw)
		if err == nil || !strings.Contains(err.Error(), "refused: shard_reference at body.shards[0]") {
			t.Errorf("want shard_reference, got %v", err)
		}
	})

	// 6. Gaps count mismatch
	t.Run("gaps_count_mismatch", func(t *testing.T) {
		bad := validCov
		bad.Counts.Gaps = "5" // reasons has 0
		body, _ := bad.Body()
		raw, _, _ := Seal(body)
		_, err := v.ValidateEnvelope(raw)
		if err == nil || !strings.Contains(err.Error(), "refused: integer_lexeme at body.counts.gaps") {
			t.Errorf("want integer_lexeme, got %v", err)
		}
	})
}
