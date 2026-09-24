package assign_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/assign"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

const (
	asSprint = "control-3103"
	asRepo   = "nova-tools"
)

// asRedis starts a throwaway redis-server with the nova_sprint library loaded.
func asRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable; run this control on a Redis bench: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logf, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logf.Close() })
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", strings.TrimPrefix(addr, "127.0.0.1:"),
		"--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(30 * time.Second)
	for client.Ping(context.Background()).Err() != nil {
		if time.Now().After(deadline) {
			t.Fatal("throwaway redis did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

func asHead(n int) string { return fmt.Sprintf("%040x", 0xabc000+n) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// asFixture: friends emma (the source), stella, johnny, fran, kim and rowan,
// all UP with 8 slots; PRs 1..n authored alternately by johnny and rowan;
// one read per PR on emma's queue, id read-<n>.
func asFixture(t *testing.T, st *store.Store, client *redis.Client, n int) {
	t.Helper()
	ctx := context.Background()
	must(t, client.SAdd(ctx, "sprints", asSprint).Err())
	must(t, client.HSet(ctx, "s:"+asSprint, "status", "open").Err())
	for _, name := range []string{"emma", "stella", "johnny", "fran", "kim", "rowan"} {
		must(t, client.SAdd(ctx, "friends", name).Err())
		must(t, client.HSet(ctx, "friend:"+name+":desired", "slots", "8", "paused", "0").Err())
		must(t, client.HSet(ctx, "friend:"+name+":beat", "host", "studio", "at", "1").Err())
	}
	for i := 1; i <= n; i++ {
		author := "johnny"
		if i%2 == 0 {
			author = "rowan"
		}
		must(t, client.HSet(ctx, "s:"+asSprint+":pr:"+asRepo+":"+strconv.Itoa(i), "head", asHead(i), "author", author).Err())
		asPush(t, st, "read-"+strconv.Itoa(i), task.KindRead, i, "emma")
	}
}

func asPush(t *testing.T, st *store.Store, id string, kind task.Kind, pr int, to string) {
	t.Helper()
	req := task.PushRequest{Sprint: asSprint, ID: id, Kind: kind, Title: id + " title", To: to, Actor: "test"}
	if pr != 0 {
		req.Repo, req.PR, req.Head = asRepo, pr, asHead(pr)
		req.Ref = "https://example.test/mas-bandwidth/nova-tools/pull/" + strconv.Itoa(pr)
	}
	status, err := task.Push(context.Background(), st, req)
	must(t, err)
	if status != task.PushCreated {
		t.Fatalf("push %s: %s", id, status)
	}
}

// asControlLines is the control's 50 lines: 46 plain moves spread over four
// readers, plus one line of each refusal a batch must answer the same way a
// single assign does (AUTHOR, LIVE, NOTFOUND, DEDUP on the same identity
// twice in one batch).
func asControlLines(t *testing.T, st *store.Store) []assign.Line {
	t.Helper()
	readers := []string{"stella", "fran", "kim", "stella"}
	var lines []assign.Line
	for i := 1; i <= 46; i++ {
		lines = append(lines, assign.Line{Task: "read-" + strconv.Itoa(i), Friend: readers[i%4]})
	}
	lines = append(lines,
		assign.Line{Task: "read-47", Friend: "johnny"}, // johnny authored PR 47
		assign.Line{Task: "read-48", Friend: "stella"}, // claimed by emma below: LIVE
		assign.Line{Task: "read-nope", Friend: "stella"},
		assign.Line{Task: "read-dup-1", Friend: "fran"}, // same identity as read-1, which line 1 gave fran
	)
	claim, ok, err := task.Take(context.Background(), st, task.TakeRequest{Sprint: asSprint, ID: "read-48", As: "emma", Actor: "emma"})
	must(t, err)
	if !ok || claim.Token == "" {
		t.Fatal("take read-48 refused")
	}
	return lines
}

// asReceipts reads the sprint log without the per-call fields (at, idem):
// what a batch and n single calls must agree on.
func asReceipts(t *testing.T, client *redis.Client, kind string) []string {
	t.Helper()
	entries, err := client.XRange(context.Background(), "s:"+asSprint+":log", "-", "+").Result()
	must(t, err)
	var out []string
	for _, e := range entries {
		if e.Values["kind"] != kind {
			continue
		}
		var b strings.Builder
		for _, f := range []string{"kind", "id", "from", "to", "attempt", "token_sha", "actor", "reason", "evidence"} {
			fmt.Fprintf(&b, "%s=%v ", f, e.Values[f])
		}
		out = append(out, b.String())
	}
	sort.Strings(out)
	return out
}

func asQueues(t *testing.T, client *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	where := map[string]string{}
	for _, name := range []string{"emma", "stella", "johnny", "fran", "kim", "rowan"} {
		ids, err := client.ZRange(ctx, "s:"+asSprint+":open:"+name, 0, -1).Result()
		must(t, err)
		for _, id := range ids {
			where[id] = name
		}
	}
	return where
}

// countingHook counts every command the client sends, pipelined or not.
type countingHook struct{ n atomic.Int64 }

func (h *countingHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *countingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}
func (h *countingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmds)
	}
}

var fcallCalls = regexp.MustCompile(`(?m)^cmdstat_fcall:calls=(\d+),`)

// TestControl46AssignStdinIsOneRoundTrip is control 46 (spec #2756 v6 4.6):
// 50 `<task> <friend>` lines are ONE Redis round trip (one FCALL counted on
// the client and on the server), and the receipts and queues they leave are
// those of 50 single assigns run one by one on a second throwaway Redis.
// The wall time is logged, not asserted (CI-WAITS: assert the event, not the
// clock); on the Studio it is a few milliseconds against the interim's ~60 s
// per call.
func TestControl46AssignStdinIsOneRoundTrip(t *testing.T) {
	ctx := context.Background()

	st, client := asRedis(t)
	asFixture(t, st, client, 50)
	asPush(t, st, "read-dup-1", task.KindRead, 1, "emma")
	lines := asControlLines(t, st)
	if len(lines) != 50 {
		t.Fatalf("control wants 50 lines, built %d", len(lines))
	}

	admin := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
	t.Cleanup(func() { _ = admin.Close() })
	must(t, admin.ConfigResetStat(ctx).Err())
	hook := &countingHook{}
	client.AddHook(hook)
	start := time.Now()
	batch, err := assign.Batch(ctx, st, asSprint, lines, "", "rowan", "control-46")
	took := time.Since(start)
	must(t, err)
	if got := hook.n.Load(); got != 1 {
		t.Fatalf("assign --stdin of 50 lines sent %d commands, want ONE round trip", got)
	}
	info, err := admin.Info(ctx, "commandstats").Result()
	must(t, err)
	m := fcallCalls.FindStringSubmatch(info)
	if m == nil || m[1] != "1" {
		t.Fatalf("server counted fcall %v, want calls=1", m)
	}
	t.Logf("CONTROL46 lines=50 round_trips=1 wall=%s", took)

	want := map[string]assign.Status{"read-47": assign.Author, "read-48": assign.Live, "read-nope": assign.NotFound, "read-dup-1": assign.Dedup}
	for i, r := range batch {
		if r.Task != lines[i].Task || r.Friend != lines[i].Friend {
			t.Fatalf("receipt %d is %s -> %s, want input order %s -> %s", i, r.Task, r.Friend, lines[i].Task, lines[i].Friend)
		}
		w, special := want[r.Task]
		if !special {
			w = assign.Moved
		}
		if r.Status != w {
			t.Fatalf("%s: %s", r.Task, r)
		}
	}
	if got := batch[49].String(); !strings.HasPrefix(got, "DEDUP read-dup-1: fran holds open read-1 at "+asHead(1)) {
		t.Fatalf("dedup line %q", got)
	}

	// The same 50 lines as 50 single assigns on a second throwaway Redis.
	st1, client1 := asRedis(t)
	asFixture(t, st1, client1, 50)
	asPush(t, st1, "read-dup-1", task.KindRead, 1, "emma")
	singleLines := asControlLines(t, st1)
	var single []assign.Receipt
	for _, l := range singleLines {
		r, err := assign.Batch(ctx, st1, asSprint, []assign.Line{l}, "", "rowan", "control-46")
		must(t, err)
		single = append(single, r...)
	}
	for i := range batch {
		if batch[i] != single[i] {
			t.Fatalf("line %d: batch %s, single %s", i+1, batch[i], single[i])
		}
	}
	rb, rs := asReceipts(t, client, "task assign"), asReceipts(t, client1, "task assign")
	if len(rb) != 50 || strings.Join(rb, "\n") != strings.Join(rs, "\n") {
		t.Fatalf("receipts differ: batch %d, single %d\nbatch:\n%s\nsingle:\n%s", len(rb), len(rs),
			strings.Join(rb, "\n"), strings.Join(rs, "\n"))
	}
	qb, qs := asQueues(t, client), asQueues(t, client1)
	if fmt.Sprint(qb) != fmt.Sprint(qs) {
		t.Fatalf("queues differ:\nbatch  %v\nsingle %v", qb, qs)
	}
	title, err := client.HGet(ctx, "s:"+asSprint+":task:read-2", "title").Result()
	must(t, err)
	if !strings.HasSuffix(title, "[moved from emma: assign]") {
		t.Fatalf("moved title %q carries no marker", title)
	}
	for _, g := range []string{"stella", "fran", "kim"} {
		if n := client.LLen(ctx, "friend:"+g+":wake").Val(); n != 1 {
			t.Fatalf("%s has %d wakes, want one per batch", g, n)
		}
	}
}

// TestAssignDedupOnFullHead: the canonical read identity is (repo, PR, full
// head, friend). A friend with a closed read at that head, or a typed line at
// that head, never receives it again; a new head is a new identity.
func TestAssignDedupOnFullHead(t *testing.T) {
	ctx := context.Background()
	st, client := asRedis(t)
	asFixture(t, st, client, 3)
	// stella answered read-1 at PR 1's head: a closed same-head read.
	moved, err := assign.Batch(ctx, st, asSprint, []assign.Line{{Task: "read-1", Friend: "stella"}}, "", "rowan", "i1")
	must(t, err)
	if moved[0].Status != assign.Moved {
		t.Fatalf("setup: %s", moved[0])
	}
	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: asSprint, ID: "read-1", As: "stella", Actor: "stella"})
	must(t, err)
	if !ok {
		t.Fatal("stella take refused")
	}
	done, err := task.Done(ctx, st, task.DoneRequest{Sprint: asSprint, ID: "read-1", Token: claim.Token,
		Evidence: "HOLD 8 at head", Verdict: "HOLD", Score: "8", Head: asHead(1), Actor: "stella"})
	must(t, err)
	if done != task.DoneClosed {
		t.Fatalf("done: %s", done)
	}
	// kim posted a typed line at PR 2's head without a task.
	must(t, client.HSet(ctx, "s:"+asSprint+":disp:"+asRepo+":2", "kim@"+asHead(2), "APPROVE 9 by lane record").Err())

	asPush(t, st, "reread-1", task.KindRead, 1, "emma") // same identity as read-1
	got, err := assign.Batch(ctx, st, asSprint, []assign.Line{
		{Task: "reread-1", Friend: "stella"},
		{Task: "read-2", Friend: "kim"},
		{Task: "read-2", Friend: "fran"},
	}, "", "rowan", "i2")
	must(t, err)
	// task done recorded stella's typed line at that head too.
	if got[0].Status != assign.Dedup || !strings.Contains(got[0].Detail, "stella posted a typed line at "+asHead(1)) {
		t.Fatalf("answered same-head read: %s", got[0])
	}
	if got[1].Status != assign.Dedup || !strings.Contains(got[1].Detail, "kim posted a typed line at "+asHead(2)) {
		t.Fatalf("typed line at head: %s", got[1])
	}
	if got[2].Status != assign.Moved {
		t.Fatalf("fran has no line at that head: %s", got[2])
	}

	// Without the typed line, the closed same-head task alone refuses it.
	must(t, client.HDel(ctx, "s:"+asSprint+":disp:"+asRepo+":1", "stella@"+asHead(1)).Err())
	got, err = assign.Batch(ctx, st, asSprint, []assign.Line{{Task: "reread-1", Friend: "stella"}}, "", "rowan", "i2b")
	must(t, err)
	if got[0].Status != assign.Dedup || !strings.Contains(got[0].Detail, "stella holds closed read-1 at "+asHead(1)) {
		t.Fatalf("closed same-head read: %s", got[0])
	}

	// PR 1 moves to a new head: a new identity, so stella may read it.
	must(t, client.HSet(ctx, "s:"+asSprint+":pr:"+asRepo+":1", "head", asHead(101)).Err())
	status, err := task.Push(ctx, st, task.PushRequest{Sprint: asSprint, ID: "reread-1-new", Kind: task.KindRead,
		Title: "re-read at new head", Repo: asRepo, PR: 1, Head: asHead(101), To: "emma", Actor: "test"})
	must(t, err)
	if status != task.PushCreated {
		t.Fatalf("push new head: %s", status)
	}
	got, err = assign.Batch(ctx, st, asSprint, []assign.Line{{Task: "reread-1-new", Friend: "stella"}}, "", "rowan", "i3")
	must(t, err)
	if got[0].Status != assign.Moved {
		t.Fatalf("new head is a new identity: %s", got[0])
	}
}

