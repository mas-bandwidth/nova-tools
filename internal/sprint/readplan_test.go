package sprint

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

// Read plans (the upper design, version 2.1, 1.0, 1.4.2 and IT05): what a plan
// costs, how it is cut to fit layer 1's bounds, and how a rule's read is
// halved after a BUDGET.

// fieldsOf is a projection of n fields.
func fieldsOf(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("f%d", i)
	}
	return out
}

func TestQueryCostTable(t *testing.T) {
	t.Parallel()
	list := func(n int) IDSource { return IDSource{Kind: SourceIDs, IDs: make([]string, n)} }
	allFollows := append([]string(nil), Follows...)
	for _, tt := range []struct {
		name string
		q    SprintQ
		want Cost // records and range ids; bytes are checked below
	}{
		// related: 1 an id, plus 1 a follow, 15 for rcards, 64 for needs.
		{"related, no follow", SprintQ{Kind: QueryRelated, Table: Work, Source: list(10)}, Cost{Records: 10}},
		{"related, one follow", SprintQ{Kind: QueryRelated, Source: list(10), Follow: []string{FollowWork}}, Cost{Records: 20}},
		{"related, rcards", SprintQ{Kind: QueryRelated, Source: list(10), Follow: []string{FollowRCards}}, Cost{Records: 160}},
		{"related, needs", SprintQ{Kind: QueryRelated, Source: list(10), Follow: []string{FollowNeeds}}, Cost{Records: 650}},
		{"related, every follow", SprintQ{Kind: QueryRelated, Source: list(2), Follow: allFollows}, Cost{Records: 2 * (1 + 8 + 15 + 64)}},
		{"related, a head reads its limit in range ids", SprintQ{Kind: QueryRelated, Source: IDSource{Kind: SourceHead, Key: "elig:s", Limit: 40}}, Cost{Records: 40, RangeIDs: 40}},
		{"related, a line's ids", SprintQ{Kind: QueryRelated, Source: IDSource{Kind: SourceLine, Seq: 9}}, Cost{Records: MaxLineIDs}},
		{"related, a line's about", SprintQ{Kind: QueryRelated, Source: IDSource{Kind: SourceLine, Seq: 9, About: true}}, Cost{Records: MaxAboutIDs}},
		{"related, no ids", SprintQ{Kind: QueryRelated}, Cost{}},
		// front: 1 + the limits, each head's records with its follows.
		{"front, no heads", SprintQ{Kind: QueryFront, Stream: "s"}, Cost{Records: 1, RangeIDs: 1}},
		{"front, four heads", SprintQ{Kind: QueryFront, Stream: "s", Heads: []HeadQ{
			{Index: HeadEligBelow, Limit: 5}, {Index: HeadFreshBelow, Limit: 6}, {Index: HeadFreshAbove, Limit: 7}, {Index: HeadAgain, Limit: 8},
		}}, Cost{Records: 1 + 26, RangeIDs: 1 + 26}},
		{"front, a follow on a head", SprintQ{Kind: QueryFront, Stream: "s", Heads: []HeadQ{
			{Index: HeadFreshBelow, Limit: 13}, {Index: HeadAgain, Limit: 13, Follow: []string{FollowWithdrawn}},
		}}, Cost{Records: 1 + 13 + 26, RangeIDs: 1 + 26}},
		// waiters: 1 + lim an id of the source.
		{"waiters, ids", SprintQ{Kind: QueryWaiters, Source: list(4), Limit: 10}, Cost{Records: 44, RangeIDs: 40}},
		{"waiters, a line's ids", SprintQ{Kind: QueryWaiters, Source: IDSource{Kind: SourceLine, Seq: 3}, Limit: 1}, Cost{Records: 2 * MaxLineIDs, RangeIDs: MaxLineIDs}},
		// streams: 2 a stream, plus stuck range ids a stream.
		{"streams, the most", SprintQ{Kind: QueryStreams, Limit: 80}, Cost{Records: 2 * MaxStreams, RangeIDs: 80 * MaxStreams}},
		{"streams, ten", SprintQ{Kind: QueryStreams, Units: 10, Limit: 5}, Cost{Records: 20, RangeIDs: 50}},
		// fleet and readers: 1 a member, 1 a reader.
		{"fleet, the most", SprintQ{Kind: QueryFleet}, Cost{Records: MaxMembers}},
		{"fleet, three", SprintQ{Kind: QueryFleet, Units: 3}, Cost{Records: 3}},
		{"readers, the most", SprintQ{Kind: QueryReaders}, Cost{Records: MaxReaders}},
		{"readers, seven", SprintQ{Kind: QueryReaders, Units: 7}, Cost{Records: 7}},
		// needchain: up to max records.
		{"needchain", SprintQ{Kind: QueryNeedchain, Source: list(50), Limit: 300}, Cost{Records: 300}},
		// jnote: 1 + subjects a note.
		{"jnote, five subjects", SprintQ{Kind: QueryJnote, Source: list(3), Subjects: 5}, Cost{Records: 18}},
		{"jnote, the most", SprintQ{Kind: QueryJnote, Source: list(1)}, Cost{Records: 1 + MaxAboutIDs}},
		// a kind the table does not know is a whole read.
		{"unknown", SprintQ{Kind: "nope"}, Cost{Records: MaxReadRecords, RangeIDs: MaxReadRangeIDs}},
	} {
		got := QueryCost(tt.q)
		if got.Records != tt.want.Records || got.RangeIDs != tt.want.RangeIDs {
			t.Errorf("%s: cost %+v, want records %d and range ids %d", tt.name, got, tt.want.Records, tt.want.RangeIDs)
		}
	}

	// Every follow adds one record, but rcards and needs.
	for _, f := range Follows {
		want := 1 + 1
		switch f {
		case FollowRCards:
			want = 1 + 15
		case FollowNeeds:
			want = 1 + 64
		}
		if got := QueryCost(SprintQ{Kind: QueryRelated, Source: list(1), Follow: []string{f}}).Records; got != want {
			t.Errorf("follow %s: %d records an id, want %d", f, got, want)
		}
	}
	if len(Follows) != 10 {
		t.Errorf("1.0 names ten follows, the table has %d", len(Follows))
	}

	// Bytes: records with their projection, and range ids.
	c := QueryCost(SprintQ{Kind: QueryRelated, Source: IDSource{Kind: SourceHead, Limit: 10}, Fields: fieldsOf(3)})
	if want := 10*(RecordEnvelopeBytes+3*FieldBytes) + 10*RangeIDBytes; c.Bytes != want {
		t.Errorf("bytes of a projected read: %d, want %d", c.Bytes, want)
	}
	c = QueryCost(SprintQ{Kind: QueryFleet, Units: 4})
	if want := 4 * WholeRecordBytes; c.Bytes != want {
		t.Errorf("bytes of a whole-record read: %d, want %d", c.Bytes, want)
	}
	if got := QueryCost(SprintQ{Kind: "nope"}); got.Bytes != MaxReadBytes {
		t.Errorf("an unknown kind's bytes: %d", got.Bytes)
	}
}

