package store

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

type failedWorkerLock struct {
	*workerFenceChurn
	owner string
	err   error
}

func (b *failedWorkerLock) Lock(ctx context.Context, op OpRecord) (bool, error) {
	if b.owner != "none" {
		if b.owner == "other" {
			op.ID = "another-holder"
		}
		if _, err := b.Mem.Lock(ctx, op); err != nil {
			return false, err
		}
	}
	return false, b.err // the reply cannot establish whether the claim happened
}

func TestAnUnansweredWorkerLockReleasesOnlyItsOwnClaim(t *testing.T) {
	t.Parallel()
	for _, owner := range []string{"none", "own", "other"} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)
			h.must(DealStep(sprint.DealReq{}))
			lost := errors.New("the reservation reply was lost")
			b := &failedWorkerLock{workerFenceChurn: &workerFenceChurn{Mem: h.m}, owner: owner, err: lost}
			st := *h.st
			st.B, st.LockAfterLoss = b, true
			_, err := st.Run(h.ctx, TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}}))
			require.ErrorIs(t, err, lost)
			f, err := h.m.ReadFence(h.ctx)
			require.NoError(t, err)
			if owner == "other" {
				require.NotNil(t, f.Pending)
				require.Equal(t, "another-holder", f.Pending.ID)
			} else {
				require.Nil(t, f.Pending)
			}
			require.Empty(t, h.snap().Fleet.Cell("m1", sprint.Working))
		})
	}
}

// Every trailing fence read loses to a tick until the worker takes its lock.
type workerFenceChurn struct {
	*Mem
	reads, locks int
	quiet        bool
	loseLock     bool
	firstRefuse  bool
}

// A local writer commits between each remote lock's generation read and CAS.
type lockClaimChurn struct{ *Mem }

func (b *lockClaimChurn) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if op.Lock {
		pulse := OpRecord{ID: NewID(), Verb: "local writer"}
		if ok, err := b.Mem.Acquire(ctx, gen, pulse); err != nil || !ok {
			return false, err
		}
		if err := b.Mem.Release(ctx, pulse, false); err != nil {
			return false, err
		}
	}
	return b.Mem.Acquire(ctx, gen, op)
}

func TestAWorkerClaimsTheFenceDespiteLocalGenerationChurn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	st := *h.st
	st.B = &lockClaimChurn{Mem: h.m}
	st.LockAfterLoss = true
	lock, err := st.takeLock(h.ctx, TakeStep(sprint.TakeReq{As: "m1"}), "remote-worker")
	require.NoError(t, err)
	require.NotNil(t, lock, "the lock does not need a quiet generation window before its read")
	require.NoError(t, h.m.Release(h.ctx, *lock, false))
}

func (b *workerFenceChurn) Apply(ctx context.Context, man ntable.BatchManifest) (ntable.Receipt, error) {
	if b.firstRefuse && b.locks > 0 {
		b.firstRefuse, b.quiet, b.reads = false, false, 0
		return ntable.Receipt{}, &ntable.Refusal{Code: "MEMBERREVISION", Sentence: "the first manifest applied nothing", Guarded: true}
	}
	return b.Mem.Apply(ctx, man)
}

