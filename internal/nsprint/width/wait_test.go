package width_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
	"github.com/redis/go-redis/v9"
)

// Controls 62 and 63 of #2756 (nova-tools #3090): a durable WAITING state,
// so a build waiting on CI, a read or a dependency holds ownership without
// occupying a child.

func taskHash(t *testing.T, client *redis.Client, id string) map[string]string {
	t.Helper()
	h, err := client.HGetAll(context.Background(), "s:"+sprint+":task:"+id).Result()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func queue(t *testing.T, client *redis.Client, friend string) []string {
	t.Helper()
	ids, err := client.ZRange(context.Background(), "s:"+sprint+":open:"+friend, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func zcard(t *testing.T, client *redis.Client, key string) int64 {
	t.Helper()
	n, err := client.ZCard(context.Background(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func wait(t *testing.T, st *store.Store, id, token, on string) {
	t.Helper()
	got, err := task.Wait(context.Background(), st, task.WaitRequest{Sprint: sprint, ID: id, Token: token, On: on, Actor: "child"})
	if err != nil || got != task.WaitWaiting {
		t.Fatalf("wait %s on %s = %s, %v; want WAITING", id, on, got, err)
	}
}

func ack(t *testing.T, st *store.Store, r width.Reserved) {
	t.Helper()
	got, err := task.Beat(context.Background(), st, task.BeatRequest{Sprint: r.Sprint, ID: r.ID, Token: r.Claim.Token, Actor: "child"})
	if err != nil || got != task.BeatWorking {
		t.Fatalf("ack %s = %s, %v", r.ID, got, err)
	}
}

// dealtTo reads the reservation the reconciler dealt to friend as its last
// wake line (kind fill) and returns it as the child would see it.
func dealtTo(t *testing.T, client *redis.Client, friend, id string) width.Reserved {
	t.Helper()
	w := lastWake(t, client, friend)
	if w["kind"] != "fill" || w["id"] != id {
		t.Fatalf("%s wake = %v; want kind=fill id=%s", friend, w, id)
	}
	h := taskHash(t, client, id)
	if h["state"] != "claimed" || h["owner"] != friend || h["token"] != w["token"] {
		t.Fatalf("%s = state %s owner %s; want claimed by %s with the wake's token", id, h["state"], h["owner"], friend)
	}
	return width.Reserved{Ready: width.Ready{Sprint: sprint, ID: id}, Claim: task.Claim{Sprint: sprint, ID: id, Token: h["token"]}}
}

// TestControl62WaitReleasesSlot: a WORKING build that calls task wait on a
// pending ci key frees one slot (its child lease closes, its token is
// fenced, its owner is kept), and the reconciler's width refill (#3071)
// starts one replacement in the next pass. waiting is never counted as
// working or as ready_open.
func TestControl62WaitReleasesSlot(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "rowan", 2)
	pushN(t, st, "rowan", "b", 3)
	h := newAck(st)
	if res := refill(t, st, "rowan", h); len(res.Launched) != 2 {
		t.Fatalf("setup refill launched %d; want 2", len(res.Launched))
	}
	b01 := h.live["b01"]

	var rows []width.Row
	duty := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2},
		AfterTick: func(r width.Result, _ []width.Reserved) { rows = append(rows, row(t, r, "rowan")) }}
	loop := &reconcile.Loop{Lease: lease(t, st), Duties: []reconcile.Duty{duty.Run}}
	if res, err := loop.Pass(ctx); err != nil || res.Err != "" || res.Counts.Dealt != 0 {
		t.Fatalf("pass 1 = %+v, %v; want a tick and nothing dealt (2/2)", res, err)
	}

	ci := task.WaitOnCI("nova-tools", headA)
	if got, err := task.Wait(ctx, st, task.WaitRequest{Sprint: sprint, ID: "b01", Token: b01.Claim.Token, On: "ci:nova-tools"}); err != nil || got != task.WaitBadKey {
		t.Fatalf("wait on an untyped key = %s, %v; want BADKEY", got, err)
	}
	wait(t, st, "b01", b01.Claim.Token, ci)

	// One slot freed, ownership kept, the child's token fenced.
	if n := zcard(t, client, "friend:rowan:living"); n != 1 {
		t.Fatalf("living = %d after the wait; want 1 (the waiting build left its slot)", n)
	}
	if n := zcard(t, client, task.WaitingKey("rowan")); n != 1 {
		t.Fatalf("waiting = %d; want 1", n)
	}
	hb := taskHash(t, client, "b01")
	if hb["state"] != task.StateWaiting || hb["owner"] != "rowan" || hb["wait_on"] != ci || hb["wait_since"] == "" {
		t.Fatalf("b01 = state %s owner %s wait_on %s since %q; want waiting, owner rowan, on %s", hb["state"], hb["owner"], hb["wait_on"], hb["wait_since"], ci)
	}
	if hb["token"] == b01.Claim.Token {
		t.Fatal("b01 token not fenced by the wait")
	}
	if got, _ := task.Beat(ctx, st, task.BeatRequest{Sprint: sprint, ID: "b01", Token: b01.Claim.Token}); got != task.BeatFenced {
		t.Fatalf("the waiting child's beat = %s; want FENCED (its lease is closed)", got)
	}
	if got, _ := task.Done(ctx, st, task.DoneRequest{Sprint: sprint, ID: "b01", Token: b01.Claim.Token, Evidence: "x"}); got != task.DoneFenced {
		t.Fatalf("the waiting child's done = %s; want FENCED", got)
	}
	if got, _ := task.Wait(ctx, st, task.WaitRequest{Sprint: sprint, ID: "b01", Token: b01.Claim.Token, On: ci}); got != task.WaitFenced {
		t.Fatalf("a second wait = %s; want FENCED", got)
	}
	if q := queue(t, client, "rowan"); len(q) != 1 || q[0] != "b03" {
		t.Fatalf("rowan queue = %v; want [b03]: a waiting task is on no open queue", q)
	}
	freed, _ := client.XRevRangeN(ctx, "cap:log", "+", "-", 1).Result()
	if len(freed) != 1 || freed[0].Values["kind"] != "slot-freed" || freed[0].Values["id"] != "b01" {
		t.Fatalf("cap:log tip = %v; want slot-freed b01", freed)
	}
	listed, err := task.ListStore(ctx, st, task.ListRequest{As: "rowan", State: task.StateWaiting})
	if err != nil || len(listed) != 1 || listed[0].ID != "b01" {
		t.Fatalf("task list --state waiting = %+v, %v; want b01 (still rowan's)", listed, err)
	}

	// The next pass: the wait receipt wakes the refill and one replacement
	// is dealt to the same friend.
	res, err := loop.Pass(ctx)
	if err != nil || res.Err != "" {
		t.Fatalf("pass 2 = %+v, %v", res, err)
	}
	if res.Counts.Dealt != 1 {
		t.Fatalf("pass 2 dealt %d; want one replacement for the waiting build", res.Counts.Dealt)
	}
	dealtTo(t, client, "rowan", "b03")
	r := rows[len(rows)-1]
	if r.Working != 1 || r.Waiting != 1 || r.ReadyOpen != 1 || r.Desired != 2 || r.Fillable != 1 {
		t.Fatalf("pass 2 row %s fillable=%d; want working 1 (waiting not counted), waiting 1, ready 1 (b03 only), desired 2, fillable 1", r.Line(), r.Fillable)
	}

	// Never counted as working: with b03 starting, rowan is full and the
	// waiting build is still only waiting.
	if res, err := loop.Pass(ctx); err != nil || res.Counts.Dealt != 0 {
		t.Fatalf("pass 3 = %+v, %v; want nothing dealt", res, err)
	}
	r = rows[len(rows)-1]
	if r.Working != 1 || r.Starting != 1 || r.Waiting != 1 || r.ReadyOpen != 0 || r.Desired != 2 {
		t.Fatalf("pass 3 row %s; want 1/2 starting=1 waiting=1 ready=0", r.Line())
	}
	if !strings.Contains(r.Line(), "WIDTH rowan 1/2 waiting=1 ") {
		t.Fatalf("width line %q; want waiting printed beside working", r.Line())
	}
	if w, _ := client.HGet(ctx, width.Key("rowan"), "waiting").Result(); w != "1" {
		t.Fatalf("%s waiting = %q; want 1", width.Key("rowan"), w)
	}
}

// TestControl63WaitResumesOwner: the ci OK end puts every task waiting on
// the key back open at the FRONT of its own owner's queue, and the next pass
// deals it to that owner; a dependency satisfied on base resumes it from the
// reconciler's sweep; a dead key (ci failed for good, a dependency that no
// longer exists) goes to unresolved.
func TestControl63WaitResumesOwner(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "rowan", 1)
	seedFriend(t, client, "stella", 1)
	pushN(t, st, "rowan", "b", 3)
	pushN(t, st, "stella", "s", 1)
	first, err := width.Fill(ctx, st, "rowan", "harness", "")
	if err != nil || len(first) != 1 || first[0].ID != "b01" {
		t.Fatalf("fill rowan = %+v, %v; want b01", first, err)
	}
	ack(t, st, first[0])
	sfirst, err := width.Fill(ctx, st, "stella", "harness", "")
	if err != nil || len(sfirst) != 1 || sfirst[0].ID != "s01" {
		t.Fatalf("fill stella = %+v, %v; want s01", sfirst, err)
	}
	ack(t, st, sfirst[0])

	duty := &width.Duty{Store: st, Policy: width.Policy{RebalanceTicks: 2}}
	loop := &reconcile.Loop{Lease: lease(t, st), Duties: []reconcile.Duty{duty.Run}}
	if _, err := loop.Pass(ctx); err != nil {
		t.Fatal(err)
	}

	ci := task.WaitOnCI("nova-tools", headA)
	wait(t, st, "b01", first[0].Claim.Token, ci)
	wait(t, st, "s01", sfirst[0].Claim.Token, ci)
	if res, err := loop.Pass(ctx); err != nil || res.Counts.Dealt != 1 {
		t.Fatalf("pass 2 = %+v, %v; want b02 dealt as rowan's replacement", res, err)
	}
	b02 := dealtTo(t, client, "rowan", "b02")

	// A wake for a key nobody waits on moves nothing.
	if w, err := task.Wake(ctx, st, task.WakeRequest{Sprint: sprint, On: task.WaitOnCI("nova-tools", strings.Repeat("c", 40)), Outcome: task.WakeOK}); err != nil || w != (task.Woke{}) {
		t.Fatalf("wake of an unwaited key = %+v, %v; want nothing", w, err)
	}
	// The ci OK end at head.
	w, err := task.Wake(ctx, st, task.WakeRequest{Sprint: sprint, On: ci, Outcome: task.WakeOK, Actor: "ci-end"})
	if err != nil || w.Resumed != 2 || w.Dead != 0 {
		t.Fatalf("ci ok wake = %+v, %v; want 2 resumed", w, err)
	}
	for id, owner := range map[string]string{"b01": "rowan", "s01": "stella"} {
		h := taskHash(t, client, id)
		if h["state"] != "open" || h["owner"] != owner || h["wait_on"] != "" {
			t.Fatalf("%s = state %s owner %s wait_on %q; want open, owner %s", id, h["state"], h["owner"], h["wait_on"], owner)
		}
	}
	if q := queue(t, client, "rowan"); len(q) != 2 || q[0] != "b01" || q[1] != "b03" {
		t.Fatalf("rowan queue = %v; want [b01 b03]: the resumed build at the front of its own owner's queue", q)
	}
	if q := queue(t, client, "stella"); len(q) != 1 || q[0] != "s01" {
		t.Fatalf("stella queue = %v; want [s01] on its own owner's queue", q)
	}
	for _, f := range []string{"rowan", "stella"} {
		if n := zcard(t, client, task.WaitingKey(f)); n != 0 {
			t.Fatalf("%s waiting = %d after the ok end; want 0", f, n)
		}
	}
	if n, _ := client.SCard(ctx, "s:"+sprint+":idx:task:waiting").Result(); n != 0 {
		t.Fatalf("idx:task:waiting = %d; want 0", n)
	}

	// The resume wakes the refill: stella's slot is free and s01 is dealt
	// back to stella; rowan is full (b02 starting), so b01 waits at the front.
	if res, err := loop.Pass(ctx); err != nil || res.Counts.Dealt != 1 {
		t.Fatalf("pass 3 = %+v, %v; want s01 dealt back to stella", res, err)
	}
	s01 := dealtTo(t, client, "stella", "s01")
	ack(t, st, s01)
	ack(t, st, b02)
	if got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sprint, ID: "b02", Token: b02.Claim.Token, Evidence: "https://example.test/b02"}); err != nil || got != task.DoneClosed {
		t.Fatalf("b02 done = %s, %v", got, err)
	}
	if res, err := loop.Pass(ctx); err != nil || res.Counts.Dealt != 1 {
		t.Fatalf("pass 4 = %+v, %v; want b01 (front) dealt to rowan", res, err)
	}
	b01 := dealtTo(t, client, "rowan", "b01")
	if a := taskHash(t, client, "b01")["attempt"]; a != "2" {
		t.Fatalf("b01 attempt %s; want 2 (a new lease for the same owner)", a)
	}

	// A dependency satisfied on base resumes from the reconciler's sweep and
	// is dealt again in the same pass.
	ack(t, st, b01)
	wait(t, st, "b01", b01.Claim.Token, task.WaitOnDep("b02"))
	if res, err := loop.Pass(ctx); err != nil || res.Counts.Dealt != 1 {
		t.Fatalf("pass 5 = %+v, %v; want b01 resumed by the sweep (dep b02 closed) and dealt", res, err)
	}
	b01 = dealtTo(t, client, "rowan", "b01")

	// Dead keys go to unresolved: ci that will never pass at this head, and a
	// dependency that does not exist.
	ack(t, st, b01)
	dead := task.WaitOnCI("nova-tools", strings.Repeat("d", 40))
	wait(t, st, "b01", b01.Claim.Token, dead)
	if w, err := task.Wake(ctx, st, task.WakeRequest{Sprint: sprint, On: dead, Outcome: task.WakeDead, Reason: "pr-closed", Actor: "ci-end"}); err != nil || w.Dead != 1 {
		t.Fatalf("dead wake = %+v, %v; want 1 dead", w, err)
	}
	wait(t, st, "s01", s01.Claim.Token, task.WaitOnDep("gone"))
	if _, err := loop.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	unresolved, _ := client.HGetAll(ctx, "s:"+sprint+":unresolved").Result()
	for id, field := range map[string]string{"b01": "b01:wait-dead:" + dead, "s01": "s01:wait-dead:dep:gone"} {
		h := taskHash(t, client, id)
		if h["state"] != "reconcile-required" || !strings.HasPrefix(h["reason"], "wait-dead") {
			t.Fatalf("%s = state %s reason %q; want reconcile-required wait-dead", id, h["state"], h["reason"])
		}
		if _, ok := unresolved[field]; !ok {
			t.Fatalf("unresolved = %v; want an item %s", unresolved, field)
		}
	}
	if !strings.Contains(unresolved["s01:wait-dead:dep:gone"], "reason=dep:missing") {
		t.Fatalf("s01 unresolved item %q; want reason=dep:missing", unresolved["s01:wait-dead:dep:gone"])
	}
	for _, f := range []string{"rowan", "stella"} {
		if n := zcard(t, client, task.WaitingKey(f)); n != 0 {
			t.Fatalf("%s waiting = %d; a dead key leaves waiting", f, n)
		}
	}
}

