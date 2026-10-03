package decide

import (
	"fmt"
	"slices"
	"strings"
)

// Calibration (SPEC-NOVA-DECIDE section 5) is how well one answer of one
// decision separates the outcomes, read from the record: the decisions of that
// name and schema whose outcome is known, scored by the probability the answer
// gave to one option (a noul's "yes", or a choice's named option), split into
// positives (the outcomes the answer should catch) and negatives.
type Calibration struct {
	Decision, Schema, Question, Option string
	Positives, Negatives               []float64 // the score of each labelled decision
	Skipped                            int       // labelled otherwise, unlabelled, or asked under another schema
}

// Bar is one threshold: decisions scoring at or above it are flagged.
type Bar struct {
	At              float64
	Caught, Bounced int // positives flagged; negatives flagged
}

// Calibrate scores every decision named decision whose outcome label is in
// positive or negative. The schema is the newest one the record holds for that
// decision: answers to other questions are never pooled. question is
// "<name>" (a noul, scored by its yes) or "<name>=<option>" (a choice).
func Calibrate(ds []Decision, decision, question string, positive, negative []string) (Calibration, error) {
	name, option, isChoice := strings.Cut(question, "=")
	c := Calibration{Decision: decision, Question: name, Option: "yes"}
	if isChoice {
		c.Option = option
	}
	for i := len(ds) - 1; i >= 0 && c.Schema == ""; i-- {
		if ds[i].Decision == decision {
			c.Schema = ds[i].Schema
		}
	}
	if c.Schema == "" {
		return c, fmt.Errorf("the record holds no decision named %s", decision)
	}
	for _, d := range ds {
		if d.Decision != decision {
			continue
		}
		a, asked := d.Answers[name]
		switch {
		case d.Schema != c.Schema || d.Outcome == nil:
			c.Skipped++
		case !asked:
			return c, fmt.Errorf("decision %s asked no question %s; its questions are in the schema %s", d.ID, name, d.Schema)
		case isChoice && a.Type != Choice, !isChoice && a.Type != Noul:
			return c, fmt.Errorf("%s is a %s; score a noul as <name> and a choice as <name>=<option>", name, a.Type)
		case slices.Contains(positive, d.Outcome.Label):
			c.Positives = append(c.Positives, a.Prob(c.Option))
		case slices.Contains(negative, d.Outcome.Label):
			c.Negatives = append(c.Negatives, a.Prob(c.Option))
		default:
			c.Skipped++
		}
	}
	if len(c.Positives) == 0 || len(c.Negatives) == 0 {
		return c, fmt.Errorf("%d positive and %d negative outcomes of %s; a calibration wants at least one of each", len(c.Positives), len(c.Negatives), decision)
	}
	return c, nil
}

// AUC is the probability that a positive scores above a negative (ties count
// half): 0.5 is no separation, 1 is perfect.
func (c Calibration) AUC() float64 {
	var wins float64
	for _, p := range c.Positives {
		for _, n := range c.Negatives {
			switch {
			case p > n:
				wins++
			case p == n:
				wins += 0.5
			}
		}
	}
	return wins / float64(len(c.Positives)*len(c.Negatives))
}

// At is the threshold row at bar.
func (c Calibration) At(bar float64) Bar {
	b := Bar{At: bar}
	for _, p := range c.Positives {
		if p >= bar {
			b.Caught++
		}
	}
	for _, n := range c.Negatives {
		if n >= bar {
			b.Bounced++
		}
	}
	return b
}

// CatchAll is the highest bar that still flags every positive (the lowest
// positive score) and the row there: the bar the record supports when no
// positive may pass.
func (c Calibration) CatchAll() Bar { return c.At(slices.Min(c.Positives)) }
