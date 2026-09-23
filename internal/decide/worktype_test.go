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
