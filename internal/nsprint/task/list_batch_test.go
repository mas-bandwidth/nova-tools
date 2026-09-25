package task_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func setupListTestRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

// recorder is the round-trip counting hook: it records every single
// command and every pipeline (as its command names) the client sends.
type listRecorder struct {
	mu        sync.Mutex
	singles   []string
	pipelines [][]string
}

func (*listRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *listRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.mu.Lock()
		r.singles = append(r.singles, listCommandLine(cmd))
		r.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (r *listRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		names := make([]string, len(cmds))
		for i, cmd := range cmds {
			names[i] = listCommandLine(cmd)
		}
		r.mu.Lock()
		r.pipelines = append(r.pipelines, names)
		r.mu.Unlock()
		return next(ctx, cmds)
	}
}

func listCommandLine(cmd redis.Cmder) string {
	args := cmd.Args()
	name := strings.ToLower(cmd.Name())
	if (name == "fcall" || name == "fcall_ro") && len(args) > 1 {
		return name + " " + fmt.Sprint(args[1])
	}
	return name
}

func (r *listRecorder) trips() int { return len(r.singles) + len(r.pipelines) }

// seedListTask writes one card record on the one store task:<id> (#3778),
// the sprint a field; the sprint's index sets name it.
func seedListTask(t *testing.T, client *redis.Client, S, id, state, owner string) {
	t.Helper()
	ctx := context.Background()
	key := "task:" + id
	if err := client.HSet(ctx, key, "sprint", S, "state", state, "owner", owner, "title", "task "+id, "kind", "build", "ref", "r-"+id,
		"priority", "5", "pushed_at", "1", "est", "30", "attempt", "0").Err(); err != nil {
		t.Fatal(err)
	}
}

func seedListOpen(t *testing.T, client *redis.Client, S, friend, id string, score float64) {
	t.Helper()
	ctx := context.Background()
	seedListTask(t, client, S, id, "open", "")
	client.ZAdd(ctx, "s:"+S+":open:"+friend, redis.Z{Score: score, Member: id})
	client.SAdd(ctx, "s:"+S+":idx:task:open", id)
}

func seedListClaimed(t *testing.T, client *redis.Client, S, owner, id string) {
	t.Helper()
	ctx := context.Background()
	seedListTask(t, client, S, id, "claimed", owner)
	client.SAdd(ctx, "s:"+S+":idx:task:claimed", id)
}

func seedListWorking(t *testing.T, client *redis.Client, S, owner, id string) {
	t.Helper()
	ctx := context.Background()
	seedListTask(t, client, S, id, "working", owner)
	client.SAdd(ctx, "s:"+S+":idx:task:working", id)
}

func seedListWaiting(t *testing.T, client *redis.Client, S, owner, id string) {
	t.Helper()
	ctx := context.Background()
	seedListTask(t, client, S, id, "waiting", owner)
	client.SAdd(ctx, "s:"+S+":idx:task:waiting", id)
}

func seedListDone(t *testing.T, client *redis.Client, S, friend, id string) {
	t.Helper()
	ctx := context.Background()
	seedListTask(t, client, S, id, "closed", friend)
	client.SAdd(ctx, "s:"+S+":done:"+friend, id)
}

func openListSprint(client *redis.Client, S string, order float64) {
	ctx := context.Background()
	client.HSet(ctx, "s:"+S, "status", "open")
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: order, Member: S})
}

