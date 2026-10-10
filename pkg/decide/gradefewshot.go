package decide

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

// ParseExamples reads an examples file, one JSON object per line ({card, heading, paths, kind,
// label}); every bad line is named in one error.
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
		var e Example
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		switch err := dec.Decode(&e); {
		case err != nil:
			problems = append(problems, fmt.Sprintf("line %d: %v", n, err))
		case e.Card == "" || e.Heading == "":
			problems = append(problems, fmt.Sprintf("line %d: an example names its card and its heading", n))
		case !slices.Contains(GradeExampleClasses, e.Label):
			problems = append(problems, fmt.Sprintf("line %d: label %q is none of %s", n, e.Label, strings.Join(GradeExampleClasses, ", ")))
		default:
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("the examples file has %d bad lines: %s", len(problems), strings.Join(problems, "; "))
	}
	return out, nil
}

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
