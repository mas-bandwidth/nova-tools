package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestRouteReportCountsPerModel is #3949's DONE-WHEN: `nova-sprint route
// report --sprint S` over a fixture of 10 cards prints one MODEL line per
// model with the right ok, crash, refused, wall, fail and open counts and mean
// wall, most cards first, then the receipt with the totals. The model and
// wall come from the card's CURRENT attempt's result hash (an older attempt's
// model is ignored); the reason falls back to w_reason; a card with no
// result counts under "-"; a record the roster does not list is not read; an
// empty roster is REFUSED (exit 1) and a missing --sprint is usage (exit 2).
func TestRouteReportCountsPerModel(t *testing.T) {
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	const s = "rr-s"
	qwen, kimi, dspro, merc := "opencode/qwen3.6-plus", "openrouter/moonshotai/kimi-k3", "deepseek/deepseek-v4-pro", "inception/mercury-2.5"
	type fix struct {
		label, attempt, where, ok, reason string
		model, route, wReason, wallMS     string
	}
	for i, f := range []fix{
		{"c1", "1", "done", "ok", "done", qwen, "ocqwenplus", "done", "100000"},
		{"c2", "2", "done", "ok", "done", qwen, "ocqwenplus", "done", "200000"},
		{"c3", "1", "done", "fail", "crash", qwen, "ocqwenplus", "crash", "300000"},
		{"c4", "1", "done", "fail", "crash", kimi, "orkimi3", "crash", "400000"},
		{"c5", "1", "done", "fail", "crash", kimi, "orkimi3", "crash", "410000"},
		{"c6", "1", "done", "fail", "wall", kimi, "orkimi3", "wall", "1800000"},
		{"c7", "1", "done", "fail", "refused", dspro, "dspro", "refused", "50000"},
		{"c8", "1", "done", "fail", "", dspro, "dspro", "tests-red", "70000"},
		{"c9", "1", "working", "-", "", "", "", "", ""},
		{"c10", "1", "done", "ok", "done", merc, "mercury", "done", ""},
	} {
		id := "s:" + s + ":card:" + f.label
		if err := client.HSet(ctx, id, "attempt", f.attempt, "where", f.where, "where_ok", f.ok, "reason", f.reason, "route", "pro").Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.ZAdd(ctx, "sprint:"+s+":cards", redis.Z{Score: float64(1000 + i), Member: id}).Err(); err != nil {
			t.Fatal(err)
		}
		if f.model == "" {
			continue
		}
		if err := client.HSet(ctx, id+":result:a"+f.attempt, "w_model", f.model, "w_route", f.route, "w_reason", f.wReason, "w_wall_ms", f.wallMS).Err(); err != nil {
			t.Fatal(err)
		}
	}
	// c2's first attempt ran on kimi; only the current attempt counts. A
	// record outside the roster is never read.
	if err := client.HSet(ctx, "s:"+s+":card:c2:result:a1", "w_model", kimi, "w_route", "orkimi3", "w_wall_ms", "999000").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "s:"+s+":card:stray", "attempt", "1", "where", "done", "where_ok", "ok").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("route", "report", "--redis", addr, "--sprint", s)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	want := strings.Join([]string{
		"MODEL opencode/qwen3.6-plus route=ocqwenplus cards=3 ok=2 crash=1 refused=0 wall=0 fail=0 open=0 mean_wall_s=200",
		"MODEL openrouter/moonshotai/kimi-k3 route=orkimi3 cards=3 ok=0 crash=2 refused=0 wall=1 fail=0 open=0 mean_wall_s=870",
		"MODEL deepseek/deepseek-v4-pro route=dspro cards=2 ok=0 crash=0 refused=1 wall=0 fail=1 open=0 mean_wall_s=60",
		"MODEL - route=pro cards=1 ok=0 crash=0 refused=0 wall=0 fail=0 open=1 mean_wall_s=-",
		"MODEL inception/mercury-2.5 route=mercury cards=1 ok=1 crash=0 refused=0 wall=0 fail=0 open=0 mean_wall_s=-",
		"ROUTE REPORT sprint=rr-s cards=10 models=5 ok=3 crash=3 refused=1 wall=1 fail=1 open=1",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("route report printed\n%s\nwant\n%s", out, want)
	}
	if code, out, _ := runSprint("route", "report", "--redis", addr, "--sprint", "no-such"); code != 1 || !strings.HasPrefix(out, "REFUSED route report sprint=no-such") {
		t.Fatalf("an empty roster: exit %d out %q, want 1 and REFUSED", code, out)
	}
	if code, out, _ := runSprint("route", "report", "--redis", addr); code != 2 || out != "" {
		t.Fatalf("no --sprint: exit %d out %q, want 2 and nothing printed", code, out)
	}
}
