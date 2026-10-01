package member

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePusher is a Pusher that records every push it is asked for and answers
// each with push (by card, else def; the zero answer is a None that names the
// fake, so a test that does not care about pushes reads the finish it always did).
type fakePusher struct {
	mu    sync.Mutex
	asked []Packet
	heads []string
	by    map[string]Push
	def   Push
	// yield, when set, makes each push yield the processor that many times
	// while it is in flight, so pushes run side by side show as several in
	// flight at once (most is the most seen); no clock is read
	yield    int
	inflight atomic.Int32
	most     atomic.Int32
}

func (f *fakePusher) Push(p Packet, r Result) Push {
	if f.yield > 0 {
		n := f.inflight.Add(1)
		for {
			m := f.most.Load()
			if n <= m || f.most.CompareAndSwap(m, n) {
				break
			}
		}
		for i := 0; i < f.yield; i++ {
			runtime.Gosched()
		}
		f.inflight.Add(-1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, p)
	f.heads = append(f.heads, r.Head)
	if pu, ok := f.by[p.Card]; ok {
		return pu
	}
	if f.def == (Push{}) {
		return Push{None: "the test's pusher pushes nothing"}
	}
	return f.def
}

func (f *fakePusher) cards() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.asked {
		out = append(out, p.Card)
	}
	return out
}

// pushRig is a member over a script, a runner and the pusher given, with
// one work card c1 at gen 1 already ours and ended with the result given.
func pushRig(t *testing.T, pu Pusher, r Result) (*rig, Packet) {
	t.Helper()
	s, rn, out := newScript(), newRunner(), &bytes.Buffer{}
	g := &rig{m: New(Config{As: "m", Width: 2}, s, rn, pu, out), s: s, r: rn, out: out}
	p := pk("c1")
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(r)
	g.s.reset()
	return g, p
}

const fullSha = "0123456789abcdef0123456789abcdef01234567"

// A child whose result names a commit has it pushed before the finish, and
// the finish carries the pushed sha as the head, the branch, and the push
// first in the report, so the merge finds the work on origin.
func TestAnEndedCardIsPushedBeforeItIsFinished(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed the thing"})
	acted, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, 1, acted)
	assert.Equal(t, []string{"c1"}, pu.cards())
	assert.Equal(t, []string{"0123456"}, pu.heads, "the pusher is handed the head the child named")
	assert.Equal(t, "work/c1", pu.asked[0].Branch, "the pusher is handed the launch's packet")
	assert.Equal(t, []string{"finish --as m c1@1 --report pushed=" + fullSha + " to work/c1: landed the thing --head " + fullSha + " --branch work/c1 --epoch 7"}, g.s.lines("finish"))
	assert.Contains(t, g.out.String(), "push c1 pushed="+fullSha+" branch=work/c1\n")
}

// A push that git refuses is a NOTE on the member's output and a failed
// finish whose report starts with git's line, naming no head and no branch:
// the sprint shows a failed card, never one whose work is not on origin
// (docs/SPEC-CARD-CONTRACT.md section 4).
func TestARefusedPushIsANoteAndAFailedFinish(t *testing.T) {
	t.Parallel()
	line := "!\t" + fullSha + ":refs/heads/work/c1\t[rejected] (non-fast-forward)"
	pu := &fakePusher{def: Push{Refused: line}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Head: fullSha, Report: "landed"})
	_, err := g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1)
	assert.Equal(t, "finish --as m c1@1 --report push refused: "+line+"; landed --failed --epoch 7", finish[0])
	assert.Contains(t, g.out.String(), "NOTE push c1 refused: "+line+"\n")
	assert.Contains(t, g.out.String(), "finish c1 ok=false exit=0\n", "the line printed says what was reported")
}

// A child that named no commit is not pushed, says so, and is finished
// failed, no commit, naming no branch.
func TestAChildWithNoCommitIsNotPushed(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Report: "nothing to commit"})
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, pu.cards(), "a result with no head is never pushed")
	assert.Equal(t, []string{"finish --as m c1@1 --report no commit: the child's result names no commit (no head: line, no push recorded); nothing to commit --failed --epoch 7"}, g.s.lines("finish"))
	assert.Contains(t, g.out.String(), "push c1: not pushed: the child's result names no commit")
}

