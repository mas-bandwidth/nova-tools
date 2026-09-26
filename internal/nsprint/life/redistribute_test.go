package life_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// rdRedis starts a throwaway redis-server with the nova_sprint library loaded.
// Named apart from the presence tests' helpers so both files share package
// life_test without a clash.
func rdRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

const (
	rdSprint = "control-3047"
	rdRepo   = "nova-tools"
)

var rdRoster = life.Roster{
	MayHold:     []string{"stella", "johnny", "fran", "sleepy"},
	Builders:    []string{"johnny"},
	Coordinator: "rowan",
}

func rdHead(c byte) string { return strings.Repeat(string(c), 40) }

// rdFixture builds the control keyspace: friend f holds five open tasks
// (two plain reads, a build, a fix, and a re-read of a PR on which f carries a
// typed HOLD from an earlier head) and two leases (a working build and a
// claimed read). stella and johnny are UP may-hold readers with free width,
// fran is UP but full, sleepy is down, johnny is the only builder and rowan is
// the coordinator. The tokens of the two leases are returned.
func rdFixture(t *testing.T, st *store.Store, client *redis.Client, f string) map[string]string {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(client.SAdd(ctx, "sprints", rdSprint).Err())
	must(client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: rdSprint}).Err())
	must(client.HSet(ctx, "s:"+rdSprint, "status", "open").Err())
	for _, name := range []string{f, "stella", "johnny", "fran", "sleepy", "rowan"} {
		must(client.SAdd(ctx, "friends", name).Err())
		must(client.HSet(ctx, "friend:"+name+":desired", "slots", "8", "machine", "studio", "paused", "0").Err())
		if name != "sleepy" {
			must(client.HSet(ctx, "friend:"+name+":beat", "host", "studio", "session", name+"-1", "at", "1").Err())
		}
		roles := ""
		switch name {
		case "stella", "fran", "sleepy":
			roles = "may-hold"
		case "johnny":
			roles = "builder,may-hold"
		case "rowan":
			roles = "coordinator"
		}
		must(client.HSet(ctx, "friend:"+name+":roles", "roles", roles).Err())
	}
	must(client.HSet(ctx, "friend:fran:desired", "slots", "2").Err())
	must(client.ZAdd(ctx, "friend:fran:cards:working",
		redis.Z{Score: 9e12, Member: "other/x1/1"}, redis.Z{Score: 9e12, Member: "other/x2/1"}).Err())

	prs := map[int]struct {
		head   string
		author string
	}{
		101: {rdHead('a'), "johnny"},
		102: {rdHead('b'), "stella"},
		103: {rdHead('d'), "rowan"},
		104: {rdHead('e'), "rowan"},
	}
	for n, pr := range prs {
		must(client.HSet(ctx, "s:"+rdSprint+":pr:"+rdRepo+":"+strconv.Itoa(n), "head", pr.head, "author", pr.author).Err())
	}
	// f's typed HOLD on 103 at the earlier head c; 103 is now at head d.
	must(client.HSet(ctx, "s:"+rdSprint+":disp:"+rdRepo+":103",
		f+"@"+rdHead('c'), "HOLD 4 https://example.test/mas-bandwidth/nova-tools/pull/103#hold").Err())

	push := func(id string, kind task.Kind, pr int) {
		t.Helper()
		req := task.PushRequest{Sprint: rdSprint, ID: id, Kind: kind, Title: id + " title", To: f, Actor: "test"}
		if pr != 0 {
			req.Repo, req.PR, req.Head = rdRepo, pr, prs[pr].head
			req.Ref = "https://example.test/mas-bandwidth/nova-tools/pull/" + strconv.Itoa(pr)
		}
		status, err := task.Push(ctx, st, req)
		must(err)
		if status != task.PushCreated {
			t.Fatalf("push %s: %s", id, status)
		}
	}
	push("read-a", task.KindRead, 101)
	push("read-b", task.KindRead, 102)
	push("build-1", task.KindWork, 0)
	push("fix-1", task.KindFix, 0)
	push("reread-c", task.KindRead, 103)
	push("build-2", task.KindWork, 0)
	push("read-d", task.KindRead, 104)

	tokens := map[string]string{}
	for _, id := range []string{"build-2", "read-d"} {
		claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: rdSprint, ID: id, As: f, Actor: f})
		must(err)
		if !ok {
			t.Fatalf("take %s refused", id)
		}
		tokens[id] = claim.Token
	}
	if status, err := task.Beat(ctx, st, task.BeatRequest{Sprint: rdSprint, ID: "build-2", Token: tokens["build-2"], Actor: f}); err != nil || status != task.BeatWorking {
		t.Fatalf("start ack build-2: %v %v", status, err)
	}
	rdCounts(t, client, f, 5, 2)
	return tokens
}

