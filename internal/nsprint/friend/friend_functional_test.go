//go:build functional

package friend_test

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// fsRedis starts a throwaway redis-server on 127.0.0.1 with the nova_sprint
// library loaded (the shape of life's rdRedis; no real host is reached).
func fsRedis(t *testing.T) (*store.Store, *redis.Client) {
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

// fsRoster is the roles fsFriends seeds: who may hold, who builds, who
// coordinates.
var fsRoster = struct {
	MayHold, Builders []string
	Coordinator       string
}{
	MayHold:     []string{"stella", "johnny"},
	Builders:    []string{"johnny"},
	Coordinator: "rowan",
}

func fsHead(c byte) string { return strings.Repeat(string(c), 40) }

// fsFriends registers the sprint and the friends: each is UP with 8 slots.
func fsFriends(t *testing.T, client *redis.Client, names ...string) {
	t.Helper()
	ctx := context.Background()
	fsMust(t, client.SAdd(ctx, "sprints", fsSprint).Err())
	fsMust(t, client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: fsSprint}).Err())
	fsMust(t, client.HSet(ctx, "s:"+fsSprint, "status", "open").Err())
	for _, name := range names {
		fsMust(t, client.SAdd(ctx, "friends", name).Err())
		fsMust(t, client.HSet(ctx, "friend:"+name+":desired", "slots", "8", "machine", "studio", "paused", "0").Err())
		fsMust(t, client.HSet(ctx, "friend:"+name+":beat", "host", "studio", "session", name+"-1", "at", "1").Err())
		var roles []string
		for _, reader := range fsRoster.MayHold {
			if reader == name {
				roles = append(roles, "may-hold")
			}
		}
		for _, builder := range fsRoster.Builders {
			if builder == name {
				roles = append(roles, "builder")
			}
		}
		if fsRoster.Coordinator == name {
			roles = append(roles, "coordinator")
		}
		fsMust(t, client.HSet(ctx, "friend:"+name+":roles", "roles", strings.Join(roles, ",")).Err())
	}
}

// fsPush puts one task on f's queue; pr 0 is a build or fix with no PR.
func fsPush(t *testing.T, st *store.Store, f, id string, kind task.Kind, pr int, head string) {
	t.Helper()
	req := task.PushRequest{Sprint: fsSprint, ID: id, Kind: kind, Title: id + " title", To: f, Actor: "test"}
	if pr != 0 {
		req.Repo, req.PR, req.Head = fsRepo, pr, head
		req.Ref = "https://example.test/mas-bandwidth/nova-tools/pull/" + strconv.Itoa(pr)
	}
	status, err := task.Push(context.Background(), st, req)
	fsMust(t, err)
	if status != task.PushCreated {
		t.Fatalf("push %s: %s", id, status)
	}
}

func fsOpen(client *redis.Client, f string) int64 {
	return client.ZCard(context.Background(), "s:"+fsSprint+":open:"+f).Val()
}

func fsLeased(client *redis.Client, f string) int64 {
	ctx := context.Background()
	return client.ZCard(ctx, "friend:"+f+":cards:working").Val()
}

