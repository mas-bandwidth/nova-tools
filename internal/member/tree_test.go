package member

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeBrief is a tree card of three work steps (docs/SPEC-SPRINT.md, a card is a tree of steps).
const treeBrief = `RESULT: c1 sha=0123456789ab
REPO: o/r

STEP 1. Enter your worktree with cd repo.
STEP 2. One.
  PATHS: a.go
  COMMIT: one
  VERDICT: ok
STEP 3. Two.
  PATHS: b.go
  COMMIT: two
  VERDICT: ok
STEP 4. Three.
  PATHS: c.go
  COMMIT: three
  VERDICT: ok
STEP 5. End as JOB.md says; write RESULT.md.
`

var (
	step1Sha = strings.Repeat("1", 40)
	step3Sha = strings.Repeat("3", 40)
)

// treeRig is pushRig with the tree brief on the packet.
func treeRig(t *testing.T, pu Pusher, r Result) *rig {
	t.Helper()
	s, rn, out := newScript(), newRunner(), &bytes.Buffer{}
	g := &rig{m: New(Config{As: "m", Width: 2}, s, rn, pu, out), s: s, r: rn, out: out}
	p := pk("c1")
	p.Brief = treeBrief
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(r)
	g.s.reset()
	return g
}

func TestAFlatCardLintsAndRunsAsBefore(t *testing.T) {
	t.Parallel()
	r := Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done", Body: "step 2: broken - not a tree card"}
	assert.Equal(t, r, treeFinish(pk("c1"), r), "a flat card's result is its own, whatever its body says")
	p := pk("c1")
	p.Brief = treeBrief
	assert.Equal(t, r.Head, treeFinish(p, Result{Ran: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done"}).Head,
		"a tree card whose result carries no step line keeps its own verdict")
	flat := pk("c1")
	flat.Brief = "RESULT: c1 sha=0123456789ab\nREPO: o/r\n\nSTEP 1. Enter your worktree with cd repo.\n  verdict: prose, not a field\nSTEP 2. End as JOB.md says; write RESULT.md.\n"
	assert.Equal(t, flat.Brief+"\n## From the sprint\n\n"+
		"This is c1: attempt 1 of p-c1 (stream a). The checkout is on branch work/c1; JOB.md, which the prompt names first, says where it is and how this card ends. When you end, the member pushes your commit to origin's branch work/c1 from outside the wall.\n\n"+
		"Your RESULT.md's `report:` line is what the sprint records as your report; the member reports it for you as:\n\n"+
		"    nova-sprint finish --as m c1@1 --epoch 7 --branch work/c1 --head <sha> --report '<one line>' [--failed]\n",
		CardText(flat), "a flat card's child text is byte for byte what it was")
	assert.Contains(t, CardText(p), "This card is a tree of steps.")
}

func TestAFailedStepTwoOfThreeLandsStepOneAndWritesTheRemainder(t *testing.T) {
	t.Parallel()
	body := "step 2: ok " + step1Sha + " one\nstep 3: broken " + step3Sha + " TestTwo is red at b.go:12\n"
	pu := &fakePusher{def: Push{Sha: step1Sha}}
	g := treeRig(t, pu, Result{Ran: true, Shaped: true, Verdict: "not-done", Head: step3Sha, Report: "step 3 failed", Body: body})
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{step1Sha}, pu.heads, "the member pushes the last ok step's commit, never the child's head")
	assert.Equal(t, []string{"finish --as m c1@1 --report pushed=" + step1Sha + " to work/c1: remainder=c1-r3 step 3 broken: TestTwo is red at b.go:12 --head " + step1Sha + " --branch work/c1 --epoch 7"},
		g.s.lines("finish"), "an ok finish at step 1's commit, so it is read and lands; the report names the remainder card")
}

func TestAFailedFirstStepIsAFailedFinishNamingTheStep(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g := treeRig(t, pu, Result{Ran: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done", Body: "step 2: broken - the gate is red\n"})
	_, err := g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1)
	assert.Contains(t, finish[0], "--report step 2 broken: the gate is red; pushed=")
	assert.Contains(t, finish[0], "--failed")
}
