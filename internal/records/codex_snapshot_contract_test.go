package records

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// This checks whether the proposed wire can carry the independently specified
// snapshots. It does not decode a source, resolve ancestry, or certify coverage.
func TestCodexSnapshotProposalWire(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "tokens", "codex-snapshots")
	allow := Allowlists{RawUsageFields: []string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens", "model_context_window"}, ReceiptFields: []string{"ordinal"}}
	v := NewValidator(allow)
	mappings := make(map[string]string)
	for _, group := range []string{"total", "last"} {
		raw, err := os.ReadFile(filepath.Join(dir, "mapping-"+group+".json"))
		if err != nil {
			t.Fatal(err)
		}
		env, err := v.ValidateEnvelope(raw)
		if err != nil {
			t.Fatal(err)
		}
		if env.Mapping == nil {
			t.Fatal("not a mapping")
		}
		m := env.Mapping
		if m.IdentityRule.NormalizedSpendSupported || m.IdentityRule.ObservationKind != "snapshot" {
			t.Fatal("snapshot must remain raw-only")
		}
		if m.IdentityRule.Namespace != "nova.codex-desktop.token-count."+group {
			t.Fatal("wrong snapshot domain")
		}
		if len(m.FieldRules) != len(allow.RawUsageFields) {
			t.Fatal("wrong field count")
		}
		for _, key := range allow.RawUsageFields {
			rule, ok := m.FieldRules[key]
			if !ok || rule.SpendRole != "non_spend" || rule.ZeroSemantics != "unknown" {
				t.Fatalf("unsafe snapshot semantics for %s", key)
			}
		}
		sealed, id, err := SealMapping(*m)
		if err != nil || id != env.ID || !bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(sealed)) {
			t.Fatalf("mapping roundtrip: %v id=%s want=%s", err, id, env.ID)
		}
		for name, digest := range m.FixtureDigests {
			source, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("sha256:%x", sha256.Sum256(source)) != digest {
				t.Fatalf("changed source fixture %s", name)
			}
		}
		mappings[env.ID] = m.IdentityRule.Namespace
	}
	raw, err := os.ReadFile(filepath.Join(dir, "expected-observations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 8 {
		t.Fatalf("want 8 observations, got %d", len(lines))
	}
	counts := make(map[string]int)
	seen := make(map[string]bool)
	for _, line := range lines {
		env, err := v.ValidateEnvelope(line)
		if err != nil {
			t.Fatal(err)
		}
		if env.Observation == nil {
			t.Fatal("not an observation")
		}
		o := env.Observation
		ns, ok := mappings[o.MappingID]
		if !ok || o.Source.Namespace != ns {
			t.Fatal("mapping reference/domain mismatch")
		}
		if seen[env.ID] {
			t.Fatal("distinct native ordinals were collapsed")
		}
		seen[env.ID] = true
		if o.Kind != "snapshot" || o.Model.ID != nil || o.Model.Basis != "unknown" || len(o.ModelUsage) != 0 {
			t.Fatal("invented snapshot attribution")
		}
		if len(o.Source.EventKey) != 2 || o.Source.EventKey[1] != o.Receipt["ordinal"] {
			t.Fatal("lost native locator")
		}
		counts[ns]++
		sealed, id, err := v.SealObservation(*o)
		if err != nil || id != env.ID || !bytes.Equal(bytes.TrimSpace(line), bytes.TrimSpace(sealed)) {
			t.Fatalf("observation roundtrip: %v", err)
		}
	}
	for _, ns := range mappings {
		if counts[ns] != 4 {
			t.Fatal("missing snapshot group")
		}
	}
}
