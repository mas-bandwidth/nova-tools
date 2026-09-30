package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// inventory is nova-config's in-memory store, and the fleet sync's reader
// over it: the real reader is config.Widths over Postgres.
type inventory struct {
	t    *testing.T
	m    *config.Mem
	fail error
}

func newInventory(t *testing.T) *inventory { return &inventory{t: t, m: config.NewMem()} }

// set makes a machine row with these slots, or changes it.
func (i *inventory) set(name string, slots int) {
	i.t.Helper()
	ctx := context.Background()
	if _, found, _ := i.m.Get(ctx, config.KindMachine, name); found {
		if _, _, err := i.m.Update(ctx, config.KindMachine, name, map[string]string{"slots": fmt.Sprint(slots)}, "t"); err != nil {
			i.t.Fatal(err)
		}
		return
	}
	row := config.Row{Name: name, Fields: map[string]string{"user": "u", "seat": "s", "slots": fmt.Sprint(slots), "runners": "0"}}
	if _, err := i.m.Insert(ctx, config.KindMachine, row, "t"); err != nil {
		i.t.Fatal(err)
	}
}

func (i *inventory) remove(name string) {
	i.t.Helper()
	if _, err := i.m.Delete(context.Background(), config.KindMachine, name, "t"); err != nil {
		i.t.Fatal(err)
	}
}

func (i *inventory) fn() inventoryFn {
	return func(ctx context.Context, pg, redisAddr string) ([]config.MachineWidth, error) {
		if i.fail != nil {
			return nil, i.fail
		}
		return config.Widths(ctx, i.m, nil)
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

// dealIndex is the fleet table's rolling index, as the store holds it.
func (ta *testApp) dealIndex() string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	if err != nil {
		ta.t.Fatal(err)
	}
	snap, err := st.Load(context.Background(), []string{sprint.Fleet, sprint.Work}, nil)
	if err != nil {
		ta.t.Fatal(err)
	}
	v, _ := snap.Fleet.Prop(sprint.PropDealIndex)
	return v
}

// TestFleetSyncBringsTheInventoryUpAtItsWidths: a member the inventory names
// and the fleet lacks is added at its width (slots less the friends'), down
// until it beats; the tick, as for fleet up, brings it up.
func TestFleetSyncBringsTheInventoryUpAtItsWidths(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 2)
	inv.set("m3", 0) // no slots: no member
	out := ta.ok("fleet sync")
	for _, want := range []string{"MOVED m1 added, down until it beats width=4", "MOVED m2 added, down until it beats width=2", "FLEET-SYNC OK moved=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("fleet sync lacks %q:\n%s", want, out)
		}
	}
	rows := ta.fleetRows()
	if len(rows) != 2 || rows["m1"]["width"] != "4" || rows["m2"]["width"] != "2" || rows["m3"] != nil {
		t.Fatalf("the fleet table: %v", rows)
	}
	ta.ok("start")
	ta.ok("tick")
	rows = ta.fleetRows()
	if rows["m1"]["status"] != sprint.Up || rows["m2"]["status"] != sprint.Up {
		t.Fatalf("presence did not bring the synced members up: %v", rows)
	}
	ta.clean()
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
	ta.ok("tick")
	before, index := ta.fleetRows(), ta.dealIndex()
	inv.set("m1", 6)
	out := ta.ok("fleet sync")
	if !strings.Contains(out, "MOVED m1 width=6 (was 4)") || strings.Contains(out, "m2") {
		t.Fatalf("a width change moves that member alone:\n%s", out)
	}
	after := ta.fleetRows()
	if after["m1"]["width"] != "6" || after["m2"]["width"] != "4" {
		t.Fatalf("widths: %v", after)
	}
	for _, m := range []string{"m1", "m2"} {
		for _, col := range []string{"status", "ready", "working"} {
			if before[m][col] != after[m][col] {
				t.Errorf("%s %s changed from %q to %q", m, col, before[m][col], after[m][col])
			}
		}
	}
	if got := ta.dealIndex(); got != index {
		t.Errorf("the deal's index moved from %q to %q", index, got)
	}
	ta.clean()
}