// fsOutbox counts friend:outbox entries of one kind for one friend.
func fsOutbox(t *testing.T, client *redis.Client, f, kind string) []map[string]any {
	t.Helper()
	entries, err := client.XRange(context.Background(), friend.OutboxKey, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range entries {
		if e.Values["friend"] == f && e.Values["kind"] == kind {
			out = append(out, e.Values)
		}
	}
	return out
}

func fsState(client *redis.Client, f string) map[string]string {
	return client.HGetAll(context.Background(), "friend:"+f+":state").Val()
}

// TestControl43 (#2756 v6 control 43 through #3101's verbs): a fixture friend
// with 5 open tasks and 2 leases reports out-of-credits with `friend report`;
// one sweep later it has 0 open and 0 leased, reads sit on non-author UP
// readers with free width, builds on another builder, every title carries
// the marker and the carried-hold re-read is one release task.
func TestControl43(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "emma", "stella", "johnny", "rowan")
	prs := map[int][2]string{101: {fsHead('a'), "johnny"}, 102: {fsHead('b'), "stella"}, 103: {fsHead('d'), "rowan"}}
	for n, pr := range prs {
		fsMust(t, client.HSet(ctx, "s:"+fsSprint+":pr:"+fsRepo+":"+strconv.Itoa(n), "head", pr[0], "author", pr[1]).Err())
	}
	fsMust(t, client.HSet(ctx, "s:"+fsSprint+":disp:"+fsRepo+":103", "emma@"+fsHead('c'), "HOLD 4 hold at c").Err())
	fsPush(t, st, "emma", "read-a", task.KindRead, 101, prs[101][0])
	fsPush(t, st, "emma", "read-b", task.KindRead, 102, prs[102][0])
	fsPush(t, st, "emma", "build-1", task.KindWork, 0, "")
	fsPush(t, st, "emma", "fix-1", task.KindFix, 0, "")
	fsPush(t, st, "emma", "reread-c", task.KindRead, 103, prs[103][0])
	fsPush(t, st, "emma", "build-2", task.KindWork, 0, "")
	fsPush(t, st, "emma", "build-3", task.KindWork, 0, "")
	for _, id := range []string{"build-2", "build-3"} {
		if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: fsSprint, ID: id, As: "emma", Actor: "emma"}); err != nil || !ok {
			t.Fatalf("take %s: %v %v", id, ok, err)
		}
	}
	if fsOpen(client, "emma") != 5 || fsLeased(client, "emma") != 2 {
		t.Fatalf("fixture: open %d leased %d", fsOpen(client, "emma"), fsLeased(client, "emma"))
	}

	until := time.Now().Add(3 * time.Hour).Truncate(time.Millisecond)
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "emma", State: friend.StateOutOfCredits,
		Until: until, Reason: "usage limit", Actor: "emma-keeper"}))
	state := fsState(client, "emma")
	if state["state"] != friend.StateOutOfCredits || state["until"] != strconv.FormatInt(until.UnixMilli(), 10) || state["rung"] != "0" {
		t.Fatalf("state after report: %v", state)
	}

	ladder := &friend.Ladder{Store: st, Actor: "reconciler"}
	res, err := ladder.Sweep(ctx)
	fsMust(t, err)
	_ = res

	// friend show prints the state, the reset and the counts from the store.
	rows, err := friend.Show(ctx, st, "emma")
	fsMust(t, err)
	if len(rows) != 1 {
		t.Fatalf("show rows %v", rows)
	}
	line := rows[0].Line()
	for _, want := range []string{"friend=emma", "state=out-of-credits", "until=", "wake=none"} {
		if !strings.Contains(line, want) {
			t.Fatalf("show line %q lacks %q", line, want)
		}
	}
	t.Log(line)
}

// fsIdleFixture: johnny is UP with 8 slots, living 0 and three open tasks (an
// idle friend); stella and rowan can receive his work.
func fsIdleFixture(t *testing.T, st *store.Store, client *redis.Client, f string) {
	t.Helper()
	fsFriends(t, client, f, "stella", "johnny", "rowan")
	for _, id := range []string{"build-a", "build-b", "build-c"} {
		fsPush(t, st, f, id, task.KindWork, 0, "")
	}
}

// fsSweepTo runs sweeps until f's rung reaches want, failing after limit.
func fsSweepTo(t *testing.T, l *friend.Ladder, client *redis.Client, f string, want, limit int) int {
	t.Helper()
	for n := 1; n <= limit; n++ {
		if _, err := l.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if fsState(client, f)["rung"] == strconv.Itoa(want) {
			return n
		}
	}
	t.Fatalf("%s did not reach rung %d in %d sweeps: %v", f, want, limit, fsState(client, f))
	return 0
}

