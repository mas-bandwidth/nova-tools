package sprintcol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const testSprint = "fixes-2026-09-23"

// spyGitHub counts every poll. The column is handed one, and a call means
// the render left the store.
type spyGitHub struct{ calls int }

func (s *spyGitHub) ListOpenPulls(context.Context, string) (int, error) {
	s.calls++
	return 0, errors.New("GitHub was called while the queue column rendered")
}

// cmdHook records the Redis commands issued after it is installed, and the
// round trips: one per single command, one per pipeline.
type cmdHook struct {
	names []string
	args  [][]any
	trips int
}

func (h *cmdHook) reset() { h.names, h.args, h.trips = nil, nil, 0 }

func (h *cmdHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *cmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.trips++
		h.names = append(h.names, cmd.Name())
		h.args = append(h.args, cmd.Args())
		return next(ctx, cmd)
	}
}

func (h *cmdHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.trips++
		for _, c := range cmds {
			h.names = append(h.names, c.Name())
			h.args = append(h.args, c.Args())
		}
		return next(ctx, cmds)
	}
}

func (h *cmdHook) count(name string) int {
	n := 0
	for _, got := range h.names {
		if got == name {
			n++
		}
	}
	return n
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

// deal is what the dealer does on push: the hash, the stream entry, and the
// open index, together.
func deal(t *testing.T, store *Redis, friend, id string, front bool) {
	t.Helper()
	ctx := context.Background()
	stream := QueuePrefix + friend
	if front {
		stream += FrontSuffix
	}
	_, err := store.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, taskPrefix+id, fieldOwner, friend, fieldState, stateOpen)
		p.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"id": id}})
		p.SAdd(ctx, OpenIndexKey(testSprint, friend), id)
		return nil
	})
	if err != nil {
		t.Fatalf("deal %s to %s: %s", id, friend, err)
	}
}

func hsetTask(t *testing.T, store *Redis, id, owner, state string) {
	t.Helper()
	err := store.rdb.HSet(context.Background(), taskPrefix+id, fieldOwner, owner, fieldState, state).Err()
	if err != nil {
		t.Fatalf("hset %s%s owner=%s state=%s: %s", taskPrefix, id, owner, state, err)
	}
}

func sadd(t *testing.T, store *Redis, friend string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := store.rdb.SAdd(context.Background(), OpenIndexKey(testSprint, friend), id).Err(); err != nil {
			t.Fatalf("sadd %s: %s", id, err)
		}
	}
}

// TestQueueColumnReadsTheFixtureStoreAndNeverCallsGitHub is the counter.
// rowan was dealt two tasks and emma five. The queue column for rowan is 2,
// emma is 5, and a friend with no index is 0. The GitHub client is in hand
// for every render and is never called, including when the index is absent.
// Each render is one SMEMBERS of the open index and, when it is not empty,
// one pipeline of HMGET owner state.
func TestQueueColumnReadsTheFixtureStoreAndNeverCallsGitHub(t *testing.T) {
	t.Parallel()

	store, hook := fixture(t)
	deal(t, store, "rowan", "t1", false)
	deal(t, store, "rowan", "t2", true)
	for _, id := range []string{"t3", "t4", "t5", "t6", "t7"} {
		deal(t, store, "emma", id, false)
	}
	hook.reset()

	gh := &spyGitHub{}
	ctx := context.Background()
	col := Queue{Sprint: testSprint}
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
		t.Fatalf("an absent index rendered %d; an empty queue is 0, not a GitHub lookup", stella.N)
	}
	if gh.calls != 0 {
		t.Fatalf("GitHub was called %d times while the queue column rendered", gh.calls)
	}
	for i, name := range hook.names {
		switch name {
		case "smembers":
			key := fmt.Sprint(hook.args[i][1])
			if !strings.HasPrefix(key, "sprint:"+testSprint+":idx:") || !strings.HasSuffix(key, ":open") {
				t.Fatalf("render read %s; the live queue is sprint:<sprint>:idx:<friend>:open", key)
			}
		case "hmget":
			if len(hook.args[i]) < 4 {
				t.Fatalf("hmget args %v", hook.args[i])
			}
			if key := fmt.Sprint(hook.args[i][1]); !strings.HasPrefix(key, taskPrefix) {
				t.Fatalf("render read %s; the check is task:<id> owner and state", key)
			}
			if fmt.Sprint(hook.args[i][2]) != fieldOwner || fmt.Sprint(hook.args[i][3]) != fieldState {
				t.Fatalf("hmget %v; want owner and state", hook.args[i])
			}
		default:
			t.Fatalf("render issued %q; the queue column reads the open index and task hashes, not the streams and not GitHub", name)
		}
	}
	// Three SMEMBERS, seven HMGET, and five round trips: rowan 2, emma 2,
	// stella 1 (an empty index sends no pipeline).
	if hook.count("smembers") != 3 || hook.count("hmget") != 7 || hook.trips != 5 {
		t.Fatalf("redis during three renders: %q in %d round trips, want three smembers and seven hmget in five", hook.names, hook.trips)
	}
}

