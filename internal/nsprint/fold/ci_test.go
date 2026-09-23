package fold_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

const ciBase = "2222222222222222222222222222222222222222"

func fullSHA(prefix string) string { return prefix + strings.Repeat("0", 40-len(prefix)) }

// ciAttempt is one ended attempt of a ci card as its `ci end` receipt.
type ciAttempt struct {
	outcome, reason, verdict string
	wallS                    int
}

// addCIHead writes one ci card, its verdict record and its receipts in the
// shapes internal/nsprint/fn/lua/ci.lua (nova-tools #2936) writes: `ci cut`,
// then per attempt a `ci end` (and a `ci rerun` between attempts). The
// record is the latest pointer: the last attempt's verdict and wall.
// Receipt evidence carries wall_s=<n> unless noWall is set.
func addCIHead(t *testing.T, c *redis.Client, sprint, pr, head string, cutMs, endMs int64, noWall bool, attempts ...ciAttempt) {
	t.Helper()
	ctx := context.Background()
	s := "s:" + sprint
	label := "ci-" + pr + "-" + head[:8]
	recKey := "ci:nova-tools:" + head
	last := attempts[len(attempts)-1]
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.HSet(ctx, s+":card:"+label, map[string]any{
		"kind": "script", "state": "ended", "outcome": last.outcome, "reason": last.reason,
		"ci_for": "nova-tools " + pr + " " + head + " " + ciBase, "ci_repo": "nova-tools", "ci_pr": pr,
		"ci_head": head, "base": ciBase, "attempt": len(attempts), "reruns": len(attempts) - 1,
		"cut_at": cutMs,
	}).Err())
	must(c.SAdd(ctx, s+":idx:card:ended", label).Err())
	rec := map[string]any{"attempt": len(attempts), "card": sprint + "/" + label, "head": head,
		"base": ciBase, "pr": pr, "repo": "nova-tools", "wall_s": last.wallS,
		"cut_at": cutMs, "end_at": endMs, "source": "card"}
	if last.verdict != "" {
		rec["verdict"] = last.verdict
	}
	must(c.HSet(ctx, recKey, rec).Err())
	receipt := func(kind, from, to string, attempt int, reason, evidence string) {
		must(c.XAdd(ctx, &redis.XAddArgs{Stream: s + ":log", Values: []string{
			"kind", kind, "id", label, "from", from, "to", to, "attempt", fmt.Sprint(attempt),
			"token_sha", "", "actor", "ctl", "reason", reason, "evidence", evidence, "idem", "", "at", "1"}}).Err())
	}
	receipt("ci cut", "", "queued", 1, "cut", recKey)
	for i, a := range attempts {
		if i > 0 {
			receipt("ci rerun", "ended", "queued", i+1, "policy ci_reruns", recKey+" b1->b2")
		}
		v := a.verdict
		if v == "" {
			v = "MISSING"
		}
		ev := recKey + " " + v + " bench=b" + fmt.Sprint(i+1) + " pkg= test= log=results/x"
		if !noWall {
			ev += fmt.Sprintf(" wall_s=%d", a.wallS)
		}
		receipt("ci end", "running", "ended", i+1, a.outcome+" "+a.reason, ev)
	}
}

// removeCICards takes every ci card out of the fixture's indexes and hashes.
func removeCICards(t *testing.T, c *redis.Client, sprint string) {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "s:"+sprint+":idx:card:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		for _, l := range c.SMembers(ctx, k).Val() {
			if c.HGet(ctx, "s:"+sprint+":card:"+l, "ci_for").Val() != "" {
				c.SRem(ctx, k, l)
				c.Del(ctx, "s:"+sprint+":card:"+l)
			}
		}
	}
}

// addCIHeadBase writes one ci card and a one-attempt record under its own
// label, pr and base, distinct from addCIHead's ciBase-only shape: it lets
// a test put two cards on the same head under two different bases so the
// shared ci:<repo>:<head> record (keyed by head alone) ends up reflecting
// whichever was written last. Used to test that the landed-PR wall loop
// only counts wall from a record whose base matches the card's own base
// (Stella's HOLD 7 on nova-tools #3060).
func addCIHeadBase(t *testing.T, c *redis.Client, sprint, pr, head, label, base, verdict string, cutMs, endMs int64, wallS int) {
	t.Helper()
	ctx := context.Background()
	s := "s:" + sprint
	recKey := "ci:nova-tools:" + head
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.HSet(ctx, s+":card:"+label, map[string]any{
		"kind": "script", "state": "ended", "outcome": "DONE", "reason": "done",
		"ci_for": "nova-tools " + pr + " " + head + " " + base, "ci_repo": "nova-tools", "ci_pr": pr,
		"ci_head": head, "base": base, "attempt": 1, "reruns": 0, "cut_at": cutMs,
	}).Err())
	must(c.SAdd(ctx, s+":idx:card:ended", label).Err())
	rec := map[string]any{"attempt": 1, "card": s + "/" + label, "head": head,
		"base": base, "pr": pr, "repo": "nova-tools", "wall_s": wallS,
		"cut_at": cutMs, "end_at": endMs, "source": "card"}
	if verdict != "" {
		rec["verdict"] = verdict
	}
	must(c.HSet(ctx, recKey, rec).Err())
	evidence := recKey + " " + verdict + " bench=b1 pkg= test= log=results/x wall_s=" + strconv.Itoa(wallS)
	must(c.XAdd(ctx, &redis.XAddArgs{Stream: s + ":log", Values: []string{
		"kind", "ci end", "id", label, "from", "running", "to", "ended", "attempt", "1",
		"token_sha", "", "actor", "ctl", "reason", "done", "evidence", evidence, "idem", "", "at", "1"}}).Err())
}

