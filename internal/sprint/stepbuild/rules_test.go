package stepbuild

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The rules of the cut that the bounds tests do not reach: guards, TWICE,
// notes, rows, identity, cursors, determinism.

func TestGuardsTravelWithEveryPartOfTheirEntry(t *testing.T) {
	t.Parallel()
	owner := mv("work", names("m", 5000))
	guard := gd("merge", []string{"g1", "g2", "g3"})
	guard.Revs = []string{"3", "4", "5"}
	owner.Guards = []Entry{guard}
	steps := must(t, cfg(), []Entry{owner})
	if len(steps) != 3 {
		t.Fatalf("5,000 members: %d steps", len(steps))
	}
	for i, s := range steps {
		if len(s.Entries) != 2 || !s.Entries[0].Guard || s.Entries[1].Guard {
			t.Fatalf("step %d holds %v: the guard is first, once, then the part", i+1, kinds(s))
		}
		if g := s.Entries[0]; g.Kind != KindGuard || g.Table != "merge" || !reflect.DeepEqual(g.IDs, guard.IDs) || !reflect.DeepEqual(g.Revs, guard.Revs) || g.Source != 0 {
			t.Fatalf("step %d's guard is not the entry's guard: %+v", i+1, g)
		}
		if m := measure(t, s.Encode()); len(m.within(Contract())) != 0 || m.guardOnly != 3 {
			t.Fatalf("step %d: %v, %d guard-only", i+1, m.within(Contract()), m.guardOnly)
		}
	}
}

// A step that holds several parts of an entry holds its guards once: two
// guard entries naming the same member in one step would be TWICE.
func TestGuardsAreOncePerStepNotOncePerWireEntry(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Bounds = Contract()
	c.Bounds.LineIDs = 500 // four wire entries of a 2,000-member step
	owner := mv("work", names("m", 2000))
	owner.Guards = []Entry{gd("merge", []string{"g"})}
	steps := must(t, c, []Entry{owner})
	if len(steps) != 1 || len(steps[0].Entries) != 5 {
		t.Fatalf("%d steps, %v", len(steps), kinds(steps[0]))
	}
	guards := 0
	for _, p := range steps[0].Entries {
		if p.Guard {
			guards++
		}
	}
	if guards != 1 || !steps[0].Entries[0].Guard {
		t.Fatalf("%d guards in %v", guards, kinds(steps[0]))
	}
	if m := measure(t, steps[0].Encode()); len(m.repeats) != 0 {
		t.Fatalf("repeated: %v", m.repeats)
	}
}

func TestGuardsCountAgainstTheGuardBoundOfEveryStepThatHoldsThem(t *testing.T) {
	t.Parallel()
	guards := func(prefix string) []Entry {
		return []Entry{gd("merge", names(prefix+"a", 1500)), gd("merge", names(prefix+"b", 1500))}
	}
	a, b := mv("work", []string{"a"}), mv("work", []string{"b"})
	a.Guards, b.Guards = guards("x"), guards("y")
	// 3,000 + 3,000 guard-only members are two steps of 3,000.
	steps := must(t, cfg(), []Entry{a, b})
	if len(steps) != 2 || steps[0].Entries[len(steps[0].Entries)-1].IDs[0] != "a" || steps[1].Entries[len(steps[1].Entries)-1].IDs[0] != "b" {
		t.Fatalf("%d steps", len(steps))
	}
}

