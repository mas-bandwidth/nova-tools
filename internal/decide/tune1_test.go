package decide

import (
	"context"
	"testing"
	"time"
)

// A row test is card work, and the ladder says so by kind rather than by size.
// The schema campaign's row cards -- one test file, one package, on a leg whose
// card shape was already proven -- ran on pro and came back green; the same
// work named fixture-retarget answered flash, one rung under, and named
// fix-with-red-test it answered opus, two rungs over.
func TestARowTestStartsAtTheCardRung(t *testing.T) {
	reg := testRegistry(t)
	row := Unit{ID: "row-9-refuse-writes-nothing", Kind: KindRowTest, Files: 1, Packages: 1}
	res := mustRoute(t, reg, row, DefaultFloor)
	if res.Rung.Name != "pro" {
		t.Errorf("a row test is card work at the pro rung, got %s (%s)", res.Rung.Name, res.Reason)
	}
	h, ok := StartHeight(KindRowTest)
	if !ok || h != 1 {
		t.Fatalf("row-test starts at height 1, got %d (known %v)", h, ok)
	}
	if !Mechanical(KindRowTest) {
		t.Error("a row test is a mechanical kind: a card rung may take it")
	}
	if !KnownKind(KindRowTest) {
		t.Error("row-test is one of the kinds a unit may name")
	}
	// The two names it used to wear, over the identical evidence, are the two
	// wrong answers the kind exists to stop.
	under := mustRoute(t, reg, Unit{ID: "as-retarget", Kind: KindFixtureRetarget, Files: 1, Packages: 1}, DefaultFloor)
	over := mustRoute(t, reg, Unit{ID: "as-red-test", Kind: KindFixWithRedTest, Files: 1, Packages: 1}, DefaultFloor)
	if under.Rung.Height >= res.Rung.Height || over.Rung.Height <= res.Rung.Height {
		t.Errorf("the kind name moved the rung: retarget %s, row-test %s, red-test %s",
			under.Rung.Name, res.Rung.Name, over.Rung.Name)
	}
}

// The size term may raise a row test's rung and never lowers it: one file is
// the shape of a trivial rebase AND of a subtle codegen fix, and the count
// cannot tell them apart.
func TestSizeNeverDropsARowTestBelowTheCardRung(t *testing.T) {
	reg := testRegistry(t)
	start, _ := StartHeight(KindRowTest)
	for _, u := range []Unit{
		{ID: "no-size", Kind: KindRowTest},
		{ID: "one-file", Kind: KindRowTest, Files: 1, Packages: 1},
		{ID: "one-lane", Kind: KindRowTest, Files: 1, Packages: 1, Lanes: 1},
		{ID: "big", Kind: KindRowTest, Files: 24, Packages: 4, Lanes: 2},
	} {
		res := mustRoute(t, reg, u, DefaultFloor)
		if res.Rung.Height < start {
			t.Errorf("unit %s dropped a row test to %s (height %d), below the kind's start %d: %s",
				u.ID, res.Rung.Name, res.Rung.Height, start, res.Reason)
		}
	}
}

// A mechanical kind that CONFIRMED-failed on a card rung was not mechanical:
// the other card rung is the same mistake one height up, so no card rung is
// eligible again and the answer is the child rung. The per-height sideways rule
// cannot reach this, because flash and pro are one lineage at two heights.
func TestAFailedCardRungTakesTheWholeLineageOut(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "cert-windows-legs", Kind: KindFleetChore, Files: 2, Packages: 2, Attempts: []Attempt{
		{Rung: "flash", Outcome: OutcomeFailed, Reason: "the class test was never written"},
	}}
	res := mustRoute(t, reg, u, DefaultFloor)
	if res.Rung.Name != "opus" {
		t.Errorf("a card rung failed this unit: the answer is the child rung, got %s (%s)", res.Rung.Name, res.Reason)
	}
	if res.Rung.Lineage == LineageDeepSeek {
		t.Errorf("no card rung is eligible after a card rung failed, got %s", res.Rung.Name)
	}
	// The same unit with the same attempt on pro answers the same way: the rule
	// is about the lineage, not about which of the two rungs failed.
	u.ID, u.Attempts = "cert-windows-legs-pro", []Attempt{{Rung: "pro", Outcome: OutcomeFailed, Reason: "the class test was never written"}}
	if res := mustRoute(t, reg, u, DefaultFloor); res.Rung.Lineage == LineageDeepSeek {
		t.Errorf("pro failed: no card rung is eligible, got %s (%s)", res.Rung.Name, res.Reason)
	}
	// An attempt that is NOT a confirmed failure leaves the card rungs where
	// they were: a timeout with no proof it died is a silence, not a death.
	open := Unit{ID: "still-running", Kind: KindFleetChore, Files: 2, Packages: 2, Attempts: []Attempt{
		{Rung: "flash", Outcome: OutcomeTimeout},
	}}
	if res := mustRoute(t, reg, open, DefaultFloor); res.Rung.Name != "flash" || !res.AwaitingTermination() {
		t.Errorf("an open attempt holds its own rung and waits, got %s wait=%s", res.Rung.Name, res.Wait)
	}
	// And a kind that was never mechanical is unchanged by any of this.
	judged := Unit{ID: "judged", Kind: KindFixWithRedTest, Files: 3, Packages: 1}
	if res := mustRoute(t, reg, judged, DefaultFloor); res.Rung.Name != "opus" {
		t.Errorf("friends first is untouched: fix-with-red-test starts at opus, got %s", res.Rung.Name)
	}
}