// A name that would select another key is refused, and the refusal does not
// call GitHub. An index of the wrong type is an error, not a poll.
func TestQueueColumnRefusesABadKeyAndDoesNotCallGitHub(t *testing.T) {
	t.Parallel()

	store, _ := fixture(t)
	if err := store.rdb.Set(context.Background(), OpenIndexKey(testSprint, "rowan"), "not-a-set", 0).Err(); err != nil {
		t.Fatal(err)
	}
	gh := &spyGitHub{}
	ctx := context.Background()
	col := Queue{Sprint: testSprint}

	for _, friend := range []string{"rowan:front", "", "row an"} {
		if _, err := col.Render(ctx, store, gh, friend); err == nil {
			t.Fatalf("friend name %q was accepted", friend)
		}
	}
	for _, sprint := range []string{"", "a:b", "a b"} {
		if _, err := (Queue{Sprint: sprint}).Render(ctx, store, gh, "ada"); err == nil {
			t.Fatalf("sprint name %q was accepted", sprint)
		}
	}
	if _, err := col.Render(ctx, nil, gh, "rowan"); err == nil {
		t.Fatal("a missing store was accepted")
	}
	if _, err := col.Render(ctx, store, gh, "rowan"); err == nil {
		t.Fatal("a string key was treated as the open index")
	}
	if gh.calls != 0 {
		t.Fatalf("GitHub was called %d times on a refusal", gh.calls)
	}
}

// TestQueueColumnCountsOnlyOpenTasksOwnedByThisFriend is the live-count
// fixture. The index is the dealer's, and the hash is the truth: a member the
// dealer did not remove does not count. ada's index holds open F and B, plus
// stale members closed C, working W, reassigned R (now emma's), and M with no
// hash. The column is F and B. Closing B, then giving F to emma, drops the
// count even while both stay in the index. GitHub is not called.
func TestQueueColumnCountsOnlyOpenTasksOwnedByThisFriend(t *testing.T) {
	t.Parallel()

	store, _ := fixture(t)
	ctx := context.Background()
	const friend = "ada"
	col := Queue{Sprint: testSprint}

	sadd(t, store, friend, "F", "C", "W", "R", "M", "B")
	hsetTask(t, store, "F", friend, stateOpen)
	hsetTask(t, store, "C", friend, "closed")
	hsetTask(t, store, "W", friend, "working")
	hsetTask(t, store, "R", "emma", stateOpen)
	hsetTask(t, store, "B", friend, stateOpen)
	// M is an index member with no task:<id> hash.

	gh := &spyGitHub{}
	got, err := col.Render(ctx, store, gh, friend)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Cell{Column: "queue", Subject: friend, N: 2}) {
		t.Fatalf("ada: got %+v, want queue=2 (open F and B; not closed C, working W, reassigned R, or missing-hash M)", got)
	}

	hsetTask(t, store, "B", friend, "closed")
	got, err = col.Render(ctx, store, gh, friend)
	if err != nil {
		t.Fatal(err)
	}
	if got.N != 1 {
		t.Fatalf("after B closed: got %+v, want queue=1 (only F); a stale index member is not queued", got)
	}

	hsetTask(t, store, "F", "emma", stateOpen)
	got, err = col.Render(ctx, store, gh, friend)
	if err != nil {
		t.Fatal(err)
	}
	if got.N != 0 {
		t.Fatalf("after F was reassigned to emma: got %+v, want queue=0", got)
	}
	if gh.calls != 0 {
		t.Fatalf("GitHub was called %d times while the queue column rendered", gh.calls)
	}
}

// TestQueueColumnCostIsTheLiveQueueNotTheHistory is the history-heavy case.
// ada's streams hold 4,000 closed tasks (front and bulk) and 3 open ones. A
// render is one SMEMBERS and one pipeline of 3 HMGET: two round trips and no
// stream read. Doubling the history to 8,000 closed tasks leaves the render
// unchanged. A column that XRANGEs the streams reads 4,003 entries, then
// 8,003, on every one-second tick.
func TestQueueColumnCostIsTheLiveQueueNotTheHistory(t *testing.T) {
	t.Parallel()

	store, hook := fixture(t)
	ctx := context.Background()
	const friend = "ada"
	col := Queue{Sprint: testSprint}

	history := func(from, to int) {
		t.Helper()
		_, err := store.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			for i := from; i < to; i++ {
				id := fmt.Sprintf("old%05d", i)
				stream := QueuePrefix + friend
				if i%4 == 0 {
					stream += FrontSuffix
				}
				p.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"id": id}})
				p.HSet(ctx, taskPrefix+id, fieldOwner, friend, fieldState, "closed")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("seed history %d..%d: %s", from, to, err)
		}
	}
	history(0, 4000)
	deal(t, store, friend, "L1", true)
	deal(t, store, friend, "L2", false)
	deal(t, store, friend, "L3", false)

	for _, closed := range []int{4000, 8000} {
		frontN, _ := store.rdb.XLen(ctx, QueuePrefix+friend+FrontSuffix).Result()
		bulkN, _ := store.rdb.XLen(ctx, QueuePrefix+friend).Result()
		if frontN+bulkN != int64(closed+3) {
			t.Fatalf("fixture streams hold %d entries, want %d", frontN+bulkN, closed+3)
		}
		hook.reset()
		got, err := col.Render(ctx, store, &spyGitHub{}, friend)
		if err != nil {
			t.Fatal(err)
		}
		if got.N != 3 {
			t.Fatalf("with %d closed tasks in history: got %+v, want queue=3", closed, got)
		}
		if hook.trips != 2 || hook.count("smembers") != 1 || hook.count("hmget") != 3 || len(hook.names) != 4 {
			t.Fatalf("with %d closed tasks in history the render issued %q in %d round trips; want one smembers and three hmget in two", closed, hook.names, hook.trips)
		}
		if hook.count("xrange") != 0 || hook.count("xlen") != 0 {
			t.Fatalf("render read the streams: %q; they are history", hook.names)
		}
		if closed == 4000 {
			history(4000, 8000)
		}
	}
}
