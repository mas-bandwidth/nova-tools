// Idle detection and its escalation ladder (nova-tools #3049, #3033
// mechanism 3).
//
// A friend with working 0 and open > 0 for more than IdleTicks consecutive
// sprint ticks is in the state `idle`, and the sprint tick escalates on its
// own: ladder tick 1 sends one bus nudge (it wakes the harness), ladder tick 2
// sends one direct `friend wake`, ladder tick 3 redistributes the friend's
// open work exactly as #3033 mechanism 1 (#3047) does for a friend out of
// credits. A friend that takes at any point leaves the ladder and is never
// redistributed. The same tick also carries the two other #3033 states: an
// out-of-credits friend is redistributed in the same tick, and a friend whose
// presence expired with open work has its wake unit repaired (#3048) and a
// wake sent at once, and is redistributed if it is still down one tick later.
//
// IdleTicks comes from the measured take latency (IdleTicksFromTakeLatency),
// never a guess. No code path here writes the word "asleep": the coordinator
// never gets to label a friend, and the ledger refuses any state outside
// IdleStates.
//
// Everything that changes the world goes through IdleActions, so the ladder
// is zero-token machinery: it never calls the coordinator or a model.
package life

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Row states the idle ladder prints. These are the only values it writes.
const (
	IdleStateWorking      = "working"
	IdleStateUp           = "up"
	IdleStateIdle         = "idle"
	IdleStateDown         = "down"
	IdleStateOutOfCredits = "out-of-credits"
)

// IdleStates is the closed set of states the ladder may write. The ledger
// refuses anything else, so "asleep" (or any hand label) can never land.
var IdleStates = map[string]bool{
	IdleStateWorking:      true,
	IdleStateUp:           true,
	IdleStateIdle:         true,
	IdleStateDown:         true,
	IdleStateOutOfCredits: true,
}

// Actions the ladder takes; each row names the one it took this tick.
const (
	IdleActionNone         = ""
	IdleActionNudge        = "nudge"
	IdleActionWake         = "wake"
	IdleActionRepairWake   = "repair-wake"
	IdleActionRedistribute = "redistribute"
)

// Redistribution reasons. #3047 puts `[moved from <f>: <reason>]` in each
// moved title.
const (
	IdleReasonIdle         = "idle"
	IdleReasonDown         = "down"
	IdleReasonOutOfCredits = "out of credits"
)

// IdlePolicy is the policy block the ladder reads. IdleTicks is the policy
// field `idle_ticks`; set it with IdleTicksFromTakeLatency.
type IdlePolicy struct {
	IdleTicks int
}

// IdleTicksFromTakeLatency turns measured take latencies (time from a task
// being open on a friend's queue with free width to that friend taking it)
// into the `idle_ticks` policy: the p95 latency in whole ticks, rounded up,
// at least 1. With no samples it refuses: the threshold is measured, never
// guessed.
func IdleTicksFromTakeLatency(samples []time.Duration, tick time.Duration) (int, error) {
	if tick <= 0 {
		return 0, fmt.Errorf("idle_ticks: tick must be positive")
	}
	if len(samples) == 0 {
		return 0, fmt.Errorf("idle_ticks: no take-latency samples; measure before setting")
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	p95 := sorted[idx]
	n := int(math.Ceil(float64(p95) / float64(tick)))
	if n < 1 {
		n = 1
	}
	return n, nil
}

// IdleObservation is what the tick read for one friend from Redis: its beat
// is within TTL (Present), its live children (Working), its open tasks
// (Open), and the keeper-written state (KeeperState, e.g. out-of-credits).
type IdleObservation struct {
	Friend      string
	Present     bool
	Working     int
	Open        int
	KeeperState string
}

// IdleMark is one friend's ladder position, kept across ticks by an
// IdleLedger. Count is consecutive ticks at working 0 with open > 0 while
// present; Step is the escalation already taken (0 none, 1 nudge, 2 wake,
// 3 redistribute); DownTicks is consecutive ticks down with open > 0.
type IdleMark struct {
	State     string
	Count     int
	Step      int
	DownTicks int
	Tick      int64
}

// IdleRow is what one friend's row prints after the tick.
type IdleRow struct {
	Friend string
	State  string
	Step   int
	Action string
	Moved  int
}

// IdleActions is every side effect of the ladder. The production adapter
// maps Nudge to a bus nudge, Wake to life.Wake (#2938), RepairWake to the
// wake-unit repair of #3048 and Redistribute to the redistribution of #3047.
// There is deliberately no coordinator method.
type IdleActions interface {
	Nudge(ctx context.Context, friend, reason string) error
	Wake(ctx context.Context, friend, reason string) error
	RepairWake(ctx context.Context, friend string) (repaired bool, err error)
	Redistribute(ctx context.Context, friend, reason string) (moved int, err error)
}

// IdleLedger keeps IdleMarks across ticks and restarts.
type IdleLedger interface {
	Load(ctx context.Context, friends []string) (map[string]IdleMark, error)
	Save(ctx context.Context, marks map[string]IdleMark) error
}

// IdleLadder runs the escalation for every friend once per sprint tick.
type IdleLadder struct {
	Policy  IdlePolicy
	Ledger  IdleLedger
	Actions IdleActions
	tick    int64
}

// Tick applies one sprint tick to the observations and returns the rows in
// friend order. An action error stops the tick after saving the marks
// already computed, so the next tick resumes from the same ladder step.
func (l *IdleLadder) Tick(ctx context.Context, obs []IdleObservation) ([]IdleRow, error) {
	if l.Ledger == nil || l.Actions == nil {
		return nil, fmt.Errorf("idle tick: ledger and actions are required")
	}
	if l.Policy.IdleTicks < 1 {
		return nil, fmt.Errorf("idle tick: policy idle_ticks must be >= 1 (measure it with IdleTicksFromTakeLatency)")
	}
	l.tick++
	sorted := append([]IdleObservation(nil), obs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Friend < sorted[j].Friend })
	names := make([]string, len(sorted))
	for i, o := range sorted {
		names[i] = o.Friend
	}
	prev, err := l.Ledger.Load(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("idle tick: load ledger: %w", err)
	}
	next := make(map[string]IdleMark, len(sorted))
	rows := make([]IdleRow, 0, len(sorted))
	var actErr error
	for _, o := range sorted {
		if actErr != nil {
			break
		}
		mark, row, err := l.step(ctx, o, prev[o.Friend])
		mark.Tick = l.tick
		next[o.Friend] = mark
		rows = append(rows, row)
		actErr = err
	}
	if err := l.Ledger.Save(ctx, next); err != nil {
		return rows, fmt.Errorf("idle tick: save ledger: %w", err)
	}
	return rows, actErr
}

