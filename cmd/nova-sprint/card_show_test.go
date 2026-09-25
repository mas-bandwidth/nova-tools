package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCardShowPrintsTheRecord is #3689 (Glenn 2026-09-24 11:55 PM: "it should
// be in REDIS, not on files"): `nova-sprint card show --sprint S --label L`
// prints the card hash (never its token) and its current attempt's result hash
// from one ns_card_show call, then one SHOWN receipt; a card that does not
// exist is refused (exit 1) and a missing flag is usage (exit 2).
func TestCardShowPrintsTheRecord(t *testing.T) {
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "s:show-s:card:c1", "state", "ended", "attempt", "2", "outcome", "DONE", "token", "2.secret-token").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "s:show-s:card:c1:result:a2", "valid", "1", "c_branch", "nova/show-s/c1-a2", "w_line2", "DONE",
		"w_note", "two\nlines", "w_model", "opencode/kimi-k3").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("card", "show", "--redis", addr, "--sprint", "show-s", "--label", "c1")
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	for _, want := range []string{"card.state ended\n", "card.outcome DONE\n", "result.valid 1\n", "result.c_branch nova/show-s/c1-a2\n",
		"result.w_line2 DONE\n", "result.w_model opencode/kimi-k3\n", "SHOWN card show-s/c1 state=ended attempt=2 outcome=DONE valid=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("card show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-token") || strings.Contains(out, "card.token ") {
		t.Fatalf("card show printed the token:\n%s", out)
	}
	if strings.Count(out, "\n") != 9 {
		t.Fatalf("want 8 record lines (a multi-line value escaped onto one) and a receipt:\n%s", out)
	}
	if code, out, _ := runSprint("card", "show", "--redis", addr, "--sprint", "show-s", "--label", "nope"); code != 1 || !strings.HasPrefix(out, "REFUSED card show show-s/nope") {
		t.Fatalf("a missing card: exit %d out %q, want 1 and REFUSED", code, out)
	}
	if code, _, _ := runSprint("card", "show", "--redis", addr, "--sprint", "show-s"); code != 2 {
		t.Fatalf("no --label: exit %d, want 2", code)
	}
}
