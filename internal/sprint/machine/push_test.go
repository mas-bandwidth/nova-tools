package machine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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
// that applied, after RT3 and off the tick: a tick whose push hangs returns
// without waiting for it; a claim the next tick sees again is not pushed
// again (2.3, R14: at most one push a period). Phase 3 records each outcome
// in the next RT1's error step: a failed push opens "a reminder could not be
// delivered" on the person, once, and a delivered one closes it.
func TestPushAfterRT3OncePerClaim(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	rs := &routes{slow: make(chan struct{}), entered: make(chan struct{}, 1)}
	var mu sync.Mutex
	route, r := "slow", int64(7)
	cfg := Config{Names: testNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{dealRule(64)}, Build: testBuild(builderOpts{}),
		Deliver: rs.deliver,
		Claims: func(rule string, s *sprint.Snapshot, rp sprint.RulePlan) []Claim {
			mu.Lock()
			defer mu.Unlock()
			return []Claim{{Goal: Goal{Person: "ann", Route: route, Text: "keep going", ClaimedGen: 1, ClaimedR: r}}}
		}}
	l, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	k := &counting{c: w.tw}
	w.tick(l, k)
	n := 0
	busy := func() Report {
		n++
		w.verb(create("s1:ready", fresh(), fmt.Sprintf("p%d", n)))
		w.clk.add(TickEvery)
		return w.tick(l, k)
	}
	if rep := busy(); rep.Pushes != 1 {
		t.Fatalf("the first claim: %+v", rep)
	}
	<-rs.entered // the push hangs on its route, and the tick has returned
	if rep := busy(); rep.Pushes != 0 || len(rep.Outcomes) != 0 {
		t.Fatalf("a claim seen again: %+v", rep)
	}
	close(rs.slow)
	l.pusher.wait()
	if rep := busy(); len(rep.Outcomes) != 1 || !rep.Outcomes[0].Delivered || rs.calls != 1 {
		t.Fatalf("phase 3 of the first push: %+v, %d calls", rep.Outcomes, rs.calls)
	}
	mu.Lock()
	route, r = "bad", r+5*60*1000
	mu.Unlock()
	busy()
	l.pusher.wait()
	if rep := busy(); len(rep.Outcomes) != 1 || rep.Outcomes[0].Delivered || len(w.hash("jopen:ann@0")) != 1 {
		t.Fatalf("phase 3 of a failed push: %+v, jopen %v", rep.Outcomes, w.hash("jopen:ann@0"))
	}
	mu.Lock()
	route, r = "ok", r+5*60*1000
	mu.Unlock()
	busy()
	l.pusher.wait()
	if rep := busy(); len(rep.Outcomes) != 1 || !rep.Outcomes[0].Delivered || len(w.hash("jopen:ann@0")) != 0 || l.failures != 0 {
		t.Fatalf("phase 3 of the next period's push: %+v, jopen %v, failures %d", rep.Outcomes, w.hash("jopen:ann@0"), l.failures)
	}
}

// TestPushOutcomesBounded: at most PushesInFlightMax pushes run at once, a
// claim past them is counted and not pushed, and the outcomes kept for phase 3
// are at most OutcomesMax, the oldest dropped (2.3, R14).
func TestPushOutcomesBounded(t *testing.T) {
	t.Parallel()
	rs := &routes{}
	p := newPusher(rs.deliver)
	var claims []Claim
	for i := 0; i < PushesInFlightMax+1; i++ {
		claims = append(claims, Claim{Goal: Goal{Person: fmt.Sprintf("p%d", i), Route: "ok", ClaimedGen: 1, ClaimedR: 1}})
	}
	started, skipped := p.start(context.Background(), claims)
	if started+skipped != len(claims) || started > PushesInFlightMax {
		t.Fatalf("started %d, skipped %d", started, skipped)
	}
	p.wait()
	for round := 0; round < 4; round++ {
		var more []Claim
		for i := 0; i < PushesInFlightMax; i++ {
			more = append(more, Claim{Goal: Goal{Person: fmt.Sprintf("q%d-%d", round, i), Route: "ok", ClaimedGen: 1, ClaimedR: 1}})
		}
		p.start(context.Background(), more)
		p.wait()
	}
	if out := p.take(); len(out) != OutcomesMax || p.lost != started+4*PushesInFlightMax-OutcomesMax {
		t.Fatalf("kept %d outcomes, lost %d", len(out), p.lost)
	}
}

// TestPushDeliverThatNeverReturnsIsAbandoned: a deliverer that never returns,
// and a route lookup that never returns, are each abandoned at the push's
// time limit: the slot is freed, the outcome is a time out, and phase 3's
// note opens "a reminder could not be delivered" on the person (2.3, R14).
func TestPushDeliverThatNeverReturnsIsAbandoned(t *testing.T) {
	t.Parallel()
	rs := &routes{slow: make(chan struct{}), entered: make(chan struct{}, 2)}
	t.Cleanup(func() { close(rs.slow) })
	lookup := make(chan struct{})
	t.Cleanup(func() { close(lookup) })
	p := newPusher(func(name string) (store.Deliverer, error) {
		if name == "nolookup" {
			<-lookup
		}
		return rs.deliver(name)
	})
	p.limit = 50 * time.Millisecond
	claims := []Claim{
		{Goal: Goal{Person: "ann", Route: "slow", ClaimedGen: 1, ClaimedR: 1}},
		{Goal: Goal{Person: "bob", Route: "nolookup", ClaimedGen: 1, ClaimedR: 1}},
	}
	if started, skipped := p.start(context.Background(), claims); started != 2 || skipped != 0 {
		t.Fatalf("started %d, skipped %d", started, skipped)
	}
	<-rs.entered // ann's deliverer is stuck in its route; bob's lookup is stuck before its own
	p.wait()     // each push ends at its limit; one that does not is the package's test timeout
	out := p.take()
	if len(out) != 2 || p.running != 0 {
		t.Fatalf("outcomes %+v, %d slots held", out, p.running)
	}
	for _, o := range out {
		if !o.TimedOut || o.Delivered || o.Err == "" {
			t.Fatalf("an abandoned push: %+v", o)
		}
		n := outcomeNote(o)
		if n.Op != "open" || n.Type != sprint.NRemindFailed || len(n.Subjects) != 1 || n.Subjects[0] != o.Person {
			t.Fatalf("the note of %+v: %+v", o, n)
		}
	}
	// The slot is free: a claim of the next period pushes.
	if started, _ := p.start(context.Background(), []Claim{{Goal: Goal{Person: "ann", Route: "ok", ClaimedGen: 1, ClaimedR: 2}}}); started != 1 {
		t.Fatalf("the next period's claim started %d", started)
	}
	p.wait()
}
