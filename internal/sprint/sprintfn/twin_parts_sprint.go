package sprintfn

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The clock part (1.2) and the sprint part (1.0, 1.3.1, 1.3.5, R18) on the
// twin. Both are steps of sprint keys that apply while the machine is RUNNING
// or STOPPED.
//
// The sprint part's data is its own (types.go, SprintPart): the counter, the
// dropping marks and their undoing, the error step's parked keys with the
// refusal's detail, the quarantine's records, the coordinator and the tick end.
// Every list and map it takes is bounded, and every read it makes of the store
// is one batched call for the whole list (T4: nothing one key at a time), so
// its cost grows with the list divided by one command's pieces and never with
// the list.

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
	// Due are the due entries the time writes moved, Claimed the people whose
	// goal records they claimed, and Clock that they wrote the clock.
	Due     []string `json:"due,omitempty"`
	Claimed []string `json:"claimed,omitempty"`
	Clock   bool     `json:"clock,omitempty"`
	cmds    []Cmd
}

func (p *sprintPlan) commands() []Cmd { return p.cmds }

// What the tick end did (sprintPlan.TickEnd).
const (
	tickEndArmed    = "armed"    // behind_n recorded, and behind entered at R + 5 min when it was not there
	tickEndDisarmed = "disarmed" // behind removed, behind_n removed
	tickEndHeld     = "held"     // armed and not zero: nothing moves
	tickEndQuiet    = "quiet"    // zero and not armed: nothing to do
)

