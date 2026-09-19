package pulse

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
