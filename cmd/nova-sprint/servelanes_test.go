package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// The server's lanes (servelanes.go; docs/SPEC-SPRINT.md section 14, The server;
// tla/ServerLanes.tla). On 2026-10-04 every verb the server answered took its one line
// of control, and a friend's beat waited minutes behind ticks, batches and the
// dashboards' reads. These tests step the server in one process on the in-memory store
// with no socket and no clock: a verb that waits for the line is seen by the line's
// waiting hook, never by a timer.

// laneRig is a server over the in-memory store with a friend, amy, on its roster and two
// cards dealt to m1.
func laneRig(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.a.friends = friendRows("amy")
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("friend sync --root " + t.TempDir())
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.deal(2)
	ta.a.serveAddr = "mem:0"
	return ta
}

// serveOne is one verb sent to the server, from the fleet (local false) or this machine.
func serveOne(a *app, local bool, argv ...string) sprintwire.Result {
	return a.serveFrom(sprintwire.Request{Verbs: [][]string{argv}}, local).Results[0]
}

// The measured fault, pinned: a land in progress (its git, its check, its push: a slow
// land injected at the push) holds nothing a verb needs. A friend's beat, the
// coordinator's reads and a worker's queue are each answered while the landing waits,
// none of them waiting for the line, and the landing then ends as it would have.
func TestVerbAnsweredWhileLandInProgress(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.a.friends = friendRows("amy")
	r.ok("friend sync --root " + t.TempDir())
	r.ok("add --stream s1 --count 1 --one")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	r.ok("start")
	r.a.serveAddr = "mem:0"
	r.a.serial.waiting = func() { t.Error("a verb waited for the line while the landing ran its git") }
	atPush, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.a.beforePush = func(int) {
		once.Do(func() { close(atPush) })
		<-release // the slow land: its push waits until the test has its answers
	}
	landed := make(chan string, 1)
	go func() {
		var out bytes.Buffer
		code := r.a.landRound(context.Background(), "mem:0", []string{"--repo-dir", r.clone, "--base", "main"}, &out)
		landed <- strings.Repeat("x", code) + out.String()
	}()
	<-atPush
	beat := serveOne(r.a, false, "friend", "beat", "amy")
	require.Equal(t, 0, beat.Code, beat.Stderr)
	assert.Contains(t, beat.Stdout, "FRIEND-BEAT OK amy")
	where := serveOne(r.a, true, "where", "--json")
	require.Equal(t, 0, where.Code, where.Stderr)
	assert.Contains(t, where.Stdout, `"landed"`)
	card := serveOne(r.a, true, "card", "s1-1")
	require.Equal(t, 0, card.Code, card.Stderr)
	queue := serveOne(r.a, false, "queue", "--as", "m1")
	require.Equal(t, 0, queue.Code, queue.Stderr)
	close(release)
	out := <-landed
	require.False(t, strings.HasPrefix(out, "x"), "the landing failed: %s", out)
	assert.Contains(t, out, "LAND OK")
	// while RUNNING, the next tick drains what the landing reported
	r.a.serial.Lock()
	r.ok("tick")
	r.a.serial.Unlock()
	assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
	assert.Equal(t, 1, r.a.served.beats, "the beat ran on the beat lane")
	assert.Equal(t, 2, r.a.served.reads, "where and card ran on the read lane")
	r.clean()
}

// While the line is held (a tick of 6 s, as at 2:18 PM on 2026-10-04, or any batch), a
// friend's beat and every read are answered without waiting for it; a worker's write
// waits for the line as before, and is answered once it is free.
func TestBeatsAndReadsAnswerWhileTheLineIsHeld(t *testing.T) {
	t.Parallel()
	ta := laneRig(t)
	require.NotNil(t, ta.a.lanesFor(context.Background()), "the in-memory store runs the lanes")
	waited := make(chan struct{}, 8)
	ta.a.serial.waiting = func() { waited <- struct{}{} }
	ta.a.serial.Lock() // the tick
	for _, v := range []struct {
		local bool
		argv  []string
		says  string
	}{
		{false, []string{"friend", "beat", "amy"}, "FRIEND-BEAT OK amy"},
		{true, []string{"where"}, "s1"},
		{true, []string{"where", "--json", "--cards"}, `"cards"`},
		{true, []string{"card", "s1-1"}, "s1-1"},
		{true, []string{"inbox"}, ""},
		{true, []string{"log", "--card", "s1-1"}, "s1-1"},
	} {
		res := serveOne(ta.a, v.local, v.argv...)
		require.Equal(t, 0, res.Code, "%v: %s", v.argv, res.Stderr)
		assert.Contains(t, res.Stdout, v.says, "%v", v.argv)
	}
	require.Empty(t, waited, "no beat or read waited for the line")
	// the reversed witness: a write still takes the line, and waits for the tick
	done := make(chan sprintwire.Result, 1)
	go func() { done <- serveOne(ta.a, false, "queue", "--as", "m1") }()
	<-waited
	ta.a.serial.Unlock()
	res := <-done
	require.Equal(t, 0, res.Code, res.Stderr)
	// inbox --read moves the coordinator's cursor: a write, on the line
	go func() { done <- serveOne(ta.a, true, "inbox", "--read", "--actor", "coordinator") }()
	res = <-done
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Equal(t, 1, ta.a.served.beats)
	assert.Equal(t, 5, ta.a.served.reads)
	assert.Equal(t, 2, ta.a.served.serialVerbs)
}

