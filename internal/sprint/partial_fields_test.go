package sprint

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// What a partial snapshot knows of the fields of a record, of the counts of a
// fleet's cells, and what LoadPartial refuses of an answer (the cold read of
// the pull request, findings 5 to 7).

// projected is a plan that reads one ready cell with a projection, and one
// record by id, and the answer.
func projected() (ReadPlan, ReadAnswer) {
	rp := ReadPlan{
		IDs:    map[string][]string{Merge: {"m1"}},
		Ranges: []RangeQ{{Table: Work, Cell: "s1:ready", Limit: 10, Records: true, Fields: []string{"attempt", "avoid"}}},
	}
	ans := ReadAnswer{Tset: answers(rp,
		map[string][]*Card{Merge: {pcard("m1", "s1", Queued, 1, "state", "waiting")}},
		[]TsetAnswer{{IDs: []string{"p1"}, Scores: []float64{1}, Records: []*Card{
			// the answer holds more than it was asked for; the snapshot holds it, but
			// a planner may read only what its plan named
			pcard("p1", "s1", "ready", 1, "attempt", "1", "refused", "deal: x"),
		}}},
		nil, nil, nil)}
	return rp, ans
}

func TestReadOfAFieldTheQueriesDidNotNamePanicsInTest(t *testing.T) {
	t.Parallel()
	rp, ans := projected()
	s, err := loadPartial(rp, ans, true)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Work.Card("p1")
	if c.F("attempt") != "1" || c.Int("attempt") != 1 {
		t.Fatalf("a field the query named: %+v", c.Fields)
	}
	if c.F("avoid") != "" {
		t.Fatal("a field the query named, and the record has no value of: absent")
	}
	for name, read := range map[string]func(){
		"F":          func() { c.F("refused") },
		"Int":        func() { c.Int("refused") },
		"IsSentinel": func() { IsSentinel(c) },
	} {
		msg := mustPanic(t, read)
		if !strings.Contains(msg, unloadedFieldMessage) || !strings.Contains(msg, "p1") {
			t.Errorf("%s: panicked with %q", name, msg)
		}
	}
	if msg := mustPanic(t, func() { c.F("refused") }); !strings.HasSuffix(msg, ": p1 refused") {
		t.Errorf("the panic does not name the record and the field: %q", msg)
	}
	// The table's own index reads the primary field without asking a planner's
	// question: a cell read with a projection can still be read.
	if got := ids(s.Work.Cell("s1", "ready")); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Fatalf("Cell: %v", got)
	}
	if s.Work.Card("p1").Fields["refused"] != "deal: x" {
		t.Error("the answer's own fields are kept")
	}
}

func TestReadOfAFieldTheQueriesDidNotNameIsRefusedInRelease(t *testing.T) {
	t.Parallel()
	rp, ans := projected()
	s, err := loadPartial(rp, ans, false)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Work.Card("p1")
	if got := c.F("refused"); got != "" {
		t.Fatalf("a field no query named gave %q, which would read as its value", got)
	}
	if got := c.Int("refused"); got != 0 {
		t.Fatalf("Int gave %d", got)
	}
	want := []string{unloadedFieldMessage + ": p1 refused", unloadedFieldMessage + ": p1 refused"}
	if got := s.Unloaded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("refused reads: %q, want %q", got, want)
	}
	if err := s.UnloadedErr(); !errors.Is(err, ErrUnloaded) {
		t.Fatalf("UnloadedErr: %v", err)
	}
	// A nil card reads as it did.
	var none *Card
	if none.F("x") != "" || none.Int("x") != 0 {
		t.Fatal("a nil card has fields")
	}
}

func TestWhatSeveralQueriesNamedOfARecordIsWhatItHolds(t *testing.T) {
	t.Parallel()
	log := &unloadedLog{}
	c := func(fields ...string) *cardLoad { return newCardLoad(fields, log) }
	for _, tt := range []struct {
		name         string
		a, b         *cardLoad
		named        []string
		unnamed      []string
		wantWhole    bool
		wantBothWays bool
	}{
		{"two projections", c("a"), c("b"), []string{"a", "b"}, []string{"c"}, false, true},
		{"the same projection", c("a"), c("a"), []string{"a"}, []string{"b"}, false, true},
		{"a projection and a whole record", c("a"), c(), []string{"a", "z"}, nil, true, true},
	} {
		got := tt.a.with(tt.b)
		if tt.wantBothWays {
			if back := tt.b.with(tt.a); back.whole != got.whole || !reflect.DeepEqual(back.fields, got.fields) {
				t.Errorf("%s: %+v and %+v are not the same both ways", tt.name, got, back)
			}
		}
		if got.whole != tt.wantWhole {
			t.Errorf("%s: whole = %v", tt.name, got.whole)
		}
		for _, f := range tt.named {
			if !got.whole && !got.fields[f] {
				t.Errorf("%s: %s is not held", tt.name, f)
			}
		}
		for _, f := range tt.unnamed {
			if got.whole || got.fields[f] {
				t.Errorf("%s: %s is held", tt.name, f)
			}
		}
	}
}

