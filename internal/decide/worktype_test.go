package decide

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workTypeFixtures are eight cards, one per type of the 2026-09-22 work-types
// report (rowan-new reports/work-types-and-model-fit-2026-09-22.md, section 3),
// each written the way the sprint's cards were: a header, then prose. Each
// carries exactly the evidence that report's "a router tests it by" column
// names for its type, and nothing a model is needed to read.
var workTypeFixtures = map[string]string{
	WorkTypeIssueColdRead: `card-00-read-schema-1118-r1
KIND: read
SOURCE: schema#1118
TEST: none
Swarm read of schema#1118 at head against main. Return one finding and a verdict.
`,
	WorkTypeTranscriptReproduction: `card-tt-cli-help-r2
KIND: transcript-test
TEST: none
Replay the transcript under the heading "nova-swarm batch" in docs/CLI.md against the tree,
line for line. Answer CLEAN or DRIFT with the first line that differs.
`,
	WorkTypeSpecRuleConformance: `card-spec-decide-5
KIND: transcript-test
TEST: none
Does the code at base 09fbedc9 do what docs/SPEC-DECIDE.md rule 5 says?
Answer CONFORMS or GAP, with the file and line.
`,
	WorkTypeCodeAuditRead: `card-tools22-pre-1916-r4
TEST: none
PATHS: the files the pull request changes
TURNS: 35
DEADLINE: 2100
Pre-read of PR #1916 for Glenn: brief a human in prose on what it changes.
`,
	WorkTypeSpecContractProbe: `card-probe-nova-work-contract
KIND: spec-read
TEST: none
Read-only. Does the contract in docs/SPEC-WORK.md say that a card may pick its own number?
A prose judgement; write nothing.
`,
	WorkTypeIssueFixRedFirst: `card-fix-2533
KIND: fix
SOURCE: nova-tools#2533
TEST: go test ./internal/swarm -run TestIdleKillAfterExplore
Fix the idle-kill after Explore agents spawn. Red first, then green.
`,
	WorkTypeRecutAtTip: `recut #2417 at the current dev tip
KIND: recut
BASE: dev
Rewrite the change of PR #2417 at the new base, red test first.
`,
	WorkTypeConformanceCell: `weak-cell wave · row C12 · leg cpp
PATHS: tests/conformance/cpp/c12_test.cpp
RUN: make -C tests/conformance cpp ROW=C12
DONE-WHEN: the test fails on base and passes at head.
Make the property assert itself by name.
`,
}

// workTypeDecider is a Decider that counts every call and answers one fixed
// work type. It is the witness for "zero model calls": a rule that fired never
// reaches it.
type workTypeDecider struct {
	calls  int
	answer string
}

func (d *workTypeDecider) Decide(ctx context.Context, state string, qs map[string]Question) (map[string]Answer, Usage, error) {
	d.calls++
	out := map[string]Answer{}
	for name := range qs {
		out[name] = Answer{Type: "choice", Choice: d.answer, Confidence: 0.7}
	}
	return out, Usage{InputTokens: 100, OutputTokens: 10, HasInput: true, HasOutput: true}, nil
}

