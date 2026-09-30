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
		{"Cards", func(s *Snapshot) any { return ids(s.Work.Cards()) }, []string{"g1", "p1", "p3"}, []string{}, "work cards"},
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

// guardedPlan reads all that the guarded reads need: the six cells of s1 whole
// (the five open ones and landed), the rows of the three tables, the fleet's
// control card, and the read cards of p3.
func guardedPlan() (ReadPlan, ReadAnswer) {
	rp := ReadPlan{
		IDs: map[string][]string{Fleet: {"ctl-m1"}},
		Sprint: []SprintQ{
			{Kind: QueryStreams}, {Kind: QueryFleet}, {Kind: QueryReaders},
			{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"p3"}}, Follow: []string{FollowRCards}},
		},
	}
	for _, st := range States {
		rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: "s1:" + string(st), Limit: MaxRangeLimit, Records: true})
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
	// The follows that reach the cards of a primary in a table make Of answer,
	// in the table the cards are in, for the primaries the query named: every
	// follow that reads a card of it there. The fleet's work card is read by two
	// follows of 1.0, `work` (the live card) and `withdrawn` (the same card, when
	// it is withdrawn), which are disjoint states of one card: one alone does not
	// read it.
	for _, tt := range []struct {
		name    string
		follows []string
		table   string
		asks    func(s *Snapshot) *Table
		answers bool
	}{
		{"rcards", []string{FollowRCards}, Readers, func(s *Snapshot) *Table { return s.Readers }, true},
		{"merge", []string{FollowMerge}, Merge, func(s *Snapshot) *Table { return s.Merge }, true},
		{"work and withdrawn", []string{FollowWork, FollowWithdrawn}, Fleet, func(s *Snapshot) *Table { return s.Fleet }, true},
		{"withdrawn and work", []string{FollowWithdrawn, FollowWork}, Fleet, func(s *Snapshot) *Table { return s.Fleet }, true},
		{"work alone", []string{FollowWork}, Fleet, func(s *Snapshot) *Table { return s.Fleet }, false},
		{"withdrawn alone", []string{FollowWithdrawn}, Fleet, func(s *Snapshot) *Table { return s.Fleet }, false},
	} {
		rp := ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"a", "b"}}, Follow: append([]string{FollowJOpen}, tt.follows...)}}}
		ans := ReadAnswer{Sprint: []Answer{{Kind: QueryRelated, Records: []TableCard{{tt.table, pcard("a@1", "x", "y", 1, "primary", "a")}}}}}
		s, err := loadPartial(rp, ans, false)
		if err != nil {
			t.Fatal(err)
		}
		tab := tt.asks(s)
		if !tt.answers {
			// Some of the follows that read the fleet's work card were not read: what the
			// read did load of it would be read as all of it.
			if got := tab.Of("a"); got != nil {
				t.Errorf("%s: Of(a) in %s = %v, want it refused", tt.name, tt.table, ids(got))
			}
			if got := tab.Of("b"); got != nil {
				t.Errorf("%s: Of(b) in %s = %v, want it refused", tt.name, tt.table, ids(got))
			}
			if len(s.Unloaded()) != 2 {
				t.Errorf("%s: refused %v, want the two reads", tt.name, s.Unloaded())
			}
			continue
		}
		if got := ids(tab.Of("a")); !reflect.DeepEqual(got, []string{"a@1"}) {
			t.Errorf("%s: Of(a) in %s = %v", tt.name, tt.table, got)
		}
		if got := tab.Of("b"); len(got) != 0 {
			t.Errorf("%s: Of(b), a primary with no card, gave %v", tt.name, ids(got))
		}
		if len(s.Unloaded()) != 0 {
			t.Errorf("%s: refused %v", tt.name, s.Unloaded())
		}
		if got := s.Work.Of("a"); got != nil || len(s.Unloaded()) != 1 {
			t.Errorf("%s: the work table's Of(a) is answered by no follow: %v, %v", tt.name, ids(got), s.Unloaded())
		}
	}

	// The follows are read of each id: work of one primary and withdrawn of
	// another do not answer for either.
	split := ReadPlan{Sprint: []SprintQ{
		{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"a", "b"}}, Follow: []string{FollowWork}},
		{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"b", "c"}}, Follow: []string{FollowWithdrawn}},
	}}
	sp, err := loadPartial(split, ReadAnswer{Sprint: []Answer{{Kind: QueryRelated}, {Kind: QueryRelated}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "c"} {
		if got := sp.Fleet.Of(id); got != nil {
			t.Errorf("Of(%s): only one of the two follows read it, gave %v", id, ids(got))
		}
	}
	if len(sp.Unloaded()) != 2 {
		t.Errorf("Of(a) and Of(c) each read a card that one follow of two loaded: refused %v", sp.Unloaded())
	}
	if got := sp.Fleet.Of("b"); len(got) != 0 || len(sp.Unloaded()) != 2 {
		t.Errorf("Of(b), both follows read in two queries, and no card of it: %v, refused %v", ids(got), sp.Unloaded())
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

// A follow that opens Of (the read cards, the merge card, the fleet's work
// cards) finds the cards of a primary by their primary field, so the query that
// reads them must read that field: a projection that leaves it out would return
// cards Of could not find, and Of would give none and say nothing (the cold read
// of the fixes to #4748). Such a plan is refused when it is built (Validate) and
// when its answer is loaded (LoadPartial), naming the query and the field.
func TestAFollowThatOpensOfMustReadThePrimaryField(t *testing.T) {
	t.Parallel()
	related := func(fields []string, follow ...string) SprintQ {
		return SprintQ{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"s1-7"}}, Fields: fields, Follow: follow}
	}
	good := SprintQ{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"p9"}}, Follow: []string{FollowRCards}}
	answerOf := func(rp ReadPlan) ReadAnswer {
		ans := ReadAnswer{}
		for _, q := range rp.Sprint {
			ans.Sprint = append(ans.Sprint, Answer{Kind: q.Kind})
		}
		return ans
	}

	// The projection that leaves out primary is refused for every follow that opens Of.
	for _, f := range []string{FollowRCards, FollowMerge, FollowWork, FollowWithdrawn} {
		for name, fields := range map[string][]string{"a projection": {"status"}, "the summary": {}} {
			rp := ReadPlan{Sprint: []SprintQ{good, related(fields, FollowJOpen, f)}}
			err := rp.Validate()
			if !errors.Is(err, ErrBadPlan) {
				t.Errorf("%s, follow %s: Validate: %v", name, f, err)
				continue
			}
			for _, want := range []string{"composite query 1", string(QueryRelated), "follows " + f, "field " + PrimaryField} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s, follow %s: the error does not say %q: %v", name, f, want, err)
				}
			}
			for _, strict := range []bool{true, false} {
				if s, err := loadPartial(rp, answerOf(rp), strict); s != nil || !errors.Is(err, ErrBadPlan) {
					t.Errorf("%s, follow %s, strict %v: LoadPartial: %v, %v", name, f, strict, s, err)
				}
			}
		}
	}
	if s, err := LoadPartial(ReadPlan{Sprint: []SprintQ{related([]string{"status"}, FollowRCards)}}, ReadAnswer{Sprint: []Answer{{Kind: QueryRelated}}}); s != nil || !errors.Is(err, ErrBadPlan) {
		t.Errorf("LoadPartial: %v, %v", s, err)
	}

	// The plans that read primary, or that open no Of, are not refused.
	for name, q := range map[string]SprintQ{
		"the whole record":               related(nil, FollowRCards),
		"primary named":                  related([]string{"status", PrimaryField}, FollowRCards),
		"primary alone":                  related([]string{PrimaryField}, FollowMerge, FollowWork, FollowWithdrawn),
		"a summary, and no follow":       related([]string{}),
		"a projection, and no follow":    related([]string{"status"}),
		"follows that open no Of":        related([]string{"status"}, FollowControl, FollowNeeds, FollowMember, FollowJOpen, FollowDue, FollowIndex),
		"a follow no table has":          related([]string{"status"}, "nothing"),
		"the summary and a plain follow": related([]string{}, FollowJOpen),
	} {
		rp := ReadPlan{Sprint: []SprintQ{q}}
		if err := rp.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", name, err)
		}
		if _, err := loadPartial(rp, answerOf(rp), true); err != nil {
			t.Errorf("%s: LoadPartial: %v", name, err)
		}
	}
	// A query of another kind names no follow to open Of, whatever its fields.
	if err := (ReadPlan{Sprint: []SprintQ{{Kind: QueryFleet, Fields: []string{"status"}}, {Kind: QueryFront, Stream: "s1", Fields: []string{}}}}).Validate(); err != nil {
		t.Errorf("listings and front: %v", err)
	}
	if err := (ReadPlan{}).Validate(); err != nil {
		t.Errorf("an empty plan: %v", err)
	}

	// The plan of the cold read: related over s1-7, following the read cards, with
	// only status named, was answered with a read card that had no primary, and Of
	// found none of it. With primary named the card is found, in a test build too.
	rp := ReadPlan{Sprint: []SprintQ{related([]string{"status", PrimaryField}, FollowRCards)}}
	ans := ReadAnswer{Sprint: []Answer{{Kind: QueryRelated, Records: []TableCard{
		{Readers, pcard("s1-7@r1", "r1", Asked, 1, "status", "asked", "primary", "s1-7")},
	}}}}
	s, err := loadPartial(rp, ans, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(s.Readers.Of("s1-7")); !reflect.DeepEqual(got, []string{"s1-7@r1"}) {
		t.Fatalf("Of(s1-7) with primary read: %v", got)
	}
	if len(s.Unloaded()) != 0 {
		t.Fatalf("refused %v", s.Unloaded())
	}
}

