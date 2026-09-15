package tokens

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// The snapshot decoder's tests, over the synthetic fixtures of proposal #154 and nothing
// else. The fixtures are invented; no real Codex session store is opened. The mapping
// decisions are the source owner's, tabulated in docs/PROPOSAL-TOKENS-CODEX-SNAPSHOTS.md
// and made executable by testdata/tokens/codex-snapshots/{mapping-total,mapping-last}.json.

func codexSnapshotDir() string {
	return filepath.Join("..", "..", "testdata", "tokens", "codex-snapshots")
}

func codexSnapshotMappings(t *testing.T) (*CodexMapping, *CodexMapping) {
	t.Helper()
	total, err := ReadCodexSnapshotMapping(filepath.Join(codexSnapshotDir(), "mapping-total.json"))
	if err != nil {
		t.Fatalf("the sealed total snapshot manifest: %v", err)
	}
	last, err := ReadCodexSnapshotMapping(filepath.Join(codexSnapshotDir(), "mapping-last.json"))
	if err != nil {
		t.Fatalf("the sealed last snapshot manifest: %v", err)
	}
	return total, last
}

func codexSnapshotA(t *testing.T, total, last *CodexMapping) *CodexSnapshotDecoding {
	t.Helper()
	d, err := DecodeCodexSnapshotRollout(total, last, []CodexSnapshotBinding{
		{OriginalRolloutID: "01990000-0000-7000-8000-000000000001", Origin: CodexBinding{ID: "fixture-origin-a", Friend: "ada", Bench: "bench-a"}},
	}, []string{filepath.Join(codexSnapshotDir(), "source-a.jsonl")})
	if err != nil {
		t.Fatalf("snapshot decode: %v", err)
	}
	return d
}

func codexSnapshotExpectedLines(t *testing.T) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(codexSnapshotDir(), "expected-observations.jsonl"))
	if err != nil {
		t.Fatalf("read expected observations: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 8 {
		t.Fatalf("want 8 expected observations, got %d", len(lines))
	}
	return lines
}

func sortSnapshotBytes(ls [][]byte) {
	sort.Slice(ls, func(i, j int) bool { return bytes.Compare(ls[i], ls[j]) < 0 })
}

// TestCodexCumulativeTokenCountMappingOwedNeverCovered is the owed mapping from issue #154:
// the cumulative token_count snapshot is no longer mapped to nothing. One token_count event
// with an object-valued info produces one observation per group (total and last), each in
// its own namespace, raw and never counted as spend. The decoder's sealed envelopes match
// the proposal's independently stated envelopes byte for byte.
func TestCodexCumulativeTokenCountMappingOwedNeverCovered(t *testing.T) {
	total, last := codexSnapshotMappings(t)
	d := codexSnapshotA(t, total, last)

	if len(d.Observations) != 4 {
		t.Fatalf("source-a produced %d observations, want 4 (two ordinals, two groups each)", len(d.Observations))
	}

	want := codexSnapshotExpectedLines(t)[:4]
	have := make([][]byte, 0, len(d.Observations))
	for _, o := range d.Observations {
		have = append(have, o.Envelope)
	}
	sortSnapshotBytes(have)
	sortSnapshotBytes(want)
	for i := range want {
		if !bytes.Equal(have[i], want[i]) {
			hid, _, _ := codexSealed(have[i])
			wid, _, _ := codexSealed(want[i])
			t.Fatalf("sealed snapshot envelope %d differs from the proposal: decoder %s, proposal %s", i, hid, wid)
		}
	}
}

// TestCodexCumulativeTokenCountMappingRoundTrips: the snapshot envelope bytes are not merely
// equal to a file but accepted by the record validator under each mapping's own allowlists,
// with the ID it derives. Both groups are raw-only: no model is invented, there is no
// per-model split, and every field of the closed seven-field allowlist is present.
func TestCodexCumulativeTokenCountMappingRoundTrips(t *testing.T) {
	total, last := codexSnapshotMappings(t)
	d := codexSnapshotA(t, total, last)
	vTotal := records.NewValidator(total.Allowlists())
	vLast := records.NewValidator(last.Allowlists())
	namespaces := map[string]int{}
	for _, o := range d.Observations {
		v := vTotal
		if strings.HasPrefix(o.SpendKey, last.Namespace) {
			v = vLast
		}
		env, err := v.ValidateEnvelope(o.Envelope)
		if err != nil {
			t.Fatalf("roundtrip: %v", err)
		}
		if env.ID != o.ID {
			t.Errorf("the decoder reports %s, the validator derives %s", o.ID, env.ID)
		}
		if env.Observation.Kind != "snapshot" {
			t.Errorf("a snapshot observation is kind snapshot, got %s", env.Observation.Kind)
		}
		if env.Observation.Model.ID != nil || env.Observation.Model.Basis != "unknown" {
			t.Errorf("a snapshot invents no model: %+v", env.Observation.Model)
		}
		if len(env.Observation.ModelUsage) != 0 {
			t.Errorf("a snapshot invents no per-model split")
		}
		if len(env.Observation.RawUsage) != len(total.Fields) {
			t.Errorf("raw_usage has %d entries, the snapshot allowlist has %d", len(env.Observation.RawUsage), len(total.Fields))
		}
		namespaces[env.Observation.Source.Namespace]++
	}
	if namespaces[total.Namespace] != 2 || namespaces[last.Namespace] != 2 {
		t.Errorf("the two groups are not split across the two namespaces: %v", namespaces)
	}
}
