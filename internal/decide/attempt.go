package decide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The attempt decision (SPEC-NOVA-DECIDE section 9): after a work take ends, how it ended,
// read from the card's brief, the child's RESULT.md and the member's reason line. Its one
// question is a choice of six classes, each with a probability; the sprint routes a failed
// finish by two of them, no-result and nothing-to-do, each when its probability is at or
// above its own bar on the sprint row (decide_attempt_no_result, decide_attempt_nothing_to_do),
// and by the reason line's prefix otherwise (docs/SPEC-SPRINT.md section 2).

// AttemptName is the attempt decision's name in the record.
const AttemptName = "attempt"

// AttemptQuestion is the attempt decision's one question.
const AttemptQuestion = "class"

// The attempt's classes.
const (
	ClassDone            = "done"
	ClassNothingToDo     = "nothing-to-do"
	ClassWrongScope      = "wrong-scope"
	ClassNoResult        = "no-result"
	ClassNeedsPro        = "needs-pro"
	ClassProviderFailure = "provider-failure"
)

// AttemptSchema is the attempt's question: the class of the take's end.
func AttemptSchema() Schema {
	return Schema{Name: AttemptName, Questions: map[string]Question{
		AttemptQuestion: {Type: Choice, Instructions: "How this take of the CARD ended, read from the child's RESULT and the member's REASON line.",
			Criteria: map[string]string{
				ClassDone:            "the take did the card's task: the work is committed and the gate the card names ran green",
				ClassNothingToDo:     "the card's task was already done at its base, or asks for nothing that can change: there was nothing to do",
				ClassWrongScope:      "the card cannot be done as written: the change it asks for lies outside its PATHS, or the card asks for the wrong thing; the fix is to the card, not another try",
				ClassNoResult:        "the child ended without leaving a result to judge (no RESULT.md, or a budget or deadline reached before any finding): another try may converge",
				ClassNeedsPro:        "the work was attempted and came back wrong in a way a stronger model would get right: tests red, the change incomplete or broken",
				ClassProviderFailure: "the provider or its harness failed the run (an HTTP error, a rate limit, a balance, a crash), not the work: the same card on a working route would run",
			}},
	}}
}

// AttemptState is the text the attempt is asked over: the card's brief, the child's RESULT.md
// (cut to MaxResultBytes) and the member's reason line, each under its own heading.
func AttemptState(brief, result, reason string) string {
	var b strings.Builder
	b.WriteString("CARD (the whole task the worker was given):\n")
	b.WriteString(strings.TrimRight(brief, "\n"))
	b.WriteString("\n\nRESULT (the child's RESULT.md):\n")
	if r := strings.TrimSpace(result); r == "" {
		b.WriteString("(none: the child wrote no RESULT.md)")
	} else {
		b.WriteString(strings.TrimRight(result[:min(len(result), MaxResultBytes)], "\n"))
	}
	b.WriteString("\n\nREASON (the member's line for the take's end):\n")
	b.WriteString(orNone(strings.TrimSpace(reason)))
	b.WriteString("\n")
	return b.String()
}

// MaxResultBytes bounds the RESULT.md an attempt's state carries: a result is a page of
// text, and the state rides to the sprint's server with the finish.
const MaxResultBytes = 16 << 10

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// Op is a decision's op id over a state: base (the card and what it decides of it,
// `<card>@<attempt>`, `<card>@grade`), then the first twelve hex of the state's SHA-256, so
// a finish reported again replays its decision with no ask, and a card id that comes back
// with another brief (a clear, a brief replaced) is a new decision, never a conflict.
func Op(base, state string) string { return base + "." + Sum([]byte(state))[:12] }

// AttemptOp is a take's attempt decision id: `<card>@<attempt>.<12 hex of the state>`.
// It is per attempt and state, not per take: two takes of one attempt that ended with the
// same state (the same reason line and RESULT.md) are one decision.
func AttemptOp(card string, attempt int, state string) string {
	return Op(card+"@"+strconv.Itoa(attempt), state)
}

// AttemptOf is the card and attempt an attempt decision's op id names (AttemptOp); ok false
// when it is not `<card>@<attempt>.<hex>`.
func AttemptOf(op string) (card string, attempt int, ok bool) {
	card, rest, found := strings.Cut(op, "@")
	num, _, dot := strings.Cut(rest, ".")
	n, err := strconv.Atoi(num)
	if !found || !dot || err != nil || card == "" {
		return "", 0, false
	}
	return card, n, true
}

// AttemptDecision asks the attempt decision of a take of card at attempt through b and
// returns it as the record keeps it, with its op id (AttemptOp), unrecorded: the sprint's
// member asks it, and the finish carries it to the record the sprint's server keeps
// (docs/SPEC-SPRINT.md section 2). A backend that fails is a BackendError.
func AttemptDecision(ctx context.Context, b Backend, card string, attempt int, brief, result, reason string, at time.Time) (Decision, error) {
	state := AttemptState(brief, result, reason)
	answers, usage, err := Ask(ctx, b, AttemptSchema(), state)
	if err != nil {
		return Decision{}, &BackendError{Backend: b.Name(), Err: err}
	}
	s := AttemptSchema()
	inputs := map[string]string{"brief_sha256": Sum([]byte(brief)), "result_sha256": Sum([]byte(result)), "reason": reason}
	return Decision{ID: AttemptOp(card, attempt, state), Decision: s.Name, Schema: s.Hash(), Backend: b.Name(),
		At: at.UTC().Format(time.RFC3339), Inputs: inputs, State: state, Answers: answers, Usage: usage}, nil
}

