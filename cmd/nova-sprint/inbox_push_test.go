package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// heldAndWaiting is a sprint with one judgment open and held (wait --for 30m)
// and the machine running: what the coordinator's inbox --wait must sleep
// through. It returns the held group.
func heldAndWaiting(t *testing.T) (*testApp, sprint.Group) {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s2 --count 1 --one")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	held := ta.group(sprint.NWorkFailed, "s1")
	ta.ok("wait " + held.ID + " --for 30m")
	ta.ok("start")
	return ta, held
}

// atSleep runs f at the n-th sleep of the test app's clock, around the
// clock's own step: the wait's time passes, and the sprint moves under it.
func (ta *testApp) atSleep(f func(n int)) {
	orig := ta.a.sleep
	n := 0
	ta.a.sleep = func(d time.Duration) {
		orig(d)
		n++
		f(n)
	}
}

// inbox --wait wakes for a judgment that was not open when the wait began,
// and for nothing else (Glenn, 2026-10-02: "push notifications for inbox from
// nova-sprint so she doesn't have to poll"; nova-tools#5096): a tick end over
// a held judgment does not wake it, a new judgment in another stream does,
// and the wake line names the new group, never the held one.
func TestInboxWaitWakesOnlyForANewJudgment(t *testing.T) {
	t.Parallel()
	ta, held := heldAndWaiting(t)
	ta.atSleep(func(n int) {
		switch n {
		case 2:
			ta.ok("tick") // the tick end over the held judgment alone
		case 4:
			ta.failOnce("m1", "s2-1.w1@1", "tests red")
			ta.ok("tick")
		}
	})
	before := ta.a.now()
	out := ta.ok("inbox --wait --timeout 1m")
	fresh := ta.group(sprint.NWorkFailed, "s2")
	require.Contains(t, out, "inbox --wait: new=", "the wake line names the new group:\n%s", out)
	require.Contains(t, out, fresh.ID, "the wake line names the new group:\n%s", out)
	assert.NotContains(t, out, "new="+held.ID, "the held judgment woke the wait:\n%s", out)
	assert.NotContains(t, out, "nothing new", "the wait ran out:\n%s", out)
	assert.Less(t, ta.a.now().Sub(before), waitLook, "woke by the tick end, not by the look between ticks") // wall-ok: the test app's clock
	assert.Contains(t, out, "JUDGMENT "+fresh.ID, "the inbox follows the wake line:\n%s", out)

	// --json carries the new groups' ids, and woke.
	ta.atSleep(func(n int) {
		if n == 2 {
			ta.ok("add --stream s3 --count 1 --one")
			ta.deal(1)
			ta.failOnce("m1", "s3-1.w1@1", "tests red")
			ta.ok("tick")
		}
	})
	var got struct {
		Woke bool     `json:"woke"`
		New  []string `json:"new"`
	}
	ta.json("inbox --wait --timeout 1m", &got)
	assert.True(t, got.Woke)
	assert.Contains(t, got.New, ta.group(sprint.NWorkFailed, "s3").ID)
}

// inbox --wait with nothing new runs out its timeout and says so; the held
// judgment is still shown, as every open judgment is.
func TestInboxWaitWithOnlyHeldJudgmentsRunsOut(t *testing.T) {
	t.Parallel()
	ta, held := heldAndWaiting(t)
	ta.atSleep(func(n int) {
		if n == 2 {
			ta.ok("tick")
		}
	})
	out := ta.ok("inbox --wait --timeout 3s")
	assert.Contains(t, out, "inbox --wait: nothing new in 3s\n", out)
	assert.Contains(t, out, "JUDGMENT "+held.ID, out)
	var got struct {
		Woke bool     `json:"woke"`
		New  []string `json:"new"`
	}
	ta.json("inbox --wait --timeout 1s", &got)
	assert.False(t, got.Woke)
	assert.Equal(t, []string{}, got.New)
}

