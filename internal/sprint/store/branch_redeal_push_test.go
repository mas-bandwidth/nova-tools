package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// gitRepo is a bare origin and a work clone of it, for pushes as the member makes them:
// plain, never forced.
type gitRepo struct {
	t            *testing.T
	origin, work string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	root := t.TempDir()
	g := &gitRepo{t: t, origin: filepath.Join(root, "origin.git"), work: filepath.Join(root, "work")}
	g.git(root, "init", "-q", "--bare", "-b", "main", g.origin)
	g.git(root, "clone", "-q", g.origin, g.work)
	require.NoError(t, os.WriteFile(filepath.Join(g.work, "README"), []byte("base\n"), 0o644))
	g.git(g.work, "add", "README")
	g.git(g.work, "commit", "-q", "-m", "base")
	g.git(g.work, "push", "-q", "origin", "HEAD:refs/heads/main")
	return g
}

// run is one git command in dir with no user or system configuration; it returns the
// trimmed output and the error.
func (g *gitRepo) run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (g *gitRepo) git(dir string, args ...string) string {
	g.t.Helper()
	out, err := g.run(dir, args...)
	require.NoError(g.t, err, "git %s: %s", strings.Join(args, " "), out)
	return out
}

// launch is one launch's work: a commit of its own on the base, pushed plainly to branch;
// it returns the commit and the push's error.
func (g *gitRepo) launch(name, branch string) (string, error) {
	g.t.Helper()
	g.git(g.work, "switch", "-q", "--detach", "origin/main")
	require.NoError(g.t, os.WriteFile(filepath.Join(g.work, name), []byte(name+"\n"), 0o644))
	g.git(g.work, "add", name)
	g.git(g.work, "commit", "-q", "-m", name)
	head := g.git(g.work, "rev-parse", "HEAD")
	out, err := g.run(g.work, "push", "-q", "origin", head+":refs/heads/"+branch)
	if err != nil {
		return head, &pushRefused{out}
	}
	return head, nil
}

type pushRefused struct{ out string }

func (p *pushRefused) Error() string { return p.out }

// THE REDEAL IN ONE EPOCH, PUSHED (the quack pass of 2026-10-01: 33 attempts refused at the
// push, `[rejected] (non-fast-forward)`). Take 1 pushes its commit and its finish does not
// land as the card's (here a provider failure, as a lost finish or a member marked down
// does); the card is dealt again at generation 2 in the same epoch; take 2 pushes a commit
// of its own, not a descendant of take 1's. Both pushes succeed, each to its launch's
// branch; the card's head is take 2's; and the read of the card is handed take 2's branch
// and head, which origin holds. On the branch named by the epoch alone, take 2's push is
// the refusal of that day.
func TestARedealInOneEpochPushesBothTakesAndTheReadChecksOutTheSecond(t *testing.T) {
	t.Parallel()
	g := newGitRepo(t)
	h := fourMembers(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()

	one := h.packetOf("s1-1.w1")
	headOne, err := g.launch("take-1", one.Branch)
	require.NoError(t, err, "take 1 pushes")
	h.failTake("s1-1.w1", providerLine)
	h.machine()

	w := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Ready, w.Col, "dealt again by the tick")
	two := h.packetOf("s1-1.w1")
	require.Equal(t, one.Epoch, two.Epoch, "the same epoch")
	require.Greater(t, two.Gen, one.Gen, "another generation")
	gens := map[string]int{w.ID: w.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: w.Row, Sel: sprint.Sel{IDs: []string{w.ID}}, Gens: gens, Who: w.Row}))
	headTwo, err := g.launch("take-2", two.Branch)
	require.NoError(t, err, "take 2 pushes to a branch of its own")
	h.must(FinishStep(sprint.FinishReq{As: w.Row, Sel: sprint.Sel{IDs: []string{w.ID}}, Gens: gens, Head: headTwo, Branch: two.Branch, Report: "ok", Who: w.Row}))

	assert.Equal(t, headOne, g.git(g.origin, "rev-parse", "refs/heads/"+one.Branch), "take 1's push stands")
	assert.Equal(t, headTwo, g.git(g.origin, "rev-parse", "refs/heads/"+two.Branch), "take 2's push stands")
	assert.Equal(t, headTwo, h.snap().Fleet.Card("s1-1.w1").F("head"), "the card's head is take 2's")

	h.machine()
	if len(h.snap().Readers.Of("s1-1")) == 0 {
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	}
	reads := h.snap().Readers.Of("s1-1")
	require.NotEmpty(t, reads, "the work is asked of its readers")
	ps, err := h.st.Packets(h.ctx, reads[:1])
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, two.Branch, ps[0].WorkBranch, "the read checks out take 2's branch")
	assert.Equal(t, headTwo, ps[0].Head, "at take 2's head")
	assert.Equal(t, headTwo, g.git(g.origin, "rev-parse", "refs/heads/"+ps[0].WorkBranch), "which origin holds")

	// the witness: on one branch per epoch, take 2's push is refused non-fast-forward
	_, err = g.launch("take-2-again", one.Branch)
	var refused *pushRefused
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.out, "non-fast-forward")
}
