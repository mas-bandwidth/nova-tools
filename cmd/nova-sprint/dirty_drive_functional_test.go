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

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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
	driveSample    = 50 // ticks between two samples of the streams (every tick before the 50th)
	// MaxTickWall is the gate every tick of the drive is held to, the owner's
	// law of 2026-09-30: "the whole intent is sub-second ticks. This is a
	// requirement." A tick over it fails the drive, named with its parts,
	// where the drive runs against a bench's store (GateStoreEnv).
	MaxTickWall = time.Second
	// GateStoreEnv, set to 1, says the drive runs against a real store on a
	// bench, and the wall-clock bound is asserted: the certification tier
	// sets it (.github/workflows/certification.yml, tick-gate). Everywhere
	// else the bound is printed, not asserted: the functional container is
	// not the store, its two CPUs shared by the redis-server, the loop and the
	// world driver playing every 10 ms, whose operations hold the fence the
	// loop's parts wait behind. The invariants (every card landed, no table
	// read whole after the first tick, the twin never off the store's counts)
	// are asserted everywhere.
	GateStoreEnv = "NOVA_SPRINT_GATE_STORE"
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
	took   time.Duration    // the tick's own wall time (TickResult.Took): the gate's
	times  []store.PartTime // what the tick spent its time on, part by part
	cost   store.PartTime   // the tick's cost summed: its trips, whole-table reads, records, mismatches
	load   string           // the machine's load averages as the tick ended
	said   []string         // what the tick said once (a NOTE): why a table was read whole
	routes int64            // the round trips of the tick's one read of the routes
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
	err := fn.Load(ctx, c)
	require.NoError(t, err)
	// three routes of the tier the cards are (a brief naming none is flash), as
	// nova-config's apply writes them: every deal draws, and the gate holds with it
	for i, name := range []string{"flash-a", "flash-b", "flash-c"} {
		require.NoError(t, c.SAdd(ctx, config.RoutesKey, name).Err())
		require.NoError(t, c.HSet(ctx, config.RouteKey(name), "name", name, "tier", "flash", "provider", "prov"+strconv.Itoa(i),
			"model", "m"+strconv.Itoa(i), "tokens", "100000", "deadline", "900", "weight", strconv.Itoa(i+1), "enabled", "true").Err())
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	getenv := func(k string) string { return env[k] }
	world, coord, loop, machines := newApp(getenv), newApp(getenv), newApp(getenv), newApp(getenv)
	defer world.close()
	defer coord.close()
	defer loop.close()
	defer machines.close()
	do := func(a *app, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		require.Zero(t, code, "%v: %d %s", args, code, errb.String())
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
	require.NotNil(t, st, "run: %d", code)

	// the coordinator: once for each tick that addressed them, the inbox is
	// read and its cursor moved; a judgment is not expected, none is open to
	// answer, and any that comes is recorded
	var (
		mu                     sync.Mutex
		notesSeen              = map[string]int{}
		judgments              []string
		coordReads             int
		accepts, acceptRefused int
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
			mu.Unlock()
			for _, g := range v.Groups {
				mu.Lock()
				notesSeen[g.Kind+": "+g.Type] += g.Count
				mu.Unlock()
				if g.Kind != sprint.Judgment {
					continue
				}
				switch g.Type {
				case sprint.NNoMember:
					// the first tick's pump deals before its fleet update brings the
					// machines up: the judgment closes itself when they are
				case sprint.NReadyToAccept:
					// accepted mechanically, with the command the inbox gives
					accepted := false
					for _, cmd := range g.Commands {
						if cmd.Decision != "accept" || len(cmd.Lines) == 0 {
							continue
						}
						words := strings.Fields(cmd.Lines[0])
						if len(words) < 2 {
							continue
						}
						var o, e bytes.Buffer
						code := coord.run(words[1:], &o, &e)
						mu.Lock()
						if code == 0 {
							accepts++
						} else {
							acceptRefused++
						}
						mu.Unlock()
						accepted = true
						break
					}
					if !accepted {
						mu.Lock()
						judgments = append(judgments, fmt.Sprintf("%s (%s) offers no accept", g.Type, g.Stream))
						mu.Unlock()
					}
				default:
					mu.Lock()
					judgments = append(judgments, fmt.Sprintf("%s (%s) on %v", g.Type, g.Stream, g.Primaries))
					mu.Unlock()
				}
			}
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
		fmt.Fprintf(os.Stderr, "drive: tick %d landed %v at %s\n", n, s.landed, time.Now().Format("15:04:05.000"))
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
			tk := driveTick{n: i, why: why, idle: res.Idle, parts: len(res.Parts), order: len(res.Order), end: res.TickEnd, queued: len(q), wall: time.Since(began), took: res.Took, times: res.Times, cost: res.Cost(), load: machineLoad(), said: res.Said, routes: res.RouteTrips}
			if err != nil && lctx.Err() == nil {
				tk.err = err.Error()
			}
			if m := res.Cost().Mismatch; m > 0 && tk.err == "" {
				// the loop's twin did not add up to the store's own counts
				tk.err = fmt.Sprintf("the twin read %d tables whole on a count mismatch: %s", m, res.TimesLine())
			}
			ticks = append(ticks, tk)
			// a sample every driveSample ticks, and at every tick while the loop
			// has ticked fewer times than that: a tick that deals or lands
			// hundreds of cards is one tick of the loop
			if i%driveSample == 0 || i < driveSample {
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

	// the machines' own beat loops: a real machine beats from its own process,
	// whatever its work loop is waiting for (the world's play beats each tick
	// too, and waits on the fence like every verb)
	go func() {
		poll := time.NewTicker(time.Second)
		defer poll.Stop()
		for {
			select {
			case <-lctx.Done():
				return
			case <-poll.C:
			}
			for _, m := range members {
				machines.run([]string{"fleet", "beat", m}, &bytes.Buffer{}, &bytes.Buffer{})
			}
		}
	}()

	// the watchdog reads through a store of its own: its whole reads are its
	// own, not counted as the loop's (the gate counts the loop's)
	watch := &store.Store{B: st.B, Names: st.Names, Actor: st.Actor, Now: st.Now, NewID: st.NewID, Sleep: st.Sleep}
	// a stall watchdog: the landed count not moving for a minute of polls ends
	// the drive with the tables, the inbox and the check printed
	stalled := make(chan string, 1)
	go func() {
		poll := time.NewTicker(time.Second)
		defer poll.Stop()
		last, still := -1, 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
			}
			n := 0
			if snap, err := watch.Load(ctx, store.All, nil); err == nil {
				for _, s := range streams {
					n += snap.Work.Count(s, sprint.Landed)
				}
			}
			if n != last {
				last, still = n, 0
			}
			if still++; n < total && still > 60 {
				var b strings.Builder
				for _, args := range [][]string{{"where"}, {"inbox"}, {"check"}} {
					var out, errb bytes.Buffer
					code := coord.run(args, &out, &errb)
					fmt.Fprintf(&b, "%v: %d\n%s%s\n", args, code, tail(out.String(), 60), errb.String())
				}
				stalled <- fmt.Sprintf("%d of %d landed and nothing moved for a minute:\n%s", n, total, b.String())
				return
			}
		}
	}()

	began := time.Now()
	var pout, perr bytes.Buffer
	played := make(chan int, 1)
	go func() {
		played <- world.run([]string{"play", "--every", "10ms", "--broken", "0", "--fail", "0", "--stuck", "0", "--cross", "0", "--down", "0", "--up", "0"}, &pout, &perr)
	}()
	select {
	case code = <-played:
	case why := <-stalled:
		cancel()
		t.Fatalf("the sprint stalled: %s", why)
	case <-ctx.Done():
		cancel()
		t.Fatalf("the sprint did not land in 12 minutes")
	}
	wall := time.Since(began)
	// let the loop's last tick say the sprint is done, then stop it
	select {
	case <-loopDone:
	case <-time.After(30 * time.Second):
	}
	cancel()
	<-loopDone
	close(wake)
	<-coordDone
	require.Zero(t, code, "play: %d\n%s\n%s", code, tail(pout.String(), 30), perr.String())

	// ---- what the drive asserts -------------------------------------------------
	snap, err := st.Load(ctx, store.All, nil)
	require.NoError(t, err)
	landed := 0
	for _, s := range streams {
		landed += snap.Work.Count(s, sprint.Landed)
	}
	require.Equal(t, total, landed, "%d of %d landed", landed, total)

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
	assert.NotEmpty(t, samples, "the loop took no sample of the streams")
	limit := total / 10
	assert.LessOrEqual(t, maxSpread, limit, "a stream was %d cards ahead of another at tick %d, over 10%% of the total (%d)", maxSpread, at, limit)

	// every machine's work is within 5% of the mean of the machines that were
	// never down during the run; a machine that lapsed is reported, with the
	// seconds it was down, and not asserted
	shape, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
	require.NoError(t, err, "the fleet table: %v", err)
	require.NotEmpty(t, shape, "the fleet table: %v", err)
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
	allNotes, _, err := st.B.NotesSince(ctx, "", 1000000)
	require.NoError(t, err)
	end := time.Now()
	downs, downFor := map[string]int{}, map[string]time.Duration{}
	since := map[string]time.Time{}
	for _, n := range allNotes {
		if n.Type != sprint.NMemberDown && n.Type != sprint.NMemberUp {
			continue
		}
		w := strings.Fields(n.What)
		if len(w) == 0 {
			continue
		}
		m := w[0]
		switch n.Type {
		case sprint.NMemberDown:
			downs[m]++
			since[m] = n.At
		case sprint.NMemberUp:
			if at, ok := since[m]; ok {
				downFor[m] += n.At.Sub(at)
				delete(since, m)
			}
		}
	}
	for m, at := range since {
		downFor[m] += end.Sub(at)
	}
	steady, steadySum := []string{}, 0
	for _, m := range members {
		if downs[m] == 0 {
			steady = append(steady, m)
			steadySum += done[m]
		}
	}
	var doneLine []string
	var table strings.Builder
	for _, m := range members {
		note := "never down"
		if downs[m] > 0 {
			note = fmt.Sprintf("down %d times, %.1f s down: reported, not asserted", downs[m], downFor[m].Seconds())
		}
		doneLine = append(doneLine, fmt.Sprintf("%s=%d", m, done[m]))
		fmt.Fprintf(&table, "    %-4s done %4d  %s\n", m, done[m], note)
	}
	if len(steady) > 0 {
		steadyMean := float64(steadySum) / float64(len(steady))
		for _, m := range steady {
			d := float64(done[m]) - steadyMean
			assert.LessOrEqual(t, d, steadyMean*0.05, "%s did %d cards, the mean of the machines never down is %.1f: over 5%% off", m, done[m], steadyMean)
			assert.LessOrEqual(t, -d, steadyMean*0.05, "%s did %d cards, the mean of the machines never down is %.1f: over 5%% off", m, done[m], steadyMean)
		}
	}
	assert.NotZero(t, sum, "no machine's done count is on the fleet table")

	// the tick-end notes: one for each tick that addressed the coordinator, none
	// for an idle tick; every tick of the loop ended, none failed
	wroteNote, idle, didSomething, settle, maxOrder := 0, 0, 0, 0, 0
	whyCount := map[string]int{}
	var slowest time.Duration
	for i, tk := range ticks {
		whyCount[tk.why]++
		assert.Empty(t, tk.err, "tick %d failed: %s", tk.n, tk.err)
		if tk.end > 0 {
			wroteNote++
			assert.False(t, tk.idle, "tick %d was idle and wrote a tick-end note", tk.n)
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
		if tk.queued > 0 && i+1 < len(ticks) {
			assert.Equal(t, tickLog, ticks[i+1].why, "tick %d left %d entries in the work queue and tick %d began on %q, not the log", tk.n, tk.queued, tk.n+1, ticks[i+1].why)
		}
	}
	tickEnds := 0
	for _, n := range allNotes {
		if n.Type == sprint.NTickEnd {
			tickEnds++
		}
	}
	assert.Equal(t, wroteNote, tickEnds, "%d tick-end notes on the inbox and %d ticks that addressed the coordinator: want one each", tickEnds, wroteNote)
	mu.Lock()
	reads, judged, accepted, refused := coordReads, append([]string(nil), judgments...), accepts, acceptRefused
	var seen []string
	for k, n := range notesSeen {
		seen = append(seen, fmt.Sprintf("%s x%d", k, n))
	}
	mu.Unlock()
	sort.Strings(seen)
	assert.Empty(t, judged, "the coordinator met %d judgments in a sprint with no gates, needs or chances: %v", len(judged), judged)

	// THE GATE: every tick under MaxTickWall; after the first tick the loop
	// reads no table whole (its twin catches up from the change streams), and
	// its twin never disagrees with the store's counts
	var over []string
	var sumTook, maxTook time.Duration
	whole, routeTrips, routeMax := int64(0), int64(0), int64(0)
	var loads, why []string
	for i, tk := range ticks {
		routeTrips += tk.routes
		routeMax = max(routeMax, tk.routes)
		sumTook += tk.took
		maxTook = max(maxTook, tk.took)
		if i > 0 {
			whole += tk.cost.Reads
		}
		for _, n := range tk.said {
			why = append(why, fmt.Sprintf("tick %d: %s", tk.n, n))
		}
		if tk.took > MaxTickWall && tk.err == "" {
			var parts []string
			for _, pt := range tk.times {
				if pt.Took >= 50*time.Millisecond {
					parts = append(parts, fmt.Sprintf("%s/%s %s %dt", pt.Table, pt.Name, pt.Took.Round(time.Millisecond), pt.Trips))
				}
			}
			over = append(over, fmt.Sprintf("tick %d took %s (load %s): %s", tk.n, tk.took.Round(time.Millisecond), tk.load, strings.Join(parts, ", ")))
		}
		if i%10 == 0 {
			loads = append(loads, tk.load)
		}
	}
	gate := fmt.Sprintf("THE GATE: %d ticks, mean %s, max %s, %d over %s; whole-table reads after the first tick %d; the routes read (3 routes): %d round trips, at most %d a tick; machine load %s",
		len(ticks), (sumTook / time.Duration(max(len(ticks), 1))).Round(time.Millisecond), maxTook.Round(time.Millisecond), len(over), MaxTickWall, whole, routeTrips, routeMax, strings.Join(loads, " | "))
	fmt.Fprintln(os.Stderr, gate)
	for _, o := range over {
		if os.Getenv(GateStoreEnv) == "1" {
			assert.Fail(t, fmt.Sprintf("over the gate of %s: %s", MaxTickWall, o))
		} else {
			fmt.Fprintf(os.Stderr, "NOTE over the gate of %s (asserted only against a bench's store, %s=1): %s\n", MaxTickWall, GateStoreEnv, o)
		}
	}
	assert.LessOrEqual(t, routeMax, int64(2), "a tick read the routes in %d round trips: once a tick, SMEMBERS and one pipeline", routeMax)
	assert.Zero(t, whole, "the loop read %d tables whole after its first tick: its twin did not catch up; it said: %s", whole, strings.Join(why, "; "))
	for _, w := range why {
		fmt.Fprintln(os.Stderr, "NOTE "+w)
	}

	report := fmt.Sprintf("DIRTY-TICK DRIVE: %d cards in %d streams on %d machines of width %d: all landed in %s over %d ticks (%.1f ticks/s)\n"+
		"  the loop's ticks began on: %v; %d idle, %d did something, %d needed more than the four first updates (most updates in one tick: %d); the slowest tick took %s\n"+
		"  tick-end notes: %d (one for each of the %d ticks that addressed the coordinator), the coordinator read the inbox %d times (accepted %d groups, %d refused as already accepted by the machine): %s\n"+
		"  the streams' widest gap over %d samples: %d cards (tick %d) of a limit of %d; done by machine: %s (mean %.1f)\n  per machine (never-down machines asserted within 5%% of their own mean):\n%s",
		total, driveStreams, driveMembers, driveWidth, wall.Round(time.Millisecond), len(ticks), float64(len(ticks))/wall.Seconds(),
		whyCount, idle, didSomething, settle, maxOrder, slowest.Round(time.Millisecond),
		tickEnds, wroteNote, reads, accepted, refused, strings.Join(seen, ", "),
		len(samples), maxSpread, at, total/10, strings.Join(doneLine, " "), mean, strings.TrimRight(table.String(), "\n"))
	for _, tk := range ticks {
		if tk.wall == slowest {
			var parts []string
			for _, pt := range tk.times {
				name := pt.Name
				if pt.Table != "" {
					name = pt.Table + " " + name
				}
				parts = append(parts, fmt.Sprintf("%s %s", name, pt.Took.Round(time.Millisecond)))
			}
			report += fmt.Sprintf("\n  the slowest tick (%d) part by part: %s", tk.n, strings.Join(parts, ", "))
			break
		}
	}
	fmt.Fprintln(os.Stderr, report)
	t.Log("\n" + report + "\n  " + gate)
}

// machineLoad is the machine's load averages, as the kernel says them in
// /proc/loadavg ("" where there is none): the gate is read beside them.
func machineLoad() string {
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 3 {
			return strings.Join(f[:3], " ")
		}
	}
	return "" // the container's kernel says it in /proc; no other is asked
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
