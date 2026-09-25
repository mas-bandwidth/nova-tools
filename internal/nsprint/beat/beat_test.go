package beat

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestJudge(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exists    bool
		at, stale string
		now       int64
		want      bool
	}{
		{"absent", false, "1000", "5000", 1000, false},
		{"inside the window", true, "1000", "5000", 5999, true},
		{"at the window", true, "1000", "5000", 6000, false},
		{"no stale_ms: existence", true, "1", "", 99999999, true},
		{"no at: existence", true, "", "5000", 99999999, true},
	} {
		if got := Judge(tc.exists, tc.at, tc.stale, tc.now); got != tc.want {
			t.Errorf("%s: Judge = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestLiveNowReadsTheStoreClock: the beat is judged against the store's TIME,
// in one pipeline, and a key with no TTL reads down once its at is older than
// its stale_ms.
func TestLiveNowReadsTheStoreClock(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	mr.SetTime(now)
	mr.HSet(FriendKey("emma"), FieldAt, "1790000000000", FieldStale, "5000")
	if live, err := LiveNow(ctx, c, FriendKey("emma")); err != nil || !live {
		t.Fatalf("fresh beat: live=%v err=%v; want live", live, err)
	}
	mr.SetTime(now.Add(5000 * time.Millisecond))
	if live, err := LiveNow(ctx, c, FriendKey("emma")); err != nil || live {
		t.Fatalf("stale beat: live=%v err=%v; want down", live, err)
	}
	if live, err := LiveNow(ctx, c, BenchKey("none")); err != nil || live {
		t.Fatalf("absent beat: live=%v err=%v; want down", live, err)
	}
}