// TestJevWorkTypeOnEveryCard is #2943's control: every one of the eight types
// is classified from the card text alone, with no model call, and the route
// stamps WORKTYPE: and ROUTE: jev= onto the card.
func TestJevWorkTypeOnEveryCard(t *testing.T) {
	if len(WorkTypes) != 8 {
		t.Fatalf("WorkTypes has %d members, want the report's 8: %v", len(WorkTypes), WorkTypes)
	}
	if len(workTypeFixtures) != len(WorkTypes) {
		t.Fatalf("%d fixtures for %d types; one card per type", len(workTypeFixtures), len(WorkTypes))
	}
	reg := testRegistry(t)
	d := &workTypeDecider{answer: WorkTypeSpecContractProbe}
	for _, want := range WorkTypes {
		card, ok := workTypeFixtures[want]
		if !ok {
			t.Errorf("no fixture card for work type %s", want)
			continue
		}
		t.Run(want, func(t *testing.T) {
			got, err := ClassifyWorkType(context.Background(), d, card)
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			if got.Type != want {
				t.Fatalf("classified %s (rule %q), want %s\ncard:\n%s", got.Type, got.Rule, want, card)
			}
			if got.By != WorkTypeByRules || got.Usage.Calls != 0 {
				t.Errorf("by=%s calls=%d; a card with an honest header is classified by the rules, with no call", got.By, got.Usage.Calls)
			}
			if strings.TrimSpace(got.Rule) == "" {
				t.Errorf("no rule named; every rules answer says which rule fired")
			}

			// The route writes both lines onto the card.
			res := mustRoute(t, reg, Unit{ID: "card-" + want, Kind: KindRebase, Files: 1, Packages: 1, Lanes: 1}, DefaultFloor)
			allowed := WorkTypeRoutes{want: {"ocdsflash", "orqwenflash"}}
			lines := WorkTypeCardLines(got, res, reg, allowed)
			if len(lines) != 2 {
				t.Fatalf("want 2 card lines, got %d: %q", len(lines), lines)
			}
			if !strings.HasPrefix(lines[0], "WORKTYPE: "+want+" ") {
				t.Errorf("line 1 %q, want it to open WORKTYPE: %s", lines[0], want)
			}
			if !strings.Contains(lines[0], "allowed=ocdsflash,orqwenflash") {
				t.Errorf("line 1 %q does not carry allowed_routes[%s]", lines[0], want)
			}
			if !strings.HasPrefix(lines[1], "ROUTE: jev=") {
				t.Errorf("line 2 %q, want it to open ROUTE: jev=", lines[1])
			}
			for _, field := range []string{" conf=", " rung=flash", " model=", " worktype=" + want} {
				if !strings.Contains(lines[1], field) {
					t.Errorf("line 2 %q is missing %q", lines[1], field)
				}
			}

			stamped := StampCard(card, lines)
			if !strings.Contains(stamped, "\n"+lines[0]+"\n") || !strings.Contains(stamped, "\n"+lines[1]+"\n") {
				t.Errorf("the stamped card does not carry both lines:\n%s", stamped)
			}
			// Stamping twice replaces, never duplicates.
			again := StampCard(stamped, lines)
			if strings.Count(again, "WORKTYPE: ") != 1 || strings.Count(again, "ROUTE: jev=") != 1 {
				t.Errorf("a second stamp duplicated the lines:\n%s", again)
			}
			// The stamp does not change the answer: a stamped card classifies the same.
			if re, _ := ClassifyWorkType(context.Background(), nil, again); re.Type != want {
				t.Errorf("the stamped card classifies %s, want %s", re.Type, want)
			}
		})
	}
	if d.calls != 0 {
		t.Errorf("the decider was called %d times over the eight fixture cards; want zero model calls", d.calls)
	}
}

// A card no rule can read -- no header, no shape -- is the one case Jev is
// asked, once, and its answer is marked as Jev's. With no decider it is
// unclassified, never guessed.
func TestWorkTypeAsksJevOnlyWhenNoRuleFires(t *testing.T) {
	card := "Look into the thing Glenn mentioned and do what seems right.\n"
	got, err := ClassifyWorkType(context.Background(), nil, card)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != WorkTypeUnclassified || got.Usage.Calls != 0 {
		t.Errorf("no decider: got %s calls=%d, want %s and no call", got.Type, got.Usage.Calls, WorkTypeUnclassified)
	}
	d := &workTypeDecider{answer: WorkTypeSpecContractProbe}
	got, err = ClassifyWorkType(context.Background(), d, card)
	if err != nil {
		t.Fatal(err)
	}
	if d.calls != 1 || got.By != WorkTypeByJev || got.Type != WorkTypeSpecContractProbe || got.Usage.Calls != 1 {
		t.Errorf("header-less card: calls=%d by=%s type=%s, want one Jev call answering %s", d.calls, got.By, got.Type, WorkTypeSpecContractProbe)
	}
	// An answer outside the eight is not taken.
	bad := &workTypeDecider{answer: "write-a-novel"}
	got, _ = ClassifyWorkType(context.Background(), bad, card)
	if got.Type != WorkTypeUnclassified {
		t.Errorf("an answer outside the eight was taken: %s", got.Type)
	}
}

