package sprint

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The tests of the time rules (rules_time.go, IT10). They run each rule the way
// the tick does: the rule's Read plans what its keys need, a twin answers that
// plan from a world of the four tables and the sprint's facts, LoadPartial
// loads a partial snapshot from the answer, and the rule plans on it. A
// planner that reads what its plan did not ask for panics in a test (1.5.2), so
// the twin holds a rule to its read. Each plan is then applied to the world the
// way the store would (the guards of an entry and of X, J's one judgment per
// cause, the merging of two entries on one card, the writes to the sprint's
// keys), and the rule plans again: what a rule left changed, and what it left
// to be done twice, is read off the world and not off the plan.

const (
	timeMinD  = time.Minute
	timeSec   = int64(1000)
	timeMin   = 60 * timeSec
	timeHour  = 60 * timeMin
	timeR0    = 10 * timeHour
	timeWall0 = int64(1_800_000_000_000)
)

// worldFacts are the sprint's own keys the rules read beside the tables: what
// the twin answers the sprint-key reads from.
type worldFacts struct {
	Notes    map[string]NoteFact
	Goals    map[string]GoalFact
	Cuts     map[string]CutFact
	Tick     TickFact
	Dropping map[string]string // stream -> the op that marked it
}

// timeWorld is the four tables, the sprint's facts, the due set's entries the
// rules move, J's open judgments and the clock, at one Now.
type timeWorld struct {
	t     testing.TB
	s     *Snapshot
	f     *worldFacts
	now   Now
	j     map[string]string // J's jopen: subject|type|cause to the note, or the hold h<note>
	due   map[string]int64  // due entries the rules move, by key
	clock Clock
	// timeMS is the store's time the twin answers with; no rule may read it.
	timeMS Decimal
	// claims counts the claims R14 wrote on goal records, parks the keys
	// parked.
	claims, parks int
}

func newTimeWorld(t testing.TB) *timeWorld {
	s := &Snapshot{Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Work.SetRows([]string{"s1"})
	s.Merge.SetRows([]string{"s1"})
	s.Fleet.SetRows([]string{"m1", "m2", "m3"})
	s.Readers.SetRows([]string{"r1", "r2", "r3"})
	w := &timeWorld{t: t, s: s, now: Now{R: timeR0, Wall: timeWall0, Running: true}, timeMS: "1790000000123",
		f: &worldFacts{Notes: map[string]NoteFact{}, Goals: map[string]GoalFact{}, Cuts: map[string]CutFact{}, Dropping: map[string]string{}},
		j: map[string]string{}, due: map[string]int64{}}
	w.member("m1", true)
	w.member("m2", true)
	w.member("m3", false)
	w.put(s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging})
	return w
}

// put places a card at revision 1.
func (w *timeWorld) put(t *Table, id, row, col string, fields map[string]string) *Card {
	c := &Card{ID: id, Row: row, Col: col, Score: 5, Rev: 1, Fields: map[string]string{}}
	for k, v := range fields {
		c.Fields[k] = v
	}
	t.Put(c)
	return c
}

// member sets a member's control card, up or down.
func (w *timeWorld) member(m string, up bool) {
	status := Down
	if up {
		status = Up
	}
	w.put(w.s.Fleet, CtlID(m), m, Ctl, map[string]string{"status": status})
}

// primary places a primary of stream s1 in a column of the work table.
func (w *timeWorld) primary(id, col string, fields map[string]string) *Card {
	f := map[string]string{"attempt": "1"}
	for k, v := range fields {
		f[k] = v
	}
	return w.put(w.s.Work, id, "s1", col, f)
}

// workCard places a work card of a member in a column of the fleet table.
func (w *timeWorld) workCard(id, member, col string, fields map[string]string) *Card {
	p, attempt, _ := ParseWorkCard(id)
	f := map[string]string{"kind": "work", "primary": p, "stream": "s1", "attempt": itoa(attempt), "gen": "1", "member": member}
	for k, v := range fields {
		f[k] = v
	}
	return w.put(w.s.Fleet, id, member, col, f)
}

// readCard places a read card of a reader in a column of the readers table.
func (w *timeWorld) readCard(id, reader, col string, fields map[string]string) *Card {
	p, attempt, _, _ := ParseReadCard(id)
	f := map[string]string{"kind": "read", "primary": p, "stream": "s1", "attempt": itoa(attempt), "reader": reader}
	for k, v := range fields {
		f[k] = v
	}
	return w.put(w.s.Readers, id, reader, col, f)
}

// card is the card of a table, nil when the table has none.
func (w *timeWorld) card(table, id string) *Card { return w.s.T(table).Card(id) }

// The columns a member's and a reader's counts are read for.
var (
	fleetCols   = []string{Ready, Working, Withdrawn, DoneOK, DoneFailed}
	readersCols = []string{Asked, Reading, OK, Broken}
)

// project is the record as a read with the fields returns it: only those.
func project(c *Card, fields []string) *Card {
	cc := *c
	if len(fields) == 0 {
		cc.Fields = map[string]string{}
		for k, v := range c.Fields {
			cc.Fields[k] = v
		}
		return &cc
	}
	cc.Fields = map[string]string{}
	for _, f := range fields {
		if v, ok := c.Fields[f]; ok {
			cc.Fields[f] = v
		}
	}
	return &cc
}

// answer is the twin's read: the answer to the plan, from the world, and to
// the plan only. Layer 1's queries are answered by IT05's stub; the composite
// queries and the sprint-key reads are answered here, each for the ids it
// names, with the fields it names.
func (w *timeWorld) answer(rp ReadPlan) ReadAnswer {
	ans := wholeStore{w.s}.Answer(rp)
	ans.TimeMS = w.timeMS
	ans.Sprint = nil
	for _, q := range rp.Sprint {
		ans.Sprint = append(ans.Sprint, w.answerSprint(q))
	}
	return ans
}

func (w *timeWorld) answerSprint(q SprintQ) Answer {
	a := Answer{Kind: q.Kind}
	switch q.Kind {
	case QueryRelated:
		t := w.s.T(q.Table)
		for _, id := range q.Source.IDs {
			c := t.Card(id)
			if c == nil {
				continue
			}
			a.Records = append(a.Records, TableCard{q.Table, project(c, q.Fields)})
			if contains(q.Follow, FollowRCards) {
				for _, rid := range Split(c.Fields["rcards"]) {
					if rc := w.s.Readers.Card(rid); rc != nil {
						a.Records = append(a.Records, TableCard{Readers, project(rc, q.Fields)})
					}
				}
			}
		}
	case QueryFleet, QueryReaders:
		t, cols := w.s.Fleet, fleetCols
		if q.Kind == QueryReaders {
			t, cols = w.s.Readers, readersCols
		}
		a.Rows = append([]string(nil), t.Rows()...)
		for _, row := range a.Rows {
			if ctl := t.Card(CtlID(row)); ctl != nil {
				a.Records = append(a.Records, TableCard{t.Name, project(ctl, q.Fields)})
			}
			for _, col := range cols {
				a.Counts = append(a.Counts, CellCount{Row: row, Col: col, N: len(t.Cell(row, col))})
			}
		}
	case QueryJnote:
		a.Time = &TimeAnswer{Notes: map[string]NoteFact{}}
		for _, id := range q.Source.IDs {
			if n, ok := w.f.Notes[id]; ok {
				a.Time.Notes[id] = n
			}
		}
	case queryGoal:
		a.Time = &TimeAnswer{Goals: map[string]GoalFact{}}
		for _, id := range q.Source.IDs {
			if g, ok := w.f.Goals[id]; ok {
				a.Time.Goals[id] = g
			}
		}
	case queryCut:
		a.Time = &TimeAnswer{Cuts: map[string]CutFact{}}
		for _, id := range q.Source.IDs {
			if c, ok := w.f.Cuts[id]; ok {
				a.Time.Cuts[id] = c
			}
		}
	case queryTick:
		tick := w.f.Tick
		a.Time = &TimeAnswer{Tick: &tick}
	case queryDropping:
		a.Time = &TimeAnswer{Dropping: map[string]string{}}
		for s, op := range w.f.Dropping {
			a.Time.Dropping[s] = op
		}
	default:
		w.t.Fatalf("the twin has no answer for a query of the kind %q", q.Kind)
	}
	return a
}