// sprintPart is the sprint part: the counter, the dropping marks, parked keys,
// the quarantine, the coordinator, the time rules' writes and the tick end. Goals and the sweep's
// position, which IT12's SprintPart also carries, are not written by this part
// (errata 2, item 3 and 8.1 do not list them): a request that names either is
// refused REQUEST, never ignored.
type sprintPart struct{}

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
// guarded by the value read, no counter goes down (U2: a score below the
// counter is placed, so the counter only rises), and the fields are at most
// the counter has.
func checkCounter(c *CounterChange) *Refusal {
	if len(c.Set) == 0 || len(c.Read) > CounterFieldsMax || len(c.Set) > CounterFieldsMax {
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
// (1.3.1, 1.3.5): the code, the rule, the stream, then the refusal's cells,
// separated by tabs (partText keeps a tab out of every value, so the same bytes
// come out of Go and Lua). It holds no note: the judgment J opens in the same
// step gets its seq from Layer 2 after the part has decided, and the record is
// the refusal's own detail.
func quarantineValue(q Quarantined) string {
	return strings.Join(append([]string{q.Code, q.Rule, q.Stream}, q.Cells...), "\t")
}

// checkQuarantine holds the quarantined cards of a step to their shape.
func checkQuarantine(qs []Quarantined) *Refusal {
	if len(qs) > QuarantineMax {
		return requestRefusal()
	}
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

// quarantineCarried says the body and the sprint part quarantine the same
// cards, in both directions (errata 2, item 6: the sprint part holds the
// quarantine): a body that names a quarantine no part writes would have X act on
// a card whose record is lost, since a part runs only when its field is set, and
// a part that writes a quarantine the body does not name would leave the card
// in the indexes X keeps, since X acts on the body's quarantine alone.
func quarantineCarried(req *Request) bool {
	var part []Quarantined
	if req.Sprint != nil {
		part = req.Sprint.Quarantine
	}
	if len(req.Body.Quarantine) == 0 && len(part) == 0 {
		return true
	}
	carried := make(map[string]bool, len(part))
	for _, q := range part {
		carried[q.ID] = true
	}
	named := make(map[string]bool, len(req.Body.Quarantine))
	for _, q := range req.Body.Quarantine {
		if !carried[q.ID] {
			return false
		}
		named[q.ID] = true
	}
	return len(named) == len(carried)
}

// parkedValue is the record of a parked key in {p}parked@e (1.1, 1.3.5): the
// refusal's code, the rule, the bound and the step's size and limit, separated
// by tabs. The judgment that names the key is found by the rule key, its
// subject, so the record holds no note.
func parkedValue(k ParkedKey) string {
	return strings.Join([]string{k.Code, k.Rule, k.Budget, k.Actual, k.Limit}, "\t")
}

// checkParked holds the error step's keys to their shape: each key named once,
// each field a name free of the delimiter (so a record is at most
// ParkedValueBytesMax), and no key both parked and unparked.
func checkParked(park []ParkedKey, unpark []string) *Refusal {
	if len(park) > SprintKeysMax || len(unpark) > SprintKeysMax {
		return requestRefusal()
	}
	named := make(map[string]bool, len(park)+len(unpark))
	optional := func(s string) bool { return s == "" || partText(s) }
	for _, k := range park {
		if !partText(k.Key) || !partText(k.Code) || !optional(k.Rule) || !optional(k.Budget) || named[k.Key] {
			return requestRefusal()
		}
		for _, d := range []string{k.Actual, k.Limit} {
			if d != "" && !tset.ValidDecimal(tset.Decimal(d)) {
				return requestRefusal()
			}
		}
		named[k.Key] = true
	}
	unnamed := make(map[string]bool, len(unpark))
	for _, k := range unpark {
		if !partText(k) || named[k] || unnamed[k] {
			return requestRefusal()
		}
		unnamed[k] = true
	}
	return nil
}

// checkDropping holds the marks and the unmarks to their shape: valid streams,
// an op each, at most 250 streams in all (members and streams together are at
// most 250), and no stream in both.
func checkDropping(mark, unmark map[string]string) *Refusal {
	if len(mark)+len(unmark) > SprintMembersMax {
		return requestRefusal()
	}
	for s, op := range mark {
		if !sprint.ValidID(s) || !partText(op) {
			return requestRefusal()
		}
		if _, both := unmark[s]; both {
			return requestRefusal()
		}
	}
	for s, op := range unmark {
		if !sprint.ValidID(s) || !partText(op) {
			return requestRefusal()
		}
	}
	return nil
}

// Pre decides every write of the part on the state the pre stage read, and
// builds the commands. The counter is guarded by the values the plan read
// (COUNTER when one moved), a stream is marked only when no other op holds it
// and unmarked only by the op that holds it (DROPPING); a parked key keeps its
// first record, and a card already quarantined keeps its first record. A parked
// key is moved out of the agenda: the park is written first and the agenda's
// ZREM last (A1), so an error between them leaves the key in both places and
// never in neither. Nothing is written that the key already holds, so a second
// run of the same request writes nothing.
func (sprintPart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	sp := req.Sprint
	if len(sp.Goals) != 0 || sp.Sweep != "" {
		return nil, requestRefusal()
	}
	if sp.Counter != nil {
		if ref := checkCounter(sp.Counter); ref != nil {
			return nil, ref
		}
	}
	if ref := checkDropping(sp.Dropping, sp.Undrop); ref != nil {
		return nil, ref
	}
	if ref := checkParked(sp.Park, sp.Unpark); ref != nil {
		return nil, ref
	}
	if len(sp.Park) != 0 && (req.Pop != nil || req.Ingest != nil) {
		return nil, requestRefusal() // the error step is a step of notes and sprint keys only: a pop or an ingest would queue the key it parks
	}
	if ref := checkQuarantine(sp.Quarantine); ref != nil {
		return nil, ref
	}
	if sp.Coordinator != "" && !partText(sp.Coordinator) {
		return nil, requestRefusal()
	}
	te := sp.TickEnd
	if te != nil && !tset.ValidDecimal(te.Backlog) {
		return nil, requestRefusal()
	}
	tm := sp.Time
	if ref := checkTime(tm, te != nil, req.Meta.Gen); ref != nil {
		return nil, ref
	}

	epoch := partEpoch(req)
	next, dropping, parked, agenda := epochKey(st, keyNext, epoch), epochKey(st, keyDropping, epoch), epochKey(st, keyParked, epoch), epochKey(st, keyAgenda, epoch)
	quarantine, coordinator := epochKey(st, keyQuarantine, epoch), sprintKey(st, keyCoordinator)
	tick, due := epochKey(st, keyTick, epoch), epochKey(st, keyDue, epoch)
	var guards []typedKey
	if sp.Counter != nil {
		guards = append(guards, typedKey{next, kindHash})
	}
	if len(sp.Dropping)+len(sp.Undrop) != 0 {
		guards = append(guards, typedKey{dropping, kindHash})
	}
	if len(sp.Park) != 0 {
		guards = append(guards, typedKey{parked, kindHash}, typedKey{agenda, kindZSet})
	} else if len(sp.Unpark) != 0 {
		guards = append(guards, typedKey{parked, kindHash})
	}
	if len(sp.Quarantine) != 0 {
		guards = append(guards, typedKey{quarantine, kindHash})
	}
	if sp.Coordinator != "" {
		guards = append(guards, typedKey{coordinator, kindString})
	}
	if te != nil {
		guards = append(guards, typedKey{tick, kindHash}, typedKey{due, kindZSet})
	}
	if tm != nil {
		if len(tm.Due) != 0 {
			guards = append(guards, typedKey{due, kindZSet})
		}
		for _, g := range sortedClaims(tm.Goals) {
			guards = append(guards, typedKey{sprintKey(st, keyGoalPrefix+g.Person), kindHash})
		}
		if tm.Clock != nil {
			guards = append(guards, typedKey{sprintKey(st, keyClock), kindHash})
		}
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
			if cur, _ := st.Keys.HGet(next, f); cur != c.Set[f] { // a field that holds the value is not written again
				set = append(set, f, c.Set[f])
			}
		}
		plan.Counter = set.count() != 0
		plan.cmds = append(plan.cmds, hsetCommands(next, set)...)
	}

	if len(sp.Dropping)+len(sp.Undrop) != 0 {
		var mark flatPairs
		var unmark []string
		for _, s := range sortedMapKeys(sp.Dropping) {
			held, marked := st.Keys.HGet(dropping, s)
			switch op := sp.Dropping[s]; {
			case marked && held != op:
				return nil, partRefusal(CodeDropping, RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: []string{s}}},
					"stream %s is frozen by another op", s)
			case !marked:
				mark = append(mark, s, op)
				plan.Marked = append(plan.Marked, s)
			}
		}
		for _, s := range sortedMapKeys(sp.Undrop) {
			held, marked := st.Keys.HGet(dropping, s)
			switch {
			case !marked: // nothing to undo
			case held != sp.Undrop[s]:
				return nil, partRefusal(CodeDropping, RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: []string{s}}},
					"stream %s is frozen by another op", s)
			default:
				unmark = append(unmark, s)
				plan.Unmarked = append(plan.Unmarked, s)
			}
		}
		plan.cmds = append(plan.cmds, hsetCommands(dropping, mark)...)
		plan.cmds = append(plan.cmds, hdelCommands(dropping, unmark)...)
	}

	if len(sp.Park)+len(sp.Unpark) != 0 {
		var put flatPairs
		var leave, unpark []string
		park := append([]ParkedKey(nil), sp.Park...)
		sort.Slice(park, func(i, j int) bool { return park[i].Key < park[j].Key })
		for _, k := range park {
			if _, isParked := st.Keys.HGet(parked, k.Key); !isParked {
				put = append(put, k.Key, parkedValue(k)) // an already parked key keeps its first record
			}
			plan.Parked = append(plan.Parked, k.Key)
			if _, queued := st.Keys.ZScore(agenda, k.Key); queued {
				leave = append(leave, k.Key) // also finishes a move a fault left half done
			}
		}
		unparkKeys := append([]string(nil), sp.Unpark...)
		sort.Strings(unparkKeys)
		for _, k := range unparkKeys {
			if _, isParked := st.Keys.HGet(parked, k); isParked {
				unpark = append(unpark, k)
				plan.Unparked = append(plan.Unparked, k)
			}
		}
		plan.cmds = append(plan.cmds, hsetCommands(parked, put)...)   // the park first (A1)
		plan.cmds = append(plan.cmds, zremCommands(agenda, leave)...) // the key leaves the agenda last
		plan.cmds = append(plan.cmds, hdelCommands(parked, unpark)...)
	}

	if qs := sp.Quarantine; len(qs) != 0 {
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
		if cur, there := st.Keys.Get(coordinator); !there || cur != sp.Coordinator { // the same coordinator is not written again
			plan.Coordinator = sp.Coordinator
			plan.cmds = append(plan.cmds, Command("SET", coordinator, kindString, sp.Coordinator))
		}
	}

	if tm != nil {
		// The time rules' writes (2.3 R14, R17, R18): each due entry moved to
		// its time unless it is there already, each claim written, and the
		// clock's fields that differ; the owed work (the due entries) first.
		var put flatPairs
		for _, d := range sortedDue(tm.Due) {
			at, _ := canonicalInt(string(d.At))
			if cur, there := st.Keys.ZScore(due, d.Key); !there || cur != float64(at) {
				put = append(put, string(d.At), d.Key)
				plan.Due = append(plan.Due, d.Key)
			}
		}
		plan.cmds = append(plan.cmds, zaddCommands(due, put)...)
		for _, g := range sortedClaims(tm.Goals) {
			plan.cmds = append(plan.cmds, hsetCommands(sprintKey(st, keyGoalPrefix+g.Person),
				flatPairs{goalFieldClaimedGen, strconv.FormatUint(req.Meta.Gen, 10), goalFieldClaimedR, string(g.R)})...)
			plan.Claimed = append(plan.Claimed, g.Person)
		}
		if c := tm.Clock; c != nil {
			rec, ref := readClock(st)
			if ref != nil {
				return nil, ref
			}
			var set flatPairs
			for _, f := range []struct {
				field string
				to    *tset.Decimal
				cur   int64
			}{{clockFieldDueSince, c.DueSince, rec.DueSinceMs}, {clockFieldStopRaised, c.StopRaised, rec.StopRaisedMs}} {
				if f.to != nil && string(*f.to) != blankOf(f.cur) {
					set = append(set, f.field, string(*f.to))
				}
			}
			plan.Clock = len(set) != 0
			plan.cmds = append(plan.cmds, hsetCommands(sprintKey(st, keyClock), set)...)
		}
	}

	if te != nil {
		r, _, ref := runningTime(st)
		if ref != nil {
			return nil, ref
		}
		behindN, isSet := st.Keys.HGet(tick, tickFieldBehind)
		if isSet && behindN != "" && !tset.ValidDecimal(tset.Decimal(behindN)) {
			return nil, storedRefusal()
		}
		isSet = isSet && behindN != ""
		_, entry := st.Keys.ZScore(due, "behind")
		switch {
		case te.Backlog == "0" && (isSet || entry):
			plan.TickEnd = tickEndDisarmed
			if entry {
				plan.cmds = append(plan.cmds, zremCommands(due, []string{"behind"})...)
			}
			if isSet {
				plan.cmds = append(plan.cmds, hdelCommands(tick, []string{tickFieldBehind})...)
			}
		case te.Backlog == "0":
			plan.TickEnd = tickEndQuiet
		case !isSet:
			plan.TickEnd = tickEndArmed
			if !entry { // the entry is not moved while it stands
				plan.cmds = append(plan.cmds, zaddCommands(due, flatPairs{decimalOf(r + BehindSpanMS), "behind"})...)
			}
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

// The goal records (1.0: {p}goal:<person>, a sprint key) and the fields R14's
// phase 1 claims on one (2.3 R14).
const (
	keyGoalPrefix       = "goal:"
	goalFieldClaimedGen = "claimed_gen"
	goalFieldClaimedR   = "claimed_r"
)

// checkTime holds the time writes to their shape: at most SprintKeysMax due
// entries, each named once, a key a part may store, due at an exact time; at
// most SprintMembersMax claims, a person each once, at an exact R, and a step
// with a lease generation to claim with; a clock write that sets something,
// each field "" or an exact time. A due entry named behind beside a tick end is
// REQUEST: the tick end is behind's one writer in a step.
func checkTime(tm *SprintTime, tickEnd bool, gen uint64) *Refusal {
	if tm == nil {
		return nil
	}
	if len(tm.Due) > SprintKeysMax || len(tm.Goals) > SprintMembersMax || len(tm.Due)+len(tm.Goals) == 0 && tm.Clock == nil {
		return requestRefusal()
	}
	exact := func(d tset.Decimal) bool {
		n, ok := canonicalInt(string(d))
		return ok && n <= maxExactMS
	}
	seen := map[string]bool{}
	for _, d := range tm.Due {
		if !partText(d.Key) || seen[d.Key] || !exact(d.At) || tickEnd && d.Key == "behind" {
			return requestRefusal()
		}
		seen[d.Key] = true
	}
	people := map[string]bool{}
	for _, g := range tm.Goals {
		if !sprint.ValidID(g.Person) || people[g.Person] || !exact(g.R) || gen == 0 {
			return requestRefusal()
		}
		people[g.Person] = true
	}
	if c := tm.Clock; c != nil {
		if c.DueSince == nil && c.StopRaised == nil {
			return requestRefusal()
		}
		for _, v := range []*tset.Decimal{c.DueSince, c.StopRaised} {
			if v != nil && *v != "" && !exact(*v) {
				return requestRefusal()
			}
		}
	}
	return nil
}

// sortedDue is the due writes by key; sortedClaims the claims by person.
func sortedDue(ds []DueAt) []DueAt {
	out := append([]DueAt(nil), ds...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func sortedClaims(gs []GoalClaim) []GoalClaim {
	out := append([]GoalClaim(nil), gs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Person < out[j].Person })
	return out
}

// blankOf is a clock field as clockFields writes it: "" for zero.
func blankOf(n int64) string {
	if n == 0 {
		return ""
	}
	return decimalOf(n)
}