func TestAMemberNamedTwiceStartsTheNextStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries []Entry
		steps   int
	}{
		{"the same member in two changes", []Entry{mv("work", []string{"a"}), mv("work", []string{"a"})}, 2},
		{"the same ID in two tables", []Entry{mv("work", []string{"a"}), mv("merge", []string{"a"})}, 1},
		{"a change and a guard on one member", []Entry{mv("work", []string{"a"}), gd("work", []string{"a"})}, 2},
		{"a guard and a change on one member", []Entry{gd("work", []string{"a"}), mv("work", []string{"a"})}, 2},
		{"a change and a guard on another", []Entry{mv("work", []string{"a"}), gd("work", []string{"b"})}, 1},
		{"one member across three entries", []Entry{mv("work", []string{"a", "b"}), mv("work", []string{"b", "c"}), mv("work", []string{"c"})}, 3},
	} {
		steps := must(t, cfg(), tc.entries)
		if len(steps) != tc.steps {
			t.Errorf("%s: %d steps, want %d", tc.name, len(steps), tc.steps)
		}
		for _, s := range steps {
			if m := measure(t, s.Encode()); len(m.repeats) != 0 {
				t.Errorf("%s: step %d repeats %v", tc.name, s.Part, m.repeats)
			}
		}
	}
	// The order is kept: the second change to a member is in a later step.
	steps := must(t, cfg(), []Entry{mv("work", []string{"a", "b"}), mv("work", []string{"b", "c"})})
	if len(steps) != 2 || !reflect.DeepEqual(steps[0].Entries[0].IDs, []string{"a", "b"}) {
		t.Fatalf("the second entry cannot take b's place in the first step's order: %d steps", len(steps))
	}
	// A repeat in the middle of an entry cuts the entry there, and the rest
	// goes on after the step that already names it.
	steps = must(t, cfg(), []Entry{mv("work", []string{"b"}), mv("work", []string{"a", "b", "c"})})
	if len(steps) != 2 || !reflect.DeepEqual(steps[0].Entries[1].IDs, []string{"a"}) || !reflect.DeepEqual(steps[1].Entries[0].IDs, []string{"b", "c"}) {
		t.Fatalf("%d steps: %+v", len(steps), steps)
	}
	// The guards of another entry are members too.
	owner := mv("work", []string{"x"})
	owner.Guards = []Entry{gd("merge", []string{"z"})}
	steps = must(t, cfg(), []Entry{owner, mv("merge", []string{"z"})})
	if len(steps) != 2 {
		t.Fatalf("a guard's member changed by the next entry: %d steps", len(steps))
	}
}

func TestAMemberNamedTwiceInOneEntryIsRefused(t *testing.T) {
	t.Parallel()
	_, err := Build(cfg(), []Entry{mv("work", []string{"a", "b", "a"})})
	var ie *InputError
	if !errors.As(err, &ie) || ie.Member != "a" || ie.Field != "ids" || ie.Entry != 0 || !errors.Is(err, ErrInput) {
		t.Fatalf("%v", err)
	}
	// A guard that names a member of its owner, or of another guard.
	owner := mv("work", []string{"a"})
	owner.Guards = []Entry{gd("work", []string{"a"})}
	_, err = Build(cfg(), []Entry{owner})
	if !errors.As(err, &ie) || ie.Member != "a" || ie.Field != "guards[0].ids" {
		t.Fatalf("%v", err)
	}
	owner.Guards = []Entry{gd("merge", []string{"g"}), gd("merge", []string{"h", "g"})}
	_, err = Build(cfg(), []Entry{owner})
	if !errors.As(err, &ie) || ie.Member != "g" || ie.Field != "guards[1].ids" {
		t.Fatalf("%v", err)
	}
}

func TestNotesFollowTheMembersOfTheirEntry(t *testing.T) {
	t.Parallel()
	e := mv("work", names("m", 2500))
	e.Notes = []Note{{Meta: map[string]string{"n": "1"}, About: []string{"p"}}, {Meta: map[string]string{"n": "2"}}}
	steps := must(t, cfg(), []Entry{e})
	if len(steps) != 2 || len(steps[0].Notes) != 0 || len(steps[1].Notes) != 2 {
		t.Fatalf("notes of an entry cut over two steps go with the last part: %d steps", len(steps))
	}
	if steps[1].Notes[0].Meta["n"] != "1" || steps[1].Notes[1].Meta["n"] != "2" || steps[1].Notes[0].Source != 0 {
		t.Fatalf("the notes are out of order: %+v", steps[1].Notes)
	}
	// Notes of two entries keep their input order, and a note that does not
	// fit the step its entry ended in leads the next one.
	full := mv("work", names("a", LimitCandidates))
	full.About = names("p", LimitCandidates)
	full.Notes = []Note{{About: names("q", 2001)}}
	next := mv("work", []string{"z"})
	next.Notes = []Note{{Meta: map[string]string{"n": "z"}}}
	steps = must(t, cfg(), []Entry{full, next})
	if len(steps) != 2 || len(steps[0].Notes) != 0 || len(steps[0].Entries) != 1 {
		t.Fatalf("%d steps", len(steps))
	}
	if len(steps[1].Notes) != 2 || steps[1].Notes[0].Source != 0 || steps[1].Notes[1].Source != 1 || steps[1].Entries[0].IDs[0] != "z" {
		t.Fatalf("the spilled note comes first in the next step, ahead of the next entry's: %+v", steps[1].Notes)
	}
}

