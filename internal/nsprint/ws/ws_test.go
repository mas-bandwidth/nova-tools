package ws_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// trips counts client round trips: one per command or pipeline.
type trips struct{ n int }

func (h *trips) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *trips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.n++; return next(ctx, cmd) }
}
func (h *trips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { h.n++; return next(ctx, cmds) }
}

// slowlog arms the server's own clock: every command that runs one second or
// longer lands in SLOWLOG. The one-second rule is asserted there (an FCALL is
// one command, its whole function body timed by Redis), not by a wall-clock
// bound in the test, which internal/ci's waits class refuses.
func slowlog(t *testing.T, c *redis.Client) func() {
	t.Helper()
	ctx := context.Background()
	if err := c.ConfigSet(ctx, "slowlog-log-slower-than", "1000000").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.SlowLogReset(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		logs, err := c.SlowLogGet(ctx, 16).Result()
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range logs {
			t.Errorf("over one second on the server: %v took %v", l.Args, l.Duration)
		}
	}
}

func check(t *testing.T, c *redis.Client, ids []string) {
	t.Helper()
	if err := ws.Check(context.Background(), c, ids); err != nil {
		t.Fatal(err)
	}
}

func counts(t *testing.T, c *redis.Client) map[string]ws.Count {
	t.Helper()
	rows, err := ws.Counts(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]ws.Count{}
	for _, r := range rows {
		m[r.Stream] = r
	}
	return m
}

func TestFixtureHoldsTheInvariants(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ids := wstest.Fixture(t, c, 1000, 10)
	check(t, c, ids)
	m := counts(t, c)
	if len(m) != 10 {
		t.Fatalf("%d streams, want 10", len(m))
	}
	want := ws.Count{Stream: wstest.StreamName(3), Rank: 4, Waiting: 40, Ready: 20, Working: 15, Merging: 10, Landed: 10}
	if got := m[wstest.StreamName(3)]; got != want {
		t.Fatalf("counts %+v, want %+v", got, want)
	}
}