func rdCounts(t *testing.T, client *redis.Client, f string, open, leased int64) {
	t.Helper()
	ctx := context.Background()
	gotOpen := client.ZCard(ctx, "s:"+rdSprint+":open:"+f).Val()
	gotLeased := client.ZCard(ctx, "friend:"+f+":cards:working").Val()
	if gotOpen != open || gotLeased != leased {
		t.Fatalf("%s: open %d leased %d, want open %d leased %d", f, gotOpen, gotLeased, open, leased)
	}
}

// rdQueueOf names the friend whose open queue holds id.
func rdQueueOf(t *testing.T, client *redis.Client, id string) string {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"emma", "stella", "johnny", "fran", "sleepy", "rowan", "kim"} {
		if _, err := client.ZScore(ctx, "s:"+rdSprint+":open:"+name, id).Result(); err == nil {
			return name
		}
	}
	return ""
}

// rdAssertMoved is the shared outcome of both controls: f ends with 0 open and
// 0 leased; every moved task sits on a live non-author may-hold reader with
// free width (reads) or on the other builder (builds and fixes), carries the
// marker, and no task reached the coordinator; the two leases are closed with
// evidence and requeued under a fence; the HOLD re-read is one release task.
func rdAssertMoved(t *testing.T, st *store.Store, client *redis.Client, f, why string, tokens map[string]string) {
	t.Helper()
	ctx := context.Background()
	rdCounts(t, client, f, 0, 0)
	marker := "[moved from " + f + ": " + why + "]"
	authors := map[string]string{"read-a": "johnny", "read-b": "stella", "read-d": "rowan"}
	for _, id := range []string{"read-a", "read-b", "read-d"} {
		owner := rdQueueOf(t, client, id)
		if owner != "stella" && owner != "johnny" {
			t.Fatalf("%s is on %q, want a live may-hold reader with free width (stella or johnny)", id, owner)
		}
		if owner == authors[id] {
			t.Fatalf("%s moved to its author %s", id, owner)
		}
	}
	for _, id := range []string{"build-1", "fix-1", "build-2"} {
		if owner := rdQueueOf(t, client, id); owner != "johnny" {
			t.Fatalf("%s is on %q, want the other builder johnny", id, owner)
		}
	}
	for _, id := range []string{"read-a", "read-b", "read-d", "build-1", "fix-1", "build-2"} {
		key := "task:" + id
		if state := client.HGet(ctx, key, "state").Val(); state != "open" {
			t.Fatalf("%s state %q, want open", id, state)
		}
		if title := client.HGet(ctx, key, "title").Val(); !strings.Contains(title, marker) {
			t.Fatalf("%s title %q lacks %q", id, title, marker)
		}
	}
	// The coordinator was never needed: nothing on its queue, no wake for it.
	if n := client.ZCard(ctx, "s:"+rdSprint+":open:rowan").Val(); n != 0 {
		t.Fatalf("coordinator queue holds %d tasks", n)
	}
	if client.Exists(ctx, "friend:rowan:wake").Val() != 0 {
		t.Fatal("the coordinator was woken")
	}
	for _, name := range []string{"fran", "sleepy"} {
		if n := client.ZCard(ctx, "s:"+rdSprint+":open:"+name).Val(); n != 0 {
			t.Fatalf("%s (full or down) received %d tasks", name, n)
		}
	}

	// The two leases: fenced, closed with evidence, requeued.
	for id, token := range tokens {
		status, err := task.Done(ctx, st, task.DoneRequest{Sprint: rdSprint, ID: id, Token: token,
			Evidence: "late", Verdict: "APPROVE", Score: "9", Head: rdHead('e')})
		if err != nil || status != task.DoneFenced {
			t.Fatalf("old token of %s: %v %v, want FENCED", id, status, err)
		}
	}
	closed := map[string]bool{}
	entries, err := client.XRange(ctx, "s:"+rdSprint+":log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Values["kind"] == "task lease-close" {
			ev, _ := e.Values["evidence"].(string)
			if !strings.Contains(ev, f) || !strings.Contains(ev, why) {
				t.Fatalf("lease-close evidence %q does not name %s and %q", ev, f, why)
			}
			closed[e.Values["id"].(string)] = true
		}
	}
	if !closed["build-2"] || !closed["read-d"] || len(closed) != 2 {
		t.Fatalf("lease-close receipts %v, want build-2 and read-d", closed)
	}

	// The carried HOLD: the re-read is cancelled and exactly one release task
	// exists, on a may-hold non-author reader other than f, at the current head.
	if state := client.HGet(ctx, "task:reread-c", "state").Val(); state != "cancelled" {
		t.Fatalf("reread-c state %q, want cancelled", state)
	}
	releases := 0
	for _, key := range client.Keys(ctx, "task:release-*").Val() {
		releases++
		rel := client.HGetAll(ctx, key).Val()
		if rel["pr"] != "103" || rel["head"] != rdHead('d') || rel["state"] != "open" {
			t.Fatalf("release task %s = %v", key, rel)
		}
		if !strings.Contains(rel["title"], marker) || !strings.Contains(rel["title"], "--releases") {
			t.Fatalf("release title %q lacks the marker or the --releases recipe", rel["title"])
		}
		owner := rdQueueOf(t, client, strings.TrimPrefix(key, "task:"))
		if owner != "stella" && owner != "johnny" {
			t.Fatalf("release task on %q", owner)
		}
	}
	if releases != 1 {
		t.Fatalf("%d release tasks, want 1", releases)
	}

	// The row prints the state.
	snap, err := table.ReadNamed(ctx, client, rdSprint)
	if err != nil {
		t.Fatal(err)
	}
	want := "state: " + map[string]string{"out of credits": life.StateOutOfCredits, "down": life.StateDown}[why]
	var row string
	for _, line := range strings.Split(snap.Render(), "\n") {
		if strings.HasPrefix(line, "friend:"+f+" ") {
			row = line
		}
	}
	if !strings.Contains(row, want) {
		t.Fatalf("friend row %q does not print %q", row, want)
	}
}

