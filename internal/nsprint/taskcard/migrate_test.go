package taskcard_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// today writes the shapes the fleet Redis held at 07:46 ET 2026-09-25: bash
// friend-queue pushes (ISO created_at, idx sets, a q:<f> entry, no stream
// field), ws-era records (state names a ws set), stale working rows nobody
// beats, cancelled and closed rows, a row in two ws sets (the refused
// ready->landed), a truth-merged row still in ready, the task:pr map hash
// and a card id in a ws set.
func today(t *testing.T, c *redis.Client) []taskcard.TruthRow {
	t.Helper()
	ctx := context.Background()
	const swarm = "swarm: cards"
	now := time.Now()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(-d).UnixMilli(), 10) }
	iso := func(d time.Duration) string { return now.Add(-d).UTC().Format("2006-01-02T15:04:05Z") }
	ix := func(f, st string) string { return "sprint:" + sprint + ":idx:" + f + ":" + st }
	p := c.Pipeline()
	p.SAdd(ctx, "ws:names", swarm, stream)
	p.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: swarm}, redis.Z{Score: 2, Member: stream})
	p.SAdd(ctx, "friends", "rowan", "stella")
	// 1. bash push, its PR merged: landed by the truth
	p.HSet(ctx, "task:read-3726-e5613a0b", "kind", "read", "ref", "nova-tools#3726", "title", "STREAM: swarm: cards | read nova-tools#3726",
		"owner", "stella", "state", "open", "head", "e5613a0b", "created_at", iso(3*time.Hour), "front", "0")
	p.SAdd(ctx, ix("stella", "open"), "read-3726-e5613a0b")
	p.XAdd(ctx, &redis.XAddArgs{Stream: "q:stella", Values: []any{"id", "read-3726-e5613a0b"}})
	// 2. ws-era ready
	p.HSet(ctx, "task:build-3420", "kind", "go-fix", "ref", "nova-tools#3420", "stream", stream, "state", "ready", "owner", "rowan", "created_at", ms(5*time.Hour))
	p.ZAdd(ctx, taskcard.StreamKey(stream, "ready"), redis.Z{Score: float64(now.Add(-5 * time.Hour).UnixMilli()), Member: "build-3420"})
	p.SAdd(ctx, ix("rowan", "open"), "build-3420")
	// 3. working, nobody beat it for an hour: back to ready
	p.HSet(ctx, "task:build-3321", "kind", "lua-fn", "ref", "nova-tools#3321", "stream", stream, "state", "working", "owner", "rowan",
		"created_at", ms(6*time.Hour), "leased_at", iso(time.Hour))
	p.ZAdd(ctx, taskcard.StreamKey(stream, "working"), redis.Z{Score: 1, Member: "build-3321"})
	p.SAdd(ctx, ix("rowan", "working"), "build-3321")
	// 4. working, taken a minute ago: stays working
	p.HSet(ctx, "task:build-3322", "kind", "go-fix", "ref", "nova-tools#3322", "stream", stream, "state", "working", "owner", "rowan",
		"created_at", ms(6*time.Hour), "leased_at", iso(time.Minute))
	p.ZAdd(ctx, taskcard.StreamKey(stream, "working"), redis.Z{Score: 1, Member: "build-3322"})
	p.SAdd(ctx, ix("rowan", "working"), "build-3322")
	// 5. cancelled, 6. closed (bash)
	p.HSet(ctx, "task:fix-1", "kind", "fix", "ref", "nova-tools#1", "owner", "stella", "state", "closed", "cancelled", "1", "created_at", iso(9*time.Hour))
	p.SAdd(ctx, ix("stella", "closed"), "fix-1")
	p.HSet(ctx, "task:fix-2", "kind", "fix", "ref", "nova-tools#2", "owner", "stella", "state", "closed", "created_at", iso(9*time.Hour))
	p.SAdd(ctx, ix("stella", "closed"), "fix-2")
	// 7. merging, merged on GitHub: landed
	p.HSet(ctx, "task:build-3700", "kind", "go-verb", "ref", "nova-tools#3700", "stream", stream, "state", "merging", "owner", "rowan", "created_at", ms(8*time.Hour))
	p.ZAdd(ctx, taskcard.StreamKey(stream, "merging"), redis.Z{Score: 1, Member: "build-3700"})
	// 8. in two sets (ready and landed): the furthest wins
	p.HSet(ctx, "task:build-3701", "kind", "go-verb", "ref", "nova-tools#3701", "stream", stream, "state", "ready", "created_at", ms(8*time.Hour))
	p.ZAdd(ctx, taskcard.StreamKey(stream, "ready"), redis.Z{Score: 1, Member: "build-3701"})
	p.ZAdd(ctx, taskcard.StreamKey(stream, "landed"), redis.Z{Score: 1, Member: "build-3701"})
	// 9. stale working whose PR merged: landed, not ready
	p.HSet(ctx, "task:build-3702", "kind", "go-verb", "ref", "nova-tools#3702", "stream", stream, "state", "working", "owner", "rowan",
		"created_at", ms(8*time.Hour), "leased_at", iso(2*time.Hour))
	p.ZAdd(ctx, taskcard.StreamKey(stream, "working"), redis.Z{Score: 1, Member: "build-3702"})
	p.SAdd(ctx, ix("rowan", "working"), "build-3702")
	// 10. waiting (bash q:waiting), no stream: a friend-only task
	p.HSet(ctx, "task:build-3703", "kind", "go-verb", "ref", "nova-tools#3703", "owner", "rowan", "state", "waiting", "created_at", iso(time.Hour))
	p.ZAdd(ctx, "q:waiting", redis.Z{Score: 1, Member: "build-3703"})
	// 11. the task:pr map and a card in a ws set
	p.HSet(ctx, "task:pr", "nova-tools#3700", "build-3700")
	p.ZAdd(ctx, taskcard.StreamKey(swarm, "ready"), redis.Z{Score: 1, Member: "s:quack-0925:card:s00-0101-quack-batman-flash"})
	if _, err := p.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	truth := `swarm: cards|ready|s:quack-0925:card:s00-0101-quack-batman-flash||#|-
swarm: cards|ready|read-3726-e5613a0b|read|nova-tools#3726|merged
nova-sprint + merge + bus|ready|build-3420|go-fix|nova-tools#3420|open
nova-sprint + merge + bus|merging|build-3700|go-verb|nova-tools#3700|merged
nova-sprint + merge + bus|working|build-3702|go-verb|nova-tools#3702|merged
nova-sprint + merge + bus|working|build-3321|lua-fn|nova-tools#3321|open
`
	rows, err := taskcard.ReadTruth(strings.NewReader(truth))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestMigrateTodaysShapes: task migrate over today's shapes places every
