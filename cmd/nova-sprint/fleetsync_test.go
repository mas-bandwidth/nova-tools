package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inventory is nova-config's in-memory store, and the fleet sync's reader
// over it: the real reader is config.Widths over Postgres.
type inventory struct {
	t    *testing.T
	m    *config.Mem
	fail error
}

func newInventory(t *testing.T) *inventory { return &inventory{t: t, m: config.NewMem()} }

// set makes a machine row with this width, or changes it. Its slots are a
// number unlike any width, so a sync that read them would be seen.
func (i *inventory) set(name string, width int) {
	i.t.Helper()
	ctx := context.Background()
	if _, found, _ := i.m.Get(ctx, config.KindMachine, name); found {
		_, _, err := i.m.Update(ctx, config.KindMachine, name, map[string]string{"width": fmt.Sprint(width)}, "t")
		require.NoError(i.t, err)
		return
	}
	row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": "160", "runners": "0", "width": fmt.Sprint(width)}}
	_, err := i.m.Insert(ctx, config.KindMachine, row, "t")
	require.NoError(i.t, err)
}

func (i *inventory) remove(name string) {
	i.t.Helper()
	_, err := i.m.Delete(context.Background(), config.KindMachine, name, "t")
	require.NoError(i.t, err)
}

func (i *inventory) fn() inventoryFn {
	return func(ctx context.Context, pg string) ([]config.MachineWidth, error) {
		if i.fail != nil {
			return nil, i.fail
		}
		return config.Widths(ctx, i.m)
	}
}

// syncApp is a test app with an initialised sprint and an inventory.
func syncApp(t *testing.T) (*testApp, *inventory) {
	t.Helper()
	ta := newTestApp(t)
	inv := newInventory(t)
	ta.a.inventory = inv.fn()
	ta.ok("init --readers reader-a,reader-b")
	return ta, inv
}

type fleetRow map[string]string

func (ta *testApp) fleetRows() map[string]fleetRow {
	ta.t.Helper()
	var w whereView
	ta.json("where", &w)
	out := map[string]fleetRow{}
	for name, row := range w.Tables["fleet"] {
		out[name] = fleetRow(row)
	}
	return out
}

// ctlStatus is the status the member's control card stores, not the view's
// derived one (the view shows up for any member with a fresh beat).
func (ta *testApp) ctlStatus(member string) string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	snap, err := st.Load(context.Background(), []string{sprint.Fleet, sprint.Work}, nil)
	require.NoError(ta.t, err)
	return snap.MemberCtl(member).F("status")
}

// dealIndex is the fleet table's rolling index, as the store holds it.
func (ta *testApp) dealIndex() string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	snap, err := st.Load(context.Background(), []string{sprint.Fleet, sprint.Work}, nil)
	require.NoError(ta.t, err)
	v, _ := snap.Fleet.Prop(sprint.PropDealIndex)
	return v
}

// TestFleetSyncBringsTheInventoryUpAtItsWidths: a member the inventory names
// and the fleet lacks is added at its width (the row's width field), down
// until it beats; the tick, as for fleet up, brings it up.
func TestFleetSyncBringsTheInventoryUpAtItsWidths(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 2)
	inv.set("m3", 0) // width 0: no member
	out := ta.ok("fleet sync")
	for _, want := range []string{"MOVED m1 added, down until it beats width=4", "MOVED m2 added, down until it beats width=2", "FLEET-SYNC OK moved=2"} {
		assert.Contains(t, out, want, "fleet sync lacks %q", want)
	}
	rows := ta.fleetRows()
	require.Len(t, rows, 2, "the fleet table: %v", rows)
	require.Equal(t, "4", rows["m1"]["width"], "the fleet table: %v", rows)
	require.Equal(t, "2", rows["m2"]["width"], "the fleet table: %v", rows)
	require.Nil(t, rows["m3"], "the fleet table: %v", rows)
	ta.ok("start")
	ta.ok("tick")
	rows = ta.fleetRows()
	require.Equal(t, sprint.Up, rows["m1"]["status"], "presence did not bring the synced members up: %v", rows)
	require.Equal(t, sprint.Up, rows["m2"]["status"], "presence did not bring the synced members up: %v", rows)
	ta.clean()
}

