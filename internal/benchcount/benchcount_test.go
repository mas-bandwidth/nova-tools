package benchcount_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/benchcount"
	"github.com/redis/go-redis/v9"
)

// orderTheTestRequires is the funnel #2687 names. Landed stays zero through
// cut and ok; useful stays zero until #2680's rule is met, which cannot be
// before the landed event.
const orderTheTestRequires = "cut, then ok, then landed"

func TestTheOrderIsCutThenOKThenLanded(t *testing.T) {
	if benchcount.FunnelOrder != orderTheTestRequires {
		t.Fatalf("the order the test requires is %q, the fold says %q", orderTheTestRequires, benchcount.FunnelOrder)
	}
	rdb := start(t)
	ctx := context.Background()
	if err := rdb.HSet(ctx, "bench:hulk",
		"queue", "12", "working", "8", "done", "140", "ok", "119", "fail", "21",
	).Err(); err != nil {
		t.Fatal(err)
	}
	fold := benchcount.New()
	label := "card-a"
	steps := []struct {
		id     string
		kind   string
		extra  map[string]string
		want   benchcount.Counts
		landed string
		useful string
		cost   string
	}{
		{
			id: "1-0", kind: "cut",
			want:   benchcount.Counts{Cut: 1},
			landed: "0", useful: "0", cost: "-",
		},
		{
			id: "2-0", kind: "ok",
			extra:  map[string]string{"usd": "1.5"},
			want:   benchcount.Counts{Cut: 1, OK: 1, USD: 1.5},
			landed: "0", useful: "0", cost: "-",
		},
		{
			id: "3-0", kind: "pullreq",
			extra: map[string]string{
				"defect": "verified", "issue": "2687", "receipt": "receipted", "pr": "2700",
			},
			want:   benchcount.Counts{Cut: 1, OK: 1, USD: 1.5},
			landed: "0", useful: "0", cost: "-",
		},
		{
			// The lander does not name the bench. The card stays on hulk.
			id: "4-0", kind: "landed",
			extra:  map[string]string{"bench": ""},
			want:   benchcount.Counts{Cut: 1, OK: 1, Landed: 1, Useful: 1, USD: 1.5},
			landed: "1", useful: "1", cost: "1.500000",
		},
	}
	for _, step := range steps {
		fields := map[string]string{"label": label, "bench": "hulk", "event": step.kind}
		for k, v := range step.extra {
			if k == "bench" && v == "" {
				delete(fields, "bench")
				continue
			}
			fields[k] = v
		}
		if err := fold.Pass(ctx, rdb, []benchcount.Entry{{ID: step.id, Fields: fields}}); err != nil {
			t.Fatalf("after %s: %s", step.kind, err)
		}
		if got := fold.Bench("hulk"); got != step.want {
			t.Fatalf("after %s (the order the test requires is %s): got %+v, want %+v", step.kind, orderTheTestRequires, got, step.want)
		}
		assertHash(t, ctx, rdb, "hulk", step.landed, step.useful, step.cost)
	}
	all, err := rdb.HGetAll(ctx, "bench:hulk").Result()
	if err != nil {
		t.Fatal(err)
	}
	if all["queue"] != "12" || all["working"] != "8" || all["done"] != "140" || all["ok"] != "119" || all["fail"] != "21" {
		t.Fatalf("the fold rewrote bench-row's fields: %#v", all)
	}
	if _, ok := all["cut"]; ok {
		t.Fatalf("the fold wrote cut onto the hash: %#v", all)
	}
}

