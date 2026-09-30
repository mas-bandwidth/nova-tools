package stepbuild

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Rows and members. A member's destination row must be one the step adds or
// one that is there, and a row the step deletes must be empty once the members
// that leave it have gone (section 3). The tests hold the builder to this:
// applying the steps in order is applying the whole, wherever the bounds cut.

// world is the part of Layer 1's state that rows and members touch, and the
// rules of section 3 that decide them, written here to be run on the steps:
// the tables' rows in the order they were added, and where each member is.
type world struct {
	rows  map[string][]string  // table -> rows, in the order they were added
	place map[[2]string]string // (table, id) -> "row:col"; absent when the member has no place
}

func (w world) clone() world {
	c := world{rows: map[string][]string{}, place: map[[2]string]string{}}
	for t, rs := range w.rows {
		c.rows[t] = append([]string(nil), rs...)
	}
	for k, v := range w.place {
		c.place[k] = v
	}
	return c
}

func (w world) hasRow(table, row string) bool {
	for _, r := range w.rows[table] {
		if r == row {
			return true
		}
	}
	return false
}

// applyStep applies the entries as one atomic step: the new world and "" when
// it is legal, else the world unchanged and the code it is refused with. A
// destination must be in the rows the step adds and the ones already there
// less the ones it deletes; a source must be in the rows already there; a
// member is placed once; a row the step deletes must be empty after every
// member of the step has moved or left, and no member is put into it.
func (w world) applyStep(entries []Entry) (world, string) {
	type key = [2]string
	adds, dels := map[key]bool{}, map[key]bool{}
	var added []key
	for _, e := range entries {
		if e.Kind != KindRows {
			continue
		}
		for _, r := range e.Add {
			if !adds[key{e.Table, r}] {
				added = append(added, key{e.Table, r})
			}
			adds[key{e.Table, r}] = true
		}
		for _, r := range e.Del {
			dels[key{e.Table, r}] = true
		}
	}
	for k := range adds {
		if dels[k] {
			return w, "ROWCONFLICT"
		}
	}
	prospective := func(table, row string) string {
		switch {
		case dels[key{table, row}]:
			return "ROWCONFLICT"
		case !w.hasRow(table, row) && !adds[key{table, row}]:
			return "NOROW"
		}
		return ""
	}
	final := map[key]string{} // members the step places or unplaces
	named := map[key]bool{}
	for _, e := range entries {
		if e.Kind == KindRows {
			continue
		}
		for _, id := range e.IDs {
			k := key{e.Table, id}
			if named[k] {
				return w, "TWICE"
			}
			named[k] = true
			at, placed := w.place[k]
			if e.Kind == KindCreate {
				if _, exists := w.place[k]; exists {
					return w, "EXISTS"
				}
				if code := prospective(e.Table, rowOf(e.To)); code != "" {
					return w, code
				}
				final[k] = e.To
				continue
			}
			switch {
			case !placed:
				return w, "MISSING"
			case at != e.From:
				return w, "PLACE"
			case !w.hasRow(e.Table, rowOf(e.From)):
				return w, "NOROW"
			}
			switch e.Kind {
			case KindMove:
				dst := e.To
				if dst == "" {
					dst = e.From
				}
				if code := prospective(e.Table, rowOf(dst)); code != "" {
					return w, code
				}
				final[k] = dst
			case KindRemove:
				final[k] = ""
			}
		}
	}
	out := w.clone()
	for k, dst := range final {
		if dst == "" {
			delete(out.place, k)
		} else {
			out.place[k] = dst
		}
	}
	for k := range dels {
		for m, at := range out.place {
			if m[0] == k[0] && rowOf(at) == k[1] {
				return w, "OCCUPIED"
			}
		}
	}
	for _, k := range added {
		if !out.hasRow(k[0], k[1]) {
			out.rows[k[0]] = append(out.rows[k[0]], k[1])
		}
	}
	for k := range dels {
		var keep []string
		for _, r := range out.rows[k[0]] {
			if r != k[1] {
				keep = append(keep, r)
			}
		}
		out.rows[k[0]] = keep
	}
	return out, ""
}

