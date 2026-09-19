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
	"os"
	"strings"
	"testing"

	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// specKinds is §5 rule 2's table, row for row, in the spec's order.
var specKinds = []struct {
	name    string
	steps   []string
	control bool // the spec's table names a control for this kind
	tokens  []string
	built   bool // this binary holds the row (rebase, sweep and mutation-kill are T13's)
}{
	{"fix-red", []string{"hygiene", "shape", "positive", "mutate"}, true, []string{"no-test", "vacuous-test", "named-test-not-red"}, true},
	{"transcript-test", []string{"hygiene", "shape", "positive"}, true, []string{"doc-edited", "transcript-not-read"}, true},
	{"rebase", nil, false, nil, false},
	{"sweep", nil, false, nil, false},
	{"mutation-kill", nil, false, nil, false},
	{"read", nil, false, nil, true},
	{"probe", nil, false, nil, true},
	{"text", nil, false, nil, true},
	{"tone", nil, false, nil, true},
}

// builtSpecKinds is the rows this binary holds, in the spec's order.
func builtSpecKinds() []int {
	var out []int
	for i, k := range specKinds {
		if k.built {
			out = append(out, i)
		}
	}
	return out
}

func TestKindsTableMatchesTheSpec(t *testing.T) {
	built := builtSpecKinds()
	if len(Kinds) != len(built) {
		t.Fatalf("the table holds %d rows, the spec's §5 table declares %d this binary builds: %v", len(Kinds), len(built), kindNames())
	}
	for n, i := range built {
		want := specKinds[i]
		got := Kinds[n]
		if got.Name != want.name {
			t.Fatalf("row %d is %q, the spec's is %q (the table is in the spec's order)", n, got.Name, want.name)
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
	all := strings.Split(strings.TrimSpace(out.String()), "\n")
	var lines, drift []string
	for _, l := range all {
		if strings.HasPrefix(l, "DRIFT ") {
			drift = append(drift, l)
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) != len(specKinds) {
		t.Fatalf("--kinds printed %d KIND lines for %d DECLARED kinds:\n%s", len(lines), len(specKinds), out.String())
	}
	// The DRIFT block is the names the CUTTERS write that the table does not hold: it
	// comes last, it never claims a gate, and every row names a kind the table DOES hold
	// as its nearest.
	if len(drift) != len(KindDrift) {
		t.Fatalf("--kinds printed %d DRIFT lines for %d drifted names:\n%s", len(drift), len(KindDrift), out.String())
	}
	if len(all) != len(lines)+len(drift) || !strings.HasPrefix(all[len(lines)], "DRIFT ") {
		t.Errorf("the DRIFT block is not last:\n%s", out.String())
	}
	for _, d := range drift {
		if !strings.Contains(d, " gate=none ") {
			t.Errorf("a drifted name claims a gate: %q", d)
		}
		name := strings.TrimPrefix(strings.Fields(d)[1], "name=")
		near, ok := NearestKind(name)
		if !ok {
			t.Errorf("%q is printed as drift but NearestKind does not know it", name)
			continue
		}
		if !strings.Contains(d, "nearest="+near) {
			t.Errorf("%q does not print its nearest name %q", d, near)
		}
		if _, held := KindNamed(near); !held {
			t.Errorf("%q points at %q, which the table does not hold either", d, near)
		}
	}
	for i, want := range specKinds {
		line := lines[i]
		if !strings.HasPrefix(line, "KIND name="+want.name+" ") {
			t.Fatalf("line %d is %q; the rows are the DECLARED set's own order and each names its kind first", i, line)
		}
		if !want.built {
			// A name §5 declares whose control this binary does not build says so
			// rather than going missing from the table a person reads.
			if !strings.Contains(line, " built=false ") || !strings.Contains(line, " gate=- ") {
				t.Errorf("%s is declared and not built here; the row must say so: %q", want.name, line)
			}
			if !KindDeclaredNotBuilt(want.name) {
				t.Errorf("KindDeclaredNotBuilt(%q) is false", want.name)
			}
			if near, ok := NearestKind(want.name); ok {
				t.Errorf("%s is the spec's own name and was offered %q instead; its remedy is the task that builds it", want.name, near)
			}
			continue
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

// ONE LIST. The fix-review-bugs lane put the card kinds' NAME SET in
// internal/hygiene/kinds.txt on #1842 (d29674df), embedded beside stray.txt, because
// internal/pulse is ABOVE internal/hygiene (SPEC-TOOLWORK §3 rule 7: one implementation,
// three callers) and a gate's step list must not end up inside the check the gate calls.
// That is the way chosen here: this table reads its NAMES from there and keeps the gate
// steps, the controls and the reject tokens, which are the only part `accept` needs and
// the part `nova-check hygiene` and `nova-merge batch` cannot use.
//
// The swap is one function body -- DeclaredKinds returns hygiene.Kinds() -- and it
// happens when #1842 is on dev, before this PR leaves draft. #1842 is an open PR on
// another lane's branch carrying 852 lines across internal/review/seed.go, cmd/nova-check
// and the docs, and merging all of that into this stack to reach one data file would bury
// this change in a reader's diff.
//
// Until then this test is the coupling: kinds.txt's nine rows, transcribed, held against
// DeclaredKinds. It turns red the moment the two lists disagree, whichever of them moved.
func TestDeclaredKindsAreTheOneNameSet(t *testing.T) {
	// internal/hygiene/kinds.txt at d29674df, name column, in file order.
	want := []string{"fix-red", "transcript-test", "rebase", "sweep", "mutation-kill", "read", "probe", "text", "tone"}
	got := DeclaredKinds()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DeclaredKinds() = %v\ninternal/hygiene/kinds.txt = %v\none list: change both, or wire DeclaredKinds to hygiene.Kinds()", got, want)
	}
	// Every row of the gate's table is a declared name, and nothing else is.
	for _, k := range Kinds {
		found := false
		for _, d := range got {
			if d == k.Name {
				found = true
			}
		}
		if !found {
			t.Errorf("the table holds %q, which the name set does not declare", k.Name)
		}
	}
	// And the names this binary does not build are exactly T13's three.
	var notBuilt []string
	for _, d := range got {
		if KindDeclaredNotBuilt(d) {
			notBuilt = append(notBuilt, d)
		}
	}
	if strings.Join(notBuilt, ",") != "rebase,sweep,mutation-kill" {
		t.Errorf("declared and not built here = %v, want rebase,sweep,mutation-kill (T13, #1658)", notBuilt)
	}
}

// TestDeclaredKindsIsNotASecondList holds the ONE LIST ruling by its mechanism rather
// than by its result. The transcription test above compares two lists and goes green
// whenever they happen to agree; this one goes red while a second list EXISTS to
// disagree, which is the state #1781's body promised to end once #1842 was on dev
// (internal/hygiene/kinds.txt, landed in integration-16am).
//
// It reads this package's own source: no kind name may appear as a literal inside
// DeclaredKinds. The names live in the data file, and the function fetches them.
func TestDeclaredKindsIsNotASecondList(t *testing.T) {
	raw, err := os.ReadFile("kinds.go")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := funcBody(string(raw), "func DeclaredKinds() []string {")
	if !ok {
		t.Fatal("DeclaredKinds is not in kinds.go under that signature")
	}
	for _, name := range hyg.Kinds() {
		if strings.Contains(body, `"`+name+`"`) {
			t.Fatalf("DeclaredKinds spells %q itself:\n%s\none list: the names are internal/hygiene/kinds.txt's, and this function returns hygiene.Kinds()", name, body)
		}
	}
	// And the one list is still the list: the swap must not quietly empty it.
	if got := strings.Join(DeclaredKinds(), ","); got != strings.Join(hyg.Kinds(), ",") {
		t.Fatalf("DeclaredKinds() = %s, hygiene.Kinds() = %s", got, strings.Join(hyg.Kinds(), ","))
	}
}

// funcBody returns the text between the brace that opens the named function and the
// first line that is a bare closing brace: enough to say what a one-line function body
// holds, and never a parser this test does not need.
func funcBody(src, signature string) (string, bool) {
	i := strings.Index(src, signature)
	if i < 0 {
		return "", false
	}
	rest := src[i+len(signature):]
	if j := strings.Index(rest, "\n}"); j >= 0 {
		return rest[:j], true
	}
	return "", false
}