func TestRowsOfOneStepDoNotConflict(t *testing.T) {
	t.Parallel()
	add := Entry{Kind: KindRows, Table: "work", Add: []string{"r"}}
	del := Entry{Kind: KindRows, Table: "work", Del: []string{"r"}}
	if steps := must(t, cfg(), []Entry{add, del}); len(steps) != 2 {
		t.Fatalf("a row added then deleted, in one step: %d steps", len(steps))
	}
	if steps := must(t, cfg(), []Entry{add, add}); len(steps) != 1 {
		t.Fatalf("a row added twice: %d steps", len(steps))
	}
	other := del
	other.Table = "merge"
	if steps := must(t, cfg(), []Entry{add, other}); len(steps) != 1 {
		t.Fatalf("the same row name in two tables: %d steps", len(steps))
	}
	// A row deleted in a step is added again in the next: still in order.
	steps := must(t, cfg(), []Entry{del, add})
	if len(steps) != 2 || len(steps[0].Entries[0].Del) != 1 || len(steps[1].Entries[0].Add) != 1 {
		t.Fatalf("%+v", steps)
	}
	_, err := Build(cfg(), []Entry{{Kind: KindRows, Table: "work", Add: []string{"r"}, Del: []string{"r"}}})
	var ie *InputError
	if !errors.As(err, &ie) || ie.Field != "del" {
		t.Fatalf("a row added and deleted by one entry: %v", err)
	}
}

func TestRowsOfAnEntryCutKeepTheirOrder(t *testing.T) {
	t.Parallel()
	e := Entry{Kind: KindRows, Table: "work", Add: names("a", 130), Del: names("d", 130)}
	steps := must(t, cfg(), []Entry{e})
	var adds, dels []string
	for _, s := range steps {
		for _, p := range s.Entries {
			adds, dels = append(adds, p.Add...), append(dels, p.Del...)
		}
	}
	if len(steps) != 3 || !reflect.DeepEqual(adds, e.Add) || !reflect.DeepEqual(dels, e.Del) {
		t.Fatalf("%d steps: %d adds, %d dels", len(steps), len(adds), len(dels))
	}
	// The step that crosses from adds to dels holds the last adds and the
	// first dels, in one wire entry.
	if p := steps[1].Entries[0]; len(p.Add) != 30 || len(p.Del) != 70 {
		t.Fatalf("the crossing step: %d adds, %d dels", len(p.Add), len(p.Del))
	}
}

func TestEachStepHasItsOwnIdentity(t *testing.T) {
	t.Parallel()
	var asked []int
	c := cfg()
	c.Ident = func(part int) Ident {
		asked = append(asked, part)
		return Ident{Op: fmt.Sprintf("move.%d", part), Intent: fmt.Sprintf(`{"part":%d}`, part), Result: "r"}
	}
	steps := must(t, c, []Entry{mv("work", names("m", 4001))})
	if len(steps) != 3 || !reflect.DeepEqual(asked, []int{1, 2, 3}) {
		t.Fatalf("%d steps, asked %v", len(steps), asked)
	}
	for i, s := range steps {
		want := fmt.Sprintf("move.%d", i+1)
		req := decode(t, s.Encode())
		if s.Ident.Op != want || req.Op != want || req.Intent != fmt.Sprintf(`{"part":%d}`, i+1) || req.Result != "r" {
			t.Fatalf("step %d: %+v, request %+v", i+1, s.Ident, req)
		}
		if len(s.Encode()) != s.Bytes {
			t.Fatalf("step %d: %d bytes, encoded %d", i+1, s.Bytes, len(s.Encode()))
		}
	}
	// No identity: no op, no intent in the request.
	steps = must(t, cfg(), []Entry{mv("work", []string{"m"})})
	if raw := string(steps[0].Encode()); strings.Contains(raw, `"op"`) || strings.Contains(raw, `"intent"`) || strings.Contains(raw, `"result"`) {
		t.Fatalf("%s", raw)
	}
}