// The table finds a card by its primary through the card's own read of the
// field (Card.field), the read that F is: a card that holds the field is
// found, and a card that does not is no primary's. A control card, read by a
// listing that names its status only, has no primary, and is not a refusal.
func TestTheIndexOfPrimariesReadsThroughTheCardsOwnGuard(t *testing.T) {
	t.Parallel()
	whole, thin, summary := newCardLoad(nil, &unloadedLog{}), newCardLoad([]string{"status"}, &unloadedLog{}), newCardLoad([]string{}, &unloadedLog{})
	for name, tt := range map[string]struct {
		load     *cardLoad
		name     string
		wantHeld bool
	}{
		"built whole":        {nil, "status", true},
		"built whole, other": {nil, "anything", true},
		"read whole":         {whole, "anything", true},
		"a field named":      {thin, "status", true},
		"a field not named":  {thin, PrimaryField, false},
		"the summary":        {summary, "status", false},
	} {
		c := &Card{ID: "c", Fields: map[string]string{"status": "up", "anything": "x", PrimaryField: "p1"}, load: tt.load}
		v, held := c.field(tt.name)
		if held != tt.wantHeld || held && v != c.Fields[tt.name] || !held && v != "" {
			t.Errorf("%s: field(%q) = %q, %v", name, tt.name, v, held)
		}
	}

	// A record of the readers' table that a query read without primary is not
	// found by the primary the answer happens to hold, though the follow read
	// nothing else of p3: the field it did not name is not there to be read.
	rp := ReadPlan{
		Ranges: []RangeQ{{Table: Readers, Cell: "r1:asked", Limit: 10, Records: true, Fields: []string{"status"}}},
		Sprint: []SprintQ{{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"p3"}}, Follow: []string{FollowRCards}}},
	}
	ans := ReadAnswer{
		Tset: []TsetAnswer{{IDs: []string{"p3@1"}, Scores: []float64{1}, Records: []*Card{pcard("p3@1", "r1", Asked, 1, "status", "asked", "primary", "p3")}}},
		Sprint: []Answer{{Kind: QueryRelated, Records: []TableCard{
			{Readers, pcard("p3@2", "r1", Asked, 2, "status", "asked", "primary", "p3")},
		}}},
	}
	s, err := loadPartial(rp, ans, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(s.Readers.Of("p3")); !reflect.DeepEqual(got, []string{"p3@2"}) {
		t.Fatalf("Of(p3) is the card that holds primary: %v", got)
	}

	// The fleet's control card, read by a listing that names status, has no
	// primary and is nobody's work card; Of over the work cards a follow read
	// neither refuses it nor finds it.
	fleet := ReadPlan{Sprint: []SprintQ{
		{Kind: QueryFleet, Units: 1, Fields: []string{"status"}},
		{Kind: QueryRelated, Table: Work, Source: IDSource{Kind: SourceIDs, IDs: []string{"p1"}}, Follow: []string{FollowWork, FollowWithdrawn}},
	}}
	fans := ReadAnswer{Sprint: []Answer{
		{Kind: QueryFleet, Rows: []string{"m1"}, Records: []TableCard{{Fleet, pcard("ctl-m1", "m1", Ctl, 0, "status", "up")}}},
		{Kind: QueryRelated, Records: []TableCard{{Fleet, pcard("p1@1", "m1", Ready, 1, "primary", "p1")}}},
	}}
	fs, err := loadPartial(fleet, fans, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(fs.Fleet.Of("p1")); !reflect.DeepEqual(got, []string{"p1@1"}) {
		t.Fatalf("Of(p1) in the fleet: %v", got)
	}
	if got := fs.UpMembers(); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("UpMembers: %v", got)
	}
	if err := fs.UnloadedErr(); err != nil {
		t.Fatalf("a control card that holds no primary was refused: %v", err)
	}
}

