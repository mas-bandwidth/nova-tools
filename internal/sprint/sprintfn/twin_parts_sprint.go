package sprintfn

import (
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The clock part (1.2) and the sprint part (1.0, 1.3.1, 1.3.5, R18) on the
// twin. Both are steps of sprint keys that apply while the machine is RUNNING
// or STOPPED.

// BehindSpanMS is the running time a backlog may stand before R18 looks at it:
// behind is due at R + 5 min (1.2, R18).
const BehindSpanMS int64 = 5 * 60 * 1000

// QuarantineValueBytesMax is the longest record of one quarantined card: 1.3.1
// gives none, so the narrower reading is 1.0's bound on a caller result.
const QuarantineValueBytesMax = tset.MaxResultBytes

// ---- the clock part (1.2) ----

// clockPlan is the clock part's reply: the verb, whether the machine runs
// after it, and R at the call's time (which start and stop leave where it
// stood, A2).
type clockPlan struct {
	Verb    string       `json:"verb"`
	Running bool         `json:"running"`
	R       tset.Decimal `json:"r"`
	SinceMS tset.Decimal `json:"stopped_since_ms,omitempty"`
	cmds    []Cmd
}

func (p *clockPlan) commands() []Cmd { return p.cmds }

// clockPart is the clock part: init, start, stop and clear, each in its own
// step (1.2).
type clockPart struct{}

// Pre decides the verb on the clock as the pre stage read it, so the guard is
// the apply's own (A2): stop while STOPPED and start while RUNNING are
// refused MACHINESTATE, and a second stop can never move stopped_since_ms. init
// writes a clock that is STOPPED since the call's time, and is refused over an
// existing one (it would move R). clear runs only while STOPPED (errata 1, the
// added notes) and leaves the clock as it is: the verb table's "clock STOPPED
// (when RUNNING)" is read the narrower way, as a refusal of a RUNNING machine.
func (clockPart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	key := sprintKey(st, keyClock)
	rec, ref := readClock(st) // checks the type
	if ref != nil {
		return nil, ref
	}
	now, ref := partNow(st)
	if ref != nil {
		return nil, ref
	}
	verb := req.Clock.Verb
	var next clockRecord
	var err error
	switch verb {
	case ClockInit:
		if st.Keys.Type(key) != kindNone {
			return nil, partRefusal(CodeMachineState, RefusalDetail{}, "the sprint is already initialised")
		}
		next = clockRecord{StoppedSinceMs: now}
	case ClockStart:
		next, err = clockStart(rec, now)
	case ClockStop:
		next, err = clockStop(rec, now)
	case ClockClear:
		next = rec
		if rec.StoppedSinceMs == 0 {
			err = errClockRunning
		}
	default:
		return nil, requestRefusal()
	}
	switch err {
	case nil:
	case errClockRunning:
		return nil, partRefusal(CodeMachineState, RefusalDetail{}, "the machine is already running")
	default: // errClockStopped
		return nil, partRefusal(CodeMachineState, RefusalDetail{}, "the machine is already stopped, since %d", rec.StoppedSinceMs)
	}
	plan := &clockPlan{Verb: verb, Running: next.StoppedSinceMs == 0, R: tset.Decimal(decimalOf(clockRunning(next, now)))}
	if next.StoppedSinceMs != 0 {
		plan.SinceMS = tset.Decimal(decimalOf(next.StoppedSinceMs))
	}
	if verb != ClockClear {
		plan.cmds = hsetCommands(key, flatPairs(clockFields(next)))
	}
	if ref := checkCommands(st, plan.cmds); ref != nil {
		return nil, ref
	}
	return plan, nil
}

// Cmds hands back the commands Pre built.
func (clockPart) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) { return cmdsOf(plan) }

// ---- the sprint part (1.0) ----