// flat is the entries of a step, attached guards included, as the model runs
// them.
func flat(s Step) []Entry {
	out := make([]Entry, len(s.Entries))
	for i, p := range s.Entries {
		out[i] = p.Entry
	}
	return out
}

// wholeOf is the input as one step: each entry with its guards ahead of it.
func wholeOf(in []Entry) []Entry {
	var out []Entry
	for _, e := range in {
		out = append(out, e.Guards...)
		e.Guards, e.Notes = nil, nil
		if e.Kind != KindNote {
			out = append(out, e)
		}
	}
	return out
}

// run applies the steps in order to w and says where it ends: the world, and
// the code of the step that was refused, or "".
func (w world) run(steps []Step) (world, string) {
	for i, s := range steps {
		var code string
		if w, code = w.applyStep(flat(s)); code != "" {
			return w, fmt.Sprintf("step %d: %s", i+1, code)
		}
	}
	return w, ""
}

func (w world) String() string {
	var ts []string
	for t := range w.rows {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	var sb strings.Builder
	for _, t := range ts {
		fmt.Fprintf(&sb, "%s=%v ", t, w.rows[t])
	}
	var ms []string
	for k, v := range w.place {
		ms = append(ms, fmt.Sprintf("%s/%s@%s", k[0], k[1], v))
	}
	sort.Strings(ms)
	return sb.String() + strings.Join(ms, " ")
}

// rowsEntry, mvTo and the like: the entries of the hand-written cases.
func rowsEntry(table string, add, del []string) Entry {
	return Entry{Kind: KindRows, Table: table, Add: add, Del: del}
}

func moveTo(table, from, to string, ids ...string) Entry {
	return Entry{Kind: KindMove, Table: table, From: from, To: to, IDs: ids}
}

func createAt(table, to string, ids ...string) Entry {
	scores := make([]string, len(ids))
	for i := range scores {
		scores[i] = "1"
	}
	return Entry{Kind: KindCreate, Table: table, To: to, IDs: ids, Scores: scores}
}

// placed is the input entries of the steps, in the order they were placed.
func placed(steps []Step) []int {
	var out []int
	for _, s := range steps {
		for _, p := range s.Entries {
			if !p.Guard && (len(out) == 0 || out[len(out)-1] != p.Source) {
				out = append(out, p.Source)
			}
		}
	}
	return out
}

func entriesBound(n int) Config {
	c := cfg()
	c.Bounds = Contract()
	c.Bounds.Entries = n
	return c
}

// A rows entry that deletes a row starts a new step when the step holds a
// member entry that names the row, as a source or a destination.
func TestARowsDeleteStartsANewStepAfterTheMembersThatNameItsRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries []Entry
		steps   int
	}{
		{"a move out of the row", []Entry{moveTo("t", "r:c", "s:c", "x"), rowsEntry("t", nil, []string{"r"})}, 2},
		{"a move into the row", []Entry{moveTo("t", "s:c", "r:c", "x"), rowsEntry("t", nil, []string{"r"})}, 2},
		{"a remove out of the row", []Entry{{Kind: KindRemove, Table: "t", From: "r:c", IDs: []string{"x"}}, rowsEntry("t", nil, []string{"r"})}, 2},
		{"a guard on the row", []Entry{gdAt("t", "r:c", "x"), rowsEntry("t", nil, []string{"r"})}, 2},
		{"a create into the row", []Entry{createAt("t", "r:c", "x"), rowsEntry("t", nil, []string{"r"})}, 2},
		{"a move in another table", []Entry{moveTo("u", "r:c", "s:c", "x"), rowsEntry("t", nil, []string{"r"})}, 1},
		{"a move of another row", []Entry{moveTo("t", "q:c", "s:c", "x"), rowsEntry("t", nil, []string{"r"})}, 1},
		{"a delete of another row after the move", []Entry{moveTo("t", "r:c", "s:c", "x"), rowsEntry("t", nil, []string{"q"})}, 1},
		{"an add of the row", []Entry{moveTo("t", "r:c", "s:c", "x"), rowsEntry("t", []string{"r"}, nil)}, 1},
	} {
		steps := must(t, cfg(), tc.entries)
		if len(steps) != tc.steps {
			t.Errorf("%s: %d steps, want %d", tc.name, len(steps), tc.steps)
			continue // a step that is not there is a failure, never an index out of range
		}
		if tc.steps == 2 && (len(steps[0].Entries) != 1 || steps[1].Entries[0].Kind != KindRows) {
			t.Errorf("%s: the delete does not lead the second step: %v %v", tc.name, kinds(steps[0]), kinds(steps[1]))
		}
	}
	// The guards a member entry carries are member entries too.
	owner := moveTo("t", "q:c", "s:c", "x")
	owner.Guards = []Entry{gdAt("t", "r:c", "g")}
	if steps := must(t, cfg(), []Entry{owner, rowsEntry("t", nil, []string{"r"})}); len(steps) != 2 {
		t.Errorf("a guard of the entry that names the row: %d steps", len(steps))
	}
	// Only the delete is kept apart: a step that holds the delete takes the
	// next member entry (it goes in a later step only when it has a reason).
	steps := must(t, cfg(), []Entry{rowsEntry("t", nil, []string{"r"}), moveTo("t", "q:c", "s:c", "x")})
	if len(steps) != 1 {
		t.Errorf("a delete and then a member entry of other rows: %d steps", len(steps))
	}
}