// TestFleetSyncTakesTheWidthAsSet: the width the row carries is the width the
// fleet table gets. The machine's slots are not it, and a friend row carrying
// slots takes nothing off it: no friend is charged to a machine, and no Redis
// is dialled to find where one runs.
func TestFleetSyncTakesTheWidthAsSet(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 32)
	inv.set("m2", 16)
	for _, f := range []string{"f1", "f2", "f3", "f4"} {
		_, err := inv.m.Insert(context.Background(), config.KindFriend, config.Row{Name: f, Fields: map[string]string{"slots": "32", "tiers": "flash", "roles": "builder"}}, "t")
		require.NoError(t, err)
	}
	_, _, err := inv.m.Update(context.Background(), config.KindFleet, config.KindFleet, map[string]string{"coordinator": "m2"}, "t")
	require.NoError(t, err)
	ta.ok("fleet sync")
	rows := ta.fleetRows()
	assert.Equal(t, "32", rows["m1"]["width"], "slots 160 and four friends of 32: %v", rows)
	assert.Equal(t, "16", rows["m2"]["width"], "slots 160 and the coordinator machine: %v", rows)
}

// TestFleetSyncSetsAChangedWidthAndNothingElse: a width that differs is set;
// the member's status, its cards and the deal's index are not touched.
func TestFleetSyncSetsAChangedWidthAndNothingElse(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("add --stream s1 --count 6")
	ta.ok("start")
	ta.ok("tick") // the fleet update, last in the tick, brings the members up
	ta.ok("tick") // the pump deals to them
	before, index := ta.fleetRows(), ta.dealIndex()
	inv.set("m1", 6)
	out := ta.ok("fleet sync")
	// the MOVED lines alone: the OK line's op id is random and can hold the letters m2
	var moved []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "MOVED ") {
			moved = append(moved, l)
		}
	}
	require.Len(t, moved, 1, "a width change moves that member alone:\n%s", out)
	require.Equal(t, "MOVED m1 width=6 (was 4)", moved[0], "a width change moves that member alone:\n%s", out)
	after := ta.fleetRows()
	require.Equal(t, "6", after["m1"]["width"], "widths: %v", after)
	require.Equal(t, "4", after["m2"]["width"], "widths: %v", after)
	for _, m := range []string{"m1", "m2"} {
		for _, col := range []string{"status", "ready", "working"} {
			assert.Equal(t, before[m][col], after[m][col], "%s %s changed", m, col)
		}
	}
	assert.Equal(t, index, ta.dealIndex(), "the deal's index moved")
	ta.clean()
}

// TestFleetSyncHoldsAMemberTheInventoryDropsAndRedealsItsCards: a machine
// gone from the inventory (its row removed, or its width 0) is held, never
// deleted, and its unfinished cards go to the members that stay.
func TestFleetSyncHoldsAMemberTheInventoryDropsAndRedealsItsCards(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 8)
	inv.set("m2", 8)
	ta.ok("fleet sync")
	ta.ok("add --stream s1 --count 8")
	ta.ok("start")
	ta.ok("tick") // the fleet update, last in the tick, brings the members up
	ta.ok("tick") // the pump deals to them
	rows := ta.fleetRows()
	require.False(t, rows["m2"]["ready"] == "0" && rows["m2"]["working"] == "0", "the test wants cards on m2: %v", rows)
	inv.remove("m2")
	out := ta.ok("fleet sync")
	require.Contains(t, out, "m2 held down", "m2 is not held")
	require.Contains(t, out, "FLEET-SYNC OK", "m2 is not held")
	rows = ta.fleetRows()
	require.Equal(t, sprint.Held, rows["m2"]["status"], "m2 after the sync: %v", rows["m2"])
	require.Equal(t, "0", rows["m2"]["ready"], "m2 still holds cards: %v", rows["m2"])
	require.Equal(t, "0", rows["m2"]["working"], "m2 still holds cards: %v", rows["m2"])
	require.Equal(t, sprint.Up, rows["m1"]["status"], "m1 is not up: %v", rows["m1"])
	// its width at 0 is the same as gone: a machine that returns at 0 stays held
	inv.set("m2", 0)
	require.Contains(t, ta.ok("fleet sync"), "nothing to do", "a row with width 0 is already held")
	ta.clean()
}

