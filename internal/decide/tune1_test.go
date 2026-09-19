package decide

import (
	"context"
	"strings"
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

// The evidence flags reach the PROVIDER, not just the rules: the size the
// caller gave is in the state the question is asked over, and a sized unit
// answers the rung its size supports at an ordinary confidence.
//
// The hurt this pins: on 2026-09-18 a manager's fix-with-red-test units came
// back astra at 0.69-0.74 and the report was that --files/--packages are
// accepted only with --no-jev. They are not dropped -- buildUnit runs before
// the verb consults jev at all. What happened is upstream of the provider: a
// unit with no size is THIN, thin scores confThin, confThin is below every
// usable floor, so the rules step the start rung up BEFORE the offer set is
// built -- and the rung the size would have supported is then not among the
// options at all, so no provider answer can recover it. Both halves are
// asserted here.
func TestTheSizeEvidenceReachesTheProviderAndThinEvidenceSaysSo(t *testing.T) {
	reg := testRegistry(t)
	sized := Unit{ID: "ev-with", Kind: KindFixWithRedTest, Files: 2, Packages: 1}
	fake := &fakeDecider{choice: "rung-1", conf: 0.80}
	res, err := RouteJev(context.Background(), fake, reg, sized, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rung.Name != "opus" {
		t.Errorf("a 2-file fix-with-red-test answers the child rung, got %s (%s)", res.Rung.Name, res.Reason)
	}
	if res.Source != SourceJev || res.SteppedUp {
		t.Errorf("an ordinary confidence over sized evidence stands as the provider gave it: source %s stepped %v", res.Source, res.SteppedUp)
	}
	// The state the provider was asked over carries the size, in buckets.
	for _, want := range []string{"files: 1-3", "packages: 1-3", "kind: fix-with-red-test"} {
		if !strings.Contains(fake.state, want) {
			t.Errorf("the state the provider saw is missing %q:\n%s", want, fake.state)
		}
	}
	// And the same unit with no size at all: thin, stepped up before the offer
	// set is built, and the line SAYS it is thin rather than leaving a caller
	// who forgot --files hunting for a floor problem.
	thin := &fakeDecider{choice: "rung-1", conf: 0.74}
	bare, err := RouteJev(context.Background(), thin, reg, Unit{ID: "ev-without", Kind: KindFixWithRedTest}, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if bare.Rung.Height <= res.Rung.Height {
		t.Errorf("thin evidence cannot support the rung a sized unit gets: %s vs %s", bare.Rung.Name, res.Rung.Name)
	}
	if !strings.Contains(bare.Reason, "no size evidence") {
		t.Errorf("the line says the unit is thin: %s", bare.Reason)
	}
	// The rung the size would have supported is genuinely gone from the offer
	// set -- which is why naming the cause on the line matters.
	for _, name := range bare.Offered {
		if name == "opus" {
			t.Error("thin evidence stepped up, so the child rung is not among the options; the reason line is the only way a caller learns why")
		}
	}
}

// A dogfood transcript diff starts at the bottom rung, and naming it does NOT
// weaken the security rule: `guard` still resolves to the designated mind on
// every path. The kind exists because the work was being named `guard` and
// priced as one -- 21 Flash cards over dogfood transcripts found four real
// drifts for about 20 cents on 2026-09-18.
func TestDogfoodIsItsOwnKindAndGuardIsUntouched(t *testing.T) {
	reg := testRegistry(t)
	res := mustRoute(t, reg, Unit{ID: "dogfood-transcript-1", Kind: KindDogfood, Files: 1, Packages: 1}, DefaultFloor)
	if res.Rung.Name != "flash" {
		t.Errorf("a dogfood transcript diff starts at the bottom rung, got %s (%s)", res.Rung.Name, res.Reason)
	}
	if !Mechanical(KindDogfood) || !KnownKind(KindDogfood) {
		t.Error("dogfood is a known, mechanical kind: a card rung may take it")
	}
	// The same work named guard is still security, and security never falls
	// through: designated, at confidence 1, with no provider call to make.
	guarded := mustRoute(t, reg, Unit{ID: "dogfood-as-guard", Kind: KindGuard, Files: 1, Packages: 1}, DefaultFloor)
	if !guarded.Designated || guarded.Rung.Name != "johnny" {
		t.Errorf("guard is a security kind and resolves to its designated mind, got %s (designated %v)", guarded.Rung.Name, guarded.Designated)
	}
	// And a dogfood unit that DOES touch something security-shaped is security
	// again, by touch and not by name: the new kind is not a way around it.
	touched := mustRoute(t, reg, Unit{ID: "dogfood-touches", Kind: KindDogfood, Files: 1, Touches: []string{TouchSecrets}}, DefaultFloor)
	if !touched.Designated || touched.Rung.Name != "johnny" {
		t.Errorf("a dogfood unit that touches secrets is still security, got %s (designated %v)", touched.Rung.Name, touched.Designated)
	}
}
