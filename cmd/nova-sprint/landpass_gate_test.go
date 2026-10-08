package main

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// gateCall is one gate the fake bench was asked to run: the bench, the stream whose
// worktree it gated (after the @ of the worktree's name) and the first run's words.
type gateCall struct {
	host, stream, argv string
	n                  int // the gate's place among the pass's gates: 0 is the base's
}

// hungGateRig is a land rig under the land loop with a fake bench, a fake clock and a fake
// timer, no socket and no real time: the clock moves only when the test moves it (jump), the
// gate bound's timer is a channel the test fires (fireBound), the bench answers from channels,
// never from a wall clock, and the loop's sleep parks it between cycles (nudge). The fake
// bench hangs the first batch gate of the stream named hang until its context ends, recording
// the cause, and answers every other gate green at once.
type hungGateRig struct {
	*landRig
	t        *testing.T
	hosts    []string // the up benches, in the fleet's order
	ctx      context.Context
	cancel   context.CancelFunc
	out      beatBuf
	clockMu  sync.Mutex
	now      time.Time
	boundMu  sync.Mutex
	bound    chan time.Time
	callsMu  sync.Mutex
	calls    []gateCall
	hung     bool
	cause    error
	entered  chan struct{}
	parked   chan struct{}
	wake     chan struct{}
	loopDone chan struct{}
}

// newHungGateRig queues one card a stream for streams s1..sN, each writing its own Go file on
// the module base, brings the benches up (and every other member down), and sets the seams.
func newHungGateRig(t *testing.T, streams int, benches []string, hang string) *hungGateRig {
	t.Helper()
	r := &hungGateRig{landRig: newLandRig(t), t: t, entered: make(chan struct{}), parked: make(chan struct{}), wake: make(chan struct{}), loopDone: make(chan struct{})}
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	heads := map[string]string{}
	var order []string
	for i := 1; i <= streams; i++ {
		stream := "s" + strconv.Itoa(i)
		r.ok("add --stream " + stream + " --count 1 --one")
		id := stream + "-1"
		heads[id] = r.card(id, map[string]string{stream + ".go": "package main\n\nfunc " + stream + "() {}\n"})
		order = append(order, id)
	}
	r.queued(heads, order...)
	for _, h := range benches {
		r.ok("fleet beat " + h + " --load 1 --cores 8")
		r.ok("fleet up " + h)
	}
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if !slices.Contains(benches, name) && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
	require.NoError(t, err)
	r.hosts = s.UpMembers()
	require.ElementsMatch(t, benches, r.hosts, "the benches up, in the fleet's order")

	r.now = r.a.now()
	r.a.now = func() time.Time {
		r.clockMu.Lock()
		defer r.clockMu.Unlock()
		return r.now
	}
	r.bound = make(chan time.Time)
	r.a.after = func(d time.Duration) <-chan time.Time {
		if d != gateBoundDefault {
			return make(chan time.Time) // no other timer of the loop fires in this test
		}
		r.boundMu.Lock()
		defer r.boundMu.Unlock()
		return r.bound
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	var once sync.Once
	b := r.a.landState()
	b.mu.Lock()
	b.hostName = func() (string, error) { return "studio.local", nil }
	b.landMore = []string{"--repo-dir", r.clone, "--base", "main"}
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		stream := dir[strings.LastIndex(dir, "@")+1:]
		r.callsMu.Lock()
		n := len(r.calls)
		r.calls = append(r.calls, gateCall{host: host, stream: stream, argv: strings.Join(runs[0], " "), n: n})
		hangThis := stream == hang && n > 0 && !r.hung // the first gate of the pass is the base's: never hung
		if hangThis {
			r.hung = true
		}
		r.callsMu.Unlock()
		if !hangThis {
			return "", 0, nil
		}
		once.Do(func() { close(r.entered) })
		<-ctx.Done()
		r.callsMu.Lock()
		r.cause = context.Cause(ctx)
		r.callsMu.Unlock()
		return "", 1, ctx.Err()
	}
	b.mu.Unlock()
	var sleepMu sync.Mutex
	r.a.sleep = func(time.Duration) {
		if !sleepMu.TryLock() {
			return // a beat inside a command this cycle runs
		}
		defer sleepMu.Unlock()
		if r.ctx.Err() != nil {
			return
		}
		select {
		case <-r.ctx.Done():
			return
		case r.parked <- struct{}{}:
		}
		select {
		case <-r.ctx.Done():
		case <-r.wake:
		}
	}
	return r
}

// start runs the land loop beside the test until the test cancels it.
func (r *hungGateRig) start() {
	go func() {
		r.a.landLoop(r.ctx, "mem:0", &r.out)
		close(r.loopDone)
	}()
}