// sprintPlan is the sprint part's reply: what it wrote.
type sprintPlan struct {
	Skipped     bool     `json:"skipped"`
	Counter     bool     `json:"counter"`
	Marked      []string `json:"marked"`
	Unmarked    []string `json:"unmarked"`
	Parked      []string `json:"parked"`
	Unparked    []string `json:"unparked"`
	Quarantined []string `json:"quarantined"`
	Coordinator string   `json:"coordinator,omitempty"`
	TickEnd     string   `json:"tickend,omitempty"`
	cmds        []Cmd
}

func (p *sprintPlan) commands() []Cmd { return p.cmds }

// What the tick end did (sprintPlan.TickEnd).
const (
	tickEndArmed    = "armed"    // behind entered at R + 5 min, behind_n recorded
	tickEndDisarmed = "disarmed" // behind removed, behind_n removed
	tickEndHeld     = "held"     // armed and not zero: nothing moves
	tickEndQuiet    = "quiet"    // zero and not armed: nothing to do
)

// sprintPart is the sprint part: the counter, the dropping marks, parked keys,
// the quarantine, the coordinator and the tick end. tickEnd is the seam of the
// tick end (TickEnd). Goals and the sweep's position, which IT12's SprintPart
// also carries, are not written by this part (errata 2, item 3 and 8.1 do not
// list them): a request that names either is refused REQUEST, never ignored.
type sprintPart struct {
	tickEnd func(*Request) *TickEnd
}

// counterField says a field name is one of {p}next@e's (1.3.1): score,
// streams, id:<s> or gate:<s>.
func counterField(f string) bool {
	if f == "score" || f == "streams" {
		return true
	}
	for _, p := range []string{"id:", "gate:"} {
		if s, ok := strings.CutPrefix(f, p); ok {
			return sprint.ValidID(s)
		}
	}
	return false
}

// decimalOrZero is a counter value, where "" is a field that is not there yet.
func decimalOrZero(s string) (tset.Decimal, bool) {
	if s == "" {
		return "0", true
	}
	return tset.Decimal(s), tset.ValidDecimal(tset.Decimal(s))
}

// checkCounter holds a counter change to its shape: every field written is
// guarded by the value read, and no counter goes down (U2: a score below the
// counter is placed, so the counter only rises).
func checkCounter(c *CounterChange) *Refusal {
	if len(c.Set) == 0 {
		return requestRefusal()
	}
	for f, v := range c.Read {
		if _, ok := decimalOrZero(v); !ok || !counterField(f) {
			return requestRefusal()
		}
	}
	for f, v := range c.Set {
		read, guarded := c.Read[f]
		if !guarded || !counterField(f) || !tset.ValidDecimal(tset.Decimal(v)) {
			return requestRefusal()
		}
		was, _ := decimalOrZero(read)
		if compareDecimal(tset.Decimal(v), was) < 0 {
			return requestRefusal()
		}
	}
	return nil
}

// sortedMapKeys is the keys of a map in order.
func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// quarantineValue is the record of a quarantined card in {p}quarantine@e
// (1.3.1, 1.3.5): the code, the rule, the stream, the judgment's note, then the
// refusal's cells, separated by tabs (validText keeps a tab out of every value,
// so the same bytes come out of Go and Lua). The note is the judgment J opens
// in the same step, whose id Layer 2 assigns after the part has decided and
// which jopen already holds for the card, so it is left empty.
func quarantineValue(q Quarantined) string {
	return strings.Join(append([]string{q.Code, q.Rule, q.Stream, ""}, q.Cells...), "\t")
}

// checkQuarantine holds the quarantined cards of a step to their shape.
func checkQuarantine(qs []Quarantined) *Refusal {
	for _, q := range qs {
		if !partText(q.ID) || !partText(q.Code) || (q.Rule != "" && !partText(q.Rule)) || (q.Stream != "" && !partText(q.Stream)) {
			return requestRefusal()
		}
		for _, c := range q.Cells {
			if !partText(c) {
				return requestRefusal()
			}
		}
		if len(quarantineValue(q)) > QuarantineValueBytesMax {
			return requestRefusal()
		}
	}
	return nil
}

