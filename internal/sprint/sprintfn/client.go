package sprintfn

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Client carries the sprint's calls: one flush of a pipeline of steps, reads
// and pages, with one result for each item, in order (8.0; errata E3). Redis
// and Twin implement it.
//
// A Client never retries. A step whose reply is lost comes back with an
// *OutcomeUnknownError holding the exact bytes it sent; whether it applied is
// the caller's to settle, by its op's done record, the same bytes again, or a
// fence (L1 5, 8). A malformed item refuses the whole pipeline before anything
// is sent, and the error is a *Refusal naming the item (L1 1.5: encoding
// failures are found before dispatch).
type Client interface {
	Pipeline(ctx context.Context, items []Item) ([]Result, error)
}

// ItemError is a pipeline refused before dispatch because one item is
// malformed: nothing was sent and nothing was run.
type ItemError struct {
	Index   int
	Refusal *Refusal
}

// Error names the item and its refusal.
func (e *ItemError) Error() string { return fmt.Sprintf("item %d: %v", e.Index, e.Refusal) }

// Unwrap is the refusal, so errors.As finds a *Refusal.
func (e *ItemError) Unwrap() error { return e.Refusal }

// OutcomeUnknownError is a step that was dispatched and whose reply was lost
// or unreadable (L1 8, OUTCOMEUNKNOWN). Step and Sprint are the exact bytes
// of its two halves, for a done lookup or a resend of the same bytes. The
// error's text carries no request data.
type OutcomeUnknownError struct {
	Step, Sprint []byte
	Cause        error
}

// Error says the outcome is unknown, and why.
func (e *OutcomeUnknownError) Error() string {
	if e.Cause == nil {
		return tset.ErrOutcomeUnknown.Error()
	}
	return tset.ErrOutcomeUnknown.Error() + ": " + e.Cause.Error()
}

// Unwrap is the transport's error.
func (e *OutcomeUnknownError) Unwrap() error { return e.Cause }

// Is makes errors.Is(err, tset.ErrOutcomeUnknown) hold, as Layer 1's own
// unknown outcome does.
func (e *OutcomeUnknownError) Is(target error) bool { return target == tset.ErrOutcomeUnknown }

// Step sends one request and returns its one result. It calls the client
// once: nothing here resends, whatever comes back.
func Step(ctx context.Context, c Client, req *Request) (Result, error) {
	results, err := c.Pipeline(ctx, []Item{{Step: req}})
	if err != nil {
		return Result{}, err
	}
	if len(results) != 1 {
		return Result{}, fmt.Errorf("sprintfn: a pipeline of one returned %d results", len(results))
	}
	return results[0], nil
}

// Steps pipelines requests in one flush and returns their results aligned
// with them. Each step is atomic alone; the pipeline is not (L1 1.5).
func Steps(ctx context.Context, c Client, reqs []*Request) ([]Result, error) {
	items := make([]Item, len(reqs))
	for i, req := range reqs {
		items[i] = Item{Step: req}
	}
	results, err := c.Pipeline(ctx, items)
	if err != nil {
		return nil, err
	}
	if len(results) != len(items) {
		return nil, fmt.Errorf("sprintfn: a pipeline of %d returned %d results", len(items), len(results))
	}
	return results, nil
}

// Read sends one atomic read: every answer comes from one snapshot, with one
// time (1.0, "One read function").
func Read(ctx context.Context, c Client, rr *ReadRequest) (Result, error) {
	results, err := c.Pipeline(ctx, []Item{{Read: rr}})
	if err != nil {
		return Result{}, err
	}
	if len(results) != 1 {
		return Result{}, fmt.Errorf("sprintfn: a pipeline of one returned %d results", len(results))
	}
	return results[0], nil
}

// checkItem says an item names exactly one of its three kinds.
func checkItem(it Item) *Refusal {
	n := 0
	if it.Step != nil {
		n++
	}
	if it.Read != nil {
		n++
	}
	if it.Page != nil {
		n++
	}
	if n != 1 {
		return refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	return nil
}

// Part is one of the parts of 1.0 (lease, pop, ingest, beat, clock, sprint):
// data that a request carries, with the two things the write path asks of
// it. Pre reads what the part needs and decides, writing nothing; Cmds turns
// the decision into write commands once the log's seqs are known, in A1
// order (the write that records owed work before the write that forgets its
// trigger). errata E3's interface.
type Part interface {
	Pre(st *State, req *Request, obs *Before) (any, *Refusal)
	Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal)
}

// PartFuncs is a Part written as data: its two functions as fields.
type PartFuncs struct {
	PreFunc  func(st *State, req *Request, obs *Before) (any, *Refusal)
	CmdsFunc func(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal)
}

// Pre calls PreFunc.
func (p PartFuncs) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	return p.PreFunc(st, req, obs)
}

// Cmds calls CmdsFunc.
func (p PartFuncs) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) {
	return p.CmdsFunc(st, plan, lp)
}

// PartRegistry is the parts by name: the Go counterpart of the Lua core's
// NS.SP.parts. A name is one of PartOrder's and is registered once.
type PartRegistry struct {
	mu    sync.Mutex
	parts map[string]Part
}

// NewPartRegistry is an empty registry. The write path's is the package's
// own, filled by RegisterPart; a test makes its own.
func NewPartRegistry() *PartRegistry { return &PartRegistry{parts: map[string]Part{}} }

// Errors of a registration.
var (
	ErrPartUnknown = errors.New("sprintfn: not a part of 1.0 (lease, pop, ingest, beat, clock, sprint)")
	ErrPartTwice   = errors.New("sprintfn: part registered twice")
	ErrPartNil     = errors.New("sprintfn: nil part")
)

// Register adds a part. A name not in PartOrder, a name already registered,
// and a nil part are refused, so no registry holds two writers of one part.
func (r *PartRegistry) Register(name string, p Part) error {
	known := false
	for _, n := range PartOrder {
		if n == name {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("%w: %q", ErrPartUnknown, name)
	}
	if p == nil {
		return fmt.Errorf("%w: %q", ErrPartNil, name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.parts[name]; ok {
		return fmt.Errorf("%w: %q", ErrPartTwice, name)
	}
	r.parts[name] = p
	return nil
}

// Lookup is the part of a name, and false when none is registered.
func (r *PartRegistry) Lookup(name string) (Part, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.parts[name]
	return p, ok
}

// defaultParts is the write path's registry.
var defaultParts = NewPartRegistry()

// RegisterPart adds a part to the write path's registry, from the init of the
// part's file (8.0). A refused registration panics: two writers of one part,
// or a part 1.0 does not have, is a build that must not start.
func RegisterPart(name string, p Part) { mustRegister(defaultParts, name, p) }

func mustRegister(r *PartRegistry, name string, p Part) {
	if err := r.Register(name, p); err != nil {
		panic(err)
	}
}

// requestPart is the part of a request by name, and whether the request
// carries it.
func requestPart(req *Request, name string) bool {
	switch name {
	case PartLease:
		return req.Lease != nil
	case PartPop:
		return req.Pop != nil
	case PartIngest:
		return req.Ingest != nil
	case PartBeat:
		return req.Beat != nil
	case PartClock:
		return req.Clock != nil
	case PartSprint:
		return req.Sprint != nil
	}
	return false
}