func TestQueryCostIsOneRowAKind(t *testing.T) {
	t.Parallel()
	kinds := []string{QueryRelated, QueryFront, QueryWaiters, QueryStreams, QueryFleet, QueryReaders, QueryNeedchain, QueryJnote}
	if len(queryCosts) != len(kinds) {
		t.Fatalf("%d rows for the %d composite queries of 1.0", len(queryCosts), len(kinds))
	}
	for _, k := range kinds {
		if queryCosts[k] == nil {
			t.Errorf("no row for %s", k)
		}
	}
}

func TestReadPlanCostIsTheSumOfItsQueries(t *testing.T) {
	t.Parallel()
	rp := ReadPlan{
		IDs:     map[string][]string{Work: make([]string, 5), Fleet: make([]string, 2), Merge: nil},
		Ranges:  []RangeQ{{Table: Work, Cell: "s:ready", Limit: 100, Records: true, Fields: fieldsOf(2)}, {Key: "agenda", Limit: 50}, {Table: Work, Cell: "s:review"}},
		Counts:  []CountQ{{Table: Work, Cells: []string{"s:ready", "s:review", "s:working"}}},
		RCounts: []RCountQ{OpenBeforeQ("s", 3)},
		Lines:   []LinesQ{{After: 4, Through: 5, Limit: 1}, {After: 0, Limit: 0}},
		Sprint:  []SprintQ{{Kind: QueryFleet, Units: 6}},
	}
	got := rp.Cost()
	want := Cost{
		Records:  5 + 2 + 100 + 6, // ids, the range with records, the fleet
		RangeIDs: 100 + 50 + MaxRangeLimit,
	}
	want.Bytes = 7*WholeRecordBytes +
		100*RangeIDBytes + 100*(RecordEnvelopeBytes+2*FieldBytes) + 50*RangeIDBytes + MaxRangeLimit*RangeIDBytes +
		3*CountBytes + 6*CountBytes +
		2*MaxLineBytes +
		6*WholeRecordBytes
	if got != want {
		t.Fatalf("cost %+v, want %+v", got, want)
	}
	// One ids query for each table that names an id, and one for each other.
	if got, want := rp.Queries(), 2+3+1+1+2+1; got != want {
		t.Fatalf("queries %d, want %d", got, want)
	}
	if (ReadPlan{}).Cost() != (Cost{}) || (ReadPlan{}).Queries() != 0 {
		t.Fatal("an empty plan costs something")
	}
}

