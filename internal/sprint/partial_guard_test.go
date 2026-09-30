package sprint

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Every read of a snapshot loaded from a read plan is guarded (the upper
// design, version 2.1, 1.5.2): a read of what the plan did not load panics in a
// test build and is refused, recorded and empty, in a release build, so that a
// scan cannot come back unnoticed. Cell, Column and Count are in
// partial_test.go; these are the others: the rows, the cards of a primary, and
// the stream's line with everything that reads it.

// guardWorld is a snapshot built whole. Stream s1 has a ready primary p1, a
// sentinel g1 waiting at 5 and a waiting primary p3 at 6, so p3 waits behind g1;
// member m1 is up; p3 has one read card.
func guardWorld() *Snapshot {
	s := wholeSnapshot([]string{"s1"}, []string{"m1"},
		pcard("p1", "s1", Ready, 1),
		pcard("g1", "s1", Waiting, 5, "kind", Sentinel),
		pcard("p3", "s1", Waiting, 6),
	)
	s.Fleet.Put(pcard("ctl-m1", "m1", Ctl, 0, "status", Up))
	s.Readers.Put(pcard("p3@1", "r1", Asked, 1, "primary", "p3"))
	return s
}

// thinPartial is what a plan that read only the id p3 loaded, in a test build
// (strict) or a release build.
func thinPartial(t *testing.T, strict bool) *Snapshot {
	t.Helper()
	rp := ReadPlan{IDs: map[string][]string{Work: {"p3"}}}
	ans := ReadAnswer{Epoch: "3", Tset: []TsetAnswer{{Records: []*Card{pcard("p3", "s1", Waiting, 6)}}}}
	s, err := loadPartial(rp, ans, strict)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// oneID is the id of a card in a list of at most one: empty for no card.
func oneID(c *Card) []string {
	if c == nil {
		return []string{}
	}
	return []string{c.ID}
}

// guardedRead is one read of a snapshot: what it gives on the whole world, what
// a release build gives when it is refused, and what the refusal is named.
type guardedRead struct {
	name    string
	read    func(s *Snapshot) any
	whole   any
	refused any
	named   string
}

func guardedReads() []guardedRead {
	sentinel := func(s *Snapshot) *Card { return guardWorld().Work.Card("g1") }
	waiting := func(s *Snapshot) *Card { return s.Work.Card("p3") }
	edges := map[string][]string{"g1": {"p3"}} // p3 waits behind g1, and g1 needs p3: a cycle through the line
	return []guardedRead{
		{"openLine", func(s *Snapshot) any { return ids(s.Work.openLine("s1")) }, []string{"p1", "g1", "p3"}, []string{}, "work s1 line"},
		{"StopBefore", func(s *Snapshot) any { return oneID(StopBefore(s, "s1", 6, nil)) }, []string{"g1"}, []string{}, "work s1 line"},
		{"PositionWaits", func(s *Snapshot) any { return PositionWaits(s, waiting(s), nil) }, []string{"g1"}, []string(nil), "work s1 line"},
		{"Behind", func(s *Snapshot) any { return ids(Behind(s, sentinel(s))) }, []string{"p3"}, []string{}, "work s1 line"},
		{"WaitsFor", func(s *Snapshot) any { return WaitsFor(s, waiting(s), nil) }, []string{"g1"}, []string(nil), "work s1 line"},
		{"NeedsCycle", func(s *Snapshot) any { return NeedsCycle(s, edges) }, []string{"g1", "p3", "g1"}, []string(nil), "work s1 line"},
		{"Of", func(s *Snapshot) any { return ids(s.Readers.Of("p3")) }, []string{"p3@1"}, []string{}, "readers cards of p3"},
		{"Streams", func(s *Snapshot) any { return s.Streams() }, []string{"s1"}, []string(nil), "work rows"},
		{"UpMembers", func(s *Snapshot) any { return s.UpMembers() }, []string{"m1"}, []string(nil), "fleet rows"},
		{"HasRow", func(s *Snapshot) any { return s.Work.HasRow("s1") }, true, false, "work rows"},
		{"Rows", func(s *Snapshot) any { return s.Work.Rows() }, []string{"s1"}, []string(nil), "work rows"},
	}
}

func TestEveryReadOfWhatWasNotLoadedPanicsInTest(t *testing.T) {
	t.Parallel()
	for _, g := range guardedReads() {
		t.Run(g.name, func(t *testing.T) {
			t.Parallel()
			s := thinPartial(t, true)
			msg := mustPanic(t, func() { g.read(s) })
			if !strings.Contains(msg, unloadedMessage+": "+g.named) {
				t.Fatalf("panicked with %q, want the read named: %q", msg, unloadedMessage+": "+g.named)
			}
		})
	}
}

func TestEveryReadOfWhatWasNotLoadedIsRefusedInRelease(t *testing.T) {
	t.Parallel()
	for _, g := range guardedReads() {
		t.Run(g.name, func(t *testing.T) {
			t.Parallel()
			s := thinPartial(t, false)
			got := g.read(s)
			if !reflect.DeepEqual(got, g.refused) {
				t.Fatalf("a refused read gave %#v, want %#v: nothing that happens to be known", got, g.refused)
			}
			if reads := s.Unloaded(); len(reads) != 1 || reads[0] != unloadedMessage+": "+g.named {
				t.Fatalf("the refused reads: %q, want the one read named %q", reads, g.named)
			}
			err := s.UnloadedErr()
			var ue *UnloadedError
			if !errors.Is(err, ErrUnloaded) || !errors.As(err, &ue) || len(ue.Reads) != 1 {
				t.Fatalf("UnloadedErr: %v", err)
			}
		})
	}
}

// The reads give what they gave before on a table built whole: the guards are
// on tables loaded from a plan and nowhere else.
func TestEveryGuardedReadOnATableBuiltWholeIsAsItWas(t *testing.T) {
	t.Parallel()
	for _, g := range guardedReads() {
		t.Run(g.name, func(t *testing.T) {
			t.Parallel()
			s := guardWorld()
			if got := g.read(s); !reflect.DeepEqual(got, g.whole) {
				t.Fatalf("on the whole world: %#v, want %#v", got, g.whole)
			}
			if err := s.UnloadedErr(); err != nil {
				t.Fatalf("a snapshot built whole refused: %v", err)
			}
		})
	}
	var none *Table
	if none.Rows() != nil {
		t.Fatal("no table has rows")
	}
}

// guardedPlan reads all that the guarded reads need: the five open cells of s1
// whole, the rows of the three tables, the fleet's control card, and the read
// cards of p3.
func guardedPlan() (ReadPlan, ReadAnswer) {
	rp := ReadPlan{
		IDs: map[string][]string{Fleet: {"ctl-m1"}},
		Sprint: []SprintQ{
			{Kind: QueryStreams}, {Kind: QueryFleet}, {Kind: QueryReaders},
			{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"p3"}}, Follow: []string{FollowRCards}},
		},
	}
	for _, cell := range OpenCells("s1") {
		rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: cell, Limit: MaxRangeLimit, Records: true})
	}
	ans := wholeStore{guardWorld()}.Answer(rp)
	ans.Sprint[3] = Answer{Kind: QueryRelated, Records: []TableCard{{Readers, pcard("p3@1", "r1", Asked, 1, "primary", "p3")}}}
	return rp, ans
}