// A pusher that finds nothing of the child's to push (None) is a failed
// finish, no commit: a card with nothing on origin never goes to review
// (docs/SPEC-CARD-CONTRACT.md section 4), and the member's output says why.
func TestAPushOfNothingIsAFailedFinishNoCommit(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{None: "the child committed nothing"}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "done"})
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"finish --as m c1@1 --report no commit: the child committed nothing; done --failed --epoch 7"}, g.s.lines("finish"))
	assert.Contains(t, g.out.String(), "NOTE finish c1 failed: no commit: the child committed nothing\n")
	assert.Contains(t, g.out.String(), "push c1: not pushed: the child committed nothing\n")
}

// A pusher that answers nothing at all is a refusal, never a silent pass.
func TestAPusherThatSaysNothingIsARefusal(t *testing.T) {
	t.Parallel()
	g, _ := pushRig(t, silentPusher{}, Result{Ran: true, OK: true, Head: fullSha, Report: "done"})
	_, err := g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1)
	assert.Contains(t, finish[0], "--report push refused: the pusher said nothing; done")
	assert.Contains(t, finish[0], "--failed")
}

type silentPusher struct{}

func (silentPusher) Push(Packet, Result) Push { return Push{} }

// A finish the store did not answer is reported again on the next pass from
// the push already made: the commit is pushed once.
func TestAFinishReportedAgainIsNotPushedAgain(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Head: fullSha, Report: "r"})
	g.s.set("finish", 2, "no route to the store")
	_, err := g.tick(t)
	require.Error(t, err)
	g.s.set("finish", 0, "")
	acted, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, 1, acted)
	assert.Equal(t, []string{"c1"}, pu.cards(), "pushed once across the two passes")
	finish := g.s.lines("finish")
	require.Len(t, finish, 2)
	assert.Equal(t, finish[0], finish[1], "the second finish is the first, again")
}

// Children that end together are pushed together: one push each, side by
// side and never more than pushWidth at once, and every finish follows in
// card order.
func TestCardsThatEndTogetherArePushedSideBySide(t *testing.T) {
	t.Parallel()
	var ids []string
	for i := 1; i <= 2*pushWidth; i++ {
		ids = append(ids, fmt.Sprintf("c%02d", i))
	}
	pu := &fakePusher{def: Push{Sha: fullSha}, yield: 2000}
	s, rn, out := newScript(), newRunner(), &bytes.Buffer{}
	g := &rig{m: New(Config{As: "m", Width: len(ids)}, s, rn, pu, out), s: s, r: rn, out: out}
	var cards []queueCard
	for _, id := range ids {
		p := pk(id)
		cards = append(cards, working(id, 1, &p))
	}
	g.s.set("queue", 0, queueJSON(t, 7, cards...))
	_, err := g.tick(t)
	require.NoError(t, err)
	for _, id := range ids {
		g.r.child(id).end(Result{Ran: true, OK: true, Head: fullSha, Report: "r"})
	}
	g.s.reset()
	acted, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, len(ids), acted)
	assert.Greater(t, pu.most.Load(), int32(1), "the pushes ran side by side, not one after another")
	assert.LessOrEqual(t, pu.most.Load(), int32(pushWidth), "never more than pushWidth pushes at once")
	assert.ElementsMatch(t, ids, pu.cards())
	finish := g.s.lines("finish")
	require.Len(t, finish, len(ids))
	for i, id := range ids {
		assert.True(t, strings.HasPrefix(finish[i], "finish --as m "+id+"@1 "), "finish %d is %s's: %s", i, id, finish[i])
	}
}

// A reader pushes nothing: its pusher may be nil and is never asked.
func TestAReaderPushesNothing(t *testing.T) {
	t.Parallel()
	s, rn, out := newScript(), newRunner(), &bytes.Buffer{}
	g := &rig{m: New(Config{As: "r", Width: 1, Reader: true}, s, rn, nil, out), s: s, r: rn, out: out}
	a := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: fullSha}
	g.s.set("queue", 0, queueJSON(t, 7, queueCard{ID: "r1", Col: "reading", Packet: &a}))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("r1").end(Result{Ran: true, OK: true, Verdict: "ok", Head: fullSha, Report: "clean"})
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"read --as r --ok r1 --finding clean --epoch 7"}, g.s.lines("report"))
	assert.NotContains(t, g.out.String(), "push")
}

