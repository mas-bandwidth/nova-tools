package sprint

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// redis_cards.go is the ev:cards half of records.go, now that #2587 has landed: the card
// event stream and its reader. RedisCards answers Landed by draining `cards:done` under its
// own consumer group ("sprint", never "fold" -- the two must not steal each other's
// deliveries) and keeping an in-memory index of every OK entry it has seen, keyed on the
// card's label. FakeCards (records.go) stays for tests that want no store in the room; this
// is what defaultSprintDeps wires in cmd/nova-pulse.

// redisCardsGroup is the consumer group this reader claims. It is separate from
// events.Group ("fold"): the fold and the sprint flip are two independent readers of the
// same stream, and XREADGROUP delivers each group its own copy.
const redisCardsGroup = "sprint"

// RedisCards is the Cards seam over an events.Store: RedisStore on the fleet Redis in
// production, events.FakeStream (or a miniredis-backed events.RedisStore) in a test.
type RedisCards struct {
	store    events.Store
	consumer string

	mu    sync.Mutex
	index map[string]Record // label -> the OK entry's evidence
}

// NewRedisCards wires the ev:cards half over an already-open events.Store. consumer is this
// process's own name in the group, so a restart takes back what it left pending, the way
// the fold does; it defaults to "sprint-1" when empty.
func NewRedisCards(ctx context.Context, store events.Store, consumer string) (*RedisCards, error) {
	if store == nil {
		return nil, fmt.Errorf("RedisCards wants an events.Store; got nil")
	}
	if strings.TrimSpace(consumer) == "" {
		consumer = "sprint-1"
	}
	if err := store.EnsureGroup(ctx, redisCardsGroup); err != nil {
		return nil, fmt.Errorf("cards:done consumer group: %w", err)
	}
	return &RedisCards{store: store, consumer: consumer, index: map[string]Record{}}, nil
}

// DialCards opens the fleet Redis for the ev:cards half, over the same addr/user/password
// the sprint Store dials -- one fleet Redis, Johnny's division of labour, core types only.
func DialCards(ctx context.Context, addr, user, password string) (*RedisCards, error) {
	store, err := events.Open(ctx, events.Dial{Addr: addr, Username: user, Password: password})
	if err != nil {
		return nil, err
	}
	cards, err := NewRedisCards(ctx, store, "sprint-1")
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return cards, nil
}

// Close releases the underlying store.
func (r *RedisCards) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close()
}

// Landed drains the stream's pending and new entries into the index, then answers what it
// knows about label. A label with no OK entry yet answers the zero Record -- no evidence is
// not negative evidence -- and Flip leaves the task exactly as it was; nothing here ever
// answers Closed from a queued, started, fail or any other kind #2587 defines.
func (r *RedisCards) Landed(ctx context.Context, label string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return Record{}, fmt.Errorf("label is required; it is the id the whole card stream joins on")
	}
	if err := r.drain(ctx); err != nil {
		return Record{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.index[label], nil
}

// drain reclaims what a dead predecessor left unacked, then takes new entries, the same
// pending-first order the fold uses (Folder.Once): otherwise a restart walks past its own
// unfinished work.
func (r *RedisCards) drain(ctx context.Context) error {
	for {
		entries, err := r.store.Pending(ctx, redisCardsGroup, r.consumer, 200)
		if err != nil {
			return fmt.Errorf("cards:done pending: %w", err)
		}
		if len(entries) == 0 {
			break
		}
		if err := r.apply(ctx, entries); err != nil {
			return err
		}
	}
	for {
		entries, err := r.store.Next(ctx, redisCardsGroup, r.consumer, 200, 0)
		if err != nil {
			return fmt.Errorf("cards:done next: %w", err)
		}
		if len(entries) == 0 {
			break
		}
		if err := r.apply(ctx, entries); err != nil {
			return err
		}
	}
	return nil
}

// apply folds one batch into the index and acks it. An entry this package cannot read, or
// whose kind is not OK, is skipped -- an "asked" or "fail" entry is not evidence of anything
// closing -- but it is still acked, the same commit-then-ack order the fold uses, so a
// malformed writer never wedges every reader behind it.
func (r *RedisCards) apply(ctx context.Context, entries []events.Entry) error {
	ids := make([]string, 0, len(entries))
	r.mu.Lock()
	for _, entry := range entries {
		ids = append(ids, entry.ID)
		ev, err := events.FromFields(entry.Fields)
		if err != nil {
			continue
		}
		if ev.Kind != events.OK {
			continue
		}
		label := strings.TrimSpace(ev.Label)
		if label == "" {
			continue
		}
		at := ev.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		r.index[label] = Record{
			Closed:   true,
			Evidence: fmt.Sprintf("cards:done %s ok at %s", entry.ID, at.UTC().Format(time.RFC3339)),
			At:       at,
		}
	}
	r.mu.Unlock()
	return r.store.Ack(ctx, redisCardsGroup, ids...)
}
