package decide

import (
	"context"
	"strings"
	"testing"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func mustRoute(t *testing.T, reg *Registry, u Unit, floor float64) RouteResult {
	t.Helper()
	res, err := RouteRules(reg, u, floor)
	if err != nil {
		t.Fatalf("route %s: %v", u.ID, err)
	}
	return res
}

// The lowest rung the evidence supports: a mechanical unit starts on Flash, and
// friends first means a unit that is not mechanical never lands on DeepSeek.
func TestMechanicalStartsLowAndFriendsFirstOtherwise(t *testing.T) {
	reg := testRegistry(t)
	mech := mustRoute(t, reg, Unit{ID: "u1", Kind: KindRebase, Files: 2, Packages: 1, Lanes: 1}, DefaultFloor)
	if mech.Rung.Name != "flash" {
		t.Errorf("a one-package rebase is Flash's, got %s (%s)", mech.Rung.Name, mech.Reason)
	}
	if mech.Rung.Ask != AskCard {
		t.Errorf("a DeepSeek rung is asked by card, got %q", mech.Rung.Ask)
	}
	for _, kind := range []string{KindFixWithRedTest, KindNewVerb, KindSpec, KindDesign, KindCauseToFind} {
		res := mustRoute(t, reg, Unit{ID: "u-" + kind, Kind: kind, Files: 2, Packages: 1, Lanes: 1}, DefaultFloor)
		if res.Rung.Lineage == "deepseek" {
			t.Errorf("friends first: %s landed on the DeepSeek rung %s", kind, res.Rung.Name)
		}
	}
}

// Friends first, wherever the DeepSeek rung sits: a mind of that lineage is not
// eligible for a kind that is not mechanical, even at a height the unit has
// climbed to.
func TestDeepSeekIsSkippedForKindsThatAreNotMechanical(t *testing.T) {
	reg, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"cheap","lineage":"deepseek","height":0,"availability":"available","ask":"card"},
	  {"name":"bulk","lineage":"deepseek","height":2,"availability":"available","ask":"card"},
	  {"name":"friend","lineage":"emma","height":2,"availability":"available","ask":"bus"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	mech := mustRoute(t, reg, Unit{ID: "m", Kind: KindRebase, Files: 2, Packages: 1}, DefaultFloor)
	if mech.Rung.Name != "cheap" {
		t.Errorf("a mechanical kind is the DeepSeek rung's, got %s", mech.Rung.Name)
	}
	res := mustRoute(t, reg, Unit{ID: "n", Kind: KindNewVerb, Files: 2, Packages: 1}, DefaultFloor)
	if res.Rung.Name != "friend" {
		t.Errorf("friends first: a new-verb landed on %s (%s)", res.Rung.Name, res.Reason)
	}
}

// Security is a kind, not a height: a guard, secrets, the sandbox, sudo, deploy
// keys or the network reaches Johnny always, even where the evidence is one
// file.
//
// This test used to assert rung=johnny, and that assertion is what the routing
// of 2026-09-18 proved wrong: Johnny is RESERVED -- a read and the STOP a read
// can call, never the work -- and answering rung=johnny sent seven of the day's
// twenty units to a mind that does not take work. He reaches every one of them
// still, as the READER, and the work goes to the rung the evidence supports.
func TestSecurityAlwaysReachesJohnnyAsTheReader(t *testing.T) {
	reg := testRegistry(t)
	for _, u := range []Unit{
		{ID: "g1", Kind: KindGuard, Files: 1, Packages: 1},
		{ID: "g2", Kind: KindRebase, Files: 1, Packages: 1, Guard: true},
		{ID: "g3", Kind: KindRebase, Files: 1, Packages: 1, Secrets: true},
	} {
		res := mustRoute(t, reg, u, DefaultFloor)
		if res.ReadField() != "johnny" {
			t.Errorf("%s: security reaches Johnny always, got read=%s (%s)", u.ID, res.ReadField(), res.Reason)
		}
		if res.Rung.Name == "johnny" {
			t.Errorf("%s: a reserved mind takes no work (%s)", u.ID, res.Reason)
		}
		if !res.Rung.Usable() {
			t.Errorf("%s: the work went to %s, which is not on the ladder", u.ID, res.Rung.Name)
		}
	}
}

// A fresh take reaches Johnny too -- the rungs below failed, or a design with
// one author -- and by the same rule it reaches him as a READ, because the mind
// designated for it is the reserved one.
func TestFreshTakeReachesJohnnyAsTheReader(t *testing.T) {
	reg := testRegistry(t)
	res := mustRoute(t, reg, Unit{ID: "f1", Kind: KindDesign, Files: 3, Packages: 1, FreshTake: true}, DefaultFloor)
	if res.ReadField() != "johnny" {
		t.Errorf("a design with one author reaches Johnny, got read=%s (%s)", res.ReadField(), res.Reason)
	}
	if res.Rung.Name == "johnny" {
		t.Errorf("a reserved mind takes no work (%s)", res.Reason)
	}
	twoLineages := Unit{ID: "f2", Kind: KindNewVerb, Files: 3, Packages: 1, Attempts: []Attempt{
		{Rung: "opus", Outcome: OutcomeFailed, Reason: "the test stayed red"},
		{Rung: "sol", Outcome: OutcomeFailed, Reason: "the same red"},
	}}
	res = mustRoute(t, reg, twoLineages, DefaultFloor)
	if res.ReadField() != "johnny" {
		t.Errorf("two lineages failed: that is a fresh take, got read=%s (%s)", res.ReadField(), res.Reason)
	}
	if res.Rung.Name == "johnny" {
		t.Errorf("two lineages failed, but the work still goes to a mind that takes work (%s)", res.Reason)
	}
}

// A failed attempt re-enters the decision with its evidence and the answer is
// the next rung: sideways first (same height, other lineage), then up.
func TestSidewaysBeforeUp(t *testing.T) {
	reg := testRegistry(t)
	after := Unit{ID: "s1", Kind: KindFixWithRedTest, Files: 3, Packages: 1, Attempts: []Attempt{
		{Rung: "opus", Outcome: OutcomeFailed, Reason: "missed the cause"},
	}}
	res := mustRoute(t, reg, after, DefaultFloor)
	if res.Rung.Name != "sol" {
		t.Errorf("opus failed: sideways to sol before up, got %s (%s)", res.Rung.Name, res.Reason)
	}
	opus, _ := reg.ByName("opus")
	if res.Rung.Height != opus.Height {
		t.Errorf("sideways keeps the height: %d vs %d", res.Rung.Height, opus.Height)
	}
	mech := Unit{ID: "s2", Kind: KindStack, Files: 2, Packages: 1, Attempts: []Attempt{
		{Rung: "flash", Outcome: OutcomeFailed, Reason: "rebase conflict"},
	}}
	res = mustRoute(t, reg, mech, DefaultFloor)
	if res.Rung.Name != "pro" {
		t.Errorf("flash failed and no sideways rung exists: up to pro, got %s (%s)", res.Rung.Name, res.Reason)
	}
	if !res.Escalated {
		t.Error("a decision that carries a prior attempt is an escalation")
	}
}

// The ladder never answers a rung below the one the evidence already burned.
func TestTheLadderNeverStepsDown(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "d1", Kind: KindRebase, Files: 1, Packages: 1, Attempts: []Attempt{
		{Rung: "pro", Outcome: OutcomeFailed, Reason: "conflicted again"},
	}}
	res := mustRoute(t, reg, u, DefaultFloor)
	pro, _ := reg.ByName("pro")
	if res.Rung.Height <= pro.Height {
		t.Errorf("pro failed: the answer is above it, got %s at %d", res.Rung.Name, res.Rung.Height)
	}
}