// TestEveryOperationIsOneRoundTripUnderOneSecond is the #3662 DONE-WHEN on the
// 1,000-task, 10-stream fixture: keep, park, unpark, rename, order, counts and
// move_many are each one round trip, no command runs a second on the server,
// and the invariants hold after every one. The measured ms are logged.
func TestEveryOperationIsOneRoundTripUnderOneSecond(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ids := wstest.Fixture(t, c, 1000, 10)
	done := slowlog(t, c)
	h := &trips{}
	c.AddHook(h)
	ctx := context.Background()
	s := wstest.StreamName
	step := func(name string, f func() string) {
		t.Helper()
		before := h.n
		start := time.Now()
		got := f()
		ms := float64(time.Since(start).Microseconds()) / 1000
		if n := h.n - before; n != 1 {
			t.Errorf("%s: %d round trips, want 1", name, n)
		}
		t.Logf("%-14s %7.2f ms  %s", name, ms, got)
		check(t, c, ids)
	}

	step("keep", func() string {
		r, err := ws.Keep(ctx, c, "test", "scope", []string{s(0), s(1)})
		if err != nil {
			t.Fatal(err)
		}
		if r != (ws.KeepResult{Kept: 2, ParkedStreams: 8, ParkedTasks: 8 * 60}) {
			t.Fatalf("keep %+v", r)
		}
		return fmt.Sprintf("%+v", r)
	})
	step("unpark", func() string {
		n, err := ws.UnparkStream(ctx, c, s(2), "test", "back")
		if err != nil || n != 60 {
			t.Fatalf("unpark %d %v, want 60", n, err)
		}
		return fmt.Sprint(n)
	})
	if got := counts(t, c)[s(2)]; got.Waiting != 40 || got.Ready != 20 || got.Parked != 0 {
		t.Fatalf("unpark did not restore waiting/ready: %+v", got)
	}
	if score, err := c.ZScore(ctx, ws.Key(s(2), "ready"), "t00082").Result(); err != nil || score != float64(wstest.Created(82)) {
		t.Fatalf("unpark lost the created_at score: %v %v, want %d", score, err, wstest.Created(82))
	}
	step("park", func() string {
		n, err := ws.ParkStream(ctx, c, s(0), "test", "pause")
		if err != nil || n != 60 {
			t.Fatalf("park %d %v, want 60", n, err)
		}
		return fmt.Sprint(n)
	})
	step("rename", func() string {
		n, err := ws.Rename(ctx, c, s(4), "swarm: renamed", "test")
		if err != nil || n != 100 {
			t.Fatalf("rename %d %v, want 100 members (done included)", n, err)
		}
		return fmt.Sprint(n)
	})
	if st, _ := c.HGet(ctx, "task:t00004", "stream").Result(); st != "swarm: renamed" {
		t.Fatalf("rename left stream %q on a member", st)
	}
	step("order", func() string {
		n, err := ws.Order(ctx, c, []string{s(9), "swarm: renamed"})
		if err != nil || n != 10 {
			t.Fatalf("order %d %v, want 10", n, err)
		}
		return fmt.Sprint(n)
	})
	step("counts", func() string {
		rows, err := ws.Counts(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if rows[0].Stream != s(9) || rows[1].Stream != "swarm: renamed" || rows[1].Parked != 60 || rows[2].Stream != s(0) {
			t.Fatalf("counts order %+v", rows[:3])
		}
		return fmt.Sprintf("%d streams", len(rows))
	})
	var ready []string
	for i, id := range ids {
		if i%10 == 1 && wstest.Mix(i/10) == "ready" {
			ready = append(ready, id)
		}
	}
	step("move_many", func() string {
		r, err := ws.MoveMany(ctx, c, "working", "test", "deal", append(ready, "t00000", "nope"))
		if err != nil {
			t.Fatal(err)
		}
		if r.Moved != 20 || r.Same != 0 || len(r.Refused) != 2 {
			t.Fatalf("move_many %+v", r)
		}
		return fmt.Sprintf("moved=%d refused=%d", r.Moved, len(r.Refused))
	})
	step("move_many 1000", func() string {
		r, err := ws.MoveMany(ctx, c, "closed", "test", "cancel", ids)
		if err != nil {
			t.Fatal(err)
		}
		// landed tasks refuse done/fail (a merge stands); the done ones are the same
		if r.Moved != 850 || r.Same != 50 || len(r.Refused) != 100 {
			t.Fatalf("move_many all %+v", r)
		}
		return fmt.Sprintf("moved=%d same=%d", r.Moved, r.Same)
	})
	done()
}

func TestMoveGraph(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ids := wstest.Fixture(t, c, 20, 1)
	ctx := context.Background()
	// t00000 waiting, t00008 ready, t00012 working, t00015 merging, t00017 landed, t00019 closed (done/fail)
	// The graph is the task card's (#3778): landed needs the merge sha, which
	// ws.Move does not carry; parked returns to waiting or ready.
	for _, tc := range []struct {
		id, to, want string
	}{
		{"t00000", "ready", "MOVED"},
		{"t00000", "ready", "SAME"},
		{"t00000", "working", "MOVED"},
		{"t00000", "merging", "MOVED"},
		{"t00000", "landed", "REFUSED"},
		{"t00001", "working", "REFUSED"},
		{"t00012", "parked", "REFUSED"},
		{"t00008", "parked", "MOVED"},
		{"t00008", "ready", "MOVED"},
		{"t00008", "waiting", "MOVED"},
		{"t00019", "parked", "REFUSED"},
		{"t00019", "waiting", "REFUSED"},
		{"t00015", "closed", "MOVED"},
		{"t00002", "limbo", "REFUSED"},
		{"missing", "ready", "REFUSED"},
	} {
		r, err := ws.Move(ctx, c, tc.id, tc.to, "test", "graph")
		got := r.Status
		if ws.IsRefused(err) {
			got = "REFUSED"
		} else if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s -> %s: %s (%v), want %s", tc.id, tc.to, got, err, tc.want)
		}
		check(t, c, append(ids, "missing"))
	}
	if n, _ := c.XLen(ctx, "ws:log").Result(); n != 7 {
		t.Errorf("ws:log has %d entries, want 7 (one per MOVED)", n)
	}
	c.HSet(ctx, "task:nostream", "state", "ready")
	if _, err := ws.Move(ctx, c, "nostream", "working", "test", ""); !ws.IsRefused(err) || !strings.Contains(err.Error(), "no stream") {
		t.Fatalf("a task with no stream: %v", err)
	}
}