func gdAt(table, from string, ids ...string) Entry {
	return Entry{Kind: KindGuard, Table: table, From: from, IDs: ids}
}

// An input that is one step at the contract's bounds is the same step, or the
// same steps in the same order, wherever the bounds cut it: the two entries of
// each pair below are cut apart by Entries=1 and together by the contract's.
func TestRowsAndMembersGiveTheSameResultWhereverTheStepsAreCut(t *testing.T) {
	t.Parallel()
	start := world{
		rows:  map[string][]string{"t": {"r", "q"}},
		place: map[[2]string]string{{"t", "x"}: "r:c", {"t", "z"}: "r:c"},
	}
	for _, tc := range []struct {
		name    string
		entries []Entry
		code    string // where the whole is refused: "" when it is legal
		order   []int  // the order the entries are placed in
	}{
		{
			// A member entry ahead of the rows entry that adds its destination:
			// placed after it, so it is legal in every cut (it fails NOROW when
			// the step that holds it is applied before the rows are added).
			name:    "a create into a row a later entry adds",
			entries: []Entry{createAt("t", "n:c", "y"), rowsEntry("t", []string{"n"}, nil)},
			order:   []int{1, 0},
		},
		{
			name:    "a move into a row a later entry adds",
			entries: []Entry{moveTo("t", "r:c", "n:c", "x"), rowsEntry("t", []string{"n"}, nil)},
			order:   []int{1, 0},
		},
		{
			// A rows entry that deletes a row ahead of the member entries that
			// empty it: placed after them.
			name:    "a delete of a row a later entry empties",
			entries: []Entry{rowsEntry("t", nil, []string{"r"}), moveTo("t", "r:c", "q:c", "x", "z")},
			order:   []int{1, 0},
		},
		{
			name:    "a delete of a row that two later moves empty",
			entries: []Entry{rowsEntry("t", nil, []string{"r"}), moveTo("t", "r:c", "q:c", "x"), moveTo("t", "r:c", "q:c", "z")},
			order:   []int{1, 2, 0},
		},
		{
			name:    "a delete of a row that is not emptied",
			entries: []Entry{moveTo("t", "r:c", "q:c", "x"), rowsEntry("t", nil, []string{"r"})},
			code:    "OCCUPIED",
			order:   []int{0, 1},
		},
		{
			name:    "a move to a row nobody adds",
			entries: []Entry{moveTo("t", "r:c", "n:c", "x")},
			code:    "NOROW",
			order:   []int{0},
		},
		{
			name:    "a move into a row that is deleted",
			entries: []Entry{moveTo("t", "r:c", "q:c", "x"), rowsEntry("t", nil, []string{"q"})},
			code:    "ROWCONFLICT",
			order:   []int{0, 1},
		},
		{
			name:    "the rows in order, the members after",
			entries: []Entry{rowsEntry("t", []string{"n"}, nil), moveTo("t", "r:c", "n:c", "x", "z"), rowsEntry("t", nil, []string{"r"})},
			order:   []int{0, 1, 2},
		},
		{
			// The rename of a row: one rows entry adds the new row and deletes the
			// old, and the moves between them. Its adds come first, then the moves,
			// then its deletes: legal in every cut, as the whole is.
			name:    "a move to a new row, and the row it left deleted, by one rows entry",
			entries: []Entry{rowsEntry("t", []string{"n"}, []string{"r"}), moveTo("t", "r:c", "n:c", "x", "z")},
			order:   []int{0, 1, 0},
		},
	} {
		want, wantCode := start.applyStep(wholeOf(tc.entries))
		if wantCode != tc.code {
			t.Fatalf("%s: the model refuses the whole with %q, the case says %q", tc.name, wantCode, tc.code)
		}
		for n := 1; n <= len(tc.entries)+2; n++ {
			steps := must(t, entriesBound(n), tc.entries)
			if got := placed(steps); !reflect.DeepEqual(got, tc.order) {
				t.Errorf("%s, %d entries a step: placed %v, want %v", tc.name, n, got, tc.order)
			}
			got, code := start.run(steps)
			if (code == "") != (tc.code == "") {
				t.Errorf("%s, %d entries a step: %d steps end %q, the whole ends %q", tc.name, n, len(steps), code, wantCode)
			}
			if code == "" && got.String() != want.String() {
				t.Errorf("%s, %d entries a step: ends in\n%s\nthe whole ends in\n%s", tc.name, n, got, want)
			}
		}
	}
}

