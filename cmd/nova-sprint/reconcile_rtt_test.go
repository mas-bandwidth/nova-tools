package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// dutyTag carries the meter of the duty a command was sent for.
type dutyTag struct{}

// dutyMeter is one duty's round trips in one pass: every command batch the
// client sends (one command, or one pipeline) is one round trip.
type dutyMeter struct {
	open   atomic.Bool
	mu     sync.Mutex
	rounds int
	cmds   map[string]int
}

// rttHook counts each duty's round trips and holds every round trip for rtt,
// the fleet store's round trip from the reconciler's host.
type rttHook struct{ rtt time.Duration }

func (h rttHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h rttHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.round(ctx, []redis.Cmder{cmd})
		return next(ctx, cmd)
	}
}

func (h rttHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.round(ctx, cmds)
		return next(ctx, cmds)
	}
}

func (h rttHook) round(ctx context.Context, cmds []redis.Cmder) {
	if m, _ := ctx.Value(dutyTag{}).(*dutyMeter); m != nil && m.open.Load() {
		m.mu.Lock()
		m.rounds++
		for _, c := range cmds {
			m.cmds[strings.ToLower(c.Name())]++
		}
		m.mu.Unlock()
	}
	if h.rtt > 0 {
		time.Sleep(h.rtt)
	}
}

// liveShape is the fleet store's size, from the 2026-09-23 Redis audit
// (DBSIZE 6,357; 4,474 tasks in one sprint's set; 11 benches) scaled by k.
type liveShape struct {
	openSprints, closedSprints, harvested, ended, units, pooled int
	streams, done, working, merging, waiting, ready             int
	benches                                                     int
}

func liveCounts(k int) liveShape {
	return liveShape{
		openSprints: 6 * k, closedSprints: 24 * k, harvested: 60, ended: 200, units: 20, pooled: 30,
		streams: 6 * k, done: 700, working: 8, merging: 2, waiting: 20, ready: 5,
		benches: 11,
	}
}

var liveFriends = []string{"emma", "johnny", "rowan", "stella", "jev"}

