package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// liveFixture is 5 cards in friend:emma:cards:working at now: 2 with a
// beat_at in the last 90 s (a task record 10 s old and a swarm card record
// 80 s old), and 3 without a live child: a task whose beat is 91 s old, a
// task that never beat, and a finished task (landed) left in the set with a
// fresh beat.
func liveFixture(now time.Time) [][]string {
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(-d).UnixMilli(), 10) }
	return [][]string{
		{"SADD", "friends", "emma"},
		{"HSET", "friend:emma", "at", now.Add(-time.Second).UTC().Format("2006-01-02T15:04:05Z"), "up", "1"},
		{"ZADD", "friend:emma:cards:working", "1", "live-a", "2", "s:s1:card:live-b", "3", "lapsed", "4", "never", "5", "landed"},
		{"HSET", "task:live-a", "where", "working", "friend", "emma", "owner", "emma", "beat_at", ms(10 * time.Second)},
		{"HSET", "s:s1:card:live-b", "where", "working", "owner", "emma", "state", "running", "beat_at", ms(80 * time.Second)},
		{"HSET", "task:lapsed", "where", "working", "friend", "emma", "owner", "emma", "beat_at", ms(91 * time.Second)},
		{"HSET", "task:never", "where", "working", "friend", "emma", "owner", "emma", "leased_at", ms(5 * time.Second)},
		{"HSET", "task:landed", "where", "landed", "friend", "emma", "owner", "emma", "beat_at", ms(time.Second)},
	}
}

// TestFriendWorkingCountsLiveBeatsOnly (DONE-WHEN of #3892; Glenn
// 2026-09-25 4:40 PM ET: "The friends table must be accurate each second.
// It must not lie."): 5 cards in friend:<f>:cards:working, 2 with beat_at
// in the last 90 s, show working=2 stale=3, in the tick's one pipeline. A
// beat moves the count on the next tick, and a lapsed beat moves it back.
// The same read runs on miniredis and on a real redis-server.
func TestFriendWorkingCountsLiveBeatsOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 20, 40, 0, 0, time.UTC)
	stores := map[string]func(t *testing.T) *redis.Client{
		"miniredis": func(t *testing.T) *redis.Client {
			c := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
			t.Cleanup(func() { _ = c.Close() })
			return c
		},
		"redis-server": func(t *testing.T) *redis.Client {
			_, c := wstest.Start(t)
			return c
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			c := open(t)
			seedCommands(t, c, liveFixture(now))
			log := &cmdLog{}
			c.AddHook(log)
			r := table.NewSprintReader(c, table.SprintConfig{Friends: []string{"emma"}, RowStale: time.Minute})
			tick := func(want, stale string) {
				t.Helper()
				snap, err := r.Read(ctx, now)
				if err != nil {
					t.Fatal(err)
				}
				row := snap.Friends[0]
				got := snap.Render(now)
				if row.Working != want || row.Stale != stale {
					t.Fatalf("emma working=%s stale=%s, want working=%s stale=%s:\n%s", row.Working, row.Stale, want, stale, got)
				}
				line := "emma       |     0 | " + strings.Repeat(" ", 7-len(want)) + want + " |     0 |     0 |     0 |    - | up stale=" + stale + "\n"
				total := "total      |     0 | " + strings.Repeat(" ", 7-len(want)) + want + " |     0 |     0 |     0 |    - | stale=" + stale + "\n"
				if !strings.Contains(got, line) || !strings.Contains(got, total) {
					t.Fatalf("rendered friend block lacks %q and %q:\n%s", line, total, got)
				}
			}
			tick("2", "3")
			if _, trips := log.reset(); trips > 2 {
				t.Fatalf("first read took %d round trips", trips)
			}
			tick("2", "3")
			if names, trips := log.reset(); trips != 1 {
				t.Fatalf("steady tick took %d round trips: %v", trips, names)
			}
			// The lapsed card's child beats: live on the next tick.
			seedCommands(t, c, [][]string{{"HSET", "task:lapsed", "beat_at", strconv.FormatInt(now.UnixMilli(), 10)}})
			tick("3", "2")
			// 30 s on, live-b's beat is 110 s old: stale, without any write.
			now = now.Add(30 * time.Second)
			tick("2", "3")
		})
	}
}

// TestFriendWorkingUnreadIsNotZero: a working set the tick could not read
// (a WRONGTYPE key) prints "?" for working and stale, never a false number.
func TestFriendWorkingUnreadIsNotZero(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 25, 20, 40, 0, 0, time.UTC)
	c := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = c.Close() })
	seedCommands(t, c, [][]string{{"SET", "friend:emma:cards:working", "not a set"}})
	snap, err := table.NewSprintReader(c, table.SprintConfig{Friends: []string{"emma"}}).Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Render(now)
	if row := snap.Friends[0]; row.Working != "?" || row.Stale != "?" || !strings.Contains(got, "total      |     0 |       ? |     0 |     0 |     0 |    - | stale=?\n") {
		t.Fatalf("unread working set: working=%q stale=%q\n%s", row.Working, row.Stale, got)
	}
}