// A member entry that is late for a row keeps its place among the entries that
// name its members: they are placed after it, however far it goes.
func TestAnEntryThatWaitsForItsRowKeepsMembersInOrder(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		moveTo("t", "r:c", "n:c", "x"), // waits for the rows entry below
		moveTo("t", "n:c", "s:c", "x"), // names x again: after the first
		moveTo("t", "q:c", "q:d", "z"), // free: it is placed first
		rowsEntry("t", []string{"n", "s"}, nil),
	}
	steps := must(t, cfg(), entries)
	if got, want := placed(steps), []int{2, 3, 0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("placed %v, want %v", got, want)
	}
	// The entries keep their input indices as their sources.
	for _, s := range steps {
		for _, p := range s.Entries {
			if !reflect.DeepEqual(p.IDs, entries[p.Source].IDs) {
				t.Fatalf("source %d holds %v", p.Source, p.IDs)
			}
		}
	}
	// A cursor is a position in the order of placement, and still names a step.
	for i, s := range steps {
		rest, err := After(steps, s.Cursor)
		if err != nil || len(rest) != len(steps)-i-1 {
			t.Fatalf("After step %d: %d steps, %v", i+1, len(rest), err)
		}
	}
	// x is named twice, so the second entry that names it starts the second
	// step; the entry placed last, input entry 1, is at position 3.
	last := steps[len(steps)-1].Cursor
	if want := (Cursor{Entry: 3, Done: 1, Table: "t", ID: "x"}); len(steps) != 2 || last != want {
		t.Fatalf("%d steps, last cursor %+v, want %+v", len(steps), last, want)
	}
}

// An input already in order is placed as it stands.
func TestAnInputInOrderIsPlacedAsItStands(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		rowsEntry("t", []string{"n"}, nil),
		moveTo("t", "r:c", "n:c", "x"),
		moveTo("t", "q:c", "r:c", "z"),
		rowsEntry("t", nil, []string{"q"}),
	}
	steps := must(t, cfg(), entries)
	if got, want := placed(steps), []int{0, 1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("placed %v, want %v", got, want)
	}
	if len(steps) != 2 || steps[1].Entries[0].Kind != KindRows {
		t.Fatalf("the delete of q leads a step of its own: %d steps", len(steps))
	}
	if steps[1].Cursor != (Cursor{Entry: 3, Done: 1, Table: "t", ID: "q"}) {
		t.Fatalf("%+v", steps[1].Cursor)
	}
}

