package width_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
	"github.com/redis/go-redis/v9"
)

var headA = strings.Repeat("a", 40)

// pushRead pushes a read of nova-tools#pr (push needs the PR head known and
// equal), then sets the task head to want and the PR hash head to have; an
// empty want or have removes that field, as metadata lost after the push.
func pushRead(t *testing.T, st *store.Store, to, id string, pr int, want, have string) {
	t.Helper()
	ctx := context.Background()
	prKey := "s:" + sprint + ":pr:nova-tools:" + itoa(pr)
	if err := st.Client().HSet(ctx, prKey, "head", headA).Err(); err != nil {
		t.Fatal(err)
	}
	req := task.PushRequest{Sprint: sprint, ID: id, Kind: task.KindRead, Title: "read " + id,
		Effects: task.EffectsNone, To: to, Ref: "briefs/" + id + ".md", Repo: "nova-tools", PR: pr, Head: headA}
	if got, err := task.Push(ctx, st, req); err != nil || got != task.PushCreated {
		t.Fatalf("push %s = %s, %v; want CREATED", id, got, err)
	}
	key := "s:" + sprint + ":task:" + id
	if want == "" {
		if err := st.Client().HDel(ctx, key, "head").Err(); err != nil {
			t.Fatal(err)
		}
	} else if err := st.Client().HSet(ctx, key, "head", want).Err(); err != nil {
		t.Fatal(err)
	}
	if have == "" {
		if err := st.Client().HDel(ctx, prKey, "head").Err(); err != nil {
			t.Fatal(err)
		}
	} else if err := st.Client().HSet(ctx, prKey, "head", have).Err(); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func lastWake(t *testing.T, client *redis.Client, friend string) map[string]any {
	t.Helper()
	msgs, err := client.XRevRangeN(context.Background(), "friend:"+friend+":wake", "+", "-", 1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		return nil
	}
	return msgs[0].Values
}

// TestHold3_CompletionReplacedWithinOnePassNoManualCall is Stella's control
// for hold 3 item 1 on #3086: a reader at its one slot with one more ready
// read; the child completes; the next reconciler pass (no fill, no width
// verb) ticks width under the lease and deals the ready read as the
// replacement, its spawn line on the reader's wake stream.
func TestHold3_CompletionReplacedWithinOnePassNoManualCall(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "stella", 1)
	pushRead(t, st, "stella", "r1", 9101, headA, headA)
	pushRead(t, st, "stella", "r2", 9102, headA, headA)
	first, err := width.Fill(ctx, st, "stella", "harness", "")
	if err != nil || len(first) != 1 || first[0].ID != "r1" {
		t.Fatalf("setup fill = %+v, %v; want r1", first, err)
	}
	if got, err := task.Beat(ctx, st, task.BeatRequest{Sprint: sprint, ID: "r1", Token: first[0].Claim.Token}); err != nil || got != task.BeatWorking {
		t.Fatalf("r1 ack = %s, %v", got, err)
	}

	var ticked []width.Result
	duty := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2, Readers: []string{"stella"}},
		AfterTick: func(r width.Result, _ []width.Reserved) { ticked = append(ticked, r) }}
	loop := &reconcile.Loop{Lease: lease(t, st), Duties: []reconcile.Duty{duty.Run}}

	res, err := loop.Pass(ctx)
	if err != nil || res.Counts.Dealt != 0 || res.Err != "" {
		t.Fatalf("pass 1 = %+v, %v; want a tick and nothing dealt (slot full)", res, err)
	}
	if w, _ := client.HGet(ctx, width.Key("stella"), "working").Result(); w != "1" {
		t.Fatalf("pass 1 wrote working=%q; want 1 from the reconciler's tick", w)
	}

	if got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sprint, ID: "r1", Token: first[0].Claim.Token,
		Evidence: "https://example.test/r1", Verdict: "APPROVE", Score: "9", Head: headA}); err != nil || got != task.DoneClosed {
		t.Fatalf("r1 done = %s, %v", got, err)
	}

	res, err = loop.Pass(ctx)
	if err != nil || res.Err != "" {
		t.Fatalf("pass 2 = %+v, %v", res, err)
	}
	if res.Counts.Dealt != 1 {
		t.Fatalf("pass 2 dealt %d; want the replacement (1) in the same pass as the completion", res.Counts.Dealt)
	}
	h, _ := client.HGetAll(ctx, "s:"+sprint+":task:r2").Result()
	if h["state"] != "claimed" || h["owner"] != "stella" {
		t.Fatalf("r2 = state %s owner %s; want claimed by stella", h["state"], h["owner"])
	}
	wake := lastWake(t, client, "stella")
	if wake["kind"] != "fill" || wake["id"] != "r2" || wake["token"] != h["token"] || wake["brief"] != "briefs/r2.md" {
		t.Fatalf("stella wake = %v; want kind=fill id=r2 with its claim token and brief", wake)
	}
	if len(ticked) != 2 {
		t.Fatalf("ticks = %d; want one per pass", len(ticked))
	}

	// A pass with no completion deals nothing more (the slot is starting).
	if res, err = loop.Pass(ctx); err != nil || res.Counts.Dealt != 0 {
		t.Fatalf("pass 3 = %+v, %v; want nothing dealt", res, err)
	}
}