// ParseAttempt reads an attempt decision as a finish carries it (one JSON record line's
// decision) and holds it to the attempt schema: its name, its schema's hash, its answers
// (Check) and its op id over its state (AttemptOp). A decision that does not fit is an error,
// and the server records nothing of it.
func ParseAttempt(raw []byte) (Decision, error) {
	var d Decision
	if err := json.Unmarshal(raw, &d); err != nil {
		return Decision{}, fmt.Errorf("the attempt decision is not JSON of a record's decision: %w", err)
	}
	s := AttemptSchema()
	var p []string
	if d.Decision != s.Name || d.Schema != s.Hash() {
		p = append(p, fmt.Sprintf("it is decision %q under schema %s, not the attempt decision under %s", d.Decision, d.Schema, s.Hash()))
	}
	if err := s.Check(d.Answers); err != nil {
		p = append(p, err.Error())
	}
	if card, n, ok := AttemptOf(d.ID); !ok || d.ID != AttemptOp(card, n, d.State) {
		p = append(p, fmt.Sprintf("its id %q is not <card>@<attempt>.<12 hex of its state>", d.ID))
	}
	if len(p) > 0 {
		return Decision{}, errors.New(strings.Join(p, "; "))
	}
	d.Outcome = nil
	return d, nil
}

// Top is a choice's chosen option and its probability.
func Top(d Decision, question string) (string, float64) {
	a := d.Answers[question]
	return a.Value, a.Prob(a.Value)
}

// Decided is a decision as it rides on a card: the chosen option, its probability and the
// decision's op id, one line: `<option> p=<p> op=<op>`.
type Decided struct {
	Value string
	P     float64
	Op    string
}

// String is the card's line.
func (d Decided) String() string {
	return fmt.Sprintf("%s p=%s op=%s", d.Value, strconv.FormatFloat(d.P, 'f', 3, 64), d.Op)
}

// ParseDecided reads a card's line (Decided.String); ok false when it is not one.
func ParseDecided(s string) (Decided, bool) {
	f := strings.Fields(s)
	if len(f) != 3 || !strings.HasPrefix(f[1], "p=") || !strings.HasPrefix(f[2], "op=") {
		return Decided{}, false
	}
	p, err := strconv.ParseFloat(strings.TrimPrefix(f[1], "p="), 64)
	if err != nil || p < 0 || p > 1 {
		return Decided{}, false
	}
	return Decided{Value: f[0], P: p, Op: strings.TrimPrefix(f[2], "op=")}, true
}

// ParseBar reads one bar as the sprint row holds it, a decimal as text: empty is no bar
// (ok false), and anything else that is not a probability is an error naming the field.
func ParseBar(field, raw string) (bar float64, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	v, perr := strconv.ParseFloat(raw, 64)
	switch {
	case perr != nil:
		return 0, false, fmt.Errorf("%s %q is not a decimal", field, raw)
	case v < 0 || v > 1:
		return 0, false, fmt.Errorf("%s %v is not a probability in [0, 1]", field, v)
	}
	return v, true, nil
}

// Over says the decision on a card is at or above the bar the card carries (raw, as the
// sprint row held it when the card was dealt or graded): no bar, or one that does not parse,
// is never over.
func (d Decided) Over(raw string) bool {
	bar, ok, err := ParseBar("bar", raw)
	return err == nil && ok && d.P >= bar
}

// AttemptDecided is the attempt decision as it rides with a finish: its class, the class's
// probability and its op id. An answer that is not one of the six classes is an error.
func AttemptDecided(d Decision) (Decided, error) {
	class, p := Top(d, AttemptQuestion)
	if _, known := AttemptSchema().Questions[AttemptQuestion].Criteria[class]; !known || d.Decision != AttemptName {
		return Decided{}, errors.New("the decision is not an attempt decision with a class")
	}
	return Decided{Value: class, P: p, Op: d.ID}, nil
}

// The attempt's outcome labels, attached when its card lands or is dropped
// (docs/SPEC-SPRINT.md section 2, the attempt decision): the card landed at the decided
// attempt; at a later attempt, on the tier that landed it; or it was dropped.
const (
	LabelLanded      = "landed"
	LabelLaterPrefix = "later-" // later-flash, later-pro, later-script
	LabelDropped     = "dropped"
)

// AttemptLabel is the outcome of an attempt decided at attempt, for a card that landed at
// landed on tier (landed 0: dropped).
func AttemptLabel(attempt, landed int, tier string) string {
	switch {
	case landed == 0:
		return LabelDropped
	case landed == attempt:
		return LabelLanded
	}
	return LabelLaterPrefix + tier
}
