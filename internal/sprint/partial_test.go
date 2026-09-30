package sprint

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The partial snapshot (the upper design, version 2.1, 1.5.2 and IT05): a
// snapshot loaded from a read plan and its answer knows which cells it loaded,
// and refuses a read of any other.

// pcard is a card of the tests: its place, score and fields (name, value, ...).
func pcard(id, row, col string, score float64, kv ...string) *Card {
	c := &Card{ID: id, Row: row, Col: col, Score: score, Rev: 1, Fields: map[string]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		c.Fields[kv[i]] = kv[i+1]
	}
	return c
}

// wholeSnapshot is a snapshot built whole from cards, with rows for the work
// and merge tables and the fleet's.
func wholeSnapshot(streams, members []string, work ...*Card) *Snapshot {
	s := &Snapshot{Epoch: 3, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Work.Rows, s.Merge.Rows, s.Fleet.Rows = streams, streams, members
	for _, c := range work {
		s.Work.Put(c)
	}
	return s
}

// mustPanic runs f and returns what it panicked with, failing when it did not.
func mustPanic(t *testing.T, f func()) (got string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("did not panic")
		}
		got = fmt.Sprint(r)
	}()
	f()
	return ""
}

func ids(cs []*Card) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

// sampleRead is a plan and its answer: two streams, s1 with ready cards p1 and
// p2 and a sentinel g1 waiting before p3, s2 with an empty ready cell, and a
// member; the ready cells are read whole, s1's waiting cell by its first card
// only, s1's working cell by its count, and the position by `front` and by an
// rcount.
func sampleRead() (ReadPlan, ReadAnswer) {
	g1 := pcard("g1", "s1", Waiting, 5, "kind", Sentinel)
	rp := ReadPlan{
		IDs: map[string][]string{Work: {"p1", "p2", "nothere"}, Fleet: {"ctl-m1"}},
		Ranges: []RangeQ{
			{Table: Work, Cell: "s1:ready", Limit: MaxRangeLimit, Records: true, Fields: []string{"attempt"}},
			{Table: Work, Cell: "s2:ready", Limit: MaxRangeLimit, Records: true},
			{Table: Work, Cell: "s1:waiting", Limit: 1, Records: true},
			{Key: "agenda", Limit: 10},
		},
		Counts:  []CountQ{{Table: Work, Cells: []string{"s1:working", "s2:working"}}},
		RCounts: []RCountQ{OpenBeforeQ("s1", 4)},
		Lines:   []LinesQ{{After: 6, Through: 7, Limit: 1}},
		Sprint: []SprintQ{
			{Kind: QueryStreams, Units: 2},
			{Kind: QueryFleet, Units: 1},
			{Kind: QueryFront, Stream: "s1"},
		},
	}
	ans := ReadAnswer{
		Epoch: 3, TimeMS: 1790000000123,
		IDs: map[string][]*Card{
			Work:  {pcard("p1", "s1", "ready", 1, "attempt", "0"), pcard("p2", "s1", "ready", 2, "attempt", "1"), nil},
			Fleet: {pcard("ctl-m1", "m1", "ctl", 0, "status", "up")},
		},
		Ranges: []RangeA{
			{IDs: []string{"p1", "p2"}, Scores: []float64{1, 2}, Cards: []*Card{pcard("p1", "s1", "ready", 1), pcard("p2", "s1", "ready", 2)}},
			{},
			{IDs: []string{"g1"}, Scores: []float64{5}, HasMore: true, Cards: []*Card{g1}},
			{IDs: []string{"job:1"}, Scores: []float64{9}},
		},
		Counts:  [][]int{{3, 0}},
		RCounts: []RCountA{{Counts: []int{0, 1, 0, 0, 0}, Sum: 1}},
		Lines: []LinesA{{Lines: []LogLine{{ID: "7-0", Body: []byte(
			`{"k":"m","ms":"1790000000123","tbl":"work","from":"s1:working","to":"s1:review","ids":["p1"],"about":["p1"],` +
				`"score":[["5","5"]],"rev":[["3","4"]],"shared":{"result":"ok","head":"abc"},` +
				`"meta":{"verb":"finish","actor":"m1","stream":"s1"}}`)}}}},
		Sprint: []Answer{
			{Rows: []string{"s1", "s2"}},
			{Rows: []string{"m1"}},
			{Records: []TableCard{{Work, g1}}, Front: &FrontAnswer{Stream: "s1", G: "g1", Sigma: 5, NBefore: 2}},
		},
	}
	return rp, ans
}