func TestAnIdentityThatWouldReplayIsRefused(t *testing.T) {
	t.Parallel()
	entries := []Entry{mv("work", names("m", 4001))}
	for _, tc := range []struct {
		name  string
		ident func(int) Ident
		field string
	}{
		{"one op for two steps", func(int) Ident { return Ident{Op: "same", Intent: "i"} }, "op"},
		{"an op without its intent", func(p int) Ident { return Ident{Op: fmt.Sprint(p)} }, "op"},
		{"an intent without its op", func(int) Ident { return Ident{Intent: "i"} }, "op"},
	} {
		c := cfg()
		c.Ident = tc.ident
		steps, err := Build(c, entries)
		var ie *InputError
		if steps != nil || !errors.As(err, &ie) || ie.Field != tc.field || ie.Entry != -1 {
			t.Errorf("%s: %v (%d steps)", tc.name, err, len(steps))
		}
	}
}

func TestCursorsNameTheLastMemberAndResumeAfterIt(t *testing.T) {
	t.Parallel()
	first := mv("work", names("m", 4500))
	first.Notes = []Note{{Meta: map[string]string{"n": "1"}}}
	second := gd("merge", []string{"g"})
	third := Entry{Kind: KindRows, Table: "work", Add: []string{"r1", "r2"}}
	entries := []Entry{first, second, third, {Kind: KindNote, Notes: []Note{{About: []string{"p"}}}}}
	steps := must(t, cfg(), entries)
	if len(steps) != 3 {
		t.Fatalf("%d steps", len(steps))
	}
	want := []Cursor{
		{Entry: 0, Done: 2000, Table: "work", ID: "m1999"},
		{Entry: 0, Done: 4000, Table: "work", ID: "m3999"},
		{Entry: 3, Done: 0, Notes: 1},
	}
	if steps[0].Cursor != want[0] || steps[1].Cursor != want[1] {
		t.Fatalf("cursors: %+v", []Cursor{steps[0].Cursor, steps[1].Cursor})
	}
	if steps[2].Cursor != want[2] {
		t.Fatalf("the last cursor: %+v", steps[2].Cursor)
	}
	for i, s := range steps {
		rest, err := After(steps, s.Cursor)
		if err != nil || len(rest) != len(steps)-i-1 || (len(rest) > 0 && rest[0].Part != i+2) {
			t.Fatalf("After step %d: %d steps, %v", i+1, len(rest), err)
		}
	}
	if _, err := After(steps, Cursor{Entry: 9, Done: 1, ID: "x"}); !errors.Is(err, ErrCursor) {
		t.Fatalf("a cursor of no step: %v", err)
	}
	// A rebuild of the whole gives the same cursors, parts and totals: the
	// steps a resume sends are the steps of the whole, never of the rest.
	again := must(t, cfg(), entries)
	for i := range steps {
		if again[i].Cursor != steps[i].Cursor || again[i].Part != steps[i].Part || again[i].Parts != steps[i].Parts {
			t.Fatalf("step %d differs on a rebuild", i+1)
		}
	}
	// Cursors strictly increase, so one names one step, also across the
	// steps of an entry whose notes spill into a step of their own.
	full := mv("work", names("a", LimitCandidates))
	full.About = names("p", LimitCandidates)
	full.Notes = []Note{{About: names("q", 2001)}}
	steps = must(t, cfg(), []Entry{full})
	if len(steps) != 2 || steps[0].Cursor == steps[1].Cursor || steps[1].Cursor.Notes != 1 || steps[0].Cursor.Notes != 0 {
		t.Fatalf("%+v", steps)
	}
}

func TestPartsAreNumberedFromOne(t *testing.T) {
	t.Parallel()
	steps := must(t, cfg(), []Entry{mv("work", names("m", 7000))})
	for i, s := range steps {
		if s.Part != i+1 || s.Parts != len(steps) || len(s.Header) != 1 || s.Header[0] != hdr[0] || s.Epoch != "1" {
			t.Fatalf("step %d: %+v", i, s)
		}
	}
	if len(steps) != 4 {
		t.Fatalf("%d steps", len(steps))
	}
}

