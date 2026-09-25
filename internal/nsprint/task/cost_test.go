package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// TestControl55Cost is #2756 control 55 (nova-tools #3105) on the task side:
// a read's done evidence carries its cost (cost_usd, tokens_in, tokens_out,
// model, route) or `cost: unmetered <reason>`; a missing cost field parses
// as not found, never as $0; a cost that does not parse refuses the done.
func TestControl55Cost(t *testing.T) {
	t.Parallel()

	metered := task.Cost{Metered: true, USDMicro: 400000, TokensIn: 91000, TokensOut: 2100, Model: "claude-sonnet-5", Route: "child"}
	if got, want := metered.Clause(), "cost_usd=0.4 tokens_in=91000 tokens_out=2100 model=claude-sonnet-5 route=child"; got != want {
		t.Fatalf("metered clause = %q; want %q", got, want)
	}
	if got, want := (task.Cost{Reason: "plan-billed main session"}).Clause(), "cost: unmetered plan-billed main session"; got != want {
		t.Fatalf("unmetered clause = %q; want %q", got, want)
	}
	for _, tc := range []struct {
		evidence string
		found    bool
		want     task.Cost
		bad      bool
	}{
		{"APPROVE 9/10 at abc1234 | " + metered.Clause(), true, metered, false},
		{"APPROVE 9/10 at abc1234 | cost_usd=1.10", true, task.Cost{Metered: true, USDMicro: 1100000}, false},
		{"APPROVE 9/10 at abc1234 | cost: unmetered plan-billed | later", true, task.Cost{Reason: "plan-billed"}, false},
		{"APPROVE 9/10 at abc1234 | cost_usd=-", true, task.Cost{Reason: "cost_usd=-"}, false},
		{"APPROVE 9/10 at abc1234", false, task.Cost{}, false},
		{"APPROVE 9/10 | cost_usd=a-dollar", true, task.Cost{}, true},
		{"APPROVE 9/10 | cost_usd=0.40 tokens_in=lots", true, task.Cost{}, true},
	} {
		got, found, err := task.ParseCost(tc.evidence)
		if (err != nil) != tc.bad || found != tc.found || (!tc.bad && got != tc.want) {
			t.Errorf("ParseCost(%q) = %+v, %v, %v; want %+v, %v, bad=%v", tc.evidence, got, found, err, tc.want, tc.found, tc.bad)
		}
	}

	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-55"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-a", 4)
	head := strings.Repeat("c", 40)
	id := task.ReviewID("repo", 55, head, "ctl-a")
	client.HSet(ctx, "s:"+sprint+":pr:repo:55", "head", head)
	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: sprint, ID: id, Kind: task.KindRead, Title: "read repo#55",
		Effects: task.EffectsNone, Repo: "repo", PR: 55, Head: head, To: "ctl-a",
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push = %s, %v; want CREATED", got, err)
	}
	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: id, As: "ctl-a"})
	if err != nil || !ok {
		t.Fatalf("take = %v, %v", ok, err)
	}
	done := task.DoneRequest{
		Sprint: sprint, ID: id, Token: claim.Token, Evidence: "APPROVE 9/10 at " + head[:7],
		Verdict: "APPROVE", Score: "9", Head: head, Cost: &metered,
	}

	// A cost that does not parse is refused before anything is written.
	bad := done
	bad.Cost = nil
	bad.Evidence += " | cost_usd=a-dollar"
	if got, err := task.Done(ctx, st, bad); err == nil {
		t.Fatalf("done with cost_usd=a-dollar = %s; want refused", got)
	}
	if state, _ := client.HGet(ctx, "task:"+id, "state").Result(); state == "closed" {
		t.Fatal("a refused cost closed the task")
	}

	if got, err := task.Done(ctx, st, done); err != nil || got != task.DoneClosed {
		t.Fatalf("done = %s, %v; want DONE", got, err)
	}
	evidence, _ := client.HGet(ctx, "task:"+id, "evidence").Result()
	if want := "APPROVE 9/10 at " + head[:7] + " | " + metered.Clause(); evidence != want {
		t.Fatalf("stored evidence = %q; want %q", evidence, want)
	}
	if got, found, err := task.ParseCost(evidence); err != nil || !found || got != metered {
		t.Fatalf("stored cost = %+v, %v, %v; want %+v", got, found, err, metered)
	}
	// The same done again is identical evidence: exit 0 CLOSED, not CONFLICT.
	if got, err := task.Done(ctx, st, done); err != nil || got != task.DoneRepeat {
		t.Fatalf("repeated done = %s, %v; want CLOSED", got, err)
	}
	// A cost is not evidence: empty evidence stays empty and is refused.
	if got := task.WithCost("", metered); got != "" {
		t.Fatalf("WithCost on empty evidence = %q; want empty", got)
	}
}
