package decide

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The presence travels through the route and into the log row: an absent
// counter is an ABSENCE there too, and an explicit zero is a zero.
func TestRouteCarriesUsagePresence(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)
	u := Unit{ID: "u", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}

	silent := &recorder{conf: 0.95} // a 200, an answer, and no usage at all
	res, err := RouteJev(context.Background(), silent, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.Calls != 1 {
		t.Fatalf("a call was made: %+v", res.Usage)
	}
	if res.Usage.HasInput || res.Usage.HasOutput || res.Usage.Known() {
		t.Errorf("the provider reported no usage, and nothing is not zero: %+v", res.Usage)
	}
	e := EntryFor(res, u, time.Unix(0, 0).UTC())
	if e.TokensIn != nil || e.TokensOut != nil {
		t.Errorf("an unreported counter is absent from the row, never a zero: %+v", e)
	}
	if e.Calls != 1 {
		t.Errorf("the call itself is still on the record: %+v", e)
	}

	zero := &recorder{conf: 0.95, usage: Usage{HasInput: true, HasOutput: true}}
	res, err = RouteJev(context.Background(), zero, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Usage.HasInput || !res.Usage.HasOutput {
		t.Fatalf("an explicitly reported zero is a measurement: %+v", res.Usage)
	}
	e = EntryFor(res, u, time.Unix(0, 0).UTC())
	if e.TokensIn == nil || *e.TokensIn != 0 || e.TokensOut == nil || *e.TokensOut != 0 {
		t.Errorf("a reported zero must survive as a zero: %+v", e)
	}
}

// twoRungRegistry is Stella's witness: two rungs and nothing above them, so a
// choice of the top rung below the floor has nowhere to step up to.
func twoRungRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"low","lineage":"one","height":0,"availability":"available","ask":"card"},
	  {"name":"high","lineage":"two","height":1,"availability":"available","ask":"bus"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// threeRungRegistry is the same witness with one rung in the middle, so a
// mechanical kind with a confirmed failure still has an offer above the burned
// rung and the step-up refusal can be reached.
func threeRungRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"low","lineage":"one","height":0,"availability":"available","ask":"card"},
	  {"name":"mid","lineage":"two","height":1,"availability":"available","ask":"card"},
	  {"name":"high","lineage":"three","height":2,"availability":"available","ask":"bus"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// (2) A routing refusal does not discard a completed call. The call was made,
// the tokens were spent, and the refusal that follows cannot unspend them: the
// result carries the usage and the refusal, so the caller can persist both
// before it exits.
func TestUsageSurvivesARoutingRefusal(t *testing.T) {
	t.Parallel()

	reg := threeRungRegistry(t)
	u := Unit{ID: "u-ref", Kind: KindRebase, Files: 2, Packages: 1, Attempts: []Attempt{
		{Rung: "low", Outcome: OutcomeFailed, Reason: "the card rung missed it"},
	}}
	// The provider picks the top rung with a confidence below the floor, so the
	// step up has nowhere to go.
	rec := &recorder{conf: 0.40, pick: 1, usage: Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}}
	res, err := RouteJev(context.Background(), rec, reg, u, DefaultFloor)
	if err == nil {
		t.Fatalf("there is no rung above the top one: want a refusal, got %s", res.Rung.Name)
	}
	if res.Usage.Calls != 1 || res.Usage.InputTokens != 937 || res.Usage.OutputTokens != 12 {
		t.Errorf("the refusal discarded the call that was already made: %+v", res.Usage)
	}
	if res.Unit != u.ID {
		t.Errorf("the refused result must still name its unit, so the row can be written: %+v", res)
	}
	if strings.TrimSpace(res.Refusal) == "" {
		t.Error("the refused result must carry the refusal, so the row says why")
	}
	e := EntryFor(res, u, time.Unix(0, 0).UTC())
	if e.Refusal == "" || e.TokensIn == nil || *e.TokensIn != 937 {
		t.Errorf("the log row must carry the spend and the reason it was refused: %+v", e)
	}
}

// A refusal the rules reach on their own carries no call, and says so.
func TestARulesRefusalCarriesNoCall(t *testing.T) {
	t.Parallel()

	reg := twoRungRegistry(t)
	u := Unit{ID: "u-top", Kind: KindRebase, Files: 2, Packages: 1, Attempts: []Attempt{
		{Rung: "low", Outcome: OutcomeFailed, Reason: "r"},
		{Rung: "high", Outcome: OutcomeFailed, Reason: "r"},
	}}
	res, err := RouteRules(reg, u, DefaultFloor)
	if err == nil {
		t.Fatalf("every rung has been tried: want a refusal, got %s", res.Rung.Name)
	}
	if res.Usage.Calls != 0 {
		t.Errorf("the rules alone spend nothing: %+v", res.Usage)
	}
	if res.Unit != u.ID || strings.TrimSpace(res.Refusal) == "" {
		t.Errorf("a refused result still names its unit and its refusal: %+v", res)
	}
}
