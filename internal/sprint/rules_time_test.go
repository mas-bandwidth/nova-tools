package sprint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The tests of the time rules (rules_time.go, IT10). They run each rule over a
// small world of the four tables and the sprint's facts, apply what it planned
// to that world the way the store would (the guards of an entry and of X, J's
// one judgment per cause, the writes to the sprint's keys), and plan again:
// what a rule left changed, and what it left to be done twice, is then read
// off the world and not off the plan.

const (
	timeSec   = int64(1000)
	timeMin   = 60 * timeSec
	timeHour  = 60 * timeMin
	timeR0    = 10 * timeHour
	timeWall0 = int64(1_800_000_000_000)
)

// timeWorld is the four tables, the sprint's facts, the due set's entries the
// rules move, J's open judgments and the clock, at one Now.
type timeWorld struct {
	t     testing.TB
	s     *Snapshot
	f     *TimeFacts
	now   Now
	j     map[string]string // J's jopen: subject|type|cause to the note, or the hold h<note>
	due   map[string]int64  // due entries the rules move, by key
	clock Clock
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
	w := &timeWorld{t: t, s: s, now: Now{R: timeR0, Wall: timeWall0, Running: true},
		f: &TimeFacts{Notes: map[string]NoteFact{}, Goals: map[string]GoalFact{}, Cuts: map[string]CutFact{}, Dropping: map[string]bool{}},
		j: map[string]string{}, due: map[string]int64{}}
	withTimeFacts(s, w.f)
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

func (w *timeWorld) plan(rule string, keys ...string) (RulePlan, TimeWrites) {
	ks := make([]AgendaKey, len(keys))
	for i, k := range keys {
		ks[i] = keyOf(k, uint64(i+1))
	}
	return TimePlan(rule, w.s, ks, w.now)
}

// timeEffect is what applying a plan changed in the world.
type timeEffect struct {
	Moves, Know, Opened, Closed, Unheld, Writes int
	// Refused is why a guard refused the whole plan, and nothing was applied.
	Refused string
}

func (e timeEffect) zero() bool { return e == timeEffect{} }

// apply applies a plan to the world as the store would, all or nothing: every
// entry's expectation and every guard the world can check are checked first.
func (w *timeWorld) apply(p RulePlan, tw TimeWrites) timeEffect {
	w.t.Helper()
	for _, g := range p.Guards {
		switch g.Kind {
		case guardMemberUp:
			if w.s.MemberCtl(g.Member).F("status") != Up {
				return timeEffect{Refused: "XGUARD: member " + g.Member + " is not up"}
			}
		case guardDue:
			if at, ok := w.due[g.Key]; ok && at > g.Score {
				return timeEffect{Refused: fmt.Sprintf("XGUARD: %s is at %d, above %d", g.Key, at, g.Score)}
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
	if tw.Tick != nil {
		w.f.Tick.BehindN = tw.Tick.BehindN
		e.Writes++
	}
	if c := tw.Clock; c != nil {
		switch {
		case c.DueSince != nil:
			w.clock.DueSinceMs = *c.DueSince
		case c.ClearDueSince:
			w.clock.DueSinceMs = 0
		}
		if c.StopRaised != nil {
			w.clock.StopRaisedMs = *c.StopRaised
		}
		e.Writes++
	}
	w.parks += len(tw.Park)
	e.Writes += len(tw.Park)
	return e
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

func keyTexts(ks []AgendaKey) []string {
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
func silent(p RulePlan, tw TimeWrites) bool {
	return len(p.Plan.Units) == 0 && len(p.Intents) == 0 && len(p.Notes) == 0 && len(p.Guards) == 0 &&
		len(p.Requeue) == 0 && len(p.Quarantine) == 0 && len(p.HeldBack) == 0 && tw.Empty()
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
	p, tw := w.plan("late", key)
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
	want(t, "keys removed", keyTexts(p.Done), []string{key})
	if !tw.Empty() {
		t.Fatalf("writes to sprint keys: %+v", tw)
	}
	if eff := w.apply(p, tw); eff != (timeEffect{Moves: 1, Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if c := w.card(Fleet, "p1.w1"); c.Row != "m2" || c.Col != Ready || c.Int("gen") != 4 {
		t.Fatalf("the card is %+v", c)
	}

	// Late again, the one replacement made: judged, and the card stays.
	w.now.R += 15 * timeMin
	p, tw = w.plan("late", key)
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
	if eff := w.apply(p, tw); eff != (timeEffect{Opened: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if c := w.card(Fleet, "p1.w1"); c.Row != "m2" || c.Int("gen") != 4 {
		t.Fatalf("the judged card moved: %+v", c)
	}
	// J writes one judgment a cause: asking again opens none.
	p, tw = w.plan("late", key)
	if eff := w.apply(p, tw); !eff.zero() {
		t.Fatalf("a second judgment: %+v", eff)
	}

	// With no other member up the card is judged at once: nothing to replace
	// it to, and no advice to take its member down.
	w2 := newTimeWorld(t)
	w2.member("m2", false)
	w2.primary("p1", Working, nil)
	w2.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
	p, _ = w2.plan("late", key)
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
	p, _ := w.plan("late", "late:untaken:p1.w1")
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

	p, _ := w.plan("late", key)
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
	w.apply(p, TimeWrites{})
	if c := w.card(Fleet, "p1.w1"); c.Int("redeals") != 3 || c.F(fieldFirstTakenR) != "" || c.Row != "m2" {
		t.Fatalf("the card after the redeal: %+v", c)
	}

	// At the bound of five: judged with its history, and the card stays,
	// since its worker may still finish.
	w.workCard("p2.w1", "m1", Working, map[string]string{"redeals": "5", fieldDueUnfinished: msText(timeR0)})
	w.primary("p2", Working, nil)
	p, _ = w.plan("late", "late:unfinished:p2.w1")
	n := noteReq(p, requestOpen, NWorkLate)
	if n == nil || n.Cause != "unfinished" || len(moved(p)) != 0 || !strings.Contains(n.Text, "5 of 5") || !strings.Contains(n.Text, "nova-sprint log --card p2") {
		t.Fatalf("at the bound: %+v %+v", n, moved(p))
	}
	want(t, "the decisions", n.Decisions, []string{"fleet down m1", "drop p2", "wait"})

	// With no other member up, likewise, below the bound.
	w.member("m2", false)
	w.workCard("p3.w1", "m1", Working, map[string]string{"redeals": "0", fieldDueUnfinished: msText(timeR0)})
	w.primary("p3", Working, nil)
	p, _ = w.plan("late", "late:unfinished:p3.w1")
	if noteReq(p, requestOpen, NWorkLate) == nil || len(moved(p)) != 0 {
		t.Fatalf("no other member up: %+v %+v", p.Notes, moved(p))
	}
}

func TestLateReadGoesToNewReader(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1,p1.r1.r2", "rereads": "0", "head": "abc"})
	w.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldAskedR: msText(timeR0), fieldDueUnbegun: msText(timeR0 + 30*timeMin)})
	w.readCard("p1.r1.r2", "r2", Asked, map[string]string{fieldAskedR: msText(timeR0), fieldDueUnbegun: msText(timeR0 + 30*timeMin)})
	w.now.R = timeR0 + 30*timeMin

	p, _ := w.plan("late", "late:unbegun:p1.r1.r1")
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
	if eff := w.apply(p, TimeWrites{}); eff != (timeEffect{Moves: 3, Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if w.card(Readers, "p1.r1.r2").Col != Asked || w.card(Readers, "p1.r1.r1").Placed() {
		t.Fatalf("the reads: r2 %+v, r1 %+v", w.card(Readers, "p1.r1.r2"), w.card(Readers, "p1.r1.r1"))
	}

	// The new read is late too: every reader has read this attempt (the retired
	// card counts), so it is judged, and offers no other reader.
	w.now.R += 30 * timeMin
	p, _ = w.plan("late", "late:unbegun:p1.r1.r3")
	n := noteReq(p, requestOpen, NReadLate)
	if n == nil || n.Cause != "unbegun" || len(moved(p)) != 0 {
		t.Fatalf("no reader left: %+v %+v", n, moved(p))
	}
	want(t, "the decisions", n.Decisions, []string{"drop p1", "wait"})

	// A reader that read this attempt and whose card was retired is not asked
	// again, though the primary's rcards do not name the card.
	w4 := newTimeWorld(t)
	w4.primary("p1", Review, map[string]string{"rereads": "1"})
	w4.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
	w4.readCard("p1.r1.r2", "r2", Asked, nil)
	retired := w4.card(Readers, "p1.r1.r2")
	retired.Col = ""
	w4.s.Readers.Put(retired)
	p, _ = w4.plan("late", "late:unbegun:p1.r1.r1")
	if ch := moved(p); len(ch) != 3 || ch[1].Entry.ID != "p1.r1.r3" {
		t.Fatalf("a retired read did not count as read: %+v", ch)
	}

	// At three rereads it is judged though a reader is free, and offers to ask.
	w2 := newTimeWorld(t)
	w2.primary("p1", Review, map[string]string{"rcards": "p1.r1.r1", "rereads": "3"})
	w2.readCard("p1.r1.r1", "r1", Reading, map[string]string{fieldDueUnreported: msText(timeR0)})
	p, _ = w2.plan("late", "late:unreported:p1.r1.r1")
	n = noteReq(p, requestOpen, NReadLate)
	if n == nil || n.Cause != "unreported" || len(moved(p)) != 0 || !strings.Contains(n.Text, "not reported") {
		t.Fatalf("at the reread bound: %+v %+v", n, moved(p))
	}
	want(t, "the decisions", n.Decisions, []string{"ask --another p1", "drop p1", "wait"})

	// At fifteen read cards no reader is asked.
	var cards []string
	for i := 1; i <= maxReadCards; i++ {
		cards = append(cards, fmt.Sprintf("p1.r%d.r1", i))
	}
	w3 := newTimeWorld(t)
	w3.primary("p1", Review, map[string]string{"rcards": strings.Join(cards, ","), "rereads": "0"})
	w3.readCard("p1.r1.r1", "r1", Asked, map[string]string{fieldDueUnbegun: msText(timeR0)})
	p, _ = w3.plan("late", "late:unbegun:p1.r1.r1")
	if noteReq(p, requestOpen, NReadLate) == nil || len(moved(p)) != 0 {
		t.Fatalf("at fifteen read cards: %+v %+v", p.Notes, moved(p))
	}
}

func TestIdleNoticeOnceUntilLanding(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	span := spanMs(IdleSpan)
	w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0 + span)})
	w.now.R = timeR0 + span
	key := "late:idle:s1"

	p, tw := w.plan("late", key)
	ch := moved(p)
	if len(ch) != 1 || ch[0].Table != Merge || ch[0].Entry.ID != "ctl-s1" || !reflect.DeepEqual(ch[0].Entry.Unset, []string{fieldDueIdle}) || ch[0].Entry.Move != nil {
		t.Fatalf("due_idle is not unset in place: %+v", ch)
	}
	n := noteReq(p, requestKnow, NIdle)
	if n == nil || !reflect.DeepEqual(n.Subjects, []string{StreamSubject("s1")}) || !strings.Contains(n.Text, "stream s1 has landed nothing for 2h0m0s") {
		t.Fatalf("no notice: %+v", p.Notes)
	}
	if eff := w.apply(p, tw); eff != (timeEffect{Moves: 1, Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}

	// Said once: the key delivered again finds no due_idle.
	p, tw = w.plan("late", key)
	if !silent(p, tw) {
		t.Fatalf("said twice: %+v", p)
	}

	// A landing sets due_idle again, and the next lapse is said again.
	w.card(Merge, "ctl-s1").Fields[fieldDueIdle] = msText(w.now.R + span)
	w.now.R += span
	p, _ = w.plan("late", key)
	if noteReq(p, requestKnow, NIdle) == nil {
		t.Fatalf("not said again after a landing: %+v", p)
	}
	// Not yet lapsed: nothing.
	w.card(Merge, "ctl-s1").Fields[fieldDueIdle] = msText(w.now.R + 1)
	p, tw = w.plan("late", key)
	if !silent(p, tw) {
		t.Fatalf("said before the span ran out: %+v", p)
	}
}

func TestCutJudgedWhileStopped(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.now = Now{R: timeR0, Wall: timeWall0, Running: false} // STOPPED
	w.f.Cuts["op-1"] = CutFact{Entry: true, At: timeWall0 - 10*timeSec}
	key := "late:cut:op-1"

	p, tw := w.plan("late", key)
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
	if !hasGuard(p, noEntryAbove("cut:op-1", timeWall0)) {
		t.Fatalf("the cut entry is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", keyTexts(p.Done), []string{key})
	if eff := w.apply(p, tw); eff != (timeEffect{Opened: 1}) {
		t.Fatalf("effect %+v", eff)
	}

	// The clock counts wall time, so the same plan is made RUNNING.
	w.now.Running = true
	if p2, _ := w.plan("late", key); !reflect.DeepEqual(p2, p) {
		t.Fatalf("RUNNING plans another judgment than STOPPED:\n%+v\n%+v", p2, p)
	}

	// A later part armed the clock again above wall: the op goes on, quiet.
	w.f.Cuts["op-1"] = CutFact{Entry: true, At: timeWall0 + 10*timeMin}
	if p, tw = w.plan("late", key); !silent(p, tw) {
		t.Fatalf("judged an op whose clock was armed again: %+v", p)
	}

	// The same plan applied after that part moved the entry is refused
	// XGUARD, and writes nothing.
	w.f.Cuts["op-1"] = CutFact{}
	pStale, twStale := w.plan("late", key)
	w.due["cut:op-1"] = timeWall0 + 10*timeMin
	if eff := w.apply(pStale, twStale); !strings.HasPrefix(eff.Refused, "XGUARD") {
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

	p, tw := w.plan("hold", key)
	n := noteReq(p, requestUnhold, NCannotAsk)
	if n == nil || n.Cause != "ask" || !reflect.DeepEqual(n.Subjects, []string{"p1", "p2"}) {
		t.Fatalf("no unheld line naming the type, cause and subjects: %+v", p.Notes)
	}
	if !hasGuard(p, XGuard{Kind: guardHold, Key: "hn7"}) {
		t.Fatalf("the hold is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", keyTexts(p.Done), []string{key})
	if len(p.Plan.Units) != 0 || !tw.Empty() {
		t.Fatalf("the expiry touches more than the line: %+v %+v", p.Plan.Units, tw)
	}
	if eff := w.apply(p, tw); eff != (timeEffect{Unheld: 2}) {
		t.Fatalf("effect %+v", eff)
	}
	// A second run finds no hold, and writes nothing.
	p, tw = w.plan("hold", key)
	if !silent(p, tw) {
		t.Fatalf("a second expiry: %+v", p)
	}
	// A note the hold of which the owner rule already ended has none.
	w.f.Notes["n8"] = NoteFact{Type: NCannotAsk, Cause: "ask"}
	if p, tw = w.plan("hold", "hold:n8", "hold:n9"); !silent(p, tw) || len(p.Done) != 2 {
		t.Fatalf("a hold that is gone or a note that is unknown: %+v", p)
	}
}

func TestOverdueMarksOnce(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.f.Notes["n3"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p1", "p2"}}
	w.f.Notes["n4"] = NoteFact{Type: NBound, Cause: "held"} // closed
	w.f.Notes["n5"] = NoteFact{Type: NBound, Cause: "held", Open: []string{"p3"}, Marked: true}

	p, tw := w.plan("overdue", "overdue:n3", "overdue:n4", "overdue:n5")
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
	want(t, "keys removed", keyTexts(p.Done), []string{"overdue:n3", "overdue:n4", "overdue:n5"})
	if eff := w.apply(p, tw); eff != (timeEffect{Know: 1}) {
		t.Fatalf("effect %+v", eff)
	}
	if p, tw = w.plan("overdue", "overdue:n3"); !silent(p, tw) {
		t.Fatalf("marked twice: %+v", p)
	}
}

func TestRemindOnePushPerPeriod(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.f.Goals["person-a"] = GoalFact{Exists: true}
	key := "remind:person-a"

	p, tw := w.plan("remind", key)
	want(t, "the entry moved", tw.Due, []DueSet{{Key: "remind:person-a", At: w.now.R + 5*timeMin}})
	want(t, "the claim", tw.Goal, []GoalClaim{{Person: "person-a", R: w.now.R}})
	if !hasGuard(p, noEntryAbove("remind:person-a", w.now.R)) {
		t.Fatalf("the entry is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", keyTexts(p.Done), []string{key})
	if len(p.Plan.Units) != 0 || len(p.Notes) != 0 {
		t.Fatalf("phase 1 writes more than its claim: %+v", p)
	}
	if eff := w.apply(p, tw); eff.Refused != "" || w.claims != 1 || w.due["remind:person-a"] != w.now.R+5*timeMin {
		t.Fatalf("effect %+v, %d claims", eff, w.claims)
	}

	// The key delivered again in the same period, planned from the entry that
	// moved: nothing.
	if p2, tw2 := w.plan("remind", key); !silent(p2, tw2) {
		t.Fatalf("a second claim in one period: %+v %+v", p2, tw2)
	}
	// A second loop's step, planned before the first applied and applied after:
	// refused XGUARD, so only one push.
	if eff := w.apply(p, tw); !strings.HasPrefix(eff.Refused, "XGUARD") || w.claims != 1 {
		t.Fatalf("a second loop's claim applied: %+v, %d claims", eff, w.claims)
	}
	// The next period claims again, once.
	w.now.R += 5 * timeMin
	p, tw = w.plan("remind", key)
	if eff := w.apply(p, tw); eff.Refused != "" || w.claims != 2 {
		t.Fatalf("the next period: %+v, %d claims", eff, w.claims)
	}
	// A goal that was dropped has no entry and no claim.
	if p, tw = w.plan("remind", "remind:person-b"); !silent(p, tw) || len(p.Done) != 1 {
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
	// backlog, and judged nothing.
	w := worldWith(TickFact{Backlog: 400, Agenda: 30, DueNow: 5, BehindN: 500})
	p, tw := w.plan("behind", "behind")
	want(t, "re-armed", tw.Due, []DueSet{{Key: "behind", At: w.now.R + 5*timeMin}})
	if tw.Tick == nil || tw.Tick.BehindN != 400 || len(p.Notes) != 0 {
		t.Fatalf("shrinking is only to know: %+v %+v", tw, p.Notes)
	}
	if !hasGuard(p, noEntryAbove("behind", w.now.R)) {
		t.Fatalf("the entry is not guarded: %+v", p.Guards)
	}
	want(t, "keys removed", keyTexts(p.Done), []string{"behind"})

	// A backlog at least as large as when it was armed is judged, once.
	w = worldWith(TickFact{Backlog: 800, Agenda: 30, DueNow: 5, BehindN: 500})
	p, tw = w.plan("behind", "behind")
	n := noteReq(p, requestOpen, typeFallingBehind)
	if n == nil || !reflect.DeepEqual(n.Subjects, []string{"sprint"}) || !tw.Empty() ||
		!strings.Contains(n.Text, "800 lines, 30 keys, 5 due for 5 minutes of running time") {
		t.Fatalf("no judgment: %+v %+v", p.Notes, tw)
	}
	want(t, "the decisions", n.Decisions, []string{"wait", "stop", "where"})
	w.now.Running = false
	if p, _ = w.plan("behind", "behind"); strings.Contains(strings.Join(noteReq(p, requestOpen, typeFallingBehind).Decisions, ","), "stop") {
		t.Fatalf("stop offered while STOPPED")
	}
	// An equal backlog counts as not shrunk.
	w = worldWith(TickFact{Backlog: 500, BehindN: 500})
	if p, _ = w.plan("behind", "behind"); noteReq(p, requestOpen, typeFallingBehind) == nil {
		t.Fatalf("an equal backlog is not judged")
	}

	// The backlog gone closes the judgment when it is open, and re-arms nothing.
	w = worldWith(TickFact{Backlog: 0, BehindN: 500, Judged: true})
	p, tw = w.plan("behind", "behind")
	if n = noteReq(p, requestClose, typeFallingBehind); n == nil || !tw.Empty() {
		t.Fatalf("no close at zero: %+v %+v", p.Notes, tw)
	}
	w = worldWith(TickFact{Backlog: 0, BehindN: 500})
	if p, tw = w.plan("behind", "behind"); !silent(p, tw) {
		t.Fatalf("a backlog of zero with nothing open: %+v", p)
	}

	// Not armed: the tick-end part arms it, not this rule. An entry armed above
	// R is left as it is.
	w = worldWith(TickFact{Backlog: 900})
	if p, tw = w.plan("behind", "behind"); !silent(p, tw) {
		t.Fatalf("armed a rule that was not armed: %+v %+v", p, tw)
	}
	w = worldWith(TickFact{Backlog: 900, BehindN: 500})
	w.f.Tick.Entry, w.f.Tick.EntryAt = true, w.now.R+1
	if p, tw = w.plan("behind", "behind"); !silent(p, tw) {
		t.Fatalf("judged while armed above R: %+v %+v", p, tw)
	}
	// No key, no plan.
	if p, tw = w.plan("behind"); !silent(p, tw) || len(p.Done) != 0 {
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
		p, tw := w.plan("behind", "behind")
		if eff := w.apply(p, tw); eff.Refused != "" {
			t.Fatalf("%+v: refused %+v", tick, eff)
		}
		// The same key delivered a second time, nothing changed: the second
		// step is empty.
		p, tw = w.plan("behind", "behind")
		if !silent(p, tw) {
			t.Fatalf("%+v: the second run plans %+v %+v", tick, p, tw)
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
	p := StoppedLook(dry, c, timeWall0)
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
	tw := StoppedWrites(dry, c, timeWall0)
	if tw.Clock == nil || tw.Clock.StopRaised == nil || *tw.Clock.StopRaised != stoppedSince || tw.Clock.DueSince != nil {
		t.Fatalf("the clock writes: %+v", tw.Clock)
	}
}

func TestStoppedNothingDueNoJudgment(t *testing.T) {
	t.Parallel()
	const stoppedSince = timeWall0 - 3*timeHour
	guardOnly := &Card{ID: "g", Row: "s1", Col: Waiting, Rev: 1}
	// Plans that change no card: notes, a guard, a key removed.
	dry := []RulePlan{
		{Notes: make([]NoteReq, 50), Done: []AgendaKey{keyOf("deal", 1)}},
		{Plan: Plan{Units: []Unit{{Changes: []Change{change(Work, guardEntry(guardOnly))}}}}},
	}
	// Nothing was due, and nothing is recorded: the look plans nothing.
	c := Clock{StoppedSinceMs: stoppedSince}
	if p, tw := StoppedLook(dry, c, timeWall0), StoppedWrites(dry, c, timeWall0); !silent(p, tw) {
		t.Fatalf("a look with nothing due: %+v %+v", p, tw)
	}
	// Moves were due, and are not now: due_since_ms is cleared, and no
	// judgment is raised however long ago it was set.
	c.DueSinceMs = timeWall0 - 3*timeHour
	p, tw := StoppedLook(dry, c, timeWall0), StoppedWrites(dry, c, timeWall0)
	if len(p.Notes) != 0 || tw.Clock == nil || !tw.Clock.ClearDueSince || tw.Clock.StopRaised != nil {
		t.Fatalf("moves gone: %+v %+v", p.Notes, tw.Clock)
	}
	// No dry plan at all likewise.
	if q := StoppedLook(nil, c, timeWall0); len(q.Notes) != 0 {
		t.Fatalf("no plan, a judgment: %+v", q.Notes)
	}
	// A RUNNING machine is not looked at.
	running := []RulePlan{dryPlan("a")}
	if p, tw = StoppedLook(running, Clock{}, timeWall0), StoppedWrites(running, Clock{}, timeWall0); !silent(p, tw) {
		t.Fatalf("a look at a RUNNING machine: %+v %+v", p, tw)
	}
}

func TestStoppedRaisedOncePerSpan(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	dry := []RulePlan{dryPlan("a")}
	span := StoppedDueSpan.Milliseconds()
	w.clock = Clock{StoppedSinceMs: timeWall0 - timeHour}
	look := func(wall int64) (RulePlan, TimeWrites) {
		return StoppedLook(dry, w.clock, wall), StoppedWrites(dry, w.clock, wall)
	}
	// The first look that finds moves due records when.
	p, tw := look(timeWall0)
	if len(p.Notes) != 0 || tw.Clock == nil || tw.Clock.DueSince == nil || *tw.Clock.DueSince != timeWall0 {
		t.Fatalf("the first look: %+v %+v", p.Notes, tw.Clock)
	}
	w.apply(p, tw)
	// Ten minutes count from then, not from the stop.
	if p, tw = look(timeWall0 + span - 1); !silent(p, tw) {
		t.Fatalf("raised before ten minutes: %+v", p)
	}
	// The judgment is raised, and this span is marked.
	p, tw = look(timeWall0 + span)
	if noteReq(p, requestOpen, NStoppedWithDue) == nil || tw.Clock == nil || tw.Clock.StopRaised == nil {
		t.Fatalf("not raised at ten minutes: %+v %+v", p.Notes, tw.Clock)
	}
	w.apply(p, tw)
	if w.clock.StopRaisedMs != w.clock.StoppedSinceMs {
		t.Fatalf("the span is not marked: %+v", w.clock)
	}
	// Once a STOPPED span, however long it goes on, and whatever the hold.
	for _, later := range []int64{span, 10 * span, 100 * span} {
		if p, tw = look(timeWall0 + later + 1); !silent(p, tw) {
			t.Fatalf("raised twice in one span at +%d: %+v", later, p)
		}
	}
	// A new span, after a start and a stop, is raised once again.
	w.clock = Clock{StoppedSinceMs: timeWall0 + 200*span, StopRaisedMs: w.clock.StopRaisedMs}
	p, tw = look(timeWall0 + 200*span)
	w.apply(p, tw)
	if p, tw = look(timeWall0 + 201*span); noteReq(p, requestOpen, NStoppedWithDue) == nil {
		t.Fatalf("not raised in a new span: %+v %+v", p.Notes, w.clock)
	}

	// A wait sets stophold_ms: no judgment until it, then one.
	w.clock = Clock{StoppedSinceMs: timeWall0 - timeHour, DueSinceMs: timeWall0 - 2*timeHour, StopHoldMs: timeWall0 + timeHour}
	if p, tw = look(timeWall0); !silent(p, tw) {
		t.Fatalf("raised under a wait: %+v", p)
	}
	if p, _ = look(timeWall0 + timeHour); noteReq(p, requestOpen, NStoppedWithDue) == nil {
		t.Fatalf("not raised when the wait ran out")
	}
}

func TestLimitHalvesThenParks(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"LIMIT", "BUDGET"} {
		key := keyOf("ask@48213", 48213)
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
			tw := BugWrites("ask", key, code, "300 entries over the bound of 256", h)
			if chunkAfter(h) > 1 {
				// Planned again at half the size: the key stays, nothing parks.
				if next != h+1 || len(p.Done) != 0 || !reflect.DeepEqual(keyTexts(p.Requeue), []string{"ask@48213"}) || !tw.Empty() ||
					!strings.Contains(n.Text, "half its size") {
					t.Fatalf("%s at %d halvings: next %d, done %v, requeue %v, writes %+v", code, h, next, p.Done, p.Requeue, tw)
				}
				h = next
				continue
			}
			// At a chunk of one card: parked, out of the agenda, its park written.
			if next != 0 || !reflect.DeepEqual(keyTexts(p.Done), []string{"ask@48213"}) || len(p.Requeue) != 0 ||
				!reflect.DeepEqual(tw.Park, []ParkKey{{Key: "ask@48213"}}) || !strings.Contains(n.Text, "parked") {
				t.Fatalf("%s at a chunk of one: next %d, done %v, writes %+v", code, next, p.Done, tw)
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
			key := keyOf("deal", 9)
			p, next := OnBug("deal", key, code, "1 entry", h)
			tw := BugWrites("deal", key, code, "1 entry", h)
			n := noteReq(p, requestOpen, NStepRefused)
			if next != 0 || n == nil || !reflect.DeepEqual(keyTexts(p.Done), []string{"deal"}) || len(p.Requeue) != 0 ||
				!reflect.DeepEqual(tw.Park, []ParkKey{{Key: "deal"}}) || !strings.Contains(n.Text, "code "+code) || !strings.Contains(n.Text, "parked") {
				t.Fatalf("%s at %d halvings: next %d, %+v, %+v", code, h, next, p, tw)
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
	}
	for _, c := range cases {
		w := newTimeWorld(t)
		c.setup(w)
		p, tw := w.plan(c.rule, c.keys...)
		if len(p.Done) != len(c.keys) {
			t.Fatalf("%s: the first run removes %v of %v", c.name, keyTexts(p.Done), c.keys)
		}
		if eff := w.apply(p, tw); eff.zero() || eff.Refused != "" {
			t.Fatalf("%s: the first run changed nothing (or was refused): %+v", c.name, eff)
		}
		p, tw = w.plan(c.rule, c.keys...)
		eff := w.apply(p, tw)
		if !eff.zero() {
			t.Fatalf("%s: the second run changed the world: %+v\n%+v", c.name, eff, p)
		}
		if c.literal && !silent(p, tw) {
			t.Fatalf("%s: the second plan is not empty: %+v %+v", c.name, p, tw)
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
		p, tw := StoppedLook(dry, w.clock, timeWall0), StoppedWrites(dry, w.clock, timeWall0)
		if eff := w.apply(p, tw); eff.zero() {
			t.Fatalf("%+v: the first look changed nothing", c)
		}
		p, tw = StoppedLook(dry, w.clock, timeWall0), StoppedWrites(dry, w.clock, timeWall0)
		if !silent(p, tw) {
			t.Fatalf("%+v: the second look plans %+v %+v", c, p, tw)
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
	// 1.4.2's order puts R11, R12, R13, R14 and R18 in a row.
	var at []int
	for i, name := range []string{"late", "overdue", "hold", "remind", "behind"} {
		r, ok := byName[name]
		if !ok {
			t.Fatalf("rule %s is not registered: %v", name, order)
		}
		if r.Priority != 11+i || r.MaxSteps != 0 || r.Read == nil || r.Plan == nil {
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
	// The registered plan is TimePlan's, without its writes.
	w := newTimeWorld(t)
	w.f.Goals["person-a"] = GoalFact{Exists: true}
	ks := []AgendaKey{keyOf("remind:person-a", 1)}
	got := byName["remind"].Plan(w.s, ks, w.now)
	wantPlan, _ := TimePlan("remind", w.s, ks, w.now)
	if !reflect.DeepEqual(got, wantPlan) {
		t.Fatalf("the registered plan differs:\n%+v\n%+v", got, wantPlan)
	}
	if p, tw := TimePlan("no-such-rule", w.s, ks, w.now); !silent(p, tw) || len(p.Done) != 0 {
		t.Fatalf("a name that is no time rule planned %+v", p)
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
	p, tw := w.plan("late", keys...)
	if !silent(p, tw) {
		t.Fatalf("planned for keys that are not late: %+v", p)
	}
	want(t, "every key removed", keyTexts(p.Done), keys)
}

func TestLateHeldBackForADroppingStream(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Working, nil)
	w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
	w.put(w.s.Merge, "ctl-s1", "s1", Ctl, map[string]string{"state": StreamMerging, fieldDueIdle: msText(timeR0)})
	w.f.Dropping["s1"] = true
	w.f.Cuts["op-1"] = CutFact{}
	p, tw := w.plan("late", "late:untaken:p1.w1", "late:idle:s1", "late:cut:op-1")
	// The cards of a stream being dropped are refused DROPPING: their keys stay,
	// held back until the mark clears. A cut clock is the op's, and is judged.
	want(t, "held back", keyTexts(p.HeldBack), []string{"late:untaken:p1.w1", "late:idle:s1"})
	want(t, "removed", keyTexts(p.Done), []string{"late:cut:op-1"})
	if len(moved(p)) != 0 || noteReq(p, requestOpen, NCutStopped) == nil || !tw.Empty() {
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
	p, _ := w.plan("late", "late:untaken:p1.w1", "late:untaken:p2.w1", "late:untaken:p3.w1")
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
		p, _ := w.plan("late", "late:mergeidle:s1")
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

func TestTimeReadsWithinBoundsAndHalve(t *testing.T) {
	t.Parallel()
	mk := func(n int, f func(i int) string) []AgendaKey {
		var ks []AgendaKey
		for i := 0; i < n; i++ {
			ks = append(ks, keyOf(f(i), uint64(i+1)))
		}
		return ks
	}
	// The keys of a read are cut to its records: 51 a note.
	notes := mk(500, func(i int) string { return fmt.Sprintf("overdue:n%d", i) })
	rp, rest := readOverdue(notes, ReadBounds{Records: 10000}, 0)
	kept := len(notes) - len(rest)
	if kept != 10000/noteReadRecords || len(rp.Sprint) != 1 || len(rp.Sprint[0].Source.IDs) != kept || rp.Sprint[0].Kind != queryJNote {
		t.Fatalf("kept %d of %d, %+v", kept, len(notes), rp.Sprint)
	}
	want(t, "the keys left", keyTexts(rest), keyTexts(notes[kept:]))

	// Each halving halves the keys, down to one, and the rest wait.
	eight := mk(8, func(i int) string { return fmt.Sprintf("hold:n%d", i) })
	var counts []int
	for h := 0; h <= 5; h++ {
		_, left := readHold(eight, ReadBounds{Records: 10000}, h)
		counts = append(counts, len(eight)-len(left))
		if len(left) > 0 {
			want(t, "the keys left keep their order", keyTexts(left), keyTexts(eight[len(eight)-len(left):]))
		}
	}
	want(t, "keys kept a halving", counts, []int{8, 4, 2, 1, 1, 1})

	// A key that costs more than the read may is still read: it must move.
	_, left := readOverdue(notes[:3], ReadBounds{Records: 1}, 0)
	if len(left) != 2 {
		t.Fatalf("a read that keeps no key: %d left", len(left))
	}

	// R11's read counts the fleet once: 250 records, and 2 a card.
	untaken := mk(3, func(i int) string { return fmt.Sprintf("late:untaken:p%d.w1", i) })
	_, left = readLate(untaken, ReadBounds{Records: fleetReadRecords + 4}, 0)
	if len(left) != 1 {
		t.Fatalf("the fleet is counted a key, not once: %d left", len(left))
	}

	// What R11 reads: each card kind's card, its primaries with rcards, the
	// fleet and the readers, the stream's control card, a stream's queued cards,
	// and the cut set.
	keys := []AgendaKey{
		keyOf("late:untaken:p1.w1", 1), keyOf("late:unfinished:p2.w3", 2), keyOf("late:unbegun:p3.r1.r2", 3),
		keyOf("late:mergeidle:s1", 4), keyOf("late:idle:s2", 5), keyOf("late:cut:op-1", 6), keyOf("late:junk", 7),
	}
	rp, rest = readLate(keys, ReadBounds{Records: 10000}, 0)
	if len(rest) != 0 {
		t.Fatalf("left %v", keyTexts(rest))
	}
	got := map[string][]string{}
	for _, q := range rp.Sprint {
		got[q.Kind+"/"+q.Table] = q.Source.IDs
	}
	want(t, "the queries", got, map[string][]string{
		"related/fleet":   {"p1.w1", "p2.w3"},
		"related/readers": {"p3.r1.r2"},
		"related/work":    {"p1", "p2", "p3"},
		"related/merge":   {"ctl-s1", "ctl-s2"},
		"fleet/":          nil,
		"readers/":        nil,
		"cut/":            {"op-1"},
	})
	want(t, "the counts", rp.Counts, []CountQ{{Table: Merge, Cells: []string{"s1:" + Queued}}})
	for _, q := range rp.Sprint {
		if q.Kind == queryRelated && len(q.Fields) == 0 {
			t.Fatalf("a read of whole records: %+v", q)
		}
		if q.Table == Work && !reflect.DeepEqual(q.Follow, []string{"rcards"}) {
			t.Fatalf("the primaries do not follow rcards: %+v", q)
		}
	}

	// The reads of R14 and R18.
	rp, _ = readRemind(mk(2, func(i int) string { return fmt.Sprintf("remind:person-%d", i) }), ReadBounds{Records: 10}, 0)
	want(t, "goals read", rp.Sprint[0].Source.IDs, []string{"person-0", "person-1"})
	if rp, _ = readBehind(mk(1, func(int) string { return "behind" }), ReadBounds{Records: 10}, 0); len(rp.Sprint) != 1 || rp.Sprint[0].Kind != queryTick {
		t.Fatalf("the tick is not read: %+v", rp)
	}
	if rp, rest = readBehind(nil, ReadBounds{Records: 10}, 0); len(rp.Sprint) != 0 || len(rest) != 0 {
		t.Fatalf("a read without keys: %+v", rp)
	}
}

// TestTimeRulesNeverReadAClock holds the file to what the design says: time
// comes from Now, never from the wall clock.
func TestTimeRulesNeverReadAClock(t *testing.T) {
	t.Parallel()
	f, err := parser.ParseFile(token.NewFileSet(), "rules_time.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	banned := map[string]bool{"Now": true, "Since": true, "Until": true, "Sleep": true, "After": true, "AfterFunc": true,
		"Tick": true, "NewTimer": true, "NewTicker": true}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" && banned[sel.Sel.Name] {
				t.Errorf("rules_time.go reads the clock: time.%s", sel.Sel.Name)
			}
		}
		return true
	})
}

// TestTimeRulesPlanTheSameFromAnyClock plans the same keys over snapshots whose
// own time differs, and finds the same plan: nothing reads it.
func TestTimeRulesPlanTheSameFromAnyClock(t *testing.T) {
	t.Parallel()
	w := newTimeWorld(t)
	w.primary("p1", Working, nil)
	w.workCard("p1.w1", "m1", Ready, map[string]string{fieldDueUntaken: msText(timeR0)})
	first, _ := w.plan("late", "late:untaken:p1.w1")
	w.s.Now = w.s.Now.AddDate(20, 0, 0)
	second, _ := w.plan("late", "late:untaken:p1.w1")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("the plan follows the snapshot's own time:\n%+v\n%+v", first, second)
	}
}

// BenchmarkTimeRules is the limit of 8.1 IT10: each plan is O(1) a key, at
// most 20 microseconds of Go time a key. A mix of every key the rules take is
// planned over a world of cards, at three sizes, so that a cost that grows with
// the keys shows as a rising ns/key; the cell index a snapshot builds on its
// first count is part of the first plan and of no later one.
func BenchmarkTimeRules(b *testing.B) {
	for _, perKind := range []int{100, 400, 1600} {
		b.Run(fmt.Sprintf("keys%d", perKind*9+1), func(b *testing.B) {
			w, planned, keys := timeBenchWorld(b, perKind)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for rule, ks := range planned {
					TimePlan(rule, w.s, ks, w.now)
				}
			}
			perKey := float64(b.Elapsed().Nanoseconds()) / float64(b.N*keys)
			b.ReportMetric(perKey, "ns/key")
			if perKey > 20_000 {
				b.Fatalf("%.0f ns a key, over the limit of 20 us", perKey)
			}
		})
	}
}

// timeBenchWorld is a world with perKind keys of each of nine kinds, all due,
// and the keys by rule.
func timeBenchWorld(b *testing.B, perKind int) (*timeWorld, map[string][]AgendaKey, int) {
	w := newTimeWorld(b)
	w.member("m3", true)
	w.f.Cuts = map[string]CutFact{}
	batches := map[string][]string{}
	add := func(rule, key string) { batches[rule] = append(batches[rule], key) }
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
	add("behind", "behind")
	w.f.Tick = TickFact{Backlog: 400, BehindN: 500}
	keys := 0
	planned := map[string][]AgendaKey{}
	for rule, ks := range batches {
		for i, k := range ks {
			planned[rule] = append(planned[rule], keyOf(k, uint64(i+1)))
		}
		keys += len(ks)
	}
	return w, planned, keys
}
