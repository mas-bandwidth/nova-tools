package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

// A tick part or worker's take, finish or read that lost a try to another
// writer takes the fence before its next read: an operation with
// no manifests, the lock, acquired at the generation read, which every other
// writer waits behind as behind any operation in flight. Its read, its plan
// and its write follow under it; its operation then takes the fence over from
// the lock (Relock), applies and releases as any operation does. A step that
// ends any other way releases the lock unwritten. At most one lock held per step, and
// only after a lost try: the world is not held up while nothing contends. A
// lock left by a writer that died is an operation past its grace with no
// manifests: another writer that finds it waits while it is in its grace, as
// behind any operation in flight, and releases it unwritten past it
// (finishOp). The tick's model is
// tla/DirtyTickRead.tla (Lock, and LockedApplyNotLost).

// Relocker is a store whose fence, held by one operation, can be handed to
// another in one exchange, only while the first still holds it.
type Relocker interface {
	Relock(ctx context.Context, held string, op OpRecord) (bool, error)
}

// Locker reserves an empty fence before reading any snapshot. A successful
// reservation advances the generation before returning. An error may leave
// this unique lock held; its caller must attempt an exact-ID release.
type Locker interface {
	Lock(ctx context.Context, op OpRecord) (bool, error)
}

// workerLock limits the worker fallback to the three handed-card verbs.
func (st *Store) workerLock(step Step) bool {
	_, can := st.B.(Relocker)
	return can && st.LockAfterLoss && (step.Verb == "take" || step.Verb == "finish" || step.Verb == "read")
}

// takeLock acquires the fence for the step before its read; nil when another
// operation holds it (the step reads as it would, and waits behind it).
func (st *Store) takeLock(ctx context.Context, step Step, family string) (*OpRecord, error) {
	// Overlapping retries of a caller operation never share a held lock.
	lock := OpRecord{ID: strings.ReplaceAll(family, "~", "-") + "-" + st.newID() + "-lock", Verb: step.Verb + " lock", At: st.now(), Lock: true}
	if b, ok := st.B.(Locker); ok && st.workerLock(step) {
		held, err := b.Lock(ctx, lock)
		if held || err != nil {
			return &lock, err
		}
		return nil, nil
	}
	f, err := st.B.ReadFence(ctx)
	if err != nil || f.Pending != nil {
		return nil, err
	}
	ok, err := st.B.Acquire(ctx, f.Gen, lock)
	if err != nil || !ok {
		return nil, err
	}
	return &lock, nil
}

// Lock has no client-observed generation to lose to a nearby writer. SET NX
// reserves only this epoch's empty fence, then INCR invalidates older reads
// before the holder reads its snapshot. A cut between them leaves an unwritten
// lock, released by repair after its grace. A late holder still cannot write
// without an exact-ID Relock, even if its delayed INCR invalidates a newer read.
func (r *Redis) Lock(ctx context.Context, op OpRecord) (bool, error) {
	body, err := json.Marshal(op)
	if err != nil {
		return false, err
	}
	held, err := r.C.SetNX(ctx, r.key(keyFence), body, 0).Result()
	if err != nil || !held {
		return held, err
	}
	err = r.C.Incr(ctx, r.key(keyGen)).Err()
	return true, err
}

func (m *Mem) Lock(ctx context.Context, op OpRecord) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("lock")
	l := m.log()
	if l.fence != nil {
		return false, nil
	}
	cp := op
	l.fence = &cp
	l.gen++
	return true, nil
}

// Relock hands the fence from the held operation to op, only while it holds
// it: WATCH on the fence, its id read, then MULTI/EXEC.
func (r *Redis) Relock(ctx context.Context, held string, op OpRecord) (bool, error) {
	body, err := json.Marshal(op)
	if err != nil {
		return false, err
	}
	id, err := json.Marshal(held)
	if err != nil {
		return false, err
	}
	prefix := `{"id":` + string(id) + `,`
	fence := r.key(keyFence)
	err = r.C.Watch(ctx, func(tx *redis.Tx) error {
		cur, err := tx.GetRange(ctx, fence, 0, int64(len(prefix)-1)).Result()
		if err != nil {
			return err
		}
		if cur != prefix {
			return errFenceMoved
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, fence, body, 0)
			return nil
		})
		return err
	}, fence)
	if errors.Is(err, errFenceMoved) || errors.Is(err, redis.TxFailedErr) {
		return false, nil
	}
	return err == nil, err
}

// Relock hands the fence from the held operation to op.
func (m *Mem) Relock(_ context.Context, held string, op OpRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("relock")
	l := m.log()
	if l.fence == nil || l.fence.ID != held {
		return false, nil
	}
	cp := op
	l.fence = &cp
	return true, nil
}

var (
	_ Relocker = (*Redis)(nil)
	_ Relocker = (*Mem)(nil)
)
