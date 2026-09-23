package life

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// idleWorld is a fixture fleet: queues of open task titles per friend, beat
// presence, live children, wake-unit state, and how a friend answers each
// escalation. Every IdleActions call is recorded.
type idleWorld struct {
	coordinator string
	queues      map[string][]string
	present     map[string]bool
	working     map[string]int
	slots       map[string]int
	unitLoaded  map[string]bool
	keeper      map[string]string

	takeOnNudge  map[string]bool
	takeOnWake   map[string]bool
	upOnRepair   map[string]bool
	calls        []string
	coordCalls   int
	redistTarget []string

	// failNudge/failWake: the next call for that friend returns an
	// injected error and clears its own flag, so a retry succeeds. Used
	// to regress the failure-path resume defect (stella's HOLD on #3058).
	failNudge map[string]bool
	failWake  map[string]bool

	// lastIdem records the idem key passed to the most recent Nudge/Wake
	// call, keyed "nudge <friend>" / "wake <friend>", win or fail. Used to
	// regress stella's idempotency-key HOLD (score 7): a retry of the same
	// rung must reuse the key, a new episode must not.
	lastIdem map[string]string
}

func newIdleWorld() *idleWorld {
	return &idleWorld{
		coordinator: "rowan",
		queues:      map[string][]string{},
		present:     map[string]bool{},
		working:     map[string]int{},
		slots:       map[string]int{},
		unitLoaded:  map[string]bool{},
		keeper:      map[string]string{},
		takeOnNudge: map[string]bool{},
		takeOnWake:  map[string]bool{},
		upOnRepair:  map[string]bool{},
		failNudge:   map[string]bool{},
		failWake:    map[string]bool{},
		lastIdem:    map[string]string{},
	}
}

func (w *idleWorld) addFriend(name string, open int) {
	w.present[name] = true
	w.unitLoaded[name] = true
	for i := 0; i < open; i++ {
		w.queues[name] = append(w.queues[name], fmt.Sprintf("build-%s-%d", name, i))
	}
}

func (w *idleWorld) observe() []IdleObservation {
	var out []IdleObservation
	for f := range w.present {
		out = append(out, IdleObservation{
			Friend: f, Present: w.present[f], Working: w.working[f], Slots: w.slots[f],
			Open: len(w.queues[f]), KeeperState: w.keeper[f],
		})
	}
	return out
}

func (w *idleWorld) take(f string) {
	if len(w.queues[f]) == 0 {
		return
	}
	w.queues[f] = w.queues[f][1:]
	w.working[f]++
}

func (w *idleWorld) touch(f string) {
	if f == w.coordinator {
		w.coordCalls++
	}
}

func (w *idleWorld) Nudge(_ context.Context, f, _, idem string) error {
	w.touch(f)
	w.lastIdem["nudge "+f] = idem
	if w.failNudge[f] {
		w.failNudge[f] = false
		w.calls = append(w.calls, "nudge-error "+f)
		return fmt.Errorf("nudge %s: injected failure", f)
	}
	w.calls = append(w.calls, "nudge "+f)
	if w.takeOnNudge[f] && w.present[f] {
		w.take(f)
	}
	return nil
}

func (w *idleWorld) Wake(_ context.Context, f, _, idem string) error {
	w.touch(f)
	w.lastIdem["wake "+f] = idem
	if w.failWake[f] {
		w.failWake[f] = false
		w.calls = append(w.calls, "wake-error "+f)
		return fmt.Errorf("wake %s: injected failure", f)
	}
	w.calls = append(w.calls, "wake "+f)
	if w.takeOnWake[f] && w.present[f] {
		w.take(f)
	}
	return nil
}

func (w *idleWorld) RepairWake(_ context.Context, f string) (bool, error) {
	w.touch(f)
	if w.unitLoaded[f] {
		return false, nil
	}
	w.unitLoaded[f] = true
	w.calls = append(w.calls, "repair "+f)
	if w.upOnRepair[f] {
		w.present[f] = true
	}
	return true, nil
}

