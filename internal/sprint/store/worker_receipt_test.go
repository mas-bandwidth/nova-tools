package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

type lostWorkerReply struct {
	*Mem
	st        *Store
	apply     bool
	acquire   bool
	wrongOp   bool
	doneErr   bool
	corrupt   bool
	wrongArgs bool
	finished  bool
	cancel    context.CancelFunc
}

func (b *lostWorkerReply) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	ok, err := b.Mem.Acquire(ctx, gen, op)
	if !b.acquire || !ok || err != nil {
		return ok, err
	}
	if _, _, err := b.st.apply(ctx, op); err != nil {
		return false, err
	}
	if err := b.Mem.Release(ctx, op, true); err != nil {
		return false, err
	}
	return false, errors.New("the acquire reply was lost after repair")
}

func (b *lostWorkerReply) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	rc, err := b.Mem.Apply(ctx, m)
	if !b.apply || err != nil {
		return rc, err
	}
	if !b.finished {
		b.finished = true
		if _, err := b.st.Repair(ctx); err != nil {
			return rc, err
		}
	}
	return rc, errors.New("the apply reply was lost")
}

func (b *lostWorkerReply) Release(ctx context.Context, op OpRecord, commit bool) error {
	if err := b.Mem.Release(ctx, op, commit); err != nil {
		return err
	}
	if b.cancel != nil {
		b.cancel()
	}
	return errors.New("the commit reply was lost")
}

func (b *lostWorkerReply) Done(ctx context.Context, id string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if b.doneErr {
		return "", false, errors.New("the receipt read failed")
	}
	if b.corrupt {
		return "unreadable", true, nil
	}
	if b.wrongOp {
		return `{"verb":"finish","op":"another-operation","moved":["another card moved"]}`, true, nil
	}
	raw, ok, err := b.Mem.Done(ctx, id)
	if b.wrongArgs && ok && err == nil {
		var rec Result
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return "", false, err
		}
		rec.Args = "other-arguments"
		body, err := json.Marshal(rec)
		return string(body), true, err
	}
	return raw, ok, err
}

// A planned move is confirmed only by this exact operation's commit receipt,
// including when the tick repaired it while its apply reply was lost.
func TestAWorkerRecoversOnlyItsExactCommittedReceipt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		apply     bool
		acquire   bool
		wrongOp   bool
		doneErr   bool
		corrupt   bool
		wrongArgs bool
		canceled  bool
	}{
		{name: "apply reply lost after repair", apply: true},
		{name: "commit reply lost"},
		{name: "acquire reply lost after repair", acquire: true},
		{name: "another operation is not confirmation", wrongOp: true},
		{name: "unanswered receipt is not confirmation", doneErr: true},
		{name: "unreadable receipt is not confirmation", corrupt: true},
		{name: "other arguments are not confirmation", wrongArgs: true},
		{name: "acquire with unanswered proof stays unknown", acquire: true, doneErr: true},
		{name: "acquire with unreadable proof stays unknown", acquire: true, corrupt: true},
		{name: "acquire with other operation stays unknown", acquire: true, wrongOp: true},
		{name: "acquire with other arguments stays unknown", acquire: true, wrongArgs: true},
		{name: "canceled proof stays unknown", canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)
			h.must(DealStep(sprint.DealReq{}))
			h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}}))
			c := h.snap().Fleet.Cell("m1", sprint.Working)[0]
			b := &lostWorkerReply{Mem: h.m, st: h.st, apply: tc.apply, acquire: tc.acquire, wrongOp: tc.wrongOp, doneErr: tc.doneErr, corrupt: tc.corrupt, wrongArgs: tc.wrongArgs}
			st := *h.st
			st.B = b
			ctx := h.ctx
			if tc.canceled {
				ctx, b.cancel = context.WithCancel(ctx)
				defer b.cancel()
			}
			res, err := st.Run(ctx, FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
			if tc.wrongOp || tc.doneErr || tc.corrupt || tc.wrongArgs || tc.canceled {
				require.ErrorIs(t, err, ErrUnknown)
				require.False(t, res.Replay)
			} else {
				require.NoError(t, err)
				require.True(t, res.Replay)
				require.Len(t, res.Moved, 1)
				require.Empty(t, res.Pending)
			}
			require.Equal(t, sprint.Review, h.state("s1-1"))
			h.clean("committed finish")
		})
	}
}