func TestLoadPartialFromReadAnswer(t *testing.T) {
	t.Parallel()
	rp, ans := sampleRead()
	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatal(err)
	}
	if s.Epoch != 3 || !s.Now.Equal(time.UnixMilli(1790000000123)) {
		t.Fatalf("epoch %d, now %v", s.Epoch, s.Now)
	}
	if s.Work == nil || s.Readers == nil || s.Merge == nil || s.Fleet == nil {
		t.Fatal("a table of the sprint is missing")
	}

	// The records of the ids, by table; an id with no record is no card.
	if c := s.Work.Card("p1"); c == nil || c.Row != "s1" || c.Col != "ready" || c.Score != 1 || c.F("attempt") != "0" {
		t.Fatalf("p1: %+v", c)
	}
	if c := s.Work.Card("p2"); c == nil || c.Int("attempt") != 1 {
		t.Fatalf("p2: %+v", c)
	}
	if s.Work.Card("nothere") != nil {
		t.Fatal("an id with no record is a card")
	}
	if c := s.Fleet.Card("ctl-m1"); c == nil || c.F("status") != "up" {
		t.Fatalf("ctl-m1: %+v", c)
	}
	if got := s.MemberCtl("m1"); got == nil || got.F("status") != Up {
		t.Fatalf("MemberCtl(m1): %+v", got)
	}

	// The cells: the two ready cells whole (one empty), the waiting cell read
	// by its first card only, a cell counted, a cell neither.
	for _, c := range []struct {
		row, col string
		loaded   bool
	}{{"s1", "ready", true}, {"s2", "ready", true}, {"s1", "waiting", false}, {"s1", "working", false}, {"s2", "review", false}} {
		if got := s.Work.Loaded(c.row, c.col); got != c.loaded {
			t.Fatalf("Loaded(%s, %s) = %v", c.row, c.col, got)
		}
	}
	if got := ids(s.Work.Cell("s1", "ready")); !reflect.DeepEqual(got, []string{"p1", "p2"}) {
		t.Fatalf("Cell(s1, ready): %v", got)
	}
	if len(s.Work.Cell("s2", "ready")) != 0 {
		t.Fatal("an empty cell read whole has cards")
	}
	if got := ids(s.Work.Column("ready")); !reflect.DeepEqual(got, []string{"p1", "p2"}) {
		t.Fatalf("Column(ready) over the rows read: %v", got)
	}
	if !reflect.DeepEqual(s.Work.Rows, []string{"s1", "s2"}) || !reflect.DeepEqual(s.Merge.Rows, []string{"s1", "s2"}) ||
		!reflect.DeepEqual(s.Fleet.Rows, []string{"m1"}) {
		t.Fatalf("rows: work %v merge %v fleet %v", s.Work.Rows, s.Merge.Rows, s.Fleet.Rows)
	}
	if got := s.Streams(); !reflect.DeepEqual(got, []string{"s1", "s2"}) {
		t.Fatalf("Streams: %v", got)
	}

	// Counts: of a cell whole, of a cell counted, and the sum of an rcount.
	if n := s.Work.Count("s1", "ready"); n != 2 {
		t.Fatalf("Count(s1, ready) = %d", n)
	}
	if n := s.Work.Count("s1", "working"); n != 3 {
		t.Fatalf("Count(s1, working) = %d", n)
	}
	if n := s.Work.Count("s2", "working"); n != 0 {
		t.Fatalf("Count(s2, working) = %d", n)
	}

	// The positions.
	if g := FirstSentinel(s, "s1"); g == nil || g.ID != "g1" || g.Score != 5 || !IsSentinel(g) {
		t.Fatalf("FirstSentinel(s1): %+v", g)
	}
	if n := OpenBefore(s, "s1", 5); n != 2 {
		t.Fatalf("OpenBefore(s1, 5) = %d, the answer's n_before is 2", n)
	}
	if n := OpenBefore(s, "s1", 4); n != 1 {
		t.Fatalf("OpenBefore(s1, 4) = %d, the rcount's sum is 1", n)
	}
	if f := s.Partial.Fronts["s1"]; f.G != "g1" || f.NBefore != 2 || f.GQuarantined {
		t.Fatalf("front of s1: %+v", f)
	}

	// The lines, parsed; the plan and the answer are kept whole, by index.
	if len(s.Partial.Events) != 1 || s.Partial.Events[0].Seq != 7 || s.Partial.Events[0].Verb != "finish" {
		t.Fatalf("events: %+v", s.Partial.Events)
	}
	if got := s.Partial.Answer.Ranges[3].IDs; !reflect.DeepEqual(got, []string{"job:1"}) {
		t.Fatalf("the raw range's answer, by its index: %v", got)
	}
	if len(s.Partial.Plan.Ranges) != 4 {
		t.Fatalf("the plan kept: %+v", s.Partial.Plan)
	}
	if got := s.Unloaded(); len(got) != 0 {
		t.Fatalf("a plan that read what it loaded was refused: %v", got)
	}
}