func TestACardBuiltWholeHoldsEveryField(t *testing.T) {
	t.Parallel()
	// No load is every field: joined with a projection it is still every field.
	one := newCardLoad([]string{"a"}, &unloadedLog{})
	if got := (*cardLoad)(nil).with(one); got != nil {
		t.Errorf("a card built whole, then a projection: %+v", got)
	}
	if got := one.with(nil); got != nil {
		t.Errorf("a projection, then a card built whole: %+v", got)
	}
}

func TestReadsOfOneRecordByQueriesOfOtherProjectionsHoldBoth(t *testing.T) {
	t.Parallel()
	rp := ReadPlan{
		Ranges: []RangeQ{
			{Table: Work, Cell: "s1:ready", Limit: 10, Records: true, Fields: []string{"attempt"}},
			{Table: Work, Cell: "s1:ready", Limit: 10, Records: true, Fields: []string{"avoid"}},
		},
		Sprint: []SprintQ{{Kind: QueryFront, Stream: "s1", Fields: []string{"kind"}}},
	}
	a := func(kv ...string) TsetAnswer {
		return TsetAnswer{IDs: []string{"p1"}, Scores: []float64{1}, Records: []*Card{pcard("p1", "s1", "ready", 1, kv...)}}
	}
	ans := ReadAnswer{
		Tset:   answers(rp, nil, []TsetAnswer{a("attempt", "1"), a("avoid", "m2")}, nil, nil, nil),
		Sprint: []Answer{{Front: &FrontAnswer{Stream: "s1"}, Records: []TableCard{{Work, pcard("p1", "s1", "ready", 1, "kind", "primary")}}}},
	}
	s, err := loadPartial(rp, ans, true)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Work.Card("p1")
	if c.F("attempt") != "1" || c.F("avoid") != "m2" || c.F("kind") != "primary" {
		t.Fatalf("the three projections: %+v", c.Fields)
	}
	mustPanic(t, func() { c.F("needs") })

	// A query that names no fields reads the record whole, whichever came first.
	for _, order := range [][]string{{"whole", "thin"}, {"thin", "whole"}} {
		rp := ReadPlan{}
		var tsets []TsetAnswer
		for _, kind := range order {
			q := RangeQ{Table: Work, Cell: "s1:ready", Limit: 10, Records: true}
			if kind == "thin" {
				q.Fields = []string{"attempt"}
			}
			rp.Ranges = append(rp.Ranges, q)
			tsets = append(tsets, a("attempt", "1", "avoid", "m2"))
		}
		s, err := loadPartial(rp, ReadAnswer{Tset: tsets}, true)
		if err != nil {
			t.Fatal(err)
		}
		if c := s.Work.Card("p1"); c.F("avoid") != "m2" {
			t.Errorf("%v: a record read whole has a field refused", order)
		}
	}

	// A record read by its id is read whole (an ids query names no fields), and
	// so is a record of a snapshot built whole.
	whole, err := loadPartial(ReadPlan{IDs: map[string][]string{Work: {"p1"}}}, ReadAnswer{Tset: []TsetAnswer{{Records: []*Card{pcard("p1", "s1", "ready", 1, "any", "x")}}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Work.Card("p1").F("any") != "x" {
		t.Error("a record read by id has a field refused")
	}
	if pcard("p1", "s1", "ready", 1, "any", "x").F("any") != "x" {
		t.Error("a card built whole refused a field")
	}
}

func TestACompositeQueryReadsItsRecordsWithItsProjection(t *testing.T) {
	t.Parallel()
	rp := ReadPlan{Sprint: []SprintQ{{Kind: QueryFleet, Units: 1, Fields: []string{"status"}}}}
	ans := ReadAnswer{Sprint: []Answer{{Kind: QueryFleet, Rows: []string{"m1"}, Records: []TableCard{{Fleet, pcard("ctl-m1", "m1", Ctl, 0, "status", "up", "load", "3")}}}}}
	s, err := loadPartial(rp, ans, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.UpMembers(); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("UpMembers over the projection that names status: %v", got)
	}
	mustPanic(t, func() { s.MemberCtl("m1").F("load") })
}

func TestTheCountsOfAFleetQueryAreTheCountsOfItsCells(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{QueryFleet, QueryReaders} {
		table := map[string]string{QueryFleet: Fleet, QueryReaders: Readers}[kind]
		rp := ReadPlan{Sprint: []SprintQ{{Kind: kind, Units: 2}}}
		ans := ReadAnswer{Sprint: []Answer{{Kind: kind, Rows: []string{"m1", "m2"}, Counts: []CellCount{{"m1", "ready", 3}, {"m1", "working", 1}, {"m2", "ready", 0}}}}}
		s, err := loadPartial(rp, ans, true)
		if err != nil {
			t.Fatal(err)
		}
		tab := s.T(table)
		for cell, want := range map[[2]string]int{{"m1", "ready"}: 3, {"m1", "working"}: 1, {"m2", "ready"}: 0} {
			if got := tab.Count(cell[0], cell[1]); got != want {
				t.Errorf("%s: Count(%s, %s) = %d, want %d", kind, cell[0], cell[1], got, want)
			}
		}
		if len(s.Unloaded()) != 0 {
			t.Errorf("%s: refused %v", kind, s.Unloaded())
		}
		// A cell the query did not count is refused, and no other table has the counts.
		mustPanic(t, func() { tab.Count("m2", "working") })
		other := s.Work
		mustPanic(t, func() { other.Count("m1", "ready") })
	}

	for name, a := range map[string]Answer{
		"a count from a query that has none": {Kind: QueryStreams, Rows: []string{"s1"}, Counts: []CellCount{{"s1", "ready", 1}}},
		"a count with no row":                {Kind: QueryFleet, Counts: []CellCount{{"", "ready", 1}}},
		"a count with no column":             {Kind: QueryFleet, Counts: []CellCount{{"m1", "", 1}}},
		"a count below nothing":              {Kind: QueryFleet, Counts: []CellCount{{"m1", "ready", -1}}},
	} {
		kind := a.Kind
		if s, err := loadPartial(ReadPlan{Sprint: []SprintQ{{Kind: kind}}}, ReadAnswer{Sprint: []Answer{a}}, false); s != nil || !errors.Is(err, ErrMisaligned) {
			t.Errorf("%s: %v, %v", name, s, err)
		}
	}
}

func TestLoadPartialRefusesWhatALayerOneAnswerCannotBe(t *testing.T) {
	t.Parallel()
	openCells := OpenCells("s1")
	for _, tt := range []struct {
		name string
		rp   ReadPlan
		a    TsetAnswer
		ok   bool
	}{
		{"an rcount whose sum is its counts'", ReadPlan{RCounts: []RCountQ{OpenBeforeQ("s1", 4)}}, TsetAnswer{Counts: []int{1, 1, 0, 0, 0}, Sum: 2}, true},
		{"an rcount whose sum is more", ReadPlan{RCounts: []RCountQ{OpenBeforeQ("s1", 4)}}, TsetAnswer{Counts: []int{1, 1, 0, 0, 0}, Sum: 7}, false},
		{"an rcount whose sum is less", ReadPlan{RCounts: []RCountQ{OpenBeforeQ("s1", 4)}}, TsetAnswer{Counts: []int{1, 1, 0, 0, 0}, Sum: 1}, false},
		{"an rcount over every score, sum less", ReadPlan{RCounts: []RCountQ{{Table: Work, Cells: openCells}}}, TsetAnswer{Counts: []int{1, 1, 0, 0, 0}, Sum: 1}, false},
		{"an rcount over every score, sum right", ReadPlan{RCounts: []RCountQ{{Table: Work, Cells: openCells}}}, TsetAnswer{Counts: []int{1, 1, 0, 0, 0}, Sum: 2}, true},
		{"a range of a sorted set, no records", ReadPlan{Ranges: []RangeQ{{Key: "agenda", Limit: 10}}}, TsetAnswer{IDs: []string{"k"}, Scores: []float64{1}}, true},
		{"a range of a sorted set, with records", ReadPlan{Ranges: []RangeQ{{Key: "agenda", Limit: 10, Records: true}}}, TsetAnswer{IDs: []string{"k"}, Scores: []float64{1}, Records: []*Card{pcard("k", "s1", "ready", 1)}}, false},
		{"a range of a sorted set, records asked and none answered", ReadPlan{Ranges: []RangeQ{{Key: "agenda", Limit: 10, Records: true}}}, TsetAnswer{IDs: []string{"k"}, Scores: []float64{1}}, false},
	} {
		s, err := LoadPartial(tt.rp, ReadAnswer{Tset: []TsetAnswer{tt.a}})
		if tt.ok && err != nil || !tt.ok && (s != nil || !errors.Is(err, ErrMisaligned)) {
			t.Errorf("%s: %v, %v", tt.name, s, err)
		}
	}
}