// TestFleetSyncTwiceWritesNothingTheSecondTime: a sync after a sync has no
// drift, writes nothing and says so; --check agrees in exit code and words.
func TestFleetSyncTwiceWritesNothingTheSecondTime(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	writes := ta.applies()
	out := ta.ok("fleet sync")
	require.Contains(t, out, "FLEET-SYNC OK moved=0 members=2: nothing to do, the fleet table already matches the inventory", "the second sync")
	require.Equal(t, writes, ta.applies(), "the second sync wrote")
	out = ta.ok("fleet sync --check")
	require.Contains(t, out, "FLEET-SYNC CHECK OK drift=0 members=2", "check after sync")
	var rep syncReport
	ta.json("fleet sync", &rep)
	require.Empty(t, rep.Drift, "--json of a sync with nothing to do: %+v", rep)
	require.Empty(t, rep.Moved, "--json of a sync with nothing to do: %+v", rep)
	require.Equal(t, 2, rep.Members, "--json of a sync with nothing to do: %+v", rep)
}

// TestFleetSyncCheckPrintsTheDriftExitsTwoAndWritesNothing.
func TestFleetSyncCheckPrintsTheDriftExitsTwoAndWritesNothing(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	inv.set("m1", 5)
	inv.set("m2", 0)
	inv.set("m3", 2)
	writes := ta.applies()
	code, out, errs := ta.do("fleet sync --check")
	require.Equal(t, 2, code, "check with drift: exit %d %q", code, errs)
	require.Empty(t, errs, "check with drift: exit %d %q", code, errs)
	for _, want := range []string{"DRIFT width m1 has width 4 and the inventory says 5", "DRIFT hold m2 is a member of the fleet and not of the inventory", "DRIFT add m3 is a member of the inventory and not of the fleet: add it at width 2", "FLEET-SYNC CHECK DRIFT drift=3 members=2"} {
		assert.Contains(t, out, want, "check lacks %q", want)
	}
	require.Equal(t, writes, ta.applies(), "--check wrote")
	var rep syncReport
	code, raw, _ := ta.do("fleet sync --check --json")
	require.Equal(t, 2, code, "--json check: exit %d", code)
	err := jsonInto(raw, &rep)
	require.NoError(t, err, "--json check: %v %+v", err, rep)
	require.True(t, rep.Check, "--json check: %v %+v", err, rep)
	require.Len(t, rep.Drift, 3, "--json check: %v %+v", err, rep)
	require.Empty(t, rep.Moved, "--json check: %v %+v", err, rep)
	// the sync writes exactly what the check printed, then the check is clean
	ta.ok("fleet sync")
	code, out, _ = ta.do("fleet sync --check")
	require.Equal(t, 0, code, "check after the sync: exit %d\n%s", code, out)
}

// TestFleetSyncExitsThreeWhenTheConfigCannotBeRead: an unreadable config and a
// config with no machine row at all change nothing.
func TestFleetSyncExitsThreeWhenTheConfigCannotBeRead(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	ta.ok("fleet sync")
	writes := ta.applies()
	inv.fail = errors.New("postgres at 127.0.0.1: connection refused")
	for _, line := range []string{"fleet sync", "fleet sync --check"} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 3, code, "%s with no config: exit %d %q", line, code, errs)
		assert.Contains(t, errs, "the config cannot be read: postgres at 127.0.0.1: connection refused", "%s with no config: exit %d %q", line, code, errs)
	}
	inv.fail = nil
	inv.remove("m1")
	for _, line := range []string{"fleet sync", "fleet sync --check"} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 3, code, "%s with an empty inventory: exit %d %q", line, code, errs)
		assert.Contains(t, errs, "holds no machine row", "%s with an empty inventory: exit %d %q", line, code, errs)
	}
	require.Equal(t, writes, ta.applies(), "a sync that could not read the config wrote")
	r := ta.fleetRows()
	require.NotEqual(t, sprint.Held, r["m1"]["status"], "an empty inventory held the fleet: %v", r)
}

