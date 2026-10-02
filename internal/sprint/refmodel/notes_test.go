package refmodel_test

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// A move carries a notification's fields, and these tests say which and how, in
// words of their own and not the reference's: what a note is, read off the type
// by reflection, and where its move holds each field of it.

// carried says how a move holds one field of a note: as one of its own fields,
// as an attr (name=value, the name the field has in the note's JSON), or not at
// all, and why.
type carried struct {
	as  string // kind, type, stream, card, words, subjects, decisions, attr, or left out
	why string // for a field left out
}

// noteCarry is every field of sprint.Note and how a move holds it. A field the
// note gains that is not here fails the test that reads the type, so a new
// field is a decision about what a move carries.
var noteCarry = map[string]carried{
	"ID":          {"left out", "the store gives every note its own id"},
	"Kind":        {"kind", ""},
	"Type":        {"type", ""},
	"Stream":      {"stream", ""},
	"Primaries":   {"subjects", ""},
	"Count":       {"attr", ""},
	"What":        {"words", ""},
	"Who":         {"attr", ""},
	"Attempt":     {"attr", ""},
	"Before":      {"attr", ""},
	"At":          {"left out", "the time a note is written at is the time Decide is given"},
	"Decisions":   {"decisions", ""},
	"Marked":      {"attr", ""},
	"Answers":     {"attr", ""},
	"Suspects":    {"attr", ""},
	"Card":        {"card", ""},
	"Other":       {"attr", ""},
	"OtherStream": {"attr", ""},
	"StreamLevel": {"attr", ""},
	"SprintLevel": {"attr", ""},
	"Needs":       {"attr", ""},
	"Review":      {"attr", ""},
	"ReviewSet":   {"attr", ""},
	"To":          {"attr", ""},
	"Hint":        {"attr", ""},
}

func TestEveryFieldOfANoteIsCarriedOrLeftOutInWords(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[sprint.Note]()
	seen := map[string]bool{}
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		seen[name] = true
		c, ok := noteCarry[name]
		switch {
		case !ok:
			t.Errorf("the note has a field %s that no test says a move carries or leaves out", name)
		case c.as == "left out" && c.why == "":
			t.Errorf("the field %s is left out of a move and says no why", name)
		case c.as == "attr" && strings.Contains(typ.Field(i).Tag.Get("json"), "-"):
			t.Errorf("the field %s is an attr but has no name in the note's JSON", name)
		}
	}
	for name := range noteCarry {
		assert.True(t, seen[name], "a test says a move carries %s, which the note does not have", name)
	}
}

// attrsOf is the fields of a note a move holds as attrs, name=value in name
// order, read off the note by reflection: a text as it is, a number in decimal,
// a flag as yes, a list of texts sorted and joined with commas, a time in UTC
// as RFC 3339; a field that is zero is left out.
func attrsOf(n sprint.Note) []string {
	v := reflect.ValueOf(n)
	var out []string
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if noteCarry[f.Name].as != "attr" {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		var text string
		switch x := v.Field(i).Interface().(type) {
		case string:
			text = x
		case int:
			if x != 0 {
				text = strconv.Itoa(x)
			}
		case bool:
			if x {
				text = "yes"
			}
		case []string:
			text = strings.Join(slices.Sorted(slices.Values(x)), ",")
		case time.Time:
			if !x.IsZero() {
				text = x.UTC().Format(time.RFC3339)
			}
		default:
			panic(fmt.Sprintf("the note's field %s is a %T: say how a move holds it", f.Name, x))
		}
		if text != "" {
			out = append(out, name+"="+text)
		}
	}
	sort.Strings(out)
	return out
}

// kindOfNote is the kind of move a note is: an open judgment, a notice, an
// answer, a hold. A kind of note the reference has no word for is its own.
func kindOfNote(kind string) string {
	if k, ok := noteKindOf[kind]; ok {
		return k
	}
	return kind
}

// isNoteKind says the kind of move is a note's.
func isNoteKind(kind string) bool {
	for _, k := range noteKindOf {
		if k == kind {
			return true
		}
	}
	return false
}

// summed are the attrs of a note that the store's grouping of notes
// (sprint.MergeNotes) sums or unites when it writes several as one: the count of
// the primaries, the most of their befores, the needs of all. A grouped note has
// the total, and the notes of a plan are not grouped, so a comparison of what
// the store wrote with what a plan says leaves them out.
var summed = []string{"count=", "before=", "needs="}