type fsRepairer struct{ calls []string }

func (r *fsRepairer) Repair(_ context.Context, f string, wp friend.WakePath) (string, error) {
	r.calls = append(r.calls, f+" "+wp.String())
	return "repaired " + wp.Unit, nil
}

// TestControl45 (#2756 v6 control 45): a friend with a unit wake path, held
// idle: rung 1 nudges, rung 2 repairs that unit and sends one wake, rung 3
// redistributes. A take at rung 2 stops the ladder. A human friend's rung 2
// sends one notice to its channel. No key or row ever says asleep.
func TestControl45(t *testing.T) {
	t.Parallel()

	policy := friend.Policy{IdleTicks: 3, UnderfullTicks: 3}

	t.Run("unit", func(t *testing.T) {
		st, client := fsRedis(t)
		ctx := context.Background()
		fsIdleFixture(t, st, client, "kim")
		wp, err := friend.ParseWakePath("unit:com.nova.loop.wake-serve-kim@studio", "")
		fsMust(t, err)
		fsMust(t, friend.SetWakePath(ctx, st, "kim", wp, "config", ""))
		repair := &fsRepairer{}
		l := &friend.Ladder{Store: st, Actor: "reconciler", Policy: policy, Repair: repair}

		if n := fsSweepTo(t, l, client, "kim", 1, 10); n != 3 {
			t.Fatalf("rung 1 after %d sweeps, want 3 (idle_ticks)", n)
		}
		if got := fsState(client, "kim"); got["state"] != friend.StateIdle {
			t.Fatalf("state %v", got)
		}
		if len(fsOutbox(t, client, "kim", "nudge")) != 1 || len(fsOutbox(t, client, "kim", "wake")) != 0 {
			t.Fatal("rung 1 is one nudge and no wake")
		}
		fsSweepTo(t, l, client, "kim", 2, 3)
		if len(fsOutbox(t, client, "kim", "wake")) != 1 || client.LLen(ctx, "friend:kim:wake").Val() != 1 {
			t.Fatal("rung 2 sends one wake on the wake key")
		}
		if len(repair.calls) != 1 || repair.calls[0] != "kim unit:com.nova.loop.wake-serve-kim@studio" {
			t.Fatalf("repair calls %v, want one for that unit", repair.calls)
		}
		if fsOpen(client, "kim") != 3 {
			t.Fatal("nothing moves before rung 3")
		}
		fsSweepTo(t, l, client, "kim", 3, 3)
		if len(fsOutbox(t, client, "kim", "wake")) != 1 || len(repair.calls) != 1 {
			t.Fatal("rung 3 sent a second wake")
		}
		// With nothing open (kim's queue emptied by hand, as her own takes
		// would), the next sweep clears the ladder: the friend is up.
		fsMust(t, client.Del(ctx, "s:"+fsSprint+":open:kim").Err())
		if _, err := l.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if client.Exists(ctx, "friend:kim:state").Val() != 0 {
			t.Fatalf("state survived an empty queue: %v", fsState(client, "kim"))
		}
		fsNoAsleep(t, st, client)
	})

	t.Run("take at rung 2 stops the ladder", func(t *testing.T) {
		st, client := fsRedis(t)
		ctx := context.Background()
		fsIdleFixture(t, st, client, "kim")
		wp, err := friend.ParseWakePath("unit:com.nova.loop.wake-serve-kim@studio", "")
		fsMust(t, err)
		fsMust(t, friend.SetWakePath(ctx, st, "kim", wp, "config", ""))
		l := &friend.Ladder{Store: st, Actor: "reconciler", Policy: policy}
		fsSweepTo(t, l, client, "kim", 2, 10)
		if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: fsSprint, ID: "build-a", As: "kim", Actor: "kim"}); err != nil || !ok {
			t.Fatalf("take: %v %v", ok, err)
		}
		if _, err := l.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if got := fsState(client, "kim"); got["rung"] != "0" {
			t.Fatalf("a take at rung 2 left %v, want rung 0", got)
		}
		for i := 0; i < 20; i++ {
			if _, err := l.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if got := fsState(client, "kim"); got["rung"] == "3" {
				t.Fatalf("rung 3 with living > 0: %v", got)
			}
		}
		if fsOpen(client, "kim") != 2 || fsLeased(client, "kim") != 1 {
			t.Fatalf("work moved off a friend with a live child: open %d leased %d", fsOpen(client, "kim"), fsLeased(client, "kim"))
		}
	})

	t.Run("human", func(t *testing.T) {
		st, client := fsRedis(t)
		ctx := context.Background()
		fsIdleFixture(t, st, client, "glenn")
		wp, err := friend.ParseWakePath("human", "bus:To:Glenn")
		fsMust(t, err)
		fsMust(t, friend.SetWakePath(ctx, st, "glenn", wp, "config", ""))
		l := &friend.Ladder{Store: st, Actor: "reconciler", Policy: policy, Repair: &fsRepairer{}}
		fsSweepTo(t, l, client, "glenn", 2, 10)
		// Stay at rung 2 for the rest of its window: still one notice.
		for i := 0; i < 2; i++ {
			if _, err := l.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
		}
		notices := fsOutbox(t, client, "glenn", "notice")
		if len(notices) != 1 || notices[0]["channel"] != "bus:To:Glenn" || notices[0]["open"] != "3" {
			t.Fatalf("notices %v, want one to bus:To:Glenn with open=3", notices)
		}
		if client.Exists(ctx, "friend:glenn:wake").Val() != 0 || len(fsOutbox(t, client, "glenn", "wake")) != 0 {
			t.Fatal("a human wake path got a unit wake")
		}
		rows, err := friend.Show(ctx, st, "glenn")
		fsMust(t, err)
		if line := rows[0].Line(); !strings.Contains(line, "waiting on human since") || !strings.Contains(line, "wake=human:bus:To:Glenn") {
			t.Fatalf("show line %q", line)
		}
		fsNoAsleep(t, st, client)
	})
}

