//go:build functional

package task_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// recorder is the round-trip counting hook of #3261: it records every single
// command and every pipeline (as its command names) the client sends.
type recorder struct {
	mu        sync.Mutex
	singles   []string
	pipelines [][]string
	before    func(cmd redis.Cmder)
}

func (*recorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *recorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.mu.Lock()
		r.singles = append(r.singles, commandLine(cmd))
		r.mu.Unlock()
		if r.before != nil {
			r.before(cmd)
		}
		return next(ctx, cmd)
	}
}

func (r *recorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		names := make([]string, len(cmds))
		for i, cmd := range cmds {
			names[i] = commandLine(cmd)
		}
		r.mu.Lock()
		r.pipelines = append(r.pipelines, names)
		r.mu.Unlock()
		return next(ctx, cmds)
	}
}

// commandLine is the command name, plus the function name for an FCALL.
func commandLine(cmd redis.Cmder) string {
	args := cmd.Args()
	name := strings.ToLower(cmd.Name())
	if (name == "fcall" || name == "fcall_ro") && len(args) > 1 {
		return name + " " + fmt.Sprint(args[1])
	}
	return name
}

func (r *recorder) trips() int { return len(r.singles) + len(r.pipelines) }

// seedOpenTask writes one open task in friend's queue of sprint S.
func seedOpenTask(t *testing.T, client *redis.Client, S, friend, id string, score float64, needs string) {
	t.Helper()
	ctx := context.Background()
	key := "task:" + id
	if err := client.HSet(ctx, key, "state", "open", "title", "task "+id, "kind", "build", "ref", "r-"+id,
		"priority", "5", "pushed_at", "1", "est", "30", "owner", "", "attempt", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if needs != "" {
		client.HSet(ctx, key, "needs", needs)
	}
	client.ZAdd(ctx, "s:"+S+":open:"+friend, redis.Z{Score: score, Member: id})
	client.SAdd(ctx, "s:"+S+":idx:task:open", id)
}

func openSprint(client *redis.Client, S string, order float64) {
	ctx := context.Background()
	client.HSet(ctx, "s:"+S, "status", "open")
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: order, Member: S})
}

// TestTakeAvailableThreeQueuesFiveClaimsOnePipelineOneFCall is the #3261
// DONE-WHEN: a take across 3 sprint queues that claims 5 tasks sends exactly
// one pipeline (the friend reads and ns_task_take_view) and one FCALL
// (ns_task_take_n); it was 4 + S + 2k = 17 round trips.
func TestTakeAvailableThreeQueuesFiveClaimsOnePipelineOneFCall(t *testing.T) {
	t.Parallel()

	st, client := setupTakeTestRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f1", 5)
	for i, S := range []string{"batch-a", "batch-b", "batch-c"} {
		openSprint(client, S, float64(i+1))
	}
	// a has 2 tasks, b has 1 blocked and 1 ready, c has 3: the 5 claims are
	// a1 a2 b2 c1 c2 (b1 passed over, c3 beyond the free slots).
	seedOpenTask(t, client, "batch-a", "f1", "a1", 1, "")
	seedOpenTask(t, client, "batch-a", "f1", "a2", 2, "")
	seedOpenTask(t, client, "batch-b", "f1", "b1", 1, "gone")
	seedOpenTask(t, client, "batch-b", "f1", "b2", 2, "")
	seedOpenTask(t, client, "batch-c", "f1", "c1", 1, "")
	seedOpenTask(t, client, "batch-c", "f1", "c2", 2, "")
	seedOpenTask(t, client, "batch-c", "f1", "c3", 3, "")

	rec := &recorder{}
	client.AddHook(rec)
	start := time.Now()
	claims, err := task.TakeAvailable(ctx, st, "f1", "", "", 0, "f1", "idem-3261")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("TakeAvailable: %v", err)
	}
	var got []string
	for _, c := range claims {
		got = append(got, c.Sprint+"/"+c.ID)
		if !strings.HasPrefix(c.Token, "1.") || c.Attempt != 1 || c.Kind != "build" || c.Ref != "r-"+c.ID {
			t.Fatalf("claim %+v", c)
		}
	}
	if want := "batch-a/a1 batch-a/a2 batch-b/b2 batch-c/c1 batch-c/c2"; strings.Join(got, " ") != want {
		t.Fatalf("claims %v, want %s", got, want)
	}
	t.Logf("take 3 queues 5 claims: %d round trips (pipelines=%v singles=%v) in %s", rec.trips(), rec.pipelines, rec.singles, elapsed)
	if len(rec.pipelines) != 1 || len(rec.singles) != 1 || rec.singles[0] != "fcall ns_task_take_n" {
		t.Fatalf("round trips: pipelines=%v singles=%v; want one pipeline and one FCALL ns_task_take_n", rec.pipelines, rec.singles)
	}
	if p := strings.Join(rec.pipelines[0], ","); !strings.Contains(p, "fcall_ro ns_task_take_view") {
		t.Fatalf("pipeline %s does not carry the view", p)
	}
	if n, _ := client.ZCard(ctx, "friend:f1:cards:working").Result(); n != 5 {
		t.Fatalf("starting=%d, want 5", n)
	}
	if st, _ := client.HGet(ctx, "task:c3", "state").Result(); st != "open" {
		t.Fatalf("c3 state=%q, want open", st)
	}
}