// TestFleetSyncHoldsAMemberTheInventoryDropsAndRedealsItsCards: a machine
// gone from the inventory (its row removed, or its slots 0) is held, never
// deleted, and its unfinished cards go to the members that stay.
func TestFleetSyncHoldsAMemberTheInventoryDropsAndRedealsItsCards(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 8)
	inv.set("m2", 8)
	ta.ok("fleet sync")
	ta.ok("add --stream s1 --count 8")
	ta.ok("start")
	ta.ok("tick")
	rows := ta.fleetRows()
	if rows["m2"]["ready"] == "0" && rows["m2"]["working"] == "0" {
		t.Fatalf("the test wants cards on m2: %v", rows)
	}
	inv.remove("m2")
	out := ta.ok("fleet sync")
	if !strings.Contains(out, "m2 held down") || !strings.Contains(out, "FLEET-SYNC OK") {
		t.Fatalf("m2 is not held:\n%s", out)
	}
	rows = ta.fleetRows()
	if rows["m2"]["status"] != sprint.Held {
		t.Fatalf("m2 after the sync: %v", rows["m2"])
	}
	if rows["m2"]["ready"] != "0" || rows["m2"]["working"] != "0" {
		t.Fatalf("m2 still holds cards: %v", rows["m2"])
	}
	if rows["m1"]["status"] != sprint.Up {
		t.Fatalf("m1 is not up: %v", rows["m1"])
	}
	// its slots at 0 is the same as gone: a machine that returns at 0 stays held
	inv.set("m2", 0)
	if out := ta.ok("fleet sync"); !strings.Contains(out, "nothing to do") {
		t.Fatalf("a row with no slots is already held:\n%s", out)
	}
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
	if !strings.Contains(out, "FLEET-SYNC OK moved=0 members=2: nothing to do, the fleet table already matches the inventory") {
		t.Fatalf("the second sync:\n%s", out)
	}
	if ta.applies() != writes {
		t.Fatalf("the second sync wrote: %d -> %d", writes, ta.applies())
	}
	out = ta.ok("fleet sync --check")
	if !strings.Contains(out, "FLEET-SYNC CHECK OK drift=0 members=2") {
		t.Fatalf("check after sync:\n%s", out)
	}
	var rep syncReport
	ta.json("fleet sync", &rep)
	if rep.Changed || len(rep.Drift) != 0 || rep.Members != 2 {
		t.Fatalf("--json of a sync with nothing to do: %+v", rep)
	}
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
	if code != 2 || errs != "" {
		t.Fatalf("check with drift: exit %d %q", code, errs)
	}
	for _, want := range []string{"DRIFT width m1 has width 4 and the inventory says 5", "DRIFT hold m2 is a member of the fleet and not of the inventory", "DRIFT add m3 is a member of the inventory and not of the fleet: add it at width 2", "FLEET-SYNC CHECK DRIFT drift=3 members=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("check lacks %q:\n%s", want, out)
		}
	}
	if ta.applies() != writes {
		t.Fatal("--check wrote")
	}
	var rep syncReport
	code, raw, _ := ta.do("fleet sync --check --json")
	if code != 2 {
		t.Fatalf("--json check: exit %d", code)
	}
	if err := jsonInto(raw, &rep); err != nil || !rep.Check || len(rep.Drift) != 3 || rep.Changed {
		t.Fatalf("--json check: %v %+v", err, rep)
	}
	// the sync writes exactly what the check printed, then the check is clean
	ta.ok("fleet sync")
	if code, out, _ := ta.do("fleet sync --check"); code != 0 {
		t.Fatalf("check after the sync: exit %d\n%s", code, out)
	}
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
		if code != 3 || !strings.Contains(errs, "the config cannot be read: postgres at 127.0.0.1: connection refused") {
			t.Errorf("%s with no config: exit %d %q", line, code, errs)
		}
	}
	inv.fail = nil
	inv.remove("m1")
	for _, line := range []string{"fleet sync", "fleet sync --check"} {
		code, _, errs := ta.do(line)
		if code != 3 || !strings.Contains(errs, "holds no machine row") {
			t.Errorf("%s with an empty inventory: exit %d %q", line, code, errs)
		}
	}
	if ta.applies() != writes {
		t.Fatal("a sync that could not read the config wrote")
	}
	if r := ta.fleetRows(); r["m1"]["status"] == sprint.Held {
		t.Fatalf("an empty inventory held the fleet: %v", r)
	}
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
	if !strings.Contains(out, "NOTE m1 is held by the coordinator and stays held; run: nova-sprint fleet up m1") {
		t.Fatalf("the hold is not said:\n%s", out)
	}
	rows := ta.fleetRows()
	if rows["m1"]["status"] != sprint.Held || rows["m1"]["width"] != "6" {
		t.Fatalf("m1: %v", rows["m1"])
	}
	if out := ta.ok("fleet sync"); !strings.Contains(out, "nothing to do") || !strings.Contains(out, "NOTE m1") {
		t.Fatalf("the second sync:\n%s", out)
	}
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
	if code != 1 || !strings.Contains(errs, "m2: a width wants a whole number from 1 to 1024") || ta.applies() != writes {
		t.Fatalf("exit %d %q, writes %d -> %d", code, errs, writes, ta.applies())
	}
	if len(ta.fleetRows()) != 0 {
		t.Fatal("a refused sync added a member")
	}
}

// TestFleetSyncIsTheCoordinatorsAlone: another actor is refused, and
// nothing is read or written (the class test's line).
func TestFleetSyncIsTheCoordinatorsAlone(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	writes := ta.applies()
	code, _, errs := ta.do("fleet sync --actor intruder")
	if code != 2 || !strings.Contains(errs, "the coordinator's alone: coordinator, not intruder") || ta.applies() != writes {
		t.Fatalf("exit %d %q", code, errs)
	}
}

// TestSyncAfterSyncIsSync is the property: over many random inventories (a
// machine added, dropped, widened, narrowed or given no slots), a sync leaves
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
		ws, _ := inv.fn()(context.Background(), "", "")
		rows := ta.fleetRows()
		for _, w := range ws {
			switch {
			case w.Member() && rows[w.Machine]["width"] != fmt.Sprint(w.Width):
				t.Fatalf("round %d: %s is at width %q, the inventory says %d", round, w.Machine, rows[w.Machine]["width"], w.Width)
			case !w.Member() && rows[w.Machine] != nil && rows[w.Machine]["status"] != sprint.Held:
				t.Fatalf("round %d: %s has no room and is %q, not held", round, w.Machine, rows[w.Machine]["status"])
			}
		}
		writes := ta.applies()
		if out := ta.ok("fleet sync"); !strings.Contains(out, "nothing to do") || ta.applies() != writes {
			t.Fatalf("round %d: the second sync wrote:\n%s", round, out)
		}
		if code, out, _ := ta.do("fleet sync --check"); code != 0 {
			t.Fatalf("round %d: check after sync: exit %d\n%s", round, code, out)
		}
		ta.ok("tick")
	}
	ta.clean()
}

func jsonInto(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }
