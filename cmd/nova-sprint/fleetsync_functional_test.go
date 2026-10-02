//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// TestFleetSyncFollowsTheInventoryOnTheStore: the inventory (the machine
// rows' width fields, read by config.Widths) becomes the fleet table at each
// machine's width, whatever friend rows and friends' beats the store holds; a
// change of the inventory is followed (a width, a machine with width 0, a
// machine gone); a sync after a sync writes nothing; and the tick's deal
// never takes a machine past DealAhead times the width the sync set.
func TestFleetSyncFollowsTheInventoryOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}

	cfg := config.NewMem()
	machine := func(name string, width int) {
		row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": "160", "runners": "0", "width": strconv.Itoa(width)}}
		if _, found, _ := cfg.Get(ctx, config.KindMachine, name); found {
			if _, _, err := cfg.Update(ctx, config.KindMachine, name, map[string]string{"width": strconv.Itoa(width)}, "t"); err != nil {
				t.Fatal(err)
			}
			return
		}
		if _, err := cfg.Insert(ctx, config.KindMachine, row, "t"); err != nil {
			t.Fatal(err)
		}
	}
	machine("m1", 5)
	machine("m2", 3)
	machine("m3", 2)
	// f1 runs on m1 by her beat and f2 has no beat: neither takes anything
	// off any width
	for f, slots := range map[string]string{"f1": "3", "f2": "1"} {
		if _, err := cfg.Insert(ctx, config.KindFriend, config.Row{Name: f, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}, "t"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := cfg.Update(ctx, config.KindFleet, config.KindFleet, map[string]string{"coordinator": "m2"}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, config.FriendBeatKey("f1"), "host", "m1").Err(); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	world := newApp(func(k string) string { return env[k] })
	defer world.close()
	world.inventory = func(ctx context.Context, _ string) ([]config.MachineWidth, error) {
		return config.Widths(ctx, cfg)
	}
	run := func(want int, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := world.run(args, &out, &errb); code != want {
			t.Fatalf("%v: exit %d, want %d\n%s%s", args, code, want, out.String(), errb.String())
		}
		return out.String()
	}
	rows := func() map[string]map[string]string {
		t.Helper()
		var w whereView
		if err := json.Unmarshal([]byte(run(0, "where", "--json")), &w); err != nil {
			t.Fatal(err)
		}
		return w.Tables["fleet"]
	}

	run(0, "init", "--readers", "reader-a,reader-b")
	out := run(0, "fleet", "sync")
	if !strings.Contains(out, "FLEET-SYNC OK moved=3") {
		t.Fatalf("the first sync:\n%s", out)
	}
	// each at the width its row carries
	got := rows()
	if got["m1"]["width"] != "5" || got["m2"]["width"] != "3" || got["m3"]["width"] != "2" {
		t.Fatalf("widths after the sync: m1=%s m2=%s m3=%s", got["m1"]["width"], got["m2"]["width"], got["m3"]["width"])
	}
	if out := run(0, "fleet", "sync"); !strings.Contains(out, "nothing to do") {
		t.Fatalf("the second sync:\n%s", out)
	}
	run(0, "fleet", "sync", "--check")

	// the inventory changes: m1 is wider, m3 has width 0, m2 is gone
	machine("m1", 6)
	machine("m3", 0)
	// m2 is the fleet's coordinator machine and cannot be removed while it is: move that
	if _, _, err := cfg.Update(ctx, config.KindFleet, config.KindFleet, map[string]string{"coordinator": "m1"}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Delete(ctx, config.KindMachine, "m2", "t"); err != nil {
		t.Fatal(err)
	}
	run(2, "fleet", "sync", "--check")
	out = run(0, "fleet", "sync")
	if !strings.Contains(out, "m3 held down") || !strings.Contains(out, "m2 held down") {
		t.Fatalf("the holds:\n%s", out)
	}
	got = rows()
	if got["m1"]["width"] != "6" || got["m2"]["status"] != "held" || got["m3"]["status"] != "held" {
		t.Fatalf("after the inventory changed: %v", got)
	}
	if out := run(0, "fleet", "sync"); !strings.Contains(out, "nothing to do") {
		t.Fatalf("the sync after it:\n%s", out)
	}

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
