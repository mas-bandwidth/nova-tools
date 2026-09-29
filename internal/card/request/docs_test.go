package request

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

const specPath = "../../../docs/SPEC-CARD-REQUESTS.md"

func specText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fenced returns the fenced blocks with the info string.
func fenced(t *testing.T, text, info string) []string {
	t.Helper()
	var out []string
	for _, p := range strings.Split(text, "```"+info+"\n")[1:] {
		end := strings.Index(p, "\n```")
		if end < 0 {
			t.Fatalf("an unclosed %s block", info)
		}
		out = append(out, p[:end+1])
	}
	return out
}

// C13: every example request in the doc parses, validates and is a fixed point of
// its own canonical form; the examples cover every operation but one that has no
// payload example of its own.
func TestEveryExampleInTheSpecParsesAndValidates(t *testing.T) {
	t.Parallel()
	seen := map[Operation]bool{}
	blocks := fenced(t, specText(t), "json")
	if len(blocks) < 7 {
		t.Fatalf("only %d examples", len(blocks))
	}
	for i, doc := range blocks {
		v, err := Parse([]byte(doc))
		if err != nil {
			t.Errorf("example %d does not parse: %v\n%s", i, err, doc)
			continue
		}
		seen[v.Kind()] = true
		if _, err := Validate(v.Request()); err != nil {
			t.Errorf("example %d does not validate: %v", i, err)
		}
		again, err := Parse(v.Canonical())
		if err != nil || string(again.Canonical()) != string(v.Canonical()) || again.Hash() != v.Hash() {
			t.Errorf("example %d: the canonical form is not a fixed point", i)
		}
	}
	for _, op := range Operations() {
		if !seen[op] {
			t.Errorf("the spec has no example of %s", op)
		}
	}
}

// The doc's tables are the code's tables: a row per input type with its sources,
// destination, class and fields, checked against the lifecycle.
func TestTheSpecsTransitionTableIsTheCodes(t *testing.T) {
	t.Parallel()
	text := specText(t)
	rows := map[InputType][]string{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 8 {
			continue
		}
		cells := make([]string, 6)
		for i := range cells {
			cells[i] = strings.TrimSpace(parts[i+1])
		}
		name := strings.Trim(cells[0], "`")
		if InputType(name).Valid() {
			rows[InputType(name)] = cells
		}
	}
	if len(rows) != len(InputTypes()) {
		t.Fatalf("the spec's table has %d input rows, the code %d", len(rows), len(InputTypes()))
	}
	for _, it := range InputTypes() {
		c := rows[it]
		var from []string
		for _, s := range SourceStates(it) {
			from = append(from, string(s))
		}
		if c[1] != strings.Join(from, ", ") {
			t.Errorf("%s: from %q, code %q", it, c[1], from)
		}
		row := transitions[it]
		to := string(row.to)
		if row.to == Unplaced {
			to = "off (" + string(row.outcome) + ")"
		}
		if c[2] != to || c[3] != string(row.class) || c[4] != strings.Join(row.required, ", ") || c[5] != strings.Join(row.allowed, ", ") {
			t.Errorf("%s: spec %v, code to=%s class=%s required=%v allowed=%v", it, c[2:], to, row.class, row.required, row.allowed)
		}
	}
}

// Every closed set is named in the doc: no cause, state, input, operation, evidence
// kind, disposition, outcome, forced move or notification field is missing from it.
func TestTheSpecNamesEveryClosedSet(t *testing.T) {
	t.Parallel()
	text := specText(t)
	need := func(kind, name string) {
		t.Helper()
		if !strings.Contains(text, "`"+name+"`") {
			t.Errorf("the spec does not name %s `%s`", kind, name)
		}
	}
	for _, c := range card.Causes() {
		need("the cause", string(c))
	}
	for _, s := range States() {
		need("the state", string(s))
	}
	for _, o := range Outcomes() {
		need("the outcome", string(o))
	}
	for _, o := range Operations() {
		need("the operation", string(o))
	}
	for _, k := range EvidenceKinds() {
		need("the evidence kind", string(k))
	}
	for _, d := range Dispositions() {
		need("the disposition", string(d))
	}
	for _, o := range SelectionOutcomes() {
		need("the selection outcome", string(o))
	}
	for _, n := range counterNames {
		need("the counter", n.name)
	}
	for _, f := range ForcedMoves() {
		if !strings.Contains(text, string(f.Kind)+" "+string(f.Disposition)+" in "+string(f.From)+" -> "+string(f.To)) {
			t.Errorf("the spec does not name the forced move %+v", f)
		}
	}
	// the words the layer keeps apart
	for _, want := range []string{"lifecycle input", "notification", "Landed is final", "leaves the table", "evidence binds to definition digest", "freshness guard", "enforced by the manager policy", "PR #4599"} {
		if !strings.Contains(text, want) {
			t.Errorf("the spec does not say %q", want)
		}
	}
	// no lifecycle "event" other than the operation's own name, and the names of PRs
	for n, line := range strings.Split(text, "\n") {
		l := strings.ToLower(line)
		for _, bad := range []string{"events", "an event", "each event", "event type"} {
			if strings.Contains(l, bad) && !strings.Contains(l, "apply_events") && !strings.Contains(l, `"event"`) {
				t.Errorf("line %d uses %q for a lifecycle input or a notification: %s", n+1, bad, line)
			}
		}
	}
	// no done state and no completed outcome or input
	for _, gone := range []string{"`done`", "`completed`", "`reopen`", "`ci-green`"} {
		for n, line := range strings.Split(text, "\n") {
			if strings.Contains(line, gone) && !strings.Contains(line, "no `done`") && !strings.Contains(line, "never a lifecycle input") && !strings.Contains(line, "is not") {
				t.Errorf("line %d names %s: %s", n+1, gone, line)
			}
		}
	}
}

// The sums of section 3.1 are the constants', the refusal line is what a refusal
// renders, and the receipt line form is the golden one.
func TestTheSpecsSumsAndLinesAreTheCodes(t *testing.T) {
	t.Parallel()
	text := specText(t)
	for _, n := range []int{card.ManifestAdmitMax, card.ManifestReplaceMax, card.ManifestInputsMax, card.ManifestEvidenceMax, card.ManifestResolveMax} {
		if !strings.Contains(text, fmt.Sprint(n)) {
			t.Errorf("the spec does not state the worst-case manifest of %d bytes", n)
		}
	}
	if !strings.Contains(text, fmt.Sprintf("%d bytes to spare", card.TableManifestBytes-card.ManifestEvidenceMax)) {
		t.Error("the spec does not state the margin")
	}
	r := envelope(OpApplyEvents)
	r.Inputs = []Input{input(InStart, "c1"), input(InResult, "c2")}
	r.Inputs[1].Expect.Place.Col = Landed
	_, err := Validate(r)
	line := err.(*Refusals).List[0].String()
	if !strings.Contains(text, line) {
		t.Errorf("the spec's refusal line is not what a refusal renders:\n%s", line)
	}
}