// TestHold3_StaleInstanceTicksAndDealsNothing: another instance took
// lease:reconciler; the width duty's tick refuses FENCED before any write,
// the pass stops, and the completion is not dealt by the stale instance.
func TestHold3_StaleInstanceTicksAndDealsNothing(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f9", 2)
	duty := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2}}
	l := lease(t, st)
	if _, err := duty.Run(ctx, l); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Ready work and a free slot after the holder's last pass: a stale
	// instance must neither deal it nor move the durable cursor.
	pushN(t, st, "f9", "s", 2)
	cursorBefore, _ := client.Get(ctx, width.CursorKey(sprint)).Result()
	if err := client.Del(ctx, width.Key("f9")).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, reconcile.LeaseKey, "token", "another-instance").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := width.Tick(ctx, st, width.Policy{RebalanceTicks: 2}, l.Token(), "control", ""); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("tick with a stale token = %v; want FENCED", err)
	}
	if _, err := duty.Run(ctx, l); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("stale duty run = %v; want FENCED", err)
	}
	if n, _ := client.Exists(ctx, width.Key("f9")).Result(); n != 0 {
		t.Fatalf("a fenced tick wrote %s", width.Key("f9"))
	}
	for _, id := range []string{"s01", "s02"} {
		if state, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "state").Result(); state != "open" {
			t.Fatalf("%s state %s after a fenced pass; want open", id, state)
		}
	}
	if got, _ := client.Get(ctx, width.CursorKey(sprint)).Result(); got != cursorBefore {
		t.Fatalf("a fenced pass moved %s from %q to %q", width.CursorKey(sprint), cursorBefore, got)
	}
	if reply, err := client.FCall(ctx, width.FunctionCursor, nil, l.Token(), sprint, "99999999999999-0").Result(); err != nil ||
		fmtReply(reply) != "FENCED" {
		t.Fatalf("cursor write with a stale token = %v, %v; want FENCED", reply, err)
	}
}

func fmtReply(reply any) string {
	if vals, ok := reply.([]any); ok && len(vals) > 0 {
		if s, ok := vals[0].(string); ok {
			return s
		}
	}
	return ""
}

