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
	// Finished says the step finished at the epoch it held as the clear
	// closed it: its moves stand there, and nothing of it is at the new one.
	Finished bool
}

func (e *ClearedError) Error() string {
	if e.Held > e.Now {
		return fmt.Sprintf("this step holds epoch %d, which is unknown to this sprint: its epoch is %d; nothing was changed; read the sprint again (nova-sprint queue, where) and act on epoch %d",
			e.Held, e.Now, e.Now)
	}
	if e.Finished {
		return fmt.Sprintf("the sprint was cleared at %s as this step finished: its moves stand at epoch %d, which the clear closed, and the sprint's epoch is now %d; read the sprint again (nova-sprint queue, where) and act on epoch %d",
			e.At.UTC().Format(time.RFC3339), e.Held, e.Now, e.Now)
	}
	return fmt.Sprintf("the sprint was cleared at %s: its epoch is now %d, and this step holds epoch %d; nothing was changed; read the sprint again (nova-sprint queue, where) and act on epoch %d",
		e.At.UTC().Format(time.RFC3339), e.Now, e.Held, e.Now)
}

// pin is the store pinned to the sprint's current epoch; a pinned store is
// itself. A restore the epoch still owes (a clear cut between its advance and
// its restore) is performed first.
func (st *Store) pin(ctx context.Context) (*Store, error) {
	c, es, err := st.pinOnly(ctx)
	if err != nil || c == st || !es.Owed {
		return c, err
	}
	if _, err := c.restore(ctx, es.N-1); err != nil {
		return nil, fmt.Errorf("finishing the clear of %s (the shape of epoch %d at epoch %d): %w", es.Cleared.UTC().Format(time.RFC3339), es.N-1, es.N, err)
	}
	return c, nil
}

// pinOnly is the store pinned to the sprint's current epoch, with the epoch as
// read, and no restore performed; a pinned store is itself.
func (st *Store) pinOnly(ctx context.Context) (*Store, EpochState, error) {
	if st.pinned {
		return st, EpochState{N: st.epoch, Cleared: st.cleared}, nil
	}
	root := st.root
	if root == nil {
		root = st.B
	}
	es, err := root.Epoch(ctx)
	if err != nil {
		return nil, es, err
	}
	c := st.clone()
	c.root, c.B, c.epoch, c.cleared, c.pinned = root, root, es.N, es.Cleared, true
	if es.N != 0 {
		// A backend is at epoch 0 until it is pinned to another: at epoch 0 the
		// store uses it as given (a test's wrapper stays in place).
		c.B = root.AtEpoch(es.N, false)
	}
	return &c, es, nil
}

// repin is the store pinned again, to the epoch the sprint is at now.
func (st *Store) repin(ctx context.Context) (*Store, error) {
	c := st.clone()
	c.pinned = false
	return c.pin(ctx)
}

// repinOnly is the store pinned again, with no restore performed.
func (st *Store) repinOnly(ctx context.Context) (*Store, EpochState, error) {
	c := st.clone()
	c.pinned = false
	return c.pinOnly(ctx)
}

// At is the store reading an earlier epoch as it was: where, card and inbox
// at that epoch.
func (st *Store) At(epoch uint64) *Store {
	root := st.root
	if root == nil {
		root = st.B
	}
	c := st.clone()
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

// Pinned is the store pinned to the sprint's current epoch (itself when it is
// pinned already, as an At store is).
func (st *Store) Pinned(ctx context.Context) (*Store, error) { return st.pin(ctx) }

// PinnedEpoch is the epoch a pinned store is at.
func (st *Store) PinnedEpoch() uint64 { return st.epoch }

// left reads the sprint's epoch and says whether the sprint has left the
// epoch the store is pinned to.
func (st *Store) left(ctx context.Context) (EpochState, bool, error) {
	es, err := st.EpochNow(ctx)
	if err != nil {
		return es, false, err
	}
	return es, es.N != st.epoch, nil
}

// SetReview sets an open judgment's next review time (wait) at the sprint's
// epoch. A judgment id of another epoch is refused, naming its epoch: an id
// of an earlier epoch never reaches a judgment of this one.
func (st *Store) SetReview(ctx context.Context, noteID string, at time.Time) error {
	st, err := st.pin(ctx)
	if err != nil {
		return err
	}
	if e := sprint.IDEpoch(noteID); e != st.epoch {
		return errors.New(sprint.OtherEpoch(noteID, e, st.epoch))
	}
	return st.B.SetReview(ctx, noteID, at, st.now())
}

// Wait is the coordinator's wait on a judgment: for a condition the tick keeps
// it is a step that closes the judgment and holds the condition until the time
// (in running time); for any other it sets the judgment's review time. held
// says it was the step, and res is its result.
func (st *Store) Wait(ctx context.Context, noteID string, at time.Time) (res Result, held bool, err error) {
	st, err = st.pin(ctx)
	if err != nil {
		return res, false, err
	}
	if _, ok := sprint.StaleStream(noteID); ok {
		// a stream's stale judgment: a step that quiets the stream until the time
		res, err = st.Run(ctx, WaitStep(sprint.WaitReq{Note: noteID, Until: at, Who: st.Actor}))
		return res, true, err
	}
	open, err := st.B.OpenNotes(ctx)
	if err != nil {
		return res, false, err
	}
	for _, o := range open {
		if o.Note.ID == noteID && o.Note.Kind == sprint.Judgment && sprint.TickKept(o.Note.Type) {
			res, err = st.Run(ctx, WaitStep(sprint.WaitReq{Note: noteID, Until: at, Who: st.Actor}))
			return res, true, err
		}
	}
	coordinator, err := st.B.Coordinator(ctx)
	if err != nil {
		return res, false, err
	}
	if why := sprint.NotCoordinator(coordinator, st.Actor, "wait"); why != "" {
		return res, false, errors.New(why)
	}
	return res, false, st.SetReview(ctx, noteID, at)
}