// A scan of every card of a table is a read of all of it: Cards is answered on a
// table built whole, and on the work table of a plan that read its rows and every
// cell of every row whole, and is refused on any other partial table. The cards
// the read did load, whatever it left out, are LoadedCards.
func TestCardsIsAScanOfTheWholeTable(t *testing.T) {
	t.Parallel()
	world := guardWorld()
	world.Work.Put(pcard("p0", "", "", 0, "outcome", "dropped")) // a kept record with no place
	if got := ids(world.Work.Cards()); !reflect.DeepEqual(got, []string{"g1", "p0", "p1", "p3"}) {
		t.Fatalf("Cards of a table built whole, placed and kept, in id order: %v", got)
	}
	if got := ids(world.Work.LoadedCards()); !reflect.DeepEqual(got, []string{"g1", "p0", "p1", "p3"}) {
		t.Fatalf("LoadedCards of a table built whole: %v", got)
	}
	var none *Table
	if none.Cards() != nil || none.LoadedCards() != nil {
		t.Fatal("no table has cards")
	}
	// The list is a copy: what a caller does to it is not done to the table.
	list := world.Work.Cards()
	list[0] = nil
	if got := ids(world.Work.Cards()); len(got) != 4 {
		t.Fatalf("Cards handed out the table's own list: %v", got)
	}

	// Every cell of every row read whole, and the rows read: the table's cards
	// (and the kept record an ids query named) are all there.
	rp, ans := guardedPlan()
	rp.IDs = map[string][]string{Work: {"p0"}, Fleet: {"ctl-m1"}}
	full := wholeStore{world}.Answer(rp)
	full.Sprint = ans.Sprint
	s, err := loadPartial(rp, full, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(s.Work.Cards()); !reflect.DeepEqual(got, []string{"g1", "p0", "p1", "p3"}) {
		t.Fatalf("Cards of the work table read whole: %v", got)
	}
	if err := s.UnloadedErr(); err != nil {
		t.Fatalf("a scan of a table loaded whole was refused: %v", err)
	}
	// Another partial table has no scan, whatever its rows and cells: its columns
	// are not named, and the merge, fleet and readers tables hold hidden ones.
	for _, name := range []string{Readers, Merge, Fleet} {
		msg := mustPanic(t, func() { s.T(name).Cards() })
		if !strings.Contains(msg, unloadedMessage+": "+name+" cards") {
			t.Errorf("Cards of the %s table: %q", name, msg)
		}
		s.T(name).LoadedCards() // never refused
	}
	if err := s.UnloadedErr(); err != nil { // a strict snapshot panics at a refused read and keeps none
		t.Fatalf("LoadedCards was refused: %v", err)
	}
	if got := ids(s.Fleet.LoadedCards()); !reflect.DeepEqual(got, []string{"ctl-m1"}) {
		t.Errorf("LoadedCards of the fleet table: %v", got)
	}

	// One cell not read whole, or the rows not read: refused, in each mode.
	for name, drop := range map[string]func(rp *ReadPlan, ans *ReadAnswer){
		"a cell not read whole": func(rp *ReadPlan, ans *ReadAnswer) {
			for i, sl := range rp.TsetSlots() { // the landed cell answered with more members than it returned
				if sl.Kind == AnswerRange && rp.Ranges[sl.Index].Cell == "s1:landed" {
					ans.Tset[i].HasMore = true
				}
			}
		},
		"the rows not read": func(rp *ReadPlan, ans *ReadAnswer) { rp.Sprint = rp.Sprint[1:]; ans.Sprint = ans.Sprint[1:] },
	} {
		for _, strict := range []bool{true, false} {
			rp2, ans2 := guardedPlan()
			drop(&rp2, &ans2)
			s2, err := loadPartial(rp2, ans2, strict)
			if err != nil {
				t.Fatal(name, err)
			}
			if strict {
				if msg := mustPanic(t, func() { s2.Work.Cards() }); !strings.Contains(msg, unloadedMessage+": work cards") {
					t.Errorf("%s: %q", name, msg)
				}
				continue
			}
			if got := s2.Work.Cards(); got != nil {
				t.Errorf("%s: a refused scan gave %v", name, ids(got))
			}
			if reads := s2.Unloaded(); len(reads) != 1 || reads[0] != unloadedMessage+": work cards" {
				t.Errorf("%s: refused %q", name, reads)
			}
			if got := ids(s2.Work.LoadedCards()); len(got) == 0 {
				t.Errorf("%s: LoadedCards gave none of what was loaded", name)
			}
		}
	}
}

// The scans of today's steps (add --count, add with no score, rank --first)
// compute a highest or a lowest over the cards, so a plan that read only some of
// them gets none of them (the cold read of the fixes to #4748: a read of s1-2
// alone got AddIDs = s1-3, s1-4 against s1-8, s1-9 whole).
func TestTheScansOfTodaysStepsAreRefusedOnAPartialSnapshot(t *testing.T) {
	t.Parallel()
	world := wholeSnapshot([]string{"s1"}, nil,
		pcard("s1-1", "s1", Landed, 1),
		pcard("s1-2", "s1", Ready, 2),
		pcard("g1", "s1", Waiting, 3, "kind", Sentinel),
		pcard("s1-7", "s1", Waiting, 7),
	)
	add := AddReq{Stream: "s1", Count: 2}
	rank := RankReq{IDs: []string{"s1-2"}, First: true}
	scans := []struct {
		name  string
		run   func(s *Snapshot) any
		whole any
	}{
		{"AddIDs", func(s *Snapshot) any { return AddIDs(s, add) }, []string{"s1-8", "s1-9"}},
		{"addScores", func(s *Snapshot) any { sc, _ := addScores(s, AddReq{Stream: "s1"}, 1); return sc }, []float64{8}},
		{"Rank --first", func(s *Snapshot) any {
			p := Rank(s, rank)
			return p.Units[0].Moved
		}, "s1-2 score 2 -> 0 (0 copies)"},
	}
	for _, sc := range scans {
		// Built whole: as it was.
		if got := sc.run(world); !reflect.DeepEqual(got, sc.whole) {
			t.Errorf("%s on the whole: %#v, want %#v", sc.name, got, sc.whole)
		}
		// A plan that read only s1-2: the scan is refused, in a test build and a
		// release build.
		rp := ReadPlan{IDs: map[string][]string{Work: {"s1-2"}}}
		ans := wholeStore{world}.Answer(rp)
		strict, err := loadPartial(rp, ans, true)
		if err != nil {
			t.Fatal(err)
		}
		if msg := mustPanic(t, func() { sc.run(strict) }); !strings.Contains(msg, unloadedMessage+": work cards") {
			t.Errorf("%s: panicked with %q", sc.name, msg)
		}
		rel, err := loadPartial(rp, ans, false)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() { _ = recover() }() // a step may go on from nothing; the plan is refused below
			sc.run(rel)
		}()
		if err := rel.UnloadedErr(); !errors.Is(err, ErrUnloaded) || !strings.Contains(err.Error(), "work cards") {
			t.Errorf("%s: a release build did not refuse the plan: %v", sc.name, err)
		}
	}
}

// No field of a table gives its cards or its rows: a read of either goes through
// the methods that are guarded (Cards, Rows).
func TestNoExportedFieldOfATableGivesItsCardsOrRows(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(Table{})
	var exported []string
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			exported = append(exported, f.Name)
		}
	}
	if want := []string{"Name", "Epoch", "Revision", "Texts"}; !reflect.DeepEqual(exported, want) {
		t.Fatalf("the exported fields of a table are %v, want %v: a card or a row would be read past the guards", exported, want)
	}
}