func TestTsetSlotsAreInTheOrderOfTheErrata(t *testing.T) {
	t.Parallel()
	// E3: Tset is aligned with the plan's Layer 1 and Layer 2 queries: the ids
	// of each table that names one (by table name), then ranges, counts,
	// rcounts and lines, each in the plan's order. The sprint's queries are
	// answered apart.
	rp := ReadPlan{
		IDs:     map[string][]string{Work: {"a", "b"}, Fleet: {"m"}, Merge: nil, Readers: {"r", "s", "t"}},
		Ranges:  []RangeQ{{Table: Work, Cell: "s:ready"}, {Key: "agenda"}},
		Counts:  []CountQ{{Table: Work, Cells: []string{"s:ready"}}},
		RCounts: []RCountQ{OpenBeforeQ("s", 1), OpenBeforeQ("s", 2)},
		Lines:   []LinesQ{{After: 4, Through: 5, Limit: 1}},
		Sprint:  []SprintQ{{Kind: QueryFleet}},
	}
	want := []TsetSlot{
		{Kind: "ids", Table: "fleet"}, {Kind: "ids", Table: "readers"}, {Kind: "ids", Table: "work"},
		{Kind: "range", Index: 0}, {Kind: "range", Index: 1},
		{Kind: "count", Index: 0},
		{Kind: "rcount", Index: 0}, {Kind: "rcount", Index: 1},
		{Kind: "lines", Index: 0},
	}
	if got := rp.TsetSlots(); !reflect.DeepEqual(got, want) {
		t.Fatalf("slots:\n got %+v\nwant %+v", got, want)
	}
	if got := len(rp.TsetSlots()) + len(rp.Sprint); got != rp.Queries() {
		t.Fatalf("%d slots and composite queries, the plan sends %d queries", got, rp.Queries())
	}
	if got := (ReadPlan{}).TsetSlots(); len(got) != 0 {
		t.Fatalf("an empty plan has slots: %+v", got)
	}
	if got := (ReadPlan{IDs: map[string][]string{Work: nil}}).TsetSlots(); len(got) != 0 {
		t.Fatalf("a table of no ids has a slot: %+v", got)
	}
}