// TestFleetSyncLeavesTheCoordinatorsHold: a member the inventory names that
// the coordinator holds down keeps its hold and gets its width; the sync says
// how to release it.
func TestFleetSyncLeavesTheCoordinatorsHold(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("fleet down m1")
	inv.set("m1", 6)
	out := ta.ok("fleet sync")
	require.Contains(t, out, "NOTE m1 is held by the coordinator and stays held; run: nova-sprint fleet up m1", "the hold is not said")
	rows := ta.fleetRows()
	require.Equal(t, sprint.Held, rows["m1"]["status"], "m1: %v", rows["m1"])
	require.Equal(t, "6", rows["m1"]["width"], "m1: %v", rows["m1"])
	out = ta.ok("fleet sync")
	require.Contains(t, out, "nothing to do", "the second sync")
	require.Contains(t, out, "NOTE m1", "the second sync")
}

// TestFleetSyncRefusesAWidthTheFleetCannotTake: a machine wider than the
// fleet's maximum refuses the whole sync before anything is written.
func TestFleetSyncRefusesAWidthTheFleetCannotTake(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", sprint.MaxWidth+1)
	writes := ta.applies()
	code, _, errs := ta.do("fleet sync")
	require.Equal(t, 1, code, "exit %d %q, writes %d -> %d", code, errs, writes, ta.applies())
	require.Contains(t, errs, "m2: a width wants a whole number from 1 to 1024", "exit %d %q, writes %d -> %d", code, errs, writes, ta.applies())
	require.Equal(t, writes, ta.applies(), "exit %d %q, writes %d -> %d", code, errs, writes, ta.applies())
	require.Empty(t, ta.fleetRows(), "a refused sync added a member")
}

// TestFleetSyncIsTheCoordinatorsAlone: another actor is refused, and
// nothing is read or written (the class test's line).
func TestFleetSyncIsTheCoordinatorsAlone(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	writes := ta.applies()
	code, _, errs := ta.do("fleet sync --actor intruder")
	require.Equal(t, 2, code, "exit %d %q", code, errs)
	require.Contains(t, errs, "the coordinator's alone: coordinator, not intruder", "exit %d %q", code, errs)
	require.Equal(t, writes, ta.applies(), "exit %d %q", code, errs)
}

// TestSyncAfterSyncIsSync is the property: over many random inventories (a
// machine added, dropped, widened, narrowed or given width 0), a sync leaves
// the fleet table matching the inventory (every member at its width, every
// other row held), a second sync writes nothing, and the check is clean.
func TestSyncAfterSyncIsSync(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(7, 11))
	ta, inv := syncApp(t)
	names := []string{"m1", "m2", "m3", "m4", "m5"}
	present := map[string]bool{}
	ta.ok("start")
	for round := 0; round < 40; round++ {
		for k := 0; k < 1+rng.IntN(3); k++ {
			n := names[rng.IntN(len(names))]
			switch {
			case present[n] && rng.IntN(4) == 0:
				inv.remove(n)
				delete(present, n)
			default:
				inv.set(n, rng.IntN(9))
				present[n] = true
			}
		}
		if len(present) == 0 {
			inv.set("m1", 3)
			present["m1"] = true
		}
		if round%5 == 0 {
			ta.ok("add --stream s1 --count 5")
		}
		ta.ok("fleet sync")
		ws, _ := inv.fn()(context.Background(), "")
		rows := ta.fleetRows()
		for _, w := range ws {
			switch {
			case w.Member() && rows[w.Machine]["width"] != fmt.Sprint(w.Width):
				t.Fatalf("round %d: %s is at width %q, the inventory says %d", round, w.Machine, rows[w.Machine]["width"], w.Width)
			case w.Member() && rows[w.Machine]["status"] == sprint.Held:
				t.Fatalf("round %d: %s is in the inventory with room and is still held", round, w.Machine)
			case !w.Member() && rows[w.Machine] != nil && rows[w.Machine]["status"] != sprint.Held:
				t.Fatalf("round %d: %s has no room and is %q, not held", round, w.Machine, rows[w.Machine]["status"])
			}
		}
		writes := ta.applies()
		out := ta.ok("fleet sync")
		require.Contains(t, out, "nothing to do", "round %d: the second sync wrote:\n%s", round, out)
		require.Equal(t, writes, ta.applies(), "round %d: the second sync wrote:\n%s", round, out)
		code, out, _ := ta.do("fleet sync --check")
		require.Equal(t, 0, code, "round %d: check after sync: exit %d\n%s", round, code, out)
		ta.ok("tick")
	}
	ta.clean()
}