var asRoster = life.Roster{MayHold: []string{"stella", "johnny", "fran", "kim"}, Builders: []string{"johnny"}, Coordinator: "rowan"}

// TestRedistributeFromByKindAndTo: `redistribute --from emma --reason
// underfull --kind work,fix` moves only builds and fixes, each title carrying
// [moved from emma: underfull], and leaves the reads and the lease of an UP
// friend alone; `--to stella` then moves the reads stella may take and keeps
// the one she authored and the one she already holds, with a DEDUP line.
func TestRedistributeFromByKindAndTo(t *testing.T) {
	ctx := context.Background()
	st, client := asRedis(t)
	asFixture(t, st, client, 3)
	must(t, client.HSet(ctx, "s:"+asSprint+":pr:"+asRepo+":3", "author", "stella").Err())
	asPush(t, st, "build-1", task.KindWork, 0, "emma")
	asPush(t, st, "fix-1", task.KindFix, 0, "emma")
	asPush(t, st, "build-2", task.KindWork, 0, "emma")
	if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: asSprint, ID: "build-2", As: "emma", Actor: "emma"}); err != nil || !ok {
		t.Fatalf("take build-2: %v %v", ok, err)
	}
	asPush(t, st, "read-2-stella", task.KindRead, 2, "stella") // stella already holds PR 2's identity

	res, err := assign.From(ctx, st, assign.FromRequest{From: "emma", Reason: "underfull",
		Kinds: []string{"work", "fix"}, Roster: asRoster, Actor: "rowan", Idem: "r1"})
	must(t, err)
	if res.Moved != 2 || res.Leases != 0 || res.Kept != 0 {
		t.Fatalf("underfull by kind: %s", res.Line())
	}
	where := asQueues(t, client)
	for _, id := range []string{"build-1", "fix-1"} {
		if where[id] != "johnny" {
			t.Fatalf("%s on %q, want the builder johnny", id, where[id])
		}
		title := client.HGet(ctx, "s:"+asSprint+":task:"+id, "title").Val()
		if !strings.HasSuffix(title, "[moved from emma: underfull]") {
			t.Fatalf("%s title %q", id, title)
		}
	}
	for _, id := range []string{"read-1", "read-2", "read-3"} {
		if where[id] != "emma" {
			t.Fatalf("%s moved with --kind work,fix: on %q", id, where[id])
		}
	}
	if client.ZCard(ctx, "friend:emma:starting").Val() != 1 {
		t.Fatal("the lease of an UP friend was closed by a hand redistribute")
	}

	res, err = assign.From(ctx, st, assign.FromRequest{From: "emma", Reason: "rebalance", To: "stella",
		Roster: asRoster, Actor: "rowan", Idem: "r2"})
	must(t, err)
	if res.Moved != 1 || res.Kept != 2 {
		t.Fatalf("--to stella: %s %v", res.Line(), res.Events)
	}
	where = asQueues(t, client)
	if where["read-1"] != "stella" || where["read-2"] != "emma" || where["read-3"] != "emma" {
		t.Fatalf("--to stella placed %v", where)
	}
	var lines []string
	for _, e := range res.Events {
		lines = append(lines, e.String())
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "DEDUP read-2: stella holds open read-2-stella at "+asHead(2)) ||
		!strings.Contains(joined, "KEPT read-3 on emma") {
		t.Fatalf("events:\n%s", joined)
	}
}