// The card a child reads names the push: the member pushes to the packet's
// branch when the child ends, and the child pushes nothing itself.
func TestCardTextSaysTheMemberPushes(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"", "sprint/base"} {
		p := pk("c1")
		p.Base = base
		got := CardText(p)
		assert.Contains(t, got, "When you end, the member pushes your commit to origin's branch work/c1 from outside the wall.", "base %q", base)
		assert.Contains(t, got, "JOB.md, which the prompt names first", "base %q", base)
		assert.NotContains(t, got, "rev:", "base %q", base)
	}
}

// Judge is the finish rule in one place (tla/CardContract.tla, Judge): ok
// only with the shape, the verdict ok and a pushed commit; every other end is
// failed with its reason.
func TestJudgeIsTheFinishRule(t *testing.T) {
	t.Parallel()
	shaped := Result{Shaped: true, Verdict: "ok", Head: fullSha, Report: "r"}
	for _, tc := range []struct {
		name string
		r    Result
		pu   Push
		fin  Finish
		why  string
	}{
		{"shaped, ok, pushed", shaped, Push{Sha: fullSha}, FinishOK, ""},
		{"no shape", Result{Verdict: "ok", Head: fullSha}, Push{Sha: fullSha}, FinishFailed, "no result: no RESULT.md shape"},
		{"not done", Result{Shaped: true, Verdict: "not-done", Report: "r"}, Push{Sha: fullSha}, FinishFailed, "verdict not-done"},
		{"no commit", shaped, Push{None: "nothing new"}, FinishFailed, "no commit: nothing new"},
		{"push refused", shaped, Push{Refused: "! [rejected]"}, FinishFailed, "push refused: ! [rejected]"},
		{"nothing to do", Result{Shaped: true, Verdict: "nothing", Report: "nothing: the check holds"}, Push{None: "nothing new"}, FinishFailed, "nothing to do: the check holds"},
		{"nothing to do, said plainly", Result{Shaped: true, Verdict: "nothing", Report: "the check holds"}, Push{None: "nothing new"}, FinishFailed, "nothing to do: the check holds"},
	} {
		fin, why := Judge(tc.r, tc.pu)
		assert.Equal(t, tc.fin, fin, tc.name)
		assert.Equal(t, tc.why, why, tc.name)
	}
}

// A work child that wrote `verdict: broken` and committed nothing is failed,
// never ok: it goes to the failed-work judgment, not to review with nothing to
// read (docs/SPEC-CARD-CONTRACT.md section 4; red on the base before it).
func TestAWorkChildWithNoCommitAndNoShapeIsFailed(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Verdict: "broken", Report: "could not build"})
	_, err := g.tick(t)
	require.NoError(t, err)
	finish := g.s.lines("finish")
	require.Len(t, finish, 1)
	assert.Contains(t, finish[0], "--failed")
	assert.NotContains(t, finish[0], "--branch", "a finish names only a branch a push landed on")
	assert.Contains(t, g.out.String(), "finish c1 ok=false exit=0\n")
}

// A pushed head whose result asked for a pull request carries its address in
// the report; one the pusher could not open is a NOTE and the finish stands.
func TestThePullRequestRidesOnTheFinish(t *testing.T) {
	t.Parallel()
	r := Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha, Report: "r", Title: "T"}
	g, _ := pushRig(t, &fakePusher{def: Push{Sha: fullSha, PR: "https://example.com/o/n/pull/7"}}, r)
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"finish --as m c1@1 --report pushed=" + fullSha + " to work/c1 pr=https://example.com/o/n/pull/7: r --head " + fullSha + " --branch work/c1 --epoch 7"}, g.s.lines("finish"))
	g, _ = pushRig(t, &fakePusher{def: Push{Sha: fullSha, PRNote: "no gh"}}, r)
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.NotContains(t, g.s.lines("finish")[0], "--failed")
	assert.Contains(t, g.out.String(), "NOTE pr c1 not opened: no gh\n")
}
