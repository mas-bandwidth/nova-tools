package decide

import (
	"context"
	"math"
	"regexp"
	"strings"
	"testing"
)

// Stella's four findings on #1327 at 78620ba3, one red test each.

// (1) The state Jev sees is TYPED and ENUMERATED: a kind, size buckets, the
// lane, an attempt count, platform and security flags, a deadline bucket --
// and nothing else. No unit id, no path name, no attempt reason, no free text
// of any kind leaves this process (SPEC-DECIDE rule 4).
func TestJevSeesOnlyTypedEnumeratedEvidence(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{
		ID:        "card-41-glenn-private-rowan-new",
		Kind:      KindNewVerb,
		Files:     7,
		Packages:  2,
		Lanes:     1,
		LaneOwner: "/Users/glenn/rowan-working/secret-lane",
		Platform:  "windows-11-box-in-the-office",
		Deadline:  "45m",
		Attempts: []Attempt{
			{Rung: "opus", Outcome: OutcomeFailed, Reason: "the token in ~/.config/deepseek/env was empty"},
		},
	}
	fake := &fakeDecider{choice: "sol", conf: 0.95}
	if _, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("the provider was called %d times, want 1", fake.calls)
	}
	for _, leak := range []string{
		"card-41", "glenn", "rowan-working", "secret-lane", "/Users",
		"token", ".config", "deepseek/env", "windows-11", "office", "45m",
	} {
		if strings.Contains(strings.ToLower(fake.state), strings.ToLower(leak)) {
			t.Errorf("the state sent to the provider carries %q:\n%s", leak, fake.state)
		}
	}
	shape := regexp.MustCompile(`^[a-z_]+: [a-z0-9+-]+$`)
	allowed := map[string]map[string]bool{
		"kind":     setOf(Kinds...),
		"files":    setOf(SizeBuckets...),
		"packages": setOf(SizeBuckets...),
		"lanes":    setOf(SizeBuckets...),
		"lane":     setOf("none", "other", "rowan-children", "stella-children", "code", "opencode", "security", "coordination", "writing"),
		"attempts": setOf(AttemptBuckets...),
		"platform": setOf(PlatformBuckets...),
		"security": setOf("yes", "no"),
		"deadline": setOf(DeadlineBuckets...),
	}
	lines := strings.Split(strings.TrimSpace(fake.state), "\n")
	if len(lines) != len(allowed) {
		t.Fatalf("the state has %d lines, want one per enumerated field (%d):\n%s", len(lines), len(allowed), fake.state)
	}
	for _, line := range lines {
		if !shape.MatchString(line) {
			t.Errorf("the line %q is not one enumerated key: value pair", line)
			continue
		}
		key, value, _ := strings.Cut(line, ": ")
		values, ok := allowed[key]
		if !ok {
			t.Errorf("the state carries the field %q, which is not one of the enumerated ones", key)
			continue
		}
		if !values[value] {
			t.Errorf("the field %s carries %q, which is not one of its enumerated values", key, value)
		}
	}
}