// countPlan is a plan of ids by table and one count query for each cell.
func countPlan(work, readers, fleet, counts int) ReadPlan {
	rp := ReadPlan{IDs: map[string][]string{}}
	for table, n := range map[string]int{Work: work, Readers: readers, Fleet: fleet} {
		for i := 0; i < n; i++ {
			rp.IDs[table] = append(rp.IDs[table], fmt.Sprintf("%s%d", table[:1], i))
		}
	}
	for i := 0; i < counts; i++ {
		rp.Counts = append(rp.Counts, CountQ{Table: Work, Cells: []string{fmt.Sprintf("s%d:ready", i)}})
	}
	return rp
}

func TestReadPlanSplitWithinBounds(t *testing.T) {
	t.Parallel()
	// A plan of 30,000 records and 1,100 counts, cut to reads of at most 10,000
	// records and 1,024 queries.
	rp := countPlan(10000, 10000, 10000, 1100)
	b := ReadBounds{Queries: MaxReadQueries, Records: MaxReadRecords}
	if rp.Cost().Records != 30000 || rp.Queries() != 1103 {
		t.Fatalf("the plan: %+v, %d queries", rp.Cost(), rp.Queries())
	}
	plans := rp.Split(b)
	if len(plans) != 4 {
		t.Fatalf("%d plans, want 4: the three tables of 10,000 and the counts beyond the 1,024 queries", len(plans))
	}
	for i, p := range plans {
		if c := p.Cost(); c.Records > b.Records || p.Queries() > b.Queries {
			t.Errorf("plan %d: %d records and %d queries", i, c.Records, p.Queries())
		}
	}
	if plans[0].Queries() != 1 || plans[3].Queries() != 1100-1023 {
		t.Errorf("plan queries: %d first, %d last", plans[0].Queries(), plans[3].Queries())
	}
	checkConserved(t, rp, plans)

	// The same by all of layer 1's bounds: bytes cut it further, never over.
	full := L1ReadBounds()
	plans = rp.Split(full)
	if len(plans) < 4 {
		t.Fatalf("%d plans by layer 1's bounds", len(plans))
	}
	for i, p := range plans {
		if !within(p.Queries(), p.Cost(), full) {
			t.Errorf("plan %d: %+v, %d queries", i, p.Cost(), p.Queries())
		}
	}
	checkConserved(t, rp, plans)

	// A plan that fits is returned alone, and an empty plan gives none.
	small := countPlan(3, 0, 1, 2)
	if got := small.Split(full); len(got) != 1 || !reflect.DeepEqual(got[0], small) {
		t.Fatalf("a plan that fits: %+v", got)
	}
	if got := (ReadPlan{}).Split(full); got != nil {
		t.Fatalf("an empty plan: %+v", got)
	}
	if got := (ReadPlan{IDs: map[string][]string{Work: nil}}).Split(full); got != nil {
		t.Fatalf("a plan of an empty ids list: %+v", got)
	}
	// A bound of 0 is no bound.
	if got := rp.Split(ReadBounds{}); len(got) != 1 {
		t.Fatalf("no bounds: %d plans", len(got))
	}
}

// atoms flattens plans into the queries' parts, grouped by what they are (the
// ids of a table, or a kind of query) and in the plans' order: an id of a table,
// an id of a splittable composite query (named by its stream label), or a whole
// query.
func atoms(plans ...ReadPlan) map[string][]string {
	out := map[string][]string{}
	for _, p := range plans {
		for _, table := range []string{Fleet, Merge, Readers, Work} {
			out["id "+table] = append(out["id "+table], p.IDs[table]...)
		}
		for _, q := range p.Ranges {
			out["range"] = append(out["range"], fmt.Sprintf("%+v", q))
		}
		for _, q := range p.Counts {
			out["count"] = append(out["count"], fmt.Sprintf("%+v", q))
		}
		for _, q := range p.RCounts {
			out["rcount"] = append(out["rcount"], fmt.Sprintf("%+v", q))
		}
		for _, q := range p.Lines {
			out["lines"] = append(out["lines"], fmt.Sprintf("%+v", q))
		}
		for _, q := range p.Sprint {
			if splittable(q) {
				for _, id := range q.Source.IDs {
					out["sprint"] = append(out["sprint"], q.Stream+" "+id)
				}
				continue
			}
			out["sprint"] = append(out["sprint"], fmt.Sprintf("%+v", q))
		}
	}
	for k, v := range out {
		if len(v) == 0 {
			delete(out, k)
		}
	}
	return out
}