func TestTheSameInputGivesTheSameSteps(t *testing.T) {
	t.Parallel()
	mk := func() []Entry {
		e := mv("work", names("m", 3300))
		e.Set = map[string]string{"z": "1", "a": "2", "m": "3"}
		e.Each = make([]map[string]string, len(e.IDs))
		for i := range e.Each {
			e.Each[i] = map[string]string{"k" + fmt.Sprint(i%7): fmt.Sprint(i), "j": "x"}
		}
		e.Meta = map[string]string{"why": "because", "and": "so"}
		g := gd("merge", []string{"g1", "g2"})
		owner := e
		owner.Guards = []Entry{g}
		owner.Notes = []Note{{Meta: map[string]string{"b": "1", "a": "2"}, About: []string{"p"}}}
		return []Entry{owner, {Kind: KindRows, Table: "work", Add: names("r", 150)}, gd("work", names("m", 10))}
	}
	a, b := must(t, cfg(), mk()), must(t, cfg(), mk())
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two builds of one input differ")
	}
	for i := range a {
		if string(a[i].Encode()) != string(b[i].Encode()) {
			t.Fatalf("step %d encodes differently", i+1)
		}
	}
}

func TestBuildDoesNotChangeItsInput(t *testing.T) {
	t.Parallel()
	mk := func() []Entry {
		e := mv("work", names("m", 2500))
		e.Each = make([]map[string]string, 2500)
		e.Guards = []Entry{gd("merge", []string{"g"})}
		e.Notes = []Note{{About: []string{"p"}}}
		return []Entry{e, {Kind: KindRows, Table: "work", Add: names("r", 120)}}
	}
	in := mk()
	must(t, cfg(), in)
	if !reflect.DeepEqual(in, mk()) {
		t.Fatalf("the input was changed")
	}
}

func TestNoEntriesIsNoSteps(t *testing.T) {
	t.Parallel()
	if steps, err := Build(cfg(), nil); err != nil || len(steps) != 0 {
		t.Fatalf("%d steps, %v", len(steps), err)
	}
	if steps, err := Build(cfg(), []Entry{{Kind: KindNote}}); err == nil || steps != nil {
		t.Fatalf("a note entry with no note is an input error: %d steps, %v", len(steps), err)
	}
}

func TestBoundsMayBeTightenedNotLoosened(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Bounds = Contract()
	c.Bounds.Candidates = 500 // a measured chunk under the admission ceiling
	steps := must(t, c, []Entry{mv("work", names("m", 2000))})
	if len(steps) != 4 || countMembers(steps[3:]) != 500 {
		t.Fatalf("a chunk of 500: %d steps", len(steps))
	}
	for _, mutate := range []func(*Bounds){
		func(b *Bounds) { b.Candidates = LimitCandidates + 1 },
		func(b *Bounds) { b.RequestBytes = LimitRequestBytes + 1 },
		func(b *Bounds) { b.Entries = 0 },
		func(b *Bounds) { b.LineBytes = -1 },
	} {
		c := cfg()
		c.Bounds = Contract()
		mutate(&c.Bounds)
		if _, err := Build(c, []Entry{mv("work", []string{"m"})}); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "bounds") {
			t.Errorf("bounds %+v: %v", c.Bounds, err)
		}
	}
	// The zero Bounds is the contract's.
	steps = must(t, cfg(), []Entry{mv("work", names("m", 2001))})
	if len(steps) != 2 {
		t.Fatalf("the zero Bounds: %d steps", len(steps))
	}
}

