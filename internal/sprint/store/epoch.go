package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The sprint's epoch. The four tables are bound to one epoch key
// (sprint.Names.EpochKey); a step pins the epoch it starts at: it reads the
// tables at that epoch, writes every manifest at it, names the sprint's keys
// of that epoch, and holds the cards by their stored ids of that epoch
// (sprint.StoredID). clear advances the epoch: every writer still holding the
// old one is refused by the table layer as stale, and the old epoch stays
// readable (At).

// errCleared is a read that found the tables at another epoch than the step
// pinned: the sprint was cleared while the step read it.
var errCleared = errors.New("the sprint was cleared while the step read it")

// ClearedError is a step holding an epoch the sprint has left.
type ClearedError struct {
	Held, Now uint64
	At        time.Time
}

func (e *ClearedError) Error() string {
	return fmt.Sprintf("the sprint was cleared at %s: its epoch is now %d, and this step holds epoch %d; nothing was changed; read the sprint again (nova-sprint queue, where) and act on epoch %d",
		e.At.UTC().Format(time.RFC3339), e.Now, e.Held, e.Now)
}

// pin is the store pinned to the sprint's current epoch; a pinned store is
// itself.
func (st *Store) pin(ctx context.Context) (*Store, error) {
	if st.pinned {
		return st, nil
	}
	root := st.root
	if root == nil {
		root = st.B
	}
	es, err := root.Epoch(ctx)
	if err != nil {
		return nil, err
	}
	c := *st
	c.root, c.B, c.epoch, c.cleared, c.pinned = root, root, es.N, es.Cleared, true
	if es.N != 0 {
		// A backend is at epoch 0 until it is pinned to another: at epoch 0 the
		// store uses it as given (a test's wrapper stays in place).
		c.B = root.AtEpoch(es.N, false)
	}
	return &c, nil
}

// repin is the store pinned again, to the epoch the sprint is at now.
func (st *Store) repin(ctx context.Context) (*Store, error) {
	c := *st
	c.pinned = false
	return c.pin(ctx)
}

// At is the store reading an earlier epoch as it was: where, card and inbox
// at that epoch.
func (st *Store) At(epoch uint64) *Store {
	root := st.root
	if root == nil {
		root = st.B
	}
	c := *st
	c.root, c.B, c.epoch, c.pinned, c.old = root, root.AtEpoch(epoch, true), epoch, true, true
	return &c
}

// EpochNow is the sprint's epoch as the store holds it.
func (st *Store) EpochNow(ctx context.Context) (EpochState, error) {
	root := st.root
	if root == nil {
		root = st.B
	}
	return root.Epoch(ctx)
}

// sid is a card's stored id at the pinned epoch.
func (st *Store) sid(id string) string { return sprint.StoredID(id, st.epoch) }

func (st *Store) sids(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = st.sid(id)
	}
	return out
}
