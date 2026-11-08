package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The trace check of tla/Bus.tla: seeded walks run the real hook over the fake store, with sends, lost
// read replies, failed writes, signals after a write, failed acks and failed wakes landing at random,
// and a restart after each. At every boundary the walk records the model action and the model state
// read off the fake (sent, delivered, pend, acked) and off what the hook did (up, mode, cur, stage,
// printed). TestBusTrace renders the walks as tla/BusLog.tla and requires the committed file to be the
// same; BusTrace.tla checks every walk is a behaviour of Bus.tla. BUS_UPDATE_LOG=1 rewrites the log.

// busState is Bus.tla's state (skip is empty: it is the "noretry" witness's).
type busState struct {
	sent, delivered, cur, crashes, fails int
	pend, acked, printed                 []int
	up, badPrint                         bool
	mode, stage                          string
}

type busRec struct {
	action string
	s      busState
}

// busWalk is the fake store as one walk sees it: Read and Ack are the fake's, with the walk's events
// landing at them.
type busWalk struct {
	*fake
	t       *testing.T
	rng     *rand.Rand
	broken  string // "newfirst": a read of the pending list returns nothing
	sends   int    // sends left
	crashes int    // crashes left to inject
	fails   int    // failed wakes left to inject
	cancel  func()
	stop    bool     // the walk is over: the hook was stopped with nothing left
	batch   []string // the rest of a pending batch, in the order the hook handles it
	st      busState
	recs    []busRec
	failW   bool // the next write fails
	sigW    bool // a signal arrives after the next write
}

func idNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(id, "-0")) // ignored: the fake's ids are <n>-0
	return n
}

func (w *busWalk) rec(action string) {
	s := w.st
	s.sent, s.delivered = len(w.entries["bus:beta"]), w.delivered["bus:beta"]
	s.pend = nil
	for _, id := range w.pending["bus:beta"] {
		s.pend = append(s.pend, idNum(id))
	}
	s.pend = slices.Sorted(slices.Values(s.pend))
	s.acked, s.printed = slices.Clone(w.st.acked), slices.Clone(w.st.printed)
	w.recs = append(w.recs, busRec{action, s})
}

func (w *busWalk) chance(p float64) bool { return w.rng.Float64() < p }

// send is the send verb's XADD; one in five has no subject, an entry the hook acks as HOOK BAD.
func (w *busWalk) send() {
	if w.chance(0.2) {
		_, err := w.fake.Add(context.Background(), "bus:beta", map[string]string{"from": "alpha"})
		require.NoError(w.t, err)
	} else {
		send(w.t, w.fake, fmt.Sprint("s", len(w.entries["bus:beta"])+1), "b")
	}
	w.sends--
	w.rec("Send")
}

func (w *busWalk) take(id, action string) {
	w.st.cur, w.st.stage = idNum(id), "read"
	w.rec(action)
}

func (w *busWalk) Read(ctx context.Context, stream string, pending bool, block time.Duration) ([]Msg, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if pending {
		var msgs []Msg
		if w.broken != "newfirst" {
			msgs, _ = w.fake.Read(ctx, stream, true, block) // ignored: the fake's pending read never fails
		}
		if len(msgs) == 0 {
			w.st.mode = "new"
			w.rec("ReadPending")
			return nil, nil
		}
		w.batch = nil
		for _, m := range msgs[1:] {
			w.batch = append(w.batch, m.ID)
		}
		w.take(msgs[0].ID, "ReadPending")
		return msgs, nil
	}
	for w.sends > 0 && w.chance(0.4) {
		w.send()
	}
	if len(w.entries[stream]) == w.delivered[stream] {
		if w.sends == 0 {
			w.stop = true
			w.cancel()
			return nil, ctx.Err()
		}
		w.send()
	}
	lose := w.crashes > 0 && w.chance(0.15)
	if lose {
		w.failRead = 1
	}
	msgs, err := w.fake.Read(ctx, stream, false, block)
	w.take(w.pending[stream][len(w.pending[stream])-1], "ReadNew")
	if err == nil {
		w.failW = w.crashes > 0 && w.chance(0.12)
		w.sigW = !w.failW && w.crashes > 0 && w.chance(0.12)
	}
	return msgs, err
}

func (w *busWalk) Ack(ctx context.Context, stream, id string) (int64, error) {
	if w.crashes > 0 && w.chance(0.15) {
		w.failAck = 1
	}
	n, err := w.fake.Ack(ctx, stream, id)
	if err != nil || n != 1 {
		return n, err
	}
	w.st.acked = slices.Sorted(slices.Values(append(w.st.acked, idNum(id))))
	w.st.cur, w.st.stage = 0, "read"
	w.rec("Ack")
	if len(w.batch) > 0 {
		next := w.batch[0]
		w.batch = w.batch[1:]
		w.take(next, "ReadPending")
	}
	return n, nil
}

// Write is the hook's stdout: a line, a receipt or HOOK BAD delivers the entry in hand.
func (w *busWalk) Write(p []byte) (int, error) {
	if w.failW {
		w.failW = false
		return 0, errors.New("write: broken pipe")
	}
	s := string(p)
	id, _, _ := strings.Cut(s, "\t")
	if _, after, ok := strings.Cut(s, " id="); ok {
		id, _, _ = strings.Cut(strings.TrimSuffix(after, "\n"), " ")
	}
	require.Equal(w.t, w.st.cur, idNum(id), "the hook delivered %q with %d in hand", s, w.st.cur)
	w.st.badPrint = w.st.badPrint || slices.Contains(w.st.acked, w.st.cur)
	if !slices.Contains(w.st.printed, w.st.cur) {
		w.st.printed = slices.Sorted(slices.Values(append(w.st.printed, w.st.cur)))
	}
	w.st.stage = "printed"
	w.rec("Print")
	if w.sigW {
		w.sigW = false
		w.cancel()
	}
	return len(p), nil
}