// TestMoveRefusesABrokenLink is Glenn's invariant ("a card can only ever be
// in no set, or one of these sets"): a move whose record names a set the id
// is not in, or whose id also sits in any other of the stream's sets (the
// target or not; any set at all for a closed task), is refused with the
// mismatch named and writes nothing; move_many refuses that id alone.
func TestMoveRefusesABrokenLink(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	wstest.Fixture(t, c, 20, 1)
	ctx := context.Background()
	s := wstest.StreamName(0)
	// t00000 is in waiting; its record is made to say ready (set -> card broken)
	c.HSet(ctx, "task:t00000", "where", "ready")
	// t00001 is in waiting and also in ready, the target (two places)
	c.ZAdd(ctx, ws.Key(s, "ready"), redis.Z{Score: float64(wstest.Created(1)), Member: "t00001"})
	// t00003 is in waiting and also in landed, a set neither named nor the target
	c.ZAdd(ctx, ws.Key(s, "landed"), redis.Z{Score: float64(wstest.Created(3)), Member: "t00003"})
	// t00019 is done but also sits in parked
	c.ZAdd(ctx, ws.Key(s, "parked"), redis.Z{Score: float64(wstest.Created(19)), Member: "t00019"})
	for _, tc := range []struct{ id, to, want string }{
		{"t00000", "working", "DRIFT unlinked ws:s0: work:ready task:t00000"},
		{"t00001", "ready", "DRIFT twice ws:s0: work:ready task:t00001"},
		{"t00003", "ready", "DRIFT twice ws:s0: work:landed task:t00003"},
		{"t00003", "closed", "DRIFT twice ws:s0: work:landed task:t00003"},
		{"t00019", "closed", "DRIFT twice ws:s0: work:parked task:t00019"},
	} {
		before, _ := c.Dump(ctx, "task:"+tc.id).Result()
		_, err := ws.Move(ctx, c, tc.id, tc.to, "test", "broken")
		var r *ws.Refused
		if !errors.As(err, &r) || !strings.HasPrefix(r.Why, tc.want) {
			t.Fatalf("%s -> %s: %v, want REFUSED %s", tc.id, tc.to, err, tc.want)
		}
		if after, _ := c.Dump(ctx, "task:"+tc.id).Result(); after != before {
			t.Fatalf("%s: a refused move rewrote the record", tc.id)
		}
	}
	if n, _ := c.XLen(ctx, "ws:log").Result(); n != 0 {
		t.Fatalf("refused moves logged %d entries", n)
	}
	r, err := ws.MoveMany(ctx, c, "ready", "test", "batch", []string{"t00001", "t00002"})
	if err != nil || r.Moved != 1 || len(r.Refused) != 1 || r.Refused[0].ID != "t00001" {
		t.Fatalf("move_many over a broken link: %+v %v", r, err)
	}
	if err := ws.Check(ctx, c, []string{"t00000"}); err == nil {
		t.Fatal("Check passes a record whose set does not hold it")
	}
}

func TestRefusalsWriteNothing(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ids := wstest.Fixture(t, c, 100, 10)
	ctx := context.Background()
	s := wstest.StreamName
	for name, f := range map[string]func() error{
		"keep unknown":       func() error { _, err := ws.Keep(ctx, c, "t", "", []string{s(0), "nope"}); return err },
		"keep none":          func() error { _, err := ws.Keep(ctx, c, "t", "", nil); return err },
		"park unknown":       func() error { _, err := ws.ParkStream(ctx, c, "nope", "t", ""); return err },
		"unpark unknown":     func() error { _, err := ws.UnparkStream(ctx, c, "nope", "t", ""); return err },
		"rename onto":        func() error { _, err := ws.Rename(ctx, c, s(0), s(1), "t"); return err },
		"rename unknown":     func() error { _, err := ws.Rename(ctx, c, "nope", "x", "t"); return err },
		"rename bad name":    func() error { _, err := ws.Rename(ctx, c, s(0), "a|b", "t"); return err },
		"order twice":        func() error { _, err := ws.Order(ctx, c, []string{s(0), s(0)}); return err },
		"order unknown":      func() error { _, err := ws.Order(ctx, c, []string{"nope"}); return err },
		"order none":         func() error { _, err := ws.Order(ctx, c, nil); return err },
		"move unknown state": func() error { _, err := ws.Move(ctx, c, "t00000", "gone", "t", ""); return err },
	} {
		before, _ := c.DBSize(ctx).Result()
		logBefore, _ := c.XLen(ctx, "ws:log").Result()
		err := f()
		if !ws.IsRefused(err) {
			t.Errorf("%s: %v, want REFUSED", name, err)
		}
		after, _ := c.DBSize(ctx).Result()
		logAfter, _ := c.XLen(ctx, "ws:log").Result()
		if before != after || logBefore != logAfter {
			t.Errorf("%s wrote: keys %d->%d log %d->%d", name, before, after, logBefore, logAfter)
		}
		check(t, c, ids)
	}
}

