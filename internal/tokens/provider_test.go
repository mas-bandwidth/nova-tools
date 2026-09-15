package tokens

import (
	"os"
	"path/filepath"
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
