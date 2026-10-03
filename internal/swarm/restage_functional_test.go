//go:build functional

package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
)

// reworkOrigin is a bare origin whose main holds files a and b at base, and a branch
// sprint/c1.g1.e1 holding attempt 1's change to a, pushed: the rework tests' world. src is
// the working repository that writes it; base and prev are main's first commit and attempt
// 1's head.
type reworkOrigin struct {
	root, src, origin, base, prev string
}

func newReworkOrigin(t *testing.T) *reworkOrigin {
	t.Helper()
	o := &reworkOrigin{root: t.TempDir()}
	o.src, o.origin = filepath.Join(o.root, "src"), filepath.Join(o.root, "origin.git")
	require.NoError(t, os.MkdirAll(o.src, 0o755))
	execCmd(t, o.src, "git", "init", "-q", "-b", "main")
	execCmd(t, o.src, "git", "config", "user.name", "test")
	execCmd(t, o.src, "git", "config", "user.email", "test@example.com")
	o.write(t, "a", "a at base\n")
	o.write(t, "b", "b at base\n")
	execCmd(t, o.src, "git", "add", "a", "b")
	execCmd(t, o.src, "git", "commit", "-q", "-m", "base")
	o.base = o.head(t)
	execCmd(t, o.root, "git", "clone", "-q", "--bare", o.src, o.origin)
	execCmd(t, o.src, "git", "switch", "-q", "-c", "sprint/c1.g1.e1")
	o.write(t, "a", "a by attempt 1\n")
	execCmd(t, o.src, "git", "commit", "-q", "-am", "attempt 1")
	o.prev = o.head(t)
	execCmd(t, o.src, "git", "push", "-q", o.origin, "sprint/c1.g1.e1")
	execCmd(t, o.src, "git", "switch", "-q", "main")
	return o
}

func (o *reworkOrigin) write(t *testing.T, name, text string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(o.src, name), []byte(text), 0o644))
}

func (o *reworkOrigin) head(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(execCmd(t, o.src, "git", "rev-parse", "HEAD"))
}

// land commits a change to one file on main and pushes it: the stream's tip moves on.
func (o *reworkOrigin) land(t *testing.T, name, text string) string {
	t.Helper()
	o.write(t, name, text)
	execCmd(t, o.src, "git", "commit", "-q", "-am", "landed: "+name)
	execCmd(t, o.src, "git", "push", "-q", o.origin, "main")
	return o.head(t)
}

// stage stages attempt 2 of c1 as the member's frame does: the frame's commit is the last
// pushed head (prev, "" for none, the base staged), its base ref ref.
func (o *reworkOrigin) stage(t *testing.T, ref, prev string) (StageResult, string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "jobs", "c1.w2", "repo")
	sha, from := o.base, 0
	if prev != "" {
		sha, from = prev, 1
	}
	res, err := StageCard(StageOptions{
		Card: []byte("c1: the card\nBASE: main\n"), TargetDir: target, JobDir: filepath.Dir(target), BenchHome: filepath.Join(o.root, "home"),
		BenchName: "testhost", Timeout: 30 * time.Second, Base: &CardBase{Repo: o.origin, Sha: sha, Ref: ref, Named: o.origin},
		Branch: "sprint/c1.g1.e2", Rework: &Rework{Prev: prev, From: from},
	})
	require.NoError(t, err)
	require.True(t, res.Staged)
	return res, target
}

func readIn(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	return string(b)
}