// checkConserved fails unless the plans together ask what the plan asked, in
// the order of its queries.
func checkConserved(t *testing.T, rp ReadPlan, plans []ReadPlan) {
	t.Helper()
	if got, want := atoms(plans...), atoms(rp); !reflect.DeepEqual(got, want) {
		for k := range want {
			if !reflect.DeepEqual(got[k], want[k]) {
				t.Fatalf("the plans do not ask what the plan asked of %q: %d parts, want %d", k, len(got[k]), len(want[k]))
			}
		}
		t.Fatalf("the plans ask what the plan did not: %v", got)
	}
}

func TestSplitOversizedPiecesGoAlone(t *testing.T) {
	t.Parallel()
	big := SprintQ{Kind: QueryFleet, Units: 500}
	rp := ReadPlan{
		Ranges: []RangeQ{{Table: Work, Cell: "s:ready", Limit: 10}},
		Sprint: []SprintQ{big, {Kind: QueryFleet, Units: 10}},
	}
	plans := rp.Split(ReadBounds{Records: 100})
	if len(plans) != 3 {
		t.Fatalf("%d plans", len(plans))
	}
	if !reflect.DeepEqual(plans[1].Sprint, []SprintQ{big}) || plans[1].Queries() != 1 {
		t.Fatalf("the query over the bound is not alone: %+v", plans[1])
	}
	if within(plans[1].Queries(), plans[1].Cost(), ReadBounds{Records: 100}) {
		t.Fatal("it was cut to fit, and it cannot be cut")
	}

	// A list of ids of which one is over a bound goes one id a plan.
	ids := ReadPlan{Sprint: []SprintQ{{Kind: QueryRelated, Source: IDSource{Kind: SourceIDs, IDs: []string{"a", "b", "c"}}, Follow: []string{FollowNeeds}}}}
	plans = ids.Split(ReadBounds{Records: 10})
	if len(plans) != 3 {
		t.Fatalf("%d plans of one id each", len(plans))
	}
	for i, p := range plans {
		if got := p.Sprint[0].Source.IDs; !reflect.DeepEqual(got, []string{"a", "b", "c"}[i:i+1]) {
			t.Errorf("plan %d: %v", i, got)
		}
	}
}