// timeRuleNamed is the registered rule.
func timeRuleNamed(t testing.TB, name string) Rule {
	t.Helper()
	for _, r := range RuleTable() {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("the rule %q is not registered", name)
	return Rule{}
}

func agendaKeys(keys []string) []AgendaKey {
	ks := make([]AgendaKey, len(keys))
	for i, k := range keys {
		ks[i] = timeKeyOf(k, uint64(i+1))
	}
	return ks
}

// load reads what the rule's Read asks for, through the twin, and loads the
// partial snapshot from the answer: the snapshot the rule plans on.
func (w *timeWorld) load(rp ReadPlan) *Snapshot {
	w.t.Helper()
	if err := rp.Validate(); err != nil {
		w.t.Fatalf("the read plan is refused: %v", err)
	}
	s, err := LoadPartial(rp, w.answer(rp))
	if err != nil {
		w.t.Fatalf("the answer does not load: %v", err)
	}
	return s
}

// unboundedReads are the bounds the planning tests read within: the whole of
// the keys, so that what a plan does is not cut by what one read may return.
// The reads' own tests cut to layer 1's bounds.
var unboundedReads = ReadBounds{Queries: 1 << 20, Records: 1 << 30, RangeIDs: 1 << 30, Bytes: 1 << 40}

// plan is the registered rule planning the keys the way the tick does: its
// Read, the twin's answer to that read alone, LoadPartial, its Plan. The read
// must keep every key (a read cut to the bounds is the tick's to repeat).
func (w *timeWorld) plan(rule string, keys ...string) RulePlan {
	w.t.Helper()
	r := timeRuleNamed(w.t, rule)
	ks := agendaKeys(keys)
	rp, rest := r.Read(ks, unboundedReads, 0)
	if len(rest) != 0 {
		w.t.Fatalf("the read of %s left %v of %v", rule, timeKeyTexts(rest), keys)
	}
	return r.Plan(w.load(rp), ks, w.now)
}

// timeEffect is what applying a plan changed in the world.
type timeEffect struct {
	Moves, Know, Opened, Closed, Unheld, Writes int
	// Refused is why a guard refused the whole plan, and nothing was applied.
	Refused string
}

func (e timeEffect) zero() bool { return e == timeEffect{} }

// planRefusal is why the store would refuse the plan's entries before any
// guard: two changes of one card that disagree (store/engine.go, mergeEntries),
// and a card created twice.
func planRefusal(p RulePlan) string {
	type cardKey struct{ table, id string }
	seen := map[cardKey]ntable.BatchMemberEntry{}
	for _, u := range p.Plan.Units {
		for _, ch := range u.Changes {
			k := cardKey{ch.Table, ch.Entry.ID}
			prev, ok := seen[k]
			if !ok {
				seen[k] = ch.Entry
				continue
			}
			merged, why := mergeTwoEntries(prev, ch.Entry)
			if why != "" {
				return fmt.Sprintf("TWICE: %s %s: %s", ch.Table, ch.Entry.ID, why)
			}
			seen[k] = merged
		}
	}
	return ""
}

// mergeTwoEntries is store/engine.go's mergeEntries: two changes of one card,
// planned on one pre-state, are one entry when they expect the same place and
// revision, move it to the same place, and set no field to two values.
func mergeTwoEntries(a, b ntable.BatchMemberEntry) (ntable.BatchMemberEntry, string) {
	ja, _ := json.Marshal(a.Expect)
	jb, _ := json.Marshal(b.Expect)
	switch {
	case a.Create != nil || b.Create != nil:
		return a, "a card is created once"
	case string(ja) != string(jb):
		return a, "they expect the card at different revisions or places"
	case a.Remove && b.Move != nil || b.Remove && a.Move != nil:
		return a, "one moves it and one takes it off the table"
	}
	out := a
	if b.Move != nil {
		if a.Move != nil {
			mj, _ := json.Marshal(a.Move)
			nj, _ := json.Marshal(b.Move)
			if string(mj) != string(nj) {
				return a, "they move it to different places"
			}
		}
		out.Move = b.Move
	}
	out.Remove = a.Remove || b.Remove
	if len(b.Set) > 0 {
		out.Set = map[string]string{}
		for k, v := range a.Set {
			out.Set[k] = v
		}
		for k, v := range b.Set {
			if x, ok := out.Set[k]; ok && x != v {
				return a, "they set " + k + " to " + x + " and to " + v
			}
			out.Set[k] = v
		}
	}
	out.Unset = append([]string(nil), a.Unset...)
	for _, k := range b.Unset {
		if !contains(out.Unset, k) {
			out.Unset = append(out.Unset, k)
		}
	}
	for _, k := range out.Unset {
		if _, ok := out.Set[k]; ok {
			return a, "one sets " + k + " and one unsets it"
		}
	}
	return out, ""
}

// apply applies a plan to the world as the store would, all or nothing: two
// changes of one card are merged or refused, and every entry's expectation and
// every guard the world can check are checked first.
func (w *timeWorld) apply(p RulePlan) timeEffect {
	w.t.Helper()
	if why := planRefusal(p); why != "" {
		return timeEffect{Refused: why}
	}
	for _, g := range p.Guards {
		switch g.Kind {
		case guardMemberUp:
			if w.s.MemberCtl(g.Member).F("status") != Up {
				return timeEffect{Refused: "XGUARD: member " + g.Member + " is not up"}
			}
		case guardDue:
			// X's due guard: the entry at the score read, or absent (DueAbsent)
			at, ok := w.entry(g.Key)
			if ok != (g.Score != DueAbsent) || ok && at != g.Score {
				return timeEffect{Refused: fmt.Sprintf("XGUARD: %s is at %d (%v), read at %d", g.Key, at, ok, g.Score)}
			}
		case guardHold:
			if strings.HasPrefix(g.Key, "h") {
				held := false
				for _, v := range w.j {
					held = held || v == g.Key
				}
				if !held {
					return timeEffect{Refused: "XGUARD: nothing holds " + g.Key}
				}
			}
		}
	}
	for _, u := range p.Plan.Units {
		for _, ch := range u.Changes {
			if why := w.expect(ch.Table, ch.Entry); why != "" {
				return timeEffect{Refused: why}
			}
		}
	}
	var e timeEffect
	for _, u := range p.Plan.Units {
		for _, ch := range u.Changes {
			if changesCard(ch.Entry) {
				e.Moves++
			}
			w.change(ch.Table, ch.Entry)
		}
	}
	for _, n := range p.Notes {
		if n.Type == "" || len(n.Subjects) == 0 {
			w.t.Errorf("a note request names no type or no subject: %+v", n)
		}
		switch n.Op {
		case requestKnow:
			e.Know++
			if n.Type == NPastDue {
				nf := w.f.Notes[n.Cause]
				nf.Marked = true
				w.f.Notes[n.Cause] = nf
			}
		case requestOpen:
			for _, sub := range n.Subjects {
				k := sub + "|" + n.Type + "|" + n.Cause
				if _, ok := w.j[k]; !ok {
					w.j[k] = "n" + itoa(len(w.j)+1)
					e.Opened++
				}
			}
			if n.Type == typeFallingBehind {
				w.f.Tick.Judged = true
			}
		case requestClose:
			for _, sub := range n.Subjects {
				k := sub + "|" + n.Type + "|" + n.Cause
				if _, ok := w.j[k]; ok {
					delete(w.j, k)
					e.Closed++
				}
			}
			if n.Type == typeFallingBehind {
				w.f.Tick.Judged = false
			}
		case requestUnhold:
			for _, sub := range n.Subjects {
				k := sub + "|" + n.Type + "|" + n.Cause
				if strings.HasPrefix(w.j[k], "h") {
					delete(w.j, k)
					e.Unheld++
				}
			}
			for note, nf := range w.f.Notes {
				if nf.Type == n.Type && nf.Cause == n.Cause {
					var left []string
					for _, s := range nf.Holds {
						if !contains(n.Subjects, s) {
							left = append(left, s)
						}
					}
					nf.Holds = left
					w.f.Notes[note] = nf
				}
			}
		default:
			w.t.Errorf("a note request with the op %q", n.Op)
		}
	}
	tw := p.Sprint
	for _, d := range tw.Due {
		w.due[d.Key] = d.At
		e.Writes++
		switch {
		case strings.HasPrefix(d.Key, entryRemind):
			w.f.Goals[strings.TrimPrefix(d.Key, entryRemind)] = GoalFact{Exists: true, Entry: true, At: d.At}
		case d.Key == entryBehind:
			w.f.Tick.Entry, w.f.Tick.EntryAt = true, d.At
		}
	}
	w.claims += len(tw.Goal)
	e.Writes += len(tw.Goal)
	if c := tw.Clock; c != nil {
		switch {
		case c.DueSince != nil:
			w.clock.DueSinceMs = *c.DueSince
		case c.ClearDueSince:
			w.clock.DueSinceMs = 0
		}
		switch {
		case c.StopRaised != nil:
			w.clock.StopRaisedMs = *c.StopRaised
		case c.ClearStopRaised:
			w.clock.StopRaisedMs = 0
		}
		e.Writes++
	}
	if tw.UnarmBehind {
		w.f.Tick.BehindN = 0
		e.Writes++
	}
	w.parks += len(tw.Park)
	e.Writes += len(tw.Park)
	return e
}

// entry is the due entry of the key as the world holds it: an entry a test or
// a step moved (w.due), else the facts' (a cut's, a goal's remind, behind).
func (w *timeWorld) entry(key string) (int64, bool) {
	if at, ok := w.due[key]; ok {
		return at, true
	}
	switch {
	case strings.HasPrefix(key, entryCut):
		c := w.f.Cuts[strings.TrimPrefix(key, entryCut)]
		return c.At, c.Entry
	case strings.HasPrefix(key, entryRemind):
		g := w.f.Goals[strings.TrimPrefix(key, entryRemind)]
		return g.At, g.Entry
	case key == entryBehind:
		return w.f.Tick.EntryAt, w.f.Tick.Entry
	}
	return 0, false
}

// tickEnd is the loop's tick end on the world (sprintfn.TickEnd, 1.4.2): a
// backlog of zero disarms; one not zero, behind_n unset, arms the entry at R +
// 5 min (unless it stands) and behind_n at the backlog; armed, it is held.
func (w *timeWorld) tickEnd(backlog int) {
	t := &w.f.Tick
	t.Backlog = backlog
	switch {
	case backlog == 0:
		t.BehindN, t.Entry = 0, false
		delete(w.due, entryBehind)
	case t.BehindN == 0:
		if !t.Entry {
			t.Entry, t.EntryAt = true, w.now.R+5*timeMin
		}
		t.BehindN = backlog
	}
}

// expect is why the entry's expectation does not hold in the world, "" when
// it does.
func (w *timeWorld) expect(table string, e ntable.BatchMemberEntry) string {
	c := w.s.T(table).Card(e.ID)
	x := e.Expect
	switch {
	case x == nil:
		return ""
	case x.Absent && c != nil:
		return "EXISTS: " + e.ID
	case x.Absent:
		return ""
	case c == nil:
		return "MISSING: " + e.ID
	case x.Revision != "" && x.Revision != u64(c.Rev):
		return "REVISION: " + e.ID
	case x.Place != nil && (x.Place.Row != c.Row || x.Place.Col != c.Col):
		return "PLACE: " + e.ID
	}
	return ""
}

// change applies one entry.
func (w *timeWorld) change(table string, e ntable.BatchMemberEntry) {
	t := w.s.T(table)
	if e.Create != nil {
		c := &Card{ID: e.ID, Row: e.Create.Row, Col: e.Create.Col, Score: e.Create.Score, Rev: 1, Fields: map[string]string{}}
		for k, v := range e.Set {
			c.Fields[k] = v
		}
		t.Put(c)
		return
	}
	c := t.Card(e.ID)
	if !changesCard(e) {
		return
	}
	if e.Remove {
		c.Col = ""
	}
	if e.Move != nil {
		c.Row, c.Col = e.Move.Row, e.Move.Col
	}
	for k, v := range e.Set {
		c.Fields[k] = v
	}
	for _, k := range e.Unset {
		delete(c.Fields, k)
	}
	c.Rev++
	t.Put(c)
}

// moved is the changes of a plan that change a card, in order.
func moved(p RulePlan) []Change {
	var out []Change
	for _, u := range p.Plan.Units {
		for _, ch := range u.Changes {
			if changesCard(ch.Entry) {
				out = append(out, ch)
			}
		}
	}
	return out
}

// noteReq is the first note request of the op and type, nil when there is
// none.
func noteReq(p RulePlan, op, typ string) *NoteReq {
	for i := range p.Notes {
		if p.Notes[i].Op == op && p.Notes[i].Type == typ {
			return &p.Notes[i]
		}
	}
	return nil
}

func timeKeyTexts(ks []AgendaKey) []string {
	var out []string
	for _, k := range ks {
		out = append(out, keyText(k))
	}
	return out
}

func hasGuard(p RulePlan, g XGuard) bool {
	for _, x := range p.Guards {
		if x == g {
			return true
		}
	}
	return false
}

// silent says a plan asks for nothing: no card guarded or changed, no note, no
// guard, no write to a sprint key; only the keys it removes.
func silent(p RulePlan) bool {
	return len(p.Plan.Units) == 0 && len(p.Intents) == 0 && len(p.Notes) == 0 && len(p.Guards) == 0 &&
		len(p.Requeue) == 0 && len(p.Quarantine) == 0 && len(p.HeldBack) == 0 && p.Sprint.Empty()
}

func want(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func TestUntakenReplacedOnceThenJudged(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Working, nil)
	w.workCard("p1.w1", "m1", Ready, map[string]string{"gen": "3", fieldUntakenR: msText(timeR0), fieldDueUntaken: msText(timeR0 + 15*timeMin)})
	w.now.R = timeR0 + 15*timeMin
	key := "late:untaken:p1.w1"

	// Late for the first time: replaced to the other up member at gen + 1, its
	// untaken time left where it was, its deadline the replacement's R + 15 min.
	p := w.plan("late", key)
	ch := moved(p)
	if len(ch) != 1 || ch[0].Table != Fleet || ch[0].Entry.ID != "p1.w1" {
		t.Fatalf("the plan moves %+v", ch)
	}
	e := ch[0].Entry
	if e.Move == nil || e.Move.Row != "m2" || e.Move.Col != Ready {
		t.Fatalf("not moved to m2 ready: %+v", e.Move)
	}
	want(t, "the fields set", e.Set, map[string]string{"gen": "4", "member": "m2", fieldUntakenReplaced: "1", fieldDueUntaken: msText(w.now.R + 15*timeMin)})
	if _, ok := e.Set[fieldUntakenR]; ok {
		t.Fatalf("the replacement moved untaken_r: %+v", e.Set)
	}
	if e.Expect == nil || e.Expect.Place == nil || e.Expect.Revision != "1" {
		t.Fatalf("the move does not guard the card's place and revision: %+v", e.Expect)
	}
	if n := noteReq(p, requestKnow, NReplacedUntaken); n == nil || !reflect.DeepEqual(n.Subjects, []string{"p1.w1"}) {
		t.Fatalf("no notice of the replacement: %+v", p.Notes)
	}
	if noteReq(p, requestOpen, NWorkLate) != nil {
		t.Fatalf("judged before it was replaced: %+v", p.Notes)
	}
	if !hasGuard(p, XGuard{Kind: guardMemberUp, Member: "m2"}) {
		t.Fatalf("the receiver's memberup is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", timeKeyTexts(p.Done), []string{key})
	if !p.Sprint.Empty() {
		t.Fatalf("writes to sprint keys: %+v", p.Sprint)
	}
	if eff := w.apply(p); eff != (timeEffect{Moves: 1, Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if c := w.card(Fleet, "p1.w1"); c.Row != "m2" || c.Col != Ready || c.Int("gen") != 4 {
		t.Fatalf("the card is %+v", c)
	}

	// Late again, the one replacement made: judged, and the card stays.
	w.now.R += 15 * timeMin
	p = w.plan("late", key)
	if len(moved(p)) != 0 {
		t.Fatalf("a second replacement: %+v", moved(p))
	}
	n := noteReq(p, requestOpen, NWorkLate)
	if n == nil || n.Cause != "untaken" || !reflect.DeepEqual(n.Subjects, []string{"p1.w1"}) {
		t.Fatalf("no judgment of the late card: %+v", p.Notes)
	}
	want(t, "the decisions", n.Decisions, []string{"fleet down m2", "drop p1", "wait"})
	if len(p.Plan.Units) != 1 || p.Plan.Units[0].Changes[0].Entry.Expect == nil {
		t.Fatalf("the judgment does not hold the card at its place and revision: %+v", p.Plan.Units)
	}
	if eff := w.apply(p); eff != (timeEffect{Opened: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if c := w.card(Fleet, "p1.w1"); c.Row != "m2" || c.Int("gen") != 4 {
		t.Fatalf("the judged card moved: %+v", c)
	}
	// J writes one judgment a cause: asking again opens none.
	p = w.plan("late", key)
	if eff := w.apply(p); !eff.zero() {
		t.Fatalf("a second judgment: %+v", eff)
	}

	// With no other member up the card is judged at once: nothing to replace
	// it to, and no advice to take its member down.
	w2 := newTimeWorld(t)
	w2.member("m2", false)
	w2.primary("p1", Working, nil)
	w2.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
	p = w2.plan("late", key)
	n = noteReq(p, requestOpen, NWorkLate)
	if n == nil || len(moved(p)) != 0 || strings.Contains(strings.Join(n.Decisions, ","), "fleet down") {
		t.Fatalf("no other member up: %+v %+v", n, moved(p))
	}
}

func TestUntakenLatePopStillReplaces(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Working, nil)
	due := timeR0
	w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(due)})
	// The pop came 20 minutes late, behind a backlog: untaken_replaced is
	// unset, so the card is replaced first, and judged only at its next
	// deadline, counted from now.
	w.now.R = due + 20*timeMin
	p := w.plan("late", "late:untaken:p1.w1")
	ch := moved(p)
	if len(ch) != 1 || ch[0].Entry.Move == nil || ch[0].Entry.Move.Row != "m2" {
		t.Fatalf("a late pop did not replace: %+v %+v", ch, p.Notes)
	}
	if got := ch[0].Entry.Set[fieldDueUntaken]; got != msText(w.now.R+15*timeMin) {
		t.Fatalf("the new deadline is %s, want the replacement's R + 15 min", got)
	}
	if noteReq(p, requestOpen, NWorkLate) != nil {
		t.Fatalf("judged: %+v", p.Notes)
	}
}

func TestUnfinishedRedealCounts(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Working, nil)
	w.workCard("p1.w1", "m1", Working, map[string]string{"gen": "2", "redeals": "2",
		fieldFirstTakenR: msText(timeR0), fieldDueUnfinished: msText(timeR0 + 2*timeHour)})
	w.now.R = timeR0 + 2*timeHour
	key := "late:unfinished:p1.w1"

	p := w.plan("late", key)
	ch := moved(p)
	if len(ch) != 1 || ch[0].Entry.Move == nil || ch[0].Entry.Move.Row != "m2" || ch[0].Entry.Move.Col != Ready {
		t.Fatalf("not replaced to m2 ready: %+v", ch)
	}
	e := ch[0].Entry
	want(t, "the fields set", e.Set, map[string]string{"gen": "3", "member": "m2", "redeals": "3",
		fieldUntakenR: msText(w.now.R), fieldDueUntaken: msText(w.now.R + 15*timeMin)})
	sort.Strings(e.Unset)
	want(t, "the fields unset", e.Unset, []string{fieldDueUnfinished, fieldFirstTakenR})
	if n := noteReq(p, requestKnow, NReplacedLateWork); n == nil || !strings.Contains(n.Text, "redeal 3 of 5") {
		t.Fatalf("the notice does not count the redeal: %+v", p.Notes)
	}
	w.apply(p)
	if c := w.card(Fleet, "p1.w1"); c.Int("redeals") != 3 || c.F(fieldFirstTakenR) != "" || c.Row != "m2" {
		t.Fatalf("the card after the redeal: %+v", c)
	}

	// At the bound of five: judged with its history, and the card stays,
	// since its worker may still finish.
	w.workCard("p2.w1", "m1", Working, map[string]string{"redeals": "5", fieldDueUnfinished: msText(timeR0)})
	w.primary("p2", Working, nil)
	p = w.plan("late", "late:unfinished:p2.w1")
	n := noteReq(p, requestOpen, NWorkLate)
	if n == nil || n.Cause != "unfinished" || len(moved(p)) != 0 || !strings.Contains(n.Text, "5 of 5") || !strings.Contains(n.Text, "nova-sprint log --card p2") {
		t.Fatalf("at the bound: %+v %+v", n, moved(p))
	}
	want(t, "the decisions", n.Decisions, []string{"fleet down m1", "drop p2", "wait"})

	// With no other member up, likewise, below the bound.
	w.member("m2", false)
	w.workCard("p3.w1", "m1", Working, map[string]string{"redeals": "0", fieldDueUnfinished: msText(timeR0)})
	w.primary("p3", Working, nil)
	p = w.plan("late", "late:unfinished:p3.w1")
	if noteReq(p, requestOpen, NWorkLate) == nil || len(moved(p)) != 0 {
		t.Fatalf("no other member up: %+v %+v", p.Notes, moved(p))
	}
}

func TestLateReadGoesToNewReader(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	// The primary sits at a score its read cards (5) do not: every copy of a card
	// sits at the card's score, so the replacement is created at its primary's.
	const primaryScore = 7
	w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1,p1.r1.r2", "rereads": "0", "head": "abc"}).Score = primaryScore
	w.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldAskedR: msText(timeR0), fieldDueUnbegun: msText(timeR0 + 30*timeMin)})
	w.readCard("p1.r1.r2", "r2", Asked, map[string]string{fieldAskedR: msText(timeR0), fieldDueUnbegun: msText(timeR0 + 30*timeMin)})
	w.now.R = timeR0 + 30*timeMin

	p := w.plan("late", "late:unbegun:p1.r1.r1")
	ch := moved(p)
	if len(ch) != 3 {
		t.Fatalf("the unit is %d changes, want the retirement, the new read and the primary: %+v", len(ch), ch)
	}
	if !ch[0].Entry.Remove || ch[0].Entry.ID != "p1.r1.r1" || ch[0].Entry.Set["retired_by"] != retiredByLate {
		t.Fatalf("the late read card is not retired: %+v", ch[0])
	}
	created := ch[1].Entry
	if created.Create == nil || created.ID != "p1.r1.r3" || created.Create.Row != "r3" || created.Create.Col != Asked {
		t.Fatalf("not asked of the reader that has not read it: %+v", created)
	}
	if created.Create.Score != primaryScore {
		t.Fatalf("the replacement is created at score %v, want its primary's %v (its retired card's is %v): %+v",
			created.Create.Score, float64(primaryScore), w.card(Readers, "p1.r1.r2").Score, created.Create)
	}
	if created.Set[fieldAskedR] != msText(w.now.R) || created.Set[fieldDueUnbegun] != msText(w.now.R+30*timeMin) || created.Set["primary"] != "p1" {
		t.Fatalf("the new read card: %+v", created.Set)
	}
	pr := ch[2].Entry
	if ch[2].Table != Work || pr.ID != "p1" || pr.Set["rereads"] != "1" || pr.Set["rcards"] != "p1.r1.r1,p1.r1.r2,p1.r1.r3" {
		t.Fatalf("the primary is not counted: %+v", ch[2])
	}
	if n := noteReq(p, requestKnow, NReplacedLateRead); n == nil || !reflect.DeepEqual(n.Subjects, []string{"p1.r1.r1"}) {
		t.Fatalf("no notice: %+v", p.Notes)
	}
	if eff := w.apply(p); eff != (timeEffect{Moves: 3, Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if w.card(Readers, "p1.r1.r2").Col != Asked || w.card(Readers, "p1.r1.r1").Placed() {
		t.Fatalf("the reads: r2 %+v, r1 %+v", w.card(Readers, "p1.r1.r2"), w.card(Readers, "p1.r1.r1"))
	}

	// The new read is late too: every reader has read this attempt (the retired
	// card counts), so it is judged, and offers no other reader.
	w.now.R += 30 * timeMin
	p = w.plan("late", "late:unbegun:p1.r1.r3")
	n := noteReq(p, requestOpen, NReadLate)
	if n == nil || n.Cause != "unbegun" || len(moved(p)) != 0 {
		t.Fatalf("no reader left: %+v %+v", n, moved(p))
	}
	want(t, "the decisions", n.Decisions, []string{"drop p1", "wait"})

	// A reader that read this attempt and whose card was retired is not asked
	// again: the primary's rcards keep the retired card, so the read that loads
	// the primary loads it too.
	w4 := newTimeWorld(t)
	w4.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1,p1.r1.r2", "rereads": "1"})
	w4.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
	w4.readCard("p1.r1.r2", "r2", Asked, nil)
	retired := w4.card(Readers, "p1.r1.r2")
	retired.Col = ""
	w4.s.Readers.Put(retired)
	p = w4.plan("late", "late:unbegun:p1.r1.r1")
	ch = moved(p)
	if len(ch) != 3 || ch[1].Entry.ID != "p1.r1.r3" || ch[2].Entry.Set["rcards"] != "p1.r1.r1,p1.r1.r2,p1.r1.r3" {
		t.Fatalf("a retired read did not count as read: %+v", ch)
	}
	// At three rereads it is judged though a reader is free, and offers to ask.
	w2 := newTimeWorld(t)
	w2.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1", "rereads": "3"})
	w2.readCard("p1.r1.r1", "r1", Reading, map[string]string{fieldDueUnreported: msText(timeR0)})
	p = w2.plan("late", "late:unreported:p1.r1.r1")
	n = noteReq(p, requestOpen, NReadLate)
	if n == nil || n.Cause != "unreported" || len(moved(p)) != 0 || !strings.Contains(n.Text, "not reported") {
		t.Fatalf("at the reread bound: %+v %+v", n, moved(p))
	}
	want(t, "the decisions", n.Decisions, []string{"ask --another p1", "drop p1", "wait"})

	// At fifteen read cards no reader is asked.
	var cards []string
	for i := 1; i <= RuleMaxReadCards; i++ {
		cards = append(cards, fmt.Sprintf("p1.r%d.r1", i))
	}
	w3 := newTimeWorld(t)
	w3.primary("p1", Review, map[string]string{"rcards": strings.Join(cards, ","), "rereads": "0"})
	w3.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
	p = w3.plan("late", "late:unbegun:p1.r1.r1")
	if noteReq(p, requestOpen, NReadLate) == nil || len(moved(p)) != 0 {
		t.Fatalf("at fifteen read cards: %+v %+v", p.Notes, moved(p))
	}
}

func TestIdleNoticeOnceUntilLanding(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	span := spanMs(ruleIdleSpan)
	w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0 + span)})
	w.now.R = timeR0 + span
	key := "late:idle:s1"

	p := w.plan("late", key)
	ch := moved(p)
	if len(ch) != 1 || ch[0].Table != Merge || ch[0].Entry.ID != "ctl-s1" || !reflect.DeepEqual(ch[0].Entry.Unset, []string{fieldDueIdle}) || ch[0].Entry.Move != nil {
		t.Fatalf("due_idle is not unset in place: %+v", ch)
	}
	n := noteReq(p, requestKnow, NIdle)
	if n == nil || !reflect.DeepEqual(n.Subjects, []string{StreamSubject("s1")}) || !strings.Contains(n.Text, "stream s1 has landed nothing for 2h0m0s") {
		t.Fatalf("no notice: %+v", p.Notes)
	}
	if eff := w.apply(p); eff != (timeEffect{Moves: 1, Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}

	// Said once: the key delivered again finds no due_idle.
	p = w.plan("late", key)
	if !silent(p) {
		t.Fatalf("said twice: %+v", p)
	}

	// A landing sets due_idle again, and the next lapse is said again.
	w.card(Merge, "ctl-s1").Fields[fieldDueIdle] = msText(w.now.R + span)
	w.now.R += span
	p = w.plan("late", key)
	if noteReq(p, requestKnow, NIdle) == nil {
		t.Fatalf("not said again after a landing: %+v", p)
	}
	// Not yet lapsed: nothing.
	w.card(Merge, "ctl-s1").Fields[fieldDueIdle] = msText(w.now.R + 1)
	p = w.plan("late", key)
	if !silent(p) {
		t.Fatalf("said before the span ran out: %+v", p)
	}
}

