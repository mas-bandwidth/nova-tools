package table_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// linesNow is the fixture's clock: every timestamp is an age before it.
var linesNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func ago(s int64) string { return strconv.FormatInt(linesNow.UnixMilli()-s*1000, 10) }

// linesFixture is a keyspace with three machines (one over its ceiling, one
// with no ceiling and a stale beat), a current sprint with cards on every
// clock (a harvest-start item 75 s old, a read-enqueued item 400 s old), ci
// cards in every state and verdict, a last sprint whose cards must not count,
// a reconciler whose last pass is 45 s old and two processes with no pass.
func linesFixture() [][]string {
	card := func(s, label, state string, kv ...string) [][]string {
		return [][]string{
			{"SADD", "s:" + s + ":idx:card:" + state, label},
			append([]string{"HSET", "s:" + s + ":card:" + label, "state", state}, kv...),
		}
	}
	cmds := [][]string{
		{"SADD", "benches", "hulk", "vision"},
		{"SADD", "friends", "stella", "emma"},
		{"HSET", "bench:hulk:desired", "slots", "16", "machine", "hulk"},
		{"HSET", "bench:vision:desired", "slots", "8", "machine", "vision"},
		{"HSET", "friend:stella:desired", "slots", "32", "machine", "studio"},
		{"HSET", "friend:emma:desired", "slots", "40", "machine", "studio"},
		{"HSET", "machine:hulk:ceiling", "slots", "16"},
		{"HSET", "machine:studio:ceiling", "slots", "64"},
		{"ZADD", "bench:hulk:living", "1", "a", "2", "b", "3", "c"},
		{"ZADD", "friend:stella:living", "1", "a", "2", "b"},
		{"ZADD", "friend:emma:living", "1", "a"},
		{"HSET", "bench:hulk:beat", "load1", "2.50", "at", ago(1)},
		{"HSET", "bench:studio:beat", "load1", "7.10", "at", ago(2)},
		{"HSET", "bench:vision:beat", "load1", "0.30", "at", ago(300)},
		{"ZADD", "sprint:order", "1", "s-old", "2", "s-now"},
		{"HSET", "proc:reconciler", "pass_at", ago(45)},
		{"HSET", "proc:harvest:hulk", "pass_at", ago(2)},
		{"HSET", "proc:ok-to-friend", "pass_at", ago(1)},
		{"HSET", "proc:pr-to-read", "pass_at", ago(3)},
		{"HSET", "proc:backpressure", "pass_at", ago(0)},
	}
	for _, c := range [][][]string{
		card("s-now", "c-late", "ended", "outcome", "DONE", "ended_at", ago(75)),
		card("s-now", "c-pushing", "ended", "outcome", "DONE", "ended_at", ago(20), "harvest_step", "pushed"),
		card("s-now", "c-young", "ended", "outcome", "DONE", "ended_at", ago(10)),
		card("s-now", "c-fail", "ended", "outcome", "FAIL", "ended_at", ago(500)),
		card("s-now", "c-harv", "harvested", "outcome", "DONE", "ended_at", ago(400), "harvested_at", ago(30)),
		card("s-now", "c-queued", "queued"),
		card("s-now", "ci-1", "queued", "verdict", "PENDING", "cut_at", ago(90)),
		card("s-now", "ci-2", "queued", "verdict", "PENDING", "cut_at", ago(5), "blocked", "no alternate bench"),
		card("s-now", "ci-3", "dealt", "verdict", "PENDING", "cut_at", ago(3)),
		card("s-now", "ci-4", "running", "verdict", "PENDING", "cut_at", ago(4)),
		card("s-now", "ci-5", "ended", "verdict", "OK", "cut_at", ago(600)),
		card("s-now", "ci-6", "ended", "verdict", "FAIL", "cut_at", ago(30)),
		card("s-now", "ci-7", "ended", "verdict", "FLAKY", "cut_at", ago(40)),
		card("s-now", "ci-8", "ended", "cut_at", ago(50)),
		card("s-old", "c-old", "ended", "outcome", "DONE", "ended_at", ago(9000)),
		card("s-old", "ci-old", "queued", "verdict", "PENDING", "cut_at", ago(9000)),
	} {
		cmds = append(cmds, c...)
	}
	return cmds
}