// lineOf is a note as one line of the fields a move carries of it, without its
// subjects (a note is compared once for each) and without the attrs summed.
func lineOf(kind, typ, stream, card, words string, decisions, attrs []string) string {
	attrs = slices.DeleteFunc(slices.Clone(attrs), func(a string) bool {
		return slices.ContainsFunc(summed, func(p string) bool { return strings.HasPrefix(a, p) })
	})
	return fmt.Sprintf("%s|%s|%s|%s|%q|%q|%q", kind, typ, stream, card, words, decisions, attrs)
}

// lineOfNote is the line of a note.
func lineOfNote(n sprint.Note) string {
	return lineOf(kindOfNote(n.Kind), n.Type, n.Stream, n.Card, n.What, n.Decisions, attrsOf(n))
}

// lineOfMove is the line of a move that is a note, of the kind it is given (an
// update rewrites a judgment or a hold: it is that kind, and the id it names is
// not part of the line).
func lineOfMove(m refmodel.Move, kind string) string {
	attrs := slices.DeleteFunc(slices.Clone(m.Attrs), func(a string) bool { return m.Kind == refmodel.KindUpdate && strings.HasPrefix(a, "id=") })
	return lineOf(kind, m.Type, m.Stream, m.Card, m.Words, m.Decisions, attrs)
}

// atomsOf is a line with each of the subjects: what is compared of a note, as
// the store may write the notes of a plan grouped. A note with no subject is
// its line alone.
func atomsOf(line string, subjects []string) []string {
	if len(subjects) == 0 {
		return []string{line + "|"}
	}
	var out []string
	for _, s := range subjects {
		out = append(out, line+"|"+s)
	}
	return out
}

// sameAtoms says two lists hold the same atoms: a note written twice, or two
// written as one, is the same atom.
func sameAtoms(a, b []string) bool {
	a, b = slices.Compact(slices.Sorted(slices.Values(a))), slices.Compact(slices.Sorted(slices.Values(b)))
	return slices.Equal(a, b)
}

// checkNoteMove says the move has every field of the note, each as the note
// has it, and returns what differs.
func checkNoteMove(n sprint.Note, m refmodel.Move) []string {
	var bad []string
	differ := func(field string, got, want any) {
		bad = append(bad, fmt.Sprintf("%s: the move has %q, the note %q", field, got, want))
	}
	if want := kindOfNote(n.Kind); m.Kind != want {
		differ("kind", m.Kind, want)
	}
	if m.Type != n.Type {
		differ("type", m.Type, n.Type)
	}
	if m.Stream != n.Stream {
		differ("stream", m.Stream, n.Stream)
	}
	if m.Card != n.Card {
		differ("card", m.Card, n.Card)
	}
	if m.Words != n.What {
		differ("words", m.Words, n.What)
	}
	if want := slices.Sorted(slices.Values(n.Subjects())); !slices.Equal(m.Subjects, want) {
		differ("subjects", m.Subjects, want)
	}
	if !slices.Equal(m.Decisions, n.Decisions) {
		differ("decisions", m.Decisions, n.Decisions)
	}
	if want := attrsOf(n); !slices.Equal(m.Attrs, want) {
		differ("attrs", m.Attrs, want)
	}
	return bad
}

// filled is a note with every field set, each to a value of its own: a text
// that names the field, a number, a flag, two texts out of order, a time. What
// makes a note a stream's or the sprint's is set by the caller.
func filled(kind string) sprint.Note {
	var n sprint.Note
	v := reflect.ValueOf(&n).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		switch v.Field(i).Interface().(type) {
		case string:
			v.Field(i).SetString("s-" + name)
		case int:
			v.Field(i).SetInt(int64(3 + i))
		case bool:
			v.Field(i).SetBool(true)
		case []string:
			v.Field(i).Set(reflect.ValueOf([]string{"b-" + name, "a-" + name}))
		case time.Time:
			v.Field(i).Set(reflect.ValueOf(t0.Add(time.Duration(i+1) * time.Minute)))
		default:
			panic("the note's field " + name + " is of a type the test cannot fill")
		}
	}
	n.Kind = kind
	return n
}