func jsonInto(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }

// TestFleetSyncAddsANewMemberDownUntilItBeats: a member the fleet lacks is
// added down even with a fresh beat, and never up by the sync: presence
// brings it up at the tick (fleet_sync.go, the add).
func TestFleetSyncAddsANewMemberDownUntilItBeats(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	ta.ok("fleet sync") // every command beats m1 and m2 first: its beat is fresh
	require.Equal(t, sprint.Down, ta.ctlStatus("m1"), "a new member with a fresh beat is stored down after the sync, until the tick")
	ta.ok("start")
	ta.ok("tick")
	require.Equal(t, sprint.Up, ta.ctlStatus("m1"), "presence did not bring it up")
}

// TestFleetSyncRedealsToTheWidthsItSets: the cards of a member the sync holds
// go to the members that stay at the widths the same sync gives them (the
// receivers' room, DealAhead times the width), not the widths the table held.
func TestFleetSyncRedealsToTheWidthsItSets(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	ta.live = []string{"m1", "m2", "m3"}
	inv.set("m1", 1)
	inv.set("m2", 4)
	inv.set("m3", 1)
	ta.ok("fleet sync")
	ta.ok("add --stream s1 --count 12")
	ta.ok("start")
	ta.ok("tick") // the fleet update, last in the tick, brings the members up
	ta.ok("tick") // the pump deals to them
	rows := ta.fleetRows()
	held := func(m string) int {
		a, _ := strconv.Atoi(rows[m]["ready"])
		b, _ := strconv.Atoi(rows[m]["working"])
		return a + b
	}
	require.Equal(t, sprint.DealAhead*1, held("m1"), "the deal: %v", rows)
	require.Equal(t, sprint.DealAhead*4, held("m2"), "the deal: %v", rows)
	require.Equal(t, sprint.DealAhead*1, held("m3"), "the deal: %v", rows)
	inv.set("m1", 10)
	inv.remove("m2")
	ta.ok("fleet sync")
	rows = ta.fleetRows()
	require.Equal(t, sprint.DealAhead*1+sprint.DealAhead*4, held("m1"), "m2's eight cards go to m1, which the sync widened to 10, and none to m3, full at DealAhead times 1: m1=%d m2=%d m3=%d", held("m1"), held("m2"), held("m3"))
	require.Equal(t, sprint.DealAhead*1, held("m3"), "m2's eight cards go to m1, which the sync widened to 10, and none to m3, full at DealAhead times 1: m1=%d m2=%d m3=%d", held("m1"), held("m2"), held("m3"))
	require.Equal(t, 0, held("m2"), "m2's eight cards go to m1, which the sync widened to 10, and none to m3, full at DealAhead times 1: m1=%d m2=%d m3=%d", held("m1"), held("m2"), held("m3"))
}