// Redistribute stands in for #3047: every open task moves to the first
// present non-coordinator friend other than f, title carrying the reason.
func (w *idleWorld) Redistribute(_ context.Context, f, reason string) (int, error) {
	w.touch(f)
	w.calls = append(w.calls, "redistribute "+f)
	target := ""
	for _, t := range w.redistTarget {
		if t != f && t != w.coordinator && w.present[t] {
			target = t
			break
		}
	}
	if target == "" {
		return 0, nil
	}
	moved := 0
	for _, title := range w.queues[f] {
		w.queues[target] = append(w.queues[target], fmt.Sprintf("%s [moved from %s: %s]", title, f, reason))
		moved++
	}
	w.queues[f] = nil
	return moved, nil
}

func (w *idleWorld) count(prefix string) int {
	n := 0
	for _, c := range w.calls {
		if c == prefix {
			n++
		}
	}
	return n
}

func rowFor(t *testing.T, rows []IdleRow, f string) IdleRow {
	t.Helper()
	for _, r := range rows {
		if r.Friend == f {
			return r
		}
	}
	t.Fatalf("no row for %s in %+v", f, rows)
	return IdleRow{}
}

func redisIdleLedger(t *testing.T) (RedisIdleLedger, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return RedisIdleLedger{Store: store.New(client)}, mr
}

func assertNoAsleep(t *testing.T, mr *miniredis.Miniredis, rows [][]IdleRow) {
	t.Helper()
	for _, key := range mr.Keys() {
		if strings.Contains(key, "asleep") {
			t.Fatalf("key %q says asleep", key)
		}
		if mr.Type(key) == "hash" {
			fields, err := mr.HKeys(key)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range fields {
				if v := mr.HGet(key, field); strings.Contains(v, "asleep") {
					t.Fatalf("%s %s = %q says asleep", key, field, v)
				}
			}
		}
	}
	for _, tick := range rows {
		for _, r := range tick {
			if strings.Contains(r.State, "asleep") || strings.Contains(r.Action, "asleep") {
				t.Fatalf("row says asleep: %+v", r)
			}
		}
	}
}

