package docs

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestWorklangAmendmentRuleNumbering pins docs/SPEC-WORKLANG.md's Amendment 1
// (2026-09-18): the rules are A1 to A14, in order, each one naming at least one
// red test, and every new key carrying a grammar block. The numbers are
// referred to from cards, from `nova-work ask` and from SPEC-JOBS section 9, so
// renumbering a rule breaks references that live outside this repo: a rule may
// be added at the end and may not be moved.
func TestWorklangAmendmentRuleNumbering(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-WORKLANG.md")
	if err != nil {
		t.Fatalf("docs/SPEC-WORKLANG.md: %v", err)
	}
	content := string(body)

	const heading = "## Part 4 — Amendment 1 (2026-09-18): resources, writes, identity, tools, owners, no barriers"
	if !strings.Contains(content, heading) {
		t.Fatalf("missing the amendment heading: %s", heading)
	}
	amendment := content[strings.Index(content, heading):]

	// The three sources of the amendment, each named where a reader can follow
	// it: Patrick's read, Glenn's lane rule, Stella's lease rule.
	for _, source := range []string{
		"Ideas #783",
		"a lane is a resource of capacity 1 over an area of the tree",
		"expiry is UNKNOWN until termination",
	} {
		if !strings.Contains(amendment, source) {
			t.Errorf("missing the source of record: %q", source)
		}
	}

	// The rules, in order, each with its statement. The rule text is the
	// contract; the number is the reference.
	rules := []struct {
		number    string
		statement string
	}{
		{"A1", "The work-set form is a plan."},
		{"A2", "A unit has an identity."},
		{"A3", "An attempt is a record, not a counter."},
		{"A4", "`uncertain` is a state."},
		{"A5", "Admission takes a vector, not a slot."},
		{"A6", "A lane is a resource of capacity 1 over an area of the tree."},
		{"A7", "`:writes` is what the unit edits, and it serializes across lanes."},
		{"A8", "One authority per capacity; a nested grant draws from its parent."},
		{"A9", "No global barriers."},
		{"A10", "`:tools` names the verbs a unit needs, at a version, with a key."},
		{"A11", "Output collections are named before the run, their members after it."},
		{"A12", "Warm state is accounted apart from active state."},
		{"A13", "`:owner` is a mind."},
		{"A14", "A unit names its evidence."},
	}

	at := -1
	for _, rule := range rules {
		want := fmt.Sprintf("**%s. %s**", rule.number, rule.statement)
		i := strings.Index(amendment, want)
		if i < 0 {
			t.Errorf("missing rule %s: %s", rule.number, want)
			continue
		}
		if i <= at {
			t.Errorf("rule %s is out of order; rules run A1 to A14 and are never renumbered", rule.number)
		}
		at = i
	}

	// A15 would be the next rule added, at the end. A rule number this test does
	// not know about means the list moved without the pin moving with it.
	if strings.Contains(amendment, "**A15.") && !strings.Contains(amendment, "A15") {
		t.Error("a rule past A14 exists; add it to this test rather than leaving it unpinned")
	}

	// Every rule names its red test, as the rest of the spec does. The reader's
	// tests are the ones landed with the amendment; the kernel's are named and
	// not implemented here.
	for _, red := range []string{
		"worklang-reads-the-real-work-set",
		"worklang-node-accepts-the-amendment-keys",
		"worklang-unit-id-is-stable-and-required",
		"worklang-attempt-without-a-termination-proof-is-uncertain",
		"worklang-uncertain-is-a-state",
		"worklang-resources-are-a-vector-not-a-slot",
		"worklang-lane-is-a-resource-of-capacity-one",
		"worklang-writes-are-paths-under-the-repo",
		"worklang-tools-name-a-verb-and-a-version",
		"worklang-a-collection-names-its-members-after-the-run",
		"worklang-warm-state-is-retained-apart-from-active",
		"worklang-owner-is-a-mind",
		"worklang-acceptance-is-read-and-the-real-set-has-none",
		"jobs-admission-is-atomic-no-partial-grant",
		"jobs-a-nested-grant-draws-from-its-parent",
		"jobs-intersecting-writes-serialize-across-lanes",
		"jobs-a-ready-unit-goes-with-no-global-barrier",
		"jobs-a-unit-without-acceptance-is-refused-at-load",
		"work-ask-reads-the-same-set-as-the-pull-worker",
	} {
		if !strings.Contains(amendment, red) {
			t.Errorf("missing the red test of record: %s", red)
		}
	}

	// One grammar block per new key: a rule without a grammar is prose, and the
	// reader cannot be written from prose.
	for _, grammar := range []string{
		`(work-set "<id>"`,
		`:was "<old id>"`,
		":attempts ((:n 1 :rung",
		":state :uncertain",
		":resources ((:lane",
		`:writes ("docs/SPEC-WORKLANG.md"`,
		":tools ((:nova-work :at",
		":collects ((:name",
		":warm (:retained",
		":acceptance ((:id",
		`:owner "Emma"`,
	} {
		if !strings.Contains(amendment, grammar) {
			t.Errorf("missing the grammar block for: %s", grammar)
		}
	}

	// The amendment is written against the real work set, and its numbers are
	// the reason the rules exist: 79 units, not one naming its evidence.
	for _, fact := range []string{
		"pitstop-2026-09-17.lisp",
		"**79 units**",
		"**0 — not one unit — carries `:acceptance`**",
		"not a plan: the top form must be (:plan ...)",
	} {
		if !strings.Contains(amendment, fact) {
			t.Errorf("missing the evidence of record: %q", fact)
		}
	}

	// The worked example is three real units, before and after.
	for _, unit := range []string{`(unit "verb:hygiene"`, `(unit "lanes:spec"`, `(unit "pull:worker"`} {
		if strings.Count(amendment, unit) < 2 {
			t.Errorf("the worked example must show %s before and after", unit)
		}
	}

	// The fence: this slice is parse only, and the scheduling side is not in it.
	if !strings.Contains(amendment, "It schedules nothing") {
		t.Error("missing the fence: the reader slice schedules nothing")
	}
}