func TestSplitConservesEveryQueryAndFitsOrIsAlone(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(20260929, 7))
	for round := 0; round < 80; round++ {
		rp := ReadPlan{IDs: map[string][]string{}}
		for _, table := range []string{Work, Readers, Merge, Fleet} {
			for i := rng.IntN(300); i > 0; i-- {
				rp.IDs[table] = append(rp.IDs[table], fmt.Sprintf("%s%d", table[:1], i))
			}
		}
		for i := rng.IntN(40); i > 0; i-- {
			rp.Ranges = append(rp.Ranges, RangeQ{Table: Work, Cell: fmt.Sprintf("s%d:ready", i), Limit: 1 + rng.IntN(400), Records: rng.IntN(2) == 0})
		}
		for i := rng.IntN(40); i > 0; i-- {
			rp.Counts = append(rp.Counts, CountQ{Table: Work, Cells: fieldsOf(1 + rng.IntN(5))})
		}
		for i := rng.IntN(5); i > 0; i-- {
			rp.RCounts = append(rp.RCounts, OpenBeforeQ(fmt.Sprintf("s%d", i), float64(i)))
		}
		for i := rng.IntN(3); i > 0; i-- {
			rp.Lines = append(rp.Lines, LinesQ{After: uint64(i), Through: uint64(i) + 1, Limit: 1 + rng.IntN(2)})
		}
		kinds := []string{QueryRelated, QueryWaiters, QueryJnote, QueryNeedchain, QueryFleet, QueryFront}
		for i := rng.IntN(8); i > 0; i-- {
			q := SprintQ{Kind: kinds[rng.IntN(len(kinds))], Stream: fmt.Sprintf("q%d", i), Limit: rng.IntN(20), Units: rng.IntN(20), Subjects: 1 + rng.IntN(5)}
			q.Source = IDSource{Kind: SourceIDs, IDs: fieldsOf(rng.IntN(200))}
			if rng.IntN(5) == 0 {
				q.Source = IDSource{Kind: SourceHead, Key: "elig:s", Limit: 1 + rng.IntN(300)}
			}
			if rng.IntN(2) == 0 {
				q.Follow = []string{Follows[rng.IntN(len(Follows))]}
			}
			q.Heads = []HeadQ{{Index: HeadAgain, Limit: rng.IntN(30)}}
			rp.Sprint = append(rp.Sprint, q)
		}
		b := ReadBounds{Queries: 1 + rng.IntN(60), Records: 50 + rng.IntN(2000), RangeIDs: 100 + rng.IntN(3000), Bytes: (1 + rng.IntN(600)) << 10}
		plans := rp.Split(b)
		checkConserved(t, rp, plans)
		if again := rp.Split(b); !reflect.DeepEqual(again, plans) {
			t.Fatalf("round %d: Split is not deterministic", round)
		}
		for i, p := range plans {
			if p.empty() {
				t.Fatalf("round %d: plan %d is empty", round, i)
			}
			if within(p.Queries(), p.Cost(), b) {
				continue
			}
			// Over a bound only as one query that cannot be cut, or one id.
			if p.Queries() != 1 {
				t.Fatalf("round %d: plan %d is over %+v with %d queries: %+v", round, i, b, p.Queries(), p.Cost())
			}
			for _, ids := range p.IDs {
				if len(ids) != 1 {
					t.Fatalf("round %d: plan %d is over a bound with %d ids of one table", round, i, len(ids))
				}
			}
			for _, q := range p.Sprint {
				if splittable(q) && len(q.Source.IDs) != 1 {
					t.Fatalf("round %d: plan %d is over a bound with %d ids of a query that could be cut", round, i, len(q.Source.IDs))
				}
			}
		}
	}
}

// dealReadPlan is the read of the deal rule (R6, 2.3): the fleet, and for each
// stream front(s) with the head of fresh below sigma and the head of again,
// each up to L = min(room, 64, 10,000 / 3s), with the withdrawn card of each
// again head.
func dealReadPlan(streams, members, room int) (ReadPlan, int) {
	l := min(room, 64, MaxReadRecords/(3*streams))
	return dealReadPlanL(streams, members, l), l
}

// dealReadPlanL is the same read at a limit of l a head.
func dealReadPlanL(streams, members, l int) ReadPlan {
	var rp ReadPlan
	if members > 0 {
		rp.Sprint = append(rp.Sprint, SprintQ{Kind: QueryFleet, Units: members, Fields: []string{"status", "ready"}})
	}
	for i := 0; i < streams; i++ {
		rp.Sprint = append(rp.Sprint, SprintQ{
			Kind: QueryFront, Stream: fmt.Sprintf("s%d", i), Fields: []string{"score", "attempt", "avoid"},
			Heads: []HeadQ{{Index: HeadFreshBelow, Limit: l}, {Index: HeadAgain, Limit: l, Follow: []string{FollowWithdrawn}}},
		})
	}
	return rp
}

