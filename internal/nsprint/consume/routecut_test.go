package consume

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

// TestRouteAdoptionCutsOnce is nova-tools #3496 item 2: the ok-to-friend rule
// `nova-sprint route` builds (RouteOkFriend) cuts the ci card for a harvested
// head through StoreCICut, the hook the pr-to-read adoption uses. With no
// base tip recorded the harvested event stays pending (no ci card, no reads,
// never a stop); once the tip is recorded the next pass cuts exactly once and
// routes the read; a redelivered harvested event answers EXISTS and cuts
// nothing more. Without the wiring the pass routes the read with no ci card.
func TestRouteAdoptionCutsOnce(t *testing.T) {
	t.Parallel()

	st, client := controlRedis(t)
	ctx := context.Background()
	const sprint, label, pr = "control-3496c0de", "card-6", 106
	seedSprint(t, client, sprint)
	must(t, client.HSet(ctx, "bench:"+ctlBench+":desired", "slots", "4", "paused", "0", "legs", "go").Err())
	must(t, client.HSet(ctx, "bench:"+ctlBench+":state", "state", "UP", "at", "1").Err())

	ok := RouteOkFriend(st, sprint, "route-okf", "route")
	ok.Block = -1
	if ok.CICut == nil {
		t.Fatal("RouteOkFriend wires no CICut; route's ok-to-friend would route reads with cut=0")
	}
	must(t, ok.Start(ctx))
	endCard(t, client, sprint, label, "DONE", "done", "internal/x/f.go", "")
	if _, err := ok.Pass(ctx); err != nil {
		t.Fatalf("pass after ended: %v", err)
	}
	head := strings.Repeat("6", 40)
	harvestCard(t, client, sprint, label, pr, head)

	// The ci card label names the (head, tested base tip) pair (#3148).
	tip := strings.Repeat("b", 40)
	ciLabel := ci.Label(pr, head, tip)
	ciKey := "s:" + sprint + ":card:" + ciLabel
	cuts := func() int {
		t.Helper()
		entries, err := client.XRange(ctx, "s:"+sprint+":log", "-", "+").Result()
		must(t, err)
		n := 0
		for _, e := range entries {
			if e.Values["kind"] == "ci cut" && e.Values["id"] == ciLabel {
				n++
			}
		}
		return n
	}

	// No base tip: the cut cannot be made, the event waits pending.
	must(t, client.Del(ctx, civerdict.TipKey(ctlRepo, "dev")).Err())
	if _, err := ok.Pass(ctx); !errors.Is(err, ErrCutSkipped) {
		t.Fatalf("pass with no base tip = %v; want ErrCutSkipped (retryable, event pending)", err)
	}
	if !retryable(ErrNoBaseTip) {
		t.Fatal("ErrNoBaseTip is not retryable; a missing tip would stop the router")
	}
	if n, _ := client.Exists(ctx, ciKey).Result(); n != 0 || cuts() != 0 {
		t.Fatalf("no tip: ci card exists=%d cuts=%d, want none", n, cuts())
	}
	if got := reviewTasks(t, client, sprint, label); len(got) != 0 {
		t.Fatalf("no tip: %d reads %v, want 0 before the cut", len(got), got)
	}
	pending, err := client.XPending(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
	must(t, err)
	if pending.Count != 1 {
		t.Fatalf("no tip: pending = %d, want the harvested event kept", pending.Count)
	}

	// Tip recorded: one cut, one read, the card review-ready.
	must(t, client.HSet(ctx, civerdict.TipKey(ctlRepo, "dev"), "sha", tip).Err())
	if _, err := ok.Pass(ctx); err != nil {
		t.Fatalf("pass with the tip recorded: %v", err)
	}
	card, err := client.HMGet(ctx, ciKey, "verdict", "attempt", "base", "base_sha", "ci_head").Result()
	must(t, err)
	if card[0] != "PENDING" || card[1] != "1" || card[2] != "dev" || card[3] != tip || card[4] != head {
		t.Fatalf("ci card verdict/attempt/base/base_sha/ci_head = %v, want PENDING 1 dev %s %s", card, tip, head)
	}
	if got := reviewTasks(t, client, sprint, label); len(got) != 1 {
		t.Fatalf("after the cut: %d reads %v, want exactly 1", len(got), got)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":card:"+label, "state").Result(); state != "review-ready" {
		t.Fatalf("%s state %q, want review-ready", label, state)
	}

	// A redelivered cut for the same head writes nothing: still one cut.
	if err := ok.CICut(ctx, CICut{Sprint: sprint, Label: label, Repo: ctlRepo, PR: pr,
		Head: head, Base: "09fbedc9", BaseRef: "dev"}); err != nil {
		t.Fatalf("second cut of the same head = %v, want nil (EXISTS)", err)
	}
	if n := cuts(); n != 1 {
		t.Fatalf("ci cut receipts for %s = %d, want exactly 1", head[:8], n)
	}
	if a, _ := client.HGet(ctx, ciKey, "attempt").Result(); a != "1" {
		t.Fatalf("ci card attempt %q after a redelivery, want 1", a)
	}
}
