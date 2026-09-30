package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

// A part of the tick that lost a try to another writer (its Acquire found the
// generation moved) takes the fence before its next read: an operation with
// no manifests, the lock, acquired at the generation read, which every other
// writer waits behind as behind any operation in flight. Its read, its plan
// and its write follow under it; its operation then takes the fence over from
// the lock (Relock), applies and releases as any operation does. A step that
// ends any other way releases the lock unwritten. At most one lock a step, and
// only after a lost try: the world is not held up while nothing contends. A
// lock left by a writer that died is an operation past its grace with no
// manifests, which repair abandons (engine.go). The model is
// tla/DirtyTickRead.tla (Lock, and LockedApplyNotLost).

// Relocker is a store whose fence, held by one operation, can be handed to
// another in one exchange, only while the first still holds it.
type Relocker interface {
	Relock(ctx context.Context, held string, op OpRecord) (bool, error)
}

// takeLock acquires the fence for the step before its read; nil when another
// operation holds it (the step reads as it would, and waits behind it).
func (st *Store) takeLock(ctx context.Context, step Step, family string) (*OpRecord, error) {
	f, err := st.B.ReadFence(ctx)
	if err != nil || f.Pending != nil {
		return nil, err
	}
	lock := OpRecord{ID: strings.ReplaceAll(family, "~", "-") + "-lock", Verb: step.Verb + " lock", At: st.now()}
	ok, err := st.B.Acquire(ctx, f.Gen, lock)
	if err != nil || !ok {
		return nil, err
	}
	return &lock, nil
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
