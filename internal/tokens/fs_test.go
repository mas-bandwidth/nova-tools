package tokens

// The readers take an fs.FS, so each one's folding and counting is pinned over an
// in-memory tree (fstest.MapFS) with no temporary directory, and the temp-dir cover tests
// beside them keep proving the real walk (symlinks, permissions).

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadClaudeFoldsOverAnFS: the transcript tree is read through the fs.FS the caller
// hands in, and the fold -- sorted paths, the last line of a streamed id wins, a file of
// another suffix ignored -- is the same one the temp-dir walk produces.
func TestReadClaudeFoldsOverAnFS(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"session.jsonl": &fstest.MapFile{Data: []byte(
			`{"timestamp":"2026-09-16T09:00:00Z","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":2}}}` + "\n" +
				`{"timestamp":"2026-09-16T09:01:00Z","message":{"id":"m1","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":7}}}` + "\n")},
		"nested/other.output": &fstest.MapFile{Data: []byte(
			`{"timestamp":"2026-09-16T10:00:00Z","message":{"id":"o1","model":"claude-opus-5","usage":{"input_tokens":3,"output_tokens":1}}}` + "\n")},
		"notes.txt": &fstest.MapFile{Data: []byte("not a transcript")},
	}

	s := ReadClaude("bench", "transcripts", fsys, claudeCoverRules(t))

	assert.Equalf(t, "transcripts", s.Path, "path=%q, want the caller's directory, not the fs", s.Path)
	assert.Equalf(t, 2, s.Stat.Files, "files=%d, want 2: the two transcripts, not the .txt", s.Stat.Files)
	assert.Equalf(t, 2, s.Stat.Messages, "messages=%d, want 2: m1 and o1", s.Stat.Messages)
	assert.Equalf(t, 1, s.Stat.Dup, "dup=%d, want 1: m1 is a streamed id", s.Stat.Dup)
	assert.Equalf(t, "7", claudeCoverMessage(s.Stream, "m1").Counts.Cell(Output), "m1 output, want 7: the last line for an id is the message")
	assert.Equalf(t, "1", claudeCoverMessage(s.Stream, "o1").Counts.Cell(Output), "o1 output, want 1: the nested .output folds")
}

// TestReadSwarmFoldsOverAnFS: the usage pool is read through the fs.FS the caller hands
// in, its rows fold through the named columns, and a done/ job with no usage file is
// counted by name alone.
func TestReadSwarmFoldsOverAnFS(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"usage/a.tsv": &fstest.MapFile{Data: []byte(swarmCoverHeader() + "\n" +
			swarmCoverRow("job1", "1", "", "2026-09-16T07:00:00Z", "2026-09-16T08:00:00Z", "", "0", "anthropic", "claude-x", "/w/schema", "10", "2", "5", "7", "-", "-") + "\n")},
		"done/orphan": &fstest.MapFile{Mode: fs.ModeDir},
	}

	s := ReadSwarm("bench", "pool", fsys, swarmCoverRules(t))

	assert.Equalf(t, "pool", s.Path, "path=%q, want the caller's pool", s.Path)
	assert.Equalf(t, 1, s.Stat.Files, "files=%d, want 1: the one usage file", s.Stat.Files)
	assert.Equalf(t, 1, s.Stat.Messages, "messages=%d, want 1", s.Stat.Messages)
	assert.Equalf(t, 1, s.Stat.NoUsage, "nousage=%d, want 1: done/orphan has no usage file", s.Stat.NoUsage)
	job1 := swarmCoverMessage(s.Stream, "claude-x")
	assert.Equalf(t, "10", job1.Counts.Cell(Input), "input=%s, want 10", job1.Counts.Cell(Input))
	assert.Equalf(t, "schema", job1.Repo, "repo=%q, want schema: the rules file named the path", job1.Repo)
}