func (l *IdleLadder) step(ctx context.Context, o IdleObservation, m IdleMark) (IdleMark, IdleRow, error) {
	row := IdleRow{Friend: o.Friend}
	switch {
	case o.KeeperState == IdleStateOutOfCredits:
		// #3033 mechanism 1: redistribute in the same tick.
		m = IdleMark{State: IdleStateOutOfCredits}
		row.State = m.State
		if o.Open > 0 || o.Working > 0 {
			row.Action = IdleActionRedistribute
			moved, err := l.Actions.Redistribute(ctx, o.Friend, IdleReasonOutOfCredits)
			row.Moved = moved
			if err != nil {
				return m, row, fmt.Errorf("redistribute %s: %w", o.Friend, err)
			}
		}
		return m, row, nil

	case !o.Present:
		// #3033 mechanisms 1 and 2: repair the wake unit and wake at once;
		// still down one tick later with open work, redistribute.
		m = IdleMark{State: IdleStateDown, DownTicks: m.DownTicks}
		row.State = m.State
		if o.Open == 0 {
			m.DownTicks = 0
			return m, row, nil
		}
		m.DownTicks++
		if m.DownTicks == 1 {
			row.Action = IdleActionRepairWake
			if _, err := l.Actions.RepairWake(ctx, o.Friend); err != nil {
				return m, row, fmt.Errorf("repair wake %s: %w", o.Friend, err)
			}
			if err := l.Actions.Wake(ctx, o.Friend, "down with open work"); err != nil {
				return m, row, fmt.Errorf("wake %s: %w", o.Friend, err)
			}
			return m, row, nil
		}
		row.Action = IdleActionRedistribute
		moved, err := l.Actions.Redistribute(ctx, o.Friend, IdleReasonDown)
		row.Moved = moved
		if err != nil {
			return m, row, fmt.Errorf("redistribute %s: %w", o.Friend, err)
		}
		return m, row, nil

	case o.Working > 0:
		m = IdleMark{State: IdleStateWorking}
		row.State = m.State
		return m, row, nil

	case o.Open == 0:
		m = IdleMark{State: IdleStateUp}
		row.State = m.State
		return m, row, nil
	}

	// Present, working 0, open > 0.
	m.DownTicks = 0
	m.Count++
	if m.Count <= l.Policy.IdleTicks {
		// Within the measured take latency: not idle yet.
		m.State = IdleStateUp
		m.Step = 0
		row.State = m.State
		return m, row, nil
	}
	m.State = IdleStateIdle
	row.State = m.State
	want := m.Count - l.Policy.IdleTicks
	if want > 3 {
		want = 3
	}
	switch {
	case m.Step < 1 && want >= 1:
		m.Step = 1
		row.Action = IdleActionNudge
		row.Step = m.Step
		if err := l.Actions.Nudge(ctx, o.Friend, fmt.Sprintf("idle: working 0, open %d", o.Open)); err != nil {
			return m, row, fmt.Errorf("nudge %s: %w", o.Friend, err)
		}
	case m.Step < 2 && want >= 2:
		m.Step = 2
		row.Action = IdleActionWake
		row.Step = m.Step
		if _, err := l.Actions.RepairWake(ctx, o.Friend); err != nil {
			return m, row, fmt.Errorf("repair wake %s: %w", o.Friend, err)
		}
		if err := l.Actions.Wake(ctx, o.Friend, "idle ladder"); err != nil {
			return m, row, fmt.Errorf("wake %s: %w", o.Friend, err)
		}
	default:
		// Step 3 and on: redistribute each tick while open work remains
		// (a pass may find no reader with free width).
		m.Step = 3
		row.Action = IdleActionRedistribute
		row.Step = m.Step
		moved, err := l.Actions.Redistribute(ctx, o.Friend, IdleReasonIdle)
		row.Moved = moved
		if err != nil {
			return m, row, fmt.Errorf("redistribute %s: %w", o.Friend, err)
		}
	}
	return m, row, nil
}

