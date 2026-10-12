//go:build unix

package tokens

// Witnesses for the three causes first blamed for the Studio's nightly fold going stale
// after 2026-10-03 (com.rowan.token-collate). None of the three was the cause -- the job
// never started (launchd exit 78, EX_CONFIG, no byte written to its log) -- and these
// pin the shapes found on the Studio so a later change cannot turn any of them into one:
//
//   - a 0-byte fold.lock left beside the day files by the last run that finished;
//   - a --claude root that is a link (~/.claude/projects/-Volumes-nova-ai-rowan-working-rowan-new
//     -> -Users-glenn-rowan-working-rowan-new, made when the working tree moved to /Volumes);
//   - a transcript line that is not JSON, among transcripts that are.
//
// Each has its reversed witness: the state the rule must not be confused with. No test
// here waits: every lock is taken with a zero bound, so no clock is read for a sleep.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// witnessTurn is one assistant turn with a usage block, stamped on 2026-10-12 UTC.
func witnessTurn(id string) string {
	return `{"type":"assistant","timestamp":"2026-10-12T12:00:00Z","cwd":"/w/schema","message":{"id":"` + id +
		`","model":"claude-opus-5-5","usage":{"input_tokens":3,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens":11}}}` + "\n"
}

// TestALeftoverFoldLockIsTakenAtOnce: the lock is a kernel lock on the file, not the
// file's existence, so a fold.lock left behind -- empty, as on the Studio, or still
// holding a finished run's pid -- is taken by the next fold with no wait and is kept, not
// deleted. Reversed: while a fold really holds it, the next fold is refused and the
// refusal names the lock.
func TestALeftoverFoldLockIsTakenAtOnce(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{"an empty lock file, as the last finished run left it", ""},
		{"a lock file naming a run that is gone", "2147483646\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, LockName)
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o644))

			release, err := TakeFoldLock(dir, 0)
			require.NoErrorf(t, err, "a lock file nobody holds refused the fold: %v", err)
			release()

			_, statErr := os.Stat(path)
			assert.NoErrorf(t, statErr, "the lock file was removed; the lock is never a delete")
		})
	}

	t.Run("reversed: a held lock is refused and named", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		release, err := TakeFoldLock(dir, 0)
		require.NoError(t, err)
		defer release()

		_, err = TakeFoldLock(dir, 0)
		require.Error(t, err, "a second fold took a lock that is held")
		assert.Truef(t, strings.Contains(err.Error(), LockName), "the refusal does not name the lock: %v", err)
	})
}

// TestReadClaudeReadsARootThatIsALink: a --claude directory given through a link is read
// through it, because the fold reads the tree through os.DirFS, whose root is opened, not
// lstat'ed. Reversed: a walk that lstats its root (filepath.WalkDir) sees the link as one
// non-directory entry and finds no transcript at all -- the silent zero this rule rules out.
func TestReadClaudeReadsARootThatIsALink(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	real := filepath.Join(base, "-Users-glenn-rowan-working-rowan-new")
	claudeCoverTranscript(t, real, "s1.jsonl", witnessTurn("m1"))
	claudeCoverTranscript(t, real, "s1/subagents/a1.jsonl", witnessTurn("m2"))
	link := filepath.Join(base, "-Volumes-nova-ai-rowan-working-rowan-new")
	require.NoError(t, os.Symlink(real, link))

	s := ReadClaude("linked", link, os.DirFS(link), claudeCoverRules(t))
	assert.Emptyf(t, s.Unreadables, "the linked root was not read: %+v", s.Unreadables)
	assert.Equal(t, 2, s.Stat.Files, "every transcript under the link is read")
	assert.Equal(t, int64(3), witnessInput(claudeCoverMessage(s.Stream, "m1")), "the turn behind the link is folded")

	t.Run("reversed: an lstat walk of the link finds nothing", func(t *testing.T) {
		found := 0
		require.NoError(t, filepath.WalkDir(link, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
				found++
			}
			return err
		}))
		assert.Equal(t, 0, found, "filepath.WalkDir followed the root link; the witness no longer shows why DirFS is used")
	})
}

// TestABadTranscriptLineIsNamedAndTheRestIsFolded: a line that is not JSON makes its file
// one unreadable naming the file and the count, and every good line -- in that file and
// in the others -- is still folded. It does not stop the fold; the exit 1 it causes is
// the claim (help: EXIT 1 STILL WRITES). Reversed: a tree with no bad line has no
// unreadable, so the unreadable is the bad line's and nothing else's.
func TestABadTranscriptLineIsNamedAndTheRestIsFolded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	claudeCoverTranscript(t, dir, "good.jsonl", witnessTurn("g1"))
	bad := claudeCoverTranscript(t, dir, "bad.jsonl", witnessTurn("b1")+`{"type":"assistant","message":`+"\n")

	s := ReadClaude("mixed", dir, os.DirFS(dir), claudeCoverRules(t))
	require.Len(t, s.Unreadables, 1, "exactly the bad file is unreadable")
	assert.Equal(t, bad, s.Unreadables[0].Path, "the unreadable names the file")
	assert.Contains(t, s.Unreadables[0].Why, "badline=1", "the unreadable counts the bad line")
	assert.Equal(t, int64(3), witnessInput(claudeCoverMessage(s.Stream, "g1")), "the good file is folded")
	assert.Equal(t, int64(3), witnessInput(claudeCoverMessage(s.Stream, "b1")), "the good line of the bad file is folded")

	t.Run("reversed: a clean tree has no unreadable", func(t *testing.T) {
		clean := t.TempDir()
		claudeCoverTranscript(t, clean, "good.jsonl", witnessTurn("g1"))
		s := ReadClaude("clean", clean, os.DirFS(clean), claudeCoverRules(t))
		assert.Empty(t, s.Unreadables)
		assert.Equal(t, 1, s.Stat.Files)
	})
}

// witnessInput is a folded turn's input count; zero when the turn or the count is absent.
func witnessInput(m Message) int64 {
	v, _ := m.Counts.Get(Input)
	return v
}
