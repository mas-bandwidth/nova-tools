//go:build functional

package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// newRedis is a throwaway redis-server with the nova_sprint library loaded:
// a member's tasks land through ns_land_member.
func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

// seed puts task t<n> in ws:<strm>:merging scored by its age (created_at,
// here readAt) and gives it a pr record with one read at its head.
func seed(t *testing.T, c *redis.Client, n int, head string, readAt int64, lines ...string) {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("t%d", n)
	if err := c.ZAdd(ctx, WSKeyAt(0, strm, "merging"), redis.Z{Score: float64(readAt), Member: id}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:"+id, "stream", strm, "state", "merging", "pr", fmt.Sprintf("%s#%d", repo, n), "created_at", fmt.Sprint(readAt)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.SAdd(ctx, "ws:names", strm).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAddNX(ctx, "ws:order", redis.Z{Score: 1, Member: strm}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(ctx, c, repo, n, RecordFields{Head: head, Base: "dev", Stream: strm, Task: id}); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, err := AddLine(ctx, c, repo, n, l); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecordAndLines(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	var ref *RefusedError
	if _, err := Record(ctx, c, repo, 7, RecordFields{CI: "green"}); !errors.As(err, &ref) {
		t.Fatalf("a new record without head/base/stream: %v, want REFUSED", err)
	}
	if _, err := AddLine(ctx, c, repo, 7, "SCORE who=rowan"); !errors.As(err, &ref) {
		t.Fatalf("a line on no record: %v, want REFUSED", err)
	}
	r, err := Record(ctx, c, repo, 7, RecordFields{Head: "aaaaaaa1", Base: "dev", Stream: strm})
	if err != nil || !r.Created || r.CI != "pending" || r.State != "open" {
		t.Fatalf("create: %+v %v", r, err)
	}
	if r, err = Record(ctx, c, repo, 7, RecordFields{CI: "green", Mergeable: "true"}); err != nil || r.Created || r.CI != "green" || r.Head != "aaaaaaa1" {
		t.Fatalf("update: %+v %v", r, err)
	}
	if k, err := AddLine(ctx, c, repo, 7, score("rowan", "aaaaaaa1", 10)); err != nil || k != 1 {
		t.Fatalf("line 1: %d %v", k, err)
	}
	if k, _ := AddLine(ctx, c, repo, 7, "HOLD who=emma head=aaaaaaa1"); k != 2 {
		t.Fatalf("line 2: %d", k)
	}
	if _, err := AddLine(ctx, c, repo, 7, "two\nlines"); err == nil {
		t.Fatal("a two-line add was accepted")
	}
	// A new head: CI and mergeable were for the old one.
	if r, _ = Record(ctx, c, repo, 7, RecordFields{Head: "bbbbbbb2"}); r.CI != "pending" || r.Mergeable != "" {
		t.Fatalf("new head kept ci/mergeable: %+v", r)
	}
	recs, _ := LoadPRs(ctx, c, repo, []int{7})
	if len(recs[0].Reads) != 2 || recs[0].Task != "" {
		t.Fatalf("record: %+v", recs[0])
	}
}

func TestMembersOrderAndFilters(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	h := func(n int) string { return fmt.Sprintf("%07d%s", n, strings.Repeat("a", 33)) }
	seed(t, c, 3, h(3), 300, score("rowan", h(3), 10))
	seed(t, c, 1, h(1), 100, score("emma", h(1), 8))
	seed(t, c, 2, h(2), 200, score("rowan", h(2), 7))                                 // below the floor
	seed(t, c, 4, h(4), 50, score("rowan", "ffffffff", 10))                           // read at an old head
	seed(t, c, 5, h(5), 60, score("rowan", h(5), 10), "HOLD who=emma head="+h(5)[:8]) // held
	seed(t, c, 6, h(6), 70, score("jev", h(6), 10))                                   // jev only
	// A task of another repo in the same stream is not a member or a skip here.
	c.ZAdd(ctx, WSKeyAt(0, strm, "merging"), redis.Z{Score: 10, Member: "other"})
	c.HSet(ctx, "task:other", "pr", "mas-bandwidth/rowan-tools#9")
	// A task with a pr but no record.
	c.ZAdd(ctx, WSKeyAt(0, strm, "merging"), redis.Z{Score: 20, Member: "t8"})
	c.HSet(ctx, "task:t8", "pr", "8")

	ms, skips, err := Members(ctx, c, repo, []string{strm}, 8)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, m := range ms {
		got = append(got, m.N)
	}
	if fmt.Sprint(got) != "[1 3]" {
		t.Fatalf("members %v, want [1 3] (oldest pr_ready_at first)", got)
	}
	why := map[int]string{}
	for _, s := range skips {
		why[s.N] = s.Why
	}
	want := map[int]string{2: "score:7<8", 4: "no-read-at-head", 5: "hold:emma", 6: "no-read-at-head", 8: "no-record"}
	if fmt.Sprint(why) != fmt.Sprint(want) {
		t.Fatalf("skips %v, want %v", why, want)
	}
	// cfg:land raises the floor for product code.
	c.HSet(ctx, "cfg:land", "min_score:"+repo, "10")
	c.Set(ctx, "cfg:land:test:"+repo, "make check", 0)
	cfg, _ := LoadConfig(ctx, c, repo)
	if cfg.MinScore != 10 || cfg.Test != "make check" {
		t.Fatalf("config %+v", cfg)
	}
	ms, _, _ = Members(ctx, c, repo, []string{strm}, cfg.MinScore)
	if len(ms) != 1 || ms[0].N != 3 {
		t.Fatalf("at 10: %+v", ms)
	}
}

// TestMembersSkipsFailingJevLine: a JEV mech line that failed at head keeps
// a PR with a friend's 10 out of the batch; a passing one, one at an old
// head, or none lets it through; cfg:land jev turns the gate off or to
// require, and jev_passes narrows the passes that gate (nova-tools#3631).
func TestMembersSkipsFailingJevLine(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	h := func(n int) string { return fmt.Sprintf("%07d%s", n, strings.Repeat("b", 33)) }
	jevLine := func(head, gate, lint, scope, base string) string {
		return "JEV who=jev pass=mech head=" + head + " gate=" + gate + " lint=" + lint + " scope=" + scope + " base=" + base + " why=x"
	}
	seed(t, c, 1, h(1), 100, jevLine(h(1), "fail", "ok", "fail", "ok"), score("rowan", h(1), 10))
	seed(t, c, 2, h(2), 200, jevLine(h(2), "ok", "ok", "ok", "ok"), score("rowan", h(2), 10))
	seed(t, c, 3, h(3), 300, jevLine("ffffffff", "fail", "fail", "ok", "ok"), score("rowan", h(3), 10))
	seed(t, c, 4, h(4), 400, score("rowan", h(4), 10))
	seed(t, c, 5, h(5), 500, jevLine(h(5), "fail", "fail", "ok", "fail"), jevLine(h(5), "ok", "ok", "ok", "ok"), score("rowan", h(5), 10))
	seed(t, c, 6, h(6), 600, jevLine(h(6), "fail", "fail", "ok", "ok"))
	check := func(label, wantMembers string, wantSkips map[int]string) {
		t.Helper()
		ms, skips, err := Members(ctx, c, repo, []string{strm}, 8)
		if err != nil {
			t.Fatal(err)
		}
		var got []int
		for _, m := range ms {
			got = append(got, m.N)
		}
		why := map[int]string{}
		for _, s := range skips {
			why[s.N] = s.Why
		}
		if fmt.Sprint(got) != wantMembers || fmt.Sprint(why) != fmt.Sprint(wantSkips) {
			t.Fatalf("%s: members %v skips %v, want %s %v", label, got, why, wantMembers, wantSkips)
		}
	}
	check("gate", "[2 3 4 5]", map[int]string{1: "jev:scope", 6: "jev:lint"})
	c.HSet(ctx, "cfg:land", "jev", "require")
	check("require", "[2 5]", map[int]string{1: "jev:scope", 3: "no-jev-at-head", 4: "no-jev-at-head", 6: "jev:lint"})
	c.HSet(ctx, "cfg:land", "jev", "gate", "jev_passes", "lint,base")
	check("scope off", "[1 2 3 4 5]", map[int]string{6: "jev:lint"})
	c.HSet(ctx, "cfg:land", "jev", "off")
	check("off", "[1 2 3 4 5]", map[int]string{6: "no-read-at-head"})
}

func TestSaveBuiltParksAndLandMembersLands(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	for n := 1; n <= 3; n++ {
		seed(t, c, n, fmt.Sprintf("%040d", n), int64(n), score("rowan", fmt.Sprintf("%040d", n), 10))
	}
	ids := []string{"t1", "t2", "t3"}
	ms, _, _ := Members(ctx, c, repo, []string{strm}, 8)
	l := Landing{Repo: repo, Slug: "landing-streams-lander", Streams: strm, Base: "dev", BaseSHA: strings.Repeat("b", 40),
		Branch: "stream/landing-streams-lander", Head: strings.Repeat("c", 40), Members: []Member{ms[0], ms[2]},
		Parked: []Parked{{Member: ms[1], Why: "red:TestX"}}, PR: 900, State: "open", Tests: 3}
	moved, err := SaveBuilt(ctx, c, l, "rowan")
	if err != nil || moved != 1 {
		t.Fatalf("built: %d %v", moved, err)
	}
	if s, _ := c.ZScore(ctx, WSKeyAt(0, strm, "working"), "t2").Result(); s != 2 {
		t.Fatalf("parked t2 scores %v in working, want its age 2", s)
	}
	if st, _ := c.HGet(ctx, "task:t2", "state").Result(); st != "working" {
		t.Fatalf("task:t2 state %q", st)
	}
	if p, _ := c.HGet(ctx, PRKey(repo, 2), "park").Result(); !strings.HasPrefix(p, "PARKED "+repo+"#2 red:TestX") {
		t.Fatalf("park receipt %q", p)
	}
	if k, _ := c.HGet(ctx, PRKey(repo, 900), "kind").Result(); k != "stream" {
		t.Fatalf("the stream PR has no record: kind=%q", k)
	}
	if err := ws.Check(ctx, c, ids); err != nil {
		t.Fatalf("after built: %v", err)
	}
	got, ok, _ := LoadLanding(ctx, c, repo, l.Slug)
	if !ok || got.PR != 900 || len(got.Members) != 2 || got.Members[1].Task != "t3" || got.Members[1].Stream != strm || got.Parked[0].Why != "red:TestX" {
		t.Fatalf("landing %+v", got)
	}

	sha := strings.Repeat("d", 40)
	// Before the landing is merged the member step is fenced: nothing moves.
	if _, err := LandMembers(ctx, c, got, "rowan", sha, nil); err == nil || !strings.Contains(err.Error(), "STALE") {
		t.Fatalf("an unmerged landing's member step: %v, want STALE", err)
	}
	already, err := SaveLanded(ctx, c, got, "rowan", sha)
	if err != nil || already {
		t.Fatalf("landed: %t %v", already, err)
	}
	if n, _ := c.ZCard(ctx, WSKeyAt(0, strm, "merging")).Result(); n != 2 {
		t.Fatalf("SaveLanded moved tasks: merging has %d", n)
	}
	res, err := LandMembers(ctx, c, got, "rowan", sha, map[int]string{1: "-"})
	if err != nil || res.Moved != 2 || res.Missing != 0 || res.Lines != 2 || len(res.Skipped) != 0 {
		t.Fatalf("land members: %+v %v", res, err)
	}
	if n, _ := c.ZCard(ctx, WSKeyAt(0, strm, "merging")).Result(); n != 0 {
		t.Fatalf("merging still has %d", n)
	}
	if n, _ := c.ZCard(ctx, WSKeyAt(0, strm, "landed")).Result(); n != 2 {
		t.Fatalf("landed has %d", n)
	}
	if why, _ := c.HGet(ctx, "task:t3", "why").Result(); why != "landed with nova-tools#900 (dddddddd)" {
		t.Fatalf("t3 why %q", why)
	}
	want := CloseLine(fmt.Sprintf("%040d", 3), l.Branch, l.Head, repo, 900, sha)
	if want != "CLOSE who=lander head=00000000 landed: in stream/landing-streams-lander at cccccccc with nova-tools#900 (dddddddd)" {
		t.Fatalf("close line %q", want)
	}
	recs, _ := LoadPRs(ctx, c, repo, []int{3, 1})
	for _, r := range recs {
		line := CloseLine(fmt.Sprintf("%040d", r.N), l.Branch, l.Head, repo, 900, sha)
		if r.Reads[len(r.Reads)-1] != line {
			t.Fatalf("#%d reads %q, want the CLOSE line last", r.N, r.Reads)
		}
		if ls, _ := c.LRange(ctx, PRKey(repo, r.N)+":lines", 0, -1).Result(); len(ls) != 1 || ls[0] != line {
			t.Fatalf("#%d :lines %q", r.N, ls)
		}
		if cl, _ := c.HGet(ctx, PRKey(repo, r.N), "close").Result(); cl != line {
			t.Fatalf("#%d close %q", r.N, cl)
		}
	}
	if recs[1].Closes != "-" {
		t.Fatalf("#1 closes %q, want the - it was given", recs[1].Closes)
	}
	log, _ := c.XRange(ctx, "ws:log", "-", "+").Result()
	if len(log) != 4 { // one park, the landing, two lands
		t.Fatalf("ws:log has %d entries", len(log))
	}
	if !strings.HasPrefix(fmt.Sprint(log[1].Values["why"]), "LANDED "+repo+"#900") {
		t.Fatalf("landing entry %v", log[1].Values)
	}
	if err := ws.Check(ctx, c, ids); err != nil {
		t.Fatalf("after land: %v", err)
	}
	// A re-run adds no line and moves nothing; a second SaveLanded is ALREADY.
	if res, err := LandMembers(ctx, c, got, "rowan", sha, nil); err != nil || res.Moved != 0 || res.Lines != 0 {
		t.Fatalf("re-run: %+v %v", res, err)
	}
	if already, _ = SaveLanded(ctx, c, got, "rowan", "x"); !already {
		t.Fatal("a second landed call moved again")
	}
}

// roundTrips counts client round trips: one per command, one per pipeline.
type roundTrips struct{ n int }

func (r *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { r.n++; return next(ctx, cmd) }
}

func (r *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { r.n++; return next(ctx, cmds) }
}

// The Redis parts for 1,000 members are a fixed number of round trips, never
// one per member and never a SCAN (the sub-second bar comes from that; the
// measured time is logged, not asserted, per the CI-WAITS rule).
func TestRedisPartsForAThousandAreFixedRoundTrips(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	pipe := c.Pipeline()
	head := strings.Repeat("a", 40)
	for n := 1; n <= 1000; n++ {
		id := fmt.Sprintf("t%d", n)
		pipe.ZAdd(ctx, WSKeyAt(0, strm, "merging"), redis.Z{Score: float64(n), Member: id})
		pipe.HSet(ctx, "task:"+id, "stream", strm, "state", "merging", "pr", fmt.Sprint(n))
		pipe.HSet(ctx, PRKey(repo, n), "head", head, "base", "dev", "stream", strm, "state", "open", "reads", score("rowan", head, 10))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// Load the script once so the count below is EVALSHA only.
	if err := script.Load(ctx, c).Err(); err != nil {
		t.Fatal(err)
	}
	rt := &roundTrips{}
	c.AddHook(rt)
	start := time.Now()
	ms, _, err := Members(ctx, c, repo, []string{strm}, 8)
	if err != nil || len(ms) != 1000 {
		t.Fatalf("members %d %v", len(ms), err)
	}
	l := Landing{Repo: repo, Slug: "s", Streams: strm, Base: "dev", Branch: "stream/s", Head: head, Members: ms, PR: 1, State: "open"}
	if _, err := SaveBuilt(ctx, c, l, "rowan"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := LoadLanding(ctx, c, repo, "s")
	if _, err := SaveLanded(ctx, c, got, "rowan", "m"); err != nil {
		t.Fatal(err)
	}
	res, err := LandMembers(ctx, c, got, "rowan", "m", nil)
	if err != nil || res.Moved != 1000 {
		t.Fatalf("moved %d %v", res.Moved, err)
	}
	t.Logf("members + built + load + landed + members landed for 1,000: %d round trips in %v", rt.n, time.Since(start))
	if rt.n != 7 { // members 3 (merging, task pr, records), built 1, load 1, landed 1, land members 1
		t.Fatalf("%d round trips for 1,000 members, want 7", rt.n)
	}
	if n, _ := c.ZCard(ctx, WSKeyAt(0, strm, "landed")).Result(); n != 1000 {
		t.Fatalf("landed %d", n)
	}
}

func TestStatusReadsRecordsOnly(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	l := Landing{Repo: repo, Slug: "a", Streams: "a", Base: "dev", Branch: "stream/a", Head: strings.Repeat("c", 40), PR: 5, State: "open"}
	if _, err := SaveBuilt(ctx, c, l, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(ctx, c, repo, 5, RecordFields{CI: "green", Mergeable: "true"}); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadStatus(ctx, c, repo)
	if err != nil || len(rows) != 1 || rows[0].CI != "green" || rows[0].Mergeable != "true" || !rows[0].HeadMatch {
		t.Fatalf("status %+v %v", rows, err)
	}
}

// The script and the member step end to end on a real redis-server (skipped
// on a laptop without one, required under NOVA_CI=1).
func TestLuaOnRealRedis(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	for n := 1; n <= 2; n++ {
		seed(t, c, n, fmt.Sprintf("%040d", n), int64(n), score("rowan", fmt.Sprintf("%040d", n), 10))
	}
	ms, _, err := Members(ctx, c, repo, []string{strm}, 8)
	if err != nil || len(ms) != 2 {
		t.Fatalf("members %+v %v", ms, err)
	}
	l := Landing{Repo: repo, Slug: "r", Streams: strm, Base: "dev", Branch: "stream/r", Head: strings.Repeat("c", 40),
		Members: ms[:1], Parked: []Parked{{Member: ms[1], Why: "red:TestY"}}, PR: 77, State: "open"}
	if moved, err := SaveBuilt(ctx, c, l, "rowan"); err != nil || moved != 1 {
		t.Fatalf("built %d %v", moved, err)
	}
	got, _, _ := LoadLanding(ctx, c, repo, "r")
	if _, err := SaveLanded(ctx, c, got, "rowan", "m"); err != nil {
		t.Fatalf("landed %v", err)
	}
	if res, err := LandMembers(ctx, c, got, "rowan", "m", nil); err != nil || res.Moved != 1 {
		t.Fatalf("land members %+v %v", res, err)
	}
	if n, _ := c.XLen(ctx, "ws:log").Result(); n != 3 {
		t.Fatalf("ws:log %d entries", n)
	}
}

// TestLandedWritesTheRelease (#4050): the landed call of a nova-tools landing
// into dev writes fleet:release version v<train>-dev.<merge sha8> and commit,
// the train carried from the version already stored; a re-run writes
// nothing, and a landing into another base names no release.
func TestLandedWritesTheRelease(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()
	c.HSet(ctx, "fleet:release", "version", "v0.17.0-dev.11111111", "commit", "1111111"+strings.Repeat("0", 33))
	land := func(slug, base, sha string) (bool, string) {
		t.Helper()
		l := Landing{Repo: repo, Slug: slug, Streams: strm, Base: base, BaseSHA: strings.Repeat("b", 40),
			Branch: "stream/" + slug, Head: strings.Repeat("c", 40), PR: 900, State: "open"}
		if _, err := SaveBuilt(ctx, c, l, "rowan"); err != nil {
			t.Fatal(err)
		}
		already, rel, err := saveLanded(ctx, c, l, "rowan", sha)
		if err != nil {
			t.Fatal(err)
		}
		return already, rel
	}
	sha := "abcdef01" + strings.Repeat("2", 32)
	if already, rel := land("a", "dev", sha); already || rel != "v0.17.0-dev.abcdef01" {
		t.Fatalf("landed into dev: already=%t release=%q", already, rel)
	}
	got := c.HGetAll(ctx, "fleet:release").Val()
	if got["version"] != "v0.17.0-dev.abcdef01" || got["commit"] != sha || got["landed"] != "nova-tools#900" {
		t.Fatalf("fleet:release = %v", got)
	}
	l, _, _ := LoadLanding(ctx, c, repo, "a")
	if already, rel, err := saveLanded(ctx, c, l, "rowan", strings.Repeat("9", 40)); err != nil || !already || rel != "" {
		t.Fatalf("re-run: already=%t release=%q %v", already, rel, err)
	}
	if already, rel := land("b", "main", strings.Repeat("9", 40)); already || rel != "" {
		t.Fatalf("landed into main: already=%t release=%q", already, rel)
	}
	if v := c.HGet(ctx, "fleet:release", "commit").Val(); v != sha {
		t.Fatalf("commit after a re-run and a main landing = %q", v)
	}
	c.Del(ctx, "fleet:release")
	if _, rel := land("c", "dev", sha); rel != "v0.16.0-dev.abcdef01" {
		t.Fatalf("first landing with no release stored: %q", rel)
	}
}