// What a plan loaded, the guarded reads give as the whole does; what it did not
// load is refused beside it.
func TestGuardedReadsAnswerWhatWasLoaded(t *testing.T) {
	t.Parallel()
	rp, ans := guardedPlan()
	s, err := LoadPartial(rp, ans)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range guardedReads() {
		if got := g.read(s); !reflect.DeepEqual(got, g.whole) {
			t.Errorf("%s on what the plan loaded: %#v, the whole gives %#v", g.name, got, g.whole)
		}
	}
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("refused: %v", err)
	}
	// Beside it, a primary the plan followed nothing of is refused.
	msg := mustPanic(t, func() { s.Readers.Of("p1") })
	if !strings.Contains(msg, "readers cards of p1") {
		t.Fatalf("Of(p1): %q", msg)
	}
}

func TestOfAnswersForTheFollowsThatWereRead(t *testing.T) {
	t.Parallel()
	// Each follow that reaches a card of a primary makes Of answer, in the
	// table the card is in, for the primaries the query named.
	for _, tt := range []struct {
		follow, table string
		asks          func(s *Snapshot) *Table
	}{
		{FollowRCards, Readers, func(s *Snapshot) *Table { return s.Readers }},
		{FollowMerge, Merge, func(s *Snapshot) *Table { return s.Merge }},
		{FollowWork, Fleet, func(s *Snapshot) *Table { return s.Fleet }},
		{FollowWithdrawn, Fleet, func(s *Snapshot) *Table { return s.Fleet }},
	} {
		rp := ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"a", "b"}}, Follow: []string{FollowJOpen, tt.follow}}}}
		ans := ReadAnswer{Sprint: []Answer{{Kind: QueryRelated, Records: []TableCard{{tt.table, pcard("a@1", "x", "y", 1, "primary", "a")}}}}}
		s, err := loadPartial(rp, ans, false)
		if err != nil {
			t.Fatal(err)
		}
		tab := tt.asks(s)
		if got := ids(tab.Of("a")); !reflect.DeepEqual(got, []string{"a@1"}) {
			t.Errorf("follow %s: Of(a) in %s = %v", tt.follow, tt.table, got)
		}
		if got := tab.Of("b"); len(got) != 0 {
			t.Errorf("follow %s: Of(b), a primary with no card, gave %v", tt.follow, ids(got))
		}
		if len(s.Unloaded()) != 0 {
			t.Errorf("follow %s: refused %v", tt.follow, s.Unloaded())
		}
		if got := s.Work.Of("a"); got != nil || len(s.Unloaded()) != 1 {
			t.Errorf("follow %s: the work table's Of(a) is answered by no follow: %v, %v", tt.follow, ids(got), s.Unloaded())
		}
	}

	// A follow that reaches no card of a primary makes Of answer for nothing.
	for _, f := range []string{FollowControl, FollowNeeds, FollowMember, FollowJOpen, FollowDue, FollowIndex} {
		rp := ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"a"}}, Follow: []string{f}}}}
		s, err := loadPartial(rp, ReadAnswer{Sprint: []Answer{{Kind: QueryRelated}}}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, tab := range []*Table{s.Readers, s.Merge, s.Fleet, s.Work} {
			if got := tab.Of("a"); got != nil {
				t.Errorf("follow %s: Of(a) in %s = %v", f, tab.Name, ids(got))
			}
		}
		if len(s.Unloaded()) != 4 {
			t.Errorf("follow %s: %d reads refused, want the four", f, len(s.Unloaded()))
		}
	}

	// A head or a line names its ids in the answer; the answer may name no more
	// than the source's bound.
	head := ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Source: IDSource{Kind: SourceHead, Key: "elig:s", Limit: 2}, Follow: []string{FollowRCards}}}}
	s, err := loadPartial(head, ReadAnswer{Sprint: []Answer{{Kind: QueryRelated, IDs: []string{"a", "b"}}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Readers.Of("a")
	s.Readers.Of("b")
	if len(s.Unloaded()) != 0 {
		t.Errorf("a head's ids: refused %v", s.Unloaded())
	}
	s.Readers.Of("c")
	if len(s.Unloaded()) != 1 {
		t.Errorf("an id the head did not name was answered")
	}
	line := ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Source: IDSource{Kind: SourceLine, Seq: 9}, Follow: []string{FollowRCards}}}}
	tooMany := make([]string, MaxLineIDs+1)
	for _, c := range []struct {
		name string
		rp   ReadPlan
		a    Answer
	}{
		{"a head that names more ids than its limit", head, Answer{Kind: QueryRelated, IDs: []string{"a", "b", "c"}}},
		{"a line that names more ids than a line holds", line, Answer{Kind: QueryRelated, IDs: tooMany}},
		{"ids the plan named, and others answered", ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Source: IDSource{Kind: SourceIDs, IDs: []string{"a"}}}}}, Answer{Kind: QueryRelated, IDs: []string{"b"}}},
	} {
		if s, err := loadPartial(c.rp, ReadAnswer{Sprint: []Answer{c.a}}, false); s != nil || !errors.Is(err, ErrMisaligned) {
			t.Errorf("%s: %v, %v", c.name, s, err)
		}
	}
	// The ids the plan named, answered again, are the same.
	same := ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Source: IDSource{Kind: SourceIDs, IDs: []string{"a"}}, Follow: []string{FollowRCards}}}}
	if _, err := loadPartial(same, ReadAnswer{Sprint: []Answer{{Kind: QueryRelated, IDs: []string{"a"}}}}, false); err != nil {
		t.Errorf("the same ids: %v", err)
	}
}