func TestIdleEscalatesNudgeWakeRedistribute(t *testing.T) {
	ctx := context.Background()
	// idle_ticks from a measured take latency: p95 2.4 s at a 1 s tick = 3.
	idleTicks, err := IdleTicksFromTakeLatency([]time.Duration{
		400 * time.Millisecond, 900 * time.Millisecond, 1100 * time.Millisecond,
		1500 * time.Millisecond, 1700 * time.Millisecond, 2 * time.Second,
		2100 * time.Millisecond, 2200 * time.Millisecond, 2300 * time.Millisecond,
		2400 * time.Millisecond,
	}, time.Second)
	if err != nil || idleTicks != 3 {
		t.Fatalf("idle_ticks = %d, %v; want 3 from the measured p95", idleTicks, err)
	}
	if _, err := IdleTicksFromTakeLatency(nil, time.Second); err == nil {
		t.Fatal("idle_ticks with no samples must refuse, never guess")
	}

	w := newIdleWorld()
	w.addFriend("johnny", 34) // never answers
	w.addFriend("tess", 4)    // takes when woken directly at ladder tick 2
	w.addFriend("nia", 4)     // takes at the nudge
	w.addFriend("stella", 0)  // has width, receives moved work
	w.addFriend("rowan", 0)   // the coordinator: never called
	w.takeOnWake["tess"] = true
	w.takeOnNudge["nia"] = true
	w.redistTarget = []string{"stella"}

	ledger, mr := redisIdleLedger(t)
	ladder := &IdleLadder{Policy: IdlePolicy{IdleTicks: idleTicks}, Ledger: ledger, Actions: w}

	var all [][]IdleRow
	tick := func() []IdleRow {
		rows, err := ladder.Tick(ctx, w.observe())
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, rows)
		return rows
	}

	// Within idle_ticks: no state idle and no action.
	for i := 1; i <= idleTicks; i++ {
		rows := tick()
		if r := rowFor(t, rows, "johnny"); r.State != IdleStateUp || r.Action != "" {
			t.Fatalf("tick %d within idle_ticks: johnny %+v", i, r)
		}
	}
	if len(w.calls) != 0 {
		t.Fatalf("actions before idle_ticks elapsed: %v", w.calls)
	}

	// Ladder tick 1: idle, one bus nudge each.
	rows := tick()
	if r := rowFor(t, rows, "johnny"); r.State != IdleStateIdle || r.Action != IdleActionNudge || r.Step != 1 {
		t.Fatalf("ladder tick 1: johnny %+v", r)
	}
	if w.count("nudge johnny") != 1 || w.count("wake johnny") != 0 || w.count("redistribute johnny") != 0 {
		t.Fatalf("ladder tick 1 calls: %v", w.calls)
	}
	if got := mr.HGet(IdleKey("johnny"), "state"); got != IdleStateIdle {
		t.Fatalf("friend:johnny:idle state = %q, want idle for the table", got)
	}

	// Ladder tick 2: one direct wake; nia took at the nudge and is working.
	rows = tick()
	if r := rowFor(t, rows, "johnny"); r.State != IdleStateIdle || r.Action != IdleActionWake || r.Step != 2 {
		t.Fatalf("ladder tick 2: johnny %+v", r)
	}
	if r := rowFor(t, rows, "nia"); r.State != IdleStateWorking {
		t.Fatalf("nia took at the nudge, row %+v", r)
	}
	if w.count("nudge johnny") != 1 || w.count("wake johnny") != 1 || w.count("redistribute johnny") != 0 {
		t.Fatalf("ladder tick 2 calls: %v", w.calls)
	}

	// Ladder tick 3: johnny is redistributed; tess took at tick 2's wake and
	// is never redistributed.
	rows = tick()
	if r := rowFor(t, rows, "johnny"); r.Action != IdleActionRedistribute || r.Step != 3 || r.Moved != 34 {
		t.Fatalf("ladder tick 3: johnny %+v", r)
	}
	if r := rowFor(t, rows, "tess"); r.State != IdleStateWorking || r.Action != "" {
		t.Fatalf("tess took at tick 2, row %+v", r)
	}
	if len(w.queues["johnny"]) != 0 {
		t.Fatalf("johnny still holds %d open", len(w.queues["johnny"]))
	}
	moved := 0
	for _, title := range w.queues["stella"] {
		if strings.HasSuffix(title, "[moved from johnny: idle]") {
			moved++
		}
	}
	if moved != 34 {
		t.Fatalf("stella holds %d moved-from-johnny titles, want 34", moved)
	}

	// Further ticks: tess and nia keep working and are never redistributed;
	// johnny (now open 0) takes no more action.
	for i := 0; i < 5; i++ {
		tick()
	}
	for _, f := range []string{"tess", "nia"} {
		if n := w.count("redistribute " + f); n != 0 {
			t.Fatalf("%s took and was redistributed %d times", f, n)
		}
	}
	if w.count("nudge johnny") != 1 || w.count("wake johnny") != 1 || w.count("redistribute johnny") != 1 {
		t.Fatalf("ladder must fire each step once: %v", w.calls)
	}
	if w.coordCalls != 0 {
		t.Fatalf("coordinator calls = %d, want 0", w.coordCalls)
	}
	assertNoAsleep(t, mr, all)

	// The ledger refuses a hand label.
	if err := ledger.Save(ctx, map[string]IdleMark{"johnny": {State: "asleep"}}); err == nil {
		t.Fatal("ledger accepted state asleep")
	}
	if err := (&MemoryIdleLedger{}).Save(ctx, map[string]IdleMark{"johnny": {State: "asleep"}}); err == nil {
		t.Fatal("memory ledger accepted state asleep")
	}
	assertNoAsleep(t, mr, nil)

	// A friend that takes at tick 2 of its own wait (before the ladder
	// starts) never reaches the ladder.
	w2 := newIdleWorld()
	w2.addFriend("early", 3)
	l2 := &IdleLadder{Policy: IdlePolicy{IdleTicks: idleTicks}, Ledger: &MemoryIdleLedger{}, Actions: w2}
	for i := 1; i <= 10; i++ {
		if i == 2 {
			w2.take("early")
		}
		if _, err := l2.Tick(ctx, w2.observe()); err != nil {
			t.Fatal(err)
		}
	}
	if len(w2.calls) != 0 {
		t.Fatalf("a friend that took at tick 2 got %v", w2.calls)
	}
}

