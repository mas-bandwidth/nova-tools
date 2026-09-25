package deal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestDealerIsTheOneSlotLedger is the dealer half of #3877's DONE-WHEN: a bench's
// capacity is bench:<b>:desired in Redis and nothing else. batman on 2026-09-25 is the
// shape: desired slots 4, five queued cards. The dealer reserves four and refuses the
// fifth (it stays queued in the pool), and the refusal writes no lease file: the bench
// home the test runs under has no nova-bench/slots directory before or after.
func TestDealerIsTheOneSlotLedger(t *testing.T) {
	const sprint = "oneledger-3877"
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := dealRedis(t)
	seedFleet(t, c, sprint, 5, map[string]int{"batman": 4})
	seedLease(t, c, "live-token")

	res, err := newFnStore(c).Reserve(ctx, "live-token", "batman", poolCards(sprint, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 {
		t.Fatalf("dealt %d, want 4: bench:batman:desired slots is the cap", len(res))
	}
	if got := zcard(t, c, "bench:batman:starting"); got != 4 {
		t.Fatalf("bench:batman:starting holds %d, want 4", got)
	}
	dealt := map[string]bool{}
	for _, r := range res {
		dealt[r.Card.Label] = true
	}
	var refused string
	for _, card := range poolCards(sprint, 5) {
		if !dealt[card.Label] {
			refused = card.Label
		}
	}
	if refused == "" {
		t.Fatal("no card was refused")
	}
	if st, _ := c.HGet(ctx, "s:"+sprint+":card:"+refused, "state").Result(); st != "queued" {
		t.Fatalf("the card beyond desired is %q, want queued (refused, still in the pool)", st)
	}
	if _, err := c.ZScore(ctx, "s:"+sprint+":pool", refused).Result(); err != nil {
		t.Fatalf("the refused card left the pool: %v", err)
	}
	// A second call on the full bench deals nothing.
	if again, err := newFnStore(c).Reserve(ctx, "live-token", "batman", poolCards(sprint, 5)); err != nil || len(again) != 0 {
		t.Fatalf("full bench: %d reservations, err %v", len(again), err)
	}
	if _, err := os.Stat(filepath.Join(home, "nova-bench", "slots")); !os.IsNotExist(err) {
		t.Fatalf("the dealer wrote a slot store under the bench home: %v", err)
	}
}