func TestCutJudgedWhileStopped(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.now = Now{R: timeR0, Wall: timeWall0, Running: false} // STOPPED
	w.f.Cuts["op-1"] = CutFact{Entry: true, At: timeWall0 - 10*timeSec}
	key := "late:cut:op-1"

	p := w.plan("late", key)
	if len(p.Plan.Units) != 0 {
		t.Fatalf("a cut judgment touches a table while STOPPED: %+v", p.Plan.Units)
	}
	n := noteReq(p, requestOpen, NCutStopped)
	if n == nil || n.Cause != "cut" || !reflect.DeepEqual(n.Subjects, []string{"op-1"}) {
		t.Fatalf("no judgment: %+v", p.Notes)
	}
	if !strings.Contains(strings.Join(n.Decisions, "|"), "the same command with --op op-1") {
		t.Fatalf("the decisions: %v", n.Decisions)
	}
	if !hasGuard(p, entryAsRead("cut:op-1", true, timeWall0-10*timeSec)) {
		t.Fatalf("the cut entry is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", timeKeyTexts(p.Done), []string{key})
	if eff := w.apply(p); eff != (timeEffect{Opened: 1}) {
		t.Fatalf("effect %+v", eff)
	}

	// The clock counts wall time, so the same plan is made RUNNING.
	w.now.Running = true
	if p2 := w.plan("late", key); !reflect.DeepEqual(p2, p) {
		t.Fatalf("RUNNING plans another judgment than STOPPED:\n%+v\n%+v", p2, p)
	}

	// A later part armed the clock again above wall: the op goes on, quiet.
	w.f.Cuts["op-1"] = CutFact{Entry: true, At: timeWall0 + 10*timeMin}
	if p = w.plan("late", key); !silent(p) {
		t.Fatalf("judged an op whose clock was armed again: %+v", p)
	}

	// The same plan applied after that part moved the entry is refused
	// XGUARD, and writes nothing.
	w.f.Cuts["op-1"] = CutFact{}
	pStale := w.plan("late", key)
	w.due["cut:op-1"] = timeWall0 + 10*timeMin
	if eff := w.apply(pStale); !strings.HasPrefix(eff.Refused, "XGUARD") {
		t.Fatalf("a stale cut judgment applied: %+v", eff)
	}
}

func TestHoldExpiryWritesUnheldLine(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.f.Notes["n7"] = NoteFact{Type: NCannotAsk, Cause: "ask", Holds: []string{"p1", "p2"}}
	w.j["p1|"+NCannotAsk+"|ask"] = "hn7"
	w.j["p2|"+NCannotAsk+"|ask"] = "hn7"
	key := "hold:n7"

	p := w.plan("hold", key)
	n := noteReq(p, requestUnhold, NCannotAsk)
	if n == nil || n.Cause != "ask" || !reflect.DeepEqual(n.Subjects, []string{"p1", "p2"}) {
		t.Fatalf("no unheld line naming the type, cause and subjects: %+v", p.Notes)
	}
	if !hasGuard(p, XGuard{Kind: guardHold, Key: "hn7"}) {
		t.Fatalf("the hold is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", timeKeyTexts(p.Done), []string{key})
	if len(p.Plan.Units) != 0 || !p.Sprint.Empty() {
		t.Fatalf("the expiry touches more than the line: %+v %+v", p.Plan.Units, p.Sprint)
	}
	if eff := w.apply(p); eff != (timeEffect{Unheld: 2}) {
		t.Fatalf("effect %+v", eff)
	}
	// A second run finds no hold, and writes nothing.
	p = w.plan("hold", key)
	if !silent(p) {
		t.Fatalf("a second expiry: %+v", p)
	}
	// A note the hold of which the owner rule already ended has none.
	w.f.Notes["n8"] = NoteFact{Type: NCannotAsk, Cause: "ask"}
	if p = w.plan("hold", "hold:n8", "hold:n9"); !silent(p) || len(p.Done) != 2 {
		t.Fatalf("a hold that is gone or a note that is unknown: %+v", p)
	}
}

func TestOverdueMarksOnce(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.f.Notes["n3"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p1", "p2"}}
	w.f.Notes["n4"] = NoteFact{Type: NBound, Cause: "held"} // closed
	w.f.Notes["n5"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p3"}, Marked: true}

	p := w.plan("overdue", "overdue:n3", "overdue:n4", "overdue:n5")
	if len(p.Notes) != 1 {
		t.Fatalf("one open unmarked note wants one line: %+v", p.Notes)
	}
	n := p.Notes[0]
	if n.Op != requestKnow || n.Type != NPastDue || n.Cause != "n3" || !reflect.DeepEqual(n.Subjects, []string{"n3"}) {
		t.Fatalf("the overdue line: %+v", n)
	}
	if !hasGuard(p, XGuard{Kind: guardHold, Key: "overdue:n3"}) {
		t.Fatalf("the mark is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", timeKeyTexts(p.Done), []string{"overdue:n3", "overdue:n4", "overdue:n5"})
	if eff := w.apply(p); eff != (timeEffect{Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if p = w.plan("overdue", "overdue:n3"); !silent(p) {
		t.Fatalf("marked twice: %+v", p)
	}
}

func TestRemindOnePushPerPeriod(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.f.Goals["person-a"] = GoalFact{Exists: true}
	key := "remind:person-a"

	p := w.plan("remind", key)
	want(t, "the entry moved", p.Sprint.Due, []DueSet{{Key: "remind:person-a", At: w.now.R + 5*timeMin}})
	want(t, "the claim", p.Sprint.Goal, []GoalClaim{{Person: "person-a", R: w.now.R}})
	if !hasGuard(p, entryAsRead("remind:person-a", false, 0)) {
		t.Fatalf("the entry is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", timeKeyTexts(p.Done), []string{key})
	if len(p.Plan.Units) != 0 || len(p.Notes) != 0 {
		t.Fatalf("phase 1 writes more than its claim: %+v", p)
	}
	if eff := w.apply(p); eff.Refused != "" || w.claims != 1 || w.due["remind:person-a"] != w.now.R+5*timeMin {
		t.Fatalf("effect %+v, %d claims", eff, w.claims)
	}

	// The key delivered again in the same period, planned from the entry that
	// moved: nothing.
	if p2 := w.plan("remind", key); !silent(p2) {
		t.Fatalf("a second claim in one period: %+v", p2)
	}
	// A second loop's step, planned before the first applied and applied after:
	// refused XGUARD, so only one push.
	if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "XGUARD") || w.claims != 1 {
		t.Fatalf("a second loop's claim applied: %+v, %d claims", eff, w.claims)
	}
	// The next period claims again, once.
	w.now.R += 5 * timeMin
	p = w.plan("remind", key)
	if eff := w.apply(p); eff.Refused != "" || w.claims != 2 {
		t.Fatalf("the next period: %+v, %d claims", eff, w.claims)
	}
	// A goal that was dropped has no entry and no claim.
	if p = w.plan("remind", "remind:person-b"); !silent(p) || len(p.Done) != 1 {
		t.Fatalf("a dropped goal was claimed: %+v", p)
	}
}

func TestBehindArmsAndJudges(t *testing.T) {
	t.Parallel()
	worldWith := func(tick TickFact) *timeWorld {
		w := newTimeWorld(t)
		w.f.Tick = tick
		return w
	}
	// A backlog that shrank since it was armed is armed again with the new
	// backlog: behind_n cleared, for the tick end to arm both; judged nothing.
	w := worldWith(TickFact{Backlog: 400, Agenda: 30, DueNow: 5, BehindN: 500})
	p := w.plan("behind", "behind")
	if len(p.Sprint.Due) != 0 || !p.Sprint.UnarmBehind {
		t.Fatalf("re-armed: %+v", p.Sprint)
	}
	if len(p.Notes) != 0 {
		t.Fatalf("shrinking is only to know: %+v %+v", p.Sprint, p.Notes)
	}
	if !hasGuard(p, entryAsRead("behind", false, 0)) {
		t.Fatalf("the entry is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", timeKeyTexts(p.Done), []string{"behind"})

	// A backlog at least as large as when it was armed is judged, once.
	w = worldWith(TickFact{Backlog: 800, Agenda: 30, DueNow: 5, BehindN: 500})
	p = w.plan("behind", "behind")
	n := noteReq(p, requestOpen, typeFallingBehind)
	if n == nil || !reflect.DeepEqual(n.Subjects, []string{"sprint"}) || !p.Sprint.Empty() ||
		!strings.Contains(n.Text, "800 lines, 30 keys, 5 due for 5 minutes of running time") {
		t.Fatalf("no judgment: %+v", p.Notes)
	}
	want(t, "the decisions", n.Decisions, []string{"wait", "stop", "where"})
	w.now.Running = false
	if p = w.plan("behind", "behind"); strings.Contains(strings.Join(noteReq(p, requestOpen, typeFallingBehind).Decisions, ","), "stop") {
		t.Fatalf("stop offered while STOPPED")
	}
	// An equal backlog counts as not shrunk.
	w = worldWith(TickFact{Backlog: 500, BehindN: 500})
	if p = w.plan("behind", "behind"); noteReq(p, requestOpen, typeFallingBehind) == nil {
		t.Fatalf("an equal backlog is not judged")
	}

	// The backlog gone closes the judgment when it is open, and re-arms nothing.
	w = worldWith(TickFact{Backlog: 0, BehindN: 500, Judged: true})
	p = w.plan("behind", "behind")
	if n = noteReq(p, requestClose, typeFallingBehind); n == nil || !p.Sprint.Empty() {
		t.Fatalf("no close at zero: %+v", p.Notes)
	}
	w = worldWith(TickFact{Backlog: 0, BehindN: 500})
	if p = w.plan("behind", "behind"); !silent(p) {
		t.Fatalf("a backlog of zero with nothing open: %+v", p)
	}

	// Not armed: the tick-end part arms it, not this rule. An entry armed above
	// R is left as it is.
	w = worldWith(TickFact{Backlog: 900})
	if p = w.plan("behind", "behind"); !silent(p) {
		t.Fatalf("armed a rule that was not armed: %+v", p)
	}
	w = worldWith(TickFact{Backlog: 900, BehindN: 500})
	w.f.Tick.Entry, w.f.Tick.EntryAt = true, w.now.R+1
	if p = w.plan("behind", "behind"); !silent(p) {
		t.Fatalf("judged while armed above R: %+v", p)
	}
	// No key, no plan.
	if p = w.plan("behind"); !silent(p) || len(p.Done) != 0 {
		t.Fatalf("a plan without a key: %+v", p)
	}
}

func TestBehindTwiceSecondEmpty(t *testing.T) {
	t.Parallel()
	for _, tick := range []TickFact{
		{Backlog: 800, Agenda: 30, DueNow: 5, BehindN: 500},   // judged
		{Backlog: 400, Agenda: 30, DueNow: 5, BehindN: 500},   // armed again
		{Backlog: 0, BehindN: 500, Judged: true},              // closed
		{Backlog: 800, BehindN: 500, Judged: true},            // judged already
		{Backlog: 800, BehindN: 500, Entry: true, EntryAt: 1}, // armed by another run, in the past: judged
	} {
		w := newTimeWorld(t)
		w.f.Tick = tick
		p := w.plan("behind", "behind")
		if eff := w.apply(p); eff.Refused != "" {
			t.Fatalf("%+v: refused %+v", tick, eff)
		}
		// The same key delivered a second time, nothing changed: the second
		// step is empty.
		p = w.plan("behind", "behind")
		if !silent(p) {
			t.Fatalf("%+v: the second run plans %+v", tick, p)
		}
	}
}

// dryPlan is the plan of a rule that would change the cards, each by one
// entry, and guard one card it changes none of.
func dryPlan(ids ...string) RulePlan {
	var p RulePlan
	for _, id := range ids {
		c := &Card{ID: id, Row: "s1", Col: Waiting, Rev: 1}
		p.Plan.Units = append(p.Plan.Units, Unit{Key: id, Changes: []Change{change(Work, moveEntry(c, "s1", Ready, nil))}})
	}
	return p
}

func TestStoppedCountsFromMovesDue(t *testing.T) {
	t.Parallel()
	const stoppedSince = timeWall0 - 3*timeHour
	guardOnly := &Card{ID: "g", Row: "s1", Col: Waiting, Rev: 1}
	dry := []RulePlan{
		dryPlan("a", "b"),
		dryPlan("b", "c"), // b again: one card, counted once
		{Plan: Plan{Units: []Unit{{Changes: []Change{change(Work, guardEntry(guardOnly))}}}}, Notes: make([]NoteReq, 100)},
	}
	// The backlog is not an input at all: three cards change, and that is
	// what is named.
	c := Clock{StoppedSinceMs: stoppedSince, DueSinceMs: timeWall0 - 11*timeMin}
	p := StoppedLook(dry, StopRead{Clock: c, Wall: timeWall0})
	n := noteReq(p, requestOpen, NStoppedWithDue)
	if n == nil || !strings.Contains(n.Text, "3 moves have been due for 11m0s") || !reflect.DeepEqual(n.Subjects, []string{"sprint"}) {
		t.Fatalf("the judgment: %+v", p.Notes)
	}
	want(t, "the decisions", n.Decisions, []string{"start", "wait --for <duration> --reason <text>"})
	if got := movesDue(dry); got != 3 {
		t.Fatalf("moves due: %d", got)
	}
	for _, k := range []string{"stopped_since_ms", "stophold_ms", "due_since_ms", "stopraised_ms"} {
		found := false
		for _, g := range p.Guards {
			found = found || g.Kind == guardClock && g.Key == k
		}
		if !found {
			t.Fatalf("clock field %s is not guarded: %+v", k, p.Guards)
		}
	}
	if p.Sprint.Clock == nil || p.Sprint.Clock.StopRaised == nil || *p.Sprint.Clock.StopRaised != stoppedSince || p.Sprint.Clock.DueSince != nil {
		t.Fatalf("the clock writes: %+v", p.Sprint.Clock)
	}
}

func TestStoppedNothingDueNoJudgment(t *testing.T) {
	t.Parallel()
	const stoppedSince = timeWall0 - 3*timeHour
	guardOnly := &Card{ID: "g", Row: "s1", Col: Waiting, Rev: 1}
	// Plans that change no card: notes, a guard, a key removed.
	dry := []RulePlan{
		{Notes: make([]NoteReq, 50), Done: []AgendaKey{timeKeyOf("deal", 1)}},
		{Plan: Plan{Units: []Unit{{Changes: []Change{change(Work, guardEntry(guardOnly))}}}}},
	}
	// Nothing was due, and nothing is recorded: the look plans nothing.
	c := Clock{StoppedSinceMs: stoppedSince}
	if p := StoppedLook(dry, StopRead{Clock: c, Wall: timeWall0}); !silent(p) {
		t.Fatalf("a look with nothing due: %+v", p)
	}
	// Moves were due, and are not now: due_since_ms is cleared, and no
	// judgment is raised however long ago it was set.
	c.DueSinceMs = timeWall0 - 3*timeHour
	p := StoppedLook(dry, StopRead{Clock: c, Wall: timeWall0})
	if len(p.Notes) != 0 || p.Sprint.Clock == nil || !p.Sprint.Clock.ClearDueSince || p.Sprint.Clock.StopRaised != nil {
		t.Fatalf("moves gone: %+v %+v", p.Notes, p.Sprint.Clock)
	}
	// No dry plan at all likewise.
	if q := StoppedLook(nil, StopRead{Clock: c, Wall: timeWall0}); len(q.Notes) != 0 {
		t.Fatalf("no plan, a judgment: %+v", q.Notes)
	}
	// A RUNNING machine is not looked at.
	running := []RulePlan{dryPlan("a")}
	if p = StoppedLook(running, StopRead{Clock: Clock{}, Wall: timeWall0}); !silent(p) {
		t.Fatalf("a look at a RUNNING machine: %+v", p)
	}
}

func TestStoppedRaisedOncePerSpan(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	dry := []RulePlan{dryPlan("a")}
	span := StoppedDueSpan.Milliseconds()
	w.clock = Clock{StoppedSinceMs: timeWall0 - timeHour}
	look := func(wall int64) RulePlan { return StoppedLook(dry, StopRead{Clock: w.clock, Wall: wall}) }
	// The first look that finds moves due records when.
	p := look(timeWall0)
	if len(p.Notes) != 0 || p.Sprint.Clock == nil || p.Sprint.Clock.DueSince == nil || *p.Sprint.Clock.DueSince != timeWall0 {
		t.Fatalf("the first look: %+v %+v", p.Notes, p.Sprint.Clock)
	}
	w.apply(p)
	// Ten minutes count from then, not from the stop.
	if p = look(timeWall0 + span - 1); !silent(p) {
		t.Fatalf("raised before ten minutes: %+v", p)
	}
	// The judgment is raised, and this span is marked.
	p = look(timeWall0 + span)
	if noteReq(p, requestOpen, NStoppedWithDue) == nil || p.Sprint.Clock == nil || p.Sprint.Clock.StopRaised == nil {
		t.Fatalf("not raised at ten minutes: %+v %+v", p.Notes, p.Sprint.Clock)
	}
	w.apply(p)
	if w.clock.StopRaisedMs != w.clock.StoppedSinceMs {
		t.Fatalf("the span is not marked: %+v", w.clock)
	}
	// Once a STOPPED span, however long it goes on, and whatever the hold.
	for _, later := range []int64{span, 10 * span, 100 * span} {
		if p = look(timeWall0 + later + 1); !silent(p) {
			t.Fatalf("raised twice in one span at +%d: %+v", later, p)
		}
	}
	// A new span, after a start and a stop, is raised once again.
	w.clock = Clock{StoppedSinceMs: timeWall0 + 200*span, StopRaisedMs: w.clock.StopRaisedMs}
	p = look(timeWall0 + 200*span)
	w.apply(p)
	if p = look(timeWall0 + 201*span); noteReq(p, requestOpen, NStoppedWithDue) == nil {
		t.Fatalf("not raised in a new span: %+v %+v", p.Notes, w.clock)
	}

	// A wait sets stophold_ms: no judgment until it, then one.
	w.clock = Clock{StoppedSinceMs: timeWall0 - timeHour, DueSinceMs: timeWall0 - 2*timeHour, StopHoldMs: timeWall0 + timeHour}
	if p = look(timeWall0); !silent(p) {
		t.Fatalf("raised under a wait: %+v", p)
	}
	if p = look(timeWall0 + timeHour); noteReq(p, requestOpen, NStoppedWithDue) == nil {
		t.Fatalf("not raised when the wait ran out")
	}
}

func TestLimitHalvesThenParks(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"LIMIT", "BUDGET"} {
		key := timeKeyOf("ask@48213", 48213)
		var chunks []int
		h, parkedAt := 0, -1
		for i := 0; i < 40; i++ {
			chunks = append(chunks, chunkAfter(h))
			p, next := OnBug("ask", key, code, "300 entries over the bound of 256", h)
			n := noteReq(p, requestOpen, NStepRefused)
			if n == nil || n.Cause != "refused" || !reflect.DeepEqual(n.Subjects, []string{"ask@48213"}) {
				t.Fatalf("%s at %d halvings: no judgment: %+v", code, h, p.Notes)
			}
			for _, part := range []string{"rule ask", "key ask@48213", "code " + code, "300 entries over the bound of 256"} {
				if !strings.Contains(n.Text, part) {
					t.Fatalf("%s: the judgment does not name %q: %s", code, part, n.Text)
				}
			}
			if chunkAfter(h) > 1 {
				// Planned again at half the size: the key stays, nothing parks.
				if next != h+1 || len(p.Done) != 0 || !reflect.DeepEqual(timeKeyTexts(p.Requeue), []string{"ask@48213"}) || !p.Sprint.Empty() ||
					!strings.Contains(n.Text, "half its size") {
					t.Fatalf("%s at %d halvings: next %d, done %v, requeue %v, writes %+v", code, h, next, p.Done, p.Requeue, p.Sprint)
				}
				h = next
				continue
			}
			// At a chunk of one card: parked, out of the agenda, its park written.
			if next != 0 || !reflect.DeepEqual(timeKeyTexts(p.Done), []string{"ask@48213"}) || len(p.Requeue) != 0 ||
				!reflect.DeepEqual(p.Sprint.Park, []ParkKey{{Key: "ask@48213", Rule: "ask", Code: code}}) || !strings.Contains(n.Text, "parked") {
				t.Fatalf("%s at a chunk of one: next %d, done %v, writes %+v", code, next, p.Done, p.Sprint)
			}
			parkedAt = h
			break
		}
		want(t, code+": the chunk at each refusal", chunks, []int{2000, 1000, 500, 250, 125, 62, 31, 15, 7, 3, 1})
		if parkedAt != 10 {
			t.Fatalf("%s parked after %d halvings, want 10", code, parkedAt)
		}
	}
}

func TestOtherBugParksAtOnce(t *testing.T) {
	t.Parallel()
	// The codes 1.3.5 lists as bugs, LIMIT aside: each parks at the first
	// refusal, the key out of the agenda and the park written, whatever the
	// key has been halved to.
	for _, code := range []string{"REQUEST", "TWICE", "EXISTS", "WRONGTYPE", "NOPERM", "OVERFLOW", "LOGID", "CONFIG", "ENGINE",
		"NOROW", "NOCOL", "FIELDNAME", "FIELDOVERLAP", "ROWCONFLICT", "OCCUPIED", "OPCONFLICT"} {
		for _, h := range []int{0, 3} {
			key := timeKeyOf("deal", 9)
			p, next := OnBug("deal", key, code, "1 entry", h)
			n := noteReq(p, requestOpen, NStepRefused)
			if next != 0 || n == nil || !reflect.DeepEqual(timeKeyTexts(p.Done), []string{"deal"}) || len(p.Requeue) != 0 ||
				!reflect.DeepEqual(p.Sprint.Park, []ParkKey{{Key: "deal", Rule: "deal", Code: code}}) || !strings.Contains(n.Text, "code "+code) || !strings.Contains(n.Text, "parked") {
				t.Fatalf("%s at %d halvings: next %d, %+v", code, h, next, p)
			}
			want(t, code+": the decisions", n.Decisions, []string{"log --since", "ack", "stop", "wait"})
		}
	}
}

func TestTimeRulesTwiceSecondEmpty(t *testing.T) {
	t.Parallel()
	type scenario struct {
		name  string
		rule  string
		keys  []string
		setup func(w *timeWorld)
		// literal says the second plan is empty of everything but its keys; a
		// judgment that stands is asked for again, and J writes it once.
		literal bool
	}
	due := func(kind string) string { return "due_" + kind }
	cases := []scenario{
		{"untaken replaced", "late", []string{"late:untaken:p1.w1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Ready, map[string]string{due("untaken"): msText(timeR0)})
		}, true},
		{"untaken judged", "late", []string{"late:untaken:p1.w1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Ready, map[string]string{due("untaken"): msText(timeR0), fieldUntakenReplaced: "1"})
		}, false},
		{"unfinished replaced", "late", []string{"late:unfinished:p1.w1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Working, map[string]string{due("unfinished"): msText(timeR0)})
		}, true},
		{"unfinished judged", "late", []string{"late:unfinished:p1.w1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Working, map[string]string{due("unfinished"): msText(timeR0), "redeals": "5"})
		}, false},
		{"unbegun replaced", "late", []string{"late:unbegun:p1.r1.r1"}, func(w *timeWorld) {
			w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1"})
			w.readCard("p1.r1.r1", "r1", Asked, map[string]string{due("unbegun"): msText(timeR0)})
		}, true},
		{"unreported judged", "late", []string{"late:unreported:p1.r1.r1"}, func(w *timeWorld) {
			w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1", "rereads": "3"})
			w.readCard("p1.r1.r1", "r1", Reading, map[string]string{due("unreported"): msText(timeR0)})
		}, false},
		{"mergeidle judged", "late", []string{"late:mergeidle:s1"}, func(w *timeWorld) {
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, due("mergeidle"): msText(timeR0)})
		}, false},
		{"idle said", "late", []string{"late:idle:s1"}, func(w *timeWorld) {
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, due("idle"): msText(timeR0)})
		}, true},
		{"cut judged", "late", []string{"late:cut:op-1"}, func(w *timeWorld) {
			w.f.Cuts["op-1"] = CutFact{Entry: true, At: timeWall0 - 1}
		}, false},
		{"overdue marked", "overdue", []string{"overdue:n1"}, func(w *timeWorld) {
			w.f.Notes["n1"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p1"}}
		}, true},
		{"hold expired", "hold", []string{"hold:n1"}, func(w *timeWorld) {
			w.f.Notes["n1"] = NoteFact{Type: NBound, Cause: "held", Holds: []string{"p1"}}
			w.j["p1|"+NBound+"|held"] = "hn1"
		}, true},
		{"remind claimed", "remind", []string{"remind:person-a"}, func(w *timeWorld) {
			w.f.Goals["person-a"] = GoalFact{Exists: true}
		}, true},
		{"behind judged", "behind", []string{"behind"}, func(w *timeWorld) {
			w.f.Tick = TickFact{Backlog: 9, BehindN: 5}
		}, true},
		{"behind armed again", "behind", []string{"behind"}, func(w *timeWorld) {
			w.f.Tick = TickFact{Backlog: 3, BehindN: 5}
		}, true},
		// The states of a second cold read, each run twice.
		{"unfinished redeal 4 to 5", "late", []string{"late:unfinished:p1.w1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Working, map[string]string{due("unfinished"): msText(timeR0), "redeals": "4"})
		}, true},
		{"untaken with three members up", "late", []string{"late:untaken:p1.w1", "late:untaken:p2.w1"}, func(w *timeWorld) {
			w.member("m3", true)
			for _, p := range []string{"p1", "p2"} {
				w.primary(p, Working, nil)
				w.workCard(p+".w1", "m1", Ready, map[string]string{due("untaken"): msText(timeR0)})
			}
		}, true},
		{"mergeidle waiting with a queued card", "late", []string{"late:mergeidle:s1"}, func(w *timeWorld) {
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamWaiting, due("mergeidle"): msText(timeR0)})
			w.put(w.s.Merge, "p1", "s1", Queued, nil)
		}, false},
		{"mergeidle and idle of one stream", "late", []string{"late:mergeidle:s1", "late:idle:s1"}, func(w *timeWorld) {
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, due("mergeidle"): msText(timeR0), due("idle"): msText(timeR0)})
		}, false},
		{"cut whose entry was popped", "late", []string{"late:cut:op-1"}, func(w *timeWorld) {
			w.f.Cuts["op-1"] = CutFact{}
		}, false},
		{"a hold on part of a note's subjects", "hold", []string{"hold:n1"}, func(w *timeWorld) {
			w.f.Notes["n1"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p1", "p2"}, Holds: []string{"p1"}}
			w.j["p1|"+NBound+"|held"] = "hn1"
		}, true},
		{"remind with its entry at R", "remind", []string{"remind:person-a"}, func(w *timeWorld) {
			w.f.Goals["person-a"] = GoalFact{Exists: true, Entry: true, At: timeR0}
		}, true},
		{"behind shrinking to 1 of 2", "behind", []string{"behind"}, func(w *timeWorld) {
			w.f.Tick = TickFact{Backlog: 1, BehindN: 2}
		}, true},
		{"two late reads of one primary, a reader for each", "late", []string{"late:unbegun:p1.r1.r1", "late:unbegun:p1.r1.r2"}, func(w *timeWorld) {
			w.s.Readers.SetRows([]string{"r1", "r2", "r3", "r4"})
			w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1,p1.r1.r2", "head": "abc"})
			w.readCard("p1.r1.r1", "r1", Asked, map[string]string{due("unbegun"): msText(timeR0)})
			w.readCard("p1.r1.r2", "r2", Asked, map[string]string{due("unbegun"): msText(timeR0)})
		}, true},
		{"idle in both key forms", "late", []string{"idle:s1", "late:idle:s1"}, func(w *timeWorld) {
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, due("idle"): msText(timeR0)})
		}, true},
		{"two late reads of one primary, one reader for both", "late", []string{"late:unbegun:p1.r1.r1", "late:unbegun:p1.r1.r2"}, func(w *timeWorld) {
			w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1,p1.r1.r2", "head": "abc"})
			w.readCard("p1.r1.r1", "r1", Asked, map[string]string{due("unbegun"): msText(timeR0)})
			w.readCard("p1.r1.r2", "r2", Asked, map[string]string{due("unbegun"): msText(timeR0)})
		}, false},
	}
	for _, c := range cases {
		w := newTimeWorld(t)
		c.setup(w)
		p := w.plan(c.rule, c.keys...)
		if len(p.Done) != len(c.keys) {
			t.Fatalf("%s: the first run removes %v of %v", c.name, timeKeyTexts(p.Done), c.keys)
		}
		if eff := w.apply(p); eff.zero() || eff.Refused != "" {
			t.Fatalf("%s: the first run changed nothing (or was refused): %+v", c.name, eff)
		}
		p = w.plan(c.rule, c.keys...)
		eff := w.apply(p)
		if !eff.zero() {
			t.Fatalf("%s: the second run changed the world: %+v\n%+v", c.name, eff, p)
		}
		if c.literal && !silent(p) {
			t.Fatalf("%s: the second plan is not empty: %+v", c.name, p)
		}
	}

	// R17's look: run twice, the second look writes nothing.
	dry := []RulePlan{dryPlan("a")}
	for _, c := range []Clock{
		{StoppedSinceMs: timeWall0 - timeHour},                                                  // first look: due_since_ms is recorded
		{StoppedSinceMs: timeWall0 - timeHour, DueSinceMs: timeWall0 - 11*timeMin},              // the judgment is raised
		{StoppedSinceMs: timeWall0 - timeHour, DueSinceMs: timeWall0 - timeHour},                // and the span is marked
		{StoppedSinceMs: timeWall0 - timeHour, DueSinceMs: timeWall0 - timeHour, StopHoldMs: 1}, // held until the past
	} {
		w := newTimeWorld(t)
		w.clock = c
		p := StoppedLook(dry, StopRead{Clock: w.clock, Wall: timeWall0})
		if eff := w.apply(p); eff.zero() {
			t.Fatalf("%+v: the first look changed nothing", c)
		}
		p = StoppedLook(dry, StopRead{Clock: w.clock, Wall: timeWall0})
		if !silent(p) {
			t.Fatalf("%+v: the second look plans %+v", c, p)
		}
	}
}

