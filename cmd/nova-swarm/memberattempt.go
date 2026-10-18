package main

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
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
		if err := decide.ParseAttemptReader.Check(&d); err != nil {
			return "", nil, err
		}
		dec, err := decide.AttemptDecided(d)
		if err != nil {
			return "", nil, err
		}
		wire := decisionWire(&d)
		if wire == nil {
			return "", nil, errors.New("DECISION: cannot build the decision table wire")
		}
		raw := []byte(base64.StdEncoding.EncodeToString(wire))
		return dec.String(), raw, nil
	}
}

// decisionWire builds the decision's binary wire from the Go record, the
// inverse of pkg/decide's decisionFromTable (pkg/decide/dualread.go). The
// value's bounds are the reader's (decide.ParseAttemptReader, checked before
// any byte is written); the writer checks none of its own. The variable-table
// text form is the padded standard base64 of this wire (the owner's rule S2).
func decisionWire(d *decide.Decision) []byte {
	var b decidewire.DecisionBuilder
	if !b.Init() {
		return nil
	}
	defer b.Shutdown()
	r := b.GetRoot()
	setS(&b.Main, &r.Id, d.ID)
	setS(&b.Main, &r.DecisionName, d.Decision)
	setS(&b.Main, &r.Schema, d.Schema)
	setS(&b.Main, &r.Backend, d.Backend)
	setS(&b.Main, &r.At, d.At)
	setS(&b.Main, &r.State, d.State)
	r.Usage = decidewire.UsageRow{InputTokens: int64(d.Usage.InputTokens), OutputTokens: int64(d.Usage.OutputTokens)}
	for _, k := range sortedKeys(d.Inputs) {
		in := decidewire.DecisionInputsAdd(&b.Main, &r.Inputs)
		setS(&b.Main, &in.Key, k)
		setS(&b.Main, &in.Value, d.Inputs[k])
	}
	for _, q := range sortedKeys(d.Answers) {
		a := d.Answers[q]
		na := decidewire.DecisionAnswersAdd(&b.Main, &r.Answers)
		setS(&b.Main, &na.Question, q)
		if a.Type == decide.Noul {
			na.Answer.Kind = decidewire.AnswerTypeNoul
		} else {
			na.Answer.Kind = decidewire.AnswerTypeChoice
		}
		setS(&b.Main, &na.Answer.Value, a.Value)
		na.Answer.Confidence = a.Confidence
		setS(&b.Main, &na.Answer.Method, a.Method)
		for _, opt := range sortedKeys(a.P) {
			o := decidewire.AnswerPAdd(&b.Main, &na.Answer.P)
			setS(&b.Main, &o.Option, opt)
			o.P = a.P[opt]
		}
	}
	if !b.Lock() {
		return nil
	}
	root := b.AsConst()
	n := decidewire.DecisionMeasure(root)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if decidewire.DecisionSave(root, buf) != n {
		return nil
	}
	return buf
}

// setS places text in a string slot, leaving it null when empty.
func setS(w *decidewire.TableWorker, slot *int64, text string) {
	if text != "" {
		decidewire.TableStringEmplace(w, slot, []byte(text))
	}
}

// sortedKeys is a map's keys in order, so an image is the same on every run.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
