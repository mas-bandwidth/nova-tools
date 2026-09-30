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

// ctlStatus is the status the member's control card stores, not the view's
// derived one (the view shows up for any member with a fresh beat).
func (ta *testApp) ctlStatus(member string) string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	if err != nil {
		ta.t.Fatal(err)
	}
	snap, err := st.Load(context.Background(), []string{sprint.Fleet, sprint.Work}, nil)
	if err != nil {
		ta.t.Fatal(err)
	}
	return snap.MemberCtl(member).F("status")
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
	ta.ok("tick") // the fleet update, last in the tick, brings the members up
	ta.ok("tick") // the pump deals to them
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
	ta.ok("tick") // the fleet update, last in the tick, brings the members up
	ta.ok("tick") // the pump deals to them
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
	if len(rep.Drift) != 0 || len(rep.Moved) != 0 || rep.Members != 2 {
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
	if err := jsonInto(raw, &rep); err != nil || !rep.Check || len(rep.Drift) != 3 || len(rep.Moved) != 0 {
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
			case w.Member() && rows[w.Machine]["status"] == sprint.Held:
				t.Fatalf("round %d: %s is in the inventory with room and is still held", round, w.Machine)
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

// TestFleetSyncAddsANewMemberDownUntilItBeats: a member the fleet lacks is
// added down even with a fresh beat, and never up by the sync: presence
// brings it up at the tick (fleet_sync.go, the add).
func TestFleetSyncAddsANewMemberDownUntilItBeats(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	ta.ok("fleet sync") // every command beats m1 and m2 first: its beat is fresh
	if st := ta.ctlStatus("m1"); st != sprint.Down {
		t.Fatalf("a new member with a fresh beat is stored %q after the sync, want down until the tick", st)
	}
	ta.ok("start")
	ta.ok("tick")
	if st := ta.ctlStatus("m1"); st != sprint.Up {
		t.Fatalf("presence did not bring it up: %q", st)
	}
}

// TestFleetSyncRedealsToTheWidthsItSets: the cards of a member the sync holds
// go to the members that stay at the widths the same sync gives them (the
// receivers' room), not the widths the table held.
func TestFleetSyncRedealsToTheWidthsItSets(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	ta.live = []string{"m1", "m2", "m3"}
	inv.set("m1", 1)
	inv.set("m2", 4)
	inv.set("m3", 1)
	ta.ok("fleet sync")
	ta.ok("add --stream s1 --count 6")
	ta.ok("start")
	ta.ok("tick") // the fleet update, last in the tick, brings the members up
	ta.ok("tick") // the pump deals to them
	rows := ta.fleetRows()
	held := func(m string) int {
		a, _ := strconv.Atoi(rows[m]["ready"])
		b, _ := strconv.Atoi(rows[m]["working"])
		return a + b
	}
	if held("m1") != 1 || held("m2") != 4 || held("m3") != 1 {
		t.Fatalf("the deal: %v", rows)
	}
	inv.set("m1", 10)
	inv.remove("m2")
	ta.ok("fleet sync")
	rows = ta.fleetRows()
	if held("m1") != 5 || held("m3") != 1 || held("m2") != 0 {
		t.Fatalf("m2's four cards go to m1, which the sync widened to 10, and none to m3, full at 1: m1=%d m2=%d m3=%d", held("m1"), held("m2"), held("m3"))
	}
}

// TestFleetSyncIsAllOrNone: one member the fleet refuses refuses the whole
// step: the other members are not added either.
func TestFleetSyncIsAllOrNone(t *testing.T) {
	t.Parallel()
	ta, _ := syncApp(t)
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.Run(context.Background(), store.FleetStep(sprint.FleetReq{Op: "sync", Who: "tester",
		Sync: []sprint.SyncMember{{Name: "m1", Width: 4}, {Name: "m2", Width: sprint.MaxWidth + 1}}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused) == 0 || len(res.Moved) != 0 {
		t.Fatalf("a sync with a refused member: moved %v refused %v", res.Moved, res.Refused)
	}
	if rows := ta.fleetRows(); len(rows) != 0 {
		t.Fatalf("the valid member was written alone: %v", rows)
	}
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
	if st := ta.fleetRows()["m2"]["status"]; st != sprint.Held {
		t.Fatalf("m2 after it left: %q", st)
	}
	inv.set("m2", 6)
	code, out, _ := ta.do("fleet sync --check")
	if code != 2 || !strings.Contains(out, "DRIFT release m2 was held by the sync and is back in the inventory: release it") || !strings.Contains(out, "DRIFT width m2") {
		t.Fatalf("check: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "NOTE") {
		t.Fatalf("a hold the sync made is listed as the coordinator's:\n%s", out)
	}
	out = ta.ok("fleet sync")
	if !strings.Contains(out, "MOVED m2 released, down until it beats, width=6 (was 4)") || strings.Contains(out, "NOTE") {
		t.Fatalf("sync:\n%s", out)
	}
	ta.ok("tick")
	if st := ta.fleetRows()["m2"]["status"]; st != sprint.Up {
		t.Fatalf("m2 did not come up when it beat: %q", st)
	}
	if out := ta.ok("fleet sync"); !strings.Contains(out, "nothing to do") {
		t.Fatalf("after the release:\n%s", out)
	}
	// the coordinator's own hold, on the machine the sync once held, stays
	ta.ok("fleet down m2")
	inv.set("m2", 7)
	out = ta.ok("fleet sync")
	if !strings.Contains(out, "NOTE m2 is held by the coordinator") {
		t.Fatalf("the coordinator's hold is not said:\n%s", out)
	}
	if st := ta.fleetRows()["m2"]["status"]; st != sprint.Held {
		t.Fatalf("the sync released the coordinator's hold: %q", st)
	}
}

// TestFleetSyncCheckIsThreeWhenTheStoreIsMissing: under --check exit 2 is
// drift alone; a sprint store that cannot be reached is 3, and without
// --check it is the usage 2.
func TestFleetSyncCheckIsThreeWhenTheStoreIsMissing(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	code, _, errs := ta.do("fleet sync --check --redis=")
	if code != 3 || !strings.Contains(errs, "--redis <addr> is required") || strings.Count(errs, "nothing was changed") != 1 {
		t.Fatalf("--check with no store: exit %d %q", code, errs)
	}
	if code, _, _ := ta.do("fleet sync --redis="); code != 2 {
		t.Fatalf("sync with no store: exit %d, want the usage 2", code)
	}
}

// TestFleetSyncJSONIsOneShape: the same fields whether the verb checked, had
// nothing to write, or wrote.
func TestFleetSyncJSONIsOneShape(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	keys := func(raw string) string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
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
	if keys(check) != keys(wrote) || keys(wrote) != keys(nothing) {
		t.Fatalf("shapes differ:\n%s\n%s\n%s", check, wrote, nothing)
	}
	var rep syncReport
	if err := json.Unmarshal([]byte(wrote), &rep); err != nil || len(rep.Moved) != 1 || !strings.Contains(rep.Moved[0], "m1 added") {
		t.Fatalf("the write's report: %v %+v", err, rep)
	}
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
	if code != 2 || !strings.Contains(out, "DRIFT release m2") {
		t.Fatalf("after the clear: exit %d\n%s", code, out)
	}
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
	if err := json.Unmarshal([]byte(out), &raw); err != nil || code != 1 {
		t.Fatalf("exit %d %v\n%s", code, err, out)
	}
	var rep syncReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["refused"]; !ok || len(rep.Refused) != 1 || !strings.Contains(rep.Refused[0], "m2") {
		t.Fatalf("the refusal is not in the report:\n%s", out)
	}
	if len(rep.Moved) != 0 || len(ta.fleetRows()) != 0 {
		t.Fatalf("a refused sync wrote: %s", out)
	}
}

// TestFleetSyncCheckIsUsageWhenNoOneActs: a missing actor is a usage refusal,
// said once, never the store's 3.
func TestFleetSyncCheckIsUsageWhenNoOneActs(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	code, _, errs := ta.do("fleet sync --check --actor=")
	if code != 2 || !strings.Contains(errs, "--actor <name> is required") || strings.Count(errs, "nothing was changed") != 1 || strings.Contains(errs, "cannot be read") {
		t.Fatalf("exit %d %q", code, errs)
	}
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
	if code != 3 || !strings.Contains(errs, "the store did not answer") || strings.Count(errs, "nothing was changed") != 1 {
		t.Fatalf("--check: exit %d %q", code, errs)
	}
	if code, _, _ := ta.do("fleet sync"); code != 2 {
		t.Fatalf("sync: exit %d, want 2", code)
	}
	if ta.applies() != writes {
		t.Fatal("a sync that could not read wrote")
	}
}