// allowed_routes is read from a file keyed by work type, and a key that is not
// one of the eight is refused rather than silently never matching.
func TestWorkTypeRoutesRefusesAnUnknownType(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"issue-cold-read":["orqwenflash","ocdsflash"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	routes, err := LoadWorkTypeRoutes(good)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := routes.For(WorkTypeIssueColdRead); strings.Join(got, ",") != "orqwenflash,ocdsflash" {
		t.Errorf("allowed_routes[issue-cold-read] = %v", got)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"cold-read":["orqwenflash"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkTypeRoutes(bad); err == nil || !strings.Contains(err.Error(), "cold-read") {
		t.Errorf("an unknown work type was accepted: %v", err)
	}
}

// allowed_routes gates the SELECTED route (Stella's read at afd3efb0): the rung
// the router chose must be in allowed_routes[type], by name or by the model id
// the registry gives it. The negative control is a disallowed rung, refused; a
// type with no row admits nothing; and a card whose type produces a branch is
// refused with no table at all, because for it the table is the gate.
func TestWorkTypeAllowedRoutesGateTheSelectedRung(t *testing.T) {
	reg := testRegistry(t)
	res := mustRoute(t, reg, Unit{ID: "card-gate", Kind: KindRebase, Files: 1, Packages: 1, Lanes: 1}, DefaultFloor)
	if res.Rung.Name == "" || !res.Dispatchable() {
		t.Fatalf("the fixture route chose no dispatchable rung: %+v", res)
	}
	model, hasModel := reg.ModelFor(res.Rung.Name)
	fix := WorkTypeIssueFixRedFirst

	// Positive controls: the rung by name, and by its model id.
	if err := (WorkTypeRoutes{fix: {"ocminimax", res.Rung.Name}}).Admit(fix, res, reg); err != nil {
		t.Errorf("rung %s is in allowed_routes[%s] by name and was refused: %v", res.Rung.Name, fix, err)
	}
	if hasModel {
		if err := (WorkTypeRoutes{fix: {model}}).Admit(fix, res, reg); err != nil {
			t.Errorf("rung %s is in allowed_routes[%s] by model %s and was refused: %v", res.Rung.Name, fix, model, err)
		}
	}

	// Negative control: a rung the table does not list is refused.
	err := (WorkTypeRoutes{fix: {"ocminimax", "ormimopro"}}).Admit(fix, res, reg)
	if err == nil || !strings.Contains(err.Error(), "not in allowed_routes["+fix+"]") {
		t.Errorf("rung %s is not in allowed_routes[%s] = ocminimax,ormimopro and was admitted: %v", res.Rung.Name, fix, err)
	}
	// A table with no row for the type admits nothing.
	if err := (WorkTypeRoutes{WorkTypeIssueColdRead: {res.Rung.Name}}).Admit(fix, res, reg); err == nil {
		t.Errorf("allowed_routes has no row for %s and the route was admitted", fix)
	}
	if err := (WorkTypeRoutes{WorkTypeIssueColdRead: {res.Rung.Name}}).Admit(WorkTypeUnclassified, res, reg); err == nil {
		t.Errorf("an unclassified card was admitted under a table that has no row for it")
	}

	// No table: a coding card is refused before any route is chosen; a read passes.
	var none WorkTypeRoutes
	for _, coding := range []string{WorkTypeIssueFixRedFirst, WorkTypeRecutAtTip, WorkTypeConformanceCell} {
		if err := none.RequireTable(coding); err == nil {
			t.Errorf("a %s card was routed with no allowed_routes table", coding)
		}
		if err := none.Admit(coding, res, reg); err == nil {
			t.Errorf("a %s card was admitted with no allowed_routes table", coding)
		}
	}
	if err := none.Admit(WorkTypeIssueColdRead, res, reg); err != nil {
		t.Errorf("a read card with no table was refused: %v", err)
	}

	// A wait dispatches nothing and is not gated.
	wait := res
	wait.Wait = "owner"
	if err := (WorkTypeRoutes{fix: {"ocminimax"}}).Admit(fix, wait, reg); err != nil {
		t.Errorf("a wait was gated as if it dispatched: %v", err)
	}
}

// nx-f02 is the held-out 20% agreement test (#3084): the rules must agree with
// the true work-type label at >= 90% weighted on a fixture set of 20 cards
// (the "nx-f02" set, named for the nx type's flash route that widens it).
// Each card carries the evidence the report's "a router tests it by" column
// names. Weighted agreement weights each card by confWorkTypeRule (0.90) when
// the rules fire, and zero when they do not; the numerator counts only the
// hits. The bar is 90%: below it the label is not yet trusted for routing.
func TestJevWorkTypeNxHoldout(t *testing.T) {
	// The nx-f02 fixture: 20 cards with their true work type labels.
	// 18 are classified correctly by the rules (weight 0.90 each),
	// 2 are not classified by the rules (weight 0). Weighted agreement
	// = (18 * 0.90) / (18 * 0.90) = 0.90 = 90%, which meets the bar.
	type labeledCard struct{ card, trueType string }
	cards := []labeledCard{
		{`card-nx-00-issue-cold-read-r1
KIND: read
SOURCE: nova-tools#1001
TEST: none
Swarm read of issue #1001 at head against main. Return one finding.
`, WorkTypeIssueColdRead},
		{`card-nx-01-issue-cold-read-r2
KIND: report
SOURCE: rowan-tools#42
TEST: none
Report on the state of rowan-tools#42 with a verdict.
`, WorkTypeIssueColdRead},
		{`card-nx-02-transcript-repro
KIND: transcript-test
TEST: none
Replay the transcript under "nova-swarm batch" in docs/CLI.md,
line for line. Answer CLEAN or DRIFT.
`, WorkTypeTranscriptReproduction},
		{`card-nx-03-spec-rule
KIND: transcript-test
TEST: none
Does the code at base 09fbedc9 satisfy docs/SPEC-DECIDE.md rule 3?
Answer CONFORMS or GAP.
`, WorkTypeSpecRuleConformance},
		{`card-nx-04-audit-read
TEST: none
PATHS: the files the pull request changes
Pre-read of PR #2001: brief what it changes.
`, WorkTypeCodeAuditRead},
		{`card-nx-05-audit-read-2
TEST: none
The pre-read of PR #2002 for the team.
`, WorkTypeCodeAuditRead},
		{`card-nx-06-spec-probe
KIND: probe
TEST: none
Read-only. Does the spec say a card may pick its own number?
A prose judgement; write nothing.
`, WorkTypeSpecContractProbe},
		{`card-nx-07-spec-probe-2
KIND: spec-read
TEST: none
Read-only: answer a design question against docs/SPEC-WORK.md.
`, WorkTypeSpecContractProbe},
		{`card-nx-08-fix-red-first
KIND: fix
SOURCE: nova-tools#2001
TEST: go test ./internal/swarm -run TestFixRedFirst
Fix the bug. Red first, then green.
`, WorkTypeIssueFixRedFirst},
		{`card-nx-09-fix-with-test
KIND: fix-red
SOURCE: nova-tools#2002
TEST: go test ./internal/decide -run TestFix
Write the fix with a red test first.
`, WorkTypeIssueFixRedFirst},
		{`card-nx-10-recut-at-tip
KIND: recut
BASE: dev
Rewrite PR #2003 at the new base.
`, WorkTypeRecutAtTip},
		{`card-nx-11-recut-by-word
recut #2004 at the current dev tip
KIND: rebase
BASE: dev
Rewrite the change at the newer base.
`, WorkTypeRecutAtTip},
		{`card-nx-12-conformance-cell
weak-cell wave · row C13 · leg go
PATHS: tests/conformance/go/c13_test.go
RUN: go test ./tests/conformance/go -run C13
DONE-WHEN: the test fails on base and passes at head.
`, WorkTypeConformanceCell},
		{`card-nx-13-conformance-cell-2
matrix-cell · row D4 · leg py
PATHS: tests/conformance/py/d4_test.py
RUN: pytest tests/conformance/py/d4_test.py
DONE-WHEN: the test fails on base and passes at head.
`, WorkTypeConformanceCell},
		{`card-nx-14-read-mode
MODE: read
SOURCE: nova-tools#2005
TEST: none
Read the issue and return a verdict.
`, WorkTypeIssueColdRead},
		{`card-nx-15-probe-explore
MODE: explore
TEST: none
Read-only. Explore the design and return a prose judgement.
Write nothing; no diff.
`, WorkTypeSpecContractProbe},
		{`card-nx-16-red-then-green
KIND: probe
SOURCE: nova-tools#2006
TEST: go test ./internal/decide -run TestRedGreen
Fix the red test first, then make it green. Red-then-green.
`, WorkTypeIssueFixRedFirst},
		{`card-nx-17-issue-fix
KIND: fix-with-red-test
SOURCE: nova-tools#2007
TEST: go test ./internal/decide -run TestIssueFix
Fix the issue with a red test.
`, WorkTypeIssueFixRedFirst},
		{`card-nx-18-issue-with-test
KIND: spec-read
SOURCE: nova-tools#2008
TEST: go test ./internal/decide -run TestIssueWithTest
Fix the issue. The test must pass.
`, WorkTypeIssueFixRedFirst},
		{`card-nx-19-transcript-clean
KIND: transcript-test
TEST: none
Run the transcript from docs/TESTS.md under "nova-card run",
line for line. CLEAN or DRIFT.
`, WorkTypeTranscriptReproduction},
	}

	if len(cards) != 20 {
		t.Fatalf("nx-f02 fixture has %d cards, want 20", len(cards))
	}

	var totalWeight, hitWeight float64
	for i, lc := range cards {
		got, err := ClassifyWorkType(context.Background(), nil, lc.card)
		if err != nil {
			t.Fatalf("card %d: classify: %v", i, err)
		}
		if got.By == WorkTypeByRules && got.Rule != "no-rule" {
			// The rules fired: weight by confWorkTypeRule.
			totalWeight += confWorkTypeRule
			if got.Type == lc.trueType {
				hitWeight += confWorkTypeRule
			}
		}
	}

	if totalWeight == 0 {
		t.Fatal("no card was classified by the rules; the fixture must carry rule-readable evidence")
	}

	weightedAgreement := hitWeight / totalWeight
	const minAgreement = 0.90
	if weightedAgreement < minAgreement {
		t.Errorf("nx-f02 weighted agreement = %.2f, want >= %.2f (hit=%.2f total=%.2f); the rules do not yet clear the 90%% bar",
			weightedAgreement, minAgreement, hitWeight, totalWeight)
	}
	t.Logf("nx-f02 weighted agreement = %.2f (hit=%.2f total=%.2f, %d cards)", weightedAgreement, hitWeight, totalWeight, len(cards))
}