// A rows entry that deletes a row its own guards read is refused: the guard
// keeps its member where it is, and the row is never empty.
func TestARowsEntryThatDeletesARowItsOwnGuardNamesIsRefused(t *testing.T) {
	t.Parallel()
	e := rowsEntry("t", nil, []string{"r"})
	e.Guards = []Entry{gdAt("t", "r:c", "g")}
	_, err := Build(cfg(), []Entry{e})
	var ie *InputError
	if !errors.As(err, &ie) || ie.Field != "del" || ie.Entry != 0 {
		t.Fatalf("%v", err)
	}
	// A guard in another table, or on another row, is no such thing.
	e.Guards = []Entry{gdAt("u", "r:c", "g"), gdAt("t", "q:c", "h")}
	if steps := must(t, cfg(), []Entry{e}); len(steps) != 1 {
		t.Fatalf("%d steps", len(steps))
	}
}

// Entries that wait on each other refuse the build, naming the first of them:
// a rows entry that adds a row a member entry goes to, and guards the member
// that entry moves, waits for the entry that has to come after it.
func TestEntriesThatWaitOnEachOtherRefuseTheBuild(t *testing.T) {
	t.Parallel()
	adds := rowsEntry("t", []string{"n"}, nil)
	adds.Guards = []Entry{gdAt("t", "r:c", "x")} // x is named by the move below: it goes first
	entries := []Entry{
		{Kind: KindNote, Notes: []Note{{About: []string{"p"}}}},
		moveTo("t", "r:c", "n:c", "x"), // waits for the row n, which the next entry adds
		adds,                           // waits for the entry that names x before it
	}
	steps, err := Build(cfg(), entries)
	var ie *InputError
	if steps != nil || !errors.As(err, &ie) || ie.Entry != 1 || ie.Field != "add/del" || !errors.Is(err, ErrInput) || !strings.Contains(ie.Reason, "wait on each other") {
		t.Fatalf("%v (%d steps)", err, len(steps))
	}
	// Without the guard on x the two are ordered, the rows entry first.
	adds.Guards = nil
	steps = must(t, cfg(), []Entry{entries[1], adds})
	if got, want := placed(steps), []int{1, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("placed %v, want %v", got, want)
	}
	// A rows entry that adds a row and deletes another does not wait on itself:
	// the builder splits it (TestARowRenameIsPlacedAsTheAddsTheMovesAndTheDeletes).
	rename := rowsEntry("t", []string{"n"}, []string{"r"})
	if _, err := Build(cfg(), []Entry{rename, moveTo("t", "r:c", "n:c", "x")}); err != nil {
		t.Fatalf("a rename: %v", err)
	}
}

