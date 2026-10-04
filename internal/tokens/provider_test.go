package tokens

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestXaiUsageJSONShapeParses pins the second accepted shape for --provider xai: a
// `grok usage` export is JSON, not CSV, and the parser folds its turns into the same rows
// the CSV shape produces. The fixture is a synthetic, sanitized turn.
func TestXaiUsageJSONShapeParses(t *testing.T) {
	t.Parallel()

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
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))
	s := ReadProvider("xai", "studio", path, nil)
	require.EqualValuesf(t, 0, s.Stat.Unreadable, "the JSON shape is unreadable: %+v", s.Unreadables)
	require.Lenf(t, s.Stream, 1, "want 1 row, got %d", len(s.Stream))
	m := s.Stream[0]
	assert.EqualValuesf(t, "grok-model-example", m.Model, "model = %q, want grok-model-example", m.Model)
	assert.EqualValuesf(t, "2026-09-12", m.Day, "day = %q, want 2026-09-12", m.Day)
	assert.EqualValuesf(t, UTC, m.Basis, "basis = %q, want utc", m.Basis)
	assert.EqualValuesf(t, Unattributed, m.Repo, "repo = %q, want unattributed", m.Repo)
	want := map[Type]int64{Input: 1000, Output: 100, CacheWrite: 0, CacheRead: 800, Reasoning: 40}
	for typ, v := range want {
		got, ok := m.Counts.Get(typ)
		if !assert.Truef(t, ok, "missing count for %s", TypeNames[typ]) {
			continue
		}
		assert.EqualValuesf(t, v, got, "%s = %d, want %d", TypeNames[typ], got, v)
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
	t.Parallel()

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
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))
	s := ReadProvider("xai", "studio", path, nil)
	require.EqualValuesf(t, 0, s.Stat.Unreadable, "the JSON shape is unreadable: %+v", s.Unreadables)
	require.Lenf(t, s.Stream, 1, "want 1 row, got %d", len(s.Stream))
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
	require.Lenf(t, gotPresent, len(want), "present types = %v, want %v (a missing key must not count as zero)", gotPresent, want)
	for i, name := range want {
		assert.EqualValuesf(t, name, gotPresent[i], "present[%d] = %q, want %q (a missing key must not count as zero)", i, gotPresent[i], name)
	}

	{
		_, ok := m.Counts.Get(CacheWrite)
		assert.False(t, ok, "cache_write reported as present, but the turn did not carry cacheCreationTokens")
	}
	{
		_, ok := m.Counts.Get(Reasoning)
		assert.False(t, ok, "reasoning reported as present, but the turn did not carry reasoningTokens")
	}

	{
		got, ok := m.Counts.Get(Input)
		assert.Falsef(t, !ok || got != 1000, "input = (%d, %v), want (1000, true)", got, ok)
	}
	{
		got, ok := m.Counts.Get(Output)
		assert.Falsef(t, !ok || got != 100, "output = (%d, %v), want (100, true)", got, ok)
	}
	{
		got, ok := m.Counts.Get(CacheRead)
		assert.Falsef(t, !ok || got != 800, "cache_read = (%d, %v), want (800, true)", got, ok)
	}
}