const wantLines = "machine | ceiling | desired | living | load1\n" +
	"machine:hulk | 16 | 16 | 3 | 2.50\n" +
	"RED machine:studio | 64 | 72 | 3 | 7.10 | over: 72/64\n" +
	"machine:vision | missing | 8 | 0 | ?\n" +
	"RED clock harvest-start | bound 60s | n 2 | oldest 75s\n" +
	"clock verified-pr | bound 5m | n 3 | oldest 75s\n" +
	"clock review-enqueue | bound 60s | n 1 | oldest 30s\n" +
	"RED clock read-enqueued | bound 6m | n 4 | oldest 400s\n" +
	"ci: cut 2, running 2, PENDING 4, OK 1, FAIL 1, FLAKY 1, MISSING-at-head 1, blocked-no-alternate-bench 1, oldest 90s\n" +
	"proc reconciler ? 45s\n" +
	"proc harvest:hulk up 2s\n" +
	"proc harvest:vision missing\n" +
	"proc ok-to-friend up 1s\n" +
	"proc pr-to-read up 3s\n" +
	"proc hold-to-fix missing\n" +
	"proc backpressure up 0s\n"

func linesStore(t *testing.T, cmds [][]string) (*redis.Client, *cmdLog) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	seedCommands(t, client, cmds)
	log := &cmdLog{}
	client.AddHook(log)
	return client, log
}

