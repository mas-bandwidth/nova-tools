package sprint_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fakePush is DrivePush's repo with no git, no socket and no real wait.
type fakePush struct {
	stream   string
	merged   bool
	gateErr  error
	queue    string
	errs     []error
	failRest error
	pushN    int
	rebuilds int

	pushes    []string
	waits     []time.Duration
	befores   []int
	landed    string
	left      string
	refused   string
	stopped   []string
	gates     []string
	judgments []string
	ended     bool
}

func (f *fakePush) Rebuild() (string, bool, error) {
	f.rebuilds++
	if f.gateErr != nil {
		return "", false, f.gateErr
	}
	return "tip-" + itoa(f.rebuilds), f.merged, nil
}

func (f *fakePush) Push(tip string) error {
	f.pushes = append(f.pushes, tip)
	if f.pushN < len(f.errs) {
		err := f.errs[f.pushN]
		f.pushN++
		return err
	}
	f.pushN++
	return f.failRest
}

func (f *fakePush) Queue() string { return f.queue }

func (f *fakePush) Wait(d time.Duration) { f.waits = append(f.waits, d) }

func (f *fakePush) Before(attempt int) { f.befores = append(f.befores, attempt) }

func (f *fakePush) Landed(tip string) { f.landed = tip }

func (f *fakePush) StopPush(reason string) {
	f.stopped = append(f.stopped, reason)
	f.judgments = append(f.judgments, sprint.StopJudgment(f.stream, reason))
}

func (f *fakePush) StopGate(reason string) {
	f.gates = append(f.gates, reason)
	f.judgments = append(f.judgments, sprint.StopJudgment(f.stream, reason))
}

func (f *fakePush) Leave(note string) { f.left = note }

func (f *fakePush) Refuse(why string) { f.refused = why }

func (f *fakePush) EndedNoMerge() { f.ended = true }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestFetchFirstRejectionRebuildsAndPushesAgain(t *testing.T) {
	t.Parallel()
	f := &fakePush{
		stream: "s1",
		merged: true,
		errs:   []error{errors.New("! [rejected] main -> main (fetch first)")},
	}
	sprint.DrivePush(sprint.PushDrive{Base: "main", Tip: "t0", Bound: 5, Wait: sprint.PushRebuildWait}, f)
	assert.Equal(t, []int{1, 2}, f.befores, "the first push is not a rebuild")
	assert.Equal(t, 1, f.rebuilds)
	assert.Equal(t, []time.Duration{sprint.PushRebuildWait}, f.waits)
	assert.Equal(t, []string{"t0", "tip-1"}, f.pushes)
	assert.Equal(t, "tip-1", f.landed)
	assert.Empty(t, f.stopped)
	assert.Empty(t, f.gates)
	assert.Empty(t, f.judgments)
	assert.Empty(t, f.left)
}

func TestFetchFirstBoundLeavesTheBatchQueued(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		bound int
		note  string
	}{
		{5, "base moving: 4 rebuilds"},
		{3, "base moving: 2 rebuilds"},
	} {
		t.Run(tc.note, func(t *testing.T) {
			t.Parallel()
			f := &fakePush{stream: "s1", merged: true, failRest: errors.New("non-fast-forward")}
			sprint.DrivePush(sprint.PushDrive{Base: "main", Tip: "t0", Bound: tc.bound, Wait: sprint.PushRebuildWait}, f)
			assert.Len(t, f.pushes, tc.bound)
			assert.Equal(t, tc.bound-1, f.rebuilds)
			assert.Equal(t, tc.note, f.left)
			assert.Empty(t, f.stopped, "a fetch-first rejection never stops the stream")
			assert.Empty(t, f.judgments)
			assert.Empty(t, f.landed)
			assert.Len(t, f.waits, tc.bound-1)
		})
	}
}