// park waits for the loop to park between cycles; true when the loop ended instead.
func (r *hungGateRig) park() bool {
	r.t.Helper()
	select {
	case <-r.parked:
		return false
	case <-r.loopDone:
		return true
	case <-r.t.Context().Done():
		return true
	}
}

// nudge runs one more cycle of the loop; true when the loop ended instead.
func (r *hungGateRig) nudge() bool {
	r.t.Helper()
	select {
	case r.wake <- struct{}{}:
	case <-r.loopDone:
		return true
	case <-r.ctx.Done():
		return true
	}
	return r.park()
}

// until nudges the loop until cond holds; false when the loop ended or the test's context
// first. No clock: the loop's cycles are the wait.
func (r *hungGateRig) until(cond func() bool) bool {
	r.t.Helper()
	for !cond() {
		if r.nudge() {
			return false
		}
		runtime.Gosched()
	}
	return true
}

// waitEntered waits for the fake bench to hang the gate.
func (r *hungGateRig) waitEntered() {
	r.t.Helper()
	select {
	case <-r.entered:
	case <-r.loopDone:
		r.t.Fatalf("the loop ended before the fake bench saw the hung gate\n%s", r.out.String())
	case <-r.t.Context().Done():
		r.t.Fatalf("the test ended before the fake bench saw the hung gate\n%s", r.out.String())
	}
}

// jump moves the clock forward by d.
func (r *hungGateRig) jump(d time.Duration) {
	r.clockMu.Lock()
	r.now = r.now.Add(d)
	r.clockMu.Unlock()
}

// fireBound fires every gate bound timer handed out so far, and hands out a fresh one from
// here on, so the gates that come after are not bounded by it.
func (r *hungGateRig) fireBound() {
	r.boundMu.Lock()
	old := r.bound
	r.bound = make(chan time.Time)
	r.boundMu.Unlock()
	close(old)
}

// stop ends the loop and waits for it.
func (r *hungGateRig) stop() {
	r.cancel()
	select {
	case <-r.loopDone:
	case <-r.t.Context().Done():
	}
}

// gates is the gates the fake bench was asked to run so far, and the hung gate's cause.
func (r *hungGateRig) gates() ([]gateCall, error) {
	r.callsMu.Lock()
	defer r.callsMu.Unlock()
	return append([]gateCall(nil), r.calls...), r.cause
}

// landedOnMain says origin's main holds a landing of each id.
func (r *hungGateRig) landedOnMain(ids ...string) bool {
	log := strings.Join(r.mainLog(), "\n")
	for _, id := range ids {
		if !strings.Contains(log, "land "+id+" ") {
			return false
		}
	}
	return true
}