// fsNoAsleep: no key value and no show line says asleep.
func fsNoAsleep(t *testing.T, st *store.Store, client *redis.Client) {
	t.Helper()
	ctx := context.Background()
	for _, key := range client.Keys(ctx, "friend:*").Val() {
		if client.Type(ctx, key).Val() != "hash" {
			continue
		}
		for field, value := range client.HGetAll(ctx, key).Val() {
			if strings.Contains(field+value, "asleep") {
				t.Fatalf("%s %s=%s says asleep", key, field, value)
			}
		}
	}
	rows, err := friend.Show(ctx, st, "")
	fsMust(t, err)
	for _, r := range rows {
		if strings.Contains(r.Line(), "asleep") {
			t.Fatalf("show says asleep: %s", r.Line())
		}
	}
}

// TestLadderSurvivesRestart (Stella's HOLD7 on #3058: a process-local tick
// resets on restart): kill the reconciler at rung 2 and start a new one; the
// next sweep continues at rung 2 (not 0) with no duplicate wake, and rung 3
// comes on the same sweep count it would have without the restart. A replay
// of one sweep's call (a lost response) does not advance twice.
func TestLadderSurvivesRestart(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsIdleFixture(t, st, client, "kim")
	wp, err := friend.ParseWakePath("unit:com.nova.loop.wake-serve-kim@studio", "")
	fsMust(t, err)
	fsMust(t, friend.SetWakePath(ctx, st, "kim", wp, "config", ""))
	policy := friend.Policy{IdleTicks: 3, UnderfullTicks: 3}

	first := &friend.Ladder{Store: st, Actor: "reconciler", Policy: policy}
	if n := fsSweepTo(t, first, client, "kim", 2, 10); n != 6 {
		t.Fatalf("rung 2 after %d sweeps, want 6", n)
	}
	since := fsState(client, "kim")["since"]
	first = nil // the reconciler is killed; nothing of it survives but the store

	second := &friend.Ladder{Store: st, Actor: "reconciler", Policy: policy}
	if _, err := second.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got := fsState(client, "kim")
	if got["rung"] != "2" || got["state"] != friend.StateIdle || got["since"] != since {
		t.Fatalf("after restart %v, want idle rung 2 since %s", got, since)
	}
	if len(fsOutbox(t, client, "kim", "wake")) != 1 || client.LLen(ctx, "friend:kim:wake").Val() != 1 ||
		len(fsOutbox(t, client, "kim", "nudge")) != 1 {
		t.Fatal("the restarted reconciler repeated a rung action")
	}

	// A replayed call (same idem) is answered from the record, not applied.
	before := fsState(client, "kim")["ticks"]
	for i := 0; i < 2; i++ {
		if _, err := friend.Observe(ctx, st, friend.Observation{Friend: "kim", State: friend.StateIdle, Open: 3,
			Ticks: policy.IdleTicks}, "reconciler", "replay-1"); err != nil {
			t.Fatal(err)
		}
	}
	if after := fsState(client, "kim")["ticks"]; after == before {
		t.Fatalf("the first call did not count: ticks %s", after)
	} else if n, _ := strconv.Atoi(after); n != mustAtoi(t, before)+1 {
		t.Fatalf("a replayed idem counted twice: ticks %s -> %s", before, after)
	}

	// The next sweep reaches rung 3: 6 + 1 + 1 (replay) + 1 = 9 = 3 * idle_ticks.
	if _, err := second.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fsState(client, "kim"); got["rung"] != "3" && client.Exists(ctx, "friend:kim:state").Val() != 0 {
		t.Fatalf("rung 3 did not come on schedule: %v", got)
	}
	if len(fsOutbox(t, client, "kim", "wake")) != 1 {
		t.Fatal("a second wake after restart")
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// fsFCall runs one Function and returns its reply as strings; a Redis error
// is fatal.
func fsFCall(t *testing.T, client *redis.Client, fn string, args ...any) []string {
	t.Helper()
	reply, err := client.FCall(context.Background(), fn, nil, args...).Slice()
	if err != nil {
		t.Fatalf("%s %v: %v", fn, args, err)
	}
	out := make([]string, len(reply))
	for i, v := range reply {
		out[i] = fmt.Sprint(v)
	}
	return out
}

// fsEvent appends one event through ns_friend_event as f itself.
func fsEvent(t *testing.T, client *redis.Client, f, kind, cause string, at int64) string {
	t.Helper()
	got := fsFCall(t, client, life.FunctionEvent, f, kind, cause, strconv.FormatInt(at, 10), f)
	if len(got) != 2 || got[0] != "OK" {
		t.Fatalf("event %s %s: %v", f, kind, got)
	}
	return got[1]
}

// TestFriendStateAcceptsLifeStates: ns_friend_state accepts offline-model and
// wake-missed on the report branch, still refuses an unknown state, and
// both hold against the ladder's idle observation.
func TestFriendStateAcceptsLifeStates(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	for _, state := range []string{friend.StateOfflineModel, friend.StateWakeMissed} {
		got := fsFCall(t, client, friend.FunctionState, "ada", state, "", "test", "keeper", "i-"+state)
		if got[0] != "OK" || got[1] != state {
			t.Fatalf("%s: %v", state, got)
		}
		if fsState(client, "ada")["state"] != state {
			t.Fatalf("stored %v, want %s", fsState(client, "ada"), state)
		}
		step, err := friend.Observe(ctx, st, friend.Observation{Friend: "ada", State: friend.StateIdle, Open: 1, Ticks: 1}, "reconciler", "obs-"+state)
		fsMust(t, err)
		if step.Status != "HELD" || step.State != state {
			t.Fatalf("idle observation over %s: %+v, want HELD", state, step)
		}
	}
	if got := fsFCall(t, client, friend.FunctionState, "ada", "sleeping", "", "test", "keeper", "i-sleep"); got[0] != "INVALID" {
		t.Fatalf("sleeping: %v, want INVALID", got)
	}
}

// TestFriendStateIfMatch: a classifier write (actor life) is applied only
// when the stored state and idem still equal what it read, and only over a
// state it may overwrite; otherwise STALE or HELD and nothing is written.
func TestFriendStateIfMatch(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	life1 := "life:ada:e1:offline-model"
	if got := fsFCall(t, client, friend.FunctionState, "ada", friend.StateOfflineModel, "", "r", friend.ActorLife, life1, "up", ""); got[0] != "OK" {
		t.Fatalf("offline-model: %v", got)
	}
	until := time.Now().Add(time.Hour)
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "ada", State: friend.StateAway, Until: until, Actor: "ada", Idem: "away-1"}))
	away := fsState(client, "ada")
	logLen := len(fsCapLog(t, client))
	outLen := client.XLen(ctx, friend.OutboxKey).Val()

	got := fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e2:clear", friend.StateOfflineModel, life1)
	if !reflect.DeepEqual(got, []string{"STALE", friend.StateAway, "away-1"}) {
		t.Fatalf("clear over a newer away: %v", got)
	}
	got = fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e3:clear", friend.StateAway, "away-1")
	if !reflect.DeepEqual(got, []string{"HELD", friend.StateAway}) {
		t.Fatalf("matching clear over a report: %v", got)
	}
	if now := fsState(client, "ada"); !reflect.DeepEqual(now, away) {
		t.Fatalf("away changed: %v -> %v", away, now)
	}
	if len(fsCapLog(t, client)) != logLen {
		t.Fatal("a refused classifier write reached cap:log")
	}

	// Back at the classifier's own offline-model: a matching clear applies.
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "ada", State: "clear", Actor: "ada", Idem: "back-1"}))
	if got := fsFCall(t, client, friend.FunctionState, "ada", friend.StateOfflineModel, "", "r", friend.ActorLife, life1, "up", ""); got[0] != "OK" {
		t.Fatalf("offline-model again: %v", got)
	}
	got = fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e4:clear", friend.StateOfflineModel, life1)
	if got[0] != "OK" || got[1] != "up" || client.Exists(ctx, friend.StateKey("ada")).Val() != 0 {
		t.Fatalf("matching clear of its own state: %v %v", got, fsState(client, "ada"))
	}

	// A write over an away the classifier did not see: STALE, no wake.
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "ada", State: friend.StateAway, Until: until, Actor: "ada", Idem: "away-2"}))
	outLen = client.XLen(ctx, friend.OutboxKey).Val()
	got = fsFCall(t, client, friend.FunctionState, "ada", friend.StateOfflineModel, "", "r", friend.ActorLife, "life:ada:e5:offline-model", "up", "")
	if got[0] != "STALE" || fsState(client, "ada")["state"] != friend.StateAway {
		t.Fatalf("offline-model over an unseen away: %v %v", got, fsState(client, "ada"))
	}
	if client.XLen(ctx, friend.OutboxKey).Val() != outLen {
		t.Fatal("a STALE write appended a wake")
	}
	// The classifier has no unconditional path.
	if got := fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e6:clear"); got[0] != "INVALID" {
		t.Fatalf("actor life without args 7-8: %v, want INVALID", got)
	}
}