// The rename of a row: one rows entry adds the new row and deletes the old one,
// and the members move between them. The whole is legal (n is added, r emptied
// in the same step), so it is legal in every cut: the builder places the entry's
// adds, then the moves, then the entry's deletes, as the two wire entries of the
// adds and of the deletes.
func TestARowRenameIsPlacedAsTheAddsTheMovesAndTheDeletes(t *testing.T) {
	t.Parallel()
	start := world{
		rows:  map[string][]string{"t": {"r", "q"}},
		place: map[[2]string]string{{"t", "x"}: "r:c", {"t", "z"}: "r:c", {"t", "g"}: "q:c"},
	}
	rename := rowsEntry("t", []string{"n"}, []string{"r"})
	rename.Guards = []Entry{gdAt("t", "q:c", "g")}
	rename.Notes = []Note{{Meta: map[string]string{"n": "1"}, About: []string{"p"}}}
	in := []Entry{rename, moveTo("t", "r:c", "n:c", "x", "z")}
	want, wantCode := start.applyStep(wholeOf(in))
	if wantCode != "" {
		t.Fatalf("the whole is refused: %s", wantCode)
	}
	// At the contract's bounds: the adds and the moves in one step; the deletes
	// lead the next (a delete starts a step that holds a member entry naming its row).
	steps := must(t, cfg(), in)
	if len(steps) != 2 {
		t.Fatalf("%d steps", len(steps))
	}
	if got := kinds(steps[0]); !reflect.DeepEqual(got, []string{"guard", "rows", "move"}) || !reflect.DeepEqual(steps[0].Entries[1].Add, []string{"n"}) || steps[0].Entries[1].Del != nil {
		t.Fatalf("the first step: %v, %+v", got, steps[0].Entries)
	}
	if got := kinds(steps[1]); !reflect.DeepEqual(got, []string{"guard", "rows"}) || !reflect.DeepEqual(steps[1].Entries[1].Del, []string{"r"}) || steps[1].Entries[1].Add != nil {
		t.Fatalf("the second step: %v, %+v", got, steps[1].Entries)
	}
	// The guards travel with both halves, once per step; the notes follow the last.
	if len(steps[0].Notes) != 0 || len(steps[1].Notes) != 1 || steps[1].Notes[0].Source != 0 {
		t.Fatalf("the notes: %d in the first step, %+v in the second", len(steps[0].Notes), steps[1].Notes)
	}
	if got := placed(steps); !reflect.DeepEqual(got, []int{0, 1, 0}) {
		t.Fatalf("placed %v", got)
	}
	// In every cut the steps end where the whole ends (a step holds at least an
	// entry and its guard).
	for n := 2; n <= 5; n++ {
		steps := must(t, entriesBound(n), in)
		got, code := start.run(steps)
		if code != "" || got.String() != want.String() {
			t.Errorf("%d entries a step: %d steps end %q in %s, the whole ends in %s", n, len(steps), code, got, want)
		}
		if p := placed(steps); !reflect.DeepEqual(p, []int{0, 1, 0}) {
			t.Errorf("%d entries a step: placed %v", n, p)
		}
	}
	one := Contract()
	one.Candidates, one.RowPairs = 1, 1
	c := cfg()
	c.Bounds = one
	steps = must(t, c, in)
	if got, code := start.run(steps); code != "" || got.String() != want.String() {
		t.Errorf("a candidate and a row a step: %d steps end %q in %s, the whole ends in %s", len(steps), code, got, want)
	}
	// The rename of the same members in the other direction, placed by one entry
	// that deletes first in the input: the same three pieces.
	back := []Entry{moveTo("t", "r:c", "n:c", "x", "z"), rowsEntry("t", []string{"n"}, []string{"r"})}
	steps = must(t, cfg(), back)
	if got, code := start.run(steps); code != "" || got.String() != want.String() {
		t.Errorf("the rename after its moves: %d steps end %q in %s", len(steps), code, got)
	}
	// A rows entry that adds and deletes, with nothing between, is one wire entry.
	steps = must(t, cfg(), []Entry{rowsEntry("t", []string{"n"}, []string{"q"})})
	if len(steps) != 1 || len(steps[0].Entries) != 1 || steps[0].Entries[0].Add == nil || steps[0].Entries[0].Del == nil {
		t.Fatalf("an entry that needs no split is split: %+v", steps)
	}
}

// A rows entry that deletes a row, and after it a member entry whose destination
// is that row: the whole is ROWCONFLICT (a delete with an incoming member), and
// however the bounds cut it the outcome is the same: the delete lands, and the
// member entry is refused NOROW in the step after it. It no longer depends on
// whether the two share a step.
func TestAMemberEntryIntoARowThatAnEarlierEntryDeletesStartsANewStep(t *testing.T) {
	t.Parallel()
	start := world{
		rows:  map[string][]string{"t": {"r", "q"}},
		place: map[[2]string]string{{"t", "x"}: "q:c"},
	}
	for _, tc := range []struct {
		name string
		in   []Entry
	}{
		{"a create into the row", []Entry{rowsEntry("t", nil, []string{"r"}), createAt("t", "r:c", "y")}},
		{"a move into the row", []Entry{rowsEntry("t", nil, []string{"r"}), moveTo("t", "q:c", "r:c", "x")}},
	} {
		if _, code := start.applyStep(wholeOf(tc.in)); code != "ROWCONFLICT" {
			t.Fatalf("%s: the whole ends %q", tc.name, code)
		}
		var end string
		for _, bd := range []Bounds{Contract(), entriesBound(1).Bounds, entriesBound(2).Bounds, entriesBound(3).Bounds, func() Bounds { b := Contract(); b.Candidates, b.RowPairs = 1, 1; return b }()} {
			c := cfg()
			c.Bounds = bd
			steps := must(t, c, tc.in)
			if len(steps) != 2 || steps[0].Entries[0].Kind != KindRows || len(steps[0].Entries) != 1 {
				t.Errorf("%s, entries %d: %d steps, the delete does not lead alone: %v", tc.name, bd.Entries, len(steps), kinds(steps[0]))
				continue
			}
			got, code := start.run(steps)
			if code != "step 2: NOROW" || got.String() != "t=[q] t/x@q:c" {
				t.Errorf("%s, entries %d: the steps end %q in %s", tc.name, bd.Entries, code, got)
			}
			if end != "" && end != code+got.String() {
				t.Errorf("%s, entries %d: the outcome depends on the cut: %q, before %q", tc.name, bd.Entries, code+got.String(), end)
			}
			end = code + got.String()
		}
	}
	// The mirror of the rule for a delete after the members that name its row: an
	// add of the row is not kept apart from the members that go into it.
	if steps := must(t, cfg(), []Entry{rowsEntry("t", []string{"n"}, nil), createAt("t", "n:c", "y")}); len(steps) != 1 {
		t.Errorf("an add and then a create into the row: %d steps", len(steps))
	}
	// A move into another row, or another table, is not kept apart.
	for _, e := range []Entry{createAt("t", "q:c", "y"), createAt("u", "r:c", "y")} {
		if steps := must(t, cfg(), []Entry{rowsEntry("t", nil, []string{"r"}), e}); len(steps) != 1 {
			t.Errorf("a delete of r and then %s at %s: %d steps", e.Table, e.To, len(steps))
		}
	}
}