// Below the floor the answer steps UP a rung, never down.
func TestBelowTheFloorStepsUp(t *testing.T) {
	reg := testRegistry(t)
	thin := Unit{ID: "t1", Kind: KindNewVerb}
	below := mustRoute(t, reg, thin, DefaultFloor)
	if below.Confidence >= DefaultFloor {
		t.Fatalf("evidence this thin cannot be a confident first attempt: %.2f", below.Confidence)
	}
	if !below.SteppedUp {
		t.Fatalf("below the floor the answer steps up: %s (%s)", below.Rung.Name, below.Reason)
	}
	sized := mustRoute(t, reg, Unit{ID: "t2", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}, DefaultFloor)
	if below.Rung.Height <= sized.Rung.Height {
		t.Errorf("the step is UP: %s at %d vs %s at %d", below.Rung.Name, below.Rung.Height, sized.Rung.Name, sized.Rung.Height)
	}
	if !strings.Contains(below.Reason, "floor") {
		t.Errorf("the reason says the floor moved it: %q", below.Reason)
	}
}

// The top is Fable/Astra, then all friends at once, then Glenn.
func TestTheTopIsAllFriendsThenGlenn(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "top", Kind: KindSpec, Files: 4, Packages: 2, Attempts: []Attempt{
		{Rung: "fable", Outcome: OutcomeFailed, Reason: "the shape is still wrong"},
		{Rung: "astra", Outcome: OutcomeFailed, Reason: "and still wrong"},
	}}
	res := mustRoute(t, reg, u, DefaultFloor)
	if res.Rung.Name != "all-friends" {
		t.Fatalf("above the top pair is all friends at once, got %s (%s)", res.Rung.Name, res.Reason)
	}
	u.Attempts = append(u.Attempts, Attempt{Rung: "all-friends", Outcome: OutcomeFailed, Reason: "no answer carried"})
	res = mustRoute(t, reg, u, DefaultFloor)
	if res.Rung.Name != "glenn" {
		t.Fatalf("above all friends is Glenn, got %s (%s)", res.Rung.Name, res.Reason)
	}
	u.Attempts = append(u.Attempts, Attempt{Rung: "glenn", Outcome: OutcomeFailed, Reason: "asleep"})
	if _, err := RouteRules(reg, u, DefaultFloor); err == nil {
		t.Error("there is no rung above Glenn: that is a refusal, not a guess")
	}
}