// TestListThreeSprintsOneTrip is the #3263 DONE-WHEN: a test with 3 sprints
// fails if List issues more than 1 round trip.
func TestListThreeSprintsOneTrip(t *testing.T) {
	st, client := setupListTestRedis(t)
	ctx := context.Background()

	// Open 3 sprints.
	for i, S := range []string{"list-a", "list-b", "list-c"} {
		openListSprint(client, S, float64(i+1))
	}

	// Sprint a: 1 open assigned to f1, 1 working owned by f1.
	seedListOpen(t, client, "list-a", "f1", "a1", 1)
	seedListWorking(t, client, "list-a", "f1", "a2")

	// Sprint b: 1 claimed owned by f1, 1 waiting owned by f1.
	seedListClaimed(t, client, "list-b", "f1", "b1")
	seedListWaiting(t, client, "list-b", "f1", "b2")

	// Sprint c: 1 open assigned to f1, 1 done by f1 (closed, should not appear without state filter).
	seedListOpen(t, client, "list-c", "f1", "c1", 1)
	seedListDone(t, client, "list-c", "f1", "c2")

	// A task owned by someone else should not appear.
	seedListWorking(t, client, "list-a", "other", "a3")

	// A record left at the retired s:<S>:task:<id> key is not the card: the
	// one store is task:<id>, so an indexed id with no task:<id> lists nothing.
	client.HSet(ctx, "s:list-b:task:b9", "state", "working", "owner", "f1")
	client.SAdd(ctx, "s:list-b:idx:task:working", "b9")

	rec := &listRecorder{}
	client.AddHook(rec)
	start := time.Now()
	rows, err := task.ListStore(ctx, st, task.ListRequest{As: "f1"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ListStore: %v", err)
	}

	t.Logf("list 3 sprints: %d rows, %d round trips (pipelines=%v singles=%v) in %s", len(rows), rec.trips(), rec.pipelines, rec.singles, elapsed)

	// The DONE-WHEN: exactly 1 round trip (one FCallRO).
	if rec.trips() != 1 {
		t.Fatalf("round trips: %d (pipelines=%v singles=%v); want exactly 1", rec.trips(), rec.pipelines, rec.singles)
	}
	if len(rec.singles) != 1 || rec.singles[0] != "fcall_ro ns_task_list" {
		t.Fatalf("singles=%v; want [fcall_ro ns_task_list]", rec.singles)
	}

	// Verify the rows are correct: a1 (open), a2 (working), b1 (claimed), b2 (waiting), c1 (open). c2 is closed, a3 is owned by other.
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	// Rows should be sorted by sprint then id.
	want := []string{
		"list-a/a1/open",
		"list-a/a2/working",
		"list-b/b1/claimed",
		"list-b/b2/waiting",
		"list-c/c1/open",
	}
	for i, w := range want {
		got := rows[i].Sprint + "/" + rows[i].ID + "/" + rows[i].State
		if got != w {
			t.Fatalf("row %d: got %s, want %s", i, got, w)
		}
	}
}

// TestListOneSprint verifies the single-sprint path also uses one round trip.
func TestListOneSprint(t *testing.T) {
	st, client := setupListTestRedis(t)
	ctx := context.Background()

	openListSprint(client, "solo", 1)
	seedListOpen(t, client, "solo", "f1", "t1", 1)
	seedListWorking(t, client, "solo", "f1", "t2")

	rec := &listRecorder{}
	client.AddHook(rec)
	rows, err := task.ListStore(ctx, st, task.ListRequest{Sprint: "solo", As: "f1"})
	if err != nil {
		t.Fatalf("ListStore: %v", err)
	}
	if rec.trips() != 1 {
		t.Fatalf("round trips: %d; want 1", rec.trips())
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

// TestListStateFilter verifies state filtering works through the FCALL.
func TestListStateFilter(t *testing.T) {
	st, client := setupListTestRedis(t)
	ctx := context.Background()

	openListSprint(client, "filter", 1)
	seedListOpen(t, client, "filter", "f1", "o1", 1)
	seedListWorking(t, client, "filter", "f1", "w1")
	seedListClaimed(t, client, "filter", "f1", "c1")

	rec := &listRecorder{}
	client.AddHook(rec)
	rows, err := task.ListStore(ctx, st, task.ListRequest{Sprint: "filter", As: "f1", State: "working"})
	if err != nil {
		t.Fatalf("ListStore: %v", err)
	}
	if rec.trips() != 1 {
		t.Fatalf("round trips: %d; want 1", rec.trips())
	}
	if len(rows) != 1 || rows[0].ID != "w1" || rows[0].State != "working" {
		t.Fatalf("got %v, want [filter/w1/working]", rows)
	}
}

// TestListNoSprints verifies the empty-sprint case returns nothing in one trip.
func TestListNoSprints(t *testing.T) {
	st, client := setupListTestRedis(t)
	ctx := context.Background()

	rec := &listRecorder{}
	client.AddHook(rec)
	rows, err := task.ListStore(ctx, st, task.ListRequest{As: "f1"})
	if err != nil {
		t.Fatalf("ListStore: %v", err)
	}
	if rec.trips() != 1 {
		t.Fatalf("round trips: %d; want 1", rec.trips())
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}