// A note with every field set gives a move with every field of it, of each kind
// of note and of each way a note has its subjects: its primaries, its stream,
// the sprint. It reaches the fields the walks never set (an answer, a review
// time, the suspects of a red branch).
func TestAMoveHoldsEveryFieldOfANoteThatIsSet(t *testing.T) {
	t.Parallel()
	tabs := &sprint.Snapshot{}
	for _, kind := range []string{sprint.Judgment, sprint.Happened, sprint.Decided, sprint.Acknowledged, "another kind"} {
		for way, set := range map[string]func(*sprint.Note){
			"primaries": func(n *sprint.Note) { n.StreamLevel, n.SprintLevel = false, false },
			"stream":    func(n *sprint.Note) { n.StreamLevel, n.SprintLevel = true, false },
			"sprint":    func(n *sprint.Note) { n.StreamLevel, n.SprintLevel = false, true },
		} {
			n := filled(kind)
			set(&n)
			ms := refmodel.PlanMoves(refmodel.DutyDeal, tabs, sprint.Plan{Notes: []sprint.Note{n}})
			require.Len(t, ms, 1, "%s note on its %s: %d moves", kind, way, len(ms))
			bad := checkNoteMove(n, ms[0])
			assert.Empty(t, bad, "%s note on its %s:\n  %s", kind, way, strings.Join(bad, "\n  "))
			// every field a move holds as an attr is set, but a note is a stream's or the sprint's or neither: one flag or none
			want := countCarried("attr") - 2
			if way != "primaries" {
				want++
			}
			got := len(attrsOf(n))
			assert.Equal(t, want, got, "%s note on its %s: %d attrs, want %d: the fixture leaves a field unset", kind, way, got, want)
			up := refmodel.PlanMoves(refmodel.DutyDeal, tabs, sprint.Plan{Updates: []sprint.Note{n}})
			if assert.Len(t, up, 1, "%s note on its %s: its update is not the note with its id in the attrs and its card kept: %+v", kind, way, up) {
				assert.Equal(t, refmodel.KindUpdate, up[0].Kind, "%s note on its %s: its update is not the note with its id in the attrs and its card kept: %+v", kind, way, up)
				assert.Contains(t, up[0].Attrs, "id="+n.ID, "%s note on its %s: its update is not the note with its id in the attrs and its card kept: %+v", kind, way, up)
				assert.Equal(t, n.Card, up[0].Card, "%s note on its %s: its update is not the note with its id in the attrs and its card kept: %+v", kind, way, up)
				bad := checkNoteMove(n, withKind(up[0], kindOfNote(n.Kind), n.ID))
				assert.Empty(t, bad, "%s note on its %s, as an update:\n  %s", kind, way, strings.Join(bad, "\n  "))
			}
		}
	}
}

// withKind is an update move as the note move it rewrites: of the kind of the
// note, without the id attr.
func withKind(m refmodel.Move, kind, id string) refmodel.Move {
	m.Kind = kind
	m.Attrs = slices.DeleteFunc(slices.Clone(m.Attrs), func(a string) bool { return a == "id="+id })
	return m
}

func countCarried(as string) int {
	n := 0
	for _, c := range noteCarry {
		if c.as == as {
			n++
		}
	}
	return n
}

// tickPlans is the plan each duty of today's tick makes on the sample, held to
// what the store applies, the tables read at the time of the sample: the parts
// of the tick as they are planned, the unknown machines told of, the judgments
// the failing routes call for.
func tickPlans(s sample) (map[string]sprint.Plan, *sprint.Snapshot) {
	c := s.snap.Clone()
	tabs := c.Tables
	tabs.Now = s.now
	plans := map[string]sprint.Plan{}
	if !c.Running {
		return plans, tabs
	}
	req := sprint.TickReq{Who: sprint.MachineActor, Beats: c.Beats,
		Stopped: func(from, to time.Time) time.Duration { return sprint.StoppedBetween(c.Stopped, from, to) }}
	if len(c.Untold) > 0 {
		plans[refmodel.DutyStrangers] = sprint.Applied(tabs, sprint.StrangerNotes(tabs, c.Untold))
	}
	for _, part := range sprint.TickParts {
		plan, _ := part.Fn(tabs, req)
		plans[part.Name] = sprint.Applied(tabs, plan)
	}
	if _, stale := c.Goals.NotesStale(); stale {
		plans[refmodel.DutyRemind] = sprint.Applied(tabs, sprint.RemindNotes(tabs, c.Goals, sprint.MachineActor))
	}
	return plans, tabs
}