// TestFriendEventOneWriter: ns_friend_event appends the six kinds and
// refuses an unknown kind, a cause off turn-start, an unknown friend and an
// actor that is not the friend (shape validation, not authentication).
func TestFriendEventOneWriter(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "bo", "stella", "johnny", "rowan")
	at := int64(1790000000000)
	id := fsEvent(t, client, "ada", life.EventDeliver, "", at)
	entry, err := client.XRange(ctx, friend.EventsKey("ada"), id, id).Result()
	fsMust(t, err)
	if len(entry) != 1 || entry[0].Values["kind"] != "deliver" || entry[0].Values["at"] != strconv.FormatInt(at, 10) {
		t.Fatalf("deliver entry %v", entry)
	}
	tid := fsEvent(t, client, "ada", life.EventTurnStart, id, at+1000)
	entry, err = client.XRange(ctx, friend.EventsKey("ada"), tid, tid).Result()
	fsMust(t, err)
	if len(entry) != 1 || entry[0].Values["cause"] != id {
		t.Fatalf("turn-start entry %v, want cause %s", entry, id)
	}
	before := client.XLen(ctx, friend.EventsKey("ada")).Val()
	for _, bad := range [][]any{
		{"ada", "sleeping", "", at, "ada"},
		{"ada", life.EventBeat, id, at, "ada"},
		{"zed", life.EventBeat, "", at, "zed"},
		{"ada", life.EventBeat, "", at, "bo"},
		{"ada", life.EventDeliver, "", at, "bo"},
	} {
		if got := fsFCall(t, client, life.FunctionEvent, bad...); got[0] != "INVALID" {
			t.Fatalf("%v: %v, want INVALID", bad, got)
		}
	}
	if after := client.XLen(ctx, friend.EventsKey("ada")).Val(); after != before {
		t.Fatalf("a refused event was appended: %d -> %d", before, after)
	}
	if client.Exists(ctx, friend.EventsKey("zed")).Val() != 0 {
		t.Fatal("an unknown friend got a stream")
	}
}

