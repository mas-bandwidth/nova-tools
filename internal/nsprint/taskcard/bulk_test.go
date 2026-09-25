package taskcard_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sprinttable "github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// calls counts round trips: one per command, one per pipeline.
type calls struct{ n atomic.Int64 }

func (h *calls) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *calls) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}
func (h *calls) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmds)
	}
}

// counted runs f and returns how many round trips it made.
func (h *calls) counted(f func()) int64 {
	before := h.n.Load()
	f()
	return h.n.Load() - before
}

// friendRow is the friend block's row as the sprint table reads it (ZCARDs,
// one pipeline).
func friendRow(t *testing.T, c *redis.Client, friend string) sprinttable.FriendRow {
	t.Helper()
	snap, err := sprinttable.NewSprintReader(c, sprinttable.SprintConfig{Friends: []string{friend}}).Read(context.Background(), time.Now())
	if err != nil || len(snap.Friends) != 1 {
		t.Fatalf("table read %v %v", snap, err)
	}
	return snap.Friends[0]
}

// TestBulkAssignExactWorking is the #3915 DONE-WHEN (Glenn 2026-09-25 11:40
// AM ET: "Can it be in bulk, but also be accurate?"): a deal of 50 cards to
// one friend is one Redis call and leaves ready=50 working=0; a take of 8 is
// one call and leaves ready=42 working=8 with 8 leases; 200 s later, with 5
// of the 8 beating (one call), the lease duty returns 3 to ready and the
// table prints ready=45 working=5; a take by a friend with no live beat is
// refused.
func TestBulkAssignExactWorking(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	h := &calls{}
	c.AddHook(h)
	c.SAdd(ctx, "friends", "emma")
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("bulk-%02d", i)
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Stream: stream, Sprint: sprint, By: "rowan"}); err != nil {
			t.Fatal(err)
		}
	}
	// one card already another friend's stays out of the deal
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "stella-own", Stream: stream, Friend: "stella", Sprint: sprint, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	clean(t, c, "push")

	var dealt []string
	var err error
	if n := h.counted(func() { dealt, err = taskcard.Deal(ctx, c, "emma", 60, "reconciler", stream) }); n != 1 {
		t.Fatalf("deal took %d round trips, want 1", n)
	}
	if err != nil || len(dealt) != 50 || dealt[0] != "bulk-00" {
		t.Fatalf("deal %d %v %v", len(dealt), dealt, err)
	}
	if r := friendRow(t, c, "emma"); r.Ready != "50" || r.Working != "0" {
		t.Fatalf("after the deal: ready=%s working=%s, want 50 and 0", r.Ready, r.Working)
	}
	if o := c.HGet(ctx, "task:stella-own", "friend").Val(); o != "stella" {
		t.Fatalf("the deal took stella's card: friend=%s", o)
	}
	clean(t, c, "deal")

	// no beat, no take: no child can be running it
	if _, err := taskcard.Take(ctx, c, "emma", 8, "emma"); err == nil || !strings.Contains(err.Error(), "NOBEAT") {
		t.Fatalf("take with no live beat: %v, want NOBEAT", err)
	}
	c.HSet(ctx, "friend:emma:beat", "session", "s1", "at", time.Now().UnixMilli())
	var took []string
	if n := h.counted(func() { took, err = taskcard.Take(ctx, c, "emma", 8, "emma") }); n != 1 {
		t.Fatalf("take took %d round trips, want 1", n)
	}
	if err != nil || len(took) != 8 || took[0] != "bulk-00" || took[7] != "bulk-07" {
		t.Fatalf("take %v %v", took, err)
	}
	if r := friendRow(t, c, "emma"); r.Ready != "42" || r.Working != "8" {
		t.Fatalf("after take 8: ready=%s working=%s, want 42 and 8", r.Ready, r.Working)
	}
	for _, id := range took {
		if c.HGet(ctx, "task:"+id, "lease_until").Val() == "" {
			t.Fatalf("%s taken without a lease", id)
		}
	}
	clean(t, c, "take")

	// 200 s pass: every lease taken then is 20 s past its 180 s.
	past := time.Now().Add(-200 * time.Second).UnixMilli()
	for _, id := range took {
		c.HSet(ctx, "task:"+id, "lease_until", past+180000, "leased_at", past, "where_at", past)
	}
	var beat taskcard.BeatAllResult
	if n := h.counted(func() { beat, err = taskcard.BeatAll(ctx, c, "emma", "emma", took[:5]...) }); n != 1 {
		t.Fatalf("beat took %d round trips, want 1", n)
	}
	if err != nil || beat.Renewed != 5 || len(beat.Refused) != 0 || beat.Until <= time.Now().UnixMilli() {
		t.Fatalf("beat %+v %v", beat, err)
	}
	if r, err := taskcard.BeatAll(ctx, c, "stella", "stella", took[0]); err != nil || r.Renewed != 0 || !strings.HasPrefix(r.Refused[took[0]], "OWNER") {
		t.Fatalf("stella beat emma's card: %+v %v", r, err)
	}
	back, err := taskcard.Expire(ctx, c, "reconciler", "emma")
	if err != nil || strings.Join(back, ",") != strings.Join(took[5:], ",") {
		t.Fatalf("lease duty returned %v %v, want %v", back, err, took[5:])
	}
	r := friendRow(t, c, "emma")
	if r.Ready != "45" || r.Working != "5" {
		t.Fatalf("after the lease duty: ready=%s working=%s, want 45 and 5", r.Ready, r.Working)
	}
	snap, _ := sprinttable.NewSprintReader(c, sprinttable.SprintConfig{Friends: []string{"emma"}}).Read(ctx, time.Now())
	if out := snap.Render(time.Now()); !strings.Contains(out, fmt.Sprintf("%-10s | %5s | %7s |", "emma", "45", "5")) {
		t.Fatalf("table:\n%s", out)
	}
	clean(t, c, "expire")

	// the beat is gone: the next take is refused, the ready cards stay
	c.Del(ctx, "friend:emma:beat")
	if _, err := taskcard.Take(ctx, c, "emma", 1, "emma"); err == nil || !strings.Contains(err.Error(), "NOBEAT") {
		t.Fatalf("take after the beat lapsed: %v, want NOBEAT", err)
	}
	if r := friendRow(t, c, "emma"); r.Ready != "45" || r.Working != "5" {
		t.Fatalf("a refused take moved cards: ready=%s working=%s", r.Ready, r.Working)
	}
}

// TestDealRefusesAndWritesNothing: a named id that is not ready, or another
// friend's, and an unregistered friend refuse the whole deal.
func TestDealRefusesAndWritesNothing(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	for _, id := range []string{"d1", "d2"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Stream: stream, Sprint: sprint}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "d3", Stream: stream, Friend: "stella", Sprint: sprint}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Deal(ctx, c, "nobody", 0, "rowan", "", "d1"); err == nil || !strings.Contains(err.Error(), "UNREGISTERED") {
		t.Fatalf("deal to an unregistered friend: %v", err)
	}
	if _, err := taskcard.Deal(ctx, c, "rowan", 0, "rowan", "", "d1", "d3"); err == nil || !strings.Contains(err.Error(), "OWNER") {
		t.Fatalf("deal of stella's card: %v", err)
	}
	if n := c.ZCard(ctx, taskcard.FriendKey("rowan", "ready")).Val(); n != 0 {
		t.Fatalf("a refused deal wrote %d", n)
	}
	got, err := taskcard.Deal(ctx, c, "rowan", 0, "rowan", "", "d2", "d1")
	if err != nil || strings.Join(got, ",") != "d2,d1" {
		t.Fatalf("deal by ids %v %v", got, err)
	}
	clean(t, c, "deal")
}