// MemoryIdleLedger keeps marks in process; for tests and one-process loops.
type MemoryIdleLedger struct {
	Marks map[string]IdleMark
}

func (m *MemoryIdleLedger) Load(_ context.Context, friends []string) (map[string]IdleMark, error) {
	out := make(map[string]IdleMark, len(friends))
	for _, f := range friends {
		if mark, ok := m.Marks[f]; ok {
			out[f] = mark
		}
	}
	return out, nil
}

func (m *MemoryIdleLedger) Save(_ context.Context, marks map[string]IdleMark) error {
	if err := checkIdleMarks(marks); err != nil {
		return err
	}
	if m.Marks == nil {
		m.Marks = map[string]IdleMark{}
	}
	for f, mark := range marks {
		m.Marks[f] = mark
	}
	return nil
}

// IdleKey is the Redis hash holding one friend's ladder mark; the table row
// reads its `state`.
func IdleKey(friend string) string { return "friend:" + friend + ":idle" }

// RedisIdleLedger keeps marks in friend:<f>:idle, one pipelined round trip
// per load and per save.
type RedisIdleLedger struct {
	Store *store.Store
}

var idleFields = []string{"state", "count", "step", "down_ticks", "tick"}

func (r RedisIdleLedger) Load(ctx context.Context, friends []string) (map[string]IdleMark, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("idle ledger: nil store")
	}
	reads := make([]store.HashRead, len(friends))
	for i, f := range friends {
		reads[i] = store.HashRead{Key: IdleKey(f), Fields: idleFields}
	}
	values, err := r.Store.PipelineHMGet(ctx, reads)
	if err != nil {
		return nil, fmt.Errorf("idle ledger load: %w", err)
	}
	out := make(map[string]IdleMark, len(friends))
	for i, f := range friends {
		v := values[i]
		if len(v) < len(idleFields) || v[0] == nil {
			continue
		}
		out[f] = IdleMark{
			State:     fmt.Sprint(v[0]),
			Count:     idleAtoi(v[1]),
			Step:      idleAtoi(v[2]),
			DownTicks: idleAtoi(v[3]),
			Tick:      int64(idleAtoi(v[4])),
		}
	}
	return out, nil
}

func (r RedisIdleLedger) Save(ctx context.Context, marks map[string]IdleMark) error {
	if r.Store == nil {
		return fmt.Errorf("idle ledger: nil store")
	}
	if err := checkIdleMarks(marks); err != nil {
		return err
	}
	if len(marks) == 0 {
		return nil
	}
	pipe := r.Store.Client().Pipeline()
	for f, m := range marks {
		pipe.HSet(ctx, IdleKey(f),
			"state", m.State,
			"count", strconv.Itoa(m.Count),
			"step", strconv.Itoa(m.Step),
			"down_ticks", strconv.Itoa(m.DownTicks),
			"tick", strconv.FormatInt(m.Tick, 10))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("idle ledger save: %w", err)
	}
	return nil
}

func checkIdleMarks(marks map[string]IdleMark) error {
	for f, m := range marks {
		if !IdleStates[m.State] {
			return fmt.Errorf("idle ledger: refusing state %q for %s (allowed: %s)", m.State, f, strings.Join(idleStateNames(), ", "))
		}
	}
	return nil
}

func idleStateNames() []string {
	names := make([]string, 0, len(IdleStates))
	for s := range IdleStates {
		names = append(names, s)
	}
	sort.Strings(names)
	return names
}

func idleAtoi(v any) int {
	if v == nil {
		return 0
	}
	n, err := strconv.Atoi(fmt.Sprint(v))
	if err != nil {
		return 0
	}
	return n
}