// record in exactly one set, lands what the truth says merged, sends stale
// working back to ready, ends with fsck clean and the expected landed count,
// and is idempotent.
func TestMigrateTodaysShapes(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	truth := today(t, c)
	res, err := taskcard.Migrate(ctx, c, sprint, "rowan", truth, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"landed": 4, "ready": 2, "working": 1, "waiting": 1, "done": 2}
	for w, n := range want {
		if res.Placed[w] != n {
			t.Errorf("placed %s = %d, want %d (%+v)", w, res.Placed[w], n, res)
		}
	}
	if res.Skipped["notatask"] != 1 {
		t.Errorf("skipped %v, want task:pr as notatask", res.Skipped)
	}
	where := func(id string) string {
		h := c.HMGet(ctx, "task:"+id, "where", "where_ok").Val()
		return h[0].(string) + "/" + h[1].(string)
	}
	for id, w := range map[string]string{
		"read-3726-e5613a0b": "landed/ok", "build-3420": "ready/-", "build-3321": "ready/-", "build-3322": "working/-",
		"fix-1": "done/fail", "fix-2": "done/ok", "build-3700": "landed/ok", "build-3701": "landed/ok",
		"build-3702": "landed/ok", "build-3703": "waiting/-",
	} {
		if got := where(id); got != w {
			t.Errorf("%s placed %s, want %s", id, got, w)
		}
	}
	if s := c.HGet(ctx, "task:read-3726-e5613a0b", "stream").Val(); s != "swarm: cards" {
		t.Errorf("stream from the title: %q", s)
	}
	if c.ZScore(ctx, taskcard.StreamKey("swarm: cards", "ready"), "s:quack-0925:card:s00-0101-quack-batman-flash").Err() != nil {
		t.Error("migrate moved a card id")
	}
	r := clean(t, c, "after migrate")
	if r.Counts["landed"] != 4 || r.Unplaced != 0 {
		t.Fatalf("fsck after migrate %+v", r)
	}
	if n := c.ZCard(ctx, taskcard.FriendKey("rowan", "working")).Val(); n != 1 {
		t.Fatalf("rowan working %d after migrate, want 1 (only the task with a live lease)", n)
	}
	again, err := taskcard.Migrate(ctx, c, sprint, "rowan", truth, 100)
	if err != nil {
		t.Fatal(err)
	}
	for w, n := range want {
		if again.Placed[w] != n {
			t.Errorf("second migrate placed %s = %d, want %d", w, again.Placed[w], n)
		}
	}
	clean(t, c, "after a second migrate")
	// a moved card after migrate goes through the one move like any other
	if _, err := taskcard.Take(ctx, c, "rowan", 1, "rowan", "build-3420"); err != nil {
		t.Fatal(err)
	}
	clean(t, c, "take after migrate")
}
