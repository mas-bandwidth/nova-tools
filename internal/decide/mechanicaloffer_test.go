package decide

import (
	"context"
	"strings"
	"testing"
)

// #1513: a mechanical kind with no CONFIRMED failure is offered its supported
// rung ALONE, so the rules answer without a decision being asked at all.
func TestAMechanicalKindIsOfferedOneRungUntilSomethingFails(t *testing.T) {
	reg := testRegistry(t)
	fake := &fakeDecider{choice: "rung-2", conf: 0.99}
	u := Unit{ID: "u", Kind: KindRebase, Files: 2, Packages: 1}
	rules := mustRoute(t, reg, u, DefaultFloor)
	res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("a mechanical kind with nothing confirmed-failed is offered its supported rung alone, so no decision is asked; the provider was called %d time(s)", fake.calls)
	}
	if res.Rung.Name != rules.Rung.Name {
		t.Errorf("with nothing confirmed-failed the rules' own rung stands: got %s, want %s", res.Rung.Name, rules.Rung.Name)
	}
	if !strings.Contains(res.Reason, "one eligible rung, so no decision to ask") {
		t.Errorf("the reason must say no decision was asked: %s", res.Reason)
	}
}

// The other half: once a mechanical kind carries a CONFIRMED failure the
// step-up offer is restored, and the rung the provider chose is the answer.
func TestAConfirmedFailureRestoresTheStepUpOffer(t *testing.T) {
	reg := testRegistry(t)
	fake := &fakeDecider{choice: "rung-2", conf: 0.99}
	u := Unit{ID: "u", Kind: KindRebase, Files: 2, Packages: 1, Attempts: []Attempt{
		{Rung: "flash", Outcome: OutcomeFailed, Reason: "the card rung missed it"},
	}}
	res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("a confirmed failure restores the step-up offer; the provider was called %d time(s), want 1", fake.calls)
	}
	if len(res.Offered) < 2 {
		t.Fatalf("a confirmed failure restores the step-up offer; offered %v", res.Offered)
	}
	if res.Rung.Name != res.Offered[1] {
		t.Errorf("the result names the rung the provider chose: got %s, offered %v", res.Rung.Name, res.Offered)
	}
	if res.Source != SourceJev {
		t.Errorf("source = %q, want %q", res.Source, SourceJev)
	}
}

// #1860 (Stella's HOLD on #1513): offer()'s bound limits how many HEIGHTS it
// gathers, not how many minds it gathers AT one height, so a mechanical kind's
// supported height with two eligible minds of different lineages -- Stella's
// own witness shape -- slipped a provider call past the len(offered) < 2
// proxy. The explicit Mechanical/no-confirmed-failure guard in routeJev must
// catch this even though the offered set itself is not a singleton.
func TestASameHeightMechanicalOfferWithTwoMindsMakesNoCall(t *testing.T) {
	reg, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"low-a","lineage":"one","height":0,"availability":"available","ask":"card"},
	  {"name":"low-b","lineage":"two","height":0,"availability":"available","ask":"card"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeDecider{choice: "low-a", conf: 0.99}
	u := Unit{ID: "u-same-height", Kind: KindRebase, Files: 2, Packages: 1}
	rules := mustRoute(t, reg, u, DefaultFloor)
	res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Offered) < 2 {
		t.Fatalf("the witness needs a same-height offer of more than one mind to be a regression at all: offered %v", res.Offered)
	}
	if fake.calls != 0 {
		t.Fatalf("a mechanical kind with nothing confirmed-failed is offered its supported rung alone, even when that height holds more than one eligible mind; the provider was called %d time(s), offered %v", fake.calls, res.Offered)
	}
	if res.Rung.Name != rules.Rung.Name {
		t.Errorf("with nothing confirmed-failed the rules' own rung stands: got %s, want %s", res.Rung.Name, rules.Rung.Name)
	}
	if !strings.Contains(res.Reason, "one eligible rung, so no decision to ask") {
		t.Errorf("the reason must say no decision was asked: %s", res.Reason)
	}
}