func TestDealReadFits250Streams(t *testing.T) {
	t.Parallel()
	// The most streams a sprint has: L is 13, and the read costs 10,000
	// records, inside layer 1's 10,000, in one read.
	rp, l := dealReadPlan(MaxStreams, 0, 64)
	if l != 13 {
		t.Fatalf("L at 250 streams is %d", l)
	}
	c := rp.Cost()
	if c.Records != MaxStreams*(1+3*l) || c.Records > MaxReadRecords {
		t.Fatalf("the deal read at 250 streams costs %d records", c.Records)
	}
	if plans := rp.Split(L1ReadBounds()); len(plans) != 1 || plans[0].Queries() != MaxStreams {
		t.Fatalf("the deal read at 250 streams is %d reads", len(plans))
	}

	// At every number of streams, the records of the heads are at most
	// 3 s L <= 10,000, as 2.3 says; the read costs one more a stream (sigma's
	// card) and one a member.
	var over []int
	worst, worstRecords := "", 0
	for s := 1; s <= MaxStreams; s++ {
		f := MaxStreams - s
		rp, l := dealReadPlan(s, f, 64)
		heads := 3 * s * l
		if heads > MaxReadRecords {
			t.Errorf("%d streams: L = %d, the heads are %d records", s, l, heads)
		}
		got := rp.Cost().Records
		if want := f + s*(1+3*l); got != want {
			t.Errorf("%d streams: %d records, want %d", s, got, want)
		}
		if got > MaxReadRecords {
			over = append(over, s)
			if w := fmt.Sprintf("%d records at %d streams", got, s); len(over) == 1 || got > worstRecords {
				worst, worstRecords = w, got
			}
		}
	}
	t.Logf("with sigma's card and the members counted, the deal read is over %d records at %d stream counts of 250: the worst is %s", MaxReadRecords, len(over), worst)

	// L that leaves room for sigma's card of each stream and for the members,
	// L = min(room, 64, (10,000 - s - f) / 3s), keeps the read inside the bound
	// at every number of streams.
	for s := 1; s <= MaxStreams; s++ {
		f := MaxStreams - s
		l := min(64, (MaxReadRecords-s-f)/(3*s))
		if l < 1 {
			t.Fatalf("%d streams: no limit fits", s)
		}
		if got := dealReadPlanL(s, f, l).Cost().Records; got > MaxReadRecords {
			t.Errorf("%d streams at L = %d: %d records", s, l, got)
		}
	}
}

