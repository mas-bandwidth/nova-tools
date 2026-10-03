package decide

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The first read of a flash card (docs/SPEC-SPRINT.md section 6, the decide read;
// SPEC-NOVA-DECIDE section 8): the read decision asked over the card and its diff,
// then routed by p(defect) against two bars the sprint's configuration holds
// (nova-config's sprint row, decide_bounce and decide_review). At or above the bounce
// bar the read bounces the work; below the review bar it lands the work with no model
// read; between the two the card goes to a strings read, whose verdict is attached to
// the decision as its outcome, so the record trains.

// The routes a first read takes.
const (
	RouteBounce  = "bounce"
	RouteStrings = "strings"
	RouteLand    = "land"
)

// Bars are the two bars on p(defect): Bounce at or above, Review below.
type Bars struct {
	Bounce float64 `json:"bounce"`
	Review float64 `json:"review"`
}

// ParseBars reads the bars as the sprint row holds them, decimals as text. Both are
// probabilities and the review bar is at most the bounce bar; every problem is named.
func ParseBars(bounce, review string) (Bars, error) {
	var b Bars
	var p []string
	for _, f := range []struct {
		name, raw string
		to        *float64
	}{{"decide_bounce", bounce, &b.Bounce}, {"decide_review", review, &b.Review}} {
		v, err := strconv.ParseFloat(strings.TrimSpace(f.raw), 64)
		switch {
		case err != nil:
			p = append(p, fmt.Sprintf("%s %q is not a decimal", f.name, f.raw))
		case v < 0 || v > 1:
			p = append(p, fmt.Sprintf("%s %v is not a probability in [0, 1]", f.name, v))
		default:
			*f.to = v
		}
	}
	if len(p) == 0 && b.Review > b.Bounce {
		p = append(p, fmt.Sprintf("decide_review %v is above decide_bounce %v; the strings read is the band between them", b.Review, b.Bounce))
	}
	if len(p) > 0 {
		return Bars{}, errors.New(strings.Join(p, "; "))
	}
	return b, nil
}

// Route is where a read with this p(defect) goes.
func (b Bars) Route(pDefect float64) string {
	switch {
	case pDefect >= b.Bounce:
		return RouteBounce
	case pDefect < b.Review:
		return RouteLand
	}
	return RouteStrings
}

// BackendError is a backend that gave no answer within the schema: nothing was recorded.
type BackendError struct {
	Backend string
	Err     error
}

func (e *BackendError) Error() string { return e.Err.Error() }
func (e *BackendError) Unwrap() error { return e.Err }

// Make asks s over state through b and appends the decision to the record under id. An
// id the record holds over the same decision, schema and state is that decision
// (existing), and nothing is asked; over another it is a ConflictError.
func Make(ctx context.Context, b Backend, s Schema, state, record, id string, inputs map[string]string, at time.Time) (d Decision, existing bool, err error) {
	ds, err := Load(record)
	if err != nil {
		return Decision{}, false, err
	}
	if have := Find(ds, id); have != nil {
		return *have, true, Replays(*have, s, state)
	}
	answers, usage, err := Ask(ctx, b, s, state)
	if err != nil {
		return Decision{}, false, &BackendError{Backend: b.Name(), Err: err}
	}
	d = Decision{ID: id, Decision: s.Name, Schema: s.Hash(), Backend: b.Name(), At: at.UTC().Format(time.RFC3339),
		Inputs: inputs, State: state, Answers: answers, Usage: usage}
	have, err := Append(record, d)
	if have != nil {
		return *have, true, err
	}
	return d, false, err
}

// Replays says the recorded decision is the one an ask of s over state under its id
// would make, so the ask is answered from the record; else a ConflictError.
func Replays(have Decision, s Schema, state string) error {
	return replays(have, s.Name, s.Hash(), state)
}

func replays(have Decision, decision, schema, state string) error {
	if have.State != state || have.Decision != decision || have.Schema != schema {
		return &ConflictError{fmt.Sprintf("the op id %s is recorded for another decision, schema or state; an op id names one operation, so choose another", have.ID)}
	}
	return nil
}

// FirstRead is the read decision over a card and its diff, recorded under id (the read
// card's id), and the route its p(defect) takes at the bars.
func FirstRead(ctx context.Context, b Backend, bars Bars, card, diff, record, id string, at time.Time) (Decision, string, error) {
	inputs := map[string]string{"card_sha256": Sum([]byte(card)), "diff_sha256": Sum([]byte(diff))}
	d, _, err := Make(ctx, b, ReadSchema(), ReadState(card, diff, ""), record, id, inputs, at)
	if err != nil {
		return Decision{}, "", err
	}
	return d, bars.Route(PDefect(d)), nil
}

// PDefect is the read's p(defect).
func PDefect(d Decision) float64 { return d.Answers["defect"].Prob("yes") }

// Finding is a first read's report: p(defect) and the bar it met, the files the diff
// changes, and the five answers. It names a file, so a bounce is a broken read with a
// finding (docs/SPEC-CARD-CONTRACT.md section 3).
func Finding(d Decision, bars Bars, files []string) string {
	var b strings.Builder
	p := PDefect(d)
	fmt.Fprintf(&b, "decide: p(defect)=%.2f", p)
	switch bars.Route(p) {
	case RouteBounce:
		fmt.Fprintf(&b, " at or above the bounce bar %.2f", bars.Bounce)
	case RouteLand:
		fmt.Fprintf(&b, " below the review bar %.2f", bars.Review)
	default:
		fmt.Fprintf(&b, " between the bars %.2f and %.2f", bars.Review, bars.Bounce)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, " over %s", strings.Join(files, ", "))
	}
	b.WriteString("; answers:")
	for _, name := range slices.Sorted(maps.Keys(d.Answers)) {
		if a := d.Answers[name]; a.Type == Noul {
			fmt.Fprintf(&b, " %s=%.2f", name, a.Prob("yes"))
		} else {
			fmt.Fprintf(&b, " %s=%s(%.2f)", name, a.Value, a.Prob(a.Value))
		}
	}
	b.WriteString(" (the decide read, docs/SPEC-SPRINT.md section 6)")
	return b.String()
}

// Settle attaches a strings read's verdict to the first read it followed: ok is LAND,
// broken is BOUNCE. Any other verdict is no outcome and attaches nothing.
func Settle(record, id, verdict, note string, at time.Time) error {
	label := map[string]string{"ok": Land, "broken": Bounce}[verdict]
	if label == "" {
		return nil
	}
	_, _, err := Attach(record, Outcome{ID: id, Label: label, Note: note, At: at.UTC().Format(time.RFC3339)})
	return err
}
