// Package decide is a decision system that is trained from its own record
// (docs/SPEC-NOVA-DECIDE.md). It has two halves over one record:
//
//   - the decide half (this file, backend.go, jev.go, read.go): a decision is a
//     named schema (a set of typed questions) asked over one state text through a
//     backend; every answer carries its probabilities.
//   - the train half (record.go, calibrate.go): every decision made is appended
//     to the record with its inputs, answers and usage; an outcome (a review's
//     label, a gate's result) is attached to it when it is known; the bar a
//     decision's answers are trusted at is calibrated from the decisions whose
//     outcome is known.
//
// A backend is a transport behind an interface: Jev (TypeSafe's System One
// model) is the first, and Fixed (answers from a file) is the one that needs no
// network. A decision never dials: the Jev backend sends through a Send function
// the caller injects, and HTTPSend (jev.go), the real one, is the only code here
// that opens a socket.
package decide

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Question types (SPEC-NOVA-DECIDE section 2). A choice picks one of named
// options and carries a probability per option; a noul is a yes/no over one
// statement and carries the probability that the statement is true.
const (
	Choice = "choice"
	Noul   = "noul"
)

// Question is one typed question of a schema.
type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"` // choice only: option -> what it means
}

// Schema is a named decision: the questions asked, by name, over one state.
type Schema struct {
	Name      string              `json:"name"`
	Questions map[string]Question `json:"questions"`
}

// Answer is one typed answer. P is the probability of each option for a
// choice, and of "yes" for a noul; Value is the chosen option, or "yes" or "no"
// at 0.5 for a noul.
type Answer struct {
	Type  string             `json:"type"`
	Value string             `json:"value"`
	P     map[string]float64 `json:"p"`
}

// Prob is the probability the answer gives to option (for a noul, "yes").
func (a Answer) Prob(option string) float64 { return a.P[option] }

// Usage is what one ask spent, as the backend reported it; zero is unreported.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Backend answers a schema over a state. It is the transport of a decision:
// the system above it (the record, the calibration) is the same whichever
// backend answered.
type Backend interface {
	Name() string
	Ask(ctx context.Context, s Schema, state string) (map[string]Answer, Usage, error)
}

// ParseSchema reads a schema and names every problem in it at once: a schema
// with no name or no questions, a question of an unknown type, a choice with
// fewer than two options, a noul carrying options, an empty statement.
func ParseSchema(raw []byte) (Schema, error) {
	var s Schema
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Schema{}, fmt.Errorf("the schema is not JSON of the shape {name, questions: {<name>: {type, instructions, criteria}}}: %w", err)
	}
	if problems := s.Problems(); len(problems) > 0 {
		return Schema{}, fmt.Errorf("the schema %q: %s", s.Name, strings.Join(problems, "; "))
	}
	return s, nil
}

// Problems is every reason the schema cannot be asked.
func (s Schema) Problems() []string {
	var p []string
	if strings.TrimSpace(s.Name) == "" {
		p = append(p, "it has no name; a decision is named so its record can be calibrated")
	}
	if len(s.Questions) == 0 {
		p = append(p, "it has no questions")
	}
	for _, name := range slices.Sorted(maps.Keys(s.Questions)) {
		q := s.Questions[name]
		switch {
		case strings.TrimSpace(q.Instructions) == "":
			p = append(p, fmt.Sprintf("question %s has no instructions", name))
		case q.Type == Choice && len(q.Criteria) < 2:
			p = append(p, fmt.Sprintf("choice %s names %d options; it wants at least two in criteria", name, len(q.Criteria)))
		case q.Type == Noul && len(q.Criteria) > 0:
			p = append(p, fmt.Sprintf("noul %s carries criteria; a noul is one statement, yes or no", name))
		case q.Type != Choice && q.Type != Noul:
			p = append(p, fmt.Sprintf("question %s has type %q; it wants choice or noul", name, q.Type))
		}
	}
	return p
}

// Hash is the schema's identity in the record: two decisions are calibrated
// together only when they asked the same questions.
func (s Schema) Hash() string { return hashJSON(s) }

// Check holds a backend's answers to the schema: one answer per question, of
// its type, a choice's value one of its options, every probability in [0, 1].
// A backend that answers something else is refused, never repaired.
func (s Schema) Check(answers map[string]Answer) error {
	var p []string
	for _, name := range slices.Sorted(maps.Keys(s.Questions)) {
		q, a, ok := s.Questions[name], Answer{}, false
		if a, ok = answers[name]; !ok {
			p = append(p, fmt.Sprintf("no answer to %s", name))
			continue
		}
		if a.Type != q.Type {
			p = append(p, fmt.Sprintf("%s answered as %s, asked as %s", name, a.Type, q.Type))
		}
		if _, known := q.Criteria[a.Value]; q.Type == Choice && !known {
			p = append(p, fmt.Sprintf("%s chose %q, not one of its options", name, a.Value))
		}
		if _, given := a.P[a.Value]; q.Type == Choice && !given {
			p = append(p, fmt.Sprintf("%s gives its choice %q no probability", name, a.Value))
		}
		if _, given := a.P["yes"]; q.Type == Noul && (!given || len(a.P) != 1) {
			p = append(p, fmt.Sprintf("%s is a noul and gives no probability of yes alone", name))
		}
		for opt := range a.P {
			if _, known := q.Criteria[opt]; q.Type == Choice && !known {
				p = append(p, fmt.Sprintf("%s gives a probability to %q, not one of its options", name, opt))
			}
		}
		for opt, v := range a.P {
			if v < 0 || v > 1 {
				p = append(p, fmt.Sprintf("%s gives %s the probability %v, outside [0, 1]", name, opt, v))
			}
		}
	}
	for name := range answers {
		if _, asked := s.Questions[name]; !asked {
			p = append(p, fmt.Sprintf("an answer to %s, which was not asked", name))
		}
	}
	if len(p) > 0 {
		slices.Sort(p)
		return fmt.Errorf("the backend's answers do not fit the schema: %s", strings.Join(p, "; "))
	}
	return nil
}

// Ask asks the schema over the state through the backend and returns the
// answers held to the schema (Check).
func Ask(ctx context.Context, b Backend, s Schema, state string) (map[string]Answer, Usage, error) {
	if strings.TrimSpace(state) == "" {
		return nil, Usage{}, fmt.Errorf("the state is empty; a decision is made over evidence")
	}
	answers, usage, err := b.Ask(ctx, s, state)
	if err != nil {
		return nil, usage, err
	}
	if err := s.Check(answers); err != nil {
		return nil, usage, err
	}
	return answers, usage, nil
}

// noulAnswer is the answer a noul probability makes.
func noulAnswer(p float64) Answer {
	v := "no"
	if p >= 0.5 {
		v = "yes"
	}
	return Answer{Type: Noul, Value: v, P: map[string]float64{"yes": p}}
}

// Sum is the hex SHA-256 of b: how the record names an input without holding
// a second copy of a file.
func Sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func hashJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil { // ignored: a Schema always marshals; an empty hash is never a match
		return ""
	}
	return Sum(raw)[:16]
}