// TestAcceptance3033 is #3033's acceptance: kill a friend's wake unit and
// expire its heartbeat with 5 open tasks; within 3 ticks the row shows the
// state, the unit is reloaded, and the friend is working or the 5 tasks are
// on other queues with the moved-from title, with zero coordinator calls.
func TestAcceptance3033(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		upOnWake  bool
		wantMoved bool
	}{
		{name: "reloaded unit brings the friend back", upOnWake: true},
		{name: "friend stays down, work moves", wantMoved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newIdleWorld()
			w.addFriend("johnny", 5)
			w.addFriend("stella", 0)
			w.addFriend("rowan", 0)
			w.redistTarget = []string{"rowan", "stella"}
			// Kill the wake unit and expire the heartbeat.
			w.unitLoaded["johnny"] = false
			w.present["johnny"] = false
			if tc.upOnWake {
				w.upOnRepair["johnny"] = true
				w.takeOnWake["johnny"] = true
			}
			ledger, mr := redisIdleLedger(t)
			ladder := &IdleLadder{Policy: IdlePolicy{IdleTicks: 3}, Ledger: ledger, Actions: w}

			var all [][]IdleRow
			sawState := false
			for i := 1; i <= 3; i++ {
				rows, err := ladder.Tick(ctx, w.observe())
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, rows)
				if r := rowFor(t, rows, "johnny"); r.State == IdleStateDown {
					sawState = true
				}
				if w.working["johnny"] > 0 || len(w.queues["johnny"]) == 0 {
					break
				}
			}
			if !sawState {
				t.Fatal("row never showed the down state")
			}
			if !w.unitLoaded["johnny"] || w.count("repair johnny") != 1 {
				t.Fatalf("wake unit not reloaded once: loaded=%v calls=%v", w.unitLoaded["johnny"], w.calls)
			}
			moved := 0
			for _, title := range w.queues["stella"] {
				if strings.Contains(title, "[moved from johnny: down]") {
					moved++
				}
			}
			switch {
			case tc.wantMoved:
				if moved != 5 || len(w.queues["johnny"]) != 0 {
					t.Fatalf("5 tasks not moved: stella=%v johnny=%v", w.queues["stella"], w.queues["johnny"])
				}
			default:
				if w.working["johnny"] == 0 {
					t.Fatalf("friend not working after repair and wake: %v", w.calls)
				}
				if w.count("redistribute johnny") != 0 {
					t.Fatalf("working friend was redistributed: %v", w.calls)
				}
			}
			if len(w.queues["rowan"]) != 0 || w.coordCalls != 0 {
				t.Fatalf("coordinator touched: calls=%d queue=%v", w.coordCalls, w.queues["rowan"])
			}
			assertNoAsleep(t, mr, all)
		})
	}

	// Out of credits redistributes in the same tick.
	w := newIdleWorld()
	w.addFriend("emma", 5)
	w.addFriend("stella", 0)
	w.keeper["emma"] = IdleStateOutOfCredits
	w.redistTarget = []string{"stella"}
	ladder := &IdleLadder{Policy: IdlePolicy{IdleTicks: 3}, Ledger: &MemoryIdleLedger{}, Actions: w}
	rows, err := ladder.Tick(ctx, w.observe())
	if err != nil {
		t.Fatal(err)
	}
	if r := rowFor(t, rows, "emma"); r.State != IdleStateOutOfCredits || r.Moved != 5 {
		t.Fatalf("out-of-credits row %+v", r)
	}
}

