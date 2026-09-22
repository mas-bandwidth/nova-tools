package sprintcol

import (
	"context"
	"errors"
	"fmt"
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
type cmdHook struct {
	names []string
	args  [][]any
}

func (h *cmdHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *cmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.names = append(h.names, cmd.Name())
		h.args = append(h.args, cmd.Args())
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
	xaddValues(t, store, stream, map[string]any{"task": task})
}

func xaddValues(t *testing.T, store *Redis, stream string, values map[string]any) {
	t.Helper()
	_, err := store.rdb.XAdd(context.Background(), &redis.XAddArgs{
		Stream: stream,
		Values: values,
	}).Result()
	if err != nil {
		t.Fatalf("xadd %s %v: %s", stream, values, err)
	}
}

// TestQueueColumnReadsTheFixtureStoreAndNeverCallsGitHub is the counter.
// The fixture stream q:rowan holds two tasks and q:emma holds five. The
// queue column for rowan is 2, emma is 5, and a friend with no stream is 0.
// The GitHub client is in hand for every render and is never called, including
// when the stream is absent. Each render reads the two dealer streams and
// does not call GitHub.
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
	hook.args = nil

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
	// Three friends, two streams each. A raw XLEN of one stream is not a render.
	if len(hook.names) != 6 {
		t.Fatalf("redis commands during three renders: %q, want six xrange", hook.names)
	}
	for _, name := range hook.names {
		if name != "xrange" {
			t.Fatalf("render issued %q; the queue column reads the dealer streams and does not call GitHub", name)
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
		t.Fatal("a string key was treated as a queue")
	}
	if gh.calls != 0 {
		t.Fatalf("GitHub was called %d times on a refusal", gh.calls)
	}
}

// TestQueueColumnCountsFrontAndBulkDedupedNotARawXLen is the fixture a raw
// XLEN of q:<friend> passes for the wrong reason. q:ada:front holds F, S, and
// S again. q:ada holds S again, B, one entry with no task field, and one whose
// task id is blank. The live queue is F, S, B.
//
// The three mistakes are not 3: a raw XLEN of the bulk stream is 4, dropping
// the front stream leaves S and B (2), and keeping every task entry on both
// streams counts S three times (5). GitHub is not called.
func TestQueueColumnCountsFrontAndBulkDedupedNotARawXLen(t *testing.T) {
	store, hook := fixture(t)
	ctx := context.Background()
	const friend = "ada"
	front, bulk := queueKeys(friend)

	xadd(t, store, front, "F")
	xadd(t, store, front, "S")
	xadd(t, store, front, "S")
	xadd(t, store, bulk, "S")
	xadd(t, store, bulk, "B")
	xaddValues(t, store, bulk, map[string]any{"note": "not-a-task"})
	xaddValues(t, store, bulk, map[string]any{"task": "   "})

	frontN, err := store.rdb.XLen(ctx, front).Result()
	if err != nil {
		t.Fatal(err)
	}
	bulkN, err := store.rdb.XLen(ctx, bulk).Result()
	if err != nil {
		t.Fatal(err)
	}
	if frontN != 3 || bulkN != 4 {
		t.Fatalf("fixture XLEN front=%d bulk=%d, want 3 and 4 so a raw length, a missed front stream, and a duplicate are not the live count", frontN, bulkN)
	}
	hook.names = nil
	hook.args = nil

	gh := &spyGitHub{}
	got, err := (Queue{}).Render(ctx, store, gh, friend)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Cell{Column: "queue", Subject: friend, N: 3}) {
		t.Fatalf("ada: got %+v, want queue=3 (front-only F, shared S once, bulk B; not XLEN %d of the bulk stream)", got, bulkN)
	}
	if gh.calls != 0 {
		t.Fatalf("GitHub was called %d times while the queue column rendered", gh.calls)
	}
	if len(hook.names) == 0 {
		t.Fatal("render issued no redis command")
	}
	sawFront, sawBulk := false, false
	for i, name := range hook.names {
		if name == "xlen" {
			t.Fatalf("render used XLEN %v; the column is not a raw length of one stream", hook.args)
		}
		if name != "xrange" {
			t.Fatalf("render issued %q; want xrange of %s and %s", name, front, bulk)
		}
		if i >= len(hook.args) || len(hook.args[i]) < 2 {
			t.Fatalf("xrange args %v", hook.args)
		}
		switch fmt.Sprint(hook.args[i][1]) {
		case front:
			sawFront = true
		case bulk:
			sawBulk = true
		default:
			t.Fatalf("render read %v; want %s and %s", hook.args[i], front, bulk)
		}
	}
	if !sawFront || !sawBulk {
		t.Fatalf("render missed a dealer stream: front=%v bulk=%v args=%v", sawFront, sawBulk, hook.args)
	}
}
