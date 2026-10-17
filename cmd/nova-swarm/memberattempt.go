package main

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// THE ATTEMPT DECISION (docs/SPEC-SPRINT.md section 2; pkg/decide, attempt.go). A work
// member whose environment holds JEV_API_KEY (the loop row's nova-secrets keys) asks, when
// any take ends, how it ended (whether the class routes the finish is the server's to say,
// by the card's bars): the member's own process asks it, in the end's long work beside the
// push, and never hands the key to native or the child (nativeChildEnv removes it). The
// finish carries the decision.

// attemptWait bounds the backend's answer, as the decide read's does (decideWait).
const attemptWait = decideWait

// attemptDecider is the member's attempt decision over b, stamped by now: the decision's
// card line and its record line, or the backend's failure (the finish then goes by its
// reason line alone).
func attemptDecider(b decide.Backend, now func() time.Time) func(member.Packet, string, string) (string, []byte, error) {
	return func(p member.Packet, result, reason string) (string, []byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), attemptWait)
		defer cancel()
		d, err := decide.AttemptDecision(ctx, b, p.Primary, p.Attempt, p.Brief, result, reason, now())
		if err != nil {
			return "", nil, err
		}
		dec, err := decide.AttemptDecided(d)
		if err != nil {
			return "", nil, err
		}
		if err := attemptWriter.Check(&d); err != nil {
			return "", nil, err
		}
		wire := attemptWire(&d)
		if wire == nil {
			return "", nil, errors.New("DECISION: no wire writer")
		}
		raw := []byte(base64.StdEncoding.EncodeToString(wire))
		return dec.String(), raw, nil
	}
}

// The attempt decision is written in the table's variable form (the owner's
// rules S1 and S2): the padded standard base64 of the binary image of the
// generated decide table (pkg/decidewire) the dual reader reads. The writer never
// falls back to the legacy JSON form. A value the table cannot hold is refused
// by name (schemaread) before a byte is written; the reader keeps reading the
// legacy form until the retire card (S4).

// attemptWriter holds the attempt decision's write-side bound (schemaread): the
// same constraints the dual reader enforces, run before a byte is written.
var attemptWriter = schemaread.NewReader(&schemaread.Codec[decide.Decision]{
	Name:        "DECISION",
	Help:        "nova-sprint answer -h",
	Reset:       func(v *decide.Decision) { *v = decide.Decision{} },
	Constraints: attemptCheck,
})

// attemptWire is the attempt decision's binary image, built from the record in
// the generated decide table the dual reader reads. A field the table does not
// declare is not written.
func attemptWire(d *decide.Decision) []byte {
	var b decidewire.DecisionBuilder
	if !b.Init() {
		return nil
	}
	defer b.Shutdown()
	r := b.GetRoot()
	setText(&b.Main, &r.Id, d.ID)
	setText(&b.Main, &r.DecisionName, d.Decision)
	setText(&b.Main, &r.Schema, d.Schema)
	setText(&b.Main, &r.Backend, d.Backend)
	setText(&b.Main, &r.At, d.At)
	setText(&b.Main, &r.State, d.State)
	r.Usage = decidewire.UsageRow{InputTokens: int64(d.Usage.InputTokens), OutputTokens: int64(d.Usage.OutputTokens)}
	for _, k := range sortedKeys(d.Inputs) {
		in := decidewire.DecisionInputsAdd(&b.Main, &r.Inputs)
		setText(&b.Main, &in.Key, k)
		setText(&b.Main, &in.Value, d.Inputs[k])
	}
	for _, q := range sortedKeys(d.Answers) {
		a := d.Answers[q]
		na := decidewire.DecisionAnswersAdd(&b.Main, &r.Answers)
		setText(&b.Main, &na.Question, q)
		na.Answer.Kind = decidewire.AnswerTypeChoice
		setText(&b.Main, &na.Answer.Value, a.Value)
		for _, opt := range sortedKeys(a.P) {
			o := decidewire.AnswerPAdd(&b.Main, &na.Answer.P)
			setText(&b.Main, &o.Option, opt)
			o.P = a.P[opt]
		}
	}
	if !b.Lock() {
		return nil
	}
	root := b.AsConst()
	n := decidewire.DecisionMeasure(root)
	buf := make([]byte, n)
	decidewire.DecisionSave(root, buf)
	return buf
}

// setText writes the text of a table row field (S2, the table's text form); a
// zero slot stays absent.
func setText(w *decidewire.TableWorker, slot *int64, s string) {
	if s != "" {
		decidewire.TableStringEmplace(w, slot, []byte(s))
	}
}

// sortedKeys returns a map's keys in order, so a table image is the same on
// every run.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// probability holds a probability to [0, 1] (S2, the schema's declared bound,
// and the reader's decisionCheck): a value outside is refused by name before a
// byte is written, and a NaN is malformed.
func probability(c *schemaread.Checker, field string, p float64) {
	if math.IsNaN(p) {
		c.Text(field, "\x00", 1)
		return
	}
	in := int64(0)
	if p >= 0 && p <= 1 {
		in = 1
	}
	c.Bound(field, in, 1, 1)
}

// attemptCheck holds an attempt decision to the bounds its code enforces: every
// probability in [0, 1] (decide.go Check, record.go recordedProbabilities).
func attemptCheck(d *decide.Decision, c *schemaread.Checker) {
	for _, a := range d.Answers {
		for _, p := range a.P {
			probability(c, "p", p)
		}
	}
}

// workAttempt is a work member's attempt decider: Jev over its real transport with the key
// JEV_API_KEY holds; nil for a reader, and with no key (the finish goes by its reason line).
func workAttempt(reader bool, getenv func(string) string) func(member.Packet, string, string) (string, []byte, error) {
	key := getenv(decide.JevSecret)
	if reader || key == "" {
		return nil
	}
	return attemptDecider(decide.JevHTTP(key, decide.JevTimeout), time.Now)
}