// TestRedistributeFromDownFriendClosesLeases: the hand verb on a friend with
// no beat is the tick's redistribution: leases closed with evidence and
// requeued, titles marked with the given reason.
func TestRedistributeFromDownFriendClosesLeases(t *testing.T) {
	ctx := context.Background()
	st, client := asRedis(t)
	asFixture(t, st, client, 2)
	asPush(t, st, "build-1", task.KindWork, 0, "emma")
	if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: asSprint, ID: "build-1", As: "emma", Actor: "emma"}); err != nil || !ok {
		t.Fatalf("take build-1: %v %v", ok, err)
	}
	must(t, client.Del(ctx, "friend:emma:beat").Err())
	res, err := assign.From(ctx, st, assign.FromRequest{From: "emma", Reason: "away", Roster: asRoster, Actor: "rowan", Idem: "r3"})
	must(t, err)
	if res.Leases != 1 || res.Moved != 3 || res.Unrouted != 0 {
		t.Fatalf("down friend: %s", res.Line())
	}
	where := asQueues(t, client)
	if where["build-1"] != "johnny" || where["read-1"] == "johnny" || where["read-2"] == "rowan" {
		t.Fatalf("placed %v", where)
	}
	if n := client.ZCard(ctx, "s:"+asSprint+":open:emma").Val() + client.ZCard(ctx, "friend:emma:starting").Val(); n != 0 {
		t.Fatalf("emma still holds %d", n)
	}
	if title := client.HGet(ctx, "s:"+asSprint+":task:build-1", "title").Val(); !strings.HasSuffix(title, "[moved from emma: away]") {
		t.Fatalf("title %q", title)
	}
}

