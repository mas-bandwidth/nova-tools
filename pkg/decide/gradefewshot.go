package decide

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// The few-shot calibration of the grade (SPEC-NOVA-DECIDE section 11): ten landed cards per
// class, drawn from the sprint record by the caller's examples file, ahead of the card under
// grade. The library reads no store: the examples are an input.

// GradeExamplesPerClass is how many examples of each class the prompt carries.
const GradeExamplesPerClass = 10

// GradeHeavy is the label of a card that needed the heavy tier; the grade's own options stay
// script, flash and pro, and the examples teach the line between them.
const GradeHeavy = "heavy"

// GradeExampleClasses are the example labels in prompt order: landed on flash within two
// attempts, needed pro, needed heavy.
var GradeExampleClasses = []string{GradeFlash, GradePro, GradeHeavy}

// Example is one landed card of the record: its brief heading, its PATHS line, its KIND and the
// label of its outcome. Never the brief's whole text.
type Example struct {
	Card    string `json:"card"`
	Heading string `json:"heading"`
	Paths   string `json:"paths"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
}

// ParseExamples reads an examples file, one value per line in any form ({card, heading, paths,
// kind, label}); every bad line is named by number in one error. The grade examples file reads
// through pkg/schemaread and the generated Example table (pkg/decidewire), so its binary image,
// its padded standard base64, the schema JSON text and the legacy form decode to one equal value,
// a malformed or clamped value of each form is refused by name, and each read is counted under
// its form (the owner's rule S2, readers first S4, UNION.md C13).
func ParseExamples(raw []byte) ([]Example, error) {
	var out []Example
	var problems []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		e, _, err := ParseExamplesReader.Read([]byte(line))
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", n, err))
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("the examples file has %d bad lines: %s", len(problems), strings.Join(problems, "; "))
	}
	return out, nil
}

// exampleLabel is an Example table label as the grade's own classes spell it.
func exampleLabel(l decidewire.ExampleLabel) string {
	switch l {
	case decidewire.ExampleLabelFlash:
		return GradeFlash
	case decidewire.ExampleLabelPro:
		return GradePro
	case decidewire.ExampleLabelHeavy:
		return GradeHeavy
	}
	return ""
}

// exampleFromTable is an example row as the grade's code holds it.
func exampleFromTable(r *decidewire.ExampleRow, ar []*decidewire.TableArena) Example {
	return Example{
		Card:    strAt(r.Card, ar),
		Heading: strAt(r.Heading, ar),
		Paths:   strAt(r.Paths, ar),
		Kind:    strAt(r.Kind, ar),
		Label:   exampleLabel(r.Label),
	}
}

func parseExampleTyped(v *Example, wire []byte) schemaread.Report {
	var b decidewire.ExampleBuilder
	if !b.Init() {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	defer b.Shutdown()
	var rep decidewire.TableReport
	if !decidewire.ExampleLoadBuilder(&b, wire, &rep) {
		return reportOf(&rep, false)
	}
	root := b.GetRoot()
	ar := []*decidewire.TableArena{&b.Arena}
	*v = exampleFromTable(root, ar)
	return typedDone(&rep, func() bool {
		return canonical[decidewire.Example](&b, wire, decidewire.ExampleMeasure, decidewire.ExampleSave)
	})
}

// exampleCheck holds an example to the bounds its reading code enforces: a card and a heading
// are required, and the label is one of the grade's classes (S2: the checks made today apply to
// every form, so one value reads alike from each).
func exampleCheck(e *Example, c *schemaread.Checker) {
	c.Required("card", e.Card != "")
	c.Required("heading", e.Heading != "")
	ord := int64(0)
	switch e.Label {
	case GradeFlash:
		ord = 1
	case GradePro:
		ord = 2
	case GradeHeavy:
		ord = 3
	}
	c.Bound("label", ord, 1, 3)
}

// newExampleReader is the grade examples file's strict reader: the table wire, the padded
// standard base64 of it, the schema JSON text and the legacy form ParseExamples read today. The
// legacy form keeps the base read (one object per line, unknown keys refused); the schema JSON
// text spells the label as the table's enum name, so the legacy read lowers its case, and both
// read to the same value.
func newExampleReader() *schemaread.Reader[Example] {
	return decided[Example]{name: "EXAMPLE", help: "nova-decide grade --examples -h",
		measure: func(wire []byte) error { _, err := decidewire.ExampleLoadMeasureReason(wire); return err },
		read: parseExampleTyped,
		legacy: func(v *Example, in []byte) schemaread.Report {
			dec := json.NewDecoder(strings.NewReader(string(in)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(v); err != nil {
				return schemaread.Report{Reason: err.Error()}
			}
			if err := dec.Decode(new(any)); err != io.EOF {
				return schemaread.Report{Malformed: true}
			}
			v.Label = strings.ToLower(v.Label)
			return schemaread.Report{}
		},
		check: exampleCheck}.reader()
}

// ParseExamplesReader is the grade examples file's package reader.
var ParseExamplesReader = newExampleReader()

// NewExamplesReader is a grade examples reader with empty counters, for a test to count.
func NewExamplesReader() *schemaread.Reader[Example] { return newExampleReader() }

// PickExamples chooses per examples of each class from pool, deterministically: held-out cards
// leave the pool, a card's rank is the SHA-256 of seed and its id (so neither the pool's order
// nor its size moves a card's rank), and the lowest ranks are taken. The result is in class
// order, then rank. A class with fewer than per examples left is an error naming it.
func PickExamples(pool []Example, heldOut map[string]bool, seed string, per int) ([]Example, error) {
	rank := func(e Example) string {
		sum := sha256.Sum256([]byte(seed + "\n" + e.Card))
		return hex.EncodeToString(sum[:])
	}
	var out []Example
	for _, label := range GradeExampleClasses {
		var have []Example
		seen := map[string]bool{}
		for _, e := range pool {
			if e.Label == label && !heldOut[e.Card] && !seen[e.Card] {
				seen[e.Card] = true
				have = append(have, e)
			}
		}
		if len(have) < per {
			return nil, fmt.Errorf("the examples have %d %s cards outside the held-out set; the grade prompt wants %d", len(have), label, per)
		}
		slices.SortFunc(have, func(a, b Example) int { return strings.Compare(rank(a), rank(b)) })
		out = append(out, have[:per]...)
	}
	return out, nil
}

// GradeStateWith is the grade's state with the examples ahead of the card: one line each
// (heading, PATHS, KIND, label), then GradeState(brief) whole. No examples is GradeState.
func GradeStateWith(brief string, shots []Example) string {
	if len(shots) == 0 {
		return GradeState(brief)
	}
	var b strings.Builder
	b.WriteString("EXAMPLES (landed cards of the record, each with the tier its outcome shows: flash landed on flash within two attempts, pro needed pro, heavy needed heavy):\n")
	for _, e := range shots {
		fmt.Fprintf(&b, "\nEXAMPLE %s | PATHS: %s | KIND: %s | LABEL: %s\n", e.Heading, e.Paths, e.Kind, e.Label)
	}
	b.WriteString("\n")
	b.WriteString(GradeState(brief))
	return b.String()
}