func TestColumnOnUnloadedPanicsInTest(t *testing.T) {
	t.Parallel()
	rp, ans := sampleRead()
	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func(){
		"Cell on a cell read by its first card":      func() { s.Work.Cell("s1", "waiting") },
		"Cell on a cell never read":                  func() { s.Work.Cell("s2", "review") },
		"Column over a column with cells not loaded": func() { s.Work.Column("ready", "waiting") },
		"Count on a cell neither loaded nor counted": func() { s.Work.Count("s2", "review") },
		"FirstSentinel on a stream with no front":    func() { FirstSentinel(s, "s2") },
		"OpenBefore at a score no query read":        func() { OpenBefore(s, "s1", 3) },
	} {
		msg := mustPanic(t, read)
		if !strings.Contains(msg, "the planner read a cell its plan did not load") &&
			!strings.Contains(msg, "the planner asked for a position its plan did not load") {
			t.Fatalf("%s: panicked with %q", name, msg)
		}
	}
	if msg := mustPanic(t, func() { s.Work.Cell("s1", "waiting") }); !strings.Contains(msg, "work s1:waiting") {
		t.Fatalf("the panic does not name the cell: %q", msg)
	}
	// A table whose rows were not read cannot say what a column means.
	if msg := mustPanic(t, func() { s.Readers.Column(Asked) }); !strings.Contains(msg, "readers rows") {
		t.Fatalf("Column on a table with no rows read: %q", msg)
	}
	// The reads the plan did load do not panic.
	_ = s.Work.Cell("s1", "ready")
	_ = s.Work.Column("ready")
	_ = s.Work.Count("s1", "working")
}

func TestColumnOnUnloadedRefusedInRelease(t *testing.T) {
	t.Parallel()
	rp, ans := sampleRead()
	s, err := loadPartial(rp, ans, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Work.Cell("s1", "waiting"); got != nil {
		t.Fatalf("a cell not loaded whole gave its cards: %v", ids(got))
	}
	if got := s.Work.Column("ready", "waiting"); got != nil {
		t.Fatalf("a column with a cell not loaded gave the cards it knew: %v", ids(got))
	}
	if n := s.Work.Count("s2", "review"); n != 0 {
		t.Fatalf("Count on an unread cell = %d", n)
	}
	if got := s.Readers.Column(Asked); got != nil {
		t.Fatalf("Column on a table with no rows read: %v", ids(got))
	}
	if g := FirstSentinel(s, "s2"); g != nil {
		t.Fatalf("FirstSentinel on a stream with no front: %+v", g)
	}
	if n := OpenBefore(s, "s1", 3); n != -1 {
		t.Fatalf("OpenBefore at a score no query read = %d, not a count that could mean none", n)
	}
	got := s.Unloaded()
	want := []string{
		"the planner read a cell its plan did not load: work s1:waiting",
		"the planner read a cell its plan did not load: work s1:waiting",
		"the planner read a cell its plan did not load: work s2:waiting",
		"the planner read a cell its plan did not load: work s2:review",
		"the planner read a cell its plan did not load: readers rows",
		"the planner asked for a position its plan did not load: front of s2",
		"the planner asked for a position its plan did not load: s1 before 3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refused reads:\n got %q\nwant %q", got, want)
	}

	// A snapshot built whole has every cell, and refuses nothing.
	w := wholeSnapshot([]string{"s1"}, nil, pcard("p1", "s1", "ready", 1))
	if !w.Work.Loaded("s1", "review") || len(w.Work.Cell("s1", "ready")) != 1 || w.Work.Count("s1", "ready") != 1 {
		t.Fatal("a table built whole refused a cell")
	}
	if len(w.Unloaded()) != 0 {
		t.Fatalf("a snapshot built whole: %v", w.Unloaded())
	}
	var none *Table
	if none.Loaded("s1", "ready") {
		t.Fatal("no table has a loaded cell")
	}
}