// inbox --wait ends when the machine stops: the sprint done stops it, and the
// coordinator is told.
func TestInboxWaitEndsWhenTheMachineStops(t *testing.T) {
	t.Parallel()
	ta, _ := heldAndWaiting(t)
	ta.atSleep(func(n int) {
		if n == 3 {
			ta.ok("stop --reason r --until 9999h")
			ta.ok("tick")
		}
	})
	out := ta.ok("inbox --wait --timeout 1m")
	assert.Contains(t, out, "inbox --wait: the machine stopped\n", out)
	assert.Contains(t, out, "machine: STOPPED by coordinator: r, back by ", out)
}

// inbox --wait --push <dir> writes every judgment the directory does not hold
// as <dir>/<note id>.md, the group as inbox --open prints it with the clock,
// then waits for the next; a timeout is quiet and the loop goes on; a file is
// never written twice, and a second run over the same directory pushes
// nothing it already holds: the directory is the cursor.
func TestInboxWaitPushWritesEachJudgmentOnce(t *testing.T) {
	t.Parallel()
	ta, held := heldAndWaiting(t)
	dir := filepath.Join(t.TempDir(), "inbox", "sprint-judgments")
	in := ta.interruptible()
	ta.atSleep(func(n int) {
		switch n {
		case 4:
			ta.failOnce("m1", "s2-1.w1@1", "tests red")
			ta.ok("tick")
		case 8:
			in.now(t)
		}
	})
	out := ta.ok("inbox --wait --push " + dir + " --timeout 200ms")
	fresh := ta.group(sprint.NWorkFailed, "s2")
	heldPath := filepath.Join(dir, held.Notes[0]+".md")
	freshPath := filepath.Join(dir, fresh.Notes[0]+".md")
	assert.Contains(t, out, "INBOX OK pushed="+held.Notes[0]+" file="+heldPath+"\n", "the held judgment had no file yet: pushed once\n%s", out)
	assert.Contains(t, out, "INBOX OK pushed="+fresh.Notes[0]+" file="+freshPath+"\n", out)
	assert.NotContains(t, out, "nothing new", "a timeout under --push is quiet\n%s", out)
	assert.NotContains(t, out, "JUDGMENT", "the inbox is not printed under --push\n%s", out)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 3, "one file per judgment")
	text, err := os.ReadFile(freshPath)
	require.NoError(t, err)
	now := ta.a.now()
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	assert.Equal(t, groupLine(fresh, now)[:len("JUDGMENT "+fresh.ID)], lines[0][:len("JUDGMENT "+fresh.ID)], "line 1 is the judgment line:\n%s", text)
	assert.Contains(t, string(text), "\n  tests red\n  rework with a fix:\n    nova-sprint rework s2-1\n", "the finding and the answer lines, as inbox --open prints them (a one-card judgment names its card):\n%s", text)
	assert.Contains(t, string(text), "\n  s2-1\n", "the members, as inbox --open prints them:\n%s", text)
	assert.Contains(t, string(text), "  notes: "+fresh.Notes[0]+"\n", "the notes, as inbox --open prints them:\n%s", text)
	assert.Regexp(t, `(?m)^clock: 2030-01-02T\d\d:\d\d:\d\dZ$`, string(text), "the clock:\n%s", text)
	assert.Equal(t, 1, in.released, "the interrupt was released")

	// A second run over the same directory: the three files stand, nothing is
	// pushed again, and the interrupt ends it.
	in = ta.interruptible()
	ta.atSleep(func(n int) {
		if n == 2 {
			in.now(t)
		}
	})
	out = ta.ok("inbox --wait --push " + dir + " --timeout 200ms")
	assert.NotContains(t, out, "pushed=", "a second run pushed a judgment the directory holds:\n%s", out)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

// --push wants --wait, and runs a loop, so --read, --open and --at-epoch are
// refused with it.
func TestInboxPushRefusesWhatItCannotCombine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	for _, line := range []string{"inbox --push " + dir, "inbox --wait --push " + dir + " --read", "inbox --wait --push " + dir + " --open x", "inbox --wait --push " + dir + " --at-epoch 0"} {
		code, out, errs := ta.do(line)
		assert.Equal(t, 2, code, "%s: %s%s", line, out, errs)
		assert.Contains(t, errs, "--push", "%s: %s%s", line, out, errs)
	}
}