// TestRedistributeFromExpiredOutOfCreditsKeepsLease: the hand verb mirrors
// the tick's expired-window rule. An up friend whose out-of-credits until has
// passed keeps its live lease unfenced (state cleared, open tasks still move);
// an active out-of-credits window and a down friend still close it.
func TestRedistributeFromExpiredOutOfCreditsKeepsLease(t *testing.T) {
	for _, tc := range []struct {
		name   string
		until  time.Duration
		down   bool
		leases int
		state  string
	}{
		{name: "expired window, up", until: -time.Minute, leases: 0, state: ""},
		{name: "active window, up", until: 3 * time.Hour, leases: 1, state: life.StateOutOfCredits},
		{name: "expired window, down", until: -time.Minute, down: true, leases: 1, state: life.StateOutOfCredits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, client := asRedis(t)
			asFixture(t, st, client, 2)
			asPush(t, st, "build-1", task.KindWork, 0, "emma")
			if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: asSprint, ID: "build-1", As: "emma", Actor: "emma"}); err != nil || !ok {
				t.Fatalf("take build-1: %v %v", ok, err)
			}
			must(t, life.SetState(ctx, st, "emma", life.StateOutOfCredits, time.Now().Add(tc.until), "usage limit", "keeper", ""))
			if tc.down {
				must(t, client.Del(ctx, "friend:emma:beat").Err())
			}
			res, err := assign.From(ctx, st, assign.FromRequest{From: "emma", Reason: "hand", Roster: asRoster, Actor: "rowan", Idem: "oc-" + strconv.Itoa(int(tc.until))})
			must(t, err)
			key := "s:" + asSprint + ":task:build-1"
			fenced := client.HGet(ctx, key, "token").Val() == "fenced"
			owner := client.HGet(ctx, key, "owner").Val()
			if res.Leases != tc.leases || res.State != tc.state || fenced != (tc.leases == 1) {
				t.Fatalf("%s: %s fenced=%v owner=%s", tc.name, res.Line(), fenced, owner)
			}
			if tc.leases == 0 {
				if s := client.HGet(ctx, key, "state").Val(); s != "claimed" && s != "working" || owner != "emma" {
					t.Fatalf("%s: live lease moved: state=%s owner=%s", tc.name, s, owner)
				}
				if client.Exists(ctx, "friend:emma:state").Val() != 0 {
					t.Fatalf("%s: expired out-of-credits state not cleared", tc.name)
				}
			}
			if res.Moved < 2 {
				t.Fatalf("%s: open reads not moved: %s", tc.name, res.Line())
			}
		})
	}
}