func TestUnloadedNamedReadsAreBounded(t *testing.T) {
	t.Parallel()
	s, err := loadPartial(ReadPlan{}, ReadAnswer{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxUnloadedNoted+10; i++ {
		s.Work.Cell(fmt.Sprintf("s%d", i), "ready")
	}
	got := s.Unloaded()
	if len(got) != MaxUnloadedNoted+1 {
		t.Fatalf("%d lines, want %d and a count", len(got), MaxUnloadedNoted)
	}
	if last := got[len(got)-1]; !strings.HasSuffix(last, "and 10 more") {
		t.Fatalf("the last line: %q", last)
	}
}

func TestLoadedCellsAreOnlyThoseAnAnswerGaveWhole(t *testing.T) {
	t.Parallel()
	cell := func(q RangeQ, a RangeA) *Snapshot {
		t.Helper()
		q.Table, q.Cell = Work, "s1:ready"
		s, err := LoadPartial(ReadPlan{Ranges: []RangeQ{q}}, ReadAnswer{Ranges: []RangeA{a}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	full := RangeA{IDs: []string{"p1"}, Scores: []float64{1}, Cards: []*Card{pcard("p1", "s1", "ready", 1)}}
	for _, tt := range []struct {
		name string
		q    RangeQ
		a    RangeA
		want bool
	}{
		{"every member, no more", RangeQ{Records: true, Limit: 10}, full, true},
		{"infinite bounds are every member", RangeQ{Records: true, Limit: 10, Min: "-inf", Max: "+inf"}, full, true},
		{"empty, and no more", RangeQ{Records: true, Limit: 10}, RangeA{}, true},
		{"more beyond the limit", RangeQ{Records: true, Limit: 1}, RangeA{IDs: full.IDs, Scores: full.Scores, Cards: full.Cards, HasMore: true}, false},
		{"a lower bound", RangeQ{Records: true, Limit: 10, Min: "(0"}, full, false},
		{"an upper bound", RangeQ{Records: true, Limit: 10, Max: "5"}, full, false},
		{"ids only", RangeQ{Limit: 10}, RangeA{IDs: full.IDs, Scores: full.Scores}, false},
	} {
		s := cell(tt.q, tt.a)
		if got := s.Work.Loaded("s1", "ready"); got != tt.want {
			t.Errorf("%s: Loaded = %v, want %v", tt.name, got, tt.want)
		}
		if got := s.Work.Loaded("s1", "waiting"); got {
			t.Errorf("%s: another cell is loaded", tt.name)
		}
	}
}

func TestCountsFromCountAndRCount(t *testing.T) {
	t.Parallel()
	rp := ReadPlan{
		Counts:  []CountQ{{Table: Work, Cells: []string{"a:b:ready", "s2:working"}}},
		RCounts: []RCountQ{{Table: Work, Cells: []string{"s3:ready", "s3:working"}}, {Table: Work, Cells: []string{"s4:ready"}, Min: "2", Max: "9"}},
	}
	ans := ReadAnswer{Counts: [][]int{{7, 8}}, RCounts: []RCountA{{Counts: []int{4, 5}, Sum: 9}, {Counts: []int{2}, Sum: 2}}}
	s, err := loadPartial(rp, ans, false)
	if err != nil {
		t.Fatal(err)
	}
	for cell, want := range map[[2]string]int{{"a:b", "ready"}: 7, {"s2", "working"}: 8, {"s3", "ready"}: 4, {"s3", "working"}: 5} {
		if got := s.Work.Count(cell[0], cell[1]); got != want {
			t.Errorf("Count(%s, %s) = %d, want %d", cell[0], cell[1], got, want)
		}
	}
	if n := s.Work.Count("s4", "ready"); n != 0 || len(s.Unloaded()) != 1 {
		t.Fatalf("a count over a range of scores is not the cell's count: %d, %v", n, s.Unloaded())
	}
}

func TestOpenBeforeAndFirstSentinelFromTheRead(t *testing.T) {
	t.Parallel()
	q := OpenBeforeQ("s1", 4.5)
	if q.Table != Work || q.Min != "-inf" || q.Max != "(4.5" || !reflect.DeepEqual(q.Cells, []string{"s1:waiting", "s1:ready", "s1:working", "s1:review", "s1:merging"}) {
		t.Fatalf("OpenBeforeQ: %+v", q)
	}
	rp := ReadPlan{RCounts: []RCountQ{q, OpenBeforeQ("s1", 9)}, Sprint: []SprintQ{{Kind: QueryFront, Stream: "s1"}, {Kind: QueryFront, Stream: "s2"}, {Kind: QueryFront, Stream: "s3"}}}
	ans := ReadAnswer{
		RCounts: []RCountA{{Counts: []int{1, 1, 0, 0, 0}, Sum: 2}, {Counts: []int{2, 2, 1, 0, 0}, Sum: 5}},
		Sprint: []Answer{
			{Front: &FrontAnswer{Stream: "s1", G: "g1", Sigma: 7, NBefore: 3}}, // its record is not returned: quarantined
			{Front: &FrontAnswer{Stream: "s2"}},                                // no sentinel
			{Front: &FrontAnswer{Stream: "s3", G: "g3", Sigma: 2, NBefore: 0}, Records: []TableCard{{Work, pcard("g3", "s3", Waiting, 2, "kind", Sentinel)}}},
		},
	}
	ans.Sprint[0].Front.GQuarantined = true
	s, err := loadPartial(rp, ans, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := OpenBefore(s, "s1", 4.5); n != 2 {
		t.Errorf("OpenBefore(s1, 4.5) = %d, want the rcount's 2", n)
	}
	if n := OpenBefore(s, "s1", 9); n != 5 {
		t.Errorf("OpenBefore(s1, 9) = %d, want the rcount's 5", n)
	}
	if n := OpenBefore(s, "s1", 7); n != 3 {
		t.Errorf("OpenBefore(s1, 7) = %d, want n_before 3 (sigma is 7)", n)
	}
	if n := OpenBefore(s, "s3", 2); n != 0 {
		t.Errorf("OpenBefore(s3, 2) = %d, want n_before 0", n)
	}
	// A quarantined sentinel is the first sentinel still: no record, so the
	// card is made from the answer, and nothing behind it is released.
	g := FirstSentinel(s, "s1")
	if g == nil || g.ID != "g1" || g.Row != "s1" || g.Col != Waiting || g.Score != 7 || !IsSentinel(g) || g.Placed() != true {
		t.Fatalf("FirstSentinel(s1), quarantined: %+v", g)
	}
	if !s.Partial.Fronts["s1"].GQuarantined {
		t.Fatal("the answer says G is quarantined")
	}
	if g := FirstSentinel(s, "s2"); g != nil {
		t.Fatalf("FirstSentinel(s2) with no sentinel: %+v", g)
	}
	if g := FirstSentinel(s, "s3"); g == nil || g.ID != "g3" || g.Rev != 1 {
		t.Fatalf("FirstSentinel(s3) is the record read: %+v", g)
	}
	if len(s.Unloaded()) != 0 {
		t.Fatalf("refused: %v", s.Unloaded())
	}
}

func TestPositionsOfASnapshotBuiltWhole(t *testing.T) {
	t.Parallel()
	s := wholeSnapshot([]string{"s1"}, nil,
		pcard("p1", "s1", Ready, 1), pcard("p2", "s1", Working, 2), pcard("g1", "s1", Waiting, 3, "kind", Sentinel),
		pcard("p3", "s1", Waiting, 4), pcard("g2", "s1", Waiting, 6, "kind", Sentinel), pcard("p0", "s1", Landed, 0))
	if g := FirstSentinel(s, "s1"); g == nil || g.ID != "g1" {
		t.Fatalf("FirstSentinel: %+v", g)
	}
	if g := FirstSentinel(s, "s9"); g != nil {
		t.Fatalf("FirstSentinel of a stream with none: %+v", g)
	}
	for score, want := range map[float64]int{0: 0, 1: 0, 1.5: 1, 3: 2, 4: 3, 4.5: 4, 99: 5} {
		if got := OpenBefore(s, "s1", score); got != want {
			t.Errorf("OpenBefore(s1, %v) = %d, want %d", score, got, want)
		}
	}
}

func TestLoadPartialRefusesAnAnswerThatDoesNotAnswerThePlan(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		edit func(rp *ReadPlan, ans *ReadAnswer)
	}{
		{"fewer records than ids", func(rp *ReadPlan, ans *ReadAnswer) { ans.IDs[Work] = ans.IDs[Work][:2] }},
		{"a record that is not the id read", func(rp *ReadPlan, ans *ReadAnswer) { ans.IDs[Work][0] = pcard("p9", "s1", "ready", 1) }},
		{"records of a table the plan did not read", func(rp *ReadPlan, ans *ReadAnswer) { ans.IDs[Merge] = []*Card{nil} }},
		{"ids of a table that is not one", func(rp *ReadPlan, ans *ReadAnswer) {
			rp.IDs["nowhere"] = []string{"x"}
			ans.IDs["nowhere"] = []*Card{nil}
		}},
		{"a range too few", func(rp *ReadPlan, ans *ReadAnswer) { ans.Ranges = ans.Ranges[:3] }},
		{"scores that are not the ids", func(rp *ReadPlan, ans *ReadAnswer) { ans.Ranges[0].Scores = []float64{1} }},
		{"records asked for and not returned", func(rp *ReadPlan, ans *ReadAnswer) { ans.Ranges[0].Cards = nil }},
		{"records returned and not asked for", func(rp *ReadPlan, ans *ReadAnswer) { rp.Ranges[0].Records = false }},
		{"a range record in another cell", func(rp *ReadPlan, ans *ReadAnswer) { ans.Ranges[0].Cards[0] = pcard("p1", "s1", "working", 1) }},
		{"a range that names no cell and no key", func(rp *ReadPlan, ans *ReadAnswer) { rp.Ranges[3].Key = "" }},
		{"a range that names both", func(rp *ReadPlan, ans *ReadAnswer) { rp.Ranges[3].Table, rp.Ranges[3].Cell = Work, "s1:ready" }},
		{"a range of half a cell", func(rp *ReadPlan, ans *ReadAnswer) { rp.Ranges[1].Cell = "" }},
		{"a range of a cell with no column", func(rp *ReadPlan, ans *ReadAnswer) { rp.Ranges[1].Cell = "s2" }},
		{"a count too few", func(rp *ReadPlan, ans *ReadAnswer) { ans.Counts = nil }},
		{"a count for fewer cells", func(rp *ReadPlan, ans *ReadAnswer) { ans.Counts[0] = []int{1} }},
		{"a count of an unknown table", func(rp *ReadPlan, ans *ReadAnswer) { rp.Counts[0].Table = "nowhere" }},
		{"a count of a cell with no column", func(rp *ReadPlan, ans *ReadAnswer) { rp.Counts[0].Cells[0] = "s1" }},
		{"an rcount too few", func(rp *ReadPlan, ans *ReadAnswer) { ans.RCounts = nil }},
		{"an rcount for fewer cells", func(rp *ReadPlan, ans *ReadAnswer) { ans.RCounts[0].Counts = []int{0} }},
		{"lines too few", func(rp *ReadPlan, ans *ReadAnswer) { ans.Lines = nil }},
		{"a composite query too few", func(rp *ReadPlan, ans *ReadAnswer) { ans.Sprint = ans.Sprint[:2] }},
		{"a composite answer of another kind", func(rp *ReadPlan, ans *ReadAnswer) { ans.Sprint[0].Kind = QueryFleet }},
		{"a composite record of an unknown table", func(rp *ReadPlan, ans *ReadAnswer) { ans.Sprint[2].Records[0].Table = "nowhere" }},
		{"a composite record that is no record", func(rp *ReadPlan, ans *ReadAnswer) { ans.Sprint[2].Records[0].Card = nil }},
		{"rows from a query that lists none", func(rp *ReadPlan, ans *ReadAnswer) { ans.Sprint[2].Rows = []string{"s1"} }},
		{"a front with no answer", func(rp *ReadPlan, ans *ReadAnswer) { ans.Sprint[2].Front = nil }},
		{"a front of another stream", func(rp *ReadPlan, ans *ReadAnswer) { ans.Sprint[2].Front.Stream = "s2" }},
	} {
		rp, ans := sampleRead()
		tt.edit(&rp, &ans)
		s, err := LoadPartial(rp, ans)
		if s != nil || !errors.Is(err, ErrMisaligned) {
			t.Errorf("%s: %v, %v", tt.name, s, err)
		}
	}

	// A line the log's contract does not allow is refused, naming its seq.
	rp, ans := sampleRead()
	ans.Lines[0].Lines[0].Body = []byte(`{"k":"m","ms":"1"}`)
	if s, err := LoadPartial(rp, ans); s != nil || err == nil || !strings.Contains(err.Error(), "line 7") {
		t.Errorf("a line of no card: %v, %v", s, err)
	}
	// The unedited pair loads.
	rp, ans = sampleRead()
	if _, err := LoadPartial(rp, ans); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPartialMergesWhatSeveralQueriesReadOfOneRecord(t *testing.T) {
	t.Parallel()
	rp := ReadPlan{
		IDs:    map[string][]string{Work: {"p1"}},
		Ranges: []RangeQ{{Table: Work, Cell: "s1:ready", Limit: 10, Records: true, Fields: []string{"open"}}},
		Sprint: []SprintQ{{Kind: QueryFront, Stream: "s1", Fields: []string{"kind"}}},
	}
	ans := ReadAnswer{
		IDs:    map[string][]*Card{Work: {pcard("p1", "s1", "ready", 1, "attempt", "2")}},
		Ranges: []RangeA{{IDs: []string{"p1"}, Scores: []float64{1}, Cards: []*Card{pcard("p1", "s1", "ready", 1, "open", "0")}}},
		Sprint: []Answer{{Front: &FrontAnswer{Stream: "s1"}, Records: []TableCard{{Work, pcard("p1", "s1", "ready", 1, "kind", "primary")}}}},
	}
	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Work.Card("p1")
	if c == nil || c.F("attempt") != "2" || c.F("open") != "0" || c.F("kind") != "primary" || len(s.Work.Cards) != 1 {
		t.Fatalf("p1 read by three queries: %+v", c)
	}
	if got := ids(s.Work.Cell("s1", "ready")); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Fatalf("Cell(s1, ready): %v", got)
	}
}

func TestLoadPartialSharesNothingWithTheAnswer(t *testing.T) {
	t.Parallel()
	rp, ans := sampleRead()
	_, ansBefore := sampleRead()
	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatal(err)
	}
	s.Work.Card("p1").Fields["attempt"] = "changed"
	s.Work.Card("p1").Score = 99
	s.Work.Card("g1").Fields["kind"] = "changed"
	if !reflect.DeepEqual(ans.IDs, ansBefore.IDs) || !reflect.DeepEqual(ans.Ranges, ansBefore.Ranges) || !reflect.DeepEqual(ans.Sprint, ansBefore.Sprint) {
		t.Fatal("a change to the snapshot changed the answer it was loaded from")
	}
	// And a card with no fields map gains none in the answer.
	bare := &Card{ID: "p1", Row: "s1", Col: "ready"}
	s2, err := LoadPartial(ReadPlan{IDs: map[string][]string{Work: {"p1"}}}, ReadAnswer{IDs: map[string][]*Card{Work: {bare}}})
	if err != nil {
		t.Fatal(err)
	}
	if bare.Fields != nil || s2.Work.Card("p1").Fields == nil {
		t.Fatalf("answer's %v, snapshot's %v", bare.Fields, s2.Work.Card("p1").Fields)
	}
}

// randomWhole is a random snapshot built whole: streams with cards in the
// work table's cells at distinct scores, some of them sentinels waiting.
func randomWhole(rng *rand.Rand) *Snapshot {
	streams := []string{"s1", "s2", "s3", "s4"}[:1+rng.IntN(4)]
	s := wholeSnapshot(streams, []string{"m1", "m2"})
	score := 0.0
	for _, st := range streams {
		for _, state := range States {
			for i := rng.IntN(4); i > 0; i-- {
				score += 1 + float64(rng.IntN(3))/2
				c := pcard(fmt.Sprintf("%s-%s-%d", st, state, i), st, state, score, "attempt", fmt.Sprint(rng.IntN(3)))
				if state == Waiting && rng.IntN(3) == 0 {
					c.Fields["kind"] = Sentinel
				}
				s.Work.Put(c)
			}
		}
	}
	return s
}

func TestPartialAgreesWithWhole(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(20260929, 5))
	for round := 0; round < 300; round++ {
		whole := randomWhole(rng)
		var store readAnswerer = wholeStore{whole}

		// A random plan: some cells read whole, some only counted, some only
		// partly read; the position of every stream; a few ids; the rows.
		rp := ReadPlan{IDs: map[string][]string{}}
		rp.Sprint = append(rp.Sprint, SprintQ{Kind: QueryStreams}, SprintQ{Kind: QueryFleet})
		for _, st := range whole.Work.Rows {
			rp.Sprint = append(rp.Sprint, SprintQ{Kind: QueryFront, Stream: st})
			for _, state := range States {
				cell := st + ":" + state
				switch rng.IntN(3) {
				case 0:
					rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: cell, Limit: MaxRangeLimit, Records: true})
				case 1:
					rp.Counts = append(rp.Counts, CountQ{Table: Work, Cells: []string{cell}})
				default:
					rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: cell, Limit: 1 + rng.IntN(2), Records: true})
				}
			}
			for k := rng.IntN(3); k > 0; k-- {
				rp.RCounts = append(rp.RCounts, OpenBeforeQ(st, float64(rng.IntN(40))/2))
			}
			for k := rng.IntN(3); k > 0; k-- {
				for _, c := range whole.Work.Column(States[rng.IntN(len(States))]) {
					rp.IDs[Work] = append(rp.IDs[Work], c.ID)
					break
				}
			}
		}
		ans := store.Answer(rp)
		part, err := LoadPartial(rp, ans)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}

		// What the read loaded is what the whole holds.
		for _, st := range whole.Work.Rows {
			for _, state := range States {
				if part.Work.Loaded(st, state) {
					if got, want := ids(part.Work.Cell(st, state)), ids(whole.Work.Cell(st, state)); !reflect.DeepEqual(got, want) {
						t.Fatalf("round %d: Cell(%s, %s) = %v, whole %v", round, st, state, got, want)
					}
				}
				if _, counted := part.Work.part.counts[[2]string{st, state}]; counted || part.Work.Loaded(st, state) {
					if got, want := part.Work.Count(st, state), whole.Work.Count(st, state); got != want {
						t.Fatalf("round %d: Count(%s, %s) = %d, whole %d", round, st, state, got, want)
					}
				}
			}
			gp, gw := FirstSentinel(part, st), FirstSentinel(whole, st)
			if (gp == nil) != (gw == nil) || gp != nil && (gp.ID != gw.ID || gp.Score != gw.Score) {
				t.Fatalf("round %d: FirstSentinel(%s) = %+v, whole %+v", round, st, gp, gw)
			}
			if gw != nil {
				if got, want := OpenBefore(part, st, gw.Score), OpenBefore(whole, st, gw.Score); got != want {
					t.Fatalf("round %d: OpenBefore(%s, sigma) = %d, whole %d", round, st, got, want)
				}
			}
		}
		for i, q := range rp.RCounts {
			row, _, _ := cellRef(q.Cells[0])
			bound := strings.TrimPrefix(q.Max, "(")
			var score float64
			fmt.Sscan(bound, &score)
			if got, want := OpenBefore(part, row, score), OpenBefore(whole, row, score); got != want {
				t.Fatalf("round %d: rcount %d: OpenBefore(%s, %v) = %d, whole %d", round, i, row, score, got, want)
			}
		}
		if !reflect.DeepEqual(part.Work.Rows, whole.Work.Rows) || !reflect.DeepEqual(part.Fleet.Rows, whole.Fleet.Rows) {
			t.Fatalf("round %d: rows %v %v, whole %v %v", round, part.Work.Rows, part.Fleet.Rows, whole.Work.Rows, whole.Fleet.Rows)
		}
		if got := part.Unloaded(); len(got) != 0 {
			t.Fatalf("round %d: %v", round, got)
		}
	}
}