// The floor is a number with rows behind it. 0.7 sits below the whole band the
// provider returned in the 2026-09-18 trial (0.61 to 0.91), so an ordinary
// confidence keeps the rung the evidence supports and only a real collapse
// steps up. The fake answers the numbers the provider actually returned.
func TestTheDefaultFloorSitsBelowTheMeasuredBand(t *testing.T) {
	if DefaultFloor > 0.67 {
		t.Fatalf("the default floor is %v; the measured band's floor is 0.68, on the rebase unit that straddled 0.7 and routed two ways", DefaultFloor)
	}
	reg := testRegistry(t)
	// The four row cards, at the confidences the log holds for them.
	for _, conf := range []float64{0.68, 0.76, 0.80, 0.89} {
		fake := &fakeDecider{choice: "rung-1", conf: conf}
		u := Unit{ID: "row-card", Kind: KindRowTest, Files: 1, Packages: 1}
		res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
		if err != nil {
			t.Fatalf("conf %v: %s", conf, err)
		}
		if res.Rung.Name != "pro" {
			t.Errorf("conf %v: an ordinary confidence keeps the card rung, got %s (%s)", conf, res.Rung.Name, res.Reason)
		}
		if res.SteppedUp {
			t.Errorf("conf %v: nothing collapsed, so nothing steps up", conf)
		}
	}
	// A real collapse still steps up, and still never down.
	fake := &fakeDecider{choice: "rung-1", conf: 0.4}
	res, err := RouteJev(context.Background(), fake, reg, Unit{ID: "collapsed", Kind: KindRowTest, Files: 1, Packages: 1}, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if !res.SteppedUp || res.Rung.Height <= 1 {
		t.Errorf("a collapsed confidence steps UP: got %s (stepped %v)", res.Rung.Name, res.SteppedUp)
	}
}

// Rule 8's other half: an outcome row joins a confidence to what followed, and
// the summary reads it without counting a second decision.
func TestAnOutcomeRowFeedsTheSummaryAndIsNotADecision(t *testing.T) {
	reg := testRegistry(t)
	at := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	decision := Entry{
		Time: at.Format(time.RFC3339), Unit: "row-card", Kind: KindRowTest,
		Evidence:  Unit{ID: "row-card", Kind: KindRowTest, Files: 1, Packages: 1},
		RungTried: "pro", Height: 1, Confidence: 0.89, Floor: DefaultFloor, Source: SourceJev,
		RowanPick: "pro", Wait: WaitNone,
	}
	before, err := Summarize(reg, []Entry{decision})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Kinds) != 1 || before.Kinds[0].Decisions != 1 || before.Kinds[0].Successes != 0 {
		t.Fatalf("a decision with no outcome: %+v", before.Kinds)
	}
	out := OutcomeEntry("row-card", KindRowTest, "pro", OutcomeOK, at.Add(time.Hour))
	if out.Source != SourceOutcome {
		t.Errorf("an outcome row is marked as one, got source %q", out.Source)
	}
	if out.RungSucceeded != "pro" {
		t.Errorf("a green outcome names the rung that succeeded, got %q", out.RungSucceeded)
	}
	after, err := Summarize(reg, []Entry{decision, out})
	if err != nil {
		t.Fatal(err)
	}
	row := after.Kinds[0]
	if row.Decisions != 1 {
		t.Errorf("an outcome is not a second decision: decisions = %d", row.Decisions)
	}
	if row.Successes != 1 {
		t.Errorf("the outcome is folded into the rung it names: successes = %d", row.Successes)
	}
	if row.StartRung != "pro" {
		t.Errorf("a rung that succeeded regenerates the starting rung, got %s", row.StartRung)
	}
	// A red outcome is a failure at the rung that ran, and no success.
	red := OutcomeEntry("row-card", KindRowTest, "pro", OutcomeFailed, at.Add(2*time.Hour))
	if red.RungSucceeded != "" {
		t.Errorf("a red outcome names no successful rung, got %q", red.RungSucceeded)
	}
	sum, err := Summarize(reg, []Entry{decision, red})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Kinds[0].Failures != 1 || sum.Kinds[0].Successes != 0 {
		t.Errorf("a red outcome is a failure at the rung that ran: %+v", sum.Kinds[0])
	}
}