func TestTimeRulesRegistered(t *testing.T) {
	t.Parallel()
	byName := map[string]Rule{}
	var order []string
	for _, r := range RuleTable() {
		byName[r.Name] = r
		order = append(order, r.Name)
	}
	// 1.4.2's order puts R11, R12, R13, R14 and R18 in a row, at the priorities
	// of RulePriorities.
	var at []int
	for i, name := range []string{"late", "overdue", "hold", "remind", "behind"} {
		r, ok := byName[name]
		if !ok {
			t.Fatalf("rule %s is not registered: %v", name, order)
		}
		if p, _ := PriorityOf(name); r.Priority != 11+i || r.Priority != p || r.MaxSteps != 0 || r.Read == nil || r.Plan == nil {
			t.Fatalf("rule %s: %+v", name, r)
		}
		for j, n := range order {
			if n == name {
				at = append(at, j)
			}
		}
	}
	if !sort.IntsAreSorted(at) {
		t.Fatalf("the table is not in priority order: %v", order)
	}
}

// The registered Plan is the rule's whole plan: the writes to the sprint's own
// keys are in the RulePlan it returns, so a step that carries the plan carries
// them and no key is removed without them (8.0's Rule.Plan returns only a
// RulePlan).
func TestRegisteredPlansCarryTheSprintWrites(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.f.Goals["person-a"] = GoalFact{Exists: true}
	p := w.plan("remind", "remind:person-a")
	want(t, "R14's move and claim", []any{p.Sprint.Due, p.Sprint.Goal},
		[]any{[]DueSet{{Key: "remind:person-a", At: w.now.R + 5*timeMin}}, []GoalClaim{{Person: "person-a", R: w.now.R}}})
	w.f.Tick = TickFact{Backlog: 400, BehindN: 500}
	p = w.plan("behind", "behind")
	if !p.Sprint.UnarmBehind {
		t.Fatalf("R18's re-arm is not in the plan: %+v", p.Sprint)
	}
	// R17 and the parked key have no registered rule: their functions return
	// the plan whole.
	if q := StoppedLook([]RulePlan{dryPlan("a")}, StopRead{Clock: Clock{StoppedSinceMs: 1}, Wall: timeWall0}); q.Sprint.Clock == nil {
		t.Fatalf("R17's clock fields are not in the plan: %+v", q.Sprint)
	}
	if q, _ := OnBug("ask", timeKeyOf("ask@1", 1), "REQUEST", "1 entry", 0); len(q.Sprint.Park) != 1 {
		t.Fatalf("the park is not in the plan: %+v", q.Sprint)
	}
}

func TestLateQuietWhenNotDueOrMovedOn(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Working, nil)
	w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0 + 1)}) // R is a millisecond short
	w.workCard("p2.w1", "m1", Working, map[string]string{fieldDueUntaken: msText(timeR0)})   // took the card since
	w.workCard("p3.w1", "m1", Ready, nil)                                                    // no due time: not in a timed state
	keys := []string{
		"late:untaken:p1.w1", "late:untaken:p2.w1", "late:untaken:p3.w1",
		"late:untaken:gone.w1",          // no such card
		"late:frobnicate:p1.w1",         // no such kind
		"late:untaken", "late:", "late", // not keys of R11
		"late:unfinished:p2.w1", // in working, but not due
	}
	p := w.plan("late", keys...)
	if !silent(p) {
		t.Fatalf("planned for keys that are not late: %+v", p)
	}
	want(t, "every key removed", timeKeyTexts(p.Done), keys)
}