// A batch whose caller went away while it waited for the line is not run: on
// 2026-10-04 every friend beat that had timed out was still run when its turn came, and
// the line never drained. The take waits behind the held line, its caller goes, and
// the card is still ready to take after the line is free.
func TestABatchWhoseCallerWentIsNotRun(t *testing.T) {
	t.Parallel()
	ta := laneRig(t)
	ta.a.lanesFor(context.Background())
	waiting := make(chan struct{})
	ta.a.serial.waiting = func() { close(waiting) }
	ta.a.serial.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan sprintwire.Response, 1)
	take := ta.withEpoch([]string{"take", "--as", "m1", "--max", "2"})
	go func() {
		done <- ta.a.serveCtx(ctx, sprintwire.Request{Verbs: [][]string{take, {"queue", "--as", "m1"}}}, false)
	}()
	<-waiting
	cancel()
	res := <-done
	ta.a.serial.Unlock()
	require.Len(t, res.Results, 2)
	for _, r := range res.Results {
		assert.Equal(t, 2, r.Code)
		assert.Contains(t, r.Stderr, "its caller went away while it waited; nothing was changed")
	}
	assert.Equal(t, 2, ta.a.served.gone)
	ta.a.serial.waiting = nil
	again := serveOne(ta.a, false, take...)
	require.Equal(t, 0, again.Code, again.Stderr)
	assert.Contains(t, again.Stdout, "s1-1", "the take that went away took nothing: the cards were still there to take")
}

// A friend's beat on the beat lane answers as the verb does on the store: a friend
// the roster lacks is refused, exit 1, and nothing is written.
func TestTheBeatLaneRefusesAFriendTheRosterLacks(t *testing.T) {
	t.Parallel()
	ta := laneRig(t)
	res := serveOne(ta.a, false, "friend", "beat", "zed")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stderr, "nova-sprint friend beat:")
	assert.Equal(t, 1, ta.a.served.beats)
	code, out, errs := ta.do("friend beat zed")
	assert.Equal(t, 1, code)
	assert.Equal(t, out, res.Stdout)
	assert.Equal(t, errs, res.Stderr, "the lane and the verb refuse alike")
}

// A twin file is written whole after every verb by the process that holds it, so on a
// twin file every verb, a read too, takes the line, as before the lanes.
func TestATwinFileTakesTheLineForEveryVerb(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1:2")
	require.Nil(t, r.a.lanesFor(context.Background()))
	waiting := make(chan struct{})
	r.a.serial.waiting = func() { close(waiting) }
	r.a.serial.Lock()
	done := make(chan sprintwire.Result, 1)
	go func() { done <- serveOne(r.a, true, "where") }()
	<-waiting
	r.a.serial.Unlock()
	res := <-done
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Zero(t, r.a.served.reads)
}

// The server says what its batches cost once a minute (a SERVE line on run's stdout):
// the evidence the next slow line is read from.
func TestServeSaysWhatItsBatchesCost(t *testing.T) {
	t.Parallel()
	ta := laneRig(t)
	var log bytes.Buffer
	ta.a.serveLog = &log
	serveOne(ta.a, false, "friend", "beat", "amy")
	serveOne(ta.a, true, "where")
	assert.Empty(t, log.String(), "nothing is said before the minute")
	ta.a.sleep(ServeSayEvery)
	serveOne(ta.a, false, "queue", "--as", "m1")
	line := log.String()
	assert.Contains(t, line, " SERVE batches=3 beat-lane=1 read-lane=1 on-line=1 gone=0 ")
	assert.Contains(t, line, "held-by=queue+--as+m1")
	log.Reset()
	serveOne(ta.a, false, "friend", "beat", "amy")
	assert.Empty(t, log.String(), "the tally starts again after its line")
}

// The line waits in the order its waiters came, can be given up, and is never held by
// a waiter that gave up.
func TestTheSerialLineIsGivenUpByACallerThatWent(t *testing.T) {
	t.Parallel()
	var l serialLock
	l.Lock()
	assert.False(t, l.TryLock())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, l.LockCtx(ctx), "a caller already gone does not wait")
	l.Unlock()
	require.NoError(t, l.LockCtx(context.Background()))
	assert.Panics(t, func() { l.Unlock(); l.Unlock() })
	assert.Error(t, l.LockCtx(ctx), "a free line is not taken by a caller that has gone")
	assert.True(t, l.TryLock(), "and is still free")
	l.Unlock()
}

// A beat or a read whose caller has gone before it started is not run either
// (tla/ServerLanes.tla GoneNeverRuns, where TLC found the first cut's beat lane writing
// the beat of a caller that had gone).
func TestTheLanesRunNothingForACallerThatWent(t *testing.T) {
	t.Parallel()
	ta := laneRig(t)
	ta.a.lanesFor(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := ta.a.serveCtx(ctx, sprintwire.Request{Verbs: [][]string{{"friend", "beat", "amy"}, {"where"}}}, true)
	for _, r := range res.Results {
		assert.Equal(t, 2, r.Code)
		assert.Contains(t, r.Stderr, "its caller went away while it waited; nothing was changed")
	}
	where := serveOne(ta.a, true, "where")
	require.Equal(t, 0, where.Code, where.Stderr)
	assert.NotContains(t, where.Stdout, "amy     |     0 |       0 |     2 |    0 | 0.0% | up", "amy never beat")
}