// TestIdleFailedNudgeIsRetriedNotSkipped regresses stella's #3058 HOLD: Step
// must not advance to 1 when Nudge itself errors, or the next tick skips the
// nudge and goes straight to wake.
func TestIdleFailedNudgeIsRetriedNotSkipped(t *testing.T) {
	ctx := context.Background()
	w := newIdleWorld()
	w.addFriend("johnny", 3)
	w.addFriend("stella", 0)
	w.redistTarget = []string{"stella"}
	ledger := &MemoryIdleLedger{}
	ladder := &IdleLadder{Policy: IdlePolicy{IdleTicks: 1}, Ledger: ledger, Actions: w}

	if _, err := ladder.Tick(ctx, w.observe()); err != nil {
		t.Fatalf("tick 1 (within idle_ticks): %v", err)
	}

	w.failNudge["johnny"] = true
	if _, err := ladder.Tick(ctx, w.observe()); err == nil {
		t.Fatal("tick 2: injected nudge failure was not surfaced")
	}
	if mark := ledger.Marks["johnny"]; mark.Step != 0 {
		t.Fatalf("persisted step after a failed nudge = %d, want 0 (retryable at the same rung)", mark.Step)
	}
	if w.count("wake johnny") != 0 {
		t.Fatalf("wake fired after a failed nudge, ladder skipped ahead: %v", w.calls)
	}

	rows, err := ladder.Tick(ctx, w.observe())
	if err != nil {
		t.Fatalf("tick 3 (retry): %v", err)
	}
	if r := rowFor(t, rows, "johnny"); r.Action != IdleActionNudge || r.Step != 1 {
		t.Fatalf("tick 3 should retry the nudge, not skip to wake: %+v", r)
	}
	if w.count("nudge-error johnny") != 1 || w.count("nudge johnny") != 1 || w.count("wake johnny") != 0 {
		t.Fatalf("ladder calls after the retry: %v", w.calls)
	}
	if mark := ledger.Marks["johnny"]; mark.Step != 1 {
		t.Fatalf("persisted step after the retried nudge succeeded = %d, want 1", mark.Step)
	}
}