// bigAnswer is a read of n records, the fields a rule names on each: half by
// their ids, and half as the records of 250 cells read whole, a stream a cell.
func bigAnswer(n int) (ReadPlan, ReadAnswer) {
	rp := ReadPlan{IDs: map[string][]string{}}
	ans := ReadAnswer{Epoch: 1, TimeMS: 1790000000123, IDs: map[string][]*Card{}}
	card := func(id, row, col string, i int) *Card {
		return pcard(id, row, col, float64(i), "attempt", "1", "avoid", "m1", "open", "0", "stream", row, "kind", "primary")
	}
	for i := 0; i < n/2; i++ {
		id := fmt.Sprintf("p%d", i)
		rp.IDs[Work] = append(rp.IDs[Work], id)
		ans.IDs[Work] = append(ans.IDs[Work], card(id, fmt.Sprintf("s%d", i%250), States[i%len(States)], i))
	}
	perCell := (n - n/2) / 250
	for s := 0; s < 250; s++ {
		row := fmt.Sprintf("t%d", s)
		rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: row + ":ready", Limit: MaxRangeLimit, Records: true})
		var a RangeA
		for j := 0; j < perCell; j++ {
			id := fmt.Sprintf("q%d.%d", s, j)
			a.IDs = append(a.IDs, id)
			a.Scores = append(a.Scores, float64(j))
			a.Cards = append(a.Cards, card(id, row, "ready", j))
		}
		ans.Ranges = append(ans.Ranges, a)
	}
	return rp, ans
}