func TestLateHeldBackForADroppingStream(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Working, nil)
	w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
	w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0)})
	w.f.Dropping["s1"] = "op-1"
	w.f.Cuts["op-1"] = CutFact{}
	p := w.plan("late", "late:untaken:p1.w1", "late:idle:s1", "late:cut:op-1")
	// The cards of a stream being dropped are refused DROPPING: their keys stay,
	// held back until the mark clears. A cut clock is the op's, and is judged.
	want(t, "held back", timeKeyTexts(p.HeldBack), []string{"late:untaken:p1.w1", "late:idle:s1"})
	want(t, "removed", timeKeyTexts(p.Done), []string{"late:cut:op-1"})
	if len(moved(p)) != 0 || noteReq(p, requestOpen, NCutStopped) == nil || !p.Sprint.Empty() {
		t.Fatalf("planned work in a dropping stream: %+v", p)
	}
}

func TestLateReplacementsSpreadOverMembers(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.member("m3", true)
	w.workCard("x.w1", "m2", Ready, nil) // m2 has one ready card, m3 none
	for _, id := range []string{"p1", "p2", "p3"} {
		w.primary(id, Working, nil)
		w.workCard(id+".w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
	}
	p := w.plan("late", "late:untaken:p1.w1", "late:untaken:p2.w1", "late:untaken:p3.w1")
	var to []string
	for _, ch := range moved(p) {
		to = append(to, ch.Entry.Move.Row)
	}
	// m3 (0), then m2 and m3 tie at 1 (row order: m2), then m3 again (1 to 2).
	want(t, "receivers", to, []string{"m3", "m2", "m3"})
	if n := noteReq(p, requestKnow, NReplacedUntaken); n == nil || len(n.Subjects) != 3 || len(p.Notes) != 1 {
		t.Fatalf("one notice names the three cards: %+v", p.Notes)
	}
	guards := 0
	for _, g := range p.Guards {
		if g.Kind == guardMemberUp {
			guards++
		}
	}
	if guards != 2 {
		t.Fatalf("one memberup a receiver, %d guards: %+v", guards, p.Guards)
	}
}

func TestMergeIdleJudgedOnlyWhenMergingOrQueued(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		state  string
		queued bool
		due    int64
		judged bool
	}{
		{StreamMerging, false, timeR0, true},
		{StreamWaiting, true, timeR0, true},
		{StreamWaiting, false, timeR0, false},
		{StreamStopped, true, timeR0, false},
		{StreamLanded, false, timeR0, false},
		{StreamMerging, false, timeR0 + 1, false},
	} {
		w := newTimeWorld(t)
		w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": c.state, fieldDueMergeIdle: msText(c.due)})
		if c.queued {
			w.put(w.s.Merge, "p1", "s1", Queued, nil)
		}
		p := w.plan("late", "late:mergeidle:s1")
		n := noteReq(p, requestOpen, NMergeLate)
		if (n != nil) != c.judged {
			t.Fatalf("%+v: judged %v", c, n != nil)
		}
		if n != nil {
			if n.Cause != "mergeidle" || !reflect.DeepEqual(n.Subjects, []string{"stream:s1"}) || len(moved(p)) != 0 || len(p.Plan.Units) != 1 {
				t.Fatalf("%+v: %+v", c, p)
			}
			want(t, "the decisions", n.Decisions, []string{"merge --stream s1", "card", "wait"})
		} else if len(p.Plan.Units) != 0 || len(p.Notes) != 0 {
			t.Fatalf("%+v: planned %+v", c, p)
		}
	}
}

func mkKeys(n int, f func(i int) string) []AgendaKey {
	var ks []AgendaKey
	for i := 0; i < n; i++ {
		ks = append(ks, timeKeyOf(f(i), uint64(i+1)))
	}
	return ks
}

func TestTimeReadsWithinBoundsAndHalve(t *testing.T) {
	t.Parallel()
	b := L1ReadBounds()
	// The keys of a read are cut to what its queries may return: a jnote of a
	// note may name the subjects a line can (MaxAboutIDs), so a note costs
	// 1 + MaxAboutIDs records.
	notes := mkKeys(500, func(i int) string { return fmt.Sprintf("overdue:n%d", i) })
	rp, rest := readOverdue(notes, b, 0)
	kept := len(notes) - len(rest)
	if kept != b.Records/(1+MaxAboutIDs) || len(rp.Sprint) != 1 || len(rp.Sprint[0].Source.IDs) != kept || rp.Sprint[0].Kind != QueryJnote {
		t.Fatalf("kept %d of %d, %+v", kept, len(notes), rp.Sprint)
	}
	if c := QueryCost(rp.Sprint[0]); c.Records != kept*(1+MaxAboutIDs) || c.Records > b.Records {
		t.Fatalf("the read may return %d records, the bound is %d", c.Records, b.Records)
	}
	want(t, "the keys left", timeKeyTexts(rest), timeKeyTexts(notes[kept:]))

	// Each halving halves the keys, down to one, and the rest wait.
	eight := mkKeys(8, func(i int) string { return fmt.Sprintf("hold:n%d", i) })
	var counts []int
	for h := 0; h <= 5; h++ {
		_, left := readHold(eight, ReadBounds{Records: 8 * (1 + MaxAboutIDs), Bytes: MaxReadBytes}, h)
		counts = append(counts, len(eight)-len(left))
		if len(left) > 0 {
			want(t, "the keys left keep their order", timeKeyTexts(left), timeKeyTexts(eight[len(eight)-len(left):]))
		}
	}
	want(t, "keys kept a halving", counts, []int{8, 4, 2, 1, 1, 1})

	// A key that costs more than the read may is still read: it must move.
	_, left := readOverdue(notes[:3], ReadBounds{Records: 1}, 0)
	if len(left) != 2 {
		t.Fatalf("a read that keeps no key: %d left", len(left))
	}

	// R11's read counts the fleet and the dropping marks once: a work card key
	// is 2 records (its card, and its primary, which follows nothing).
	untaken := mkKeys(3, func(i int) string { return fmt.Sprintf("late:untaken:p%d.w1", i) })
	once := QueryCost(SprintQ{Kind: QueryFleet, Fields: memberCtlFields}).Records + QueryCost(SprintQ{Kind: queryDropping, Fields: droppingFields}).Records
	if c := lateRowOf("untaken").Cost; c.Records != 2 {
		t.Fatalf("a work card key costs %d records, want 2", c.Records)
	}
	_, left = readLate(untaken, ReadBounds{Records: once + 4}, 0)
	if len(left) != 1 {
		t.Fatalf("the fleet is counted a key, not once: %d left", len(left))
	}
	// A read card key is 1 + (1 + 15): its card, its primary and the read cards
	// the primary names.
	if c := lateRowOf("unbegun").Cost; c.Records != 1+1+RuleMaxReadCards {
		t.Fatalf("a read card key costs %d records, want %d", c.Records, 1+1+RuleMaxReadCards)
	}

	// What R11 reads: each card kind's card, the primaries of its work cards
	// (no follow) and of its read cards (with rcards), the fleet and the
	// readers, the dropping marks, the stream's control card, a stream's queued
	// count, and the cut set.
	keys := []AgendaKey{
		timeKeyOf("late:untaken:p1.w1", 1), timeKeyOf("late:unfinished:p2.w3", 2), timeKeyOf("late:unbegun:p3.r1.r2", 3),
		timeKeyOf("late:mergeidle:s1", 4), timeKeyOf("late:idle:s2", 5), timeKeyOf("late:cut:op-1", 6), timeKeyOf("late:junk", 7),
	}
	rp, rest = readLate(keys, b, 0)
	if len(rest) != 0 {
		t.Fatalf("left %v", timeKeyTexts(rest))
	}
	got := map[string][]string{}
	for _, q := range rp.Sprint {
		got[q.Kind+"/"+q.Table+"/"+strings.Join(q.Follow, ",")] = q.Source.IDs
		if q.Kind == QueryRelated && len(q.Fields) == 0 {
			t.Fatalf("a read of whole records: %+v", q)
		}
		if q.Kind != QueryRelated && q.Source.Kind != "" && q.Source.Kind != SourceIDs {
			t.Fatalf("a source that is not a list: %+v", q)
		}
	}
	want(t, "the queries", got, map[string][]string{
		"related/fleet/":      {"p1.w1", "p2.w3"},
		"related/work/":       {"p1", "p2"},
		"related/readers/":    {"p3.r1.r2"},
		"related/work/rcards": {"p3"},
		"related/merge/":      {"ctl-s1", "ctl-s2"},
		"fleet//":             nil,
		"readers//":           nil,
		"dropping//":          nil,
		"cut//":               {"op-1"},
	})
	want(t, "the counts", rp.Counts, []CountQ{{Table: Merge, Cells: []string{"s1:" + Queued}}})

	// A read of stream keys and cut keys alone asks for no fleet and no readers:
	// the cut clock is the op's and reads no dropping mark.
	rp, _ = readLate([]AgendaKey{timeKeyOf("late:cut:op-1", 1)}, b, 0)
	if len(rp.Sprint) != 1 || rp.Sprint[0].Kind != queryCut {
		t.Fatalf("a cut key reads more than its entry: %+v", rp.Sprint)
	}

	// The reads of R14 and R18.
	rp, _ = readRemind(mkKeys(2, func(i int) string { return fmt.Sprintf("remind:person-%d", i) }), ReadBounds{Records: 10}, 0)
	want(t, "goals read", rp.Sprint[0].Source.IDs, []string{"person-0", "person-1"})
	if rp, _ = readBehind(mkKeys(1, func(int) string { return "behind" }), ReadBounds{Records: 10}, 0); len(rp.Sprint) != 1 || rp.Sprint[0].Kind != queryTick {
		t.Fatalf("the tick is not read: %+v", rp)
	}
	if rp, rest = readBehind(nil, ReadBounds{Records: 10}, 0); len(rp.Sprint) != 0 || len(rest) != 0 {
		t.Fatalf("a read without keys: %+v", rp)
	}
}

// No read of the time rules is over layer 1's bounds, at any number of keys and
// at any halving: a read refused BUDGET is a bug (1.4.2). The cost is IT05's own
// (ReadPlan.Cost, which counts each query by QueryCost), and the plan is left
// whole by IT05's Split, the two functions a tick sizes a read with: so a query
// left out of a key's cost, and a kind of read the table of query costs does not
// know, show here.
func TestNoTimeReadIsOverTheBounds(t *testing.T) {
	t.Parallel()
	b := L1ReadBounds()
	lateKinds := func(kinds ...string) func(i int) string {
		return func(i int) string {
			k := kinds[i%len(kinds)]
			switch k {
			case "untaken", "unfinished":
				return fmt.Sprintf("late:%s:p%d.w1", k, i)
			case "unbegun", "unreported":
				return fmt.Sprintf("late:%s:p%d.r1.r1", k, i)
			}
			return fmt.Sprintf("late:%s:x%d", k, i)
		}
	}
	for _, c := range []struct {
		name string
		read func([]AgendaKey, ReadBounds, int) (ReadPlan, []AgendaKey)
		key  func(i int) string
	}{
		{"untaken", readLate, lateKinds("untaken")},
		{"unfinished", readLate, lateKinds("unfinished")},
		{"unbegun", readLate, lateKinds("unbegun")},
		{"unreported", readLate, lateKinds("unreported")},
		{"mergeidle", readLate, lateKinds("mergeidle")},
		{"idle", readLate, lateKinds("idle")},
		{"cut", readLate, lateKinds("cut")},
		{"a mix of every kind", readLate, lateKinds("untaken", "unfinished", "unbegun", "unreported", "mergeidle", "idle", "cut")},
		{"overdue", readOverdue, func(i int) string { return fmt.Sprintf("overdue:n%d", i) }},
		{"hold", readHold, func(i int) string { return fmt.Sprintf("hold:n%d", i) }},
		{"remind", readRemind, func(i int) string { return fmt.Sprintf("remind:person-%d", i) }},
		{"behind", readBehind, func(int) string { return "behind" }},
	} {
		keys := mkKeys(3000, c.key)
		for h := 0; h <= 4; h++ {
			rest := keys
			for len(rest) > 0 {
				rp, next := c.read(rest, b, h)
				if len(next) >= len(rest) {
					t.Fatalf("%s at %d halvings: a read that moves no key", c.name, h)
				}
				if err := rp.Validate(); err != nil {
					t.Fatalf("%s at %d halvings: %v", c.name, h, err)
				}
				cost := rp.Cost()
				if cost.Records > b.Records || cost.RangeIDs > b.RangeIDs || cost.Bytes > b.Bytes || rp.Queries() > b.Queries {
					t.Fatalf("%s at %d halvings, %d keys left: the read may return %+v in %d queries, over %+v", c.name, h, len(rest), cost, rp.Queries(), b)
				}
				// A read inside the bounds is one read: a tick that splits it with
				// IT05's Split leaves its queries together, so the plan is loaded
				// whole (the dropping marks with the cards it judges).
				if parts := rp.Split(b); len(parts) != 1 || !reflect.DeepEqual(parts[0], rp) {
					t.Fatalf("%s at %d halvings, %d keys left: Split cuts the read into %d reads", c.name, h, len(rest), len(parts))
				}
				rest = next
			}
		}
	}
	// The fixed queries are counted once, not for each key: a read of many
	// keys of one kind may hold more keys than one read of one.
	rp, rest := readLate(mkKeys(3000, lateKinds("untaken")), b, 0)
	if kept := 3000 - len(rest); kept < 1000 {
		t.Fatalf("a read of work cards keeps %d keys: %+v", kept, rp.Cost())
	}
}

// R14's read costs each remind key what reading its goal costs: the goal record
// and the score of its due entry (goalReadRecords, 2). A bound the keys reach
// cuts the read at the keys that fit, and the read that is kept costs that
// many goals by IT05's Cost. A key costed at nothing keeps them all, and
// TestNoTimeReadIsOverTheBounds, whose 3,000 goals fit layer 1's bounds, does
// not see it.
func TestRemindReadCostsEachKeyItsGoalRecords(t *testing.T) {
	t.Parallel()
	keys := mkKeys(10, func(i int) string { return fmt.Sprintf("remind:person-%d", i) })
	perKey := QueryCost(idsQ(queryGoal, oneKeyID, goalFields))
	if perKey.Records != goalReadRecords {
		t.Fatalf("one goal costs %d records, want %d", perKey.Records, goalReadRecords)
	}
	for _, c := range []struct {
		name string
		b    ReadBounds
		kept int
	}{
		{"records", ReadBounds{Records: 4 * goalReadRecords}, 4},
		{"records, one over", ReadBounds{Records: 4*goalReadRecords + 1}, 4},
		{"records, one short", ReadBounds{Records: 4*goalReadRecords - 1}, 3},
		{"bytes", ReadBounds{Bytes: 3 * perKey.Bytes}, 3},
		{"bytes, one short", ReadBounds{Bytes: 3*perKey.Bytes - 1}, 2},
	} {
		rp, rest := readRemind(keys, c.b, 0)
		kept := len(keys) - len(rest)
		if kept != c.kept {
			t.Fatalf("%s: %d of %d keys kept, want %d", c.name, kept, len(keys), c.kept)
		}
		want(t, c.name+": the keys left keep their order", timeKeyTexts(rest), timeKeyTexts(keys[kept:]))
		if len(rp.Sprint) != 1 || len(rp.Sprint[0].Source.IDs) != kept {
			t.Fatalf("%s: the goals read are %+v, want %d", c.name, rp.Sprint, kept)
		}
		if cost := rp.Cost(); cost.Records != kept*goalReadRecords || cost.Bytes != kept*perKey.Bytes {
			t.Fatalf("%s: the read of %d goals costs %+v, want %d records and %d bytes", c.name, kept, cost, kept*goalReadRecords, kept*perKey.Bytes)
		}
	}
	// Each halving halves the keys kept, down to one.
	var counts []int
	for h := 0; h <= 4; h++ {
		_, left := readRemind(keys, ReadBounds{Records: 8 * goalReadRecords}, h)
		counts = append(counts, len(keys)-len(left))
	}
	want(t, "keys kept a halving", counts, []int{8, 4, 2, 1, 1})
}