// legacy writes the friend-queue shape ns_ws_migrate reads: task:<id> hashes
// with owner and a friend-queue state, the stream in a `stream` field (even
// ids) or a "STREAM: <s> |" title (odd ids), every fifth id with neither, the
// owner's idx sets for sprint S, and q:waiting / q:blocked.
func legacy(t *testing.T, c *redis.Client, n int) (ids []string, want map[string]string) {
	t.Helper()
	ctx := context.Background()
	pipe := c.Pipeline()
	want = map[string]string{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("L%04d", i)
		owner := fmt.Sprintf("f%d", i%4)
		stream := wstest.StreamName(i % 10)
		// created_at as friend-queue wrote it (RFC 3339 UTC), as epoch ms, or
		// absent (migrate writes the score it chose back as created_at)
		fields := []any{"owner", owner}
		switch {
		case i%7 == 0:
			fields = append(fields, "created_at", time.Unix(1790000000+int64(i), 0).UTC().Format(time.RFC3339))
		case i%11 == 0:
		default:
			fields = append(fields, "created_at", fmt.Sprint(5000+i))
		}
		switch {
		case i%5 == 4:
			fields = append(fields, "title", "no stream here")
		case i%2 == 0:
			fields = append(fields, "stream", stream, "title", "plain")
		default:
			fields = append(fields, "title", "STREAM: "+stream+" | the work")
		}
		ix := "sprint:S:idx:" + owner + ":"
		var state, wsState string
		switch i % 6 {
		case 0:
			state, wsState = "open", "ready"
			pipe.SAdd(ctx, ix+"open", id)
		case 1:
			state, wsState = "working", "working"
			pipe.SAdd(ctx, ix+"working", id)
			fields = append(fields, "leased_at", time.Now().UTC().Format(time.RFC3339))
		case 2:
			state, wsState = "closed", "done"
			pipe.SAdd(ctx, ix+"closed", id)
		case 3:
			state, wsState = "waiting", "waiting"
			pipe.ZAdd(ctx, "q:waiting", redis.Z{Score: float64(9000 + i), Member: id})
		case 4:
			state, wsState = "blocked", "waiting"
			pipe.ZAdd(ctx, "q:blocked", redis.Z{Score: 1, Member: id})
		case 5:
			// the index says working though the hash still says open (a take
			// that crashed between SMOVE and HSET): the index wins
			state, wsState = "open", "working"
			pipe.SAdd(ctx, ix+"working", id)
			fields = append(fields, "leased_at", time.Now().UTC().Format(time.RFC3339))
		}
		fields = append(fields, "state", state)
		pipe.HSet(ctx, "task:"+id, fields...)
		ids = append(ids, id)
		if i%5 != 4 {
			want[id] = wsState
		}
	}
	pipe.Set(ctx, "task:notahash", "x", 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return ids, want
}

func migrateAll(t *testing.T, c *redis.Client) ws.MigrateResult {
	t.Helper()
	var total ws.MigrateResult
	cursor := "0"
	for i := 0; ; i++ {
		r, err := ws.Migrate(context.Background(), c, cursor, "S", 100, "test")
		if err != nil {
			t.Fatal(err)
		}
		total.Add(r)
		cursor = r.Cursor
		if cursor == "0" {
			return total
		}
		if i > 10000 {
			t.Fatal("migrate never finished its scan")
		}
	}
}

// TestMigrateBuildsTheSetsAndIsIdempotent is the #3662 migrate DONE-WHEN: the
// sets come from task:* hashes (stream field or STREAM: title prefix), the
// friend-queue idx sets and q:waiting / q:blocked; a second pass places
// nothing; a ws move made after migrate survives a re-run.
func TestMigrateBuildsTheSetsAndIsIdempotent(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ids, want := legacy(t, c, 1000)
	done := slowlog(t, c)
	ctx := context.Background()
	start := time.Now()
	first := migrateAll(t, c)
	t.Logf("migrate 1,000 legacy tasks: %.2f ms, %+v", float64(time.Since(start).Microseconds())/1000, first)
	// SCAN may return a key twice; a second visit counts as same, never placed.
	if first.Placed != 800 || first.NoStream < 200 || first.Skipped < 1 || first.Scanned < 1001 {
		t.Fatalf("first pass %+v, want placed=800 nostream=200 skipped=1 scanned=1001", first)
	}
	check(t, c, ids)
	for id, st := range want {
		got, _ := c.HGet(ctx, "task:"+id, "where").Result()
		if got != st {
			t.Errorf("%s: where %q, want %q", id, got, st)
		}
	}
	if st, _ := c.HGet(ctx, "task:L0001", "stream").Result(); st != wstest.StreamName(1) {
		t.Fatalf("title-derived stream %q", st)
	}
	if fq, _ := c.HGet(ctx, "task:L0000", "state").Result(); fq != "open" {
		t.Fatalf("the friend-queue state of a ready task is not open: %q", fq)
	}
	// every set's score is created_at (ws.Check above); the RFC 3339 form parses
	if sc, _ := c.ZScore(ctx, ws.Key(wstest.StreamName(0), "ready"), "L0000").Result(); sc != 1790000000000 {
		t.Fatalf("L0000 scores %.0f, want its created_at 1790000000000", sc)
	}
	if n, _ := c.SCard(ctx, "ws:names").Result(); n != 8 {
		t.Fatalf("ws:names %d, want 8 (streams 4 and 9 are the no-stream ids)", n)
	}
	second := migrateAll(t, c)
	if second.Placed != 0 || second.Same < 800 {
		t.Fatalf("second pass %+v, want placed=0 same=800", second)
	}
	if _, err := ws.Move(ctx, c, "L0000", "working", "test", "after migrate"); err != nil {
		t.Fatal(err)
	}
	third := migrateAll(t, c)
	if third.Placed != 0 {
		t.Fatalf("a re-run moved a task ws had moved: %+v", third)
	}
	if st, _ := c.HGet(ctx, "task:L0000", "state").Result(); st != "working" {
		t.Fatalf("re-run reverted L0000 to %q", st)
	}
	check(t, c, ids)
	done()
}

func TestCheckpointWritesEverySet(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	wstest.Fixture(t, c, 1000, 10)
	ctx := context.Background()
	if _, err := ws.ParkStream(ctx, c, wstest.StreamName(0), "t", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sub", "ws.tsv")
	r, err := ws.Checkpoint(ctx, c, path, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != 1000 || r.Streams != 10 {
		t.Fatalf("checkpoint %+v, want 1000 rows (done included) over 10 streams", r)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 1002 {
		t.Fatalf("%d lines, want 2 header + 1000 rows", len(lines))
	}
	if !strings.HasPrefix(lines[2], wstest.StreamName(0)+"\tworking\t1700000000120\tt00120\t1012\t") {
		t.Fatalf("first row %q", lines[2])
	}
	got, _ := c.Get(ctx, ws.CheckpointKey).Result()
	if got != "utc=2023-11-14T22:13:20Z path="+path+" rows=1000" {
		t.Fatalf("receipt %q", got)
	}
}

func TestReadIDsAndParseStreams(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ids")
	if err := os.WriteFile(path, []byte("a b\n# comment\nc,a  # trailing\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids, err := ws.ReadIDs("@"+path, nil)
	if err != nil || strings.Join(ids, " ") != "a b c" {
		t.Fatalf("ReadIDs file %v %v", ids, err)
	}
	ids, err = ws.ReadIDs("@-", strings.NewReader("x\ny\n"))
	if err != nil || strings.Join(ids, " ") != "x y" {
		t.Fatalf("ReadIDs stdin %v %v", ids, err)
	}
	ids, err = ws.ReadIDs("p,q", nil)
	if err != nil || strings.Join(ids, " ") != "p q" {
		t.Fatalf("ReadIDs list %v %v", ids, err)
	}
	if _, err := ws.ReadIDs("@"+path+".missing", nil); err == nil {
		t.Fatal("a missing ids file read")
	}
	if got := ws.ParseStreams(" swarm: cards | nova-sprint ||"); strings.Join(got, "/") != "swarm: cards/nova-sprint" {
		t.Fatalf("ParseStreams %q", got)
	}
}
