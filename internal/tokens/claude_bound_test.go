package tokens

// The bound on a recursive transcript tree: the whole tree is walked and counted before
// one file is opened, an over-ceiling tree is refused with the totals it found, and an
// --exclude glob is what keeps a known temporary tree out of the walk. Both are pinned
// over an in-memory fs.FS, so the rule is the reader's and not a temporary directory's.

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeBoundLine is one transcript line, valid for the day and model columns and with the
// id the file name carries, so a folded message names the file it came from.
func claudeBoundLine(id, stamp, model string, in, out int) string {
	return fmt.Sprintf(`{"timestamp":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":%d}}}`+"\n", stamp, id, model, in, out)
}

// TestReadClaudeRefusesATreeOverItsFileCeiling: the walk counts every transcript file
// before the first open, so a tree over MaxFiles yields one unreadable that names the file
// and byte totals and the ceiling, and no file is read (files=0, no stream).
func TestReadClaudeRefusesATreeOverItsFileCeiling(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"a.jsonl":        &fstest.MapFile{Data: []byte(claudeBoundLine("a", "2026-09-16T09:00:00Z", "claude-opus-5", 1, 2))},
		"b.output":       &fstest.MapFile{Data: []byte(claudeBoundLine("b", "2026-09-16T09:01:00Z", "claude-opus-5", 3, 4))},
		"deeper/c.jsonl": &fstest.MapFile{Data: []byte(claudeBoundLine("c", "2026-09-16T09:02:00Z", "claude-opus-5", 5, 6))},
	}

	s := ReadClaude("bench", "transcripts", fsys, claudeCoverRules(t), ClaudeBound{MaxFiles: 2})

	require.Lenf(t, s.Unreadables, 1, "the over-ceiling tree is one refusal")
	assert.Equalf(t, "transcripts", s.Unreadables[0].Path, "path=%q, want the tree the refusal names", s.Unreadables[0].Path)
	assert.Contains(t, s.Unreadables[0].Why, "3 transcript files", "the refusal names the files it found")
	assert.Contains(t, s.Unreadables[0].Why, "over the --max-files ceiling of 2", "the refusal names the ceiling it crossed")
	assert.Contains(t, s.Unreadables[0].Why, "--exclude", "the refusal names the remedy that keeps a temporary tree out")
	assert.Zerof(t, s.Stat.Files, "files=%d, want 0: the tree is refused before any file is opened", s.Stat.Files)
	assert.Zero(t, s.Stat.Messages, "messages=%d, want 0: nothing was read", s.Stat.Messages)
	assert.Empty(t, s.Stream, "stream=%d, want 0: nothing was read", len(s.Stream))
}

// TestReadClaudeAdmitsATreeAtItsFileCeiling: the ceiling admits a tree exactly at it, so
// the count that refuses is one over and not one at.
func TestReadClaudeAdmitsATreeAtItsFileCeiling(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"a.jsonl": &fstest.MapFile{Data: []byte(claudeBoundLine("a", "2026-09-16T09:00:00Z", "claude-opus-5", 1, 2))},
		"b.jsonl": &fstest.MapFile{Data: []byte(claudeBoundLine("b", "2026-09-16T09:01:00Z", "claude-opus-5", 3, 4))},
	}

	s := ReadClaude("bench", "transcripts", fsys, claudeCoverRules(t), ClaudeBound{MaxFiles: 2})

	assert.Empty(t, s.Unreadables, "a tree at its ceiling is read whole")
	assert.Equalf(t, 2, s.Stat.Files, "files=%d, want 2", s.Stat.Files)
	assert.Equalf(t, 2, s.Stat.Messages, "messages=%d, want 2", s.Stat.Messages)
}

// TestReadClaudeExcludesATemporaryTree: an --exclude glob keeps a directory and its whole
// subtree out of the walk, so the same tree that was refused is admitted under the same
// ceiling, and the excluded tree contributes no file and no message.
func TestReadClaudeExcludesATemporaryTree(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"good.jsonl":        &fstest.MapFile{Data: []byte(claudeBoundLine("good", "2026-09-16T09:00:00Z", "claude-opus-5", 1, 2))},
		"tmp/scratch.jsonl": &fstest.MapFile{Data: []byte(claudeBoundLine("scratch", "2026-09-16T09:01:00Z", "claude-opus-5", 3, 4))},
	}

	over := ReadClaude("bench", "transcripts", fsys, claudeCoverRules(t), ClaudeBound{MaxFiles: 1})
	require.Lenf(t, over.Unreadables, 1, "without --exclude the temporary tree puts the tree over the ceiling")

	s := ReadClaude("bench", "transcripts", fsys, claudeCoverRules(t), ClaudeBound{MaxFiles: 1, Exclude: []string{"tmp"}})

	assert.Empty(t, s.Unreadables, "the excluded tree leaves the source under its ceiling")
	assert.Equalf(t, 1, s.Stat.Files, "files=%d, want 1: the excluded file is never counted", s.Stat.Files)
	assert.Equalf(t, 1, s.Stat.Messages, "messages=%d, want 1", s.Stat.Messages)
	require.Lenf(t, s.Stream, 1, "stream=%d, want 1", len(s.Stream))
	assert.Equalf(t, "good", s.Stream[0].ID, "id=%q, want the file outside the excluded tree", s.Stream[0].ID)
}

// TestExcludedByIsTheFamilyRule: the --exclude rule is the one nova-memory keeps -- an
// exact path, a path under a matched directory, or a path.Match glob -- so the same flag
// means the same thing in every tool.
func TestExcludedByIsTheFamilyRule(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		globs    []string
		path     string
		excluded bool
	}{
		{"an exact path", []string{"tmp"}, "tmp", true},
		{"a path under a matched directory", []string{"tmp"}, "tmp/scratch.jsonl", true},
		{"a deeper path under a matched directory", []string{"tmp"}, "tmp/a/b.jsonl", true},
		{"a path.Match glob", []string{"*/tmp"}, "a/tmp", true},
		{"a neighbour is kept", []string{"tmp"}, "tmpx/scratch.jsonl", false},
		{"no glob excludes nothing", nil, "tmp/scratch.jsonl", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equalf(t, tc.excluded, excludedBy(tc.globs, tc.path), "excludedBy(%v, %q)", tc.globs, tc.path)
		})
	}
}
