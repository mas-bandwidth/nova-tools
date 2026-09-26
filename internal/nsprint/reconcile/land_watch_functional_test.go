//go:build functional

package reconcile_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// TestLandWatchCutsANewIdEachEpisode (nova-tools #4324): through the real
// task push, a stream's first episode cuts merge-<slug>-1 for the frontier
// friend; when merging empties the card fields go but seq stays; the next
// episode cuts merge-<slug>-2 (a repeated id would be CONFLICT or CLOSED
// against the old card, one id per pass forever).
func TestLandWatchCutsANewIdEachEpisode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const s, sprint = "quack", "lw-sprint"
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.SAdd(ctx, "ws:names", s)
	c.HSet(ctx, "s:"+sprint, "status", "open")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	for _, f := range []string{"stella", "rowan"} {
		c.SAdd(ctx, "friends", f)
		c.HSet(ctx, "friend:"+f+":desired", "slots", 4, "paused", "0", "tiers", "frontier")
		c.HSet(ctx, "friend:"+f+":beat", "host", "fixture")
	}
	c.HSet(ctx, "friend:rowan:roles", "roles", "coordinator")
	now := time.UnixMilli(1700000000000)
	w := &reconcile.LandWatch{Client: c, Now: func() time.Time { return now }, Repo: "mas-bandwidth/quack",
		Notify: func(context.Context, reconcile.LandWake) error { return nil }}
	pass := func() {
		t.Helper()
		now = now.Add(time.Second)
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatalf("pass: %v", err)
		}
	}
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 1, Member: "q1"})
	pass()
	rec, _ := c.HGetAll(ctx, reconcile.LandMergeKey(s)).Result()
	if rec["task"] != "merge-quack-1" || rec["to"] != "rowan" && rec["to"] != "stella" || rec["seq"] != "1" {
		t.Fatalf("first episode: %v", rec)
	}
	card, _ := c.HGetAll(ctx, "task:merge-quack-1").Result()
	if card["kind"] != "merge" || card["state"] != "open" || card["dest"] != rec["to"] {
		t.Fatalf("merge card record: %v", card)
	}
	// The landing moved q1: the fields go, seq stays.
	c.ZRem(ctx, "ws:"+s+":merging", "q1")
	pass()
	if rec, _ := c.HGetAll(ctx, reconcile.LandMergeKey(s)).Result(); len(rec) != 1 || rec["seq"] != "1" {
		t.Fatalf("after the episode: %v", rec)
	}
	// The next episode gets the next id through the real push: no CONFLICT.
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 2, Member: "q2"})
	pass()
	if v, _ := c.HGet(ctx, reconcile.LandMergeKey(s), "task").Result(); v != "merge-quack-2" {
		t.Fatalf("second episode card %q", v)
	}
	if st, _ := c.HGet(ctx, "task:merge-quack-2", "state").Result(); st != "open" {
		t.Fatalf("second card state %q", st)
	}
	// A third pass with the card open cuts nothing more.
	pass()
	if v, _ := c.HGet(ctx, reconcile.LandMergeKey(s), "seq").Result(); v != "2" {
		t.Fatalf("seq after a steady pass %q", v)
	}
}

