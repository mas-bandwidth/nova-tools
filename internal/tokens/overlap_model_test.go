package tokens

// TokenFold invariants, over the in-memory fake (fstest.MapFS) and the fold.
// The module is TokenFold: Naturals, Sequences, FiniteSets, TLC. TLC is not run
// here; each test is named for the invariant it holds.

import (
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func overlapModelLine(id string, input int) string {
	return `{"timestamp":"2026-09-11T10:00:00Z","message":{"id":"` + id + `","model":"fable","usage":{"input_tokens":` + strconv.Itoa(input) + `},"content":[{"type":"tool_use","input":{"file_path":"/x/schema/a.go"}}]}}` + "\n"
}

func overlapModelFold(t *testing.T, label, body string, rules *Rules, f *Folder) {
	t.Helper()
	fsys := fstest.MapFS{"a.jsonl": &fstest.MapFile{Data: []byte(body)}}
	s := ReadClaude(label, "transcripts", fsys, rules)
	for _, m := range s.Stream {
		f.Add(s.Label, m)
	}
}

// TestOverlapRefusedBeforeWrite pins TokenFold invariant OverlapRefusedBeforeWrite:
// two sources of one provider that share ids are a refusal, and neither message is dropped.
func TestOverlapRefusedBeforeWrite(t *testing.T) {
	t.Parallel()

	rules := claudeCoverRules(t)
	f := NewFolder()
	body := overlapModelLine("m1", 100) + overlapModelLine("m2", 1)
	overlapModelFold(t, "bench", body, rules, f)
	overlapModelFold(t, "copy", body, rules, f)

	require.True(t, f.RefuseWrite(), "a detected overlap is a refusal before the write")
	overs := f.Overlaps()
	require.Len(t, overs, 1)
	assert.Equal(t, "claude:bench", overs[0].A)
	assert.Equal(t, "claude:copy", overs[0].B)
	assert.Equal(t, 2, overs[0].IDs, "the duplicate count is the shared ids, not a drop")
	row := f.rows[Key{Day: "2026-09-11", Model: "fable", Repo: "schema"}]
	require.NotNil(t, row)
	got, ok := row.Counts.Get(Input)
	require.True(t, ok)
	assert.Equal(t, int64(202), got, "both sources' messages stay; the refusal is not a de-duplication")
}

// TestDisjointSourcesFold pins TokenFold invariant DisjointSourcesFold: two sources
// of one provider that share no id fold, and the write is not refused.
func TestDisjointSourcesFold(t *testing.T) {
	t.Parallel()

	rules := claudeCoverRules(t)
	f := NewFolder()
	overlapModelFold(t, "bench", overlapModelLine("m1", 100), rules, f)
	overlapModelFold(t, "copy", overlapModelLine("m2", 40), rules, f)

	assert.False(t, f.RefuseWrite())
	assert.Empty(t, f.Overlaps())
	rows, mixed := f.DayRows("2026-09-11")
	assert.Empty(t, mixed)
	require.Len(t, rows, 1)
	got, ok := rows[0].Counts.Get(Input)
	require.True(t, ok)
	assert.Equal(t, int64(140), got)
	assert.Equal(t, []string{"claude:bench", "claude:copy"}, rows[0].Sources())
}

// TestUnscopedIDNotDeduped pins TokenFold invariant UnscopedIDNotDeduped: an id
// shared by two providers is not an overlap and is not dropped.
func TestUnscopedIDNotDeduped(t *testing.T) {
	t.Parallel()

	f := NewFolder()
	m := Message{ID: "m1", Day: "2026-09-11", Basis: UTC, Model: "fable", Repo: "schema"}
	m.Counts.Set(Input, 100)
	f.Add("claude:bench", m)
	f.Add("opencode:extra", m)

	assert.False(t, f.RefuseWrite(), "an id that is not scoped to one provider is not a refusal")
	assert.Empty(t, f.Overlaps())
	row := f.rows[Key{Day: "2026-09-11", Model: "fable", Repo: "schema"}]
	require.NotNil(t, row)
	got, ok := row.Counts.Get(Input)
	require.True(t, ok)
	assert.Equal(t, int64(200), got, "the foreign id is not a drop key")
	assert.Equal(t, []string{"claude:bench", "opencode:extra"}, row.Sources())
}