func TestReadHalvings(t *testing.T) {
	t.Parallel()
	// Each halving halves what is read, down to one.
	want := []int{2000, 1000, 500, 250, 125, 63, 32, 16, 8, 4, 2, 1, 1, 1}
	for h, w := range want {
		if got := Halved(2000, h); got != w {
			t.Errorf("Halved(2000, %d) = %d, want %d", h, got, w)
		}
	}
	for n, w := range map[int]int{0: 0, 1: 1, 2: 1, 3: 1, 5: 1, 64: 1} {
		if got := Halved(n, 100); got != w {
			t.Errorf("Halved(%d, 100) = %d, want %d", n, got, w)
		}
	}
	if got := Halved(9, 1); got != 5 {
		t.Errorf("Halved(9, 1) = %d, the larger half of an odd count", got)
	}
	if got := Halved(9, 0); got != 9 {
		t.Errorf("Halved(9, 0) = %d", got)
	}
	if got := Halved(9, -3); got != 9 {
		t.Errorf("Halved(9, -3) = %d", got)
	}
	// Never below one from a count above zero, however many times.
	for n := 1; n <= 300; n++ {
		prev := n
		for h := 0; h < 12; h++ {
			got := Halved(n, h)
			if got < 1 || got > prev {
				t.Fatalf("Halved(%d, %d) = %d after %d", n, h, got, prev)
			}
			prev = got
		}
		if prev != 1 && n < 4096 {
			t.Fatalf("Halved(%d, 11) = %d: not down to one", n, prev)
		}
	}

	// The limits of a read halve the same way: deal's L of 13 at 250 streams,
	// and the read costs fewer records at each, down to one card of each head.
	_, l := dealReadPlan(MaxStreams, 0, 64)
	prev := 1 << 30
	for h, wantL := range []int{13, 7, 4, 2, 1, 1} {
		got := Halved(l, h)
		if got != wantL {
			t.Errorf("deal's L after %d halvings: %d, want %d", h, got, wantL)
		}
		records := dealReadPlanL(MaxStreams, 0, got).Cost().Records
		if want := MaxStreams * (1 + 3*wantL); records != want {
			t.Errorf("deal's read at L = %d costs %d records, want %d", got, records, want)
		}
		if (h == 0 || wantL > 1) && records >= prev || records > prev {
			t.Errorf("after %d halvings the read costs %d records, not fewer than %d", h, records, prev)
		}
		prev = records
	}

	// A rule's keys are cut to the read that fits, then halved.
	keys := make([]AgendaKey, 40)
	perKey := func(AgendaKey) Cost { return Cost{Records: 10, RangeIDs: 5} }
	fixed := Cost{Records: 20}
	b := ReadBounds{Records: 200, RangeIDs: 1000}
	take, rest := FitKeys(keys, perKey, fixed, b, 0)
	if len(take) != 18 || len(rest) != 22 {
		t.Fatalf("keys that fit: %d taken, %d left", len(take), len(rest))
	}
	for h, w := range []int{18, 9, 5, 3, 2, 1, 1} {
		take, rest = FitKeys(keys, perKey, fixed, b, h)
		if len(take) != w || len(rest) != 40-w {
			t.Errorf("after %d halvings: %d taken and %d left, want %d", h, len(take), len(rest), w)
		}
	}
	// A bound on range ids binds as well.
	if take, _ = FitKeys(keys, perKey, fixed, ReadBounds{Records: 1000, RangeIDs: 50}, 0); len(take) != 10 {
		t.Errorf("keys under a range ids bound: %d", len(take))
	}
	// Keys go in order and none is skipped for a smaller one behind it.
	sized := make([]AgendaKey, 5)
	i := 0
	varied := func(AgendaKey) Cost {
		i++
		return Cost{Records: []int{4, 4, 50, 1, 1}[i-1]}
	}
	if take, _ = FitKeys(sized, varied, Cost{}, ReadBounds{Records: 20}, 0); len(take) != 2 {
		t.Errorf("keys with a large one third: %d taken", len(take))
	}
	// At least one key, however large; none when there is none.
	huge := func(AgendaKey) Cost { return Cost{Records: 1 << 20} }
	if take, rest = FitKeys(keys, huge, fixed, b, 0); len(take) != 1 || len(rest) != 39 {
		t.Errorf("a key over the bound alone: %d taken, %d left", len(take), len(rest))
	}
	if take, rest = FitKeys(nil, huge, fixed, b, 3); len(take) != 0 || len(rest) != 0 {
		t.Errorf("no keys: %d, %d", len(take), len(rest))
	}
	// What is taken is a prefix, and what is left the rest.
	if take, rest = FitKeys(keys, perKey, fixed, b, 2); len(take)+len(rest) != len(keys) {
		t.Errorf("taken and left do not make the keys")
	}
	if cap(take) != len(take) {
		t.Errorf("the taken keys share room with the ones left: cap %d for %d", cap(take), len(take))
	}
}

// splitLimit is the most a Split of a 30,000-record plan may take (IT05: 5 ms
// of Go time, measured by BenchmarkSplit30000, which runs on every tick).
const splitLimit = 5 * time.Millisecond

func BenchmarkSplit30000(b *testing.B) {
	rp := countPlan(10000, 10000, 10000, 0)
	for i := 0; i < 500; i++ {
		rp.Sprint = append(rp.Sprint, SprintQ{Kind: QueryFleet, Units: 3})
	}
	bounds := L1ReadBounds()
	b.ReportAllocs()
	for b.Loop() {
		if plans := rp.Split(bounds); len(plans) < 3 {
			b.Fatalf("%d plans", len(plans))
		}
	}
	per := b.Elapsed() / time.Duration(b.N)
	b.ReportMetric(float64(per)/float64(time.Millisecond), "ms/split")
	if per > splitLimit {
		b.Fatalf("Split of a plan of 30,000 records took %v, the limit is %v", per, splitLimit)
	}
}
