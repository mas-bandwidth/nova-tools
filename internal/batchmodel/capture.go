package batchmodel

import (
	"context"
	"fmt"
)

// Observe reads a complete physical store image. Implementations must read
// the store, not derive an observation from the request or receipt. A
// disposable Redis server with no other clients gives this multi-command
// capture a stable before/after boundary.
type Observe func(context.Context, Request) (Snapshot, error)

// ProjectionContext supplies only the non-store control facts needed for the
// model's outcome, returned, attempt, and harness step. Physical values must
// still come from the frozen Snapshot passed separately to Project.
type ProjectionContext struct {
	Index   uint64
	Phase   string // before or after
	Request Request
	Result  Result   // set only for after
	Receipt *Receipt // set only for after when returned
}

// Project maps a frozen physical snapshot plus control facts into the model's
// finite vocabulary. It uses fixed seed revision baselines and must not copy
// physical fields from the request or receipt.
type Project func(Snapshot, ProjectionContext) (ModelState, error)

// Invoke sends exactly one FCALL and returns its decoded reply. The retained
// prior receipt on an identical retry must come from the durable operation
// record, not be synthesized from the new reply.
type Invoke func(context.Context, Request) (Result, *Receipt, *Receipt, error)

// CaptureStep reads before, invokes once, and reads after. It freezes all
// mutable maps/slices so later decoder reuse or mutation cannot silently edit
// the evidence against which a receipt is checked.
func CaptureStep(ctx context.Context, index uint64, q Request, action BatchAction, tableBaseline string, memberBaseline map[string]string, observe Observe, project Project, invoke Invoke) (Step, error) {
	if observe == nil || project == nil || invoke == nil {
		return Step{}, fmt.Errorf("capture needs observe, project, and invoke")
	}
	before, err := observe(ctx, q)
	if err != nil {
		return Step{}, fmt.Errorf("capture before: %w", err)
	}
	before = cloneSnapshot(before)
	beforeModel, err := project(cloneSnapshot(before), ProjectionContext{Index: index, Phase: "before", Request: cloneRequest(q)})
	if err != nil {
		return Step{}, fmt.Errorf("project before: %w", err)
	}
	beforeModel = cloneModelState(beforeModel)
	result, receipt, prior, err := invoke(ctx, q)
	if err != nil {
		return Step{}, fmt.Errorf("invoke: %w", err)
	}
	receipt = cloneReceipt(receipt)
	prior = cloneReceipt(prior)
	after, err := observe(ctx, q)
	if err != nil {
		return Step{}, fmt.Errorf("capture after: %w", err)
	}
	after = cloneSnapshot(after)
	afterModel, err := project(cloneSnapshot(after), ProjectionContext{Index: index, Phase: "after", Request: cloneRequest(q), Result: result, Receipt: cloneReceipt(receipt)})
	if err != nil {
		return Step{}, fmt.Errorf("project after: %w", err)
	}
	afterModel = cloneModelState(afterModel)
	copyBaseline := map[string]string{}
	for k, v := range memberBaseline {
		copyBaseline[k] = v
	}
	step := Step{Index: index, Request: cloneRequest(q), Result: result, Before: before, After: after, Receipt: receipt, PriorReceipt: prior,
		BeforeModel: beforeModel, AfterModel: afterModel, Action: cloneAction(action), TableBaseline: tableBaseline, MemberBaseline: copyBaseline}
	if err := ValidateStep(step); err != nil {
		return Step{}, err
	}
	return step, nil
}

func cloneRequest(q Request) Request {
	q.Canonical = append([]byte(nil), q.Canonical...)
	q.Selected = append([]string(nil), q.Selected...)
	q.Entries = cloneAction(BatchAction{Members: q.Entries}).Members
	if q.ExpectedRevision != nil {
		m := make(map[string]*string, len(q.ExpectedRevision))
		for k, v := range q.ExpectedRevision {
			if v == nil {
				m[k] = nil
			} else {
				x := *v
				m[k] = &x
			}
		}
		q.ExpectedRevision = m
	}
	return q
}
func cloneAction(a BatchAction) BatchAction {
	a.Canonical = append([]byte(nil), a.Canonical...)
	a.Members = append([]MemberAction(nil), a.Members...)
	for i := range a.Members {
		a.Members[i].GuardValues = append([]string(nil), a.Members[i].GuardValues...)
		if a.Members[i].Revision != nil {
			v := *a.Members[i].Revision
			a.Members[i].Revision = &v
		}
	}
	return a
}
func cloneMember(m Member) Member {
	if m.Place != nil {
		p := *m.Place
		m.Place = &p
	}
	if m.Fields != nil {
		f := make(map[string]string, len(m.Fields))
		for k, v := range m.Fields {
			f[k] = v
		}
		m.Fields = f
	}
	return m
}
func cloneReceipt(r *Receipt) *Receipt {
	if r == nil {
		return nil
	}
	out := *r
	out.Members = append([]Delta(nil), r.Members...)
	for i := range out.Members {
		out.Members[i].Before = cloneMember(out.Members[i].Before)
		out.Members[i].After = cloneMember(out.Members[i].After)
	}
	return &out
}
func cloneSnapshot(s Snapshot) Snapshot {
	if s.Image != nil {
		m := make(map[string][]byte, len(s.Image))
		for k, v := range s.Image {
			m[k] = append([]byte(nil), v...)
		}
		s.Image = m
	}
	if s.Members != nil {
		m := make(map[string]Member, len(s.Members))
		for k, v := range s.Members {
			m[k] = cloneMember(v)
		}
		s.Members = m
	}
	if s.Recorded != nil {
		r := *s.Recorded
		r.Canonical = append([]byte(nil), r.Canonical...)
		s.Recorded = &r
	}
	s.LastReceipt = cloneReceipt(s.LastReceipt)
	return s
}

func cloneModelState(s ModelState) ModelState {
	for i := range s.Values {
		s.Values[i] = cloneExpr(s.Values[i])
	}
	return s
}
func cloneExpr(e Expr) Expr {
	e.items = append([]Expr(nil), e.items...)
	for i := range e.items {
		e.items[i] = cloneExpr(e.items[i])
	}
	e.keys = append([]Expr(nil), e.keys...)
	for i := range e.keys {
		e.keys[i] = cloneExpr(e.keys[i])
	}
	e.values = append([]Expr(nil), e.values...)
	for i := range e.values {
		e.values[i] = cloneExpr(e.values[i])
	}
	if e.fields != nil {
		m := make(map[string]Expr, len(e.fields))
		for k, v := range e.fields {
			m[k] = cloneExpr(v)
		}
		e.fields = m
	}
	return e
}