// TestTableMachineCiClockLines (DONE-WHEN of #3045): the fixture renders the
// exact machine, clock, ci and process lines; the harvest-start item older
// than 60 s and the machine over its ceiling are red lines, the stale
// reconciler prints ? with its age, and a steady tick is one pipeline with no
// KEYS and no SCAN.
func TestTableMachineCiClockLines(t *testing.T) {
	t.Parallel()

	client, log := linesStore(t, linesFixture())
	ctx := context.Background()
	r := table.NewLinesReader(client, table.LinesConfig{})
	if _, err := r.Read(ctx, linesNow); err != nil {
		t.Fatal(err)
	}
	log.reset()
	lines, err := r.Read(ctx, linesNow)
	if err != nil {
		t.Fatal(err)
	}
	names, trips := log.reset()
	if lines.RoundTrips != 1 || trips != 1 {
		t.Fatalf("steady tick: RoundTrips=%d, client round trips=%d; want 1 and 1", lines.RoundTrips, trips)
	}
	for _, n := range names {
		if n == "KEYS" || n == "SCAN" {
			t.Fatalf("the tick sent %s: %v", n, names)
		}
	}
	if lines.Sprint != "s-now" {
		t.Fatalf("sprint = %q, want the newest of sprint:order, s-now", lines.Sprint)
	}
	if got := lines.Render(); got != wantLines {
		t.Fatalf("lines differ\ngot:\n%s\nwant:\n%s", got, wantLines)
	}

	// The harvest-start item leaves the clock once harvest starts, and the
	// line is no longer red; the change is on the very next tick.
	seedCommands(t, client, [][]string{{"HSET", "s:s-now:card:c-late", "harvest_step", "pushed"}})
	lines, err = r.Read(ctx, linesNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := lines.Render(); !strings.Contains(got, "\nclock harvest-start | bound 60s | n 1 | oldest 10s\n") {
		t.Fatalf("harvest-start after the step:\n%s", got)
	}
}

// TestTableLinesEmptyKeyspace: nothing registered and no sprint renders the
// header, every clock at zero, a zero ci line and every fixed process missing.
func TestTableLinesEmptyKeyspace(t *testing.T) {
	t.Parallel()

	client, _ := linesStore(t, nil)
	lines, err := table.NewLinesReader(client, table.LinesConfig{}).Read(context.Background(), linesNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "machine | ceiling | desired | living | load1\n" +
		"clock harvest-start | bound 60s | n 0 | oldest -\n" +
		"clock verified-pr | bound 5m | n 0 | oldest -\n" +
		"clock review-enqueue | bound 60s | n 0 | oldest -\n" +
		"clock read-enqueued | bound 6m | n 0 | oldest -\n" +
		"ci: cut 0, running 0, PENDING 0, OK 0, FAIL 0, FLAKY 0, MISSING-at-head 0, blocked-no-alternate-bench 0, oldest -\n" +
		"proc reconciler missing\n" +
		"proc ok-to-friend missing\n" +
		"proc pr-to-read missing\n" +
		"proc hold-to-fix missing\n" +
		"proc backpressure missing\n"
	if got := lines.Render(); got != want {
		t.Fatalf("empty keyspace\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestTableRendererLease (DONE-WHEN of #3045): a second renderer on the same
// output, however the path is spelled, is refused with the holder named; the
// lease frees on release; and a reader racing the writer never observes the
// output partially written, nor is a temp file left behind.
func TestTableRendererLease(t *testing.T) {
	t.Parallel()

	client, _ := linesStore(t, nil)
	ctx := context.Background()
	dir := t.TempDir()
	out := filepath.Join(dir, "table.txt")

	if err := table.AcquireRenderer(ctx, client, out, "first", time.Minute); err != nil {
		t.Fatalf("first renderer: %v", err)
	}
	for _, spelling := range []string{out, filepath.Join(dir, ".", "table.txt")} {
		err := table.AcquireRenderer(ctx, client, spelling, "second", time.Minute)
		var held *table.LeaseHeld
		if !errors.As(err, &held) || held.Holder != "first" || held.Key != table.LeaseKey(out) {
			t.Fatalf("second renderer on %s: err=%v, want refused naming first on %s", spelling, err, table.LeaseKey(out))
		}
		if !strings.HasPrefix(err.Error(), "REFUSED: lease:table:") {
			t.Fatalf("refusal line = %q", err)
		}
	}
	if err := table.AcquireRenderer(ctx, client, filepath.Join(dir, "other.txt"), "second", time.Minute); err != nil {
		t.Fatalf("another output is its own lease: %v", err)
	}
	if err := table.ReleaseRenderer(ctx, client, out, "second"); err != nil {
		t.Fatal(err)
	}
	if err := table.AcquireRenderer(ctx, client, out, "second", time.Minute); err == nil {
		t.Fatal("a release by a non-holder freed the lease")
	}
	if err := table.ReleaseRenderer(ctx, client, out, "first"); err != nil {
		t.Fatal(err)
	}
	if err := table.AcquireRenderer(ctx, client, out, "second", time.Minute); err != nil {
		t.Fatalf("after release: %v", err)
	}

	bodyA := strings.Repeat("a", 256<<10)
	bodyB := strings.Repeat("b", 256<<10)
	if err := table.WriteAtomic(out, bodyA); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var partial string
	reads := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(out)
			if err != nil {
				partial = "read: " + err.Error()
				return
			}
			reads++
			if s := string(data); s != bodyA && s != bodyB {
				partial = "len " + strconv.Itoa(len(s))
				return
			}
		}
	}()
	for i := 0; i < 50; i++ {
		body := bodyA
		if i%2 == 0 {
			body = bodyB
		}
		if err := table.WriteAtomic(out, body); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if partial != "" {
		t.Fatalf("the reader observed a partial table: %s (after %d good reads)", partial, reads)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "table.txt" {
			t.Fatalf("left in the output dir: %s", e.Name())
		}
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("output mode: %v %v", info, err)
	}
}