// TestTakeAvailableOneSprintEightSlotsTwoTrips is the issue's shape: one
// sprint and 8 free slots was 21 round trips; it is one pipeline and one FCALL.
func TestTakeAvailableOneSprintEightSlotsTwoTrips(t *testing.T) {
	t.Parallel()

	st, client := setupTakeTestRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f1", 8)
	openSprint(client, "one", 1)
	for i := 0; i < 10; i++ {
		seedOpenTask(t, client, "one", "f1", fmt.Sprintf("t%02d", i), float64(i), "")
	}
	rec := &recorder{}
	client.AddHook(rec)
	start := time.Now()
	claims, err := task.TakeAvailable(ctx, st, "f1", "", "", 0, "f1", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("take 1 sprint 8 slots: %d claims, %d round trips in %s", len(claims), rec.trips(), time.Since(start))
	if len(claims) != 8 || rec.trips() != 2 || len(rec.pipelines) != 1 {
		t.Fatalf("claims=%d pipelines=%v singles=%v; want 8 claims in one pipeline and one FCALL", len(claims), rec.pipelines, rec.singles)
	}
}

// TestTakeAvailableFullFriendOneTrip: no free slot is one pipeline, no claim
// call, and the view reads no queue.
func TestTakeAvailableFullFriendOneTrip(t *testing.T) {
	t.Parallel()

	st, client := setupTakeTestRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f1", 1)
	openSprint(client, "full", 1)
	seedOpenTask(t, client, "full", "f1", "x", 1, "")
	client.ZAdd(ctx, "friend:f1:cards:working", redis.Z{Score: 1, Member: "full/y/1"})
	rec := &recorder{}
	client.AddHook(rec)
	claims, err := task.TakeAvailable(ctx, st, "f1", "", "", 0, "f1", "")
	if err != nil || len(claims) != 0 || rec.trips() != 1 {
		t.Fatalf("claims=%v err=%v trips=%d, want none in 1", claims, err, rec.trips())
	}
}

// TestTakeAvailableIDBlockedStrict: --id on a task with unmet needs returns
// the BlockedError, still in two round trips.
func TestTakeAvailableIDBlockedStrict(t *testing.T) {
	t.Parallel()

	st, client := setupTakeTestRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f1", 4)
	openSprint(client, "strict", 1)
	seedOpenTask(t, client, "strict", "f1", "b", 1, "n1 n2")
	seedOpenTask(t, client, "strict", "f1", "ok", 2, "")
	rec := &recorder{}
	client.AddHook(rec)
	_, err := task.TakeAvailable(ctx, st, "f1", "", "b", 0, "f1", "")
	var blocked *task.BlockedError
	if !errors.As(err, &blocked) || strings.Join(blocked.Needs, " ") != "n1 n2" || blocked.ID != "b" {
		t.Fatalf("err=%v, want BLOCKED b needs n1 n2", err)
	}
	if rec.trips() != 2 {
		t.Fatalf("trips=%d, want 2", rec.trips())
	}
	claims, err := task.TakeAvailable(ctx, st, "f1", "strict", "ok", 0, "f1", "")
	if err != nil || len(claims) != 1 || claims[0].ID != "ok" {
		t.Fatalf("take --id ok: %v %v", claims, err)
	}
}

// TestTakeAvailableRetryFallsBackToOneTake: an attempt that moves between the
// view and the batch claim answers RETRY in the batch; that one task is then
// taken on its own, so the claim still lands.
func TestTakeAvailableRetryFallsBackToOneTake(t *testing.T) {
	t.Parallel()

	st, client := setupTakeTestRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f1", 2)
	openSprint(client, "race", 1)
	seedOpenTask(t, client, "race", "f1", "r1", 1, "")
	seedOpenTask(t, client, "race", "f1", "r2", 2, "")
	other := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = other.Close() })
	rec := &recorder{}
	rec.before = func(cmd redis.Cmder) {
		if commandLine(cmd) == "fcall ns_task_take_n" {
			other.HSet(ctx, "task:r1", "attempt", "3")
		}
	}
	client.AddHook(rec)
	claims, err := task.TakeAvailable(ctx, st, "f1", "", "", 0, "f1", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range claims {
		got = append(got, fmt.Sprintf("%s@%d", c.ID, c.Attempt))
	}
	if strings.Join(got, " ") != "r2@1 r1@4" {
		t.Fatalf("claims %v, want r2@1 then r1@4 by the single take", got)
	}
}
