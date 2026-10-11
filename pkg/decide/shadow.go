package decide

import (
	"context"
	"slices"
	"time"
)

// The shadow of the judgment decision (SPEC-NOVA-DECIDE section 13, jev-shadow-judgments.w1):
// Jev is asked the judgment decision for every judgment as it is raised, and the answer is
// recorded beside the coordinator's real one with every answer marked shadow. A shadow
// record is never applied: it has no act, and nothing reads it but ShadowAgreement.

const (
	// ShadowName is the shadow records' decision name; nova-sprint run --decide keeps them in
	// <dir>/judgment-shadow.jsonl.
	ShadowName = "judgment-shadow"
	// ShadowMethod marks each answer of a shadow record.
	ShadowMethod = "shadow"
	// HighP is the probability at and above which agreement is also counted.
	HighP = 0.95
)

// ShadowKey joins a shadow record to the real answer: the judgment's note and its card.
func ShadowKey(d Decision) string { return d.Inputs["note"] + "|" + d.Inputs["card"] }

// ShadowAsk asks the judgment decision over in through b and appends the answer, marked
// shadow, to record under an id per note, card and state. A judgment already shadowed is
// returned as recorded (existing) and nothing is asked.
func ShadowAsk(ctx context.Context, b Backend, in JudgmentInput, note, record string, at time.Time) (d Decision, existing bool, err error) {
	s, state := JudgmentSchema(), JudgmentState(in)
	s.Name = ShadowName
	id := Op(in.Card+"@shadow."+note, state)
	ds, err := Load(record)
	if err != nil {
		return Decision{}, false, err
	}
	if have := Find(ds, id); have != nil {
		return *have, true, nil
	}
	answers, usage, err := Ask(ctx, b, s, state)
	if err != nil {
		return Decision{}, false, &BackendError{Backend: b.Name(), Err: err}
	}
	for q, a := range answers {
		a.Method = ShadowMethod
		answers[q] = a
	}
	d = Decision{ID: id, Decision: ShadowName, Schema: s.Hash(), Backend: b.Name(), At: at.UTC().Format(time.RFC3339), State: state, Answers: answers, Usage: usage,
		Inputs: map[string]string{"card": in.Card, "note": note, "kind": in.Kind, "verb": answers["verb"].Value, "shadow": "true"}}
	have, err := Append(record, d)
	if have != nil {
		return *have, true, err
	}
	return d, false, err
}

// KindAgreement is how a judgment kind's shadow answers agreed with the real ones: Count
// pairs joined, Agree of them the same verb, Pct that as a percent; HighCount of the pairs
// had the shadow verb at p HighP or above, HighAgree of those agreed, HighPct as a percent.
type KindAgreement struct {
	Kind      string
	Count     int
	Agree     int
	Pct       int
	HighCount int
	HighAgree int
	HighPct   int
}

// ShadowAgreement joins each shadow record to the real judgment-answer record of the same
// note and card and scores the pairs per judgment kind, kinds in name order. A real answer is
// the first for its note and card; verbs are compared as recorded.
func ShadowAgreement(shadows, real []Decision) []KindAgreement {
	answered := map[string]string{}
	for _, r := range real {
		if k := ShadowKey(r); answered[k] == "" {
			answered[k] = r.Answers["verb"].Value
		}
	}
	byKind := map[string]*KindAgreement{}
	for _, s := range shadows {
		verb, ok := answered[ShadowKey(s)]
		if !ok {
			continue
		}
		k := s.Inputs["kind"]
		a := byKind[k]
		if a == nil {
			a = &KindAgreement{Kind: k}
			byKind[k] = a
		}
		v := s.Answers["verb"]
		agree := v.Value == verb
		a.Count++
		if agree {
			a.Agree++
		}
		if v.Prob(v.Value) >= HighP {
			a.HighCount++
			if agree {
				a.HighAgree++
			}
		}
	}
	out := make([]KindAgreement, 0, len(byKind))
	for _, a := range byKind {
		a.Pct, a.HighPct = percent(a.Agree, a.Count), percent(a.HighAgree, a.HighCount)
		out = append(out, *a)
	}
	slices.SortFunc(out, func(x, y KindAgreement) int {
		switch {
		case x.Kind < y.Kind:
			return -1
		case x.Kind > y.Kind:
			return 1
		}
		return 0
	})
	return out
}

func percent(n, of int) int {
	if of == 0 {
		return 0
	}
	return (100*n + of/2) / of
}

// ShadowPending is how many shadow records have no real answer yet.
func ShadowPending(shadows, real []Decision) int {
	answered := map[string]bool{}
	for _, r := range real {
		answered[ShadowKey(r)] = true
	}
	n := 0
	for _, s := range shadows {
		if !answered[ShadowKey(s)] {
			n++
		}
	}
	return n
}