// seedLive writes shape into c in pipelined batches.
func seedLive(t *testing.T, ctx context.Context, c *redis.Client, sh liveShape) {
	t.Helper()
	pipe := c.Pipeline()
	flush := func() {
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	now := time.Now()
	const repo = "mas-bandwidth/nova-tools"
	for _, f := range liveFriends {
		pipe.SAdd(ctx, "friends", f)
		pipe.HSet(ctx, "friend:"+f, "at", now.UTC().Format(time.RFC3339))
		pipe.Set(ctx, "friend:"+f+":beat", "1", 0)
		pipe.Set(ctx, "friend:"+f+":slots", "2", 0)
		pipe.ZAdd(ctx, "friend:"+f+":cards:working", redis.Z{Score: 1, Member: f + "-w1"}, redis.Z{Score: 2, Member: f + "-w2"})
	}
	pipe.SAdd(ctx, "readers", "emma", "johnny", "stella")
	pipe.HSet(ctx, "friend:rowan:roles", "roles", "coordinator")
	for i := 0; i < sh.benches; i++ {
		b := fmt.Sprintf("live-b%02d", i)
		pipe.SAdd(ctx, "benches", b)
		pipe.HSet(ctx, "bench:"+b+":state", "state", "UP", "at", "1")
		pipe.HSet(ctx, "bench:"+b+":beat", "host", b, "user", "nova", "at", "1")
		pipe.HSet(ctx, "bench:"+b+":desired", "slots", "4")
		// Every slot leased: the deal sweep reads the pools and plans nothing.
		for j := 0; j < 4; j++ {
			pipe.ZAdd(ctx, "bench:"+b+":living", redis.Z{Score: 1, Member: fmt.Sprintf("%s-slot-%d", b, j)})
		}
	}
	pipe.SAdd(ctx, reconcile.BasesKey, "nova-tools/dev")
	pipe.HSet(ctx, civerdict.TipKey("nova-tools", "dev"), "sha", "0123456789abcdef0123456789abcdef01234567")
	pipe.HSet(ctx, reconcile.CIRecordKey("nova-tools", "0123456789abcdef0123456789abcdef01234567"), civerdict.Field, "OK")
	flush()
	streams := make([]string, sh.streams)
	pr := 1000
	for i := range streams {
		s := fmt.Sprintf("live-stream-%02d", i)
		streams[i] = s
		pipe.ZAdd(ctx, "ws:order", redis.Z{Score: float64(i), Member: s})
		pipe.SAdd(ctx, "ws:names", s)
		add := func(where string, n int, fields ...any) {
			for j := 0; j < n; j++ {
				id := fmt.Sprintf("%s-%s-%04d", s, where, j)
				pipe.ZAdd(ctx, "ws:"+s+":"+where, redis.Z{Score: float64(j), Member: id})
				args := append([]any{"state", where, "where", where, "stream", s, "created_at", "1"}, fields...)
				if where == "working" || where == "merging" {
					pr++
					args = append(args, "pr", fmt.Sprint(pr), "repo", repo)
					pipe.HSet(ctx, prkey.Key(repo, pr), "head", fmt.Sprintf("%040x", pr), "state", "open")
				}
				pipe.HSet(ctx, "task:"+id, args...)
			}
			flush()
		}
		add("done", sh.done)
		add("working", sh.working)
		add("merging", sh.merging)
		add("waiting", sh.waiting, "blocked_on", fmt.Sprintf("%s#%d", repo, 99000+i))
		add("ready", sh.ready, "who", "any", "kind", "build")
	}
	for i := 0; i < sh.openSprints+sh.closedSprints; i++ {
		S := fmt.Sprintf("live-sprint-%03d", i)
		pipe.SAdd(ctx, "sprints", S)
		if i >= sh.openSprints {
			pipe.HSet(ctx, "s:"+S, "status", "closed")
			continue
		}
		pipe.HSet(ctx, "s:"+S, "status", "open", "stream", streams[i%len(streams)])
		pipe.HSet(ctx, "s:"+S+":policy", "share", "1", "backpressure_missing", "open")
		pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: float64(i), Member: S})
		for j := 0; j < sh.pooled; j++ {
			l := fmt.Sprintf("q-%04d", j)
			pipe.HSet(ctx, "s:"+S+":card:"+l, "state", "queued", "priority", "5", "attempt", "0", "retries", "0")
			pipe.ZAdd(ctx, "s:"+S+":pool", redis.Z{Score: float64(j), Member: l})
			pipe.SAdd(ctx, "s:"+S+":idx:card:queued", l)
		}
		for j := 0; j < sh.harvested; j++ {
			l := fmt.Sprintf("h-%04d", j)
			pipe.HSet(ctx, "s:"+S+":card:"+l, "state", "harvested", "pr", fmt.Sprint(5000+j), "repo", repo, "head", fmt.Sprintf("%040x", j))
			pipe.SAdd(ctx, "s:"+S+":idx:card:harvested", l)
		}
		for j := 0; j < sh.ended; j++ {
			l := fmt.Sprintf("e-%04d", j)
			pipe.HSet(ctx, "s:"+S+":card:"+l, "state", "ended", "outcome", "ok", "reason", "done", "exit", "0", "retries", "0")
			pipe.SAdd(ctx, "s:"+S+":idx:card:ended", l)
		}
		for j := 0; j < sh.units; j++ {
			u := fmt.Sprintf("u-%03d", j)
			pipe.SAdd(ctx, "s:"+S+":units", u)
			pipe.HSet(ctx, "s:"+S+":u:"+u, "head", fmt.Sprintf("%040x", j), "state", "landed", "repo", repo, "pr", fmt.Sprint(7000+j))
		}
		flush()
	}
}

// dutyRun is one duty's measure in one pass.
type dutyRun struct {
	name   string
	took   time.Duration
	rounds int
	cmds   map[string]int
	err    error
}

// measured is one run of reconciler passes.
type measured struct {
	runs    [][]dutyRun // per pass, per duty in pass order, then "(loop)"
	results []reconcile.PassResult
	procMS  string // proc:reconciler took_ms after the last pass
}

