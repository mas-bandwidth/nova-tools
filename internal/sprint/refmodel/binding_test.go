package refmodel_test

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The differential against the real binding. A sample is loaded into the
// store's in-memory backend (every card of the four tables, placed or kept, the
// rows, the open judgments and the acknowledged conditions), and a part of the
// tick is run on it as the store runs it: store.TickPartStep through Store.Run,
// with the store's own read and the extras it loads, its own lawful filter, its
// own manifests, and the backend's guards on every entry. What the store wrote
// is read back and compared with what the reference decides: the cards and the
// rows as they stand, the notes written, the judgments open, what is left due,
// what is refused, what is said of each move. A part is run alone on a sample
// as it was loaded, at a quiet point: no other writer, no part before it.
//
// The reference calls the planners of today's tick, as the store does, so a
// change to a planner changes both sides alike and is the fixtures' to catch
// (decide_test.go states each duty's moves). What this test holds is the rest:
// that the moves are what the store writes of a plan (the bounds, the lawful
// filter, the conversion of every note and close), and that the store's read
// gives the planners what the snapshot gives them.

// The samples compared: every part on the first bindingAll (the scenarios and
// the first walks), and beyond them each part only where the reference has moves,
// up to bindingPerPart samples of it, so the parts a walk rarely reaches are
// compared too, and where what the store applies of a plan (sprint.Applied)
// changes it, so the store's filter is seen at work. At least bindingMin samples
// are compared.
const (
	bindingAll     = 80
	bindingPerPart = 40
	bindingMin     = 200
)

// loaded is a sample held by the store's in-memory backend, and the point its
// inbox was at after the load.
type loaded struct {
	tb    testing.TB
	ctx   context.Context
	st    *store.Store
	m     *store.Mem
	ops   int
	inbox string
}

// firstCell is the owned cell a kept card is written to before it is removed: a
// kept card is a record with no place.
var firstCell = map[string]string{sprint.Work: sprint.Waiting, sprint.Readers: sprint.Asked, sprint.Merge: sprint.Queued, sprint.Fleet: sprint.Ready}

func loadSample(tb testing.TB, s sample) *loaded {
	tb.Helper()
	l := &loaded{tb: tb, ctx: context.Background(), m: store.NewMem()}
	t := s.snap.Tables
	n := 0
	l.st = &store.Store{B: l.m, Names: sprint.Names{Prefix: "d-"}, Actor: t.Actor,
		Now:   func() time.Time { return s.now },
		NewID: func() string { n++; return strconv.Itoa(n) },
		Sleep: func(time.Duration) {}}
	l.must(l.st.Init(l.ctx))
	l.must(l.m.SetCoordinator(l.ctx, t.Coordinator))
	for _, name := range store.All {
		tab := t.T(name)
		stored := l.st.Names.Table(name)
		if len(tab.Rows) > 0 {
			l.must(l.m.RowsAdd(l.ctx, stored, tab.Rows))
		}
		var placed, kept, removed []ntable.BatchMemberEntry
		for _, id := range slices.Sorted(maps.Keys(tab.Cards)) {
			c := tab.Cards[id]
			e := ntable.BatchMemberEntry{ID: id, Expect: &ntable.MemberExpect{Absent: true}, Set: maps.Clone(c.Fields)}
			if c.Placed() {
				e.Create = &ntable.MemberCreateOp{Row: c.Row, Col: c.Col, Score: c.Score}
				placed = append(placed, e)
				continue
			}
			e.Create = &ntable.MemberCreateOp{Row: tab.Rows[0], Col: firstCell[name], Score: c.Score}
			kept = append(kept, e)
			removed = append(removed, ntable.BatchMemberEntry{ID: id, Remove: true,
				Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: tab.Rows[0], Col: firstCell[name]}}})
		}
		l.apply(stored, placed)
		l.apply(stored, kept)
		l.apply(stored, removed)
	}
	l.judgments(t)
	_, l.inbox, _ = l.m.Tails(l.ctx)
	return l
}

func (l *loaded) must(err error) {
	l.tb.Helper()
	if err != nil {
		l.tb.Fatal(err)
	}
}

