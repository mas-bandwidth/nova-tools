package sprint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitTestRepo is a git repository with a bare origin and a working checkout.
type gitTestRepo struct {
	t            *testing.T
	origin, work string
}

func newTestGitRepo(t *testing.T) *gitTestRepo {
	t.Helper()
	root := t.TempDir()
	g := &gitTestRepo{t: t, origin: filepath.Join(root, "origin.git"), work: filepath.Join(root, "work")}
	g.git(root, "init", "-q", "--bare", "-b", "main", g.origin)
	g.git(root, "clone", "-q", g.origin, g.work)
	require.NoError(t, os.WriteFile(filepath.Join(g.work, "README"), []byte("base\n"), 0o644))
	g.git(g.work, "add", "README")
	g.git(g.work, "commit", "-q", "-m", "base")
	g.git(g.work, "push", "-q", "origin", "HEAD:refs/heads/main")
	return g
}

func (g *gitTestRepo) run(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(g.t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (g *gitTestRepo) git(dir string, args ...string) string {
	g.t.Helper()
	out, err := g.run(dir, args...)
	require.NoError(g.t, err, "git %s: %s", strings.Join(args, " "), out)
	return out
}

// TestStreamsResumeWhenTheBaseGatePassesAgain verifies that:
//  1. The base tree gate failing stops all landing with ONE judgment naming the failing test.
//  2. Each land pass re-checks the base tip, and when it passes, every stream stopped only
//     by the red base resumes by rule (RuleBaseGate), recorded in the log.
//  3. Streams stopped for other causes (such as conflict) remain stopped.
//  4. Turning the rule off stops auto-resumption.
func TestStreamsResumeWhenTheBaseGatePassesAgain(t *testing.T) {
	t.Parallel()
	g := newTestGitRepo(t)
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")

	// Add stream s2 with cards queued to merge
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s2", Count: 2}))
	accepted(w, "s2-1", "s2-2")

	// Add stream s3 stopped by a conflict
	w.must(Add(w.s, AddReq{Brief: proBrief, Stream: "s3", Count: 1}))
	accepted(w, "s3-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s3", Batch: 1, Conflict: "s3-1"}))
	require.Equal(t, StreamStopped, w.s.StreamCtl("s3").F("state"))
	require.Equal(t, "conflict", w.s.StreamCtl("s3").F("cause"))

	failing := true
	gate := func(dir, sha string) string {
		if failing {
			return "go test ./...: exit status 1: --- FAIL: TestBaseGateCheck"
		}
		return ""
	}

	// Pass 1: Land pass over red base.
	p, err := LandPass(w.s, LandPassReq{RepoDir: g.work, Base: "main", Gate: gate, Who: "coordinator"})
	require.NoError(t, err)
	w.must(p)

	// Invariant & state checks:
	// Both s1 and s2 stopped with cause "base"
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"))
	assert.Equal(t, "base", w.s.StreamCtl("s1").F("cause"))
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s2").F("state"))
	assert.Equal(t, "base", w.s.StreamCtl("s2").F("cause"))

	// ONE judgment naming the failing test
	baseRedNotes := map[string]Note{}
	for _, o := range w.s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NBaseRed {
			baseRedNotes[o.Note.ID] = o.Note
		}
	}
	require.Len(t, baseRedNotes, 1, "the base tree gate failing stops all landing with ONE judgment")
	for _, n := range baseRedNotes {
		assert.Contains(t, n.What, "TestBaseGateCheck", "judgment names the failing test")
	}

	// In the inbox, exactly one judgment group is displayed
	inbox := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open})
	baseRedGroups := 0
	for _, grp := range inbox {
		if grp.Type == NBaseRed {
			baseRedGroups++
		}
	}
	assert.Equal(t, 1, baseRedGroups, "one judgment group in inbox")

	// Invariants hold
	w.clean("base red stop")

	// Pass 2: Second land pass while base is still red adds no duplicate judgment
	p2, err := LandPass(w.s, LandPassReq{RepoDir: g.work, Base: "main", Gate: gate, Who: "coordinator"})
	require.NoError(t, err)
	w.must(p2)
	notesCount := 0
	for _, o := range w.s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NBaseRed {
			notesCount++
		}
	}
	assert.Equal(t, len(baseRedNotes), 1, "still only one judgment")

	// Pass 3: The base passes its gate again!
	failing = false
	require.NoError(t, os.WriteFile(filepath.Join(g.work, "FIX.txt"), []byte("fixed\n"), 0o644))
	g.git(g.work, "add", "FIX.txt")
	g.git(g.work, "commit", "-q", "-m", "fix the base")
	g.git(g.work, "push", "-q", "origin", "HEAD:refs/heads/main")

	p3, err := LandPass(w.s, LandPassReq{RepoDir: g.work, Base: "main", Gate: gate, Who: "coordinator"})
	require.NoError(t, err)
	w.must(p3)

	// Both s1 and s2 resumed by rule!
	assert.Equal(t, StreamMerging, w.s.StreamCtl("s1").F("state"))
	assert.Equal(t, StreamMerging, w.s.StreamCtl("s2").F("state"))
	assert.Empty(t, w.s.StreamCtl("s1").F("cause"))
	assert.Empty(t, w.s.StreamCtl("s2").F("cause"))

	// Resumption recorded in the log on the stream and via decided notes
	assert.Contains(t, w.s.StreamCtl("s1").F("did"), "answered by rule base-gate")
	assert.Contains(t, w.s.StreamCtl("s2").F("did"), "answered by rule base-gate")
	assert.Contains(t, w.s.StreamCtl("s1").F("did"), "the base main passes its tree gate again")

	foundDecided := false
	for _, n := range w.notes {
		if n.Kind == Decided && strings.Contains(n.What, "answered by rule base-gate: the base main passes its tree gate again") {
			foundDecided = true
			break
		}
	}
	assert.True(t, foundDecided, "resumption by rule recorded in the log")

	// The judgment is closed
	for _, o := range w.s.Open {
		assert.NotEqual(t, NBaseRed, o.Note.Type, "base red judgment must be closed")
	}

	// Stream s3 (stopped by conflict) was NOT resumed
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s3").F("state"), "conflict stream stays stopped")
	assert.Equal(t, "conflict", w.s.StreamCtl("s3").F("cause"))

	w.clean("base red resume")

	// Pass 4: With rule off, streams stopped on red base do not auto-resume
	w.s.RulesOff = []string{RuleBaseGate}
	failing = true
	p4, err := LandPass(w.s, LandPassReq{RepoDir: g.work, Base: "main", Gate: gate, Who: "coordinator"})
	require.NoError(t, err)
	w.must(p4)
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"))

	failing = false
	p5, err := LandPass(w.s, LandPassReq{RepoDir: g.work, Base: "main", Gate: gate, Who: "coordinator"})
	require.NoError(t, err)
	w.must(p5)
	assert.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"), "rule off prevents auto-resume")
}