func TestInputThatIsNotARequestIsRefused(t *testing.T) {
	t.Parallel()
	over := func(f func(*Entry)) []Entry {
		e := mv("work", []string{"a", "b"})
		f(&e)
		return []Entry{e}
	}
	for _, tc := range []struct {
		name    string
		cfg     Config
		entries []Entry
		field   string
	}{
		{"an unknown kind", cfg(), []Entry{{Kind: "bump", Table: "work"}}, "kind"},
		{"a guard that sets fields", cfg(), []Entry{{Kind: KindGuard, Table: "work", From: "r:c", IDs: []string{"a"}, Set: map[string]string{"a": "b"}}}, "set"},
		{"a create without scores", cfg(), []Entry{{Kind: KindCreate, Table: "work", To: "r:c", IDs: []string{"a"}}}, "scores"},
		{"a move without a source", cfg(), over(func(e *Entry) { e.From = "" }), "from"},
		{"a remove that moves", cfg(), []Entry{{Kind: KindRemove, Table: "work", From: "r:c", To: "r:d", IDs: []string{"a"}}}, "to"},
		{"an entry with no member", cfg(), over(func(e *Entry) { e.IDs = nil }), "ids"},
		{"scores out of step", cfg(), over(func(e *Entry) { e.Scores = []string{"1"} }), "scores"},
		{"each out of step", cfg(), over(func(e *Entry) { e.Each = make([]map[string]string, 3) }), "each"},
		{"about out of step", cfg(), over(func(e *Entry) { e.About = []string{"p"} }), "about"},
		{"revs out of step", cfg(), over(func(e *Entry) { e.Revs = []string{"1", "2", "3"} }), "revs"},
		{"a revision that is not canonical", cfg(), over(func(e *Entry) { e.Revs = []string{"1", "01"} }), "revs"},
		{"a revision past uint64", cfg(), over(func(e *Entry) { e.Revs = []string{"1", "18446744073709551616"} }), "revs"},
		{"an empty id", cfg(), []Entry{mv("work", []string{"a", ""})}, "ids"},
		{"an id that is not UTF-8", cfg(), []Entry{mv("work", []string{"a\xff"})}, "ids"},
		{"a value that is not UTF-8", cfg(), over(func(e *Entry) { e.Set = map[string]string{"f": "\xfe"} }), "set"},
		{"a cell without a column", cfg(), over(func(e *Entry) { e.From = "row" }), "from"},
		{"a cell with an empty column", cfg(), over(func(e *Entry) { e.From = "row:" }), "from"},
		{"a rows entry with no rows", cfg(), []Entry{{Kind: KindRows, Table: "work"}}, "add/del"},
		{"a row with a control character", cfg(), []Entry{{Kind: KindRows, Table: "work", Add: []string{"a\tb"}}}, "add"},
		{"an attached guard that changes", cfg(), over(func(e *Entry) { e.Guards = []Entry{mv("work", []string{"g"})} }), "guards[0].kind"},
		{"an attached guard with notes", cfg(), over(func(e *Entry) {
			g := gd("merge", []string{"g"})
			g.Notes = []Note{{}}
			e.Guards = []Entry{g}
		}), "guards[0].notes"},
		{"an empty table", cfg(), []Entry{mv("", []string{"a"})}, "t"},
		{"an empty header value", Config{Epoch: "1", Header: []Member{{"space", ""}}}, []Entry{mv("work", []string{"a"})}, "header value"},
		{"an empty header key", Config{Epoch: "1", Header: []Member{{"", "v"}}}, []Entry{mv("work", []string{"a"})}, "header key"},
		{"a header key the request has", Config{Epoch: "1", Header: []Member{{"entries", "v"}}}, []Entry{mv("work", []string{"a"})}, "header key"},
		{"a header key twice", Config{Epoch: "1", Header: []Member{{"space", "a"}, {"space", "b"}}}, []Entry{mv("work", []string{"a"})}, "header key"},
		{"an epoch with a leading zero", Config{Epoch: "01", Header: hdr}, []Entry{mv("work", []string{"a"})}, "epoch"},
		{"an epoch past uint64", Config{Epoch: "18446744073709551616", Header: hdr}, []Entry{mv("work", []string{"a"})}, "epoch"},
		{"a note with no note", cfg(), []Entry{{Kind: KindNote}}, "notes"},
	} {
		steps, err := Build(tc.cfg, tc.entries)
		var ie *InputError
		if steps != nil || !errors.As(err, &ie) || ie.Field != tc.field {
			t.Errorf("%s: %v (%d steps), want a refusal of field %s", tc.name, err, len(steps), tc.field)
		}
	}
}

func TestNilAndEmptyAreDistinctOnTheWire(t *testing.T) {
	t.Parallel()
	e := mv("work", []string{"a"})
	raw := string(must(t, cfg(), []Entry{e})[0].Encode())
	for _, key := range []string{`"set"`, `"unset"`, `"each"`, `"before_fields"`, `"meta"`, `"scores"`, `"about"`, `"revs"`} {
		if strings.Contains(raw, key) {
			t.Errorf("absent %s written: %s", key, raw)
		}
	}
	e.Set, e.Unset, e.BeforeFields, e.Meta = map[string]string{}, []string{}, []string{}, map[string]string{}
	e.Each, e.About, e.Revs, e.Scores = []map[string]string{nil}, []string{"p"}, []string{"1"}, []string{"1"}
	req := decode(t, must(t, cfg(), []Entry{e})[0].Encode())
	w := req.Entries[0]
	if w.Set == nil || w.Unset == nil || w.BeforeFields == nil || w.Meta == nil || w.Each == nil || len(w.Each[0]) != 0 {
		t.Fatalf("present empty values were dropped: %+v", w)
	}
}
