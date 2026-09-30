//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/redis/go-redis/v9"
)

// The dirty-driven tick driven on a store (the owner's shape of 2026-09-30:
// the work pump once a tick, readers, merge and fleet settled until their
// queues are empty; tla/DirtyTick.tla): three streams of a thousand cards, no
// gates and no needs, eight machines of width 64, the world played with no
// chances at 10 ms, the loop ticking on the log, a coordinator reading the
// inbox once for each tick that addressed them. Every card lands; the streams
// land together over time; the machines do equal shares; a tick that addressed
// the coordinator wrote one note and an idle tick none; the tick's own queue
// always started the next tick. The wall time and the ticks are printed.
const (
	driveStreams   = 3
	drivePerStream = 1000
	driveMembers   = 8
	driveWidth     = 64
	driveSample    = 50 // ticks between two samples of the streams
)

// driveTick is one tick of the loop as it was told of: what started it, what
// it did, and what it left in the work queue.
type driveTick struct {
	n      int
	why    string
	idle   bool
	parts  int
	order  int
	end    int // the tick-end note's count, 0 when none was written
	queued int // entries in the work queue when the tick ended
	err    string
	wall   time.Duration
}

// driveSampleAt is the landed count of each stream, every driveSample ticks.
type driveSampleAt struct {
	tick   int
	landed [driveStreams]int
}

func TestTheDirtyTickDriveOnAStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx, stop := context.WithTimeout(context.Background(), 12*time.Minute)
	defer stop()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	getenv := func(k string) string { return env[k] }
	world, coord, loop := newApp(getenv), newApp(getenv), newApp(getenv)
	defer world.close()
	defer coord.close()
	defer loop.close()
	do := func(a *app, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := a.run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: %d %s", args, code, errb.String())
		}
		return out.String()
	}
	var members, spec []string
	for i := 1; i <= driveMembers; i++ {
		members = append(members, "m"+strconv.Itoa(i))
		spec = append(spec, members[i-1]+":"+strconv.Itoa(driveWidth))
	}
	streams := []string{"a", "b", "c"}
	total := driveStreams * drivePerStream
	do(world, "init", "--readers", "reader-a,reader-b,reader-c", "--members", strings.Join(spec, ","))
	do(world, "add", "--stream", strings.Join(streams, ","), "--count", strconv.Itoa(drivePerStream))
	for _, m := range members {
		do(world, "fleet", "beat", m)
	}
	do(world, "start")

	st, _, code := loop.machineVerb("run", nil, &bytes.Buffer{})
	if st == nil {
		t.Fatalf("run: %d", code)
	}

	// the coordinator: once for each tick that addressed them, the inbox is
	// read and its cursor moved; a judgment is not expected, none is open to
	// answer, and any that comes is recorded
	var (
		mu         sync.Mutex
		notesSeen  = map[string]int{}
		judgments  []string
		coordReads int
	)
	wake := make(chan struct{}, 1)
	coordDone := make(chan struct{})
	go func() {
		defer close(coordDone)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-wake:
				if !ok {
					return
				}
			}
			var out, errb bytes.Buffer
			if code := coord.run([]string{"inbox", "--read", "--json"}, &out, &errb); code != 0 {
				mu.Lock()
				judgments = append(judgments, fmt.Sprintf("inbox: %d %s", code, errb.String()))
				mu.Unlock()
				continue
			}
			var v struct {
				Groups []sprint.Group `json:"groups"`
			}
			if err := json.Unmarshal(out.Bytes(), &v); err != nil {
				mu.Lock()
				judgments = append(judgments, "inbox json: "+err.Error())
				mu.Unlock()
				continue
			}
			mu.Lock()
			coordReads++
			for _, g := range v.Groups {
				notesSeen[g.Kind+": "+g.Type] += g.Count
				if g.Kind == sprint.Judgment {
					judgments = append(judgments, fmt.Sprintf("%s (%s) on %v", g.Type, g.Stream, g.Primaries))
				}
			}
			mu.Unlock()
		}
	}()

	// the loop: run's own, with every tick's result kept
	var ticks []driveTick
	var samples []driveSampleAt
	sampleNow := func(n int) {
		shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work)})
		if err != nil || len(shapes) == 0 {
			return
		}
		j := shapes[0].Column(sprint.Landed)
		var s driveSampleAt
		s.tick = n
		for _, r := range shapes[0].Rows {
			for i, name := range streams {
				if r.Key == name && j >= 0 && j < len(r.Cells) {
					s.landed[i] = int(r.Cells[j].Count)
				}
			}
		}
		samples = append(samples, s)
	}
	first := make(chan struct{})
	loopDone := make(chan struct{})
	lctx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(loopDone)
		cursor, _ := st.LogTail(lctx)
		why := tickStart
		for i := 1; lctx.Err() == nil; i++ {
			began := time.Now()
			res, err := st.Tick(lctx)
			q, _ := st.B.QueueRead(lctx)
			tk := driveTick{n: i, why: why, idle: res.Idle, parts: len(res.Parts), order: len(res.Order), end: res.TickEnd, queued: len(q), wall: time.Since(began)}
			if err != nil && lctx.Err() == nil {
				tk.err = err.Error()
			}
			ticks = append(ticks, tk)
			if i%driveSample == 0 {
				sampleNow(i)
			}
			if i == 1 {
				close(first)
			}
			if res.TickEnd > 0 {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			if err != nil {
				loop.sleep(store.TickEvery)
				why = tickRetry
				continue
			}
			if res.Done != "" {
				return
			}
			cursor, why = loop.pace(lctx, st, res.Epoch, cursor, began)
		}
	}()
	<-first

	began := time.Now()
	var pout, perr bytes.Buffer
	code = world.run([]string{"play", "--every", "10ms"}, &pout, &perr)
	wall := time.Since(began)
	// let the loop's last ticks say the sprint is done, then stop it
	for i := 0; i < 100; i++ {
		select {
		case <-loopDone:
			i = 100
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	<-loopDone
	close(wake)
	<-coordDone
	if code != 0 {
		t.Fatalf("play: %d\n%s\n%s", code, tail(pout.String(), 30), perr.String())
	}

	// ---- what the drive asserts -------------------------------------------------
	snap, err := st.Load(ctx, store.All, nil)
	if err != nil {
		t.Fatal(err)
	}
	landed := 0
	for _, s := range streams {
		landed += snap.Work.Count(s, sprint.Landed)
	}
	if landed != total {
		t.Fatalf("%d of %d landed", landed, total)
	}

	// the streams land together: no stream ahead of another by more than 10% of
	// the total at any sample, and the last sample's spread is printed
	maxSpread, at := 0, 0
	for _, s := range samples {
		lo, hi := s.landed[0], s.landed[0]
		for _, n := range s.landed[1:] {
			lo, hi = min(lo, n), max(hi, n)
		}
		if hi-lo > maxSpread {
			maxSpread, at = hi-lo, s.tick
		}
	}
	if limit := total / 10; maxSpread > limit {
		t.Errorf("a stream was %d cards ahead of another at tick %d, over 10%% of the total (%d)", maxSpread, at, limit)
	}

	// every machine's work is within 5% of the mean
	shape, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
	if err != nil || len(shape) == 0 {
		t.Fatalf("the fleet table: %v", err)
	}
	col := shape[0].Column(sprint.DoneOK)
	done := map[string]int{}
	sum := 0
	for _, r := range shape[0].Rows {
		if col >= 0 && col < len(r.Cells) {
			done[r.Key] = int(r.Cells[col].Count)
			sum += done[r.Key]
		}
	}
	mean := float64(sum) / float64(len(members))
	var doneLine []string
	for _, m := range members {
		doneLine = append(doneLine, fmt.Sprintf("%s=%d", m, done[m]))
		if d := float64(done[m]) - mean; d > mean*0.05 || -d > mean*0.05 {
			t.Errorf("%s did %d cards, the mean is %.1f: over 5%% off", m, done[m], mean)
		}
	}
	if sum == 0 {
		t.Errorf("no machine's done count is on the fleet table")
	}

	// the tick-end notes: one for each tick that addressed the coordinator, none
	// for an idle tick; every tick of the loop ended, none failed
	wroteNote, idle, didSomething, settle, maxOrder := 0, 0, 0, 0, 0
	whyCount := map[string]int{}
	var slowest time.Duration
	for i, tk := range ticks {
		whyCount[tk.why]++
		if tk.err != "" {
			t.Errorf("tick %d failed: %s", tk.n, tk.err)
		}
		if tk.end > 0 {
			wroteNote++
			if tk.idle {
				t.Errorf("tick %d was idle and wrote a tick-end note", tk.n)
			}
		}
		if tk.idle {
			idle++
		} else {
			didSomething++
		}
		if tk.order > 5 {
			settle++
		}
		maxOrder = max(maxOrder, tk.order)
		slowest = max(slowest, tk.wall)
		// W12: the queue a tick leaves starts the next tick, never the clock
		if tk.queued > 0 && i+1 < len(ticks) && ticks[i+1].why != tickLog {
			t.Errorf("tick %d left %d entries in the work queue and tick %d began on %q, not the log", tk.n, tk.queued, tk.n+1, ticks[i+1].why)
		}
	}
	notes, _, err := st.B.NotesSince(ctx, "", 1000000)
	if err != nil {
		t.Fatal(err)
	}
	tickEnds := 0
	for _, n := range notes {
		if n.Type == sprint.NTickEnd {
			tickEnds++
		}
	}
	if tickEnds != wroteNote {
		t.Errorf("%d tick-end notes on the inbox and %d ticks that addressed the coordinator: want one each", tickEnds, wroteNote)
	}
	mu.Lock()
	reads, judged := coordReads, append([]string(nil), judgments...)
	var seen []string
	for k, n := range notesSeen {
		seen = append(seen, fmt.Sprintf("%s x%d", k, n))
	}
	mu.Unlock()
	sort.Strings(seen)
	if len(judged) > 0 {
		t.Errorf("the coordinator met %d judgments in a sprint with no gates, needs or chances: %v", len(judged), judged)
	}

	report := fmt.Sprintf("DIRTY-TICK DRIVE: %d cards in %d streams on %d machines of width %d: all landed in %s over %d ticks (%.1f ticks/s)\n"+
		"  the loop's ticks began on: %v; %d idle, %d did something, %d needed more than the four first updates (most updates in one tick: %d); the slowest tick took %s\n"+
		"  tick-end notes: %d (one for each of the %d ticks that addressed the coordinator), the coordinator read the inbox %d times: %s\n"+
		"  the streams' widest gap at a sample: %d cards (tick %d) of a limit of %d; done by machine: %s (mean %.1f)",
		total, driveStreams, driveMembers, driveWidth, wall.Round(time.Millisecond), len(ticks), float64(len(ticks))/wall.Seconds(),
		whyCount, idle, didSomething, settle, maxOrder, slowest.Round(time.Millisecond),
		tickEnds, wroteNote, reads, strings.Join(seen, ", "),
		maxSpread, at, total/10, strings.Join(doneLine, " "), mean)
	fmt.Fprintln(os.Stderr, report)
	t.Log("\n" + report)
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