// TestWaitReadKeyResolvesAtHead: read:<pr>@<head> resumes when a typed read
// at that head is recorded, and is dead when the PR head moves.
func TestWaitReadKeyResolvesAtHead(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "rowan", 2)
	for _, id := range []string{"w1", "w2"} {
		if got, err := task.Push(ctx, st, task.PushRequest{Sprint: sprint, ID: id, Kind: task.KindWork, Title: id,
			Effects: task.EffectsNone, To: "rowan", Ref: "briefs/" + id + ".md", Repo: "nova-tools"}); err != nil || got != task.PushCreated {
			t.Fatalf("push %s = %s, %v", id, got, err)
		}
	}
	got, err := width.Fill(ctx, st, "rowan", "harness", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("fill = %+v, %v", got, err)
	}
	for _, r := range got {
		ack(t, st, r)
	}
	client.HSet(ctx, "s:"+sprint+":pr:nova-tools:9301", "head", headA)
	client.HSet(ctx, "s:"+sprint+":pr:nova-tools:9302", "head", headA)
	wait(t, st, got[0].ID, got[0].Claim.Token, task.WaitOnRead(9301, headA))
	wait(t, st, got[1].ID, got[1].Claim.Token, task.WaitOnRead(9302, headA))

	if w, err := task.WaitSweep(ctx, st, sprint, "", "control", ""); err != nil || w != (task.Woke{}) {
		t.Fatalf("sweep with no read = %+v, %v; want nothing", w, err)
	}
	client.HSet(ctx, "s:"+sprint+":disp:nova-tools:9301", "stella@"+headA, "APPROVE 9 https://example.test")
	client.HSet(ctx, "s:"+sprint+":pr:nova-tools:9302", "head", strings.Repeat("e", 40))
	w, err := task.WaitSweep(ctx, st, sprint, "", "control", "")
	if err != nil || w.Resumed != 1 || w.Dead != 1 {
		t.Fatalf("sweep = %+v, %v; want 1 resumed (read at head) and 1 dead (head moved)", w, err)
	}
	if s := taskHash(t, client, got[0].ID)["state"]; s != "open" {
		t.Fatalf("%s state %s; want open", got[0].ID, s)
	}
	if s := taskHash(t, client, got[1].ID)["state"]; s != "reconcile-required" {
		t.Fatalf("%s state %s; want reconcile-required", got[1].ID, s)
	}
	if _, err := task.WaitSweep(ctx, st, sprint, "not-the-lease", "control", ""); err == nil {
		t.Fatal("a sweep with a token that does not hold lease:reconciler must refuse FENCED")
	}
}