// TestLandWatchCrossStreamWaitsOnTheRealSentinel (nova-tools #4324
// DONE-WHEN with #4318's stop): on the real library, the streams' sentinels
// are the ones registration creates; a planted cross-stream end of alpha's
// merge card (paths= in beta's PATHS) cuts the coordinator's escalation
// through the real push with after=beta:sentinel; with the escalation closed
// and beta's stop in waiting the watch cuts nothing and the land duty's
// claim is refused on the edge; beta's stop landed through the one move
// (taskcard.Land) meets the edge and alpha's next merge card is cut.
func TestLandWatchCrossStreamWaitsOnTheRealSentinel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const a, b, sprint, repo = "alpha", "beta", "lw-sprint", "mas-bandwidth/quack"
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: a}, redis.Z{Score: 2, Member: b})
	c.SAdd(ctx, "ws:names", a, b)
	if r, err := taskcard.SentinelsWalk(ctx, c, true); err != nil || r.Created != 2 {
		t.Fatalf("sentinels: %+v %v", r, err)
	}
	if w := c.HGet(ctx, "task:"+ws.SentinelID(b), "where").Val(); w != "waiting" {
		t.Fatalf("beta's stop where=%q", w)
	}
	c.HSet(ctx, "s:"+sprint, "status", "open")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	for _, f := range []string{"stella", "rowan"} {
		c.SAdd(ctx, "friends", f)
		c.HSet(ctx, "friend:"+f+":desired", "slots", 4, "paused", "0", "tiers", "frontier")
		c.HSet(ctx, "friend:"+f+":beat", "host", "fixture")
	}
	c.HSet(ctx, "friend:rowan:roles", "roles", "coordinator")
	c.ZAdd(ctx, "ws:"+a+":merging", redis.Z{Score: 1, Member: "a1"})
	c.HSet(ctx, "task:a1", "paths", "a/")
	c.ZAdd(ctx, "ws:"+b+":working", redis.Z{Score: 1, Member: "b1"})
	c.HSet(ctx, "task:b1", "paths", "internal/y/")
	now := time.UnixMilli(1700000000000)
	var out bytes.Buffer
	w := &reconcile.LandWatch{Client: c, Now: func() time.Time { return now }, Repo: repo, Out: &out,
		Notify: func(context.Context, reconcile.LandWake) error { return nil }}
	pass := func() {
		t.Helper()
		now = now.Add(time.Second)
		if _, err := w.Run(ctx, nil); err != nil {
			t.Fatalf("pass: %v", err)
		}
	}
	pass()
	if st := c.HGet(ctx, "task:merge-alpha-1", "state").Val(); st != "open" {
		t.Fatalf("merge card state %q", st)
	}
	c.HSet(ctx, "task:merge-alpha-1", "state", "closed", "reason", "BLOCKED cross-stream paths=internal/y/z.go")
	pass()
	esc := c.HGetAll(ctx, "task:cross-alpha-1").Val()
	if esc["state"] != "open" || esc["kind"] != "work" || esc["dest"] != "rowan" || !strings.Contains(esc["title"], "AFTER: beta:sentinel") {
		t.Fatalf("escalation record: %v", esc)
	}
	rec := c.HGetAll(ctx, reconcile.LandMergeKey(a)).Val()
	if rec["after"] != "beta:sentinel" || rec["escalation"] != "cross-alpha-1" || rec["owner"] != "card:cross-alpha-1" {
		t.Fatalf("land:merge:alpha %v", rec)
	}
	if !strings.Contains(out.String(), "LAND-CROSS alpha card=merge-alpha-1 escalation=cross-alpha-1 to=rowan after=beta:sentinel ") {
		t.Fatalf("out:\n%s", out.String())
	}
	c.HSet(ctx, "task:cross-alpha-1", "state", "closed")
	pass()
	if n := c.Exists(ctx, "task:merge-alpha-2").Val(); n != 0 {
		t.Fatal("a merge card before beta's stop landed")
	}
	c.HSet(ctx, "lease:land:"+repo, "token", "feed")
	var held *stream.OwnedError
	if err := stream.Claim(ctx, c, []string{a}, stream.DutyOwner(repo, "feed"), now, now); !errors.As(err, &held) || held.Owner != "after:beta:sentinel" {
		t.Fatalf("the duty's claim before the stop: %v", err)
	}
	// Beta's last card goes; its stop lands through the one move.
	c.ZRem(ctx, "ws:"+b+":working", "b1")
	c.Del(ctx, "task:b1")
	if _, err := taskcard.Land(ctx, c, ws.SentinelID(b), "lander", strings.Repeat("d", 40), ""); err != nil {
		t.Fatalf("land beta's stop: %v", err)
	}
	pass()
	if st := c.HGet(ctx, "task:merge-alpha-2", "state").Val(); st != "open" {
		t.Fatalf("alpha's next merge card after the stop landed: state %q; out:\n%s", st, out.String())
	}
}
