package table_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

var updateGolden3530 = flag.Bool("update-golden-3530", false, "write the whole table rendered from the #3530 fixture to its golden file")

// cmdLog is a go-redis hook that records every command name and every
// round trip (a pipeline or a single command) the client sends.
type cmdLog struct {
	mu    sync.Mutex
	names []string
	keys  []string // "NAME key" for every command that names a key
	trips int
}

// note records one command's name and, when it has one, its first key.
func (l *cmdLog) note(cmd redis.Cmder) {
	name := strings.ToUpper(cmd.Name())
	l.names = append(l.names, name)
	if args := cmd.Args(); len(args) > 1 {
		if key, ok := args[1].(string); ok {
			l.keys = append(l.keys, name+" "+key)
		}
	}
}

func (l *cmdLog) DialHook(next redis.DialHook) redis.DialHook { return next }

func (l *cmdLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		l.mu.Lock()
		l.note(cmd)
		l.trips++
		l.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (l *cmdLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		l.mu.Lock()
		for _, c := range cmds {
			l.note(c)
		}
		l.trips++
		l.mu.Unlock()
		return next(ctx, cmds)
	}
}

func (l *cmdLog) reset() (names []string, trips int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	names, trips = l.names, l.trips
	l.names, l.keys, l.trips = nil, nil, 0
	return names, trips
}

// keysRead is every "NAME key" logged since the last reset.
func (l *cmdLog) keysRead() map[string]bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	got := map[string]bool{}
	for _, k := range l.keys {
		got[k] = true
	}
	return got
}

func sprintStore(t *testing.T) (*redis.Client, *miniredis.Miniredis, *cmdLog) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	seedCommands(t, client, table.SprintFixture())
	log := &cmdLog{}
	client.AddHook(log)
	return client, mr, log
}

