package decide

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// A batch of decisions (SPEC-NOVA-DECIDE section 9): one schema asked over many
// states, each under its own op id. The record is read once, the decisions it
// already holds are answered from it, the rest are asked through the backend at
// most width at a time, and every new decision is appended in one write under
// the record's lock. Make reads the whole record for each decision; a batch of
// six hundred cards would read it six hundred times.

// Item is one decision of a batch: its op id, the state it is asked over, and
// the inputs the record names.
type Item struct {
	ID     string
	State  string
	Inputs map[string]string
}

// Made is one item's decision. Existing says the record held it and nothing was
// asked. Err is why it could not be made (its Decision then holds only the
// item's ID and Inputs): a *BackendError (nothing recorded) or
// a *ConflictError (the op id is recorded over another decision, schema or state).
type Made struct {
	Decision
	Existing bool
	Err      error
}

// MakeAll makes the decision of s over each item, in the items' order, each ask
// within wait (0 is ctx's own bound); once ctx is done no further item is asked
// and each is that item's Err. An empty record keeps nothing: no decision is read
// from it or written to it. The error
// is the record's (it cannot be read or written); a decision that could not be
// made is its item's Err, and the others are made and recorded.
func MakeAll(ctx context.Context, b Backend, s Schema, items []Item, record string, at time.Time, width int, wait time.Duration) ([]Made, error) {
	var ds []Decision
	if record != "" {
		var err error
		if ds, err = Load(record); err != nil {
			return nil, err
		}
	}
	out := make([]Made, len(items))
	var ask []int
	for i, it := range items {
		if have := Find(ds, it.ID); have != nil {
			out[i] = Made{Decision: *have, Existing: true, Err: Replays(*have, s, it.State)}
			continue
		}
		ask = append(ask, i)
	}
	stamp, hash := at.UTC().Format(time.RFC3339), s.Hash()
	sem := make(chan struct{}, max(width, 1))
	var wg sync.WaitGroup
	for _, i := range ask {
		sem <- struct{}{}
		if ctx.Err() != nil { // the batch's deadline passed: what is left is not asked
			<-sem
			out[i] = Made{Decision: Decision{ID: items[i].ID, Inputs: items[i].Inputs},
				Err: &BackendError{Backend: b.Name(), Err: fmt.Errorf("not asked: %w", ctx.Err())}}
			continue
		}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			it, actx, cancel := items[i], ctx, context.CancelFunc(func() {})
			if wait > 0 {
				actx, cancel = context.WithTimeout(ctx, wait)
			}
			defer cancel()
			answers, usage, err := Ask(actx, b, s, it.State)
			if err != nil {
				out[i] = Made{Decision: Decision{ID: it.ID, Inputs: it.Inputs}, Err: &BackendError{Backend: b.Name(), Err: err}}
				return
			}
			out[i].Decision = Decision{ID: it.ID, Decision: s.Name, Schema: hash, Backend: b.Name(), At: stamp,
				Inputs: it.Inputs, State: it.State, Answers: answers, Usage: usage}
		}()
	}
	wg.Wait()
	if record == "" || !slices.ContainsFunc(ask, func(i int) bool { return out[i].Err == nil }) {
		return out, nil // nothing new to record: the record is not opened for writing
	}
	err := locked(record, func(ds []Decision) ([]line, error) {
		var add []line
		for _, i := range ask {
			if out[i].Err != nil {
				continue
			}
			if have := Find(ds, out[i].ID); have != nil { // another writer recorded it meanwhile
				out[i] = Made{Decision: *have, Existing: true, Err: Replays(*have, s, items[i].State)}
				continue
			}
			d := out[i].Decision
			add = append(add, line{Decision: &d})
		}
		return add, nil
	})
	return out, err
}
