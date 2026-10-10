package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

// SeatPushes reads all four proofs from the existing seat push key; clear and
// teardown keep their existing ownership of that key (SPEC-SPRINT section 8).
func (st *Store) SeatPushes(ctx context.Context, name string) (sprint.SeatPushSet, bool, error) {
	var set sprint.SeatPushSet
	kv, ok := st.B.(KV)
	if !ok {
		return set, false, fmt.Errorf("this store keeps no seat push keys")
	}
	raw, found, err := kv.GetKey(ctx, SeatPushKey(name))
	if err != nil || !found {
		return set, false, err
	}
	if err = json.Unmarshal([]byte(raw), &set); err != nil {
		return set, false, fmt.Errorf("seat push record of %s is not JSON: %w", name, err)
	}
	set.Name = name
	return set, true, nil
}

// PutSeatPushes keeps watch receipts with the judgments record; no second key,
// index, presence store or per-friend write is introduced.
func (st *Store) PutSeatPushes(ctx context.Context, set sprint.SeatPushSet) error {
	kv, ok := st.B.(KV)
	if !ok {
		return fmt.Errorf("this store keeps no seat push keys")
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, SeatPushKey(set.Name), string(raw))
}

// BeatSeatPush records only a native observer's completed pass, with the current
// seat/epoch and selected delivery target. Failure revokes the old beat immediately.
func (st *Store) BeatSeatPush(ctx context.Context, name, source, failed string) error {
	return st.beatSeatPush(ctx, name, source, failed, nil)
}

// BeatSeatPushObserved refuses a read that finished under another seat, epoch or
// target; publishing it cannot relabel that old observation as a new proof.
func (st *Store) BeatSeatPushObserved(ctx context.Context, name, source, failed string, observed sprint.SeatWatchProof) error {
	return st.beatSeatPush(ctx, name, source, failed, &observed)
}

func (st *Store) beatSeatPush(ctx context.Context, name, source, failed string, observed *sprint.SeatWatchProof) error {
	if _, ok := sprint.SeatPushPeriods()[source]; !ok {
		return fmt.Errorf("unknown seat push %q: bus, friends or transitions required", source)
	}
	seat, err := st.SeatState(ctx)
	if err != nil {
		return err
	}
	if failed == "" && seat.Holder != name {
		return fmt.Errorf("%s is no longer the seat; no observer proof was renewed", name)
	}
	if observed != nil && (observed.Epoch != seat.Epoch || observed.Generation != seat.Generation) {
		return fmt.Errorf("the seat or epoch changed during the observer pass; no proof was renewed")
	}
	return st.UpdateSeatPushes(ctx, name, func(set *sprint.SeatPushSet) error {
		if set.Harness == "" {
			return fmt.Errorf("no judgments push target recorded for %s; run: nova-sprint seat install --actor %s --harness <harness> --target <session-dir>", name, name)
		}
		if observed != nil && (observed.Target != set.Target || observed.Session != set.Session) {
			return fmt.Errorf("the delivery target changed during the observer pass; no proof was renewed")
		}
		if set.Watches == nil {
			set.Watches = map[string]sprint.SeatWatchProof{}
		}
		p := set.Watches[source]
		if failed == "" {
			p = sprint.SeatWatchProof{At: st.Now(), Epoch: seat.Epoch, Generation: seat.Generation, Target: set.Target, Session: set.Session}
		}
		p.Failed = failed
		set.Watches[source] = p
		return nil
	})
}

// SeatPushUpdater applies one proof merge atomically with the judgments record.
// Watch beats cannot overwrite a newer nonce or another observer's beat.
type SeatPushUpdater interface {
	UpdateSeatPushes(context.Context, string, func(*sprint.SeatPushSet) error) error
}

func (st *Store) UpdateSeatPushes(ctx context.Context, name string, change func(*sprint.SeatPushSet) error) error {
	u, ok := st.B.(SeatPushUpdater)
	if !ok {
		return fmt.Errorf("this store cannot merge seat push receipts atomically")
	}
	return u.UpdateSeatPushes(ctx, name, change)
}

func seatPushMerge(raw, name string, change func(*sprint.SeatPushSet) error) (string, error) {
	var set sprint.SeatPushSet
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &set); err != nil {
			return "", err
		}
	}
	set.Name = name
	if err := change(&set); err != nil {
		return "", err
	}
	body, err := json.Marshal(set)
	return string(body), err
}

// UpdateSeatPushes makes the same atomic merge on the in-memory twin and Redis.
func (m *Mem) UpdateSeatPushes(ctx context.Context, name string, change func(*sprint.SeatPushSet) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("kv")
	if err := m.fail("kv"); err != nil {
		return err
	}
	next, err := seatPushMerge(m.kv[SeatPushKey(name)], name, change)
	if err != nil {
		return err
	}
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[SeatPushKey(name)] = next
	return nil
}

func (r *Redis) UpdateSeatPushes(ctx context.Context, name string, change func(*sprint.SeatPushSet) error) error {
	key := r.key(SeatPushKey(name))
	for attempt := 0; attempt < 4; attempt++ {
		err := r.C.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, key).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return err
			}
			next, err := seatPushMerge(raw, name, change)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error { p.Set(ctx, key, next, 0); return nil })
			return err
		}, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return fmt.Errorf("seat push receipt changed at four consecutive attempts; no proof was renewed")
}