// The sprint-key read kinds are in IT05's table of query costs: QueryCost, and
// so ReadPlan.Cost and Split, charge each what it reads and not a whole read
// (the read kind "unknown" is charged a whole one, and the store refuses it).
// The rows the time rules cost their keys with when the package's variables are
// set, before init registers them, are the rows QueryCost answers.
func TestTimeReadKindsAreInTheQueryCosts(t *testing.T) {
	t.Parallel()
	whole := Cost{Records: MaxReadRecords, RangeIDs: MaxReadRangeIDs, Bytes: MaxReadBytes}
	three := []string{"a", "b", "c"}
	for _, c := range []struct {
		q       SprintQ
		records int
	}{
		{idsQ(queryGoal, three, goalFields), 3 * goalReadRecords},
		{SprintQ{Kind: queryTick, Fields: tickFields}, tickReadRecords},
		{idsQ(queryCut, three, cutFields), 3 * cutReadRecords},
		{SprintQ{Kind: queryDropping, Fields: droppingFields}, MaxStreams},
	} {
		if _, ok := queryCosts[c.q.Kind]; !ok {
			t.Errorf("%s has no row in the table of query costs", c.q.Kind)
		}
		got := QueryCost(c.q)
		if got == whole || got.Records != c.records || got.RangeIDs != 0 || got.Bytes != c.records*recordBytes(c.q.Fields) {
			t.Errorf("%s costs %+v, want %d records", c.q.Kind, got, c.records)
		}
		if got != timeQueryCost(c.q) {
			t.Errorf("%s: QueryCost %+v, the cost the rules' own variables are set with %+v", c.q.Kind, got, timeQueryCost(c.q))
		}
	}
	if got := QueryCost(SprintQ{Kind: "unknown"}); got != whole {
		t.Errorf("a kind the table does not know costs %+v, want a whole read", got)
	}
	// The row of a cut key was set before init registered it, and is QueryCost's.
	if got, want := lateRowOf(kindCut).Cost, QueryCost(idsQ(queryCut, oneKeyID, cutFields)); got != want {
		t.Errorf("a cut key costs %+v in lateRows, %+v by QueryCost", got, want)
	}

	// A read of one key of each kind is inside the bounds by IT05's Cost, and
	// Split leaves it one read: the dropping marks stay with the cards they judge.
	b := L1ReadBounds()
	for _, key := range []string{"late:untaken:p1.w1", "late:unfinished:p1.w1", "late:unbegun:p1.r1.r1", "late:unreported:p1.r1.r1",
		"late:mergeidle:s1", "late:idle:s1", "late:cut:op-1"} {
		rp, rest := readLate([]AgendaKey{timeKeyOf(key, 1)}, b, 0)
		if len(rest) != 0 {
			t.Fatalf("%s: left %v", key, timeKeyTexts(rest))
		}
		if cost := rp.Cost(); cost.Records > b.Records || cost.Bytes > b.Bytes {
			t.Errorf("%s: the read costs %+v by IT05's Cost, over %+v", key, cost, b)
		}
		if parts := rp.Split(b); len(parts) != 1 || !reflect.DeepEqual(parts[0], rp) {
			t.Errorf("%s: Split cuts the read into %d reads", key, len(parts))
		}
	}
	for name, rp := range map[string]ReadPlan{
		"remind": mustRead(readRemind, "remind:person-a"), "behind": mustRead(readBehind, "behind"),
	} {
		if cost := rp.Cost(); cost.Records > b.Records || cost.Bytes > b.Bytes {
			t.Errorf("%s: the read costs %+v by IT05's Cost, over %+v", name, cost, b)
		}
		if parts := rp.Split(b); len(parts) != 1 {
			t.Errorf("%s: Split cuts the read into %d reads", name, len(parts))
		}
	}
}

// mustRead is the read a rule plans for one key within layer 1's bounds.
func mustRead(read func([]AgendaKey, ReadBounds, int) (ReadPlan, []AgendaKey), key string) ReadPlan {
	rp, _ := read([]AgendaKey{timeKeyOf(key, 1)}, L1ReadBounds(), 0)
	return rp
}

// A kind that already has a row in a table of query costs is a collision, and
// refused at registration; a kind without one is added.
func TestQueryCostRowsAreRegisteredOnce(t *testing.T) {
	t.Parallel()
	row := func(SprintQ) Cost { return Cost{Records: 1} }
	table := map[string]func(q SprintQ) Cost{QueryFleet: row}
	registerQueryCosts(table, map[string]func(q SprintQ) Cost{"new": row})
	if _, ok := table["new"]; !ok || len(table) != 2 {
		t.Fatalf("the row is not registered: %d rows", len(table))
	}
	got := mustPanic(t, func() { registerQueryCosts(table, map[string]func(q SprintQ) Cost{QueryFleet: row}) })
	if !strings.Contains(got, QueryFleet) {
		t.Fatalf("the refusal does not name the kind: %s", got)
	}
}

// TestTimeRulesNeverReadAClock holds the package to what the design says: time
// comes from Now, never from the wall clock. No file of the package but its
// tests calls time.Now, and the time rules' own file calls none of the calls
// that read or wait on a clock.
func TestTimeRulesNeverReadAClock(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) < 10 {
		t.Fatalf("read %d files of the package: %v", len(files), err)
	}
	banned := map[string]bool{"Now": true, "Since": true, "Until": true, "Sleep": true, "After": true, "AfterFunc": true,
		"Tick": true, "NewTimer": true, "NewTicker": true}
	scanned := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		local := ""
		for _, im := range f.Imports {
			if im.Path.Value == `"time"` {
				local = "time"
				if im.Name != nil {
					local = im.Name.Name
				}
			}
		}
		if local == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
				if sel.Sel.Name == "Now" || name == "rules_time.go" && banned[sel.Sel.Name] {
					t.Errorf("%s reads the clock: time.%s", name, sel.Sel.Name)
				}
			}
			return true
		})
	}
	if scanned < 10 {
		t.Fatalf("scanned %d files", scanned)
	}
}

// TestTimeRulesPlanTheSameFromAnyClock plans one state of every rule (and of
// every kind of R11) over snapshots whose own time differs, and finds the same
// plan: nothing reads it. The wall stamps a late read writes are the wall of
// Now, and the syntax-tree test sees only a call, so this is what holds a rule
// that took a stamp from the snapshot's time (Snapshot.Now, the read's TimeMS).
func TestTimeRulesPlanTheSameFromAnyClock(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		rule  string
		keys  []string
		setup func(w *timeWorld)
	}{
		{"untaken", "late", []string{"late:untaken:p1.w1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
		}},
		{"unfinished", "late", []string{"late:unfinished:p1.w1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Working, map[string]string{fieldDueUnfinished: msText(timeR0)})
		}},
		{"unbegun", "late", []string{"late:unbegun:p1.r1.r1"}, func(w *timeWorld) {
			w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1", "head": "abc"})
			w.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
		}},
		{"unreported", "late", []string{"late:unreported:p1.r1.r1"}, func(w *timeWorld) {
			w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1", "head": "abc"})
			w.readCard("p1.r1.r1", "r1", Reading, map[string]string{fieldDueUnreported: msText(timeR0)})
		}},
		{"mergeidle", "late", []string{"late:mergeidle:s1"}, func(w *timeWorld) {
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueMergeIdle: msText(timeR0)})
		}},
		{"idle", "late", []string{"late:idle:s1"}, func(w *timeWorld) {
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0)})
		}},
		{"cut", "late", []string{"late:cut:op-1"}, func(w *timeWorld) {
			w.f.Cuts["op-1"] = CutFact{Entry: true, At: timeWall0 - 1, Verb: "drop"}
		}},
		{"overdue", "overdue", []string{"overdue:n1"}, func(w *timeWorld) {
			w.f.Notes["n1"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p1"}}
		}},
		{"hold", "hold", []string{"hold:n1"}, func(w *timeWorld) {
			w.f.Notes["n1"] = NoteFact{Type: NBound, Cause: "held", Holds: []string{"p1"}}
			w.j["p1|"+NBound+"|held"] = "hn1"
		}},
		{"remind", "remind", []string{"remind:person-a"}, func(w *timeWorld) {
			w.f.Goals["person-a"] = GoalFact{Exists: true}
		}},
		{"behind", "behind", []string{"behind"}, func(w *timeWorld) {
			w.f.Tick = TickFact{Backlog: 800, Agenda: 30, DueNow: 5, BehindN: 500}
		}},
		{"a mixed run of late kinds", "late", []string{"late:untaken:p1.w1", "late:unbegun:p2.r1.r1", "late:idle:s1"}, func(w *timeWorld) {
			w.primary("p1", Working, nil)
			w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
			w.primary("p2", Review, map[string]string{"rcards": "p2.r1.r1", "head": "abc"})
			w.readCard("p2.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
			w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0)})
		}},
	} {
		var first RulePlan
		for i, ms := range []Decimal{"1790000000123", "1999999999999", "1600000000000"} {
			w := newTimeWorld(t)
			c.setup(w)
			w.timeMS = ms
			p := w.plan(c.rule, c.keys...)
			if silent(p) {
				t.Fatalf("%s: planned nothing, so the comparison holds nothing", c.name)
			}
			// A stamp the plan writes is the wall of Now.
			for _, ch := range moved(p) {
				for _, f := range []string{"retired", "asked"} {
					if v, ok := ch.Entry.Set[f]; ok && v != wallStamp(w.now) {
						t.Fatalf("%s at time %s: %s is %s, the wall of Now is %s", c.name, ms, f, v, wallStamp(w.now))
					}
				}
			}
			if i == 0 {
				first = p
				continue
			}
			if !reflect.DeepEqual(first, p) {
				t.Fatalf("%s: the plan follows the snapshot's own time (%s):\n%+v\n%+v", c.name, ms, first, p)
			}
		}
	}
}

// The two read cards of one primary that are late in one run are one change to
// the primary: R8 asks two readers in one step, so both fall due at the same R
// and pop in one tick. Each is retired and replaced by a distinct reader, the
// primary is set once with its rereads raised by both, and the store, which
// refuses a card created twice and a field set to two values, accepts the plan
// (store/engine.go, mergeEntries).
func TestLateTwoReadsOfOnePrimaryAreOneChange(t *testing.T) {
	t.Parallel()
	setup := func(readers ...string) *timeWorld {
		w := newTimeWorld(t)
		w.s.Readers.SetRows(readers)
		w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1,p1.r1.r2", "rereads": "0", "head": "abc"})
		for _, rd := range []string{"r1", "r2"} {
			w.readCard("p1.r1."+rd, rd, Asked, map[string]string{fieldAskedR: msText(timeR0), fieldDueUnbegun: msText(timeR0 + 30*timeMin)})
		}
		w.now.R = timeR0 + 30*timeMin
		return w
	}
	keys := []string{"late:unbegun:p1.r1.r1", "late:unbegun:p1.r1.r2"}
	primarySets := func(p RulePlan) []Change {
		var out []Change
		for _, ch := range moved(p) {
			if ch.Table == Work {
				out = append(out, ch)
			}
		}
		return out
	}
	creates := func(p RulePlan) (ids, readers []string) {
		for _, ch := range moved(p) {
			if ch.Entry.Create != nil {
				ids, readers = append(ids, ch.Entry.ID), append(readers, ch.Entry.Create.Row)
			}
		}
		return ids, readers
	}

	// With four readers each late read has a reader to go to: r3 and r4.
	w := setup("r1", "r2", "r3", "r4")
	p := w.plan("late", keys...)
	if why := planRefusal(p); why != "" {
		t.Fatalf("the store would refuse the plan: %s", why)
	}
	ids, readers := creates(p)
	want(t, "the read cards created, each once", ids, []string{"p1.r1.r3", "p1.r1.r4"})
	want(t, "the readers asked", readers, []string{"r3", "r4"})
	sets := primarySets(p)
	if len(sets) != 1 || sets[0].Entry.ID != "p1" || sets[0].Entry.Set["rereads"] != "2" ||
		sets[0].Entry.Set["rcards"] != "p1.r1.r1,p1.r1.r2,p1.r1.r3,p1.r1.r4" {
		t.Fatalf("the primary is not set once with both replacements: %+v", sets)
	}
	if e := sets[0].Entry; e.Expect == nil || e.Expect.Revision != "1" || e.Expect.Place == nil {
		t.Fatalf("the primary's set is not guarded at its place and revision: %+v", e.Expect)
	}
	retired := 0
	for _, ch := range moved(p) {
		if ch.Entry.Remove {
			retired++
		}
	}
	if retired != 2 || len(p.Plan.Units) != 1 {
		t.Fatalf("%d retired in %d units, want both in the primary's one", retired, len(p.Plan.Units))
	}
	if eff := w.apply(p); eff != (timeEffect{Moves: 5, Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if pr := w.card(Work, "p1"); pr.Int("rereads") != 2 || pr.F("rcards") != "p1.r1.r1,p1.r1.r2,p1.r1.r3,p1.r1.r4" {
		t.Fatalf("the primary after: %+v", pr)
	}

	// With three, the first goes to r3 and the second has no reader left: it is
	// judged, and offers no other reader; the primary counts the one reread.
	w = setup("r1", "r2", "r3")
	p = w.plan("late", keys...)
	if why := planRefusal(p); why != "" {
		t.Fatalf("the store would refuse the plan: %s", why)
	}
	ids, _ = creates(p)
	want(t, "the read cards created", ids, []string{"p1.r1.r3"})
	sets = primarySets(p)
	if len(sets) != 1 || sets[0].Entry.Set["rereads"] != "1" || sets[0].Entry.Set["rcards"] != "p1.r1.r1,p1.r1.r2,p1.r1.r3" {
		t.Fatalf("the primary: %+v", sets)
	}
	n := noteReq(p, requestOpen, NReadLate)
	if n == nil || !reflect.DeepEqual(n.Subjects, []string{"p1.r1.r2"}) {
		t.Fatalf("the read no reader is left for is not judged: %+v", p.Notes)
	}
	want(t, "the decisions", n.Decisions, []string{"drop p1", "wait"})
	if eff := w.apply(p); eff != (timeEffect{Moves: 3, Know: 1, Opened: 1}) {
		t.Fatalf("effect %+v", eff)
	}

	// The count of rereads is the plan's own as it goes: a primary at two
	// rereads with two late reads replaces one and judges the other at three.
	w = setup("r1", "r2", "r3", "r4")
	w.card(Work, "p1").Fields["rereads"] = "2"
	p = w.plan("late", keys...)
	ids, _ = creates(p)
	want(t, "the read cards created at two rereads", ids, []string{"p1.r1.r3"})
	if n := noteReq(p, requestOpen, NReadLate); n == nil || !strings.Contains(n.Text, "rereads 3 of 3") {
		t.Fatalf("the second is not judged at the bound: %+v", p.Notes)
	}
}

// A key given twice is planned once: one replacement, one create, the key
// removed once.
func TestDuplicateKeysArePlannedOnce(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1", "rereads": "0"})
	w.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
	w.primary("p2", Working, nil)
	w.workCard("p2.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
	p := w.plan("late", "late:unbegun:p1.r1.r1", "late:untaken:p2.w1", "late:unbegun:p1.r1.r1", "late:untaken:p2.w1")
	if why := planRefusal(p); why != "" {
		t.Fatalf("the store would refuse the plan: %s", why)
	}
	creates := 0
	for _, ch := range moved(p) {
		if ch.Entry.Create != nil {
			creates++
		}
	}
	if creates != 1 || len(moved(p)) != 4 {
		t.Fatalf("%d creates and %d changes, want one read card created and 4 changes (retire, create, primary, redeal): %+v", creates, len(moved(p)), moved(p))
	}
	want(t, "keys removed once", timeKeyTexts(p.Done), []string{"late:unbegun:p1.r1.r1", "late:untaken:p2.w1"})

	w2 := newTimeWorld(t)
	w2.f.Notes["n1"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p1"}, Holds: []string{"p1"}}
	w2.f.Goals["person-a"] = GoalFact{Exists: true}
	if p := w2.plan("overdue", "overdue:n1", "overdue:n1"); len(p.Done) != 1 || len(p.Notes) != 1 || len(p.Notes[0].Subjects) != 1 {
		t.Fatalf("overdue twice: %+v", p)
	}
	if p := w2.plan("hold", "hold:n1", "hold:n1"); len(p.Done) != 1 || len(p.Notes) != 1 {
		t.Fatalf("hold twice: %+v", p)
	}
	p = w2.plan("remind", "remind:person-a", "remind:person-a")
	if len(p.Done) != 1 || len(p.Sprint.Due) != 1 || len(p.Sprint.Goal) != 1 {
		t.Fatalf("remind twice: %+v", p)
	}
	w2.f.Tick = TickFact{Backlog: 9, BehindN: 5}
	if p := w2.plan("behind", "behind", "behind"); len(p.Done) != 1 {
		t.Fatalf("behind twice: %+v", p)
	}

	// The two texts of an idle key (the pop's late:idle:<stream> and the
	// idle:<stream> ServingRule sends to R11) are one key: one unset, one
	// notice naming the stream once, and both texts removed.
	idle := func() *timeWorld {
		w := newTimeWorld(t)
		w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0)})
		return w
	}
	w3 := idle()
	p = w3.plan("late", "idle:s1", "late:idle:s1")
	if len(moved(p)) != 1 || len(p.Notes) != 1 || !reflect.DeepEqual(p.Notes[0].Subjects, []string{"stream:s1"}) {
		t.Fatalf("idle in both forms is planned twice: %+v", p)
	}
	want(t, "both texts removed", timeKeyTexts(p.Done), []string{"idle:s1", "late:idle:s1"})
	// Either order, and with another key between them.
	p = idle().plan("late", "late:idle:s1", "late:untaken:gone.w1", "idle:s1")
	if len(moved(p)) != 1 || len(p.Notes) != 1 || len(p.Done) != 3 {
		t.Fatalf("idle in both forms, apart: %+v", p)
	}
	// Held back for a stream being dropped, both texts are held back.
	w4 := idle()
	w4.f.Dropping["s1"] = "op-1"
	p = w4.plan("late", "idle:s1", "late:idle:s1")
	if len(p.Done) != 0 || len(p.Plan.Units) != 0 || len(p.Notes) != 0 {
		t.Fatalf("a dropping stream's idle key was planned: %+v", p)
	}
	want(t, "both texts held back", timeKeyTexts(p.HeldBack), []string{"idle:s1", "late:idle:s1"})
}

// R11 plans a key once by its kind and its id, not by its id alone: the same id
// with two kinds is two keys. A stream whose mergeidle key and idle key are in
// one run is judged late (mergeidle) and told it is idle (idle), each once, in
// either order and with either text of the idle key. A memo keyed by the id
// alone plans the second key of the pair never: its unset and its notice are
// lost, and the stream is not told.
func TestOneIdWithTwoKindsOfKeyIsPlannedForBoth(t *testing.T) {
	t.Parallel()
	for _, keys := range [][]string{
		{"late:mergeidle:s1", "late:idle:s1"},
		{"late:idle:s1", "late:mergeidle:s1"},
		{"late:mergeidle:s1", "idle:s1"},
		{"idle:s1", "late:mergeidle:s1"},
	} {
		label := strings.Join(keys, " + ")
		w := newTimeWorld(t)
		w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueMergeIdle: msText(timeR0), fieldDueIdle: msText(timeR0)})
		p := w.plan("late", keys...)
		if why := planRefusal(p); why != "" {
			t.Fatalf("%s: the store would refuse the plan: %s", label, why)
		}
		if len(p.Notes) != 2 {
			t.Fatalf("%s: %d notes, want the judgment and the notice: %+v", label, len(p.Notes), p.Notes)
		}
		n := noteReq(p, requestOpen, NMergeLate)
		if n == nil || n.Cause != "mergeidle" || !reflect.DeepEqual(n.Subjects, []string{StreamSubject("s1")}) {
			t.Fatalf("%s: mergeidle is not judged: %+v", label, p.Notes)
		}
		ch := moved(p)
		if len(ch) != 1 || ch[0].Table != Merge || ch[0].Entry.ID != "ctl-s1" || !reflect.DeepEqual(ch[0].Entry.Unset, []string{fieldDueIdle}) {
			t.Fatalf("%s: idle's due_idle is not unset: %+v", label, ch)
		}
		k := noteReq(p, requestKnow, NIdle)
		if k == nil || !reflect.DeepEqual(k.Subjects, []string{StreamSubject("s1")}) {
			t.Fatalf("%s: idle is not said: %+v", label, p.Notes)
		}
		want(t, label+": both keys removed", timeKeyTexts(p.Done), keys)
		if eff := w.apply(p); eff != (timeEffect{Moves: 1, Know: 1, Opened: 1}) {
			t.Fatalf("%s: effect %+v, want one move, one notice and one judgment", label, eff)
		}
		if _, ok := w.card(Merge, "ctl-s1").Fields[fieldDueIdle]; ok {
			t.Fatalf("%s: due_idle is still set after the plan", label)
		}
	}
}