// TestIdleFailedWakeIsRetriedNotSkipped is the same regression one rung up:
// a successful nudge (Step 1) followed by a failing wake must leave Step at
// 1, so the next tick repeats repair-wake + wake instead of jumping to
// redistribute.
func TestIdleFailedWakeIsRetriedNotSkipped(t *testing.T) {
	ctx := context.Background()
	w := newIdleWorld()
	w.addFriend("johnny", 3)
	w.addFriend("stella", 0)
	w.redistTarget = []string{"stella"}
	ledger := &MemoryIdleLedger{}
	ladder := &IdleLadder{Policy: IdlePolicy{IdleTicks: 1}, Ledger: ledger, Actions: w}

	if _, err := ladder.Tick(ctx, w.observe()); err != nil { // within idle_ticks
		t.Fatalf("tick 1: %v", err)
	}
	rows, err := ladder.Tick(ctx, w.observe()) // ladder tick 1: nudge succeeds
	if err != nil {
		t.Fatalf("tick 2 (nudge): %v", err)
	}
	if r := rowFor(t, rows, "johnny"); r.Action != IdleActionNudge || r.Step != 1 {
		t.Fatalf("tick 2 should nudge: %+v", r)
	}

	w.failWake["johnny"] = true
	if _, err := ladder.Tick(ctx, w.observe()); err == nil { // ladder tick 2: wake errors
		t.Fatal("tick 3: injected wake failure was not surfaced")
	}
	if mark := ledger.Marks["johnny"]; mark.Step != 1 {
		t.Fatalf("persisted step after a failed wake = %d, want 1 (retryable at the same rung)", mark.Step)
	}
	if w.count("redistribute johnny") != 0 {
		t.Fatalf("redistribute fired after a failed wake, ladder skipped ahead: %v", w.calls)
	}

	rows, err = ladder.Tick(ctx, w.observe()) // retry: wake succeeds this time
	if err != nil {
		t.Fatalf("tick 4 (retry): %v", err)
	}
	if r := rowFor(t, rows, "johnny"); r.Action != IdleActionWake || r.Step != 2 {
		t.Fatalf("tick 4 should retry wake, not skip to redistribute: %+v", r)
	}
	if w.count("wake-error johnny") != 1 || w.count("wake johnny") != 1 || w.count("redistribute johnny") != 0 {
		t.Fatalf("ladder calls after the retry: %v", w.calls)
	}
	if mark := ledger.Marks["johnny"]; mark.Step != 2 {
		t.Fatalf("persisted step after the retried wake succeeded = %d, want 2", mark.Step)
	}
}

// TestIdleStatesMutationCannotWidenLedger regresses stella's other #3058
// HOLD item: IdleStates is exported and mutable, so a caller (or a bug) can
// write IdleStates["asleep"] = true. checkIdleMarks must not trust that map:
// the private isIdleState switch is the guard, so widening IdleStates must
// never let "asleep" (or any other hand label) past either ledger's Save.
func TestIdleStatesMutationCannotWidenLedger(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { delete(IdleStates, "asleep") })
	IdleStates["asleep"] = true

	if err := (&MemoryIdleLedger{}).Save(ctx, map[string]IdleMark{"johnny": {State: "asleep"}}); err == nil {
		t.Fatal("memory ledger accepted \"asleep\" after external mutation of IdleStates")
	}
	ledger, _ := redisIdleLedger(t)
	if err := ledger.Save(ctx, map[string]IdleMark{"johnny": {State: "asleep"}}); err == nil {
		t.Fatal("redis ledger accepted \"asleep\" after external mutation of IdleStates")
	}
}

// TestIdleActionIdemKeyStableAcrossRetryDistinctAcrossEpisodes regresses
// stella's second #3058 hold (score 7): Nudge and Wake carried no
// idempotency key, so an adapter whose transport loses a success response
// (the call happened, the error came back anyway) would replay it as a
// second side effect on the next tick's retry. The key must stay the same
// across a retry of the same rung within the same idle episode (so an
// adapter can dedupe the replay) and must not be reused by a later,
// unrelated episode for the same friend.
func TestIdleActionIdemKeyStableAcrossRetryDistinctAcrossEpisodes(t *testing.T) {
	ctx := context.Background()
	w := newIdleWorld()
	w.addFriend("johnny", 3)
	w.addFriend("stella", 0)
	w.redistTarget = []string{"stella"}
	ledger := &MemoryIdleLedger{}
	ladder := &IdleLadder{Policy: IdlePolicy{IdleTicks: 1}, Ledger: ledger, Actions: w}

	if _, err := ladder.Tick(ctx, w.observe()); err != nil { // within idle_ticks
		t.Fatalf("tick 1: %v", err)
	}

	w.failNudge["johnny"] = true
	if _, err := ladder.Tick(ctx, w.observe()); err == nil { // ladder tick 1: nudge errors
		t.Fatal("tick 2: injected nudge failure was not surfaced")
	}
	firstIdem := w.lastIdem["nudge johnny"]
	if firstIdem == "" {
		t.Fatal("no idem recorded on the failed nudge")
	}

	if _, err := ladder.Tick(ctx, w.observe()); err != nil { // retry: nudge succeeds
		t.Fatalf("tick 3 (retry): %v", err)
	}
	if retryIdem := w.lastIdem["nudge johnny"]; retryIdem != firstIdem {
		t.Fatalf("nudge idem changed across a retry of the same rung: %q -> %q; a lost response would duplicate the side effect", firstIdem, retryIdem)
	}

	// A later, unrelated idle episode for the same friend (open drains to
	// 0 and comes back) must not reuse the first episode's idem.
	w.queues["johnny"] = nil
	if _, err := ladder.Tick(ctx, w.observe()); err != nil { // open 0: state up, mark resets
		t.Fatalf("drain tick: %v", err)
	}
	w.addFriend("johnny", 3) // new open work: a new idle episode
	if _, err := ladder.Tick(ctx, w.observe()); err != nil {
		t.Fatalf("second episode tick 1: %v", err)
	}
	if _, err := ladder.Tick(ctx, w.observe()); err != nil { // ladder tick 1 again: nudge
		t.Fatalf("second episode ladder tick 1: %v", err)
	}
	if secondIdem := w.lastIdem["nudge johnny"]; secondIdem == firstIdem {
		t.Fatalf("a new idle episode reused the prior episode's idem key %q", secondIdem)
	}
}

