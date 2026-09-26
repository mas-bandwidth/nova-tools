//go:build functional

package deal

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// TestPitstopDealPassPlansNothingUntilCleared is the dealer half of the
// DONE-WHEN of nova-tools #3371: with s:<S>:pitstop set, a deal pass plans
// nothing, reserves nothing and opens no session; ns_card_deal called
// directly (a dealer that skipped the plan) deals nothing from the sprint;
// cleared, the same pass deals the pool.
func TestPitstopDealPassPlansNothingUntilCleared(t *testing.T) {
	t.Parallel()

	const sprint = "control-00003371"
	ctx := context.Background()
	c := dealRedis(t)
	seedFleet(t, c, sprint, 8, map[string]int{"ctl-a": 8})
	seedLease(t, c, "lease-1")
	if r, err := pitstop.Set(ctx, c, sprint, "rowan", "control", false, ""); err != nil || r.Outcome != pitstop.Done {
		t.Fatalf("set: %+v %v", r, err)
	}

	in, err := RedisSource{Client: c}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Sprints) != 1 || !in.Sprints[0].Pitstop {
		t.Fatalf("source sprints = %+v, want the one sprint read as pit-stopped", in.Sprints)
	}
	if plan := Plan(in, DefaultRefusedHold); len(plan) != 0 {
		t.Fatalf("pit-stopped plan = %+v, want nothing", plan)
	}

	f := newFixture(t)
	st := newFnStore(c)
	p := &Pass{Source: RedisSource{Client: c}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 0 || res.Launched() != 0 || st.calls["ctl-a"] != 0 {
		t.Fatalf("pit-stopped pass: rounds %d launched %d reserve calls %d, want 0 0 0", res.Rounds, res.Launched(), st.calls["ctl-a"])
	}
	if n := len(f.lines("ctl-a", "sessions.log")); n != 0 {
		t.Fatalf("pit-stopped pass opened %d sessions", n)
	}
	if n := zcard(t, c, "s:"+sprint+":pool"); n != 8 {
		t.Fatalf("pool = %d, want 8 untouched", n)
	}

	// The function is the backstop: a reservation that skips the plan is refused.
	res2, err := st.Reserve(ctx, "lease-1", "ctl-a", poolCards(sprint, 2))
	if err != nil {
		t.Fatal(err)
	}
	if len(res2) != 0 || zcard(t, c, "bench:ctl-a:cards:working") != 0 {
		t.Fatalf("ns_card_deal on a pit-stopped sprint reserved %+v", res2)
	}
	if n := len(logEntries(t, c, sprint, "card deal", "")); n != 0 {
		t.Fatalf("pit-stopped sprint has %d card deal receipts", n)
	}

	if r, err := pitstop.Clear(ctx, c, sprint, "rowan", ""); err != nil || r.Outcome != pitstop.Done {
		t.Fatalf("clear: %+v %v", r, err)
	}
	res, err = p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Launched() != 8 {
		t.Fatalf("cleared pass launched %d, want 8 (%+v)", res.Launched(), res.Benches)
	}
	if n := zcard(t, c, "s:"+sprint+":pool"); n != 0 {
		t.Fatalf("pool = %d after the cleared pass, want 0", n)
	}
}