// TestOutOfCreditsRedistributesSameTick: the keeper marks the friend
// out-of-credits, and the very next tick (one Redis Function call, no model,
// no coordinator) moves every open task and closes every lease.
func TestOutOfCreditsRedistributesSameTick(t *testing.T) {
	t.Parallel()

	st, client := rdRedis(t)
	ctx := context.Background()
	tokens := rdFixture(t, st, client, "emma")

	until := time.Now().Add(3 * time.Hour)
	if err := life.SetState(ctx, st, "emma", life.StateOutOfCredits, until, "usage limit", "keeper", ""); err != nil {
		t.Fatal(err)
	}
	moves, err := life.Redistribute(ctx, st, rdRoster, "rowan", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 1 || moves[0].Friend != "emma" || moves[0].State != life.StateOutOfCredits ||
		moves[0].Moved != 6 || moves[0].Leases != 2 || moves[0].Released != 1 || moves[0].Unrouted != 0 || moves[0].Pending {
		t.Fatalf("moves %+v", moves)
	}
	t.Log(moves[0].Line())
	rdAssertMoved(t, st, client, "emma", "out of credits", tokens)

	// A second tick is a no-op: nothing is left to move.
	again, err := life.Redistribute(ctx, st, rdRoster, "rowan", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range again {
		if m.Moved+m.Leases+m.Released+m.Unrouted != 0 {
			t.Fatalf("second tick moved again: %+v", m)
		}
	}
}

// TestDownFriendRedistributesAfterOneTick: a friend whose presence key
// expired with open work is marked down on the first tick and redistributed
// on the next, with no human noticing.
func TestDownFriendRedistributesAfterOneTick(t *testing.T) {
	t.Parallel()

	st, client := rdRedis(t)
	ctx := context.Background()
	tokens := rdFixture(t, st, client, "emma")
	if err := client.Del(ctx, "friend:emma:beat").Err(); err != nil {
		t.Fatal(err)
	}

	first, err := life.Redistribute(ctx, st, rdRoster, "rowan", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Friend != "emma" || !first[0].Pending || first[0].Moved != 0 {
		t.Fatalf("first tick %+v, want emma pending with nothing moved", first)
	}
	rdCounts(t, client, "emma", 5, 2)

	second, err := life.Redistribute(ctx, st, rdRoster, "rowan", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].State != life.StateDown || second[0].Moved != 6 ||
		second[0].Leases != 2 || second[0].Released != 1 || second[0].Pending {
		t.Fatalf("second tick %+v", second)
	}
	t.Log(second[0].Line())
	rdAssertMoved(t, st, client, "emma", "down", tokens)
}

// TestFriendBackBeforeSecondTickKeepsWork: a friend whose beat returns before
// the second tick keeps its work and its down mark is cleared.
func TestFriendBackBeforeSecondTickKeepsWork(t *testing.T) {
	t.Parallel()

	st, client := rdRedis(t)
	ctx := context.Background()
	rdFixture(t, st, client, "emma")
	if err := client.Del(ctx, "friend:emma:beat").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := life.Redistribute(ctx, st, rdRoster, "rowan", ""); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:emma:beat", "host", "studio", "session", "emma-2", "at", "2").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := life.Redistribute(ctx, st, rdRoster, "rowan", ""); err != nil {
		t.Fatal(err)
	}
	rdCounts(t, client, "emma", 5, 2)
	if client.Exists(ctx, "friend:emma:state").Val() != 0 {
		t.Fatal("down mark survived the friend's return")
	}
}