// apply writes one manifest of members to a table.
func (l *loaded) apply(table string, members []ntable.BatchMemberEntry) {
	l.tb.Helper()
	if len(members) == 0 {
		return
	}
	l.ops++
	_, err := l.m.Apply(l.ctx, ntable.BatchManifest{Schema: 1, Table: table, Epoch: "0", ExpectedTableRevision: strconv.FormatUint(l.m.Revision(table), 10),
		OperationID: fmt.Sprintf("load-%d", l.ops), Actor: "load", Members: members})
	if err != nil {
		l.tb.Fatalf("load %s: %v", table, err)
	}
}

// judgments writes the open judgments and the acknowledged conditions as one
// committed operation: each note once, and each of its subjects that is not open
// closed again.
func (l *loaded) judgments(t *sprint.Snapshot) {
	l.tb.Helper()
	op := store.OpRecord{ID: "load", Verb: "load", At: t.Now}
	open := map[string]bool{}
	seen := map[string]bool{}
	for _, o := range slices.Concat(t.Open, t.Acked) {
		open[o.Key] = true
		if !seen[o.Note.ID] {
			seen[o.Note.ID] = true
			op.Notes = append(op.Notes, o.Note)
		}
	}
	if len(op.Notes) == 0 {
		return
	}
	for _, n := range op.Notes {
		for _, sub := range n.Subjects() {
			if k := sprint.OpenKey(n.ID, sub); !open[k] {
				op.Closes = append(op.Closes, k)
			}
		}
	}
	f, err := l.m.ReadFence(l.ctx)
	l.must(err)
	ok, err := l.m.Acquire(l.ctx, f.Gen, op)
	l.must(err)
	if !ok {
		l.tb.Fatal("load: the fence is not free")
	}
	l.must(l.m.Release(l.ctx, op, true))
}

// run is a part of the tick as the store runs it, at the time of the sample:
// its result, and what it left due.
func (l *loaded) run(s sample, part string) (store.Result, int) {
	l.tb.Helper()
	m := store.Machine{Spans: s.snap.Stopped}
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, Beats: s.snap.Beats}
	due := 0
	for _, p := range sprint.TickParts {
		if p.Name == part {
			res, err := l.st.Run(l.ctx, store.TickPartStep(p.Name, p.Fn, req, nil, nil, &due))
			l.must(err)
			return res, due
		}
	}
	l.tb.Fatalf("the tick has no part %s", part)
	return store.Result{}, 0
}

// noteKindOf is the kind of move a notification is.
var noteKindOf = map[string]string{sprint.Judgment: refmodel.KindOpen, sprint.Happened: refmodel.KindNotice, sprint.Decided: refmodel.KindDecided, sprint.Acknowledged: refmodel.KindHold}

func TestTheStoreWritesWhatTheReferenceDecides(t *testing.T) {
	t.Parallel()
	byDuty := func(ms []refmodel.Move) map[string][]refmodel.Move {
		out := map[string][]refmodel.Move{}
		for _, m := range ms {
			out[m.Duty] = append(out[m.Duty], m)
		}
		return out
	}
	withMoves, compared, filtered := map[string]int{}, map[int]bool{}, 0
	for i, s := range snapshots() {
		if !s.snap.Running {
			continue
		}
		decided := byDuty(refmodel.Decide(s.snap, s.now))
		matters := appliedMatters(s)
		for _, part := range sprint.TickParts {
			moves := decided[part.Name]
			if i >= bindingAll && !matters[part.Name] && (len(moves) == 0 || withMoves[part.Name] >= bindingPerPart) {
				continue
			}
			l := loadSample(t, s)
			res, due := l.run(s, part.Name)
			if problem := l.differs(s, moves, res, due); problem != "" {
				t.Fatalf("sample %d, part %s: %s\nthe reference:%s", i, part.Name, problem, show(moves))
			}
			compared[i] = true
			if len(moves) > 0 {
				withMoves[part.Name]++
			}
			if matters[part.Name] {
				filtered++
			}
		}
	}
	for _, part := range sprint.TickParts {
		if withMoves[part.Name] < minSamplesWithMoves {
			t.Errorf("the part %s had moves on %d samples, fewer than %d: the store was not compared on it", part.Name, withMoves[part.Name], minSamplesWithMoves)
		}
	}
	if len(compared) < bindingMin {
		t.Errorf("%d samples were compared, fewer than %d", len(compared), bindingMin)
	}
	if filtered < minSamplesWithFilter {
		t.Errorf("the store's filter changed a plan on %d of the parts compared, fewer than %d: it is not seen at work", filtered, minSamplesWithFilter)
	}
	t.Logf("%d samples compared; parts with moves, by part: %v; parts the store's filter changed: %d", len(compared), withMoves, filtered)
}