func TestAGateFailureStopsWithTheReason(t *testing.T) {
	t.Parallel()
	const why = "the head abc of c1 fails the tree gate: go build ./..."
	f := &fakePush{stream: "s1", gateErr: sprint.GateFailure(why)}
	sprint.DrivePush(sprint.PushDrive{Base: "main", Tip: "t0", Bound: 5, Moved: true}, f)
	require.Equal(t, []string{why}, f.gates)
	require.Len(t, f.judgments, 1)
	assert.Equal(t, sprint.StopJudgment("s1", why), f.judgments[0])
	assert.Empty(t, f.pushes)
	assert.Empty(t, f.stopped)
	choice := sprint.ChooseGate(why)
	assert.Equal(t, "stop", choice.Kind)
	assert.Equal(t, "red", choice.Cause)
	assert.Equal(t, "stopped: "+why, choice.Row)
	assert.False(t, sprint.LandResumes(choice.Cause), "a gate stop stays for resume")
}

func TestARejectedPushOtherThanFetchFirstStopsOnce(t *testing.T) {
	t.Parallel()
	for _, msg := range []string{
		"[remote rejected] main (protected branch hook declined)",
		"authentication failed for https://remote.invalid/repo",
		"GH006: protected branch hook declined",
	} {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			f := &fakePush{stream: "s9", merged: true, failRest: errors.New(msg)}
			sprint.DrivePush(sprint.PushDrive{Base: "main", Tip: "t0", Bound: 5}, f)
			require.Equal(t, []string{msg}, f.stopped)
			require.Len(t, f.judgments, 1, "one judgment, not one per attempt")
			assert.Equal(t, sprint.StopJudgment("s9", msg), f.judgments[0])
			assert.Equal(t, []string{"t0"}, f.pushes, "a non-fetch-first rejection does not rebuild")
			assert.Empty(t, f.left)
			assert.Equal(t, 0, f.rebuilds)
			choice := sprint.ChoosePush(1, 5, msg)
			assert.Equal(t, "rejected", choice.Cause)
			require.True(t, sprint.LandResumes(choice.Cause), "land retries the persisted push-stop cause")
			f.failRest = nil
			sprint.DrivePush(sprint.PushDrive{Base: "main", Tip: "t0", Bound: 5}, f)
			assert.Equal(t, []string{"t0", "t0"}, f.pushes)
			assert.Equal(t, "t0", f.landed)
			assert.Len(t, f.judgments, 1, "successful retry adds no stop judgment")
		})
	}
}

func TestLandResumesOnlyAPushStop(t *testing.T) {
	t.Parallel()
	assert.True(t, sprint.LandResumes("rejected"))
	assert.True(t, sprint.LandResumes("push"))
	assert.False(t, sprint.LandResumes("base"))
	assert.False(t, sprint.LandResumes("red"))
	assert.False(t, sprint.LandResumes("protected"))
	got := sprint.ChooseProtected("card c1 lands on main, a protected branch")
	assert.Equal(t, "stop", got.Kind)
	assert.Equal(t, "protected", got.Cause)
	assert.Equal(t, "card c1 lands on main, a protected branch", got.Reason)
	assert.Equal(t, sprint.PushRebuildsDefault, sprint.PushRebuildBound(""))
	assert.Equal(t, 5, sprint.PushRebuildBound("5"))
	assert.Equal(t, 1, sprint.PushRebuildBound("1"))
	assert.Equal(t, sprint.PushRebuildsDefault, sprint.PushRebuildBound("0"))
	assert.Equal(t, sprint.PushRebuildsDefault, sprint.PushRebuildBound("nope"))
	assert.Equal(t, "base moving: 4 rebuilds", sprint.BaseMovingNote(4))
	assert.True(t, sprint.FetchFirst("! [rejected] (fetch first)"))
	assert.True(t, sprint.FetchFirst("non-fast-forward"))
	assert.False(t, sprint.FetchFirst("[remote rejected] protected branch"))
}