// TestFriendWakeModeRechecksReceipt: ns_friend_wakemode re-reads the pair it
// is given; a late, wrongly caused or wrong-kind pair is REFUSED and writes
// nothing, a real receipt declares scheduled-model-turn.
func TestFriendWakeModeRechecksReceipt(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	at := int64(1790000000000)
	beat := fsEvent(t, client, "ada", life.EventBeat, "", at)
	d := fsEvent(t, client, "ada", life.EventDeliver, "", at+1000)
	other := fsEvent(t, client, "ada", life.EventDeliver, "", at+2000)
	late := fsEvent(t, client, "ada", life.EventTurnStart, d, at+1000+121000)
	wrong := fsEvent(t, client, "ada", life.EventTurnStart, other, at+1000+30000)
	good := fsEvent(t, client, "ada", life.EventTurnStart, d, at+1000+60000)
	for _, pair := range [][2]string{{d, late}, {d, wrong}, {d, beat}, {beat, good}, {d, "1-1"}} {
		if got := fsFCall(t, client, life.FunctionWakeMode, "ada", pair[0], pair[1], "ada", ""); got[0] != "REFUSED" {
			t.Fatalf("%v: %v, want REFUSED", pair, got)
		}
	}
	if client.Exists(ctx, friend.WakeModeKey("ada")).Val() != 0 {
		t.Fatal("a refused pair wrote a wake mode")
	}
	got := fsFCall(t, client, life.FunctionWakeMode, "ada", d, good, "ada", "")
	if !reflect.DeepEqual(got, []string{"OK", life.WakeModeScheduled, "60000"}) {
		t.Fatalf("real receipt: %v", got)
	}
	h := client.HGetAll(ctx, friend.WakeModeKey("ada")).Val()
	if h["mode"] != life.WakeModeScheduled || h["lag_ms"] != "60000" || h["deliver"] != d || h["turn"] != good {
		t.Fatalf("wakemode %v", h)
	}
	n := 0
	for _, m := range fsCapLog(t, client) {
		if m.Values["kind"] == "friend-wakemode" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d friend-wakemode receipts, want 1", n)
	}
}
