package ws_test

import (
	"context"
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