// busWaker fails a wake now and then (a wake that surely did not arrive) and leaves one unconfirmed.
type busWaker struct{ w *busWalk }

func (b busWaker) wake(context.Context, Msg) error {
	switch {
	case b.w.fails > 0 && b.w.chance(0.3):
		return errors.New("socket gone")
	case b.w.chance(0.1):
		return unconfirmed{errors.New("cut off")}
	}
	return nil
}

// busRun is one walk: the hook runs until it stops, is restarted after every crash and failed wake,
// and the walk ends once every send is made and the hook has nothing left.
func busRun(t *testing.T, seed uint64, wakes bool, broken string) []busRec {
	w := &busWalk{fake: newFake(), t: t, rng: rand.New(rand.NewPCG(seed, 1)), broken: broken, sends: 6, crashes: 4, fails: 2}
	w.st = busState{up: true, mode: "pending", stage: "read"}
	w.rec("Init")
	for w.sends > 2 && w.chance(0.5) {
		w.send()
	}
	var wk waker
	if wakes {
		wk = busWaker{w}
	}
	for {
		w.failW, w.sigW, w.failRead, w.failAck = false, false, 0, 0
		ctx, cancel := context.WithCancel(context.Background())
		w.cancel = cancel
		err := hook(ctx, w, "beta", testValue, w.fake, w, io.Discard, func(int64) {}, wk, nil)
		cancel()
		if w.stop {
			return w.recs
		}
		var wf wakeFailed
		if errors.As(err, &wf) {
			w.fails--
			w.st.fails++
			w.st.cur, w.st.up = 0, false
			w.rec("FailWake")
		} else {
			require.Error(t, err)
			w.crashes--
			w.st.crashes++
			w.st.cur, w.st.stage, w.st.up = 0, "read", false
			w.rec("Crash")
		}
		w.st.up, w.st.mode = true, "pending"
		w.rec("Restart")
	}
}

func busLog(walks map[string][][]busRec) string {
	set := func(xs []int) string {
		var s []string
		for _, x := range xs {
			s = append(s, strconv.Itoa(x))
		}
		return "{" + strings.Join(s, ", ") + "}"
	}
	render := func(ws [][]busRec) string {
		var out []string
		for _, recs := range ws {
			var rows []string
			for _, r := range recs {
				s := r.s
				rows = append(rows, fmt.Sprintf("B(%q, %d, %d, %s, %s, %s, %q, %d, %q, %d, %d, %s, %s)", r.action, s.sent, s.delivered,
					set(s.pend), set(s.acked), strings.ToUpper(strconv.FormatBool(s.up)), s.mode, s.cur, s.stage, s.crashes, s.fails,
					set(s.printed), strings.ToUpper(strconv.FormatBool(s.badPrint))))
			}
			out = append(out, "<<\n        "+strings.Join(rows, ",\n        ")+"\n    >>")
		}
		return "<<\n    " + strings.Join(out, ",\n    ") + "\n>>"
	}
	return `--------------------------------- MODULE BusLog ---------------------------------
(* The walks trace_bus_test.go took over the real hook and the fake store: per walk, each step's      *)
(* action and the state after it (sent, delivered, pend, acked, up, mode, cur, stage, crashes, fails, *)
(* printed, badPrint). BrokenNewFirst is a walk over a store whose read of the pending list returns   *)
(* nothing. Written by the test (BUS_UPDATE_LOG=1); BusTrace.tla checks each walk is a behaviour of    *)
(* Bus.tla.                                                                                           *)
B(a, s, d, p, k, u, m, c, g, x, f, pr, bp) ==
    [action |-> a, sent |-> s, delivered |-> d, pend |-> p, acked |-> k, up |-> u, mode |-> m, cur |-> c,
     stage |-> g, crashes |-> x, fails |-> f, printed |-> pr, badPrint |-> bp]
Walks == ` + render(walks["Walks"]) + `
BrokenNewFirst == ` + render(walks["BrokenNewFirst"]) + `
=============================================================================
`
}

// checkLog compares a rendered log with the committed file, or rewrites it when env is 1.
func checkLog(t *testing.T, file, env, got, test string) {
	t.Helper()
	if os.Getenv(env) == "1" {
		require.NoError(t, os.WriteFile(file, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(file)
	require.NoError(t, err)
	if string(want) != got {
		t.Errorf("the walks of the real code are not the ones %s holds: the code and the model have drifted. "+
			"Change both together, then rewrite the log and run TLC on it; run: %s=1 go test -run %s ./cmd/nova-bus && tests/tla/check.sh <jar> cmd/nova-bus/tla", file, env, test)
	}
}

func TestBusTrace(t *testing.T) {
	t.Parallel()
	walks := map[string][][]busRec{}
	seen := map[string]bool{}
	for seed := uint64(1); seed <= 8; seed++ {
		recs := busRun(t, seed, seed%2 == 0, "")
		walks["Walks"] = append(walks["Walks"], recs)
		for _, r := range recs {
			seen[r.action] = true
		}
	}
	for _, a := range []string{"Send", "ReadPending", "ReadNew", "Print", "FailWake", "Ack", "Crash", "Restart"} {
		require.True(t, seen[a], "no walk takes %s", a)
	}
	walks["BrokenNewFirst"] = [][]busRec{busRun(t, 3, false, "newfirst")}
	checkLog(t, "tla/BusLog.tla", "BUS_UPDATE_LOG", busLog(walks), "TestBusTrace")
}
