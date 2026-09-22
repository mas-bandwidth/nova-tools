package sprintcol

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// spyGitHub counts every poll. The column is handed one, and a call means
// the render left the store.
type spyGitHub struct{ calls int }

func (s *spyGitHub) ListOpenPulls(context.Context, string) (int, error) {
	s.calls++
	return 0, errors.New("GitHub was called while the queue column rendered")
}

// cmdHook records the Redis commands issued after it is installed.
type cmdHook struct{ names []string }

func (h *cmdHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *cmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.names = append(h.names, cmd.Name())
		return next(ctx, cmd)
	}
}

func (h *cmdHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func fixture(t *testing.T) (*Redis, *cmdHook) {
	t.Helper()
	mr := miniredis.RunT(t)
	store, err := Open(mr.Addr())
	if err != nil {
		t.Fatalf("open fixture store: %s", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hook := &cmdHook{}
	store.rdb.AddHook(hook)
	return store, hook
}

func xadd(t *testing.T, store *Redis, stream, task string) {
	t.Helper()
	_, err := store.rdb.XAdd(context.Background(), &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{"task": task},
	}).Result()
	if err != nil {
		t.Fatalf("xadd %s %s: %s", stream, task, err)
	}
}

// TestQueueColumnReadsTheFixtureStoreAndNeverCallsGitHub is the counter.
// The fixture stream q:rowan holds two tasks and q:emma holds five. The
// queue column for rowan is 2, emma is 5, and a friend with no stream is 0.
// The GitHub client is in hand for every render and is never called, including
// when the stream is absent. The only Redis command during a render is XLEN.
func TestQueueColumnReadsTheFixtureStoreAndNeverCallsGitHub(t *testing.T) {
	store, hook := fixture(t)
	xadd(t, store, "q:rowan", "t1")
	xadd(t, store, "q:rowan", "t2")
	xadd(t, store, "q:emma", "t3")
	xadd(t, store, "q:emma", "t4")
	xadd(t, store, "q:emma", "t5")
	xadd(t, store, "q:emma", "t6")
	xadd(t, store, "q:emma", "t7")
	hook.names = nil

	gh := &spyGitHub{}
	ctx := context.Background()
	col := Queue{}
	if col.Name() != "queue" {
		t.Fatalf("column name %q, want queue", col.Name())
	}

	rowan, err := col.Render(ctx, store, gh, "rowan")
	if err != nil {
		t.Fatal(err)
	}
	if rowan != (Cell{Column: "queue", Subject: "rowan", N: 2}) {
		t.Fatalf("rowan: got %+v, want queue=2", rowan)
	}
	emma, err := col.Render(ctx, store, gh, "emma")
	if err != nil {
		t.Fatal(err)
	}
	if emma.N != 5 || emma.Subject != "emma" || emma.Column != "queue" {
		t.Fatalf("emma: got %+v, want queue=5", emma)
	}
	stella, err := col.Render(ctx, store, gh, "stella")
	if err != nil {
		t.Fatal(err)
	}
	if stella.N != 0 {
		t.Fatalf("an absent stream rendered %d; an empty queue is 0, not a GitHub lookup", stella.N)
	}
	if gh.calls != 0 {
		t.Fatalf("GitHub was called %d times while the queue column rendered", gh.calls)
	}
	if len(hook.names) != 3 {
		t.Fatalf("redis commands during three renders: %q, want three xlen", hook.names)
	}
	for _, name := range hook.names {
		if name != "xlen" {
			t.Fatalf("render issued %q; the queue column is one XLEN and nothing else", name)
		}
	}
}

// A name that would select another key is refused, and the refusal does not
// call GitHub. A stream of the wrong type is an error, not a poll.
func TestQueueColumnRefusesABadKeyAndDoesNotCallGitHub(t *testing.T) {
	store, _ := fixture(t)
	if err := store.rdb.Set(context.Background(), "q:rowan", "not-a-stream", 0).Err(); err != nil {
		t.Fatal(err)
	}
	gh := &spyGitHub{}
	ctx := context.Background()

	if _, err := (Queue{}).Render(ctx, store, gh, "rowan:front"); err == nil {
		t.Fatal("a friend name containing a colon was accepted")
	}
	if _, err := (Queue{}).Render(ctx, store, gh, ""); err == nil {
		t.Fatal("an empty friend name was accepted")
	}
	if _, err := (Queue{}).Render(ctx, store, gh, "row an"); err == nil {
		t.Fatal("a friend name containing a space was accepted")
	}
	if _, err := (Queue{}).Render(ctx, nil, gh, "rowan"); err == nil {
		t.Fatal("a missing store was accepted")
	}
	_, err := (Queue{}).Render(ctx, store, gh, "rowan")
	if err == nil {
		t.Fatal("XLEN of a string key was treated as a queue")
	}
	if gh.calls != 0 {
		t.Fatalf("GitHub was called %d times on a refusal", gh.calls)
	}
}