// setOf is a small set helper for the enumerations above.
func setOf(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

// (2) Security never falls through. A unit that touches a guard, secrets, the
// sandbox, sudo, a deploy key or the network resolves to the designated rung on
// EVERY path: with Jev on and off, at any floor, and with prior attempts --
// including prior attempts by the designated rung itself.
func TestSecurityNeverFallsThrough(t *testing.T) {
	reg := testRegistry(t)
	touches := []Unit{
		{Guard: true},
		{Secrets: true},
		{Kind: KindGuard},
		{Touches: []string{TouchSandbox}},
		{Touches: []string{TouchSudo}},
		{Touches: []string{TouchDeployKeys}},
		{Touches: []string{TouchNetwork}},
		{Touches: []string{TouchGuard, TouchSecrets, TouchNetwork}},
	}
	attempts := map[string][]Attempt{
		"none":          nil,
		"johnny failed": {{Rung: "johnny", Outcome: OutcomeFailed, Reason: "r"}},
		"johnny timed out": {
			{Rung: "johnny", Outcome: OutcomeTimeout},
		},
		"the top failed": {
			{Rung: "fable", Outcome: OutcomeFailed, Reason: "r"},
			{Rung: "astra", Outcome: OutcomeFailed, Reason: "r"},
			{Rung: "all-friends", Outcome: OutcomeFailed, Reason: "r"},
		},
		"two lineages failed": {
			{Rung: "opus", Outcome: OutcomeFailed, Reason: "r"},
			{Rung: "sol", Outcome: OutcomeFailed, Reason: "r"},
		},
	}
	floors := []float64{0, 0.5, DefaultFloor, 1}
	for i, base := range touches {
		for aname, atts := range attempts {
			for _, floor := range floors {
				u := base
				u.ID = "sec"
				if u.Kind == "" {
					u.Kind = KindFleetChore
				}
				u.Files, u.Packages = 1, 1
				u.Attempts = atts

				rules, err := RouteRules(reg, u, floor)
				if err != nil {
					t.Fatalf("touch %d, attempts %s, floor %g: %v", i, aname, floor, err)
				}
				if rules.Rung.Name != "johnny" {
					t.Errorf("rules: touch %d, attempts %s, floor %g: got %s, want johnny (%s)", i, aname, floor, rules.Rung.Name, rules.Reason)
				}
				if rules.SteppedUp {
					t.Errorf("rules: touch %d, attempts %s, floor %g: the floor moved a security designation", i, aname, floor)
				}
				fake := &fakeDecider{choice: "flash", conf: 0.99}
				jev, err := RouteJev(context.Background(), fake, reg, u, floor)
				if err != nil {
					t.Fatalf("jev: touch %d, attempts %s, floor %g: %v", i, aname, floor, err)
				}
				if jev.Rung.Name != "johnny" {
					t.Errorf("jev: touch %d, attempts %s, floor %g: got %s (%s)", i, aname, floor, jev.Rung.Name, jev.Reason)
				}
				if fake.calls != 0 {
					t.Errorf("jev: touch %d, attempts %s, floor %g: the provider was asked about security work", i, aname, floor)
				}
			}
		}
	}
}

// Security waits for its rung; it never spills onto another one. Where the
// designated mind is asleep, or the registry names none, that is a refusal.
func TestSecurityWaitsRatherThanSpills(t *testing.T) {
	asleep, err := ParseRegistry([]byte(`{"minds":[
	  {"name":"cheap","lineage":"deepseek","height":0,"availability":"available","ask":"card"},
	  {"name":"guardian","lineage":"johnny","height":3,"kinds":["guard","fresh-take"],"availability":"asleep","ask":"bus"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := RouteRules(asleep, Unit{ID: "s", Kind: KindFleetChore, Files: 1, Guard: true}, DefaultFloor); err == nil {
		t.Errorf("the designated rung is asleep: security waits for it, it does not go to %s", res.Rung.Name)
	}
	none, err := ParseRegistry([]byte(`{"minds":[{"name":"cheap","lineage":"deepseek","height":0,"availability":"available","ask":"card"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := RouteRules(none, Unit{ID: "s", Kind: KindGuard, Files: 1}, DefaultFloor); err == nil {
		t.Errorf("no rung is designated for security: that is a refusal, not %s", res.Rung.Name)
	}
}

// An unknown touch is a refusal: the six are an enumeration, not free text.
func TestTouchesAreEnumerated(t *testing.T) {
	reg := testRegistry(t)
	if _, err := RouteRules(reg, Unit{ID: "s", Kind: KindRebase, Files: 1, Touches: []string{"the vibes"}}, DefaultFloor); err == nil {
		t.Error("an unknown touch routed, want a refusal naming the six")
	}
}

// (3) A timeout is not a death. An attempt that timed out with no termination
// proof leaves expiry UNKNOWN (Stella's lease rule): the answer is the SAME
// rung until the attempt is known dead. Only a CONFIRMED failure moves up.
func TestATimeoutDoesNotAdvanceTheRung(t *testing.T) {
	reg := testRegistry(t)
	timedOut := Unit{ID: "t", Kind: KindFixWithRedTest, Files: 3, Packages: 1, Attempts: []Attempt{
		{Rung: "opus", Outcome: OutcomeTimeout},
	}}
	res := mustRoute(t, reg, timedOut, DefaultFloor)
	if res.Rung.Name != "opus" {
		t.Errorf("the attempt on opus is not known dead: the answer is the same rung, got %s (%s)", res.Rung.Name, res.Reason)
	}
	if !res.AwaitingTermination {
		t.Error("a timeout with no termination proof is an await, and the result must say so")
	}
	if res.SteppedUp {
		t.Error("the floor must not move a rung that is still occupied")
	}
	for _, phrase := range []string{"termination", "unknown"} {
		if !strings.Contains(strings.ToLower(res.Reason), phrase) {
			t.Errorf("the reason must name the rule (%q): %q", phrase, res.Reason)
		}
	}

	// Even at a floor of 1, the answer stays put rather than climbing.
	if high := mustRoute(t, reg, timedOut, 1); high.Rung.Name != "opus" || high.SteppedUp {
		t.Errorf("at floor 1 the answer is still opus, got %s stepped=%v", high.Rung.Name, high.SteppedUp)
	}

	// Termination proof turns the timeout into a confirmed failure, and THEN
	// the ladder moves: sideways first.
	confirmed := timedOut
	confirmed.Attempts = []Attempt{{Rung: "opus", Outcome: OutcomeTimeout, Terminated: true, Reason: "killed at 10m"}}
	res = mustRoute(t, reg, confirmed, DefaultFloor)
	if res.Rung.Name != "sol" {
		t.Errorf("a confirmed timeout is a failure: sideways to sol, got %s (%s)", res.Rung.Name, res.Reason)
	}
	if res.AwaitingTermination {
		t.Error("a terminated attempt is not an await")
	}

	// A confirmed failure elsewhere does not cancel an open timeout.
	mixed := timedOut
	mixed.Attempts = []Attempt{
		{Rung: "sol", Outcome: OutcomeFailed, Reason: "red"},
		{Rung: "opus", Outcome: OutcomeTimeout},
	}
	res = mustRoute(t, reg, mixed, DefaultFloor)
	if res.Rung.Name != "opus" || !res.AwaitingTermination {
		t.Errorf("an open timeout holds the answer to its own rung, got %s awaiting=%v (%s)", res.Rung.Name, res.AwaitingTermination, res.Reason)
	}
}

// There is nothing for the provider to choose while an attempt is still alive.
func TestJevIsNotAskedWhileAnAttemptMayStillBeAlive(t *testing.T) {
	reg := testRegistry(t)
	fake := &fakeDecider{choice: "fable", conf: 0.99}
	res, err := RouteJev(context.Background(), fake, reg, Unit{ID: "t", Kind: KindNewVerb, Files: 3, Packages: 1,
		Attempts: []Attempt{{Rung: "opus", Outcome: OutcomeTimeout}}}, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Errorf("the provider was asked %d times about a rung that is still occupied", fake.calls)
	}
	if res.Rung.Name != "opus" || !res.AwaitingTermination {
		t.Errorf("got %s awaiting=%v, want opus awaiting (%s)", res.Rung.Name, res.AwaitingTermination, res.Reason)
	}
}

// A timeout that is not known dead is not counted as a failure in the log
// either: the starting rung is regenerated from confirmed outcomes only.
func TestTheLogCountsConfirmedFailuresOnly(t *testing.T) {
	reg := testRegistry(t)
	sum, err := Summarize(reg, []Entry{
		{Unit: "a", Kind: KindRebase, RungTried: "flash", Height: 0, Outcome: OutcomeOK, RungSucceeded: "flash",
			Evidence: Unit{ID: "a", Kind: KindRebase, Attempts: []Attempt{{Rung: "flash", Outcome: OutcomeTimeout}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Kinds[0].Failures != 0 {
		t.Errorf("an open timeout is not a failure: failures = %d", sum.Kinds[0].Failures)
	}
}

// (4) A floor that is not a number is a refusal with a remedy, not a silent
// pass: NaN compares false against every bound, which is exactly how it slipped
// through.
func TestFloorRefusesNaNAndBothBounds(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "f", Kind: KindRebase, Files: 1, Packages: 1}
	for name, floor := range map[string]float64{
		"NaN":       math.NaN(),
		"+Inf":      math.Inf(1),
		"-Inf":      math.Inf(-1),
		"negative":  -0.1,
		"over one":  1.1,
		"large NaN": math.Float64frombits(0x7FF8000000000001),
	} {
		_, err := RouteRules(reg, u, floor)
		if err == nil {
			t.Errorf("%s: a floor of %v routed, want a refusal", name, floor)
			continue
		}
		if !strings.Contains(err.Error(), "0.9") {
			t.Errorf("%s: the refusal must carry one remedy naming a floor that works: %v", name, err)
		}
	}
	if _, err := RouteRules(reg, u, 0); err != nil {
		t.Errorf("a floor of 0 is a floor: %v", err)
	}
	if _, err := RouteRules(reg, u, 1); err != nil {
		t.Errorf("a floor of 1 is a floor: %v", err)
	}
	if _, err := Help(HelpState{Hours: math.NaN()}); err == nil {
		t.Error("a help state of NaN hours answered, want a refusal")
	}
	if _, err := Help(HelpState{Uncertainty: math.NaN()}); err == nil {
		t.Error("a help state of NaN uncertainty answered, want a refusal")
	}
}
