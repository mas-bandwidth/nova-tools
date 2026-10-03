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

// TestFleetSyncFollowsTheInventoryOnTheStore: the inventory (the machine
// rows' width fields, read by config.Widths) becomes the fleet table at each
// machine's width, whatever friend rows and friends' beats the store holds; a
// change of the inventory is followed (a width, a machine with width 0, a
// machine gone and back); a sync after a sync writes nothing; and the tick's deal
// never takes a machine past DealAhead times the width the sync set.
func TestFleetSyncFollowsTheInventoryOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, fn.Load(ctx, c))

	cfg := config.NewMem()
	machine := func(name string, width int) {
		row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": "160", "runners": "0", "width": strconv.Itoa(width)}}
		if _, found, _ := cfg.Get(ctx, config.KindMachine, name); found {
			_, _, err := cfg.Update(ctx, config.KindMachine, name, map[string]string{"width": strconv.Itoa(width)}, "t")
			require.NoError(t, err)
			return
		}
		_, err := cfg.Insert(ctx, config.KindMachine, row, "t")
		require.NoError(t, err)
	}
	machine("m1", 5)
	machine("m2", 3)
	machine("m3", 2)
	// f1 runs on m1 by her beat and f2 has no beat: neither takes anything
	// off any width
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
	world.inventory = func(ctx context.Context, _ string) ([]config.MachineWidth, error) {
		return config.Widths(ctx, cfg)
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
	// each at the width its row carries
	got := rows()
	require.Equal(t, "5", got["m1"]["width"], "widths after the sync: m1=%s m2=%s m3=%s", got["m1"]["width"], got["m2"]["width"], got["m3"]["width"])
	require.Equal(t, "3", got["m2"]["width"], "widths after the sync: m1=%s m2=%s m3=%s", got["m1"]["width"], got["m2"]["width"], got["m3"]["width"])
	require.Equal(t, "2", got["m3"]["width"], "widths after the sync: m1=%s m2=%s m3=%s", got["m1"]["width"], got["m2"]["width"], got["m3"]["width"])
	require.Contains(t, run(0, "fleet", "sync"), "nothing to do", "the second sync")
	run(0, "fleet", "sync", "--check")

	// the inventory changes: m1 is wider, m3 has width 0, m2 is gone: m3 is held and m2 removed
	machine("m1", 6)
	machine("m3", 0)
	// m2 is the fleet's coordinator machine and cannot be removed while it is: move that
	_, _, err = cfg.Update(ctx, config.KindFleet, config.KindFleet, map[string]string{"coordinator": "m1"}, "t")
	require.NoError(t, err)
	_, err = cfg.Delete(ctx, config.KindMachine, "m2", "t")
	require.NoError(t, err)
	run(2, "fleet", "sync", "--check")
	out = run(0, "fleet", "sync")
	require.Contains(t, out, "m3 held down", "the hold")
	require.Contains(t, out, "m2 removed", "the removal: no machine row and no card on it")
	got = rows()
	require.Equal(t, "6", got["m1"]["width"], "after the inventory changed: %v", got)
	require.Nil(t, got["m2"], "m2's row is deleted: %v", got)
	require.Equal(t, "held", got["m3"]["status"], "after the inventory changed: %v", got)
	require.Contains(t, run(0, "fleet", "sync"), "nothing to do", "the sync after it")

	// m2's machine row comes back: its control card is placed again by the table
	// layer's cell add, and the sync releases it at its width
	machine("m2", 3)
	out = run(0, "fleet", "sync")
	require.Contains(t, out, "NOTE m2 rejoins the fleet", "the rejoin")
	require.Contains(t, out, "MOVED m2 released, down until it beats", "the rejoin")
	got = rows()
	require.Equal(t, "3", got["m2"]["width"], "after m2 rejoined: %v", got)
	require.Contains(t, run(0, "fleet", "sync"), "nothing to do", "the sync after the rejoin")

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