func TestUsefulIsLandedPlusVerifiedDefectPlusReceiptedIssue(t *testing.T) {
	cases := []struct {
		name   string
		events []benchcount.Entry
		want   benchcount.Counts
	}{
		{
			name:   "landed alone is not useful",
			events: []benchcount.Entry{ev("1-0", "card", "landed", nil)},
			want:   benchcount.Counts{Landed: 1},
		},
		{
			name: "verified defect without a receipted issue",
			events: []benchcount.Entry{ev("1-0", "card", "landed", map[string]string{
				"defect": "verified",
			})},
			want: benchcount.Counts{Landed: 1},
		},
		{
			name: "receipted issue without a verified defect",
			events: []benchcount.Entry{ev("1-0", "card", "landed", map[string]string{
				"issue": "2687", "receipt": "receipted",
			})},
			want: benchcount.Counts{Landed: 1},
		},
		{
			name: "an issue that was not receipted",
			events: []benchcount.Entry{ev("1-0", "card", "landed", map[string]string{
				"defect": "verified", "issue": "2687",
			})},
			want: benchcount.Counts{Landed: 1},
		},
		{
			name: "a receipt with no issue id",
			events: []benchcount.Entry{ev("1-0", "card", "landed", map[string]string{
				"defect": "verified", "receipt": "receipted",
			})},
			want: benchcount.Counts{Landed: 1},
		},
		{
			name: "an unverified defect",
			events: []benchcount.Entry{ev("1-0", "card", "landed", map[string]string{
				"defect": "unverified", "issue": "2687", "receipt": "receipted",
			})},
			want: benchcount.Counts{Landed: 1},
		},
		{
			name: "the rule on the landed entry",
			events: []benchcount.Entry{ev("1-0", "card", "landed", map[string]string{
				"defect": "verified", "issue": "2680", "receipt": "receipted",
			})},
			want: benchcount.Counts{Landed: 1, Useful: 1},
		},
		{
			name: "facts split across pullreq and landed",
			events: []benchcount.Entry{
				ev("1-0", "card", "pullreq", map[string]string{"issue": "2687", "receipt": "receipted"}),
				ev("2-0", "card", "landed", map[string]string{"defect": "verified"}),
			},
			want: benchcount.Counts{Landed: 1, Useful: 1},
		},
		{
			name: "the rule before the landed event is not useful yet",
			events: []benchcount.Entry{ev("1-0", "card", "pullreq", map[string]string{
				"defect": "verified", "issue": "2687", "receipt": "receipted",
			})},
			want: benchcount.Counts{},
		},
		{
			name: "ok cannot carry the rule",
			events: []benchcount.Entry{
				ev("1-0", "card", "ok", map[string]string{
					"defect": "verified", "issue": "2687", "receipt": "receipted",
				}),
				ev("2-0", "card", "landed", nil),
			},
			want: benchcount.Counts{OK: 1, Landed: 1},
		},
		{
			name: "harvested cannot carry the rule",
			events: []benchcount.Entry{
				ev("1-0", "card", "harvested", map[string]string{
					"defect": "verified", "issue": "2687", "receipt": "receipted",
				}),
				ev("2-0", "card", "landed", nil),
			},
			want: benchcount.Counts{Landed: 1},
		},
		{
			name: "pr is the pullreq kind the writers emit",
			events: []benchcount.Entry{
				ev("1-0", "card", "pr", map[string]string{
					"defect": "verified", "issue": "2687", "receipt": "receipted",
				}),
				ev("2-0", "card", "landed", nil),
			},
			want: benchcount.Counts{Landed: 1, Useful: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := benchcount.New()
			for _, e := range tc.events {
				f.Apply(e)
			}
			if got := f.Bench("hulk"); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestALandingDoesNotMoveTheCardOffItsBench(t *testing.T) {
	f := benchcount.New()
	f.Apply(ev("1-0", "card", "cut", nil))
	f.Apply(benchcount.Entry{ID: "2-0", Fields: map[string]string{
		"label": "card", "bench": "vision", "event": "landed",
		"defect": "verified", "issue": "2687", "receipt": "receipted",
	}})
	if got := f.Bench("hulk"); got.Landed != 1 || got.Useful != 1 {
		t.Fatalf("hulk = %+v, want the card that ran there", got)
	}
	if got := f.Bench("vision"); got != (benchcount.Counts{}) {
		t.Fatalf("vision stole the card: %+v", got)
	}
}

func TestLandedWithNoEarlierBenchUsesTheLandedEntry(t *testing.T) {
	f := benchcount.New()
	f.Apply(benchcount.Entry{ID: "1-0", Fields: map[string]string{
		"label": "card", "event": "landed", "bench": "Space",
	}})
	if got := f.Bench("space"); got.Landed != 1 || got.Useful != 0 {
		t.Fatalf("space = %+v", got)
	}
	if got := f.Bench("hulk"); got.Landed != 0 {
		t.Fatalf("hulk = %+v", got)
	}
}

func TestRedeliveryDoesNotCountTwice(t *testing.T) {
	f := benchcount.New()
	e := ev("9-1", "card", "landed", map[string]string{
		"defect": "verified", "issue": "2687", "receipt": "receipted", "usd": "0.5",
	})
	f.Apply(e)
	f.Apply(e)
	f.Apply(ev("9-2", "card", "landed", map[string]string{"usd": "0.5"}))
	got := f.Bench("hulk")
	if got.Landed != 1 || got.Useful != 1 || got.USD != 1 {
		t.Fatalf("got %+v, want one card, both spends, one landing", got)
	}
}

func TestQueuedCountsAsCut(t *testing.T) {
	f := benchcount.New()
	f.Apply(ev("1-0", "card", "queued", nil))
	if got := f.Bench("hulk"); got.Cut != 1 || got.OK != 0 || got.Landed != 0 {
		t.Fatalf("queued did not count as cut: %+v", got)
	}
}

func TestFixtureStreamIsOneHGETPerBench(t *testing.T) {
	rdb := start(t)
	ctx := context.Background()
	seed := []map[string]string{
		{"label": "card-a", "bench": "hulk", "event": "cut"},
		{"label": "card-a", "bench": "hulk", "event": "ok", "usd": "1.5"},
		{"label": "card-a", "bench": "hulk", "event": "pullreq", "defect": "verified", "issue": "2687", "receipt": "receipted"},
		{"label": "card-a", "bench": "hulk", "event": "landed"},
		{"label": "card-b", "bench": "hulk", "event": "ok", "usd": "0.5"},
		{"label": "card-b", "bench": "hulk", "event": "landed"},
		{"label": "card-c", "bench": "space", "event": "landed", "usd": "4"},
	}
	for _, fields := range seed {
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: benchcount.Stream, Values: fields}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	fold, err := benchcount.FoldStream(ctx, rdb, benchcount.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := benchcount.Write(ctx, rdb, fold.Counts()); err != nil {
		t.Fatal(err)
	}
	assertHash(t, ctx, rdb, "hulk", "2", "1", "2.000000")
	assertHash(t, ctx, rdb, "space", "1", "0", "-")

	again, err := benchcount.FoldStream(ctx, rdb, benchcount.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if again.Bench("hulk") != fold.Bench("hulk") || again.Bench("space") != fold.Bench("space") {
		t.Fatalf("replay diverged: hulk %+v vs %+v, space %+v vs %+v",
			again.Bench("hulk"), fold.Bench("hulk"), again.Bench("space"), fold.Bench("space"))
	}

	hook := &hgetCount{}
	rdb.AddHook(hook)
	cost, err := benchcount.CostPerUseful(ctx, rdb, "hulk")
	if err != nil {
		t.Fatal(err)
	}
	if cost != "2.000000" {
		t.Fatalf("cost = %q, want 2.000000", cost)
	}
	if hook.n != 1 {
		t.Fatalf("cost per useful card took %d HGETs, want 1", hook.n)
	}
	hook.n = 0
	missing, err := benchcount.CostPerUseful(ctx, rdb, "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if missing != "-" {
		t.Fatalf("missing bench cost = %q, want a dash", missing)
	}
	if hook.n != 1 {
		t.Fatalf("a missing bench took %d HGETs, want 1", hook.n)
	}
}

func TestTheFoldDoesNotPollGitHub(t *testing.T) {
	src, err := os.ReadFile("benchcount.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, banned := range []string{"api.github.com", "os/exec", "net/http", "gh pr"} {
		if strings.Contains(text, banned) {
			t.Fatalf("benchcount.go contains %q; the counts are read from the stream, not from GitHub", banned)
		}
	}
}

func ev(id, label, kind string, extra map[string]string) benchcount.Entry {
	fields := map[string]string{"label": label, "bench": "hulk", "event": kind}
	for k, v := range extra {
		fields[k] = v
	}
	return benchcount.Entry{ID: id, Fields: fields}
}

func assertHash(t *testing.T, ctx context.Context, rdb *redis.Client, host, landed, useful, cost string) {
	t.Helper()
	key := "bench:" + host
	gotLanded, err := rdb.HGet(ctx, key, benchcount.FieldLanded).Result()
	if err != nil {
		t.Fatalf("%s landed: %s", host, err)
	}
	gotUseful, err := rdb.HGet(ctx, key, benchcount.FieldUseful).Result()
	if err != nil {
		t.Fatalf("%s useful: %s", host, err)
	}
	gotCost, err := rdb.HGet(ctx, key, benchcount.FieldUSDPerUseful).Result()
	if err != nil {
		t.Fatalf("%s cost: %s", host, err)
	}
	if gotLanded != landed || gotUseful != useful || gotCost != cost {
		t.Fatalf("%s hash landed=%s useful=%s cost=%s, want landed=%s useful=%s cost=%s",
			host, gotLanded, gotUseful, gotCost, landed, useful, cost)
	}
}

func start(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

type hgetCount struct{ n int }

func (h *hgetCount) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *hgetCount) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), "hget") {
			h.n++
		}
		return next(ctx, cmd)
	}
}

func (h *hgetCount) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
