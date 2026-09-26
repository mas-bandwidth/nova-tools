package card_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// registered reports whether the stream is in ws:names and ranked in
// ws:order, and its rank.
func registered(t *testing.T, ctx context.Context, client *redis.Client, stream string) (bool, float64) {
	t.Helper()
	named, err := client.SIsMember(ctx, "ws:names", stream).Result()
	if err != nil {
		t.Fatal(err)
	}
	rank, err := client.ZScore(ctx, "ws:order", stream).Result()
	if err == redis.Nil {
		return false, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return named, rank
}

// TestCardPushRegistersItsStream: a card pushed with STREAM: x makes x a
// registered stream (ws:names, ranked last in ws:order) like the task-card
// path, so `stream ls` (ws.Counts) lists it with the card counted. Found
// 2026-09-26 on the live store: card push --dir with STREAM: ci wrote
// ws:ci:ready and ws:ci:waiting but not ws:names or ws:order.
func TestCardPushRegistersItsStream(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	client.SAdd(ctx, "ws:names", "first")
	client.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: "first"})

	f := validCard(srv.URL + "/acme/public.git")
	f.label = "stream-reg-1"
	if res := card.Push(ctx, client, sprint, withHeader(f, "STREAM: ci")); res.Code != 0 {
		t.Fatalf("push: exit %d stderr %q", res.Code, res.Stderr)
	}
	if ok, rank := registered(t, ctx, client, "ci"); !ok || rank != 2 {
		t.Fatalf("after push: ci registered=%v rank=%v, want in ws:names and ranked 2 in ws:order", ok, rank)
	}
	// A second card of the same stream keeps its rank.
	f.label = "stream-reg-2"
	if res := card.Push(ctx, client, sprint, withHeader(f, "STREAM: ci")); res.Code != 0 {
		t.Fatalf("second push: exit %d stderr %q", res.Code, res.Stderr)
	}
	if ok, rank := registered(t, ctx, client, "ci"); !ok || rank != 2 || client.ZCard(ctx, "ws:order").Val() != 2 {
		t.Fatalf("after second push: ci registered=%v rank=%v order=%v", ok, rank, client.ZRange(ctx, "ws:order", 0, -1).Val())
	}
	rows, err := ws.Counts(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	var got *ws.Count
	for i := range rows {
		if rows[i].Stream == "ci" {
			got = &rows[i]
		}
	}
	if got == nil || got.Rank != 2 || got.Ready != 2 {
		t.Fatalf("stream ls rows = %+v, want ci at rank 2 with ready=2", rows)
	}
	if rep, err := card.Fsck(ctx, client, sprint, false); err != nil || rep.Drift != 0 {
		t.Fatalf("fsck after push: %v %s %v", err, rep.Line("FSCK"), rep.Lines)
	}
}

// TestCardFsckRegistersAnUnregisteredStream: a stream with members of the
// sprint that is missing from ws:names and ws:order (a card pushed before
// the fix) is UNREGISTERED drift; --repair registers it (registered=1) and a
// second walk is clean. An ORPHAN-SET, a ws set whose members belong to no
// card of any sprint in sprint:order, is reported and never removed.
func TestCardFsckRegistersAnUnregisteredStream(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})

	f := validCard(srv.URL + "/acme/public.git")
	f.label = "stream-fsck-1"
	if res := card.Push(ctx, client, sprint, withHeader(f, "STREAM: late")); res.Code != 0 {
		t.Fatalf("push: exit %d stderr %q", res.Code, res.Stderr)
	}
	// The pre-fix store: members, but no name and no rank.
	client.SRem(ctx, "ws:names", "late")
	client.ZRem(ctx, "ws:order", "late")

	rep, err := card.Fsck(ctx, client, sprint, false)
	want := "UNREGISTERED stream=late members=1"
	if err != nil || rep.Drift != 1 || rep.Registered != 0 || len(rep.Lines) != 1 || rep.Lines[0] != want {
		t.Fatalf("read-only fsck: %v %s %q, want drift=1 and %q", err, rep.Line("FSCK"), rep.Lines, want)
	}
	if ok, _ := registered(t, ctx, client, "late"); ok || client.SIsMember(ctx, "ws:names", "late").Val() {
		t.Fatal("a read-only fsck registered the stream")
	}
	rep, err = card.Fsck(ctx, client, sprint, true)
	if err != nil || rep.Registered != 1 || rep.Fixed != 1 || !rep.Clean() || !strings.Contains(rep.Line("FSCK"), " registered=1") {
		t.Fatalf("fsck --repair: %v %s %q, want registered=1", err, rep.Line("FSCK"), rep.Lines)
	}
	if ok, rank := registered(t, ctx, client, "late"); !ok || rank != 1 {
		t.Fatalf("after repair: late registered=%v rank=%v", ok, rank)
	}
	if rep, err = card.Fsck(ctx, client, sprint, false); err != nil || rep.Drift != 0 {
		t.Fatalf("fsck after repair: %v %s %q", err, rep.Line("FSCK"), rep.Lines)
	}

	// Orphan sets: ws:gone:ready holds a card of a sprint not in
	// sprint:order and a task with no record; ws:late:ready holds this
	// sprint's live card and is not reported.
	client.SAdd(ctx, "ws:names", "gone")
	client.ZAdd(ctx, "ws:gone:ready", redis.Z{Score: 1, Member: "s:closed-sprint:card:a"}, redis.Z{Score: 2, Member: "no-such-task"})
	orph, err := card.OrphanSets(ctx, client)
	wantOrphan := "ORPHAN-SET key=ws:gone:ready members=2"
	if err != nil || orph.Orphans != 1 || len(orph.Lines) != 1 || orph.Lines[0] != wantOrphan {
		t.Fatalf("orphan sets = %+v (%v), want one %q", orph, err, wantOrphan)
	}
	if _, err := card.Fsck(ctx, client, sprint, true); err != nil {
		t.Fatal(err)
	}
	if n := client.ZCard(ctx, "ws:gone:ready").Val(); n != 2 {
		t.Fatalf("ws:gone:ready has %d members after repair, want 2 (reported, never removed)", n)
	}
	// A task record of a live sprint owns its set.
	client.HSet(ctx, "task:no-such-task", "sprint", sprint, "stream", "gone")
	if orph, err = card.OrphanSets(ctx, client); err != nil || orph.Orphans != 0 {
		t.Fatalf("orphan sets with a live task = %+v (%v), want none", orph, err)
	}
}
