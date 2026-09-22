package pulse

// The swarm table: the render, and the read behind it. The store is a written map in every
// test here but the last, which is gated on NOVA_REDIS_TEST=1, writes only bench:test-*
// (the prefix the bench ACL user may write) and deletes what it wrote in Cleanup.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// fakeStore is the SwarmStoreReader a test writes: plain maps, no socket.
type fakeStore struct {
	strings map[string]string
	hashes  map[string]map[string]string
	err     error
}

func newFakeStore() *fakeStore {
	return &fakeStore{strings: map[string]string{}, hashes: map[string]map[string]string{}}
}

func (f *fakeStore) Keys(_ context.Context, pattern string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	prefix := strings.TrimSuffix(pattern, "*")
	var out []string
	for k := range f.hashes {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	for k := range f.strings {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeStore) Hash(_ context.Context, key string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.hashes[key], nil
}

func (f *fakeStore) Get(_ context.Context, key string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.strings[key]
	return v, ok, nil
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// twoBenchStore is the fixture most of these tests read: two benches, a stuck-DONE total,
// four friends in the three presence states, and a current sprint.
func twoBenchStore() *fakeStore {
	s := newFakeStore()
	s.hashes["bench:hulk"] = map[string]string{
		"host": "hulk", "queue": "12", "working": "8", "done": "140", "ok": "119", "fail": "21",
		"load1": "4.20", "ncpu": "16", "at": "2026-09-22T18:00:00Z",
	}
	s.hashes["bench:space"] = map[string]string{
		"host": "space", "queue": "3", "working": "2", "done": "60", "ok": "60", "fail": "0",
		"load1": "0.90", "ncpu": "8", "at": "2026-09-22T18:00:00Z",
	}
	s.hashes["bench:stuck_done"] = map[string]string{"total": "7", "at": "2026-09-22T17:55:00Z"}
	s.strings["friend:johnny"] = "2026-09-22T17:59:48Z"
	s.strings["friend:johnny:last"] = "2026-09-22T17:59:48Z"
	s.strings["friend:emma:last"] = "2026-09-22T16:48:00Z"
	s.strings["sprint:current"] = "cards-v2"
	s.hashes["sprint:cards-v2"] = map[string]string{"closed": "18", "open": "9", "working": "3"}
	return s
}

// The whole table, byte for byte. The columns, the widths, the separators and the totals
// line are bin/sprint-table-redis's, because the table Glenn reads must not change shape on
// the day it changes implementation; only the header word does.
func TestSwarmTableIsTheTableTheShellPrinted(t *testing.T) {
	t.Parallel()
	now := at("2026-09-22T18:00:03Z")
	st, err := ReadSwarmState(context.Background(), twoBenchStore(), []string{"johnny", "stella", "emma", "freddy"}, now)
	if err != nil {
		t.Fatal(err)
	}
	got := RenderSwarmTable(st, now)
	want := "SWARM TABLE  2026-09-22T18:00:03Z  (each bench pushes its row every 1 s; absent = no row for 5 s)\n" +
		"\n" +
		"host       | queue | working |  done |    ok |  fail |  ok% |   load\n" +
		"-----------+-------+---------+-------+-------+-------+------+-------\n" +
		"hulk       |    12 |       8 |   140 |   119 |    21 |  85% |   4.20\n" +
		"space      |     3 |       2 |    60 |    60 |     0 | 100% |   0.90\n" +
		"-----------+-------+---------+-------+-------+-------+------+-------\n" +
		"total      |    15 |      10 |   200 |   179 |    21 |  89% |\n" +
		"\n" +
		"stuck-DONE: 7  (bench:stuck_done at 2026-09-22T17:55:00Z)\n" +
		"friends: emma AWAY 1h12m (last 16:48Z) · freddy none · johnny up 15s · stella none\n" +
		"S=cards-v2 C=18 O=9 W=3  18/30 60%\n"
	if got != want {
		t.Fatalf("the swarm table changed shape.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// bench:stuck_done shares the bench:* namespace because the bench ACL user may only write
// there. It is the fleet total, not a host, and must never become a row.
func TestSwarmTableNeverShowsStuckDoneAsABench(t *testing.T) {
	t.Parallel()
	now := at("2026-09-22T18:00:00Z")
	st, err := ReadSwarmState(context.Background(), twoBenchStore(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range st.Rows {
		if r.Host == "stuck_done" || strings.Contains(r.Host, "stuck") {
			t.Fatalf("bench:stuck_done became a host row: %+v", r)
		}
	}
	if len(st.Rows) != 2 {
		t.Fatalf("want the two benches, got %d rows", len(st.Rows))
	}
}

// A bench whose row expired is ABSENT. It is not a row of zeros: a row of zeros reads as a
// bench with nothing to do, which is how a fleet nobody could see looked healthy on
// 2026-09-17.
func TestABenchWhoseRowExpiredIsAbsentAndNotARowOfZeros(t *testing.T) {
	t.Parallel()
	s := twoBenchStore()
	// The key is still in the scan's answer but its hash is gone: the row expired between
	// the SCAN and the HGETALL, which is a race the table meets every few minutes.
	s.hashes["bench:vision"] = map[string]string{}
	now := at("2026-09-22T18:00:00Z")
	st, err := ReadSwarmState(context.Background(), s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	table := RenderSwarmTable(st, now)
	if strings.Contains(table, "vision") {
		t.Fatalf("an expired bench is absent, not a row:\n%s", table)
	}
}

// A count nobody took is a `?`, never a 0.
func TestStuckDoneWithNoTotalPrintsAQuestionMark(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	s.hashes["bench:hulk"] = map[string]string{"host": "hulk", "done": "1", "ok": "1", "load1": "0.10"}
	now := at("2026-09-22T18:00:00Z")
	st, err := ReadSwarmState(context.Background(), s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	table := RenderSwarmTable(st, now)
	if !strings.Contains(table, "stuck-DONE: ?\n") {
		t.Fatalf("a total nobody measured is ?, never 0:\n%s", table)
	}
}

// The friends line, #2610's three states. "We are repeatedly failing to notice when friends
// are not here": a friend who is gone reads as gone, and a friend nobody has ever seen
// reads as none rather than being quietly left off.
func TestFriendsLineHasTheThreeStates(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	s.strings["friend:johnny"] = "2026-09-22T17:59:48Z"
	s.strings["friend:emma:last"] = "2026-09-22T16:48:00Z"
	now := at("2026-09-22T18:00:00Z")
	friends, err := readFriends(context.Background(), s, []string{"freddy"}, now)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(friends))
	for _, f := range friends {
		got = append(got, f.Line())
	}
	want := []string{"emma AWAY 1h12m (last 16:48Z)", "freddy none", "johnny up 12s"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("friends = %v, want %v", got, want)
	}
}

// A heartbeat stamped in the future -- two machines' clocks a second apart -- is zero
// seconds old, never a negative age. A friend who has been up for minus four hours is worse
// than one with no number.
func TestAFriendAheadOfOurClockReadsAsZeroSecondsNotNegative(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	s.strings["friend:stella"] = "2026-09-22T18:00:05Z"
	friends, err := readFriends(context.Background(), s, nil, at("2026-09-22T18:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if len(friends) != 1 || friends[0].Line() != "stella up 0s" {
		t.Fatalf("got %v", friends)
	}
}

// With no friend keys and no roster there is no friends: line at all -- an empty line would
// read as a fleet with no friends on it.
func TestNoFriendKeysMeansNoFriendsLine(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	s.hashes["bench:hulk"] = map[string]string{"host": "hulk", "load1": "0.10"}
	now := at("2026-09-22T18:00:00Z")
	st, err := ReadSwarmState(context.Background(), s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(RenderSwarmTable(st, now), "friends:") {
		t.Fatal("a friends: line with nothing on it says a fleet has no friends")
	}
}

// The COWS line is OMITTED when there is no current sprint. A sprint of 0/0 0% on the table
// is a sprint somebody will act on.
func TestTheCowsLineIsOmittedWithNoCurrentSprint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(*fakeStore)
		want  bool
	}{
		{"no sprint:current", func(*fakeStore) {}, false},
		{"sprint:current naming a hash that is gone", func(s *fakeStore) { s.strings["sprint:current"] = "ghost" }, false},
		{"a current sprint with counts", func(s *fakeStore) {
			s.strings["sprint:current"] = "cards-v2"
			s.hashes["sprint:cards-v2"] = map[string]string{"closed": "1", "open": "1", "working": "0"}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newFakeStore()
			s.hashes["bench:hulk"] = map[string]string{"host": "hulk", "load1": "0.10"}
			tc.setup(s)
			now := at("2026-09-22T18:00:00Z")
			st, err := ReadSwarmState(context.Background(), s, nil, now)
			if err != nil {
				t.Fatal(err)
			}
			has := strings.Contains(RenderSwarmTable(st, now), "S=")
			if has != tc.want {
				t.Fatalf("COWS line present = %v, want %v", has, tc.want)
			}
		})
	}
}

// COWS arithmetic: the total is C+O+W and never a field of its own, because a total that
// can disagree with its parts is a total nobody can check.
func TestCowsLine(t *testing.T) {
	t.Parallel()
	c := Cows{Sprint: "cards-v2", Closed: 18, Open: 9, Working: 3}
	if c.Total() != 30 || c.Pct() != 60 {
		t.Fatalf("total=%d pct=%d", c.Total(), c.Pct())
	}
	if got := c.Line(); got != "S=cards-v2 C=18 O=9 W=3  18/30 60%" {
		t.Fatalf("COWS line = %q", got)
	}
	empty := Cows{Sprint: "new"}
	if got := empty.Line(); got != "S=new C=0 O=0 W=0  0/0 0%" {
		t.Fatalf("an empty sprint's line = %q", got)
	}
}

// A half-written row still shows up under the right name: the host falls back to the key's
// own suffix and an unreadable count is zero, rather than a blank line on the table.
func TestAHalfWrittenRowStillNamesItsBench(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	s.hashes["bench:batman"] = map[string]string{"queue": "not a number"}
	now := at("2026-09-22T18:00:00Z")
	st, err := ReadSwarmState(context.Background(), s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rows) != 1 || st.Rows[0].Host != "batman" || st.Rows[0].Queue != 0 {
		t.Fatalf("got %+v", st.Rows)
	}
	if !strings.Contains(RenderSwarmTable(st, now), "batman") {
		t.Fatal("the bench is not on the table")
	}
}

// The whole thing over a real Redis wire protocol, with miniredis standing in for the fleet
// store: the rows a bench pushed are the rows the table reads.
func TestRowAndTableAgreeOverTheWire(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(dir, "results")
	writeResult(t, filepath.Join(results, "card-1", "RESULT.md"), "RESULT: DONE\n")
	writeResult(t, filepath.Join(results, "card-2", "RESULT.md"), "RESULT: BLOCKED\n")
	queue := filepath.Join(dir, "queue")
	writeResult(t, filepath.Join(queue, "ready", "card-9.md"), "a card\n")
	writeResult(t, filepath.Join(queue, "ready-pro", "card-10.md"), "a card\n")

	var out, errb strings.Builder
	code := Row(BenchRowInput{
		Store:   StoreOptions{Addr: mr.Addr()},
		Host:    "test-bench",
		Once:    true,
		Queue:   queue,
		Results: results,
		Since:   since,
		Stdout:  &out,
		Stderr:  &errb,
		Now:     func() time.Time { return at("2026-09-22T18:00:00Z") },
	})
	if code != 0 {
		t.Fatalf("row exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "queue=2 working=0 done=2 ok=1 fail=1") {
		t.Fatalf("the row's own receipt: %s", out.String())
	}

	// The TTL is what makes a stopped bench vanish rather than go stale.
	if ttl := mr.TTL("bench:test-bench"); ttl != BenchRowTTL {
		t.Fatalf("bench:test-bench TTL = %s, want %s", ttl, BenchRowTTL)
	}

	rdb, err := DialStore(context.Background(), StoreOptions{Addr: mr.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	st, err := ReadSwarmState(context.Background(), redisSwarmReader{rdb: rdb}, nil, at("2026-09-22T18:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rows) != 1 {
		t.Fatalf("want one row, got %+v", st.Rows)
	}
	r := st.Rows[0]
	if r.Host != "test-bench" || r.Queue != 2 || r.Done != 2 || r.OK != 1 || r.Fail != 1 {
		t.Fatalf("the table read back a different row than the bench pushed: %+v", r)
	}
	if r.OKPct() != 50 {
		t.Fatalf("ok%% = %d", r.OKPct())
	}
}

// One live-store test, gated, writing only bench:test-* and deleting it in Cleanup.
func TestSwarmTableAgainstTheLiveStore(t *testing.T) {
	if os.Getenv("NOVA_REDIS_TEST") != "1" {
		t.Skip("set NOVA_REDIS_TEST=1 and NOVA_REDIS_ADDR=<host:port>, under nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD, to reach the fleet store")
	}
	// The address is an environment variable and not a literal here, because a host name
	// written into a test is a test that tries to reach a machine on somebody else's CI.
	addr := os.Getenv("NOVA_REDIS_ADDR")
	if addr == "" {
		t.Skip("NOVA_REDIS_TEST=1 without NOVA_REDIS_ADDR names no store")
	}
	host := fmt.Sprintf("test-row-%d", os.Getpid())
	key := BenchKeyPrefix + host

	rdb, err := DialStore(context.Background(), StoreOptions{Addr: addr})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		_ = rdb.Del(context.Background(), key).Err()
		_ = rdb.Close()
	})

	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := Row(BenchRowInput{
		Store: StoreOptions{Addr: addr}, Host: host, Once: true, Since: since,
		Stdout: &out, Stderr: &errb,
	}); code != 0 {
		t.Fatalf("row against the live store exited %d: %s", code, errb.String())
	}
	st, err := ReadSwarmState(context.Background(), redisSwarmReader{rdb: rdb}, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	found := false
	for _, r := range st.Rows {
		if r.Host == host {
			found = true
		}
	}
	if !found {
		t.Fatalf("the row this test pushed is not on the table: %s", RenderSwarmTable(st, time.Now().UTC()))
	}
}

func writeResult(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
