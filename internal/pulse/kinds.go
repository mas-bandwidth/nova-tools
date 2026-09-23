package pulse

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The kinds table: a card kind is a template PLUS the gate that judges it and the control
// that proves the gate (SPEC-TOOLWORK.md §5 rules 2-3, PR #1637). The template is text
// for the worker; this is code in the tool the worker never sees and cannot edit, chosen
// by the card's KIND: line and by nothing the worker wrote (§5 rule 6).
//
// T03 (#1648) puts the table here because `accept` cannot run without knowing a kind's
// steps. T06 (#1651) adds `nova-pulse accept --kinds`, which prints it, and the class
// test that pins it to the spec's table; T13 (#1658) adds `rebase`, `sweep` and
// `mutation-kill` with their controls. There is no default kind: a card whose KIND: the
// table does not hold is refused by `cut` and abstained by `accept`.

// Kind is one row of the table.
type Kind struct {
	Name string
	// Does is the one-line description of the card's work.
	Does string
	// Paths says what the card's PATHS: line may hold, for a reader.
	Paths string
	// Steps are the gate's steps in order, from {hygiene, shape, positive, mutate}. An
	// empty list is gate `none`: the HARVEST row says gate=none and accept refuses to
	// judge the card, so a green row never claims a check that did not run.
	Steps []string
	// Control is what must be seen red before the gate's green counts for this card.
	Control string
	// Tokens are the kind-specific reject tokens, beyond the common gate's.
	Tokens []string
}

// The step names, spelled once.
const (
	StepHygiene  = "hygiene"
	StepShape    = "shape"
	StepPositive = "positive"
	StepMutate   = "mutate"
)

// Kinds is the table, in the spec's order.
var Kinds = []Kind{
	{
		Name:    "fix-red",
		Does:    "fixes one defect, red test first",
		Paths:   "the named source and test files",
		Steps:   []string{StepHygiene, StepShape, StepPositive, StepMutate},
		Control: "the change reverted: nova-review mutate range form PASS, and TEST: is among the tests that went red",
		Tokens:  []string{"no-test", "vacuous-test", "named-test-not-red"},
	},
	{
		Name:    "transcript-test",
		Does:    "makes one docs/TESTS.md section executed line for line",
		Paths:   "cmd/<tool>/firstrun_test.go, cmd/<tool>/testdata/firstrun/** -- never docs/TESTS.md, never the rest of testdata/",
		Steps:   []string{StepHygiene, StepShape, StepPositive},
		Control: "three seeds applied to a copy of the tool's section, each one edit: a line dropped, a value altered, a line moved; the new test goes red on each",
		Tokens:  []string{"doc-edited", "transcript-not-read"},
	},
	{Name: "read", Does: "read and report", Paths: "none"},
	{Name: "probe", Does: "read and report", Paths: "none"},
	{Name: "text", Does: "read and report", Paths: "none"},
	{Name: "tone", Does: "read and report", Paths: "none"},
}

// KindNamed looks a kind up by its KIND: value.
func KindNamed(name string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// Gated says whether the kind declares a gate at all.
func (k Kind) Gated() bool { return len(k.Steps) > 0 }

// Step says whether the kind's gate runs the named step.
func (k Kind) Step(name string) bool {
	for _, s := range k.Steps {
		if s == name {
			return true
		}
	}
	return false
}

// ControlBuilt says whether THIS build of the tool implements the kind's own control.
// fix-red's is the mutate range form (T01). transcript-test's three seeds are T08's and
// T13's; until they are built, accept refuses a transcript-test card rather than print an
// ACCEPT OK its control never proved.
func (k Kind) ControlBuilt() bool {
	switch k.Name {
	case "fix-red":
		return true
	}
	return !k.Gated()
}

// The kinds the CUTTERS actually write, and the nearest name the table holds.
//
// Measured 2026-09-19 over the 91 cards under tmp/session-0919b (`grep -h '^KIND:'`):
// fix-red 43, fix-with-red-test 20, dogfood 11, transcript-test 8, sweep 3, new-verb 2,
// row-test 1, rebase 1, mutation-kill 1, docs-fix 1. Five of those ten names -- 35 of the
// 91 cards -- are not in SPEC-TOOLWORK §5's table at any head, including `origin/dev`,
// although several of the cards say `SPEC: docs/SPEC-TOOLWORK.md §5 kind <name>`. A card
// writer's claim that a kind is in the spec is not the spec.
//
// This is a SPELLING map and nothing else. It never assigns a gate: `accept` abstains on
// a kind the table does not hold (§5 rule 3, there is no default kind), and that does not
// change here. What it buys is a refusal a person can act on -- `cut` and `lint` can say
// which name the table does hold instead of only that this one is wrong.
//
// The nearest name is read off the card's own header, not off its title:
//   - fix-with-red-test, dogfood, new-verb, row-test all carry PATHS: plus a real
//     `TEST: <pkg> <TestName>`, which is fix-red's shape.
//   - docs-fix carries `TEST: none`, which is an ungated kind's shape; `text` is the
//     table's name for it.
//
// The other road is that §5 GAINS these names with their own controls. That is a spec
// change, not a code change, and it is asked on the PRs rather than decided here.
var KindDrift = map[string]string{
	"fix-with-red-test": "fix-red",
	"dogfood":           "fix-red",
	"new-verb":          "fix-red",
	"row-test":          "fix-red",
	"docs-fix":          "text",
}

// NearestKind is the table's name for a kind the table does not hold, and whether there
// is one. A name the table DOES hold is not drift and answers false.
func NearestKind(name string) (string, bool) {
	if _, ok := KindNamed(name); ok {
		return "", false
	}
	near, ok := KindDrift[strings.TrimSpace(name)]
	return near, ok
}

// UnknownKindRemedy is the one sentence every refusal of an unknown kind says, in one
// place so `cut`, `lint --card` and `accept` cannot say it differently.
func UnknownKindRemedy(name string) string {
	if near, ok := NearestKind(name); ok {
		return fmt.Sprintf("the kinds table does not hold %s; the nearest name it holds is %s -- use that if it is the shape of this card, or ask for %s to be added to SPEC-TOOLWORK §5 with its own control (run `nova-pulse accept --kinds` for the table)",
			oneline.Field(name), oneline.Field(near), oneline.Field(name))
	}
	return fmt.Sprintf("the kinds table does not hold %s, and there is no default kind (run `nova-pulse accept --kinds` for the table)", oneline.Field(name))
}