// Pre decides every write of the part on the state the pre stage read, and
// builds the commands. The counter is guarded by the values the plan read
// (COUNTER when one moved), a stream is marked only when no other op holds it
// (DROPPING), and a key is parked only when no other note holds it (XGUARD); a
// card already quarantined keeps its first record. A parked key is moved out of
// the agenda: the park is written first and the agenda's ZREM last (A1), so an
// error between them leaves the key in both places and never in neither.
func (p sprintPart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	sp := req.Sprint
	if len(sp.Goals) != 0 || sp.Sweep != "" {
		return nil, requestRefusal()
	}
	var te *TickEnd
	if p.tickEnd != nil {
		te = p.tickEnd(req)
	}
	if sp.Counter != nil {
		if ref := checkCounter(sp.Counter); ref != nil {
			return nil, ref
		}
	}
	if len(sp.Dropping) > SprintMembersMax {
		return nil, requestRefusal()
	}
	for s, op := range sp.Dropping {
		if !sprint.ValidID(s) || (op != "" && !partText(op)) {
			return nil, requestRefusal()
		}
	}
	for k, note := range sp.Park {
		if !partText(k) || (note != "" && !partText(note)) {
			return nil, requestRefusal()
		}
	}
	if ref := checkQuarantine(req.Body.Quarantine); ref != nil {
		return nil, ref
	}
	if sp.Coordinator != "" && !partText(sp.Coordinator) {
		return nil, requestRefusal()
	}
	if te != nil && !tset.ValidDecimal(te.Backlog) {
		return nil, requestRefusal()
	}

	epoch := partEpoch(req)
	next, dropping, parked, agenda := epochKey(st, keyNext, epoch), epochKey(st, keyDropping, epoch), epochKey(st, keyParked, epoch), epochKey(st, keyAgenda, epoch)
	quarantine, coordinator := epochKey(st, keyQuarantine, epoch), sprintKey(st, keyCoordinator)
	tick, due := epochKey(st, keyTick, epoch), epochKey(st, keyDue, epoch)
	var guards []typedKey
	if sp.Counter != nil {
		guards = append(guards, typedKey{next, kindHash})
	}
	if len(sp.Dropping) != 0 {
		guards = append(guards, typedKey{dropping, kindHash})
	}
	if len(sp.Park) != 0 {
		guards = append(guards, typedKey{parked, kindHash}, typedKey{agenda, kindZSet})
	}
	if len(req.Body.Quarantine) != 0 {
		guards = append(guards, typedKey{quarantine, kindHash})
	}
	if sp.Coordinator != "" {
		guards = append(guards, typedKey{coordinator, kindString})
	}
	if te != nil {
		guards = append(guards, typedKey{tick, kindHash}, typedKey{due, kindZSet})
	}
	if ref := guardTypes(st, guards...); ref != nil {
		return nil, ref
	}

	plan := &sprintPlan{Marked: []string{}, Unmarked: []string{}, Parked: []string{}, Unparked: []string{}, Quarantined: []string{}}
	if req.Lease != nil { // a loop that does not hold the lease writes its idle fields and nothing else (1.4.2, RT1)
		held, ref := tickAuthority(st, req)
		if ref != nil {
			return nil, ref
		}
		if !held {
			plan.Skipped = true
			return plan, nil
		}
	}

	if c := sp.Counter; c != nil {
		for _, f := range sortedMapKeys(c.Read) {
			if cur, _ := st.Keys.HGet(next, f); cur != c.Read[f] {
				return nil, partRefusal(CodeCounter, RefusalDetail{}, "the counter moved since the read: %s is %q, read as %q", f, cur, c.Read[f])
			}
		}
		var set flatPairs
		for _, f := range sortedMapKeys(c.Set) {
			set = append(set, f, c.Set[f])
		}
		plan.Counter = true
		plan.cmds = append(plan.cmds, hsetCommands(next, set)...)
	}

	if len(sp.Dropping) != 0 {
		var mark flatPairs
		var unmark []string
		for _, s := range sortedMapKeys(sp.Dropping) {
			held, marked := st.Keys.HGet(dropping, s)
			switch op := sp.Dropping[s]; {
			case op == "":
				if marked {
					unmark = append(unmark, s)
					plan.Unmarked = append(plan.Unmarked, s)
				}
			case marked && held != op:
				return nil, partRefusal(CodeDropping, RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: []string{s}}},
					"stream %s is frozen by another op", s)
			case !marked:
				mark = append(mark, s, op)
				plan.Marked = append(plan.Marked, s)
			}
		}
		plan.cmds = append(plan.cmds, hsetCommands(dropping, mark)...)
		plan.cmds = append(plan.cmds, hdelCommands(dropping, unmark)...)
	}

	if len(sp.Park) != 0 {
		var park flatPairs
		var leave, unpark []string
		for _, k := range sortedMapKeys(sp.Park) {
			held, isParked := st.Keys.HGet(parked, k)
			note := sp.Park[k]
			switch {
			case note == "":
				if isParked {
					unpark = append(unpark, k)
					plan.Unparked = append(plan.Unparked, k)
				}
				continue
			case isParked && held != note:
				return nil, partRefusal(CodeXGuard, RefusalDetail{}, "the key is already parked by another note")
			case !isParked:
				park = append(park, k, note)
			}
			plan.Parked = append(plan.Parked, k)
			if _, queued := st.Keys.ZScore(agenda, k); queued {
				leave = append(leave, k)
			}
		}
		plan.cmds = append(plan.cmds, hsetCommands(parked, park)...)  // the park first (A1)
		plan.cmds = append(plan.cmds, zremCommands(agenda, leave)...) // the key leaves the agenda last
		plan.cmds = append(plan.cmds, hdelCommands(parked, unpark)...)
	}

	if qs := req.Body.Quarantine; len(qs) != 0 {
		var put flatPairs
		seen := map[string]bool{}
		sorted := append([]Quarantined(nil), qs...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
		for _, q := range sorted {
			if seen[q.ID] {
				continue // a card named twice keeps its first record
			}
			seen[q.ID] = true
			if _, there := st.Keys.HGet(quarantine, q.ID); there {
				continue // already quarantined: the evidence of the first refusal stands
			}
			put = append(put, q.ID, quarantineValue(q))
			plan.Quarantined = append(plan.Quarantined, q.ID)
		}
		plan.cmds = append(plan.cmds, hsetCommands(quarantine, put)...)
	}

	if sp.Coordinator != "" {
		plan.Coordinator = sp.Coordinator
		plan.cmds = append(plan.cmds, Command("SET", coordinator, kindString, sp.Coordinator))
	}

	if te != nil {
		r, _, ref := runningTime(st)
		if ref != nil {
			return nil, ref
		}
		_, armed := st.Keys.ZScore(due, "behind")
		switch {
		case te.Backlog == "0" && armed:
			plan.TickEnd = tickEndDisarmed
			plan.cmds = append(plan.cmds, zremCommands(due, []string{"behind"})...)
			plan.cmds = append(plan.cmds, hdelCommands(tick, []string{tickFieldBehind})...)
		case te.Backlog == "0":
			plan.TickEnd = tickEndQuiet
		case !armed:
			plan.TickEnd = tickEndArmed
			plan.cmds = append(plan.cmds, zaddCommands(due, flatPairs{decimalOf(r + BehindSpanMS), "behind"})...)
			plan.cmds = append(plan.cmds, hsetCommands(tick, flatPairs{tickFieldBehind, string(te.Backlog)})...)
		default:
			plan.TickEnd = tickEndHeld
		}
	}

	if ref := checkCommands(st, plan.cmds); ref != nil {
		return nil, ref
	}
	return plan, nil
}

// Cmds hands back the commands Pre built.
func (sprintPart) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) { return cmdsOf(plan) }