// The lane's owner wins among rungs of the same height.
func TestLaneOwnerWinsAtEqualHeight(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "l1", Kind: KindNewVerb, Files: 12, Packages: 4, Lanes: 2, LaneOwner: "opencode"}
	res := mustRoute(t, reg, u, DefaultFloor)
	if res.Rung.Name != "freddy" {
		t.Errorf("the opencode lane is freddy's, got %s (%s)", res.Rung.Name, res.Reason)
	}
}

// An unknown kind, a unit with no id, and an attempt naming no rung are each a
// refusal: the evidence pointer is the unit's id (rule 10).
func TestRouteRefusesThinContract(t *testing.T) {
	reg := testRegistry(t)
	for name, u := range map[string]Unit{
		"unknown kind":  {ID: "x", Kind: "vibes"},
		"no id":         {Kind: KindRebase},
		"unknown rung":  {ID: "x", Kind: KindRebase, Attempts: []Attempt{{Rung: "gandalf", Outcome: OutcomeFailed}}},
		"bad outcome":   {ID: "x", Kind: KindRebase, Attempts: []Attempt{{Rung: "flash", Outcome: "meh"}}},
		"bad deadline":  {ID: "x", Kind: KindRebase, Deadline: "soonish"},
		"negative size": {ID: "x", Kind: KindRebase, Files: -1},
	} {
		if _, err := RouteRules(reg, u, DefaultFloor); err == nil {
			t.Errorf("%s: routed, want a refusal", name)
		}
	}
	if _, err := RouteRules(reg, Unit{ID: "x", Kind: KindRebase}, 1.5); err == nil {
		t.Error("a floor outside 0..1 is a refusal")
	}
}

// The one line carries the unit (the evidence pointer), the rung, the
// confidence, the floor, the reason and how the rung is asked.
func TestRouteLineShape(t *testing.T) {
	reg := testRegistry(t)
	res := mustRoute(t, reg, Unit{ID: "card 41", Kind: KindRebase, Files: 2, Packages: 1}, DefaultFloor)
	line := res.Line()
	if strings.Contains(line, "\n") {
		t.Fatalf("exactly one line: %q", line)
	}
	for _, want := range []string{"ROUTE ", "unit=card\\x2041", "rung=flash", "confidence=0.9", "floor=0.90", "reason=\"", "ask=card"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is missing %q: %s", want, line)
		}
	}
}

// fakeDecider answers one choice with one confidence and never dials anything.
type fakeDecider struct {
	choice  string
	conf    float64
	err     error
	options []string
	calls   int
	state   string
}