// seedCI is the fold fixture (three landed PRs: 101, 102, 105) with its
// placeholder ci card replaced by three required ci heads: 101 OK at the
// first attempt; 102 crashed on its first attempt (FAILED crash, no verdict)
// and its one rerun came back OK; 103 (not landed) FAIL.
func seedCI(t *testing.T, noWall bool) (*redis.Client, string) {
	t.Helper()
	_, client, fx := seed(t)
	removeCICards(t, client, fx.Sprint)
	const t0 = int64(1_790_000_000_000)
	addCIHead(t, client, fx.Sprint, "101", fullSHA("aaaa1111"), t0, t0+200_000, noWall,
		ciAttempt{"DONE", "done", "OK", 120})
	addCIHead(t, client, fx.Sprint, "102", fullSHA("aaaa2222"), t0, t0+600_000, noWall,
		ciAttempt{"FAILED", "crash", "", 30}, ciAttempt{"DONE", "done", "OK", 150})
	addCIHead(t, client, fx.Sprint, "103", fullSHA("aaaa3333"), t0, t0+100_000, noWall,
		ciAttempt{"DONE", "done", "FAIL", 90})
	return client, fx.Sprint
}

// #2756 10.4 item 5 and control 34: the fold charges every ci attempt,
// failures, crashes and reruns included, on its own ci cost line, writes ci
// bench-minutes and wall per landed PR, and leaves the model cost per landed
// card exactly as it is without the ci cards.
func TestFoldCICostOnItsOwnLine(t *testing.T) {
	ctx := context.Background()

	t.Run("fixture", func(t *testing.T) {
		client, s := seedCI(t, false)
		sum, err := fold.Read(ctx, client, s)
		if err != nil {
			t.Fatal(err)
		}
		c := sum.CI
		// 4 attempts: 101 once, 102 twice (crash + rerun), 103 once.
		// bench 120+30+150+90 = 390 s = 6.5 min over 3 landed PRs = 2.17 min.
		// wall cut->verdict: 101 200 s, 102 600 s; 105 has no ci head.
		want := fold.CICost{Heads: 3, OK: 2, Attempts: 4, Unmeasured: 0, BenchS: 390,
			LandedPRs: 3, WallS: 800, WallPRs: 2}
		if c != want {
			t.Fatalf("ci cost %+v\nwant     %+v", c, want)
		}
		var out bytes.Buffer
		fold.PrintLines(&out, sum)
		line := "FOLD CI COST sprint=" + s + " heads=3 ok=2 attempts=4 unmeasured=0 bench_min=6.5 landed_prs=3 bench_min_per_landed=2.17 wall_min_per_landed=6.67 wall_unmeasured=1\n"
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q in\n%s", line, out.String())
		}
		// Its own line: the sprint line's dollars are the model cards' alone.
		if !strings.Contains(out.String(), "FOLD SPRINT sprint="+s+" cards=9 done=7 useful=4 landed=3 usd=2.45 usd_per_useful=0.6125 usd_per_landed=0.816667 ") {
			t.Fatalf("the sprint line moved with the ci cards:\n%s", out.String())
		}
		sexp := string(fold.Sexp(sum))
		for _, w := range []string{
			`:ci-cost (:heads 3 :ok 2 :attempts 4 :unmeasured 0 :bench-min "6.5" :landed-prs 3 :bench-min-per-landed "2.17" :wall-min-per-landed "6.67" :wall-unmeasured 1)`,
		} {
			if !strings.Contains(sexp, w) {
				t.Fatalf("fold record lacks %q:\n%s", w, sexp)
			}
		}
	})

	t.Run("model-cost-identical-with-and-without-ci-cards", func(t *testing.T) {
		with, s := seedCI(t, false)
		withSum, err := fold.Read(ctx, with, s)
		if err != nil {
			t.Fatal(err)
		}
		_, without, fx := seed(t)
		removeCICards(t, without, fx.Sprint)
		withoutSum, err := fold.Read(ctx, without, fx.Sprint)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(withSum.Total, withoutSum.Total) || !reflect.DeepEqual(withSum.Routes, withoutSum.Routes) {
			t.Fatalf("model cost moved with the ci cards:\nwith    %+v %+v\nwithout %+v %+v",
				withSum.Total, withSum.Routes, withoutSum.Total, withoutSum.Routes)
		}
		if withoutSum.CI.Attempts != 0 || withoutSum.CI.Heads != 0 {
			t.Fatalf("no ci cards, ci cost %+v", withoutSum.CI)
		}
		var out bytes.Buffer
		fold.PrintLines(&out, withoutSum)
		if !strings.Contains(out.String(), "FOLD CI COST sprint="+fx.Sprint+" heads=0 ok=0 attempts=0 unmeasured=0 bench_min=- landed_prs=3 bench_min_per_landed=- wall_min_per_landed=- wall_unmeasured=3\n") {
			t.Fatalf("empty ci cost line:\n%s", out.String())
		}
	})

	t.Run("an-attempt-without-a-wall-is-unmeasured-never-zero", func(t *testing.T) {
		// Receipts that carry no wall_s: only the record's latest attempt of
		// each head has a wall, so 102's crashed attempt is charged as an
		// attempt and counted unmeasured, never priced at zero.
		client, s := seedCI(t, true)
		sum, err := fold.Read(ctx, client, s)
		if err != nil {
			t.Fatal(err)
		}
		if sum.CI.Attempts != 4 || sum.CI.Unmeasured != 1 || sum.CI.BenchS != 360 {
			t.Fatalf("ci cost %+v, want 4 attempts, 1 unmeasured, 360 s measured", sum.CI)
		}
	})

	// Stella's HOLD 7 on nova-tools #3060: the same head cut against two
	// bases shares one ci:<repo>:<head> record (keyed by head alone), so
	// the record ends up reflecting whichever cut was written last. A
	// landed PR's own ci card here is the earlier, ciBase cut; a second,
	// unrelated PR (999, not landed) recut the same head against another
	// base afterward, so the shared record now belongs to that other base.
	// The landed PR's wall must be left unmeasured, never borrowed from
	// the other base's cut/end interval.
	t.Run("wall-only-counts-the-matching-base-record", func(t *testing.T) {
		client, s := seedCI(t, false)
		const otherBase = "3333333333333333333333333333333333333333"
		head := fullSHA("aaaa6666")
		ctx := context.Background()
		if err := client.HSet(ctx, "s:"+s+":card:c6", map[string]any{
			"kind": "model", "route": "sonnet", "state": "landed", "outcome": "DONE",
			"repo": "nova-tools", "pr": "106", "head": "aaaa6666", "usd": "0.30",
		}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.SAdd(ctx, "s:"+s+":idx:card:landed", "c6").Err(); err != nil {
			t.Fatal(err)
		}
		const t0 = int64(1_790_000_000_000)
		// 106's own cut, base ciBase: cut->end would be 300 s if counted.
		addCIHeadBase(t, client, s, "106", head, "ci-106-aaaa6666", ciBase, "OK", t0, t0+300_000, 100)
		// An unrelated PR 999 recuts the same head against another base
		// afterward: the shared record now points at this cut instead.
		addCIHeadBase(t, client, s, "999", head, "ci-999-aaaa6666", otherBase, "OK", t0+50_000, t0+450_000, 200)

		sum, err := fold.Read(ctx, client, s)
		if err != nil {
			t.Fatal(err)
		}
		// Heads/OK/Attempts/BenchS pick up both new cards; the wall stays
		// exactly the 101+102 baseline (800 s, 2 PRs): 106 is unmeasured,
		// not the 400 s a base-blind match would borrow from PR 999's cut.
		want := fold.CICost{Heads: 5, OK: 3, Attempts: 6, Unmeasured: 0, BenchS: 690,
			LandedPRs: 4, WallS: 800, WallPRs: 2}
		if sum.CI != want {
			t.Fatalf("ci cost %+v\nwant     %+v (106's wall must stay unmeasured, base ciBase != record base otherBase)", sum.CI, want)
		}
		var out bytes.Buffer
		fold.PrintLines(&out, sum)
		line := "FOLD CI COST sprint=" + s + " heads=5 ok=3 attempts=6 unmeasured=0 bench_min=11.5 landed_prs=4 bench_min_per_landed=2.88 wall_min_per_landed=6.67 wall_unmeasured=2\n"
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q in\n%s", line, out.String())
		}
	})

	t.Run("the-fold-commit-carries-the-ci-cost", func(t *testing.T) {
		client, s := seedCI(t, false)
		work := workRepo(t)
		var out bytes.Buffer
		if _, err := fold.Run(ctx, client, fold.Options{Sprint: s, Work: work, Actor: "fold-test"}, &out); err != nil {
			t.Fatalf("fold: %v\n%s", err, out.String())
		}
		body, err := os.ReadFile(filepath.Join(work, "docs", "roadmaps", "folds", s+".sexp"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte(`:ci-cost (:heads 3 :ok 2 :attempts 4 `)) {
			t.Fatalf("the committed fold lacks the ci cost:\n%s", body)
		}
	})
}
