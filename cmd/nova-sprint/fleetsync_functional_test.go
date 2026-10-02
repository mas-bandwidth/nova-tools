//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFleetSyncFollowsTheInventoryOnTheStore: the inventory (machine rows,
// friend rows and the friends' beats in the store, read by config.Widths and
// RedisApplier.FriendHosts) becomes the fleet table at each machine's width;
// a change of the inventory is followed (a width, a machine with no room, a
// machine gone); a sync after a sync writes nothing; and the tick's deal
// never takes a machine past DealAhead times the width the sync set.
func TestFleetSyncFollowsTheInventoryOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, fn.Load(ctx, c))

	cfg := config.NewMem()
	machine := func(name string, slots int) {
		row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": strconv.Itoa(slots), "runners": "0"}}
		if _, found, _ := cfg.Get(ctx, config.KindMachine, name); found {
			_, _, err := cfg.Update(ctx, config.KindMachine, name, map[string]string{"slots": strconv.Itoa(slots)}, "t")
			require.NoError(t, err)
			return
		}
		_, err := cfg.Insert(ctx, config.KindMachine, row, "t")
		require.NoError(t, err)
	}
	machine("m1", 8)
	machine("m2", 4)
	machine("m3", 2)
	// f1 runs on m1 by her beat; f2 has no beat and is charged to the fleet's
	// coordinator machine
	for f, slots := range map[string]string{"f1": "3", "f2": "1"} {
		_, err := cfg.Insert(ctx, config.KindFriend, config.Row{Name: f, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}, "t")
		require.NoError(t, err)
	}
	_, _, err := cfg.Update(ctx, config.KindFleet, config.KindFleet, map[string]string{"coordinator": "m2"}, "t")
	require.NoError(t, err)
	require.NoError(t, c.HSet(ctx, config.FriendBeatKey("f1"), "host", "m1").Err())

	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	world := newApp(func(k string) string { return env[k] })
	defer world.close()
	world.inventory = func(ctx context.Context, _, _ string) ([]config.MachineWidth, error) {
		return config.Widths(ctx, cfg, &config.RedisApplier{Client: c})
	}
	run := func(want int, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := world.run(args, &out, &errb)
		require.Equal(t, want, code, "%v: exit %d, want %d\n%s%s", args, code, want, out.String(), errb.String())
		return out.String()
	}
	rows := func() map[string]map[string]string {
		t.Helper()
		var w whereView
		require.NoError(t, json.Unmarshal([]byte(run(0, "where", "--json")), &w))
		return w.Tables["fleet"]
	}

	run(0, "init", "--readers", "reader-a,reader-b")
	out := run(0, "fleet", "sync")
	require.Contains(t, out, "FLEET-SYNC OK moved=3", "the first sync")
	// m1: 8 less f1's 3; m2: 4 less f2's 1 (the coordinator machine); m3: 2
	got := rows()
	require.Equal(t, "5", got["m1"]["width"], "widths after the sync: m1=%s m2=%s m3=%s", got["m1"]["width"], got["m2"]["width"], got["m3"]["width"])
	require.Equal(t, "3", got["m2"]["width"], "widths after the sync: m1=%s m2=%s m3=%s", got["m1"]["width"], got["m2"]["width"], got["m3"]["width"])
	require.Equal(t, "2", got["m3"]["width"], "widths after the sync: m1=%s m2=%s m3=%s", got["m1"]["width"], got["m2"]["width"], got["m3"]["width"])
	require.Contains(t, run(0, "fleet", "sync"), "nothing to do", "the second sync")
	run(0, "fleet", "sync", "--check")

	// the inventory changes: m1 is wider, m3 has no slots, m2 is gone
	machine("m1", 10)
	machine("m3", 0)
	// m2 is the fleet's coordinator machine and cannot be removed while it is: clear that
	_, _, err = cfg.Update(ctx, config.KindFleet, config.KindFleet, map[string]string{"coordinator": ""}, "t")
	require.NoError(t, err)
	_, err = cfg.Delete(ctx, config.KindMachine, "m2", "t")
	require.NoError(t, err)
	// f2 now has no machine to be charged to: the inventory cannot be read
	// (exit 3) and nothing is written
	var errb bytes.Buffer
	code := world.run([]string{"fleet", "sync"}, &bytes.Buffer{}, &errb)
	require.Equal(t, 3, code, "a friend charged to nothing: exit %d %s", code, errb.String())
	require.Contains(t, errb.String(), "fleet names no coordinator machine", "a friend charged to nothing: exit %d %s", code, errb.String())
	_, _, err = cfg.Update(ctx, config.KindFleet, config.KindFleet, map[string]string{"coordinator": "m1"}, "t")
	require.NoError(t, err)
	run(2, "fleet", "sync", "--check")
	out = run(0, "fleet", "sync")
	require.Contains(t, out, "m3 held down", "the holds")
	require.Contains(t, out, "m2 held down", "the holds")
	got = rows()
	// m1: 10 less f1's 3 and f2's 1 (now charged to m1)
	require.Equal(t, "6", got["m1"]["width"], "after the inventory changed: %v", got)
	require.Equal(t, "held", got["m2"]["status"], "after the inventory changed: %v", got)
	require.Equal(t, "held", got["m3"]["status"], "after the inventory changed: %v", got)
	require.Contains(t, run(0, "fleet", "sync"), "nothing to do", "the sync after it")

	// the deal fills the synced member to its dealt-ahead room and no further
	run(0, "add", "--stream", "s1", "--count", "20")
	run(0, "fleet", "beat", "m1")
	run(0, "start")
	run(0, "tick")
	run(0, "tick")
	got = rows()
	held := 0
	for _, col := range []string{"ready", "working"} {
		n, _ := strconv.Atoi(got["m1"][col])
		held += n
	}
	assert.Equal(t, sprint.DealAhead*6, held, "the synced width is 6: %v", got["m1"])
}