// LastDecision reads the decision an outcome answers, and never an outcome row:
// the kind and the rung come from the decision, not from a caller's memory.
func TestLastDecisionSkipsOutcomeRows(t *testing.T) {
	at := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	first := Entry{Unit: "u", Kind: KindRowTest, RungTried: "pro", Source: SourceJev}
	second := Entry{Unit: "u", Kind: KindRowTest, RungTried: "opus", Source: SourceRules}
	out := OutcomeEntry("u", KindRowTest, "opus", OutcomeOK, at)
	got, ok := LastDecision([]Entry{first, second, out}, "u")
	if !ok || got.RungTried != "opus" {
		t.Errorf("the last DECISION row, got %+v (found %v)", got, ok)
	}
	if _, ok := LastDecision([]Entry{out}, "u"); ok {
		t.Error("an outcome row is not a decision an outcome can answer")
	}
	if _, ok := LastDecision([]Entry{first}, "other"); ok {
		t.Error("a unit the log does not hold is not found")
	}
}

// A mind that is asleep is not on the height ladder at all -- eligible() gates
// on Usable() -- so the answer steps sideways to a mind that is awake, and up
// only when no sideways rung is left. The mechanism was there; what was not
// there was a test over the HEIGHT ladder (hold_test covers only the security
// designation), and an asleep row in the registry to exercise it.
//
// The hurt: hygiene-delete-on-shape, a fix-with-red-test of 2 files and 1
// package, answered emma ask=bus on 2026-09-18 while Emma had been asleep on
// the bus since about 21:45Z. The ladder read the registry faithfully and the
// registry said she was available: a safety fix was handed to a sleeping
// friend, and a coordinator dispatched it to a child by hand instead. Keeping
// the field TRUE is the open half (nova-tools#1501); this is the half that says
// the ladder honours it.
func TestAnAsleepMindIsNotOnTheHeightLadder(t *testing.T) {
	asleep, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"flash","lineage":"deepseek","height":0,"kinds":[],"lanes":[],"availability":"available","ask":"card"},
	  {"name":"pro","lineage":"deepseek","height":1,"kinds":[],"lanes":[],"availability":"available","ask":"card"},
	  {"name":"opus","lineage":"rowan","height":2,"kinds":[],"lanes":["rowan-children"],"availability":"asleep","ask":"child"},
	  {"name":"sol","lineage":"stella","height":2,"kinds":[],"lanes":["stella-children"],"availability":"asleep","ask":"child"},
	  {"name":"emma","lineage":"emma","height":3,"kinds":[],"lanes":["code"],"availability":"asleep","ask":"bus"},
	  {"name":"freddy","lineage":"freddy","height":3,"kinds":[],"lanes":["opencode"],"availability":"available","ask":"bus"},
	  {"name":"astra","lineage":"stella","height":4,"kinds":[],"lanes":["coordination"],"availability":"available","ask":"bus"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	// The unit hygiene-delete-on-shape wore: the child rungs are its start, and
	// both of them plus emma are asleep, so the answer is freddy -- sideways at
	// emma's height to the lineage that is awake, not up to astra and not onto
	// any of the three that are not there.
	u := Unit{ID: "hygiene-delete-on-shape", Kind: KindFixWithRedTest, Files: 2, Packages: 1}
	res := mustRoute(t, asleep, u, DefaultFloor)
	if res.Rung.Name != "freddy" {
		t.Errorf("three minds asleep: the answer is the awake one at that height, got %s (%s)", res.Rung.Name, res.Reason)
	}
	if res.Rung.Availability != AvailabilityAvailable {
		t.Errorf("an asleep mind is never the answer, got %s at %s", res.Rung.Name, res.Rung.Availability)
	}
	// With freddy asleep as well there is no awake rung at that height, so it
	// steps UP to one that is awake rather than down onto a sleeper.
	for i := range asleep.Minds {
		if asleep.Minds[i].Name == "freddy" {
			asleep.Minds[i].Availability = AvailabilityAsleep
		}
	}
	if res := mustRoute(t, asleep, u, DefaultFloor); res.Rung.Name != "astra" {
		t.Errorf("no awake rung at that height: up to astra, got %s (%s)", res.Rung.Name, res.Reason)
	}
	// And an asleep mind is not offered to the provider either: the option set
	// is the eligible minds, so nothing the provider could name is asleep.
	offered := offer(asleep, u, 2, map[string]bool{}, map[int]map[string]bool{})
	for _, m := range offered {
		if m.Availability == AvailabilityAsleep {
			t.Errorf("an asleep mind was offered to the provider: %s", m.Name)
		}
	}
}