// restartFixture is Stella's hold 3 at 54755384: a reader at its one slot
// (r1 ACKed) with r2 ready; instance A runs a pass (it reads the log to the
// tip and records its cursor); then r1 completes after A's read; then A is
// gone (restart) and instance B acquires the lease with a new Duty (a new
// process: no memory of A). Returns B's lease and the log id of r1's done
// receipt (empty when complete is false).
func restartFixture(t *testing.T, withReady bool, complete bool) (*store.Store, *redis.Client, *reconcile.Lease, string) {
	t.Helper()
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "stella", 1)
	pushRead(t, st, "stella", "r1", 9301, headA, headA)
	if withReady {
		pushRead(t, st, "stella", "r2", 9302, headA, headA)
	}
	first, err := width.Fill(ctx, st, "stella", "harness", "")
	if err != nil || len(first) != 1 || first[0].ID != "r1" {
		t.Fatalf("setup fill = %+v, %v; want r1", first, err)
	}
	tok := first[0].Claim.Token
	if got, err := task.Beat(ctx, st, task.BeatRequest{Sprint: sprint, ID: "r1", Token: tok}); err != nil || got != task.BeatWorking {
		t.Fatalf("r1 ack = %s, %v", got, err)
	}
	a := lease(t, st)
	dutyA := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2, Readers: []string{"stella"}}}
	loopA := &reconcile.Loop{Lease: a, Duties: []reconcile.Duty{dutyA.Run}}
	if res, err := loopA.Pass(ctx); err != nil || res.Err != "" || res.Counts.Dealt != 0 {
		t.Fatalf("instance A pass = %+v, %v; want a read to the tip and nothing dealt (slot working)", res, err)
	}
	doneID := ""
	if complete {
		// The completion lands after A's last read and before the restart.
		if got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sprint, ID: "r1", Token: tok,
			Evidence: "https://example.test/r1", Verdict: "APPROVE", Score: "9", Head: headA}); err != nil || got != task.DoneClosed {
			t.Fatalf("r1 done = %s, %v", got, err)
		}
		msgs, _ := client.XRevRangeN(ctx, "s:"+sprint+":log", "+", "-", 1).Result()
		if len(msgs) == 0 || msgs[0].Values["kind"] != "task done" {
			t.Fatalf("log tip = %v; want r1's task done receipt", msgs)
		}
		doneID = msgs[0].ID
		if cur, _ := client.Get(ctx, width.CursorKey(sprint)).Result(); cur >= doneID {
			t.Fatalf("A's durable cursor %q; want before the done receipt %s (the completion is after A's read)", cur, doneID)
		}
	}
	// Restart: A releases (or is fenced), B is a new instance and process.
	if err := a.Release(ctx); err != nil {
		t.Fatalf("release A: %v", err)
	}
	b, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "fixture-b", TTL: time.Minute})
	if err != nil {
		t.Fatalf("acquire B: %v", err)
	}
	return st, client, b, doneID
}

// TestHold3at54755384_CompletionBeforeRestartIsReplacedInOnePass is the
// negative fixture: the completion lands between instance A's read and the
// restart; instance B's first pass must replace it. At 54755384 B seeded its
// cursor at the log tip, returned no completions, and dealt nothing (r2 idle).
func TestHold3at54755384_CompletionBeforeRestartIsReplacedInOnePass(t *testing.T) {
	st, client, b, doneID := restartFixture(t, true, true)
	ctx := context.Background()
	var freed [][]string
	dutyB := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2, Readers: []string{"stella"}},
		AfterRead: func(f []string) { freed = append(freed, f) }}
	loopB := &reconcile.Loop{Lease: b, Duties: []reconcile.Duty{dutyB.Run}}
	res, err := loopB.Pass(ctx)
	if err != nil || res.Err != "" {
		t.Fatalf("instance B pass = %+v, %v", res, err)
	}
	if res.Counts.Dealt != 1 {
		t.Fatalf("instance B dealt %d; want r2 in its first pass", res.Counts.Dealt)
	}
	if len(freed) != 1 || strings.Join(freed[0], ",") != "stella" {
		t.Fatalf("instance B read completions %v; want stella's (the durable cursor replays it, never the tip)", freed)
	}
	h, _ := client.HGetAll(ctx, "s:"+sprint+":task:r2").Result()
	if h["state"] != "claimed" || h["owner"] != "stella" {
		t.Fatalf("r2 = state %s owner %s; want claimed by stella", h["state"], h["owner"])
	}
	if w := lastWake(t, client, "stella"); w["kind"] != "fill" || w["id"] != "r2" {
		t.Fatalf("stella wake = %v; want the fill line for r2", w)
	}
	if cur, _ := client.Get(ctx, width.CursorKey(sprint)).Result(); cur < doneID {
		t.Fatalf("B's cursor %q; want at or past the done receipt %s", cur, doneID)
	}
	// The replayed completion is consumed once: the next pass reads nothing.
	if res, err = loopB.Pass(ctx); err != nil || res.Counts.Dealt != 0 || len(freed) != 2 || len(freed[1]) != 0 {
		t.Fatalf("instance B pass 2 = %+v, %v, freed %v; want nothing read or dealt", res, err, freed)
	}
}