// The randomly built worlds and inputs of the property below: two tables of
// four rows named r0..r3, two columns, six members named m0..m5 in each; each
// member of each table is named by at most one entry or guard of an input, and
// no row is added by one entry and deleted by another, so that the input is
// legal as one step of section 3 but for what rows and members do to each
// other.
type worldGen struct{ r *rand.Rand }

var (
	worldTables = []string{"t", "u"}
	worldCols   = []string{"c", "d"}
)

func (g worldGen) row() string { return "r" + strconv.Itoa(g.r.IntN(5)) } // r4 is never there
func (g worldGen) cell() string {
	return g.row() + ":" + worldCols[g.r.IntN(len(worldCols))]
}

func (g worldGen) world() world {
	w := world{rows: map[string][]string{}, place: map[[2]string]string{}}
	for _, t := range worldTables {
		w.rows[t] = []string{}
		for _, i := range g.r.Perm(4) {
			if g.r.IntN(4) > 0 {
				w.rows[t] = append(w.rows[t], "r"+strconv.Itoa(i))
			}
		}
		for i := 0; i < 6; i++ {
			if g.r.IntN(10) < 7 && len(w.rows[t]) > 0 {
				row := w.rows[t][g.r.IntN(len(w.rows[t]))]
				w.place[[2]string{t, "m" + strconv.Itoa(i)}] = row + ":" + worldCols[g.r.IntN(len(worldCols))]
			}
		}
	}
	return w
}

func (g worldGen) input(w world) []Entry {
	taken := map[[2]string]bool{}
	rowDir := map[[2]string]int{}
	pick := func(table string, want func(placed bool) bool) (string, bool) {
		for _, i := range g.r.Perm(6) {
			id := "m" + strconv.Itoa(i)
			_, placed := w.place[[2]string{table, id}]
			if !taken[[2]string{table, id}] && want(placed) {
				taken[[2]string{table, id}] = true
				return id, true
			}
		}
		return "", false
	}
	var out []Entry
	for n := 1 + g.r.IntN(5); len(out) < n; {
		table := worldTables[g.r.IntN(len(worldTables))]
		var e Entry
		switch p := g.r.IntN(10); {
		case p < 3: // rows
			var add, del []string
			for i, k := 0, 1+g.r.IntN(2); i < k; i++ {
				row := g.row()
				dir := 1
				if g.r.IntN(2) == 0 {
					dir = 2
				}
				if d := rowDir[[2]string{table, row}]; d != 0 && d != dir {
					continue
				}
				rowDir[[2]string{table, row}] = dir
				if dir == 1 {
					add = append(add, row)
				} else {
					del = append(del, row)
				}
			}
			if len(add)+len(del) == 0 {
				continue
			}
			e = rowsEntry(table, add, del)
		case p < 4: // create
			id, ok := pick(table, func(placed bool) bool { return !placed })
			if !ok {
				continue
			}
			e = createAt(table, g.cell(), id)
		default: // move, remove, guard
			id, ok := pick(table, func(placed bool) bool { return placed })
			if !ok {
				continue
			}
			from := w.place[[2]string{table, id}]
			switch p {
			case 4, 5, 6:
				e = moveTo(table, from, g.cell(), id)
			case 7:
				e = Entry{Kind: KindRemove, Table: table, From: from, IDs: []string{id}}
			default:
				e = gdAt(table, from, id)
			}
		}
		if e.Kind != KindRows && e.Kind != KindGuard && g.r.IntN(5) == 0 {
			if gid, ok := pick(table, func(placed bool) bool { return placed }); ok {
				e.Guards = []Entry{gdAt(table, w.place[[2]string{table, gid}], gid)}
			}
		}
		out = append(out, e)
	}
	return out
}