// R17's look counts the cards the dry plans' intents change as well as those
// their entries change: R4 plans only intents (needmet lowers the open count of
// each waiter), so a STOPPED machine whose only due work is a landing's waiters
// must be named (1.3.3, 2.3 R17).
func TestStoppedLookCountsTheCardsIntentsChange(t *testing.T) {
	t.Parallel()
	const stoppedSince = timeWall0 - 3*timeHour
	needmet := RulePlan{Intents: []Intent{{Kind: "needmet", Need: "n1", Waiters: []string{"w1", "w2"}}}}
	due := Clock{StoppedSinceMs: stoppedSince, DueSinceMs: timeWall0 - 11*timeMin}

	// Two waiters, and nothing else: moves are due, and named.
	p := StoppedLook([]RulePlan{needmet}, StopRead{Clock: due, Wall: timeWall0})
	n := noteReq(p, requestOpen, NStoppedWithDue)
	if n == nil || !strings.Contains(n.Text, "2 moves have been due") || p.Sprint.Clock == nil || p.Sprint.Clock.ClearDueSince {
		t.Fatalf("an intent-only dry plan is not named: %+v", p)
	}
	// The first look records when.
	p = StoppedLook([]RulePlan{needmet}, StopRead{Clock: Clock{StoppedSinceMs: stoppedSince}, Wall: timeWall0})
	if p.Sprint.Clock == nil || p.Sprint.Clock.DueSince == nil || *p.Sprint.Clock.DueSince != timeWall0 || p.Sprint.Clock.ClearDueSince {
		t.Fatalf("due_since_ms is not recorded for an intent-only dry plan: %+v", p.Sprint.Clock)
	}
	// A card an entry and an intent both change is counted once, and so is one
	// two intents name.
	both := []RulePlan{dryPlan("w1", "w3"), needmet, {Intents: []Intent{{Kind: "waitfor", Card: "w3", Needs: []string{"n2"}}, {Kind: "waive", Card: "w4", Needs: []string{"n1"}}}}}
	if got := movesDue(both); got != 4 { // w1, w2, w3, w4
		t.Fatalf("moves due: %d, want 4", got)
	}
	// needgone opens a judgment on each waiter and changes no card.
	gone := RulePlan{Intents: []Intent{{Kind: "needgone", Need: "n1", Waiters: []string{"w1", "w2"}}}}
	if got := movesDue([]RulePlan{gone}); got != 0 {
		t.Fatalf("needgone counted %d cards", got)
	}
	p = StoppedLook([]RulePlan{gone}, StopRead{Clock: due, Wall: timeWall0})
	if p.Sprint.Clock == nil || !p.Sprint.Clock.ClearDueSince || len(p.Notes) != 0 {
		t.Fatalf("a dry plan that changes no card is not cleared: %+v", p)
	}
}

// The cut clock is due when the wall time is at least the entry's score, and a
// deadline is due when R is at least it: the boundary itself is due. The rule
// and the predicates it exports for the holder table (IT11) say the same.
func TestDueBoundaryIsInclusive(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		at  int64
		due bool
	}{{timeWall0 - 1, true}, {timeWall0, true}, {timeWall0 + 1, false}} {
		if got := CutDue(Now{Wall: timeWall0}, c.at); got != c.due {
			t.Errorf("CutDue at wall %d, entry %d: %v", timeWall0, c.at, got)
		}
		w := newTimeWorld(t)
		w.f.Cuts["op-1"] = CutFact{Entry: true, At: c.at}
		p := w.plan("late", "late:cut:op-1")
		if judged := noteReq(p, requestOpen, NCutStopped) != nil; judged != c.due {
			t.Errorf("a cut entry at %d with wall %d: judged %v, want %v", c.at, timeWall0, judged, c.due)
		}
	}
	for _, c := range []struct {
		due  int64
		late bool
	}{{timeR0 - 1, true}, {timeR0, true}, {timeR0 + 1, false}} {
		if got := LateDue(Now{R: timeR0}, c.due); got != c.late {
			t.Errorf("LateDue at R %d, due %d: %v", timeR0, c.due, got)
		}
		w := newTimeWorld(t)
		w.primary("p1", Working, nil)
		w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(c.due)})
		p := w.plan("late", "late:untaken:p1.w1")
		if replaced := len(moved(p)) == 1; replaced != c.late {
			t.Errorf("a work card due at %d with R %d: replaced %v, want %v", c.due, timeR0, replaced, c.late)
		}
	}
}

// The idle notice unsets due_idle in place, guarded at the control card's place
// and revision: a landing between read and apply sets due_idle again, and the
// unset must not erase it.
func TestIdleUnsetIsGuardedByTheControlCard(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0)})
	p := w.plan("late", "late:idle:s1")
	ch := moved(p)
	if len(ch) != 1 {
		t.Fatalf("the plan: %+v", p.Plan.Units)
	}
	x := ch[0].Entry.Expect
	if x == nil || x.Revision != "1" || x.Place == nil || x.Place.Row != "s1" || x.Place.Col != Ctl {
		t.Fatalf("the unset is not guarded at the control card's place and revision: %+v", x)
	}
	// A landing set due_idle again since: refused, and it stays set.
	c := w.card(Merge, "ctl-s1")
	c.Fields[fieldDueIdle] = msText(timeR0 + spanMs(ruleIdleSpan))
	c.Rev++
	w.s.Merge.Put(c)
	if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "REVISION") || w.card(Merge, "ctl-s1").F(fieldDueIdle) == "" {
		t.Fatalf("the unset applied over a landing: %+v", eff)
	}
}

// The replacement read card is asked at the primary's head, and the retirement
// and the primary's count are guarded at the places and revisions they were
// read at: a read begun or reported between read and apply is not retired, and
// a change to the primary is not overwritten.
func TestLateReadIsGuardedWhereItIsRead(t *testing.T) {
	t.Parallel()
	setup := func() (*timeWorld, RulePlan) {
		w := newTimeWorld(t)
		w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1,p1.r1.r2", "rereads": "0", "head": "abc123"})
		w.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
		w.readCard("p1.r1.r2", "r2", Asked, map[string]string{fieldDueUnbegun: msText(timeR0 + 30*timeMin)})
		return w, w.plan("late", "late:unbegun:p1.r1.r1")
	}
	w, p := setup()
	ch := moved(p)
	if len(ch) != 3 {
		t.Fatalf("the unit is %d changes: %+v", len(ch), ch)
	}
	// The new read card carries the head the read is of.
	created := ch[1].Entry
	if created.Create == nil || created.Set["head"] != "abc123" || created.Set["kind"] != "read" || created.Set["reader"] != "r3" ||
		created.Set["attempt"] != "1" || created.Set["stream"] != "s1" {
		t.Fatalf("the replacement read card: %+v", created)
	}
	// The retirement holds the late card at its place and revision.
	x := ch[0].Entry.Expect
	if x == nil || x.Revision != "1" || x.Place == nil || x.Place.Row != "r1" || x.Place.Col != Asked {
		t.Fatalf("the retirement is not guarded: %+v", x)
	}
	// The primary's set holds it at its place and revision.
	x = ch[2].Entry.Expect
	if x == nil || x.Revision != "1" || x.Place == nil || x.Place.Row != "s1" || x.Place.Col != Review {
		t.Fatalf("the primary's set is not guarded: %+v", x)
	}

	// The reader began the read between read and apply: the card is in reading
	// (a place the card was not read at), and nothing is retired, created or
	// counted.
	c := w.card(Readers, "p1.r1.r1")
	c.Col = Reading
	w.s.Readers.Put(c)
	if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "PLACE") || w.card(Readers, "p1.r1.r3") != nil || w.card(Work, "p1").Int("rereads") != 0 {
		t.Fatalf("a read begun since was retired: %+v", eff)
	}
	// Or the card changed where it stands (a revision it was not read at).
	c.Col = Asked
	c.Rev++
	w.s.Readers.Put(c)
	if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "REVISION") || w.card(Readers, "p1.r1.r3") != nil {
		t.Fatalf("a read changed since was retired: %+v", eff)
	}

	// The primary was changed between read and apply: refused, and nothing is
	// written.
	w, p = setup()
	pr := w.card(Work, "p1")
	pr.Fields["head"] = "def456"
	pr.Rev++
	w.s.Work.Put(pr)
	if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "REVISION") || w.card(Readers, "p1.r1.r3") != nil || w.card(Readers, "p1.r1.r1").Placed() == false {
		t.Fatalf("a change to the primary was overwritten: %+v", eff)
	}
}

// A read card judged late is held at its place and revision (2.3 R11, guard),
// whether the judgment is for the reread bound or for want of a reader: a reader
// that began or reported it between read and apply has moved the card, and a
// judgment that it is past its deadline must not open for a card that is not
// late any more. The work cards' and the merge control card's guards are checked
// where those are judged (TestUntakenReplacedOnceThenJudged, the mergeidle
// test).
func TestLateReadJudgmentHoldsTheCardWhereItIsRead(t *testing.T) {
	t.Parallel()
	for _, k := range []struct {
		kind, col, due, movesTo string
	}{
		{"unbegun", Asked, fieldDueUnbegun, Reading},
		{"unreported", Reading, fieldDueUnreported, OK},
	} {
		for _, why := range []struct{ name, rcards, rereads string }{
			{"at the reread bound", "p1.r1.r1", "3"},
			{"with no reader left", "p1.r1.r1,p1.r1.r2,p1.r1.r3", "0"},
		} {
			name := k.kind + " " + why.name
			setup := func() (*timeWorld, RulePlan) {
				w := newTimeWorld(t)
				w.primary("p1", Review, map[string]string{"rcards": why.rcards, "rereads": why.rereads, "head": "abc"})
				w.readCard("p1.r1.r1", "r1", k.col, map[string]string{k.due: msText(timeR0)})
				return w, w.plan("late", "late:"+k.kind+":p1.r1.r1")
			}
			w, p := setup()
			if noteReq(p, requestOpen, NReadLate) == nil || len(moved(p)) != 0 || len(p.Plan.Units) != 1 {
				t.Fatalf("%s: not judged by one unit that changes no card: %+v", name, p)
			}
			u := p.Plan.Units[0]
			if u.Key != "p1.r1.r1" || u.Stream != "s1" || len(u.Changes) != 1 || u.Changes[0].Table != Readers || u.Changes[0].Entry.ID != "p1.r1.r1" {
				t.Fatalf("%s: the unit does not hold the read card: %+v", name, u)
			}
			x := u.Changes[0].Entry.Expect
			if x == nil || x.Revision != "1" || x.Place == nil || x.Place.Row != "r1" || x.Place.Col != k.col {
				t.Fatalf("%s: the read card is not held at its place and revision: %+v", name, x)
			}
			// Nothing moved it: the judgment opens.
			if eff := w.apply(p); eff != (timeEffect{Opened: 1}) {
				t.Fatalf("%s: effect %+v", name, eff)
			}

			// The reader moved it on between read and apply (the place it was not
			// read at): refused, and no judgment opens.
			w, p = setup()
			c := w.card(Readers, "p1.r1.r1")
			c.Col = k.movesTo
			w.s.Readers.Put(c)
			if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "PLACE") || len(w.j) != 0 {
				t.Fatalf("%s: a read that moved on was judged late: %+v %v", name, eff, w.j)
			}
			// Or the card changed where it stands (a revision it was not read at).
			w, p = setup()
			c = w.card(Readers, "p1.r1.r1")
			c.Rev++
			w.s.Readers.Put(c)
			if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "REVISION") || len(w.j) != 0 {
				t.Fatalf("%s: a read that changed was judged late: %+v %v", name, eff, w.j)
			}
		}
	}
}

// `ask --another` is offered on a late read only where the verb accepts it: the
// primary has fewer than fifteen read cards (F1-26) and a reader that has not
// read the attempt.
func TestAskAnotherIsOfferedOnlyWhereItIsAccepted(t *testing.T) {
	t.Parallel()
	readersTo := func(n int) []string {
		var rows []string
		for i := 1; i <= n; i++ {
			rows = append(rows, fmt.Sprintf("r%d", i))
		}
		return rows
	}
	cardsOf := func(n int) string {
		var cards []string
		for i := 1; i <= n; i++ {
			cards = append(cards, fmt.Sprintf("p1.r1.r%d", i))
		}
		return strings.Join(cards, ",")
	}
	judged := func(readers, cards int) []string {
		w := newTimeWorld(t)
		w.s.Readers.SetRows(readersTo(readers))
		w.primary("p1", Review, map[string]string{"rcards": cardsOf(cards), "rereads": "0"})
		w.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
		p := w.plan("late", "late:unbegun:p1.r1.r1")
		n := noteReq(p, requestOpen, NReadLate)
		if n == nil || len(moved(p)) != 0 {
			t.Fatalf("%d cards, %d readers: %+v %+v", cards, readers, n, moved(p))
		}
		return n.Decisions
	}
	// Fifteen read cards, and a sixteenth reader free: the verb refuses.
	want(t, "at fifteen read cards", judged(16, 15), []string{"drop p1", "wait"})
	// Fourteen, and a fifteenth free: the reread bound is what judges; another
	// reader is asked by the coordinator.
	w := newTimeWorld(t)
	w.s.Readers.SetRows(readersTo(15))
	w.primary("p1", Review, map[string]string{"rcards": cardsOf(14), "rereads": "3"})
	w.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
	n := noteReq(w.plan("late", "late:unbegun:p1.r1.r1"), requestOpen, NReadLate)
	if n == nil {
		t.Fatal("not judged at the reread bound")
	}
	want(t, "at fourteen read cards with a reader free", n.Decisions, []string{"ask --another p1", "drop p1", "wait"})
	// Fourteen, and every reader has read the attempt: nobody to ask.
	want(t, "with no reader free", judged(14, 14), []string{"drop p1", "wait"})
}

// R18's judgment and its close are guarded on the `behind` entry like its
// re-arm: a step planned before another loop's and applied after is refused
// XGUARD.
// R18 armed at a backlog of 1000 that fires on 999 arms again "with the new
// backlog" (2.3): the tick end after its step records 999, so a machine that
// caught up by one line and then stalled is judged at the next fire. With
// behind_n left at 1000 it would arm again at every fire and never judge.
func TestBehindReArmsWithTheNewBacklog(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.tickEnd(1000)
	fire := func(backlog int) RulePlan {
		w.now.R += 5 * timeMin
		w.f.Tick.Entry, w.f.Tick.Backlog = false, backlog // the pop takes the entry
		p := w.plan("behind", "behind")
		if eff := w.apply(p); eff.Refused != "" {
			t.Fatalf("refused: %+v", eff)
		}
		return p
	}
	if p := fire(999); !p.Sprint.UnarmBehind || len(p.Notes) != 0 {
		t.Fatalf("the first fire, on 999: %+v %+v", p.Sprint, p.Notes)
	}
	if w.f.Tick.BehindN != 0 {
		t.Fatalf("behind_n after the re-arm: %d", w.f.Tick.BehindN)
	}
	w.tickEnd(999)
	if w.f.Tick.BehindN != 999 || !w.f.Tick.Entry {
		t.Fatalf("the tick end armed %+v", w.f.Tick)
	}
	if p := fire(999); noteReq(p, requestOpen, typeFallingBehind) == nil {
		t.Fatalf("the second fire, on 999, was not judged: %+v %+v", p.Sprint, p.Notes)
	}
}