// TestIdleDeficitAdvancesDespitePartialWorking regresses rowan's bus finding
// (Stella): IdleLadder.step read `working` whenever Working > 0, so a
// friend with 2 of 16 slots busy and 10 open tasks was reported simply
// "working" and the ladder never advanced, masking a 14-slot deficit. With
// Slots observed, a friend under its slot count with open work must still
// escalate.
func TestIdleDeficitAdvancesDespitePartialWorking(t *testing.T) {
	ctx := context.Background()
	w := newIdleWorld()
	w.addFriend("johnny", 10)
	w.addFriend("stella", 0)
	w.redistTarget = []string{"stella"}
	w.working["johnny"] = 2
	w.slots["johnny"] = 16
	ledger := &MemoryIdleLedger{}
	ladder := &IdleLadder{Policy: IdlePolicy{IdleTicks: 1}, Ledger: ledger, Actions: w}

	if rows, err := ladder.Tick(ctx, w.observe()); err != nil { // within idle_ticks
		t.Fatalf("tick 1: %v", err)
	} else if r := rowFor(t, rows, "johnny"); r.State == IdleStateWorking {
		t.Fatalf("2 of 16 slots working with 10 open must not read as fully working: %+v", r)
	}

	rows, err := ladder.Tick(ctx, w.observe()) // ladder tick 1: must still nudge
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if r := rowFor(t, rows, "johnny"); r.State != IdleStateIdle || r.Action != IdleActionNudge {
		t.Fatalf("deficit (2 of 16 working, 10 open) never advanced the ladder: %+v", r)
	}
	if w.count("nudge johnny") != 1 {
		t.Fatalf("deficit never nudged: %v", w.calls)
	}

	// Control: fully busy (working == slots) reads as working and the
	// ladder does not fire.
	w2 := newIdleWorld()
	w2.addFriend("nia", 10)
	w2.working["nia"] = 16
	w2.slots["nia"] = 16
	ladder2 := &IdleLadder{Policy: IdlePolicy{IdleTicks: 1}, Ledger: &MemoryIdleLedger{}, Actions: w2}
	for i := 0; i < 4; i++ {
		rows, err := ladder2.Tick(ctx, w2.observe())
		if err != nil {
			t.Fatalf("nia tick %d: %v", i, err)
		}
		if r := rowFor(t, rows, "nia"); r.State != IdleStateWorking || r.Action != "" {
			t.Fatalf("fully busy (16 of 16) must read working, not escalate: %+v", r)
		}
	}
	if len(w2.calls) != 0 {
		t.Fatalf("fully busy friend triggered actions: %v", w2.calls)
	}
}