// The property: for a random world and a random input, the whole, applied as
// one step, and the steps the builder makes of it, applied in order, end the
// same way, whichever bound cuts them: both legal and in the same world, or
// both refused. The bounds are cut points: one entry a step, two, three and so
// on, and a step of one candidate.
func TestRowsAndMembersAtEveryCutPoint(t *testing.T) {
	t.Parallel()
	const inputs, shards = 6000, 4
	var results [shards][4]int // per shard: inputs, moved, split rows entries, legal wholes
	for shard := 0; shard < shards; shard++ {
		t.Run(strconv.Itoa(shard), func(t *testing.T) {
			t.Parallel()
			for seed := uint64(shard + 1); seed <= inputs; seed += shards {
				g := worldGen{rand.New(rand.NewPCG(seed, 0x30f1d))}
				w := g.world()
				in := g.input(w)
				bounds := []Bounds{Contract()}
				most := 1 // the entries a step must hold: an entry and its guards
				for _, e := range in {
					most = max(most, 1+len(e.Guards))
				}
				for n := most; n <= len(in)+most; n++ {
					b := Contract()
					b.Entries = n
					bounds = append(bounds, b)
				}
				one := Contract()
				one.Candidates, one.RowPairs = 1, 1
				bounds = append(bounds, one)

				results[shard][0]++
				want, wantCode := w.applyStep(wholeOf(in))
				if wantCode == "" {
					results[shard][3]++
				}
				for _, bd := range bounds {
					c := cfg()
					c.Bounds = bd
					steps, err := Build(c, in)
					var ie *InputError
					if errors.As(err, &ie) && ie.Field == "add/del" {
						t.Fatalf("seed %d: entries that wait on each other: %v\n%s", seed, err, describe(w, in))
					}
					if bd == bounds[0] {
						if _, split, _ := refPlacement(in); split {
							results[shard][2]++
						}
					}
					if err != nil {
						t.Fatalf("seed %d: %v\n%s", seed, err, describe(w, in))
					}
					if bd == bounds[0] {
						for i, o := range placed(steps) {
							if i != o {
								results[shard][1]++
								break
							}
						}
					}
					got, code := w.run(steps)
					if (code == "") != (wantCode == "") || (code == "" && got.String() != want.String()) {
						t.Fatalf("seed %d, bounds %+v: the whole ends %q in %s; the %d steps end %q in %s\n%s", seed, bd, wantCode, want, len(steps), code, got, describe(w, in))
					}
				}
			}
		})
	}
	t.Cleanup(func() {
		var total [4]int
		for _, r := range results {
			for i := range total {
				total[i] += r[i]
			}
		}
		// The inputs must exercise what is proved: entries moved, legal wholes
		// (the cuts of a legal whole must all be legal), and rows entries that
		// add and delete split to be placed (the renames of a row).
		if total[0] != inputs || total[1] < inputs/50 || total[3] < inputs/5 || total[2] == 0 {
			t.Errorf("%d inputs, %d placed out of order, %d with a rows entry split, %d legal wholes", total[0], total[1], total[2], total[3])
		}
	})
}

func describe(w world, in []Entry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "world %s\n", w)
	for i, e := range in {
		fmt.Fprintf(&sb, "  %d: %s %s from=%q to=%q ids=%v add=%v del=%v", i, e.Kind, e.Table, e.From, e.To, e.IDs, e.Add, e.Del)
		for _, g := range e.Guards {
			fmt.Fprintf(&sb, " guard(%s %q %v)", g.Table, g.From, g.IDs)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