func TestLoadPartialTenThousandRecords(t *testing.T) {
	t.Parallel()
	rp, ans := bigAnswer(MaxReadRecords)
	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Work.Cards) != MaxReadRecords || s.Work.Card("p4999") == nil || s.Work.Card("q249.19") == nil {
		t.Fatalf("%d cards", len(s.Work.Cards))
	}
	if !s.Work.Loaded("t7", "ready") || len(s.Work.Cell("t7", "ready")) != 20 || s.Work.Loaded("t7", "review") {
		t.Fatal("the cells of the ranges are not loaded whole")
	}
}

// loadLimit is the most a LoadPartial of a 10,000-record answer may take
// (IT05: 20 ms of Go time, measured by BenchmarkLoadPartial10000, which runs
// on every tick).
const loadLimit = 20 * time.Millisecond

func BenchmarkLoadPartial10000(b *testing.B) {
	rp, ans := bigAnswer(MaxReadRecords)
	b.ReportAllocs()
	for b.Loop() {
		s, err := LoadPartial(rp, ans)
		if err != nil {
			b.Fatal(err)
		}
		// The first read of a cell builds the table's index over every card
		// it holds: part of what a load costs the tick.
		if len(s.Work.Cell("t7", "ready")) != 20 {
			b.Fatal("the cell is not what the answer gave")
		}
	}
	per := b.Elapsed() / time.Duration(b.N)
	b.ReportMetric(float64(per)/float64(time.Millisecond), "ms/load")
	if per > loadLimit {
		b.Fatalf("LoadPartial of %d records took %v, the limit is %v", MaxReadRecords, per, loadLimit)
	}
}