// TestHold3at54755384_LostCompletionStillRefillsFromDeficit isolates the
// second guard: the completion is never read (the durable cursor already sits
// past it, as if the event were lost), yet the pass refills the free slot
// from the tick's fillable count.
func TestHold3at54755384_LostCompletionStillRefillsFromDeficit(t *testing.T) {
	st, client, b, doneID := restartFixture(t, true, true)
	ctx := context.Background()
	if err := client.Set(ctx, width.CursorKey(sprint), doneID, 0).Err(); err != nil {
		t.Fatal(err)
	}
	var freed [][]string
	dutyB := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2, Readers: []string{"stella"}},
		AfterRead: func(f []string) { freed = append(freed, f) }}
	res, err := (&reconcile.Loop{Lease: b, Duties: []reconcile.Duty{dutyB.Run}}).Pass(ctx)
	if err != nil || res.Err != "" || res.Counts.Dealt != 1 {
		t.Fatalf("pass = %+v, %v; want r2 dealt from the deficit", res, err)
	}
	if len(freed) != 1 || len(freed[0]) != 0 {
		t.Fatalf("completions read %v; want none (the event is lost in this fixture)", freed)
	}
	if owner, _ := client.HGet(ctx, "s:"+sprint+":task:r2", "owner").Result(); owner != "stella" {
		t.Fatalf("r2 owner %q; want stella", owner)
	}
}

// TestHold3at54755384_PositiveControlRestartWithNothingPending: the same
// restart with no completion (r1 still working), and with a completion but no
// ready work, deals nothing. The fixture's dealt=1 above comes from the
// pending completion and the ready read, not from the restart itself.
func TestHold3at54755384_PositiveControlRestartWithNothingPending(t *testing.T) {
	for _, tc := range []struct {
		name            string
		ready, complete bool
		freed           string
	}{
		{"no completion", true, false, ""},
		{"no ready work", false, true, "stella"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, client, b, _ := restartFixture(t, tc.ready, tc.complete)
			ctx := context.Background()
			var freed [][]string
			dutyB := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2, Readers: []string{"stella"}},
				AfterRead: func(f []string) { freed = append(freed, f) }}
			res, err := (&reconcile.Loop{Lease: b, Duties: []reconcile.Duty{dutyB.Run}}).Pass(ctx)
			if err != nil || res.Err != "" || res.Counts.Dealt != 0 {
				t.Fatalf("pass = %+v, %v; want nothing dealt", res, err)
			}
			if len(freed) != 1 || strings.Join(freed[0], ",") != tc.freed {
				t.Fatalf("completions read %v; want %q", freed, tc.freed)
			}
			if tc.ready {
				if state, _ := client.HGet(ctx, "s:"+sprint+":task:r2", "state").Result(); state != "open" {
					t.Fatalf("r2 state %s; want open (the slot is working)", state)
				}
			}
		})
	}
}

// TestHold3_ReadWithNoHeadMetadataIsNeverDealt is Stella's control for hold 3
// item 2: a read whose PR hash has no head, or whose task has no head, is not
// ready (reason head:missing) and is never listed, reserved or dealt; a read
// at a known, equal head beside them is.
func TestHold3_ReadWithNoHeadMetadataIsNeverDealt(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "stella", 4)
	pushRead(t, st, "stella", "nopr", 9201, headA, "")   // PR hash has no head
	pushRead(t, st, "stella", "notask", 9202, "", headA) // task has no head
	pushRead(t, st, "stella", "moved", 9203, headA, strings.Repeat("b", 40))
	pushRead(t, st, "stella", "good", 9204, headA, headA)

	r := row(t, tick(t, st, width.Policy{RebalanceTicks: 2}), "stella")
	if r.ReadyOpen != 1 {
		t.Fatalf("ready_open = %d (%s); want 1: only the read at a known head", r.ReadyOpen, r.Line())
	}
	if why, _ := client.HGet(ctx, width.Key("stella"), "blocked_why").Result(); why != "head:missing=2 head:moved=1" {
		t.Fatalf("blocked_why = %q; want head:missing=2 head:moved=1", why)
	}
	gen, _, ids, err := width.ReadyIDs(ctx, st, "stella")
	if err != nil || len(ids) != 1 || ids[0].ID != "good" {
		t.Fatalf("ready ids = %+v, %v; want only good", ids, err)
	}
	for _, id := range []string{"nopr", "notask"} {
		_, _, err := width.Reserve(ctx, st, width.Ready{Sprint: sprint, ID: id, Kind: "read"}, "stella", gen, "harness", "")
		if !errors.Is(err, width.ErrNotReady) || !strings.Contains(err.Error(), "head:missing") {
			t.Fatalf("reserve %s = %v; want NOTREADY head:missing", id, err)
		}
	}
	got, err := width.Fill(ctx, st, "stella", "harness", "")
	if err != nil || len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("fill = %+v, %v; want only good", got, err)
	}
	for _, id := range []string{"nopr", "notask", "moved"} {
		if state, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "state").Result(); state != "open" {
			t.Fatalf("%s state %s; a read with no known head must never be dealt", id, state)
		}
	}
}