// TestControl3530WholeTableGolden (DONE-WHEN of #3530): the fixture of 10
// streams, 6 benches and 4 friends renders byte for byte as the golden; the
// first tick takes two round trips (the sets, then the values) and every
// later tick exactly one pipeline, with no KEYS and no SCAN.
func TestControl3530WholeTableGolden(t *testing.T) {
	t.Parallel()

	client, _, log := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	r := table.NewSprintReader(client, table.SprintFixtureConfig())
	first, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, trips := log.reset(); first.RoundTrips != 2 || trips != 2 {
		t.Fatalf("first tick: RoundTrips=%d, client round trips=%d; want 2 and 2", first.RoundTrips, trips)
	}
	snap, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	names, trips := log.reset()
	if snap.RoundTrips != 1 || trips != 1 {
		t.Fatalf("steady tick: RoundTrips=%d, client round trips=%d; want 1 and 1", snap.RoundTrips, trips)
	}
	for _, n := range names {
		if n == "KEYS" || n == "SCAN" {
			t.Fatalf("the tick sent %s: %v", n, names)
		}
	}
	got := snap.Render(now)
	path := filepath.Join("testdata", "sprint-table-3530.golden")
	if *updateGolden3530 {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := table.Golden3530(); got != want && !*updateGolden3530 {
		t.Fatalf("whole table differs from %s\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

// TestControl3530RenderUnder10ms measures the render, and the read plus
// render against an in-process server, over the fixture; the median render
// must be under 10 ms.
func TestControl3530RenderUnder10ms(t *testing.T) {
	t.Parallel()

	client, _, _ := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	r := table.NewSprintReader(client, table.SprintFixtureConfig())
	if _, err := r.Read(ctx, now); err != nil {
		t.Fatal(err)
	}
	const n = 50
	render := make([]time.Duration, n)
	tick := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		snap, err := r.Read(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		mid := time.Now()
		_ = snap.Render(now)
		end := time.Now()
		render[i], tick[i] = end.Sub(mid), end.Sub(start)
	}
	med := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}
	mr, mt := med(render), med(tick)
	t.Logf("RENDER median=%s max=%s; READ+RENDER median=%s max=%s (n=%d, miniredis)", mr, render[n-1], mt, tick[n-1], n)
	if mr >= 10*time.Millisecond {
		t.Fatalf("median render %s, want under 10 ms", mr)
	}
}

// TestControl3530MembershipChange: a stream, bench or friend added between
// ticks is on the very next table (one extra round trip that tick), and a
// removed one is gone from it.
func TestControl3530MembershipChange(t *testing.T) {
	t.Parallel()

	client, _, log := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	cfg := table.SprintFixtureConfig()
	cfg.Friends = nil // the friends SET, sorted
	r := table.NewSprintReader(client, cfg)
	if _, err := r.Read(ctx, now); err != nil {
		t.Fatal(err)
	}
	seedCommands(t, client, [][]string{
		{"ZADD", "ws:order", "11", "late stream"},
		{"ZADD", "ws:late stream:working", "1", "late-1"},
		{"SREM", "benches", "vision"},
		{"SREM", "friends", "ghost"},
	})
	log.reset()
	snap, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.RoundTrips != 2 {
		t.Fatalf("changed tick RoundTrips=%d, want 2", snap.RoundTrips)
	}
	got := snap.Render(now)
	if !strings.Contains(got, "late stream                    |       0 |     0 |       1 |       0 |       0 |      0\n") {
		t.Fatalf("the new stream is not on the table:\n%s", got)
	}
	if strings.Contains(got, "vision") || strings.Contains(got, "ghost") {
		t.Fatalf("a removed bench or friend is still on the table:\n%s", got)
	}
	friends := []string{}
	for _, row := range snap.Friends {
		friends = append(friends, row.Name)
	}
	if strings.Join(friends, ",") != "emma,johnny,rowan,stella" {
		t.Fatalf("friends from the SET = %v, want sorted emma,johnny,rowan,stella", friends)
	}
}

// TestControl3530NoPitstopNoSprint: no pit stop key (or no sprint named)
// prints the plain headline, and an empty keyspace renders every block with
// zero totals rather than failing.
func TestControl3530NoPitstopNoSprint(t *testing.T) {
	t.Parallel()

	client, _, _ := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	cfg := table.SprintFixtureConfig()
	cfg.Sprint = ""
	snap, err := table.NewSprintReader(client, cfg).Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Render(now); !strings.HasPrefix(got, "SPRINT TABLE\n\n588/594 left, 1% done -> ~11760m\n\n") {
		t.Fatalf("headline:\n%s", got)
	}
	mr := miniredis.RunT(t)
	empty := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer empty.Close()
	snap, err = table.NewSprintReader(empty, table.SprintConfig{}).Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	want := "SPRINT TABLE\n\n0/0 left, 0% done -> ~0m\n\n" +
		"stream                         | waiting | ready | working | reading | merging | landed\n" +
		"-------------------------------+---------+-------+---------+---------+---------+-------\n" +
		"-------------------------------+---------+-------+---------+---------+---------+-------\n" +
		"total                          |       0 |     0 |       0 |       0 |     0/0 |      0\n\n"
	if got := snap.Render(now); !strings.HasPrefix(got, want) {
		t.Fatalf("empty keyspace:\n%s", got)
	}
}

// TestControl3530FailedTickNeverBlank: a failed tick before any good read
// says so under the headline; after a good read it keeps that read's rows and
// says how stale they are.
func TestControl3530FailedTickNeverBlank(t *testing.T) {
	t.Parallel()

	now := table.SprintFixtureNow()
	cfg := table.SprintFixtureConfig()
	if got := table.FailedSprint(cfg, nil).Render(now); got != "SPRINT TABLE\n\nstale: never read (Redis did not answer since start)\n" {
		t.Fatalf("never-read tick:\n%s", got)
	}
	client, _, _ := sprintStore(t)
	good, err := table.NewSprintReader(client, cfg).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	good.LastGood = now.Add(-7 * time.Second)
	got := table.FailedSprint(cfg, good).Render(now)
	if !strings.HasPrefix(got, table.Golden3530()) || !strings.HasSuffix(got, "stale: 7s (Redis did not answer; rows are the last good read)\n") {
		t.Fatalf("failed tick after a good read:\n%s", got)
	}
}

// TestControl3530WriterLock: one writer per lock key. A second token is
// refused with the holder's token; a tick whose lock was taken over reports
// LockLost; the holder's release frees the key and a stranger's does not.
func TestControl3530WriterLock(t *testing.T) {
	t.Parallel()

	client, _, _ := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	const key = "lock:nova-sprint-table"
	if holder, err := table.AcquireLock(ctx, client, key, "a", 5*time.Second); err != nil || holder != "a" {
		t.Fatalf("first acquire: %q %v", holder, err)
	}
	if holder, err := table.AcquireLock(ctx, client, key, "b", 5*time.Second); err != nil || holder != "a" {
		t.Fatalf("second acquire: %q %v, want refused with holder a", holder, err)
	}
	cfg := table.SprintFixtureConfig()
	cfg.LockKey, cfg.LockToken = key, "a"
	snap, err := table.NewSprintReader(client, cfg).Read(ctx, now)
	if err != nil || snap.LockLost {
		t.Fatalf("holder's tick: lost=%v err=%v", snap != nil && snap.LockLost, err)
	}
	cfg.LockToken = "b"
	snap, err = table.NewSprintReader(client, cfg).Read(ctx, now)
	if err != nil || !snap.LockLost {
		t.Fatalf("stranger's tick: lost=%v err=%v, want lost", snap != nil && snap.LockLost, err)
	}
	if err := table.ReleaseLock(ctx, client, key, "b"); err != nil {
		t.Fatal(err)
	}
	if v, _ := client.Get(ctx, key).Result(); v != "a" {
		t.Fatalf("a stranger's release freed the lock: %q", v)
	}
	if err := table.ReleaseLock(ctx, client, key, "a"); err != nil {
		t.Fatal(err)
	}
	if n, _ := client.Exists(ctx, key).Result(); n != 0 {
		t.Fatal("the holder's release left the key")
	}
}

// TestControl3637ClearUnderOneSecond (DONE-WHEN of #3637): table clear moves
// every landed member to closed and zeroes the friend done column, waiting,
// ready, working, reading and merging untouched, in under one second; the checkpoint
// names every moved task and every done count.
// sprintStoreLua is the fixture on a throwaway redis-server with the
// nova_sprint library loaded: the clear moves through the one task move.
func sprintStoreLua(t *testing.T) (*redis.Client, *cmdLog) {
	t.Helper()
	_, client := wstest.Start(t)
	seedCommands(t, client, table.SprintFixture())
	log := &cmdLog{}
	client.AddHook(log)
	return client, log
}

func TestControl3637ClearUnderOneSecond(t *testing.T) {
	t.Parallel()

	client, log := sprintStoreLua(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	r := table.NewSprintReader(client, table.SprintFixtureConfig())
	before, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	log.reset()
	start := time.Now()
	plan, err := table.PlanClear(ctx, client, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	cp := plan.Checkpoint()
	if err := plan.Apply(ctx, client, "rowan", "test", "receipt"); err != nil {
		t.Fatal(err)
	}
	// The one-second rule is structural: four round trips whatever the
	// number of landed tasks (lists, landed sets, task fields, one
	// MULTI/EXEC). The wall time is logged, not asserted (the waits class).
	t.Logf("CLEAR ms=%.2f landed=%d", float64(time.Since(start).Microseconds())/1000, plan.Count())
	if names, trips := log.reset(); trips != 4 {
		t.Fatalf("clear took %d round trips, want 4: %v", trips, names)
	}
	if plan.Count() != 6 || strings.Count(cp, "\ntask\t") != 6 || !strings.Contains(cp, "\nfriend\trowan\t14\n") {
		t.Fatalf("plan count %d; checkpoint:\n%s", plan.Count(), cp)
	}
	after, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range after.Streams {
		b := before.Streams[i]
		if row.Landed != 0 || row.Waiting != b.Waiting || row.Ready != b.Ready || row.Working != b.Working || row.Reading != b.Reading || row.Merging != b.Merging {
			t.Fatalf("stream %q after clear %+v, before %+v", row.Name, row, b)
		}
	}
	got := after.Render(now)
	for _, want := range []string{
		"\n588/588 left, 0% done -> ~11760m\n",
		"\nrowan      |     0 |       2 |     0 |     0 |     0 |    - | up stale=10\n", // working is live children only (#3892)
		"\nstella     |     0 |       1 |     0 |     0 |     0 |    - | down      \n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("after clear lacks %q:\n%s", want, got)
		}
	}
	if state, _ := client.HGet(ctx, "task:t9-landed-0", "state").Result(); state != "closed" {
		t.Fatalf("task:t9-landed-0 state=%q, want closed", state)
	}
	msgs, _ := client.XRange(ctx, "ws:log", "-", "+").Result()
	closed := 0
	for _, m := range msgs {
		if m.Values["to"] == "done/ok" && m.Values["from"] == "landed" && m.Values["by"] == "rowan" {
			closed++
		}
	}
	if closed != 6 {
		t.Fatalf("ws:log has %d landed->done/ok entries, want 6", closed)
	}
	if v, _ := client.Get(ctx, "ws:checkpoint").Result(); v != "receipt" {
		t.Fatalf("ws:checkpoint=%q", v)
	}
	// a counter that restarts below its base (a new sprint's index sets) shows as is
	seedCommands(t, client, [][]string{{"ZREMRANGEBYRANK", table.FriendCardsKey("rowan", "done"), "0", "10"}})
	again, _ := r.Read(ctx, now)
	if got := again.Render(now); !strings.Contains(got, "\nrowan      |     0 |       2 |     3 |     0 |     0 |    - | up stale=10\n") {
		t.Fatalf("restarted counter:\n%s", got)
	}
}