// One hung bench gate never holds the other streams' landings (landpass.go, the pass; the
// cold read of PR 5435 and the serial pass of 2026-10-07: three green streams sat unpushed
// for 45 minutes behind one stream's hung ssh, 27 batches behind one bench in the day).
// Four streams merge beside each other; the fourth's gate hangs on its bench. The three
// green ones land while it hangs: phase 2 drains the jobs as they finish, not after the
// slowest. At the gate bound the hung gate is abandoned on that bench with the exact line
// (`gate abandoned: <bench> after <t>`), its lane given back, and the batch gates again on
// the next bench of the ring, where it is green and lands; no card is blamed, and the
// batch's line names both benches. Every landed batch's report carries its land time.
func TestThreeGreenStreamsLandWhileAFourthsGateHangsAndItIsRegatedOnTheNextBench(t *testing.T) {
	t.Parallel()
	r := newHungGateRig(t, 4, []string{"vision", "space"}, "s4")
	ring := benchRing("s4", r.hosts)
	r.start()
	require.False(t, r.park(), "the loop ended before a beat\n%s", r.out.String())
	r.waitEntered()

	landed := r.until(func() bool { return r.landedOnMain("s1-1", "s2-1", "s3-1") })
	require.True(t, landed, "the three green streams did not land while the fourth's gate hung\n%s", r.out.String())
	calls, cause := r.gates()
	assert.Nil(t, cause, "the hung gate is still hung while the others land: %+v", calls)
	var s4 []gateCall // the hung stream's own gates: never the pass's first, the base's
	for _, c := range calls {
		if c.stream == "s4" && c.n > 0 {
			s4 = append(s4, c)
		}
	}
	require.Len(t, s4, 1, "one gate of s4 so far, hung: %+v", calls)
	assert.Equal(t, ring[0], s4[0].host, "the hung gate is on the stream's own slot of the ring")
	assert.False(t, r.landedOnMain("s4-1"), "the hung stream has not landed")

	r.jump(gateBoundDefault)
	r.fireBound()
	finished := r.until(func() bool {
		text := r.out.String()
		return strings.Contains(text, "LAND OK stream=s4") || strings.Contains(text, "LAND REFUSED") || strings.Contains(text, "LAND FAILED")
	})
	r.stop()
	text := r.out.String()
	require.True(t, finished, "the pass did not finish after the bound fired\n%s", text)

	calls, cause = r.gates()
	assert.True(t, errors.Is(cause, errGateBound), "the hung gate's context ended with the bound as its cause: %v", cause)
	s4 = s4[:0]
	for _, c := range calls {
		if c.stream == "s4" && c.n > 0 {
			s4 = append(s4, c)
		}
	}
	// the hung gate, then the gate again on the next bench; a base gate at the tip the
	// three landings moved may follow, in phase 2 (again), as for any batch
	require.GreaterOrEqual(t, len(s4), 2, "the hung gate, then the gate again on the next bench: %+v", calls)
	assert.Equal(t, []string{ring[0], ring[1]}, []string{s4[0].host, s4[1].host}, "abandoned on the stream's slot, gated again on the next slot of the ring: %+v", calls)

	assert.Contains(t, text, "NOTE tree gate: gate abandoned: "+ring[0]+" after 8m0s", "the exact abandoned line, said as a NOTE of the batch:\n%s", text)
	for _, stream := range []string{"s1", "s2", "s3", "s4"} {
		assert.Regexp(t, regexp.MustCompile(`LAND OK stream=`+stream+` cards=1 `), text, "every stream landed:\n%s", text)
	}
	assert.Regexp(t, regexp.MustCompile(`LAND OK stream=s4 .* bench=`+ring[0]+`\+`+ring[1]+` wall=\d+\.\ds`), text, "the batch's line names both benches:\n%s", text)
	assert.Equal(t, 4, strings.Count(text, "NOTE landed at "), "each landed batch's report says when it landed:\n%s", text)
	assert.NotContains(t, text, "LAND REFUSED", "no batch was refused:\n%s", text)
	assert.NotContains(t, text, "fact=", "no fact was recorded against a card:\n%s", text)
	assert.NotContains(t, r.ok("lane list"), "lander", "the lander holds and waits on no lane after")
	r.clean()
}

// LandDeadline cancels the pass's gate context when it fires, so the stuck judgment is also
// an action (landloop.go, raiseIfStuck): the gate hung on the bench is abandoned with the
// fact on the batch's line, the batch waits for the next pass and no card is blamed; the
// next pass gates it again and it lands.
func TestLandDeadlineCancelsTheGateContextAndBlamesNoCard(t *testing.T) {
	t.Parallel()
	r := newHungGateRig(t, 1, []string{"vision"}, "s1")
	r.start()
	require.False(t, r.park(), "the loop ended before a beat\n%s", r.out.String())
	r.waitEntered()
	calls, cause := r.gates()
	require.Len(t, calls, 2, "the base's gate, then the batch's, hung: %+v", calls)
	assert.Nil(t, cause, "the gate hangs until the deadline: %+v", calls)

	r.jump(LandDeadline)
	refused := r.until(func() bool {
		return strings.Contains(r.out.String(), "LAND REFUSED") || strings.Contains(r.out.String(), "LAND FAILED")
	})
	require.True(t, refused, "the deadline did not end the hung gate\n%s", r.out.String())
	landed := r.until(func() bool {
		return strings.Contains(r.out.String(), "LAND OK stream=s1") || strings.Contains(r.out.String(), "LAND FAILED")
	})
	r.stop()
	text := r.out.String()
	require.True(t, landed, "the next pass did not land the batch\n%s", text)

	_, cause = r.gates()
	assert.True(t, errors.Is(cause, errLandDeadline), "the hung gate's context ended with the deadline as its cause: %v", cause)
	refusedLine := regexp.MustCompile(`LAND REFUSED stream=s1 cards=1 [^\n]*`).FindString(text)
	require.NotEmpty(t, refusedLine, "the batch was refused for this pass:\n%s", text)
	assert.Contains(t, refusedLine, "reason=gate abandoned: vision after 10m0s", "the refusal is the abandoned line: %s", refusedLine)
	assert.NotContains(t, refusedLine, "fact=", "no fact was recorded: %s", refusedLine)
	assert.Contains(t, text, "NOTE tree gate: gate abandoned: vision after 10m0s", "the abandoned line is said:\n%s", text)
	assert.Regexp(t, regexp.MustCompile(`LAND OK stream=s1 cards=1 .* bench=vision wall=\d+\.\ds`), text, "the next pass landed it on the bench:\n%s", text)
	inbox := r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, sprint.NOpStuck), "one stuck judgment:\n%s", inbox)
	assert.NotContains(t, text, "fails the tree gate", "no head was blamed:\n%s", text)
	r.clean()
}