// A rework is staged at the tip of its base branch as origin holds it, not at the commit its
// frame names, which may be hours behind (nova-tools#5215); with no earlier attempt that
// pushed it is the bare tip. A base that is a full sha never moves, and is staged as before.
func TestAReworkIsStagedAtTheTipOfItsBase(t *testing.T) {
	t.Parallel()
	o := newReworkOrigin(t)
	tip := o.land(t, "b", "b landed since\n")

	res, target := o.stage(t, "main", "")
	require.NotNil(t, res.Carry)
	assert.Equal(t, cardcontract.Carry{Base: "main", Tip: tip, Staged: tip, State: cardcontract.CarryNone}, *res.Carry)
	assert.Equal(t, tip, res.BaseSha, "the staged commit is the tip")
	assert.Equal(t, tip, strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")))
	assert.Equal(t, "sprint/c1.g1.e2", strings.TrimSpace(execCmd(t, target, "git", "symbolic-ref", "--short", "HEAD")))

	// the head attempt 1 pushed already descends from the tip: it is the staged commit itself
	stood := newReworkOrigin(t)
	res, _ = stood.stage(t, "main", stood.prev)
	assert.Equal(t, cardcontract.CarryOK, res.Carry.State)
	assert.Equal(t, stood.prev, res.BaseSha, "a head on the tip is staged as it is, its commits kept")

	res, target = o.stage(t, o.base, o.prev)
	assert.Nil(t, res.Carry, "a sha base never moves")
	assert.Equal(t, o.prev, res.BaseSha)
	assert.Equal(t, o.prev, strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")))
}

// The earlier attempt's work is carried onto the tip as one commit when it applies cleanly:
// the checkout holds the tip's landings and that work, the staged commit is the carry, its
// parent the tip; work the tip already holds adds no commit.
func TestAReworkCarriesThePreviousWorkThatApplies(t *testing.T) {
	t.Parallel()
	o := newReworkOrigin(t)
	tip := o.land(t, "b", "b landed since\n")

	res, target := o.stage(t, "main", o.prev)
	require.NotNil(t, res.Carry)
	c := *res.Carry
	assert.Equal(t, cardcontract.CarryOK, c.State)
	assert.Equal(t, tip, c.Tip)
	assert.Equal(t, o.prev, c.Prev)
	assert.Equal(t, 1, c.From)
	assert.NotEqual(t, tip, c.Staged)
	assert.Equal(t, c.Staged, res.BaseSha, "the carry is the commit the finish counts from")
	assert.Equal(t, c.Staged, strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")))
	assert.Equal(t, tip, strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD^")), "one commit on the tip")
	assert.Equal(t, "a by attempt 1\n", readIn(t, target, "a"), "the previous work is carried")
	assert.Equal(t, "b landed since\n", readIn(t, target, "b"), "the tip's landing is kept")
	assert.Empty(t, strings.TrimSpace(execCmd(t, target, "git", "status", "--porcelain")))
	assert.Contains(t, execCmd(t, target, "git", "log", "-1", "--format=%s"), "carry attempt 1's work")

	held := newReworkOrigin(t)
	heldTip := held.land(t, "a", "a by attempt 1\n") // the same change landed on main by another way
	res, target = held.stage(t, "main", held.prev)
	assert.Equal(t, cardcontract.CarryHeld, res.Carry.State)
	assert.Equal(t, heldTip, res.BaseSha, "nothing to carry: the tip is staged")
	assert.Equal(t, heldTip, strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")))
}

// Work that does not apply cleanly at the tip is not carried: the checkout is the bare tip,
// clean, and the carry says so, for JOB.md to ask that the work be redone.
func TestAReworkWhoseWorkDoesNotApplyIsTheBareTip(t *testing.T) {
	t.Parallel()
	o := newReworkOrigin(t)
	tip := o.land(t, "a", "a changed on main another way\n")

	res, target := o.stage(t, "main", o.prev)
	require.NotNil(t, res.Carry)
	assert.Equal(t, cardcontract.Carry{Base: "main", Tip: tip, Prev: o.prev, From: 1, Staged: tip, State: cardcontract.CarryConflict}, *res.Carry)
	assert.Equal(t, tip, res.BaseSha)
	assert.Equal(t, tip, strings.TrimSpace(execCmd(t, target, "git", "rev-parse", "HEAD")))
	assert.Equal(t, "a changed on main another way\n", readIn(t, target, "a"))
	assert.Empty(t, strings.TrimSpace(execCmd(t, target, "git", "status", "--porcelain")), "no conflict is left in the checkout")
	assert.Contains(t, cardcontract.For("claude").JobText(cardcontract.Frame{Kind: "work", Attempt: 2, PrevHead: o.prev, PrevFrom: 1, BaseRef: "main", Branch: "sprint/c1.g1.e2"},
		cardcontract.Staged{Job: "/j", Repo: target, Head: tip, Carry: res.Carry}), "The previous work: the work of attempt 1 must be redone from this tip")
}
