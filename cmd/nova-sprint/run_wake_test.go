package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The run loop wakes on the log, not the clock (store/waitlog.go): the tests
// step an injected clock, and read the loop's own tick count and why each
// tick began, never the wall clock.

// loopTick is one tick of run as the loop told of it.
type loopTick struct {
	n     int
	began time.Time
	why   string
}

// wakeApp is the test app with run's waits on a quiet log scripted: each
// wait takes the next step of the world, if any, and else steps the clock by
// the whole wait.
type wakeApp struct {
	*testApp
	world []func(d time.Duration)
	ticks []loopTick
}

func newWakeApp(t *testing.T) *wakeApp {
	w := &wakeApp{testApp: newTestApp(t)}
	w.m.LogWait = func(d time.Duration) {
		if len(w.world) > 0 {
			step := w.world[0]
			w.world = w.world[1:]
			step(d)
			return
		}
		w.a.sleep(d)
	}
	w.a.ticked = func(n int, began time.Time, why string) { w.ticks = append(w.ticks, loopTick{n, began, why}) }
	return w
}

// run runs the loop for n ticks.
func (w *wakeApp) run(n int) {
	w.t.Helper()
	st, _, code := w.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(w.t, st, "run: %d", code)
	var out, errb bytes.Buffer
	w.a.runLoop(context.Background(), st, 20, n, &out, &errb)
	require.Empty(w.t, errb.String(), "run: %s", errb.String())
}

// firstTickAfter is the first tick that began at or after at.
func (w *wakeApp) firstTickAfter(at time.Time) (loopTick, bool) {
	for _, k := range w.ticks {
		if !k.began.Before(at) {
			return k, true
		}
	}
	return loopTick{}, false
}

// cardsOf is a member's work cards, ready and working, from queue --json.
func (w *wakeApp) cardsOf(m string) []queueCard {
	w.t.Helper()
	var q struct {
		Cards []queueCard `json:"cards"`
	}
	require.NoError(w.t, json.Unmarshal([]byte(w.ok("queue --as "+m+" --json")), &q))
	return q.Cards
}

// 8 machines of width 4, 3 streams of 30 ready: the first deal fills every
// machine to DealAhead times 4, 8. m1 takes its width, 4, and finishes them,
// its other 4 ready behind; the finish's line wakes the loop, and the tick it
// wakes refills m1 within TickFloor of the finish, with no tick of the clock
// between: m1 holds DealAhead times its width again.
func TestAFinishWakesTheLoopAndItsRoomIsDealtWithinTheFloor(t *testing.T) {
	t.Parallel()
	w := newWakeApp(t)
	members := make([]string, 8)
	for i := range members {
		members[i] = "m" + strconv.Itoa(i+1)
	}
	w.live = members
	var spec []string
	for _, m := range members {
		spec = append(spec, m+":4")
	}
	w.ok("init --readers reader-a,reader-b --members " + strings.Join(spec, ","))
	w.ok("add --stream a,b,c --count 30")
	w.ok("start")
	var finished time.Time
	atFinish := -1
	w.world = []func(time.Duration){
		func(time.Duration) {
			require.Equal(t, sprint.DealAhead*4, len(w.cardsOf("m1")), "the first deal gave m1 cards, want DealAhead times its width 4")
			w.a.sleep(30 * time.Millisecond)
			w.ok("take --as m1 --limit 4")
		},
		func(time.Duration) {
			w.a.sleep(250 * time.Millisecond)
			var ids []string
			for _, c := range w.cardsOf("m1") {
				if c.Col == "working" {
					ids = append(ids, c.ID+"@"+strconv.Itoa(c.Gen))
				}
			}
			require.Len(t, ids, 4, "m1 works %d cards, want its width 4", len(ids))
			w.ok("finish --as m1 " + strings.Join(ids, " "))
			finished, atFinish = w.a.now(), len(w.ticks)
			require.Equal(t, sprint.DealAhead*4-4, len(w.cardsOf("m1")), "after the finish m1 holds cards, want the %d ready behind its lanes", sprint.DealAhead*4-4)
		},
	}
	w.run(8)
	require.GreaterOrEqual(t, atFinish, 0, "the world never finished: ticks %v", w.ticks)
	k, ok := w.firstTickAfter(finished)
	require.True(t, ok, "no tick after the finish: %v", w.ticks)
	require.Equal(t, atFinish+1, k.n, "the tick after the finish is #%d (%s), want #%d woken by the log: %v", k.n, k.why, atFinish+1, w.ticks)
	require.Equal(t, tickLog, k.why, "the tick after the finish is #%d (%s), want #%d woken by the log: %v", k.n, k.why, atFinish+1, w.ticks)
	require.LessOrEqual(t, k.began.Sub(finished), store.TickFloor, "the finish waited for its tick, over the floor %s", store.TickFloor)
	cs := w.cardsOf("m1")
	require.Len(t, cs, sprint.DealAhead*4, "m1 holds %d cards after the finish's tick, want DealAhead times its width 4: %+v", len(cs), cs)
}