// minSamplesWithFilter is the fewest of the parts compared that sprint.Applied
// changes the plan of: the walks reach it rarely.
const minSamplesWithFilter = 5

// appliedMatters is the parts of the tick whose plan on the sample is changed by
// what the store applies of a plan: sprint.Applied, its lawful filter and its
// one judgment for each cause.
func appliedMatters(s sample) map[string]bool {
	c := s.snap.Clone()
	tabs := c.Tables
	tabs.Now = s.now
	m := store.Machine{Spans: c.Stopped}
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, Beats: c.Beats}
	out := map[string]bool{}
	if !c.Running {
		return out
	}
	for _, part := range sprint.TickParts {
		plan, _ := part.Fn(tabs, req)
		if !reflect.DeepEqual(plan, sprint.Applied(tabs, plan)) {
			out[part.Name] = true
		}
	}
	return out
}

// differs is "" when what the store wrote of a part is what the moves say, and
// else the first thing that differs.
func (l *loaded) differs(s sample, moves []refmodel.Move, res store.Result, due int) string {
	l.tb.Helper()
	want := s.snap.Clone().Tables
	if err := applyMoves(want, moves); err != nil {
		return "the moves do not apply: " + err.Error()
	}
	for _, name := range store.All {
		for _, c := range want.T(name).Cards {
			if !c.Placed() {
				c.Score = 0 // a card with no place has no score to read back from the store
			}
		}
	}
	if a, b := cards(want), cards(l.tables(want)); a != b {
		return "the cards or the rows differ (the reference, then the store):\n" + firstDifference(a, b)
	}
	if problem := l.notesDiffer(moves); problem != "" {
		return problem
	}
	if problem := l.openDiffer(s.snap.Tables, moves); problem != "" {
		return problem
	}
	var wantDue int
	var wantRefused, wantMoved []string
	for _, m := range moves {
		switch {
		case m.Kind == refmodel.KindDue && len(m.Attrs) == 1:
			wantDue, _ = strconv.Atoi(strings.TrimPrefix(m.Attrs[0], "due="))
		case m.Kind == refmodel.KindRefuse:
			wantRefused = append(wantRefused, m.Card+": "+m.Words)
		case m.Table != "" && m.Words != "":
			wantMoved = append(wantMoved, m.Words)
		}
	}
	var gotRefused []string
	for _, r := range res.Refused {
		gotRefused = append(gotRefused, r.Key+": "+r.Why)
	}
	switch {
	case due != wantDue:
		return fmt.Sprintf("the store left %d due, the reference %d", due, wantDue)
	case !sameSet(gotRefused, wantRefused):
		return fmt.Sprintf("the store refused %q, the reference %q", gotRefused, wantRefused)
	case !sameSet(res.Moved, wantMoved):
		return fmt.Sprintf("the store says it moved %q, the reference %q", res.Moved, wantMoved)
	}
	return ""
}

// sameSet says the lists hold the same texts, whatever their order.
func sameSet(a, b []string) bool {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	return slices.Equal(a, b)
}

// tables is the store's four tables as they stand: the rows, and every card
// that the reference's tables have or the store has placed.
func (l *loaded) tables(want *sprint.Snapshot) *sprint.Snapshot {
	l.tb.Helper()
	out := &sprint.Snapshot{}
	names := make([]string, len(store.All))
	for i, name := range store.All {
		names[i] = l.st.Names.Table(name)
	}
	shapes, err := l.m.Shapes(l.ctx, names)
	l.must(err)
	placed, err := l.m.CellIDs(l.ctx, shapes)
	l.must(err)
	for i, name := range store.All {
		tab := sprint.NewTable(name)
		for _, r := range shapes[i].Rows {
			tab.Rows = append(tab.Rows, r.Key)
		}
		ids := map[string]bool{}
		for id := range want.T(name).Cards {
			ids[id] = true
		}
		for _, id := range placed[names[i]] {
			ids[id] = true
		}
		all := slices.Sorted(maps.Keys(ids))
		for start := 0; start < len(all); start += ntable.LimitReadSetMembers {
			rs, err := l.m.ReadSet(l.ctx, names[i], all[start:min(start+ntable.LimitReadSetMembers, len(all))])
			l.must(err)
			for _, m := range rs.Members {
				c := &sprint.Card{ID: m.ID, Score: m.Score, Fields: m.Fields}
				if m.Placed {
					c.Row, c.Col = m.Row, m.Col
				}
				tab.Put(c)
			}
		}
		switch name {
		case sprint.Work:
			out.Work = tab
		case sprint.Readers:
			out.Readers = tab
		case sprint.Merge:
			out.Merge = tab
		case sprint.Fleet:
			out.Fleet = tab
		}
	}
	return out
}

