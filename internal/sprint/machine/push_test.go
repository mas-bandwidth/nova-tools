package machine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// routes is a deliverer that records each push, fails a route named "bad", and
// holds a route named "slow" until it is let go.
type routes struct {
	mu      sync.Mutex
	got     []store.Reminder
	slow    chan struct{}
	entered chan struct{}
	calls   int
}

type route struct {
	r    *routes
	name string
}

func (r *routes) deliver(name string) (store.Deliverer, error) { return route{r, name}, nil }

func (d route) Deliver(m store.Reminder) error {
	d.r.mu.Lock()
	d.r.calls++
	d.r.mu.Unlock()
	switch d.name {
	case "bad":
		return errors.New("no such route")
	case "slow":
		d.r.entered <- struct{}{}
		<-d.r.slow
	}
	d.r.mu.Lock()
	defer d.r.mu.Unlock()
	d.r.got = append(d.r.got, m)
	return nil
}

// TestPushOnePerPeriod: phase 2 of R14 pushes a claimed period once, however
// often the loop sees the claim, and again only for the next period's claim;
// a push that does not arrive within its time limit is timed out and the loop
// does not wait for it (2.3, R14).
func TestPushOnePerPeriod(t *testing.T) {
	t.Parallel()
	rs := &routes{slow: make(chan struct{}), entered: make(chan struct{}, 1)}
	t.Cleanup(func() { close(rs.slow) })
	p := newPusher(rs.deliver)
	claim := func(person, route string, gen uint64, r int64) Claim {
		return Claim{Goal: Goal{Person: person, Text: "keep going", Route: route, ClaimedGen: gen, ClaimedR: r}, Packet: Packet{Open: 2}}
	}
	ctx := context.Background()
	if out := p.run(ctx, []Claim{claim("ann", "ok", 1, 1000)}); len(out) != 1 || !out[0].Delivered {
		t.Fatalf("the first claim: %+v", out)
	}
	if out := p.run(ctx, []Claim{claim("ann", "ok", 1, 1000), claim("ann", "ok", 1, 1000)}); len(out) != 0 {
		t.Fatalf("a claim seen again pushed: %+v", out)
	}
	if out := p.run(ctx, []Claim{claim("ann", "ok", 1, 1000+5*60*1000)}); len(out) != 1 || !out[0].Delivered {
		t.Fatalf("the next period: %+v", out)
	}
	if len(rs.got) != 2 || rs.got[0].To != "ann" {
		t.Fatalf("pushed %+v", rs.got)
	}
	if out := p.run(ctx, []Claim{claim("bob", "bad", 1, 1000)}); len(out) != 1 || out[0].Delivered || out[0].Err == "" {
		t.Fatalf("a failing route: %+v", out)
	}
	// A route that does not return: the push gives up when its context ends
	// (PushLimit, or the caller's sooner end), and the loop does not wait.
	stop, cancel := context.WithCancel(ctx)
	go func() {
		<-rs.entered
		cancel()
	}()
	out := p.run(stop, []Claim{claim("cy", "slow", 1, 1000)})
	if len(out) != 1 || !out[0].TimedOut || out[0].Delivered {
		t.Fatalf("a slow route: %+v", out)
	}
}

// TestPushAfterRT3OncePerClaim: the loop pushes phase 1's claims of the steps
// that applied, after RT3, and a claim the next tick sees again is not pushed
// again (2.3, R14: at most one push a period).
func TestPushAfterRT3OncePerClaim(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	rs := &routes{}
	cfg := Config{Names: testNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{dealRule(64)}, Build: testBuild(builderOpts{}),
		Deliver: rs.deliver,
		Claims: func(rule string, s *sprint.Snapshot, rp sprint.RulePlan) []Claim {
			return []Claim{{Goal: Goal{Person: "ann", Route: "ok", Text: "keep going", ClaimedGen: 1, ClaimedR: 7}}}
		}}
	l, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	k := &counting{c: w.tw}
	w.tick(l, k)
	for i := 0; i < 3; i++ {
		w.verb(create("s1:ready", fresh(), "p"+string(rune('1'+i))))
		w.clk.add(TickEvery)
		w.tick(l, k)
	}
	if len(rs.got) != 1 || len(l.outcomes) != 1 || !l.outcomes[0].Delivered {
		t.Fatalf("pushed %d times, outcomes %+v", len(rs.got), l.outcomes)
	}
}
