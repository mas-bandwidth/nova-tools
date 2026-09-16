package tokens

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestXaiUsageJSONShapeParses pins the second accepted shape for --provider xai: a
// `grok usage` export is JSON, not CSV, and the parser folds its turns into the same rows
// the CSV shape produces. The fixture is the sanitized turn from docs/MAPPING-TOKENS-GROK.md.
func TestXaiUsageJSONShapeParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grok-usage.json")
	raw := `{
  "sessionId": "fixture-grok-session",
  "updatedAt": "2026-09-12T00:06:00Z",
  "session": {},
  "turns": [
    {
      "turnNumber": 1,
      "endedAt": "2026-09-12T00:05:00Z",
      "inputTokens": 1000,
      "outputTokens": 100,
      "cachedReadTokens": 800,
      "cacheCreationTokens": 0,
      "reasoningTokens": 40,
      "totalTokens": 1100,
      "modelCalls": 2,
      "costUsdTicks": 77,
      "turnCount": 1,
      "primaryModelId": "grok-model-example",
      "modelUsage": {
        "grok-model-example": {
          "inputTokens": 1000,
          "outputTokens": 100,
          "cachedReadTokens": 800,
          "cacheCreationTokens": 0,
          "reasoningTokens": 40,
          "totalTokens": 1100,
          "modelCalls": 2,
          "costUsdTicks": 77
        }
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := ReadProvider("xai", "studio", path, nil)
	if s.Stat.Unreadable != 0 {
		t.Fatalf("the JSON shape is unreadable: %+v", s.Unreadables)
	}
	if len(s.Stream) != 1 {
		t.Fatalf("want 1 row, got %d", len(s.Stream))
	}
	m := s.Stream[0]
	if m.Model != "grok-model-example" {
		t.Errorf("model = %q, want grok-model-example", m.Model)
	}
	if m.Day != "2026-09-12" {
		t.Errorf("day = %q, want 2026-09-12", m.Day)
	}
	if m.Basis != UTC {
		t.Errorf("basis = %q, want utc", m.Basis)
	}
	if m.Repo != Unattributed {
		t.Errorf("repo = %q, want unattributed", m.Repo)
	}
	want := map[Type]int64{Input: 1000, Output: 100, CacheWrite: 0, CacheRead: 800, Reasoning: 40}
	for typ, v := range want {
		got, ok := m.Counts.Get(typ)
		if !ok {
			t.Errorf("missing count for %s", TypeNames[typ])
			continue
		}
		if got != v {
			t.Errorf("%s = %d, want %d", TypeNames[typ], got, v)
		}
	}
}

// TestXaiUsageJSONShapeParsesMissingKeysNotZero pins the issue's exact rule:
// a field the turn did not carry is absent, not zero. The fixture omits
// `cacheCreationTokens` and `reasoningTokens`; the parser must report 3 of
// the 5 token types (every present field is a Measure; every absent field
// has Counts.has[type] == false), and CacheWrite / Reasoning must NOT be
// counted as 0 -- which a refactor that folds "missing is zero" would.
// Without this guard a future change could re-introduce the failure mode
// nova-tools #450 was filed about.
func TestXaiUsageJSONShapeParsesMissingKeysNotZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grok-usage-sparse.json")
	raw := `{
  "sessionId": "fixture-grok-sparse",
  "turns": [
    {
      "turnNumber": 1,
      "endedAt": "2026-09-13T00:05:00Z",
      "inputTokens": 1000,
      "outputTokens": 100,
      "cachedReadTokens": 800,
      "primaryModelId": "grok-model-example"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := ReadProvider("xai", "studio", path, nil)
	if s.Stat.Unreadable != 0 {
		t.Fatalf("the JSON shape is unreadable: %+v", s.Unreadables)
	}
	if len(s.Stream) != 1 {
		t.Fatalf("want 1 row, got %d", len(s.Stream))
	}
	m := s.Stream[0]

	gotPresent := []string{}
	for _, t2 := range []Type{Input, Output, CacheWrite, CacheRead, Reasoning} {
		if _, ok := m.Counts.Get(t2); ok {
			gotPresent = append(gotPresent, TypeNames[t2])
		}
	}
	sort.Strings(gotPresent)
	want := []string{"cache_read", "input", "output"}
	sort.Strings(want)
	if len(gotPresent) != len(want) {
		t.Fatalf("present types = %v, want %v (a missing key must not count as zero)", gotPresent, want)
	}
	for i, name := range want {
		if gotPresent[i] != name {
			t.Errorf("present[%d] = %q, want %q (a missing key must not count as zero)", i, gotPresent[i], name)
		}
	}

	if _, ok := m.Counts.Get(CacheWrite); ok {
		t.Error("cache_write reported as present, but the turn did not carry cacheCreationTokens")
	}
	if _, ok := m.Counts.Get(Reasoning); ok {
		t.Error("reasoning reported as present, but the turn did not carry reasoningTokens")
	}

	if got, ok := m.Counts.Get(Input); !ok || got != 1000 {
		t.Errorf("input = (%d, %v), want (1000, true)", got, ok)
	}
	if got, ok := m.Counts.Get(Output); !ok || got != 100 {
		t.Errorf("output = (%d, %v), want (100, true)", got, ok)
	}
	if got, ok := m.Counts.Get(CacheRead); !ok || got != 800 {
		t.Errorf("cache_read = (%d, %v), want (800, true)", got, ok)
	}
}
