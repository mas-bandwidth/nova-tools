package beat_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/beat"
	"github.com/redis/go-redis/v9"
)

// TestStoppedBeatReadsDown (#4233): a friend's beat has no TTL, so its
// existence says nothing; a beat whose at is under Window old reads up, and
// the same key once the friend stopped beating (at two minutes old) reads
// down, as does a beat with no at, an unparsable at, or no beat at all.
func TestStoppedBeatReadsDown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	for _, tc := range []struct {
		at   string
		want bool
	}{
		{ms(-time.Second), true}, {ms(-59 * time.Second), true}, {ms(-2 * time.Minute), false}, {ms(2 * time.Minute), false},
		{strconv.FormatInt(now.Add(-time.Second).Unix(), 10), true}, {now.Add(-30 * time.Second).Format(time.RFC3339), true},
		{now.Add(-3 * time.Minute).Format(time.RFC3339), false}, {"2026-09-26T16:59:30Z", true}, {"", false}, {"1", false}, {"soon", false},
	} {
		if got := beat.Up(tc.at, now); got != tc.want {
			t.Errorf("Up(%q) = %v, want %v", tc.at, got, tc.want)
		}
	}

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	mr.HSet(beat.Key("rowan"), "host", "laptop", "at", ms(-time.Second))
	mr.HSet(beat.Key("emma"), "host", "studio") // beat with no at
	pipe := client.Pipeline()
	rowan, emma, ghost := beat.Read(ctx, pipe, "rowan"), beat.Read(ctx, pipe, "emma"), beat.Read(ctx, pipe, "ghost")
	_, _ = pipe.Exec(ctx)
	if !beat.UpCmd(rowan, now) || beat.UpCmd(emma, now) || beat.UpCmd(ghost, now) || beat.UpCmd(nil, now) {
		t.Fatalf("rowan up=%v emma up=%v ghost up=%v", beat.UpCmd(rowan, now), beat.UpCmd(emma, now), beat.UpCmd(ghost, now))
	}
	// the friend stopped beating: the key stays, the friend reads down
	mr.HSet(beat.Key("rowan"), "at", ms(-2*time.Minute))
	stopped := beat.Read(ctx, client, "rowan")
	if beat.UpCmd(stopped, now) {
		t.Fatal("a beat two minutes old reads up")
	}
}