// measurePasses runs `passes` reconciler passes of the production duties
// over a store seeded at shape, every round trip held for rtt, one pass per
// interval. sweep makes every sweep due every pass: the refill's deal and
// each sprint's expire sweep.
func measurePasses(t *testing.T, sh liveShape, rtt, interval time.Duration, passes int, sweep bool) measured {
	t.Helper()
	addr := startThrowawayRedis(t)
	ctx := context.Background()
	seedClient := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = seedClient.Close() })
	if err := fn.Load(ctx, seedClient); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	seedLive(t, ctx, seedClient, sh)
	old := reconcileSweep
	t.Cleanup(func() { reconcileSweep = old })
	if sweep {
		for i := 0; i < sh.openSprints; i++ {
			seedClient.HSet(ctx, fmt.Sprintf("s:live-sprint-%03d:policy", i), "expire_every_ms", "1")
		}
		reconcileSweep = time.Nanosecond
	}

	ssh := &verbSSH{}
	seams, hseams, prober := reconcileSeams, consumeHarvestSeams, expireProber
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return ssh, verbForge{} }
	consumeHarvestSeams = func() (harvest.Forge, harvest.Pusher) {
		return &consumeForge{prs: map[string]harvest.PR{}}, &consumePusher{pushes: map[string]int{}}
	}
	expireProber = func() reconcile.Prober { return nil }
	t.Cleanup(func() { reconcileSeams, consumeHarvestSeams, expireProber = seams, hseams, prober })

	c := redis.NewClient(&redis.Options{Addr: addr, PoolSize: 64})
	c.AddHook(rttHook{rtt: rtt})
	st := store.New(c)
	t.Cleanup(func() { _ = c.Close() })
	duties, names, stops, err := productionDuties(st, nil)
	if err != nil {
		t.Fatalf("productionDuties: %v", err)
	}
	t.Cleanup(func() { stopDuties(&syncBuffer{}, stops) })
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "rtt-host"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var mu sync.Mutex
	var m measured
	cur := []dutyRun{}
	wrapped := make([]reconcile.Duty, len(duties))
	for i, d := range duties {
		d, name := d, names[i]
		wrapped[i] = func(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
			dm := &dutyMeter{cmds: map[string]int{}}
			dm.open.Store(true)
			began := time.Now()
			cnt, err := d(context.WithValue(ctx, dutyTag{}, dm), l)
			took := time.Since(began)
			dm.open.Store(false)
			dm.mu.Lock()
			mu.Lock()
			cur = append(cur, dutyRun{name: name, took: took, rounds: dm.rounds, cmds: dm.cmds, err: err})
			mu.Unlock()
			dm.mu.Unlock()
			return cnt, err
		}
	}
	loopMeter := &dutyMeter{cmds: map[string]int{}}
	loopMeter.open.Store(true)
	loop := &reconcile.Loop{Lease: lease, Duties: wrapped, Names: names, Passes: passes, Interval: interval,
		AfterPass: func(r reconcile.PassResult) {
			mu.Lock()
			defer mu.Unlock()
			loopMeter.mu.Lock()
			cur = append(cur, dutyRun{name: "(loop)", rounds: loopMeter.rounds, cmds: loopMeter.cmds})
			loopMeter.rounds, loopMeter.cmds = 0, map[string]int{}
			loopMeter.mu.Unlock()
			m.runs, cur = append(m.runs, cur), []dutyRun{}
			m.results = append(m.results, r)
		}}
	if err := loop.Run(context.WithValue(ctx, dutyTag{}, loopMeter)); err != nil {
		t.Fatalf("loop: %v", err)
	}
	if len(m.results) != passes {
		t.Fatalf("recorded %d passes, want %d", len(m.results), passes)
	}
	m.procMS = seedClient.HGet(ctx, reconcile.ProcKey, "took_ms").Val()
	return m
}

func formatRuns(runs []dutyRun) string {
	var b strings.Builder
	for _, r := range runs {
		var cs []string
		for k, v := range r.cmds {
			cs = append(cs, fmt.Sprintf("%s=%d", k, v))
		}
		sort.Strings(cs)
		fmt.Fprintf(&b, "  %-16s rounds=%-4d took=%-8s err=%v cmds=%s\n", r.name, r.rounds, r.took.Round(time.Millisecond), r.err, strings.Join(cs, ","))
	}
	return b.String()
}

// fleetRTT is the fleet store's round trip from the host the reconciler runs
// on (the Studio): `redis-cli --latency` averaged 83.0 ms in the 2026-09-23
// Redis batching audit (rowan-new reports), ICMP 81.3 ms. At that round trip
// a one second pass holds about twelve round trips in all.
const fleetRTT = 83 * time.Millisecond

// steady is the passes a check reads: every pass after the restart pass and
// the one after it, whose names the first pass had not yet read (the first
// pass after a restart reads every name once, a few round trips per duty).
const steady = 2

// noScan fails on any SCAN-family or KEYS command a duty sent.
func noScan(t *testing.T, m measured) {
	t.Helper()
	for i, pass := range m.runs {
		for _, r := range pass {
			for name := range r.cmds {
				switch name {
				case "scan", "sscan", "hscan", "zscan", "keys":
					t.Errorf("pass %d: %s sent %s: no duty walks the keyspace", i, r.name, name)
				}
			}
		}
	}
}

