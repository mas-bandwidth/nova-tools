package sprint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// redis_cards_test.go is Johnny's bar 3 for the ev:cards half, against miniredis rather than
// the fleet: it XADDs to cards:done the way nova-swarm does and asks RedisCards.Landed what
// it now knows, the same question Flip asks at every sprint status --flip.

func openTestCards(t *testing.T) (*RedisCards, *events.RedisStore) {
	t.Helper()
	mr := miniredis.RunT(t)
	ctx := context.Background()
	store, err := events.Open(ctx, events.Dial{Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("dialing the miniredis events store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cards, err := NewRedisCards(ctx, store, "test-1")
	if err != nil {
		t.Fatalf("NewRedisCards: %v", err)
	}
	return cards, store
}

// TestRedisCardsClosesFromAnOKEntry: one OK entry on cards:done, matching the task's ref by
// label, is what closes it -- and the evidence carries the event id and the entry's `at`.
func TestRedisCardsClosesFromAnOKEntry(t *testing.T) {
	cards, store := openTestCards(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 22, 18, 0, 0, 0, time.UTC)
	id, err := store.Emit(ctx, events.Event{Label: "cell-a", Kind: events.OK, At: at})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	tasks := []Task{{ID: "t1", Kind: KindCard, Ref: "cell-a", State: StateOpen}}
	changed, problems := Flip(ctx, tasks, nil, cards, time.Now())
	if len(problems) != 0 {
		t.Fatalf("Flip reported problems: %v", problems)
	}
	if len(changed) != 1 {
		t.Fatalf("Flip closed %d tasks, wants 1: %+v", len(changed), changed)
	}
	got := changed[0]
	if got.State != StateClosed {
		t.Fatalf("task state = %q, wants closed", got.State)
	}
	if !strings.Contains(got.Evidence, id) {
		t.Fatalf("evidence %q does not carry the event id %q", got.Evidence, id)
	}
	if !strings.Contains(got.Evidence, "2026-09-22T18:00:00Z") {
		t.Fatalf("evidence %q does not carry the entry's at", got.Evidence)
	}
	if !got.DoneAt.Equal(at) {
		t.Fatalf("DoneAt = %v, wants %v (the record's at, not now)", got.DoneAt, at)
	}
}

// TestRedisCardsIgnoresANonMatchingEntry: an entry for a different label, and an entry for
// the right label but the wrong kind, close nothing -- no evidence is not negative evidence,
// so the task is left exactly as it was.
func TestRedisCardsIgnoresANonMatchingEntry(t *testing.T) {
	cards, store := openTestCards(t)
	ctx := context.Background()
	if _, err := store.Emit(ctx, events.Event{Label: "cell-b", Kind: events.OK, At: time.Now()}); err != nil {
		t.Fatalf("Emit (different label): %v", err)
	}
	if _, err := store.Emit(ctx, events.Event{Label: "cell-a", Kind: events.Fail, At: time.Now()}); err != nil {
		t.Fatalf("Emit (wrong kind): %v", err)
	}
	if _, err := store.Emit(ctx, events.Event{Label: "cell-a", Kind: events.Asked, At: time.Now()}); err != nil {
		t.Fatalf("Emit (wrong kind): %v", err)
	}

	tasks := []Task{
		{ID: "t1", Kind: KindCard, Ref: "cell-a", State: StateOpen},
		{ID: "t2", Kind: KindCard, Ref: "cell-b-not-quite", State: StateOpen},
	}
	changed, problems := Flip(ctx, tasks, nil, cards, time.Now())
	if len(problems) != 0 {
		t.Fatalf("Flip reported problems: %v", problems)
	}
	if len(changed) != 0 {
		t.Fatalf("Flip closed %d tasks on non-matching evidence, wants 0: %+v", len(changed), changed)
	}
}