// TestFleetSyncIsAllOrNone: one member the fleet refuses refuses the whole
// step: the other members are not added either.
func TestFleetSyncIsAllOrNone(t *testing.T) {
	t.Parallel()
	ta, _ := syncApp(t)
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	res, err := st.Run(context.Background(), store.FleetStep(sprint.FleetReq{Op: "sync", Who: "tester",
		Sync: []sprint.SyncMember{{Name: "m1", Width: 4}, {Name: "m2", Width: sprint.MaxWidth + 1}}}))
	require.NoError(t, err)
	require.NotEmpty(t, res.Refused, "a sync with a refused member: moved %v refused %v", res.Moved, res.Refused)
	require.Empty(t, res.Moved, "a sync with a refused member: moved %v refused %v", res.Moved, res.Refused)
	rows := ta.fleetRows()
	require.Empty(t, rows, "the valid member was written alone: %v", rows)
}

// TestFleetSyncReleasesItsOwnHoldAndNotTheCoordinators: a machine the sync
// held that is back in the inventory with room is released (the check calls
// it drift), and comes up when it beats; a hold the coordinator made stays,
// even on a machine the sync once held.
func TestFleetSyncReleasesItsOwnHoldAndNotTheCoordinators(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("start")
	ta.ok("tick")
	inv.set("m2", 0)
	ta.ok("fleet sync")
	require.Equal(t, sprint.Held, ta.fleetRows()["m2"]["status"], "m2 after it left")
	inv.set("m2", 6)
	code, out, _ := ta.do("fleet sync --check")
	require.Equal(t, 2, code, "check: exit %d\n%s", code, out)
	require.Contains(t, out, "DRIFT release m2 was held by the sync and is back in the inventory: release it", "check: exit %d\n%s", code, out)
	require.Contains(t, out, "DRIFT width m2", "check: exit %d\n%s", code, out)
	require.NotContains(t, out, "NOTE", "a hold the sync made is listed as the coordinator's")
	out = ta.ok("fleet sync")
	require.Contains(t, out, "MOVED m2 released, down until it beats, width=6 (was 4)", "sync")
	require.NotContains(t, out, "NOTE", "sync")
	ta.ok("tick")
	require.Equal(t, sprint.Up, ta.fleetRows()["m2"]["status"], "m2 did not come up when it beat")
	require.Contains(t, ta.ok("fleet sync"), "nothing to do", "after the release")
	// the coordinator's own hold, on the machine the sync once held, stays
	ta.ok("fleet down m2")
	inv.set("m2", 7)
	out = ta.ok("fleet sync")
	require.Contains(t, out, "NOTE m2 is held by the coordinator", "the coordinator's hold is not said")
	require.Equal(t, sprint.Held, ta.fleetRows()["m2"]["status"], "the sync released the coordinator's hold")
}

// TestFleetSyncCheckIsThreeWhenTheStoreIsMissing: under --check exit 2 is
// drift alone; a sprint store that cannot be reached is 3, and without
// --check it is the usage 2.
func TestFleetSyncCheckIsThreeWhenTheStoreIsMissing(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	code, _, errs := ta.do("fleet sync --check --redis=")
	require.Equal(t, 3, code, "--check with no store: exit %d %q", code, errs)
	require.Contains(t, errs, "--redis <addr> is required", "--check with no store: exit %d %q", code, errs)
	require.Equal(t, 1, strings.Count(errs, "nothing was changed"), "--check with no store: exit %d %q", code, errs)
	code, _, _ = ta.do("fleet sync --redis=")
	require.Equal(t, 2, code, "sync with no store: exit %d, want the usage 2", code)
}

// TestFleetSyncJSONIsOneShape: the same fields whether the verb checked, had
// nothing to write, or wrote.
func TestFleetSyncJSONIsOneShape(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	keys := func(raw string) string {
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(raw), &m), "%s", raw)
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	_, check, _ := ta.do("fleet sync --check --json")
	_, wrote, _ := ta.do("fleet sync --json")
	_, nothing, _ := ta.do("fleet sync --json")
	require.Equal(t, keys(check), keys(wrote), "shapes differ:\n%s\n%s\n%s", check, wrote, nothing)
	require.Equal(t, keys(wrote), keys(nothing), "shapes differ:\n%s\n%s\n%s", check, wrote, nothing)
	var rep syncReport
	err := json.Unmarshal([]byte(wrote), &rep)
	require.NoError(t, err, "the write's report: %v %+v", err, rep)
	require.Len(t, rep.Moved, 1, "the write's report: %v %+v", err, rep)
	require.Contains(t, rep.Moved[0], "m1 added", "the write's report: %v %+v", err, rep)
}