func (f *fakeDecider) Decide(_ context.Context, state string, qs map[string]Question) (map[string]Answer, Usage, error) {
	f.calls++
	f.state = state
	if f.err != nil {
		return nil, Usage{}, f.err
	}
	q, ok := qs[RungQuestion]
	if !ok {
		return nil, Usage{}, errNoRungQuestion
	}
	f.options = f.options[:0]
	for name := range q.Choice {
		f.options = append(f.options, name)
	}
	if strings.TrimSpace(state) == "" {
		return nil, Usage{}, errNoRungQuestion
	}
	return map[string]Answer{RungQuestion: {Type: "choice", Choice: f.choice, Confidence: f.conf}}, Usage{}, nil
}

// Jev chooses among the eligible rungs, and its choice is clamped: never below
// the rung the rules already support.
func TestJevChoiceNeverStepsBelowTheRules(t *testing.T) {
	reg := testRegistry(t)
	fake := &fakeDecider{choice: "flash", conf: 0.99}
	u := Unit{ID: "j1", Kind: KindSpec, Files: 4, Packages: 2}
	res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rung.Lineage == "deepseek" {
		t.Errorf("a spec is not DeepSeek's, and the option was never offered: got %s", res.Rung.Name)
	}
	// The options are opaque ids and never a mind's own name (Stella's R1), so
	// what is asserted here is opacity: no registry name is ever an option.
	for _, opt := range fake.options {
		if _, ok := reg.ByName(opt); ok {
			t.Errorf("jev was offered the registry name %q; the options are opaque ids", opt)
		}
		if !strings.HasPrefix(opt, "rung-") {
			t.Errorf("the option %q is not an opaque id", opt)
		}
	}
	if res.RulesRung == "" {
		t.Error("every decision is logged beside what Rowan would have picked")
	}
}

// A Jev answer below the floor steps up, and the provider's confidence is the
// number the floor is applied to (rule 5).
func TestJevBelowFloorStepsUp(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "j2", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}
	sure := &fakeDecider{choice: "rung-1", conf: 0.97} // rung-1 is opus, the lowest offered
	high, err := RouteJev(context.Background(), sure, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if high.Rung.Name != "opus" || high.SteppedUp {
		t.Fatalf("an above-floor choice stands: %s stepped=%v", high.Rung.Name, high.SteppedUp)
	}
	if high.Source != SourceJev {
		t.Errorf("source = %q, want %q", high.Source, SourceJev)
	}
	unsure := &fakeDecider{choice: "rung-1", conf: 0.40}
	low, err := RouteJev(context.Background(), unsure, reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if !low.SteppedUp || low.Rung.Height <= high.Rung.Height {
		t.Fatalf("below the floor the answer steps UP: %s at %d (%s)", low.Rung.Name, low.Rung.Height, low.Reason)
	}
	if low.Confidence != 0.40 {
		t.Errorf("the line carries the provider's number, got %.2f", low.Confidence)
	}
}

// A designation is machinery, not a judgment: no provider call is made for it.
func TestJevIsNotAskedForADesignation(t *testing.T) {
	reg := testRegistry(t)
	fake := &fakeDecider{choice: "flash", conf: 0.99}
	res, err := RouteJev(context.Background(), fake, reg, Unit{ID: "j3", Kind: KindRebase, Files: 1, Guard: true}, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Errorf("security is the machinery's word: %d provider calls made", fake.calls)
	}
	if res.ReadField() != "johnny" || res.Source != SourceRules {
		t.Errorf("got read=%s from %s, want johnny from the rules", res.ReadField(), res.Source)
	}
}

// A provider error, or an answer naming a rung nobody offered, falls back to
// the rules answer: a decision advises, the machinery decides.
func TestJevFallsBackToTheRules(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "j4", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}
	rules := mustRoute(t, reg, u, DefaultFloor)
	for name, fake := range map[string]*fakeDecider{
		"provider error": {err: errNoRungQuestion},
		"unknown rung":   {choice: "gandalf", conf: 0.99},
	} {
		res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Rung.Name != rules.Rung.Name {
			t.Errorf("%s: got %s, want the rules answer %s", name, res.Rung.Name, rules.Rung.Name)
		}
		if res.Source != SourceRules {
			t.Errorf("%s: source = %q, want %q", name, res.Source, SourceRules)
		}
	}
}