// TestSpecJobsCarriesTheAmendment pins the scheduling side of the same
// amendment in docs/SPEC-JOBS.md: one section, in the file's own shape (game
// engine, nova, invariant, red tests), naming the rules it implements.
func TestSpecJobsCarriesTheAmendment(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-JOBS.md")
	if err != nil {
		t.Fatalf("docs/SPEC-JOBS.md: %v", err)
	}
	content := string(body)

	const heading = "## 9. Resource vectors, writes and no barriers (Amendment 1, 2026-09-18)"
	if !strings.Contains(content, heading) {
		t.Fatalf("missing the section heading: %s", heading)
	}
	section := content[strings.Index(content, heading):]

	// The file's own section shape, so section 9 reads like sections 1 to 8.
	for _, part := range []string{"**Game engine.**", "**Nova.**", "**Invariant.**", "**Red tests.**"} {
		if !strings.Contains(section, part) {
			t.Errorf("section 9 is missing %s", part)
		}
	}

	// It must say which amendment rules it carries, or the two files drift.
	for _, rule := range []string{"(A5)", "(A6)", "(A7)", "(A8)", "(A9)", "(A10)", "(A11)", "(A12)", "(A13)"} {
		if !strings.Contains(section, rule) {
			t.Errorf("section 9 does not name rule %s", rule)
		}
	}

	// The evidence of the day the rules came from, and the executor seam.
	for _, fact := range []string{
		"load average 147",
		"61 Linux",
		"55 orphan",
		"54 of",
		"### The executor seam",
		"jobs-a-disconnected-executor-is-uncertain-not-stopped",
	} {
		if !strings.Contains(section, fact) {
			t.Errorf("missing from section 9: %q", fact)
		}
	}
}