// The ack line inbox prints joins the group's notes with commas; ack takes
// that line as printed (nova-tools#5096, item 14): the comma list is every
// note of the group, closed in one step. A need naming a dropped card is
// refused at add (docs/SPEC-SPRINT.md section 11), so the two waiters on the
// dropped need are never admitted: the add names the id and its outcome,
// writes nothing, and no blocked group opens. The comma-list close itself is
// pinned at the store (store/ack_line_test.go covers NBlocked and
// NMissingNeed).
func TestAckTakesTheCommaListInboxPrints(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("drop s1-1 --reason obsolete")
	before := ta.applies()
	for _, id := range []string{"b", "c"} {
		code, out, errs := ta.do("add --stream s2 " + id + " --one --needs s1-1")
		require.Equal(t, 1, code, "add on a dropped need: %s%s", out, errs)
		require.NotContains(t, out, "MOVED", "add on a dropped need wrote: %s", out)
		assert.Contains(t, errs, "needs s1-1, which was dropped", "add on a dropped need: %s", errs)
	}
	require.Equal(t, before, ta.applies(), "a refused add wrote")
	for _, g := range ta.inboxGroups() {
		assert.False(t, g.Kind == sprint.Judgment && g.Type == sprint.NBlocked, "a refused add opened a blocked judgment: %+v", g)
	}
	ta.clean()
}

// inbox --wait --push through the server: the wait and the files are here,
// each group read whole through the server (inbox --json --open <id>) before
// it is written, and the interrupt ends the loop.
func TestInboxPushThroughTheServerWritesTheGroupWhole(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1:2",
		"nova-sprint add --stream s1 --count 1 --one --brief-file "+proBriefFile(t), "nova-sprint start", "nova-sprint tick", "nova-sprint tick",
		"nova-sprint take --as m1 --limit 1", "nova-sprint finish --as m1 --epoch 0 s1-1.w1@1")
	clock := r.a.now()
	r.a.now = func() time.Time { return clock }
	var sent [][]string
	c, boss := clientOf(t, r, "boss", &sent)
	waited := clock
	c.now = func() time.Time { return waited }
	ctx, stop := context.WithCancel(t.Context())
	t.Cleanup(stop)
	c.notify = func(context.Context) (context.Context, context.CancelFunc) { return ctx, stop }
	polls := 0
	c.sleep = func(d time.Duration) {
		waited = waited.Add(d)
		switch polls++; polls {
		case 2:
			r.boss("nova-sprint reader away reader-a") // fewer than two readers up: a judgment at the tick
			r.boss("nova-sprint tick")
		case 4:
			stop()
		}
	}
	dir := t.TempDir()
	code, out, errs := boss("inbox", "--wait", "--push", dir, "--timeout", "4s")
	require.Equal(t, 0, code, errs)
	var in struct {
		Judgments []inboxJudgment `json:"judgments"`
	}
	_, text, _ := boss("inbox", "--json")
	require.NoError(t, json.Unmarshal([]byte(text), &in), text)
	var few *inboxJudgment
	for i := range in.Judgments {
		if in.Judgments[i].Type == sprint.NFewReaders {
			few = &in.Judgments[i]
		}
	}
	require.NotNil(t, few, text)
	id := few.ID
	path := filepath.Join(dir, id+".md")
	assert.Contains(t, out, "INBOX OK pushed="+id+" file="+path+"\n", "one push, said once")
	file, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(file), "JUDGMENT "+id+" ! fewer than two readers up", string(file))
	assert.Contains(t, string(file), "\n  wait:\n    nova-sprint wait "+id+" --for 30m\n  notes: "+id+"\nclock: ", string(file))
	assert.Contains(t, sent, []string{"inbox", "--actor", "boss", "--json", "--open", id}, "the group was read whole through the server: %v", sent)
}
