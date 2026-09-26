package ws_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// TestCountsLeaveEverySentinelOut (#4411, the coordinator's ruling on
// Glenn's "zeros everywhere after sprint clear"): a stream's sentinel is its
// stop, not work: in no cell (waiting, landed or parked), no total and no
// left, and its landing is not in the eta's rate; a stream holding only its
// sentinel counts 0; with no card the eta is "-", never the current time.
func TestCountsLeaveEverySentinelOut(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	logAt := func(d time.Duration, id string) {
		t.Helper()
		if err := c.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: formatMs(now.Add(d)), Values: []any{"id", id, "to", ws.Landed}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, z := range []struct {
		key string
		ids []string
	}{
		{"ws:order", []string{"alpha", "beta", "gamma"}},
		{ws.Key("alpha", ws.Waiting), []string{ws.SentinelID("alpha"), "a1"}},
		{ws.Key("alpha", ws.Landed), []string{"a2"}},
		{ws.Key("beta", ws.Landed), []string{ws.SentinelID("beta"), "b1"}},
		{ws.Key("gamma", ws.Parked), []string{ws.SentinelID("gamma")}},
	} {
		for i, id := range z.ids {
			if err := c.ZAdd(ctx, z.key, redis.Z{Score: float64(i + 1), Member: id}).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	logAt(-40*time.Minute, "a2")
	logAt(-30*time.Minute, ws.SentinelID("beta"))
	logAt(-20*time.Minute, "b1")
	got, err := (&ws.CountsReader{}).Read(ctx, c, now)
	if err != nil {
		t.Fatal(err)
	}
	// 3 cards (a1, a2, b1), 2 landed at 2 an hour: 1 left is 30 minutes
	if h := got.Header(); h != "2/3 done 66%, left 1, eta 14:30 ET" {
		t.Fatalf("header %q; want 2/3 done 66%%, left 1, eta 14:30 ET (the sentinels aside)", h)
	}
	if g := got.Streams[2]; g.Sum() != 0 || g.Parked != 0 {
		t.Fatalf("gamma (its sentinel alone, parked) %+v; want 0 everywhere", g)
	}

	// no card: the eta is "-" even with landings in the hour
	for _, s := range []string{"alpha", "beta"} {
		for _, w := range []string{ws.Waiting, ws.Landed} {
			c.ZRem(ctx, ws.Key(s, w), "a1", "a2", "b1")
		}
	}
	got, err = (&ws.CountsReader{}).Read(ctx, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.All() != 0 || got.ETA() != "-" || got.Header() != "0/0 done 0%, left 0, eta -" {
		t.Fatalf("no card: %q (eta %q); want 0/0 done 0%%, left 0, eta -", got.Header(), got.ETA())
	}
}

func formatMs(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) + "-0" }

// TestCountsRefuseASprintNotOpen (#4411): a CountsReader naming a sprint
// that is not the open one returns *ws.NotOpen naming the open sprint (or
// "-" with none open), never the ws index's counts under the other name; the
// open sprint's own name reads them, and so does a primed reader (the live
// layout's SCAN trip) on its first pipeline.
func TestCountsRefuseASprintNotOpen(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: "alpha"})
	c.ZAdd(ctx, ws.Key("alpha", ws.Landed), redis.Z{Score: 1, Member: "a1"})
	c.ZAdd(ctx, ws.Key("alpha", ws.Ready), redis.Z{Score: 1, Member: "a2"})

	notOpen := func(name, wantOpen string) {
		t.Helper()
		_, err := (&ws.CountsReader{Sprint: name}).Read(ctx, c, now)
		var e *ws.NotOpen
		if !errors.As(err, &e) || e.Name != name || e.Open != wantOpen {
			t.Fatalf("--sprint %s: %v; want NotOpen open=%q", name, err, wantOpen)
		}
	}
	notOpen("s1", "") // no sprint open at all
	if _, err := (&ws.CountsReader{Sprint: "s1"}).Read(ctx, c, now); err == nil ||
		err.Error() != "--sprint s1: not the open sprint; open=-" {
		t.Fatalf("the refusal's words: %v", err)
	}
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "old"}, redis.Z{Score: 2, Member: "s1"})
	c.HSet(ctx, "s:old", "status", "closed")
	c.HSet(ctx, "s:s1", "status", "open")
	notOpen("old", "s1") // closed
	notOpen("other", "s1")
	got, err := (&ws.CountsReader{Sprint: "s1"}).Read(ctx, c, now)
	if err != nil || got.Sprint != "s1" || got.Status != "open" || got.Header() != "1/2 done 50%, left 1, eta ?" {
		t.Fatalf("--sprint s1 (open): %+v %v", got, err)
	}
	// primed: the memberships from an earlier trip, then one pipeline
	r := &ws.CountsReader{Sprint: "other"}
	pipe := c.Pipeline()
	order, sprints := r.QueueMembers(ctx, pipe)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Prime(order, sprints); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(ctx, c, now); !errors.As(err, new(*ws.NotOpen)) {
		t.Fatalf("primed --sprint other: %v; want NotOpen", err)
	}
}