func TestBehindEveryEffectIsGuarded(t *testing.T) {
	t.Parallel()
	for name, tick := range map[string]TickFact{
		"judged":   {Backlog: 800, Agenda: 30, DueNow: 5, BehindN: 500},
		"closed":   {Backlog: 0, BehindN: 500, Judged: true},
		"re-armed": {Backlog: 400, BehindN: 500},
	} {
		w := newTimeWorld(t)
		w.f.Tick = tick
		p := w.plan("behind", "behind")
		if !hasGuard(p, entryAsRead("behind", false, 0)) {
			t.Fatalf("%s: the entry is not guarded: %+v", name, p.Guards)
		}
		// Another loop armed it again after the read: refused, nothing written.
		w.due["behind"] = w.now.R + 5*timeMin
		if eff := w.apply(p); !strings.HasPrefix(eff.Refused, "XGUARD") {
			t.Fatalf("%s: a stale plan applied: %+v", name, eff)
		}
	}
}

// A verb in parts that stopped offers the decisions its verb accepts (2.2): its
// abort for a drop or a remove, and `ack` for the others; a verb the read does
// not know offers them all, for IT06 to filter.
func TestCutOffersWhatItsVerbAccepts(t *testing.T) {
	t.Parallel()
	same := "the same command with --op op-1"
	for verb, decisions := range map[string][]string{
		"drop":   {same, "drop --abort --op op-1", "wait"},
		"remove": {same, "remove --abort --op op-1", "wait"},
		"add":    {same, "ack", "wait"},
		"":       {same, "drop --abort --op op-1", "remove --abort --op op-1", "ack", "wait"},
	} {
		w := newTimeWorld(t)
		w.f.Cuts["op-1"] = CutFact{Entry: true, At: timeWall0 - 1, Verb: verb}
		n := noteReq(w.plan("late", "late:cut:op-1"), requestOpen, NCutStopped)
		if n == nil {
			t.Fatalf("%q: not judged", verb)
		}
		want(t, "the decisions of "+verb, n.Decisions, decisions)
	}
}

// The keys each rule serves are the keys ServingRule sends it, and a key of
// R11's shape that ServingRule names (idle:<stream>) is planned as the pop's
// late:idle:<stream>.
func TestTimeKeysAreServedByTheirRule(t *testing.T) {
	t.Parallel()
	for rule, keys := range map[string][]string{
		"late":    {"late:untaken:p1.w1", "late:unfinished:p1.w1", "late:unbegun:p1.r1.r1", "late:unreported:p1.r1.r1", "late:mergeidle:s1", "late:idle:s1", "idle:s1", "late:cut:op-1"},
		"overdue": {"overdue:n1"},
		"hold":    {"hold:n1"},
		"remind":  {"remind:person-a"},
		"behind":  {"behind"},
	} {
		for _, k := range keys {
			if got := ServingRule(k); got != rule {
				t.Errorf("ServingRule(%q) = %q, want %q", k, got, rule)
			}
		}
		if _, ok := PriorityOf(rule); !ok {
			t.Errorf("the rule %s has no priority", rule)
		}
		timeRuleNamed(t, rule)
	}
	w := newTimeWorld(t)
	w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0)})
	a, b := w.plan("late", "idle:s1"), w.plan("late", "late:idle:s1")
	if silent(a) || !reflect.DeepEqual(a.Plan, b.Plan) || !reflect.DeepEqual(a.Notes, b.Notes) {
		t.Fatalf("idle:s1 is not planned as late:idle:s1:\n%+v\n%+v", a, b)
	}
	if !silent(w.plan("late", "idle:", "late:idle:")) {
		t.Fatal("a key naming no stream planned work")
	}
}

// The numbers of the time rules are the design's own, named for these rules;
// where today's machine has another they differ, and each difference is a
// question for the switch (IT23) that the pull request lists.
func TestRuleNumbersAreTheDesigns(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		got, design  any
		today        any // today's machine's number, nil where it has none
		differsToday bool
	}{
		{"RuleMaxRedeals (2.3 R2, R11)", RuleMaxRedeals, 5, MaxRedeals, true},
		{"RuleMaxRereads (2.3 R11)", RuleMaxRereads, 3, nil, false},
		{"RuleMaxReadCards (3, F1-26)", RuleMaxReadCards, 15, nil, false},
		{"RuleDeadlineUntaken (1.2)", RuleDeadlineUntaken, 15 * timeMinD, DeadlineUntaken, false},
		{"RuleDeadlineUnfinished (1.2)", RuleDeadlineUnfinished, 2 * time.Hour, DeadlineUnfinished, false},
		{"RuleDeadlineUnbegun (1.2)", RuleDeadlineUnbegun, 30 * timeMinD, DeadlineUnbegun, false},
		{"RuleDeadlineMergeIdle (1.2)", RuleDeadlineMergeIdle, 30 * timeMinD, DeadlineMergeIdle, false},
		{"RuleRemindEvery (1.2)", RuleRemindEvery, 5 * timeMinD, RemindEvery, false},
		{"BehindSpan (1.2, R18)", BehindSpan, 5 * timeMinD, nil, false},
		{"StoppedDueSpan (2.3 R17)", StoppedDueSpan, 10 * timeMinD, nil, false},
	} {
		if !reflect.DeepEqual(c.got, c.design) {
			t.Errorf("%s is %v, the design says %v", c.name, c.got, c.design)
		}
		if c.today != nil && (c.differsToday) == reflect.DeepEqual(c.today, c.got) {
			t.Errorf("%s is %v and today's machine has %v: a difference between them is a question for the switch, listed in the pull request, and a change to either changes the list", c.name, c.got, c.today)
		}
	}
	// Today's machine keeps its own.
	if MaxRedeals != 3 {
		t.Errorf("sprint.MaxRedeals is %d: today's machine keeps 3", MaxRedeals)
	}
}

// A time rule that reads a fact of the sprint its plan did not ask for is
// refused as any read of what was not loaded (1.5.2): it panics in a test.
func TestTimeFactsAreHeldToTheRead(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.f.Notes["n1"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p1"}}
	w.f.Goals["person-a"] = GoalFact{Exists: true}
	w.f.Cuts["op-1"] = CutFact{Entry: true, At: 1}
	w.f.Tick = TickFact{Backlog: 3}
	w.f.Dropping["s1"] = "op-1"
	s := w.load(ReadPlan{Sprint: []SprintQ{
		idsQ(QueryJnote, []string{"n1"}, noteFields), idsQ(queryGoal, []string{"person-a"}, goalFields),
		idsQ(queryCut, []string{"op-1"}, cutFields), {Kind: queryTick, Fields: tickFields}, {Kind: queryDropping, Fields: droppingFields},
	}})
	f := timeFactsOf(s)
	if n, ok := f.Note("n1"); !ok || n.Type != NBound {
		t.Fatalf("the note asked for: %+v", n)
	}
	if !f.Goal("person-a").Exists || f.Cut("op-1").At != 1 || f.Tick().Backlog != 3 {
		t.Fatal("the facts asked for are not there")
	}
	if op, ok := f.DroppingOp("s1"); !ok || op != "op-1" {
		t.Fatalf("the dropping mark: %q %v", op, ok)
	}
	// Facts not asked for: a note, a goal, an op the plan did not name.
	mustPanic(t, func() { f.Note("n2") })
	mustPanic(t, func() { f.Goal("person-b") })
	mustPanic(t, func() { f.Cut("op-2") })
	// And no fact of a kind the plan did not ask.
	empty := timeFactsOf(w.load(ReadPlan{}))
	mustPanic(t, func() { empty.Tick() })
	mustPanic(t, func() { empty.DroppingOp("s1") })
	mustPanic(t, func() { empty.Note("n1") })
	// A snapshot built whole has none, and refuses nothing.
	whole := timeFactsOf(w.s)
	if _, ok := whole.Note("n1"); ok || whole.Goal("person-a").Exists || whole.Tick().Backlog != 0 {
		t.Fatal("a snapshot built whole has facts")
	}
}

// pickRef is the choice R11 made before the pool: the load of each name, the
// lowest not skipped, the first in row order on a tie.
func pickRef(names []string, load map[string]int, skip func(string) bool) string {
	best := ""
	for _, n := range names {
		if skip(n) {
			continue
		}
		if best == "" || load[n] < load[best] {
			best = n
		}
	}
	return best
}

func TestPoolPicksTheLeastLoadedFirstInRowOrder(t *testing.T) {
	t.Parallel()
	names := []string{"a", "b", "c", "d"}
	loads := map[string]int{"a": 2, "b": 1, "c": 1, "d": 3}
	p := newPool(names, func(n string) int { return loads[n] })
	// A tie goes to the first in row order, a pick counts, and skipped names are
	// put back as they were.
	var got []string
	for i := 0; i < 5; i++ {
		got = append(got, p.pick(nil, true))
	}
	want(t, "picks", got, []string{"b", "c", "a", "b", "c"})
	if p.pick(func(string) bool { return true }, true) != "" || len(p.h) != 4 {
		t.Fatalf("a pick with everything skipped: %d names left", len(p.h))
	}
	if p := newPool(nil, nil); p.pick(nil, true) != "" {
		t.Fatal("a pick from no names")
	}
	// A pick that does not count leaves the load where it was.
	p = newPool(names, func(n string) int { return loads[n] })
	if a, b := p.pick(nil, false), p.pick(nil, false); a != "b" || b != "b" {
		t.Fatalf("an uncounted pick moved: %s %s", a, b)
	}

	// Against the choice it replaces, over random loads and skips.
	rnd := uint64(88172645463325252)
	next := func(n int) int {
		rnd ^= rnd << 13
		rnd ^= rnd >> 7
		rnd ^= rnd << 17
		return int(rnd % uint64(n))
	}
	for trial := 0; trial < 300; trial++ {
		n := 1 + next(40)
		var rows []string
		load := map[string]int{}
		for i := 0; i < n; i++ {
			rows = append(rows, fmt.Sprintf("r%02d", i))
			load[rows[i]] = next(6)
		}
		ref := map[string]int{}
		for k, v := range load {
			ref[k] = v
		}
		skipped := map[string]bool{}
		for i := 0; i < next(16); i++ {
			skipped[rows[next(n)]] = true
		}
		skip := func(s string) bool { return skipped[s] }
		pl := newPool(rows, func(s string) int { return load[s] })
		for k := 0; k < 3*n; k++ {
			want := pickRef(rows, ref, skip)
			if want != "" {
				ref[want]++
			}
			if got := pl.pick(skip, true); got != want {
				t.Fatalf("trial %d pick %d: the pool chose %q, the scan %q", trial, k, got, want)
			}
		}
	}
}

// timeBenchPlan is one read of a tick planned on: the rule, the snapshot its read
// loaded and the keys it kept.
type timeBenchPlan struct {
	rule Rule
	s    *Snapshot
	keys []AgendaKey
}

// timeBenchPlans reads each rule's keys the way the tick does, in as many reads
// as layer 1's bounds make of them, and loads a snapshot for each; the loading
// is IT05's, and is not what the benchmark times.
func timeBenchPlans(b *testing.B, w *timeWorld, batches map[string][]string) (plans []timeBenchPlan, keys int) {
	names := make([]string, 0, len(batches))
	for name := range batches {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := timeRuleNamed(b, name)
		ks := agendaKeys(batches[name])
		keys += len(ks)
		for len(ks) > 0 {
			rp, rest := r.Read(ks, L1ReadBounds(), 0)
			if len(rest) >= len(ks) {
				b.Fatalf("%s: a read that keeps no key", name)
			}
			plans = append(plans, timeBenchPlan{r, w.load(rp), ks[:len(ks)-len(rest)]})
			ks = rest
		}
	}
	return plans, keys
}

// timeBenchRun times the plans, and fails when a key costs more than the limit
// of 8.1 IT10: 20 microseconds of Go time.
func timeBenchRun(b *testing.B, w *timeWorld, plans []timeBenchPlan, keys int) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, bp := range plans {
			bp.rule.Plan(bp.s, bp.keys, w.now)
		}
	}
	perKey := float64(b.Elapsed().Nanoseconds()) / float64(b.N*keys)
	b.ReportMetric(perKey, "ns/key")
	if perKey > 20_000 {
		b.Fatalf("%.0f ns a key, over the limit of 20 us", perKey)
	}
}

// BenchmarkTimeRules is the limit of 8.1 IT10: each plan is O(1) a key, at
// most 20 microseconds of Go time a key. It is measured over a mix of every key
// the rules take, at three sizes, so that a cost that grows with the keys shows
// as a rising ns/key; and at the sizes of the sprint the design allows (3):
// late work at 250 members, and late reads at 30, 250 and 1,000 readers, where
// a replacement chooses among the readers.
func BenchmarkTimeRules(b *testing.B) {
	for _, perKind := range []int{100, 400, 1600} {
		b.Run(fmt.Sprintf("mix/keys%d", perKind*9+1), func(b *testing.B) {
			w, batches := timeBenchWorld(b, perKind)
			plans, keys := timeBenchPlans(b, w, batches)
			timeBenchRun(b, w, plans, keys)
		})
	}
	b.Run("untaken/250members", func(b *testing.B) {
		w := newTimeWorld(b)
		var members []string
		for i := 0; i < 250; i++ {
			members = append(members, fmt.Sprintf("m%03d", i))
			w.member(members[i], true)
		}
		w.s.Fleet.SetRows(members)
		var ks []string
		for i := 0; i < 2000; i++ {
			p := fmt.Sprintf("p%d", i)
			w.primary(p, Working, nil)
			w.workCard(p+".w1", members[i%250], Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
			ks = append(ks, "late:untaken:"+p+".w1")
		}
		plans, keys := timeBenchPlans(b, w, map[string][]string{"late": ks})
		timeBenchRun(b, w, plans, keys)
	})
	for _, readers := range []int{30, 250, 1000} {
		b.Run(fmt.Sprintf("unbegun/%dreaders", readers), func(b *testing.B) {
			w := newTimeWorld(b)
			var rows []string
			for i := 0; i < readers; i++ {
				rows = append(rows, fmt.Sprintf("r%d", i))
			}
			w.s.Readers.SetRows(rows)
			var ks []string
			for i := 0; i < 2000; i++ {
				p := fmt.Sprintf("p%d", i)
				w.primary(p, Review, map[string]string{"rcards": p + ".r1.r0", "rereads": "0", "head": "abc"})
				w.readCard(p+".r1.r0", "r0", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
				ks = append(ks, "late:unbegun:"+p+".r1.r0")
			}
			plans, keys := timeBenchPlans(b, w, map[string][]string{"late": ks})
			timeBenchRun(b, w, plans, keys)
		})
	}
}

// timeBenchWorld is a world with perKind keys of each of nine kinds, all due,
// and the keys by rule.
func timeBenchWorld(b *testing.B, perKind int) (*timeWorld, map[string][]string) {
	w := newTimeWorld(b)
	w.member("m3", true)
	batches := map[string][]string{}
	add := func(rule, key string) { batches[rule] = append(batches[rule], key) }
	var streams []string
	for i := 0; i < perKind; i++ {
		p := fmt.Sprintf("p%d", i)
		w.primary(p, Working, map[string]string{"rcards": p + ".r1.r1", "rereads": "0"})
		w.workCard(p+".w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
		add("late", "late:untaken:"+p+".w1")
		q := fmt.Sprintf("q%d", i)
		w.primary(q, Working, nil)
		w.workCard(q+".w1", "m1", Working, map[string]string{fieldDueUnfinished: msText(timeR0)})
		add("late", "late:unfinished:"+q+".w1")
		r := fmt.Sprintf("r%d", i)
		w.primary(r, Review, map[string]string{"rcards": r + ".r1.r1", "rereads": "0"})
		w.readCard(r+".r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
		add("late", "late:unbegun:"+r+".r1.r1")
		stream := fmt.Sprintf("s%d", i)
		streams = append(streams, stream)
		w.put(w.s.Merge, CtlID(stream), stream, Ctl, map[string]string{"state": StreamMerging, fieldDueMergeIdle: msText(timeR0), fieldDueIdle: msText(timeR0)})
		add("late", "late:mergeidle:"+stream)
		add("late", "late:idle:"+stream)
		op := fmt.Sprintf("op-%d", i)
		w.f.Cuts[op] = CutFact{Entry: true, At: timeWall0 - 1}
		add("late", "late:cut:"+op)
		note := fmt.Sprintf("n%d", i)
		w.f.Notes[note] = NoteFact{Type: NBound, Cause: "held", Open: []string{"a", "b"}, Holds: []string{"a", "b"}}
		add("overdue", "overdue:"+note)
		add("hold", "hold:"+note)
		person := fmt.Sprintf("person-%d", i)
		w.f.Goals[person] = GoalFact{Exists: true}
		add("remind", "remind:"+person)
	}
	w.s.Merge.SetRows(append([]string{"s1"}, streams...))
	add("behind", "behind")
	w.f.Tick = TickFact{Backlog: 400, BehindN: 500}
	return w, batches
}

// TestTimeRuleTypesAreTheTables: every type the time rules raise through their
// helpers (know, judge, note), whose type the class test of the rules' notes
// cannot read at the site (sprintfn TestRuleNotesJAccepts stands a type in for
// a helper's), is a row of 2.2's judgments or of 2.5's notices in the tables'
// own words, so J takes it (a know of a type 2.5 does not have is REQUEST).
func TestTimeRuleTypesAreTheTables(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{NReplacedUntaken, NReplacedLateWork, NReplacedLateRead, NIdle, NPastDue, NCutStopped, NStepRefused,
		NWorkLate, NReadLate, NMergeLate, NStoppedWithDue} {
		_, judgment := Judgments[typ]
		_, notice := Notices[typ]
		if !judgment && !notice {
			t.Errorf("%q is in neither table", typ)
		}
	}
}