// Each note, update and close of the plans of today's tick, on the samples, is
// converted alone and gives the move that has every field of it: what the tick
// writes is what a move says, whatever else is in the plan.
func TestTheMoveOfEachNoteAndCloseOfTodaysPlansHasEveryFieldOfIt(t *testing.T) {
	t.Parallel()
	seen := map[string]int{}
	for i, s := range snapshots() {
		plans, tabs := tickPlans(s)
		for duty, plan := range plans {
			var notes []sprint.Note
			var closes []sprint.Open
			for _, u := range plan.Units {
				notes = append(notes, u.Notes...)
				closes = append(closes, u.Closes...)
				for _, o := range u.Closes {
					one(t, i, duty, tabs, sprint.Plan{Units: []sprint.Unit{{Closes: []sprint.Open{o}}}}, o, seen, "close in a unit")
				}
			}
			notes = append(notes, plan.Notes...)
			for _, n := range notes {
				ms := refmodel.PlanMoves(duty, tabs, sprint.Plan{Notes: []sprint.Note{n}})
				require.Len(t, ms, 1, "sample %d, %s: a note gives %d moves", i, duty, len(ms))
				bad := checkNoteMove(n, ms[0])
				require.Empty(t, bad, "sample %d, %s: the move of the note %q lacks:\n  %s", i, duty, n.What, strings.Join(bad, "\n  "))
				seen[ms[0].Kind]++
				if len(n.Decisions) > 0 {
					seen["a note with decisions"]++
				}
			}
			for _, o := range plan.Closes {
				one(t, i, duty, tabs, sprint.Plan{Closes: []sprint.Open{o}}, o, seen, "close")
			}
			for _, n := range plan.Updates {
				ms := refmodel.PlanMoves(duty, tabs, sprint.Plan{Updates: []sprint.Note{n}})
				require.Len(t, ms, 1, "sample %d, %s: an update is not the move that names the judgment it rewrites: %s", i, duty, show(ms))
				require.Equal(t, refmodel.KindUpdate, ms[0].Kind, "sample %d, %s: an update is not the move that names the judgment it rewrites: %s", i, duty, show(ms))
				require.Contains(t, ms[0].Attrs, "id="+n.ID, "sample %d, %s: an update is not the move that names the judgment it rewrites: %s", i, duty, show(ms))
				bad := checkNoteMove(n, withKind(ms[0], kindOfNote(n.Kind), n.ID))
				require.Empty(t, bad, "sample %d, %s: the update of %q lacks:\n  %s", i, duty, n.What, strings.Join(bad, "\n  "))
				seen["update"]++
			}
		}
	}
	for _, kind := range []string{refmodel.KindOpen, refmodel.KindNotice, refmodel.KindHold, "update", "close", "close in a unit", "a note with decisions"} {
		assert.GreaterOrEqual(t, seen[kind], minSamplesWithMoves, "only %d of %s in the plans of the samples: the test does not try them (%v)", seen[kind], kind, seen)
	}
}

// one says a close alone is the one move that closes that judgment on that
// subject, and counts it.
func one(t *testing.T, i int, duty string, tabs *sprint.Snapshot, p sprint.Plan, o sprint.Open, seen map[string]int, how string) {
	t.Helper()
	ms := refmodel.PlanMoves(duty, tabs, p)
	require.Len(t, ms, 1, "sample %d, %s: a %s of %s on %s is not the one move that closes it: %s", i, duty, how, o.Note.ID, o.Subject(), show(ms))
	require.Equal(t, refmodel.KindClose, ms[0].Kind, "sample %d, %s: a %s of %s on %s is not the one move that closes it: %s", i, duty, how, o.Note.ID, o.Subject(), show(ms))
	require.Equal(t, o.Note.ID, ms[0].Card, "sample %d, %s: a %s of %s on %s is not the one move that closes it: %s", i, duty, how, o.Note.ID, o.Subject(), show(ms))
	require.Equal(t, []string{o.Subject()}, ms[0].Subjects, "sample %d, %s: a %s of %s on %s is not the one move that closes it: %s", i, duty, how, o.Note.ID, o.Subject(), show(ms))
	require.Equal(t, o.Note.Type, ms[0].Type, "sample %d, %s: a %s of %s on %s is not the one move that closes it: %s", i, duty, how, o.Note.ID, o.Subject(), show(ms))
	require.Equal(t, o.Note.Stream, ms[0].Stream, "sample %d, %s: a %s of %s on %s is not the one move that closes it: %s", i, duty, how, o.Note.ID, o.Subject(), show(ms))
	seen[how]++
}