// A line on the log wakes the loop: a line TickFloor or more after the tick
// before began is ticked on at once; a line sooner waits for the floor. The
// clock is the loop's own count of ticks and the injected clock.
func TestALineWakesTheLoopWithinTheFloor(t *testing.T) {
	t.Parallel()
	w := newWakeApp(t)
	w.ok("init --readers reader-a,reader-b --members m1")
	w.ok("start")
	var soon, late time.Time
	w.world = []func(time.Duration){
		func(time.Duration) {
			w.a.sleep(30 * time.Millisecond)
			soon = w.a.now()
			w.ok("add --stream s1 --count 1")
		},
		// the add's tick deals the card: its lines wake the next tick, which
		// finds nothing; then the log is quiet
		func(time.Duration) {
			w.a.sleep(500 * time.Millisecond)
			late = w.a.now()
			w.ok("add --stream s2 --count 1")
		},
	}
	w.run(7)
	k, ok := w.firstTickAfter(soon)
	require.True(t, ok, "the first line: no tick woken by the log: %v", w.ticks)
	require.Equal(t, tickLog, k.why, "the first line: no tick woken by the log: %v", w.ticks)
	before := w.ticks[k.n-2]
	require.Equal(t, store.TickFloor, k.began.Sub(before.began), "a line 30ms after a tick began: the next began %s after it, want the floor %s: %v", k.began.Sub(before.began), store.TickFloor, w.ticks)
	require.LessOrEqual(t, k.began.Sub(soon), store.TickFloor, "a line 30ms after a tick began: the next began %s after it, want the floor %s: %v", k.began.Sub(before.began), store.TickFloor, w.ticks)
	k, ok = w.firstTickAfter(late)
	require.True(t, ok, "a line 500ms after a tick began: want a tick woken by the log at once: %v (line at %s)", w.ticks, late.Format("15:04:05.000"))
	require.Equal(t, tickLog, k.why, "a line 500ms after a tick began: want a tick woken by the log at once: %v (line at %s)", w.ticks, late.Format("15:04:05.000"))
	require.True(t, k.began.Equal(late), "a line 500ms after a tick began: want a tick woken by the log at once: %v (line at %s)", w.ticks, late.Format("15:04:05.000"))
}

// A quiet log ticks the loop once a second, by the clock. The first tick
// brings the member up, and its own lines wake one tick more (the tick after
// a change reads the state whole, as it always has); then the log is quiet.
func TestAQuietLogTicksOnceASecond(t *testing.T) {
	t.Parallel()
	w := newWakeApp(t)
	w.ok("init --readers reader-a,reader-b --members m1")
	w.ok("start")
	w.run(6)
	require.Len(t, w.ticks, 6, "ticks: %v", w.ticks)
	require.Equal(t, tickStart, w.ticks[0].why, "ticks: %v", w.ticks)
	require.Equal(t, tickLog, w.ticks[1].why, "ticks: %v", w.ticks)
	for i := 2; i < len(w.ticks); i++ {
		require.Equal(t, tickClock, w.ticks[i].why, "tick #%d on a quiet log: %s %s after the one before, want the clock at %s: %v",
			i+1, w.ticks[i].why, w.ticks[i].began.Sub(w.ticks[i-1].began), store.TickEvery, w.ticks)
		require.Equal(t, store.TickEvery, w.ticks[i].began.Sub(w.ticks[i-1].began), "tick #%d on a quiet log: %s %s after the one before, want the clock at %s: %v",
			i+1, w.ticks[i].why, w.ticks[i].began.Sub(w.ticks[i-1].began), store.TickEvery, w.ticks)
	}
}