// TestReconcilePassUnderOneSecondAtLiveCounts is nova-tools #3831's
// DONE-WHEN: at the fleet store's key counts (six open sprints of 60
// harvested and 200 ended cards and a pool of 30, 24 closed sprints, six
// streams of 735 tasks, 11 benches, five friends) and the fleet's 83 ms
// round trip, every pass after the restart takes under a second on its own
// record (proc:reconciler took_ms), with every sweep due (the refill's deal
// and every sprint's expire sweep, the heaviest pass the loop makes), every
// duty under a second in one round trip (the refill two: its own and the
// deal's read), and no duty sending SCAN or KEYS. On dev the route duty
// alone made 420 round trips a pass here (one ns_route_read per harvested
// card), 35 s at 83 ms: the 10,694 ms pass of #3802 was the route duty and
// the per-sprint reads as much as any ssh.
func TestReconcilePassUnderOneSecondAtLiveCounts(t *testing.T) {
	m := measurePasses(t, liveCounts(1), fleetRTT, reconcile.DefaultInterval, steady+2, true)
	noScan(t, m)
	budget := map[string]int{"refill": 2}
	for i := steady; i < len(m.runs); i++ {
		res := m.results[i]
		detail := fmt.Sprintf("pass %d took %s err=%q\n%s", i, res.Took.Round(time.Millisecond), res.Err, formatRuns(m.runs[i]))
		if res.Took >= reconcile.PassBar {
			t.Errorf("pass over the bar at the fleet round trip: %s", detail)
		}
		for _, r := range m.runs[i] {
			want := 1
			if b, ok := budget[r.name]; ok {
				want = b
			}
			if r.name == "(loop)" {
				continue
			}
			if r.rounds > want || r.took >= reconcile.PassBar {
				t.Errorf("duty %s: %d round trip(s) in %s; want at most %d, under %s: %s", r.name, r.rounds, r.took.Round(time.Millisecond), want, reconcile.PassBar, detail)
			}
		}
		t.Logf("%s", detail)
	}
	if ms, err := strconv.Atoi(m.procMS); err != nil || ms >= int(reconcile.PassBar.Milliseconds()) {
		t.Errorf("proc:reconciler took_ms=%q after the last pass; want under %d", m.procMS, reconcile.PassBar.Milliseconds())
	}
}

// TestReconcileRoundTripsConstantInKeyCounts is #3831's rule, one pipeline
// or one Redis Function call per duty, no per-key round trips: each duty's
// round trips on an idle pass are the same at three times the live key
// counts (three times the sprints and streams) as at the live counts, and
// at most one.
func TestReconcileRoundTripsConstantInKeyCounts(t *testing.T) {
	rounds := map[int]map[string]int{}
	for _, k := range []int{1, 3} {
		m := measurePasses(t, liveCounts(k), 0, 10*time.Millisecond, steady+2, false)
		noScan(t, m)
		rounds[k] = map[string]int{}
		for i := steady; i < len(m.runs); i++ {
			for _, r := range m.runs[i] {
				if r.name == "(loop)" {
					continue
				}
				if r.rounds > 1 {
					t.Errorf("k=%d pass %d: duty %s made %d round trips on an idle pass; want at most 1\n%s", k, i, r.name, r.rounds, formatRuns(m.runs[i]))
				}
				rounds[k][r.name] = max(rounds[k][r.name], r.rounds)
			}
		}
	}
	for name, n := range rounds[1] {
		if rounds[3][name] != n {
			t.Errorf("duty %s: %d round trip(s) at the live counts, %d at three times them; want the same", name, n, rounds[3][name])
		}
	}
}

// TestReconcileSlowPassNamesTheDuty is #3831's DUTY line: every DUTY line
// carries the duty's took_ms; an idle pass under the bar prints nothing in
// the loop, and a pass at or over reconcile.PassBar prints every duty's line
// and one SLOW line naming the duty that held it.
func TestReconcileSlowPassNamesTheDuty(t *testing.T) {
	quick := func(context.Context, *reconcile.Lease) (reconcile.Counts, error) { return reconcile.Counts{}, nil }
	slow := func(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
		time.Sleep(30 * time.Millisecond)
		return reconcile.Counts{}, nil
	}
	named := &namedDuties{errOut: &syncBuffer{}}
	duties := named.wrap([]reconcile.Duty{quick, slow, quick}, []string{"refill", "route", "expire"})
	pass := func() {
		for _, d := range duties {
			if _, err := d(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
		}
	}

	pass()
	var idle syncBuffer
	named.report(&idle, false, reconcile.PassResult{Took: 40 * time.Millisecond})
	if idle.String() != "" {
		t.Fatalf("an idle pass under the bar printed:\n%s", idle.String())
	}

	pass()
	var out syncBuffer
	named.report(&out, false, reconcile.PassResult{Took: 1200 * time.Millisecond})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("a slow pass printed %d lines, want three DUTY lines and SLOW:\n%s", len(lines), out.String())
	}
	for i, name := range []string{"refill", "route", "expire"} {
		if !strings.HasPrefix(lines[i], "DUTY "+name+" ") || !strings.Contains(lines[i], " took_ms=") || !strings.HasSuffix(lines[i], "err=") {
			t.Fatalf("line %d %q; want DUTY %s ... took_ms=<n> err=", i, lines[i], name)
		}
	}
	if !strings.HasPrefix(lines[3], "SLOW pass took_ms=1200 duty=route duty_ms=") {
		t.Fatalf("SLOW line %q; want it to name route, the duty that held the pass", lines[3])
	}
}