// notesDiffer compares the notes the store wrote (its inbox after the load)
// with the moves that are notes, each as its atoms (lineOf, for each subject):
// the store groups the notes of a plan, and a note grouped is the same atoms.
// The store also writes a decided note for each judgment a plan closes; those
// are compared with the close moves.
func (l *loaded) notesDiffer(moves []refmodel.Move) string {
	l.tb.Helper()
	notes, _, err := l.m.NotesSince(l.ctx, l.inbox, 1<<20)
	l.must(err)
	var got, want, gotDecided, wantDecided []string
	for _, n := range notes {
		if n.Kind == sprint.Decided {
			for _, sub := range n.Primaries {
				gotDecided = append(gotDecided, strings.Join([]string{n.Answers, sub, n.Type, n.Stream}, "|"))
			}
			continue
		}
		got = append(got, atomsOf(lineOfNote(n), slices.Sorted(slices.Values(n.Subjects())))...)
	}
	for _, m := range moves {
		switch {
		case m.Kind == refmodel.KindClose:
			wantDecided = append(wantDecided, strings.Join([]string{m.Card, m.Subjects[0], m.Type, m.Stream}, "|"))
		case isNoteKind(m.Kind) && m.Kind != refmodel.KindDecided:
			want = append(want, atomsOf(lineOfMove(m, m.Kind), m.Subjects)...)
		}
	}
	if !sameAtoms(got, want) {
		return "the notes written differ (the store, then the reference):\n" + firstDifference(joined(got), joined(want))
	}
	if !sameAtoms(gotDecided, wantDecided) {
		return "the judgments answered differ (the store, then the closes of the reference):\n" + firstDifference(joined(gotDecided), joined(wantDecided))
	}
	return ""
}

// joined is the atoms sorted, one to a line.
func joined(atoms []string) string {
	return strings.Join(slices.Compact(slices.Sorted(slices.Values(atoms))), "\n")
}

// openDiffer compares the judgments and the holds the store has open after the
// part with the ones the sample had open, less the closes of the moves, with the
// updates made and the notes of the moves opened: each as a line and a subject.
func (l *loaded) openDiffer(before *sprint.Snapshot, moves []refmodel.Move) string {
	l.tb.Helper()
	type entry struct{ id, subject, kind, line string }
	var entries []entry
	for _, o := range slices.Concat(before.Open, before.Acked) {
		entries = append(entries, entry{o.Note.ID, o.Subject(), kindOfNote(o.Note.Kind), lineOfNote(o.Note)})
	}
	for _, m := range moves {
		switch m.Kind {
		case refmodel.KindClose:
			at := slices.IndexFunc(entries, func(e entry) bool { return e.id == m.Card && len(m.Subjects) == 1 && e.subject == m.Subjects[0] })
			if at < 0 {
				return fmt.Sprintf("the reference closes %s on %v, which was not open", m.Card, m.Subjects)
			}
			entries = slices.Delete(entries, at, at+1)
		case refmodel.KindUpdate:
			id := ""
			for _, a := range m.Attrs {
				if v, ok := strings.CutPrefix(a, "id="); ok {
					id = v
				}
			}
			found := false
			for i, e := range entries {
				if e.id == id {
					entries[i].line = lineOfMove(m, e.kind)
					found = true
				}
			}
			if !found {
				return fmt.Sprintf("the reference updates %q, which was not open", id)
			}
		case refmodel.KindOpen, refmodel.KindHold:
			for _, sub := range m.Subjects {
				entries = append(entries, entry{"", sub, m.Kind, lineOfMove(m, m.Kind)})
			}
		}
	}
	var want, got []string
	for _, e := range entries {
		want = append(want, e.line+"|"+e.subject)
	}
	open, err := l.m.OpenNotes(l.ctx)
	l.must(err)
	for _, o := range open {
		got = append(got, lineOfNote(o.Note)+"|"+o.Subject())
	}
	if !sameAtoms(got, want) {
		return "the judgments open differ (the store, then the reference):\n" + firstDifference(joined(got), joined(want))
	}
	return ""
}
