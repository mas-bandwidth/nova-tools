package pulse

// T06b (#1651), SPEC-TOOLWORK §5 rule 3: "a kind's gate is declared in the tool, in one
// table, and printed". The table is data in kinds.go; `nova-pulse accept --kinds` prints
// it; and this is the class test that holds the printed table to the spec's §5 table --
// the same kinds, in the same order, with the same steps and the same reject tokens.
//
// The spec's table is TRANSCRIBED here rather than read out of docs/SPEC-TOOLWORK.md
// because that document is PR #1637 and is not in the tree yet. When it lands, this test
// gains the doc read the way admission_checklist_spec_test.go has it; until then the
// transcription is what turns red when someone edits the table and not the spec.

import (
	"bytes"
	"strings"
	"testing"
)

// specKinds is §5 rule 2's table, row for row, in the spec's order.
var specKinds = []struct {
	name    string
	steps   []string
	control bool // the spec's table names a control for this kind
	tokens  []string
}{
	{"fix-red", []string{"hygiene", "shape", "positive", "mutate"}, true, []string{"no-test", "vacuous-test", "named-test-not-red"}},
	{"transcript-test", []string{"hygiene", "shape", "positive"}, true, []string{"doc-edited", "transcript-not-read"}},
	{"read", nil, false, nil},
	{"probe", nil, false, nil},
	{"text", nil, false, nil},
	{"tone", nil, false, nil},
}

func TestKindsTableMatchesTheSpec(t *testing.T) {
	if len(Kinds) != len(specKinds) {
		t.Fatalf("the table holds %d kinds, the spec's §5 table %d: %v", len(Kinds), len(specKinds), kindNames())
	}
	for i, want := range specKinds {
		got := Kinds[i]
		if got.Name != want.name {
			t.Fatalf("row %d is %q, the spec's is %q (the table is in the spec's order)", i, got.Name, want.name)
		}
		if strings.Join(got.Steps, ",") != strings.Join(want.steps, ",") {
			t.Errorf("%s: gate is %v, the spec's is %v", got.Name, got.Steps, want.steps)
		}
		if (got.Control != "") != want.control {
			t.Errorf("%s: control=%q, the spec's table names one: %v", got.Name, got.Control, want.control)
		}
		if strings.Join(got.Tokens, ",") != strings.Join(want.tokens, ",") {
			t.Errorf("%s: tokens are %v, the spec's are %v", got.Name, got.Tokens, want.tokens)
		}
		if got.Gated() != (len(want.steps) > 0) {
			t.Errorf("%s: Gated()=%v with steps %v", got.Name, got.Gated(), got.Steps)
		}
	}
}

func TestKindsPrintsOneLinePerKindAndNamesTheSameTable(t *testing.T) {
	var out bytes.Buffer
	if code := PrintKinds(&out); code != 0 {
		t.Fatalf("--kinds exited %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(specKinds) {
		t.Fatalf("--kinds printed %d lines for %d kinds:\n%s", len(lines), len(specKinds), out.String())
	}
	for i, want := range specKinds {
		line := lines[i]
		if !strings.HasPrefix(line, "KIND name="+want.name+" ") {
			t.Fatalf("line %d is %q; the rows are the table's own order and each names its kind first", i, line)
		}
		gate := "none"
		if len(want.steps) > 0 {
			gate = strings.Join(want.steps, ",")
		}
		if !strings.Contains(line, " gate="+gate+" ") {
			t.Errorf("%s: the printed row does not carry gate=%s: %q", want.name, gate, line)
		}
		tokens := "-"
		if len(want.tokens) > 0 {
			tokens = strings.Join(want.tokens, ",")
		}
		if !strings.Contains(line, " tokens="+tokens) {
			t.Errorf("%s: the printed row does not carry tokens=%s: %q", want.name, tokens, line)
		}
		if !strings.Contains(line, " control=") {
			t.Errorf("%s: the printed row names no control at all: %q", want.name, line)
		}
		if strings.Contains(line, "\t") {
			t.Errorf("%s: a printed row is one line of tokens, not a tab table: %q", want.name, line)
		}
	}
}

// kindNames is the table's names, for a failure message.
func kindNames() []string {
	var out []string
	for _, k := range Kinds {
		out = append(out, k.Name)
	}
	return out
}