func (b *workerFenceChurn) Relock(ctx context.Context, held string, op OpRecord) (bool, error) {
	if b.loseLock {
		b.loseLock = false
		f, err := b.Mem.ReadFence(ctx)
		if err != nil {
			return false, err
		}
		if f.Pending != nil {
			if err := b.Mem.Release(ctx, *f.Pending, false); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	return b.Mem.Relock(ctx, held, op)
}

func (b *workerFenceChurn) ReadFence(ctx context.Context) (Fence, error) {
	b.reads++
	f, err := b.Mem.ReadFence(ctx)
	if err != nil || f.Pending != nil || b.quiet || b.reads%2 != 0 {
		return f, err
	}
	op := OpRecord{ID: NewID(), Verb: "tick pulse"}
	if ok, err := b.Mem.Acquire(ctx, f.Gen, op); err != nil || !ok {
		return f, err
	}
	if err := b.Mem.Release(ctx, op, false); err != nil {
		return f, err
	}
	return b.Mem.ReadFence(ctx)
}

func (b *workerFenceChurn) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	ok, err := b.Mem.Acquire(ctx, gen, op)
	if ok && op.Lock {
		b.locks++
		b.quiet = true
	}
	return ok, err
}

func (b *workerFenceChurn) Lock(ctx context.Context, op OpRecord) (bool, error) {
	ok, err := b.Mem.Lock(ctx, op)
	if ok {
		b.locks++
		b.quiet = true
	}
	return ok, err
}

// Worker convergence uses the existing lock only after a read loses to the
// tick. A STOPPED queue is drained outside the worker's lock.
func TestAWorkerLocksAfterItsReadLosesToTheTick(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		queued      bool
		loseLock    bool
		firstRefuse bool
	}{
		{name: "take"},
		{name: "finish with stopped queue", queued: true},
		{name: "abandoned lock is reacquired", loseLock: true},
		{name: "unwritten relocked operation reacquires", firstRefuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(3)
			h.must(DealStep(sprint.DealReq{}))
			step := TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}})
			if tc.queued {
				h.startMachine()
				h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 2}}))
				cs := h.snap().Fleet.Cell("m1", sprint.Working)
				require.Len(t, cs, 2)
				c := cs[0]
				h.must(FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
				// A stop cut after its flag write leaves the queue for the next
				// verb; the public stop's later note step would drain it.
				m, _, err := h.st.Machine(h.ctx)
				require.NoError(t, err)
				m.State, m.Since = Stopped, h.st.now()
				m.Spans = append(m.Spans, Span{From: m.Since})
				require.NoError(t, h.st.putMachine(h.ctx, m))
				c = cs[1]
				step = FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}})
			}
			b := &workerFenceChurn{Mem: h.m, loseLock: tc.loseLock, firstRefuse: tc.firstRefuse}
			st := *h.st
			st.B, st.LockAfterLoss = b, true
			res, err := st.Run(h.ctx, step)
			require.NoError(t, err)
			require.False(t, res.Lost)
			require.Len(t, res.Moved, 1)
			require.Empty(t, res.Refused)
			require.Positive(t, b.locks)
			f, err := h.m.ReadFence(h.ctx)
			require.NoError(t, err)
			require.Nil(t, f.Pending)
			if tc.queued || tc.loseLock || tc.firstRefuse {
				require.GreaterOrEqual(t, b.locks, 2, "drain releases the lock and the worker reacquires")
			}
			h.clean("worker converged")
		})
	}
}

func TestADeadWorkersLockIsReleasedPastItsGrace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{}))
	lock, err := h.st.takeLock(h.ctx, TakeStep(sprint.TakeReq{As: "m1"}), "worker")
	require.NoError(t, err)
	require.NotNil(t, lock)
	res, err := h.st.Repair(h.ctx)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Equal(t, RepairOpen, res[0].Done)
	h.tick(2 * h.st.grace())
	res, err = h.st.Repair(h.ctx)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Equal(t, RepairAbandoned, res[0].Done)
	f, err := h.m.ReadFence(h.ctx)
	require.NoError(t, err)
	require.Nil(t, f.Pending)
	require.Len(t, h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}})).Moved, 1)
}

func TestConcurrentRetriesHaveDistinctFenceLocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	step := FinishStep(sprint.FinishReq{As: "m1"})
	a, err := h.st.takeLock(h.ctx, step, "same-caller-op")
	require.NoError(t, err)
	require.NotNil(t, a)
	require.NoError(t, h.m.Release(h.ctx, *a, false))
	b, err := h.st.takeLock(h.ctx, step, "same-caller-op")
	require.NoError(t, err)
	require.NotNil(t, b)
	require.NotEqual(t, a.ID, b.ID, "overlapping retries must not treat each other's lock as their own")
	require.NoError(t, h.m.Release(h.ctx, *b, false))
}