// TestAClearKeepsWhoMadeAHold: the sync's mark on its hold crosses a clear, so
// the sync still releases its own hold afterwards and never the coordinator's.
func TestAClearKeepsWhoMadeAHold(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	inv.set("m2", 0)
	ta.ok("fleet sync")
	ta.ok("clear --confirm sprint")
	inv.set("m2", 4)
	code, out, _ := ta.do("fleet sync --check")
	require.Equal(t, 2, code, "after the clear: exit %d\n%s", code, out)
	require.Contains(t, out, "DRIFT release m2", "after the clear: exit %d\n%s", code, out)
}

// TestFleetSyncJSONWithARefusalKeepsTheRefusedKey: a machine the fleet
// refuses is named in refused, in the same shape.
func TestFleetSyncJSONWithARefusalKeepsTheRefusedKey(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", sprint.MaxWidth+1)
	code, out, _ := ta.do("fleet sync --json")
	var raw map[string]json.RawMessage
	err := json.Unmarshal([]byte(out), &raw)
	require.NoError(t, err, "exit %d %v\n%s", code, err, out)
	require.Equal(t, 1, code, "exit %d %v\n%s", code, err, out)
	var rep syncReport
	require.NoError(t, json.Unmarshal([]byte(out), &rep))
	_, ok := raw["refused"]
	require.True(t, ok, "the refusal is not in the report:\n%s", out)
	require.Len(t, rep.Refused, 1, "the refusal is not in the report:\n%s", out)
	require.Contains(t, rep.Refused[0], "m2", "the refusal is not in the report:\n%s", out)
	require.Empty(t, rep.Moved, "a refused sync wrote: %s", out)
	require.Empty(t, ta.fleetRows(), "a refused sync wrote: %s", out)
}

// TestFleetSyncCheckIsUsageWhenNoOneActs: a missing actor is a usage refusal,
// said once, never the store's 3.
func TestFleetSyncCheckIsUsageWhenNoOneActs(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	code, _, errs := ta.do("fleet sync --check --actor=")
	require.Equal(t, 2, code, "exit %d %q", code, errs)
	require.Contains(t, errs, "--actor <name> is required", "exit %d %q", code, errs)
	require.Equal(t, 1, strings.Count(errs, "nothing was changed"), "exit %d %q", code, errs)
	require.NotContains(t, errs, "cannot be read", "exit %d %q", code, errs)
}

// shapesDown is a backend whose reads of the tables fail while down is set.
type shapesDown struct {
	store.Backend
	down *atomic.Bool
}

func (b shapesDown) Shapes(ctx context.Context, tables []string) ([]ntable.Table, error) {
	if b.down.Load() {
		return nil, errors.New("the store did not answer")
	}
	return b.Backend.Shapes(ctx, tables)
}

// TestFleetSyncCheckIsThreeWhenTheTablesCannotBeRead: the read of the fleet
// table under --check is 3, where a sync's is 2; nothing is written.
func TestFleetSyncCheckIsThreeWhenTheTablesCannotBeRead(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	var down atomic.Bool
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) {
		return shapesDown{Backend: ta.m, down: &down}, nil
	}
	ta.ok("fleet sync")
	down.Store(true)
	writes := ta.applies()
	code, _, errs := ta.do("fleet sync --check")
	require.Equal(t, 3, code, "--check: exit %d %q", code, errs)
	require.Contains(t, errs, "the store did not answer", "--check: exit %d %q", code, errs)
	require.Equal(t, 1, strings.Count(errs, "nothing was changed"), "--check: exit %d %q", code, errs)
	code, _, _ = ta.do("fleet sync")
	require.Equal(t, 2, code, "sync: exit %d, want 2", code)
	require.Equal(t, writes, ta.applies(), "a sync that could not read wrote")
}
