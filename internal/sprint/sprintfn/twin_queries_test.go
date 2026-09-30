package sprintfn

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

func asRelated(t *testing.T, r QueryResult) RelatedResult {
	t.Helper()
	v, ok := r.(RelatedResult)
	if !ok {
		t.Fatalf("result is %T, want a related answer", r)
	}
	return v
}

// TestRelatedFollows: `related` reads each id's record and what each follow
// reaches from it (1.0's table): the live work card (work) and the same card
// when it is withdrawn (withdrawn), the read cards of rcards, the merge card,
// the stream's control card, each need's place and record with whether the
// card is in its wait:n (needs), the control card of the member a card names,
// the count of judgments open on it (jopen), its due entries and its index
// memberships. A follow that finds nothing is empty, and a record the follow
// names that does not exist is left out, except a need, whose absence is how a
// card waits on something missing.
func TestRelatedFollows(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.note("blocked", "c1", "p1")

	run := func(table string, idList []string, follow string, fields ...string) RelatedResult {
		t.Helper()
		if fields == nil {
			fields = []string{}
		}
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: table, Source: ids(idList...), Fields: fields, Follow: []string{follow}})
		return asRelated(t, res)
	}
	byID := func(r RelatedResult) map[string]*Follows {
		out := map[string]*Follows{}
		for _, it := range r.Items {
			out[it.ID] = it.Follows
		}
		return out
	}

	t.Run("work", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"k1", "a1", "x1", "p1"}, sprint.FollowWork, "attempt"))
		if recordIDs(got["k1"].Work)[0] != "k1.w1" || recordIDs(got["a1"].Work)[0] != "a1.w2" {
			t.Fatalf("work follows %+v", got)
		}
		if len(got["x1"].Work) != 0 || len(got["p1"].Work) != 0 {
			t.Fatalf("a withdrawn card is not the live one, and a card never dealt has none: %+v", got)
		}
		// The projection is the query's: the field the follow derived from is not returned.
		res := run(sprint.Work, []string{"k1"}, sprint.FollowWork)
		if len(res.Items[0].Record.Fields) != 0 {
			t.Fatalf("the summary carries fields: %+v", res.Items[0].Record.Fields)
		}
	})
	t.Run("withdrawn", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"x1", "k1"}, sprint.FollowWithdrawn))
		if recordIDs(got["x1"].Withdrawn)[0] != "x1.w1" || len(got["k1"].Withdrawn) != 0 {
			t.Fatalf("withdrawn follows %+v", got)
		}
	})
	t.Run("rcards", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"v1", "p1"}, sprint.FollowRCards, "primary"))
		if !reflect.DeepEqual(recordIDs(got["v1"].RCards), []string{"v1.r1.r1", "v1.r1.r2"}) || len(got["p1"].RCards) != 0 {
			t.Fatalf("rcards follows %+v", got)
		}
		if fieldValue(got["v1"].RCards[0], "primary") != "v1" {
			t.Fatalf("a read card is returned with the projection: %+v", got["v1"].RCards[0])
		}
	})
	t.Run("merge", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"q1", "p1"}, sprint.FollowMerge, "ci"))
		if recordIDs(got["q1"].Merge)[0] != "q1" || fieldValue(got["q1"].Merge[0], "ci") != "green" || len(got["p1"].Merge) != 0 {
			t.Fatalf("merge follows %+v", got)
		}
	})
	t.Run("control", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"p1", "h1"}, sprint.FollowControl, "state"))
		if recordIDs(got["p1"].Control)[0] != "ctl-s1" || recordIDs(got["h1"].Control)[0] != "ctl-s2" ||
			fieldValue(got["h1"].Control[0], "state") != "stopped" {
			t.Fatalf("control follows %+v", got)
		}
	})
	t.Run("needs", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"w1"}, sprint.FollowNeeds, "open"))
		needs := got["w1"].Needs
		if len(needs) != 2 || needs[0].ID != "p1" || !needs[0].Record.Exists || needs[0].Record.Place.Col != "waiting" ||
			needs[1].ID != "ghost" || needs[1].Record.Exists {
			t.Fatalf("needs %+v", needs)
		}
		if !needs[0].InWait || !needs[1].InWait {
			t.Fatalf("w1 is in wait:p1 and wait:ghost: %+v", needs)
		}
		// A card that names a need that has landed is in no wait:n (1.3.1: n is open).
		w.cards(card{sprint.Work, "s1", "waiting", "w9", "90", fields("kind", "work", "open", "0", "needs", "d1")})
		got = byID(run(sprint.Work, []string{"w9"}, sprint.FollowNeeds))
		if len(got["w9"].Needs) != 1 || got["w9"].Needs[0].InWait || got["w9"].Needs[0].Record.Place.Col != "landed" {
			t.Fatalf("w9 is in no wait:d1: %+v", got["w9"].Needs)
		}
	})
	t.Run("member", func(t *testing.T) {
		got := byID(run(sprint.Fleet, []string{"k1.w1", "x1.w1"}, sprint.FollowMember, "status"))
		if recordIDs(got["k1.w1"].Member)[0] != "ctl-m1" || fieldValue(got["k1.w1"].Member[0], "status") != "up" ||
			recordIDs(got["x1.w1"].Member)[0] != "ctl-m2" {
			t.Fatalf("member follows %+v", got)
		}
	})
	t.Run("jopen", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"p1", "p2"}, sprint.FollowJOpen))
		if got["p1"].JOpen == nil || got["p1"].JOpen.Count != 1 || got["p2"].JOpen == nil || got["p2"].JOpen.Count != 0 {
			t.Fatalf("jopen follows %+v %+v", got["p1"].JOpen, got["p2"].JOpen)
		}
	})
	t.Run("due", func(t *testing.T) {
		got := byID(run(sprint.Fleet, []string{"k1.w1", "a1.w2", "x1.w1"}, sprint.FollowDue))
		if !reflect.DeepEqual(got["k1.w1"].Due, []DueEntry{{"unfinished:k1.w1", "9000"}}) ||
			!reflect.DeepEqual(got["a1.w2"].Due, []DueEntry{{"untaken:a1.w2", "8000"}}) || len(got["x1.w1"].Due) != 0 {
			t.Fatalf("due follows %+v", got)
		}
		got = byID(run(sprint.Merge, []string{"ctl-s1", "q1"}, sprint.FollowDue))
		if !reflect.DeepEqual(got["ctl-s1"].Due, []DueEntry{{"mergeidle:s1", "7000"}}) || len(got["q1"].Due) != 0 {
			t.Fatalf("a stream's entry is named by its row: %+v", got)
		}
		got = byID(run(sprint.Readers, []string{"v1.r1.r1", "v1.r1.r2"}, sprint.FollowDue))
		if got["v1.r1.r1"].Due[0].Key != "unbegun:v1.r1.r1" || got["v1.r1.r2"].Due[0].Key != "unreported:v1.r1.r2" {
			t.Fatalf("read cards' due entries %+v", got)
		}
	})
	t.Run("index", func(t *testing.T) {
		got := byID(run(sprint.Work, []string{"p1", "g1", "f1", "a1", "v1", "k1"}, sprint.FollowIndex))
		want := map[string][]IndexEntry{
			"p1": {{"elig:s1", "10"}}, "g1": {{"sent:s1", "50"}}, "f1": {{"fresh:s1", "30"}},
			"a1": {{"again:s1", "35"}}, "v1": {{"askwait", "5"}},
		}
		for id, w := range want {
			if !reflect.DeepEqual(got[id].Index, w) {
				t.Errorf("%s: index memberships %+v, want %+v", id, got[id].Index, w)
			}
		}
		if len(got["k1"].Index) != 0 {
			t.Errorf("a working card is in no index: %+v", got["k1"].Index)
		}
	})
	t.Run("a record that does not exist is returned absent, as a list names it", func(t *testing.T) {
		res := run(sprint.Work, []string{"nobody"}, sprint.FollowWork)
		if res.Items[0].Record.Exists || len(res.IDs) != 1 {
			t.Fatalf("%+v", res.Items[0])
		}
	})
	t.Run("no follow, no follows object", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p1"), Fields: []string{"open"}})
		r := asRelated(t, res)
		if r.Items[0].Follows != nil || fieldValue(r.Items[0].Record, "open") != "0" {
			t.Fatalf("%+v", r.Items[0])
		}
	})
}

// TestRelatedSources: an id source is a list, the head of an index or of a
// cell with a limit, or a line by seq with its ids or its `about`, from an
// offset (1.0). An id taken from an index or a line must have a record at the
// epoch (MISSING, MEMBEREPOCH otherwise): it is a lower layer's refusal of a
// card (1.3.5).
func TestRelatedSources(t *testing.T) {
	t.Parallel()
	w := standard(t)
	fieldsOpen := []string{"open"}

	t.Run("the head of an index", func(t *testing.T) {
		res, c := w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s1", 2), Fields: fieldsOpen})
		r := asRelated(t, res)
		if !reflect.DeepEqual(r.IDs, []string{"p1", "p2"}) || c.RangeIDs != 2 || c.Records != 2 {
			t.Fatalf("ids %v, charged %+v", r.IDs, c)
		}
	})
	t.Run("the head of a cell", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("s1:ready", 2), Fields: fieldsOpen})
		if ids := asRelated(t, res).IDs; !reflect.DeepEqual(ids, []string{"f1", "a1"}) {
			t.Fatalf("ids %v", ids)
		}
	})
	t.Run("a line by seq, its ids from an offset and its about", func(t *testing.T) {
		w.cards(card{sprint.Work, "s2", "waiting", "l1", "11", fields("kind", "work", "open", "0")},
			card{sprint.Work, "s2", "waiting", "l2", "12", fields("kind", "work", "open", "0")},
			card{sprint.Work, "s2", "waiting", "l3", "13", fields("kind", "work", "open", "0")})
		last := len(w.log.Lines(testPrefix, "0"))
		src := sprint.IDSource{Kind: sprint.SourceLine, Seq: uint64(last), Offset: 1}
		res, c := w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: src, Fields: fieldsOpen})
		r := asRelated(t, res)
		if !reflect.DeepEqual(r.IDs, []string{"l2", "l3"}) || c.Lines != 1 {
			t.Fatalf("ids %v, lines %d", r.IDs, c.Lines)
		}
		src.Limit = 1
		res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: src, Fields: fieldsOpen})
		if ids := asRelated(t, res).IDs; !reflect.DeepEqual(ids, []string{"l2"}) {
			t.Fatalf("a window of one: %v", ids)
		}
		src = sprint.IDSource{Kind: sprint.SourceLine, Seq: uint64(last), About: true}
		res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: src, Fields: fieldsOpen})
		if ids := asRelated(t, res).IDs; !reflect.DeepEqual(ids, []string{"l1", "l2", "l3"}) {
			t.Fatalf("a line's about: %v", ids)
		}
		// A line the log does not have is DRIFT.
		ref := w.refused(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Fields: fieldsOpen,
			Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: uint64(last + 50)}})
		if ref.Code != codeDrift {
			t.Fatalf("a line that is not there: %v", ref)
		}
	})
	t.Run("an id taken from an index that has no record", func(t *testing.T) {
		w.seed(w.zadd("elig:s9", "1", "phantom"))
		ref := w.refused(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s9", 5), Fields: fieldsOpen})
		if ref.Code != codeMissing || !reflect.DeepEqual(ref.Detail.IDs, []string{"phantom"}) || ref.Detail.Table != sprint.Work {
			t.Fatalf("%+v", ref)
		}
	})
	t.Run("the missing index names ids that have no record, and that is not refused", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("missing", 5), Fields: fieldsOpen})
		r := asRelated(t, res)
		if !reflect.DeepEqual(r.IDs, []string{"ghost"}) || r.Items[0].Record.Exists {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("a record of another epoch", func(t *testing.T) {
		// A record the table holds at another epoch cannot be read at this one: the
		// twin's Mem keeps each epoch apart, so the id has no record here.
		w.seed(w.zadd("elig:s8", "1", "ld0"))
		ref := w.refused(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s8", 5), Fields: fieldsOpen})
		if ref.Code != codeMissing {
			t.Fatalf("%+v", ref)
		}
	})
	t.Run("a table the read does not know", func(t *testing.T) {
		ref := w.refused(sprint.SprintQ{Kind: sprint.QueryRelated, Table: "nosuch", Source: ids("p1"), Fields: fieldsOpen})
		if ref.Code != "NOTABLE" {
			t.Fatalf("%+v", ref)
		}
	})
}

// TestFrontOneSnapshot: front(s) returns G and sigma (the first of sent:s),
// n_before (the count of s's five open cells below sigma), G's record and the
// heads of elig:s and fresh:s below sigma, of fresh:s above sigma and of
// again:s, each up to its limit with its records and follows (1.0), all from
// one snapshot (DECISIONS 58). Two writers flip the world between two states
// while readers ask front: every answer is exactly one of the two states'
// answers, never a mix of the tables of one and the indexes of the other.
func TestFrontOneSnapshot(t *testing.T) {
	t.Parallel()
	w := standard(t)
	front := sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"attempt"}, Heads: []sprint.HeadQ{
		{Index: sprint.HeadEligBelow, Limit: 10}, {Index: sprint.HeadFreshBelow, Limit: 10},
		{Index: sprint.HeadFreshAbove, Limit: 10}, {Index: sprint.HeadAgain, Limit: 10, Follow: []string{sprint.FollowWork}}}}

	res, c := w.query(front)
	f := res.(FrontResult)
	if f.G != "g1" || f.Sigma != "50" || f.GQuarantined || f.GRecord == nil || f.GRecord.ID != "g1" {
		t.Fatalf("G and sigma: %+v", f)
	}
	// Below sigma = 50 in s1's waiting, ready, working, review and merging cells:
	// p1 10, p2 20 (waiting), f1 30, a1 35 (ready), k1 5, v1 8, q1 9: seven cards.
	if f.NBefore != 7 {
		t.Fatalf("n_before %d, want 7", f.NBefore)
	}
	heads := map[string][]string{}
	for _, h := range f.Heads {
		heads[h.Index] = h.IDs
		if h.Index == sprint.HeadAgain && len(h.Items) != 1 || len(h.Items) != len(h.IDs) {
			t.Fatalf("head %s: ids %v, items %d", h.Index, h.IDs, len(h.Items))
		}
	}
	want := map[string][]string{
		sprint.HeadEligBelow: {"p1", "p2"}, sprint.HeadFreshBelow: {"f1"},
		sprint.HeadFreshAbove: {"f2"}, sprint.HeadAgain: {"a1"},
	}
	if !reflect.DeepEqual(heads, want) {
		t.Fatalf("heads %v, want %v", heads, want)
	}
	if again := f.Heads[3].Items[0]; again.Follows == nil || len(again.Follows.Work) != 1 || again.Follows.Work[0].ID != "a1.w2" {
		t.Fatalf("again's follow: %+v", again.Follows)
	}
	if c.RangeIDs != 1+2+1+1+1 {
		t.Fatalf("range ids %d: sigma's head and the heads' ids", c.RangeIDs)
	}

	// A stream with no sentinel: sigma is nothing, all of elig and fresh are below
	// it, and nothing is above.
	res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s9", Fields: []string{}, Heads: front.Heads})
	if g := res.(FrontResult); g.G != "" || g.Sigma != "" || g.NBefore != 0 || g.GRecord != nil {
		t.Fatalf("no sentinel: %+v", g)
	}

	// Atomicity. World A is the standard one; world B moves g1 out of the way
	// (landed), so that sigma, n_before and the heads all change together.
	// A card's revision counts its moves, so the two states are told apart without it.
	revision := regexp.MustCompile(`"revision":"[0-9]+"`)
	answer := func() string {
		res, _, err := w.tw.QueryFull(front)
		if err != nil {
			t.Error(err)
			return ""
		}
		return revision.ReplaceAllString(mustJSON(t, res), `"revision":"-"`)
	}
	a := answer()
	w.move(sprint.Work, "s1:waiting", "s1:landed", "g1")
	b := answer()
	if a == b {
		t.Fatal("the two worlds answer alike; the test proves nothing")
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			from, to := "s1:landed", "s1:waiting"
			if i%2 == 1 {
				from, to = to, from
			}
			req := &Request{Epoch: "0", Meta: Meta{Verb: "test"}, Body: Body{Entries: []tset.Entry{{Kind: "move", Table: sprint.Work, From: from, To: to, IDs: []string{"g1"}, About: []string{"g1"}}}}}
			if _, err := Step(context.Background(), w.tw, req); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	seen := map[string]int{}
	for i := 0; i < 300; i++ {
		seen[answer()]++
	}
	close(stop)
	wg.Wait()
	for got := range seen {
		if got != a && got != b {
			t.Fatalf("an answer that is neither state's:\n%s\nA %s\nB %s", got, a, b)
		}
	}
}

// TestWaitersFromLineSeq: `waiters` takes its ids from a line by seq, its ids
// or its `about`, from an offset, as R4's `made@<seq>` does (1.3.1, 2.3): each
// id's place, its score in {p}missing@e and the head of wait:n up to the limit
// with the waiters' records.
func TestWaitersFromLineSeq(t *testing.T) {
	t.Parallel()
	w := standard(t)
	// A line that created two cards, one of them a need that waiters named while it
	// had no record.
	w.cards(card{sprint.Work, "s2", "waiting", "ghost", "20", fields("kind", "work", "open", "0")},
		card{sprint.Work, "s2", "waiting", "n2", "21", fields("kind", "work", "open", "0")})
	seq := uint64(len(w.log.Lines(testPrefix, "0")))
	w.seed(w.zadd("wait:n2", "0", "w2"))

	// The line's window is the two ids it names: a line is planned for the most a
	// line may hold (2,000 ids) unless its window is cut.
	q := sprint.SprintQ{Kind: sprint.QueryWaiters, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: seq, Limit: 2}, Limit: 1, Fields: []string{"open"}}
	res, c := w.query(q)
	r := res.(WaitersResult)
	if !reflect.DeepEqual(r.IDs, []string{"ghost", "n2"}) || c.Lines != 1 {
		t.Fatalf("ids %v, lines %d", r.IDs, c.Lines)
	}
	ghost, n2 := r.Items[0], r.Items[1]
	if ghost.Missing == nil || *ghost.Missing != "100" || n2.Missing != nil {
		t.Fatalf("missing scores: ghost %v, n2 %v", ghost.Missing, n2.Missing)
	}
	if !ghost.Record.Exists || ghost.Record.Place.Col != "waiting" {
		t.Fatalf("ghost's place: %+v", ghost.Record)
	}
	// A limit of one: the head of wait:ghost is w1 and there are more.
	if !reflect.DeepEqual(ghost.Wait.IDs, []string{"w1"}) || !ghost.Wait.HasMore || fieldValue(ghost.Wait.Items[0].Record, "open") != "2" {
		t.Fatalf("wait:ghost %+v", ghost.Wait)
	}
	if !reflect.DeepEqual(n2.Wait.IDs, []string{"w2"}) || n2.Wait.HasMore {
		t.Fatalf("wait:n2 %+v", n2.Wait)
	}
	q.Limit = 5
	res, _ = w.query(q)
	if got := res.(WaitersResult).Items[0].Wait.IDs; !reflect.DeepEqual(got, []string{"w1", "w2"}) {
		t.Fatalf("wait:ghost at limit 5: %v", got)
	}

	// Ids named in a list, one with no record: its place is absent and its waiters
	// are still read.
	res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("nothing", "p1"), Limit: 2, Fields: []string{}})
	r = res.(WaitersResult)
	if r.Items[0].Record.Exists || len(r.Items[0].Wait.IDs) != 0 || !reflect.DeepEqual(r.Items[1].Wait.IDs, []string{"w1"}) {
		t.Fatalf("%+v", r)
	}
	// The head of the missing index as the source.
	res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryWaiters, Source: head("missing", 10), Limit: 5, Fields: []string{}})
	if got := res.(WaitersResult).IDs; !reflect.DeepEqual(got, []string{"ghost"}) {
		t.Fatalf("missing's head: %v", got)
	}
}

// TestStreamsStuckBounded: `streams` reads every stream of the work table
// (up to its units), its control card, and for a stream stopped on a cross
// need that need card's record and the first `limit` ids of its stuck cell
// (range ids, no records): the ids returned never exceed the limit, and
// HasMore says the cell had more (1.0, R5).
func TestStreamsStuckBounded(t *testing.T) {
	t.Parallel()
	w := standard(t)
	q := sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 2}
	res, c := w.query(q)
	r := res.(StreamsResult)
	if !reflect.DeepEqual(r.Rows, []string{"s1", "s2"}) || r.HasMore || len(r.Items) != 2 {
		t.Fatalf("rows %v, more %v", r.Rows, r.HasMore)
	}
	s1, s2 := r.Items[0], r.Items[1]
	if s1.Control == nil || fieldValue(*s1.Control, "state") != "merging" || s1.Need != nil || s1.Stuck != nil {
		t.Fatalf("s1 is merging: %+v", s1)
	}
	if s2.Control == nil || s2.Need == nil || s2.Need.ID != "p2" || !s2.Need.Exists || s2.Need.Place.Col != "waiting" {
		t.Fatalf("s2 is stopped on p2: %+v", s2)
	}
	if !reflect.DeepEqual(s2.Stuck.IDs, []string{"st1", "st2"}) || !s2.Stuck.HasMore {
		t.Fatalf("the stuck ids are cut at the limit: %+v", s2.Stuck)
	}
	// Two rows and the two stuck ids are what the read returned as range ids; the
	// rows are read apart (RowIDs).
	if c.RangeIDs != 2+2 || c.RowIDs != 2 || c.Records != 2+1 {
		t.Fatalf("charged %+v", c)
	}

	q.Limit = 10
	res, _ = w.query(q)
	if st := res.(StreamsResult).Items[1].Stuck; !reflect.DeepEqual(st.IDs, []string{"st1", "st2", "st3"}) || st.HasMore {
		t.Fatalf("a limit past the cell: %+v", st)
	}
	// A limit of 0 reads no stuck id and still says the stream is stopped on p2.
	q.Limit = 0
	res, c = w.query(q)
	if st := res.(StreamsResult).Items[1].Stuck; st == nil || len(st.IDs) != 0 {
		t.Fatalf("limit 0: %+v", st)
	}
	if c.RangeIDs != 2 {
		t.Fatalf("limit 0 read %d range ids", c.RangeIDs)
	}
	// Units cut the rows: one stream, and HasMore.
	q.Units = 1
	res, _ = w.query(q)
	r = res.(StreamsResult)
	if !reflect.DeepEqual(r.Rows, []string{"s1"}) || !r.HasMore || len(r.Items) != 1 {
		t.Fatalf("units 1: %+v", r)
	}
	// The projection is the stuck listing's too: a listing that says more is not the table's rows.
	if a := r.Project(q); !a.HasMore || !reflect.DeepEqual(a.Rows, []string{"s1"}) {
		t.Fatalf("projection %+v", a)
	}
}

// TestNeedchainCut: `needchain` walks the needs reachable from the ids through
// open waiting cards, each card once, in the order reached, up to `max`
// records; `cut` says the limit was reached with cards still to read (1.0).
func TestNeedchainCut(t *testing.T) {
	t.Parallel()
	w := newQWorld(t)
	w.rows(sprint.Work, "s1")
	var cs []card
	// A chain c1 -> c2 -> ... -> c6 of waiting cards, each needing the next; the
	// last lands. A second start, d1, needs c3 and itself (a cycle the walk must
	// survive).
	for i := 1; i <= 5; i++ {
		id := "c" + strconv.Itoa(i)
		cs = append(cs, card{sprint.Work, "s1", "waiting", id, strconv.Itoa(i), fields("kind", "work", "open", "1", "needs", "c"+strconv.Itoa(i+1))})
	}
	cs = append(cs, card{sprint.Work, "s1", "landed", "c6", "6", fields("kind", "work")},
		card{sprint.Work, "s1", "waiting", "d1", "9", fields("kind", "work", "open", "2", "needs", "c3,d1")})
	w.cards(cs...)

	run := func(src sprint.IDSource, max int) NeedchainResult {
		t.Helper()
		res, c := w.query(sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: src, Limit: max, Fields: []string{"open"}})
		r := res.(NeedchainResult)
		if c.Records != len(r.Items) || c.Records > max {
			t.Fatalf("read %d records for %d items at max %d", c.Records, len(r.Items), max)
		}
		return r
	}
	order := func(r NeedchainResult) []string {
		out := []string{}
		for _, it := range r.Items {
			out = append(out, it.ID)
		}
		return out
	}

	r := run(ids("c1"), 100)
	if !reflect.DeepEqual(order(r), []string{"c1", "c2", "c3", "c4", "c5", "c6"}) || r.Cut {
		t.Fatalf("the whole chain: %v cut %v", order(r), r.Cut)
	}
	if !r.Items[0].Start || r.Items[1].Start || !reflect.DeepEqual(r.Items[0].Needs, []string{"c2"}) {
		t.Fatalf("items %+v", r.Items[:2])
	}
	if r.Items[5].Record.Place.Col != "landed" || len(r.Items[5].Needs) != 0 {
		t.Fatalf("a landed need is read and not walked through: %+v", r.Items[5])
	}
	// Cut at max: the limit is met with c5 and c6 still to read.
	r = run(ids("c1"), 4)
	if !reflect.DeepEqual(order(r), []string{"c1", "c2", "c3", "c4"}) || !r.Cut {
		t.Fatalf("cut at 4: %v cut %v", order(r), r.Cut)
	}
	// Exactly the chain's length: nothing is left to read, so it is not cut.
	if r = run(ids("c1"), 6); r.Cut || len(r.Items) != 6 {
		t.Fatalf("max = length: %v cut %v", order(r), r.Cut)
	}
	if r = run(ids("c1"), 5); !r.Cut {
		t.Fatalf("one short of the length is cut")
	}
	// Two starts and a cycle: each card is read once.
	r = run(ids("d1", "c4"), 100)
	if !reflect.DeepEqual(order(r), []string{"d1", "c4", "c3", "c5", "c6"}) || r.Cut {
		t.Fatalf("two starts: %v", order(r))
	}
	// A need with no record is read, absent, and not walked through.
	w.cards(card{sprint.Work, "s1", "waiting", "e1", "30", fields("kind", "work", "open", "1", "needs", "nowhere")})
	r = run(ids("e1"), 10)
	if !reflect.DeepEqual(order(r), []string{"e1", "nowhere"}) || r.Items[1].Record.Exists {
		t.Fatalf("a missing need: %+v", r.Items)
	}
	// An id list and its projection: IT05's Answer has the records of the cards read.
	a := r.Project(sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: ids("e1")})
	if len(a.Records) != 1 || a.Records[0].Card.ID != "e1" || a.IDs != nil {
		t.Fatalf("projection %+v", a)
	}
}

// TestJnoteSubjects: `jnote` reads each note from its line by seq: its type
// and cause from the line's meta, its subjects from the line's `about`, and
// for each subject the count of judgments open on it and what its jopen holds
// for this note's own type and cause (1.0, 1.3.4, R12, R13).
func TestJnoteSubjects(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.note("blocked", "c1", "p1", "p2")
	first := "n" + strconv.Itoa(len(w.log.Lines(testPrefix, "0")))
	w.note("stalled", "c2", "p2")
	second := "n" + strconv.Itoa(len(w.log.Lines(testPrefix, "0")))

	res, c := w.query(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(first, second), Fields: []string{}, Subjects: 10})
	r := res.(JnoteResult)
	if len(r.Items) != 2 {
		t.Fatalf("%+v", r)
	}
	n1, n2 := r.Items[0], r.Items[1]
	if n1.Note != first || n1.Type != "blocked" || n1.Cause != "c1" || len(n1.Subjects) != 2 {
		t.Fatalf("the first note: %+v", n1)
	}
	own := func(s SubjectOpen) string {
		if s.Own == nil {
			return "<nil>"
		}
		return *s.Own
	}
	// p1 has one judgment open (this note's), p2 two.
	if n1.Subjects[0].ID != "p1" || n1.Subjects[0].Count != 1 || own(n1.Subjects[0]) != first ||
		n1.Subjects[1].ID != "p2" || n1.Subjects[1].Count != 2 || own(n1.Subjects[1]) != first {
		t.Fatalf("the first note's subjects: %+v", n1.Subjects)
	}
	if n2.Type != "stalled" || len(n2.Subjects) != 1 || n2.Subjects[0].Count != 2 || own(n2.Subjects[0]) != second {
		t.Fatalf("the second note: %+v", n2)
	}
	// Two notes of three subjects: a line each, a quarantine probe a note, and an
	// HLEN and an HMGET a subject.
	if c.Lines != 2 || c.Probes != 2+3*2 {
		t.Fatalf("charged %+v", c)
	}
	// The open notes as the source: the head of jnotes.
	res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryJnote, Source: head("jnotes", 5), Fields: []string{}, Subjects: 10})
	if got := res.(JnoteResult).IDs; !reflect.DeepEqual(got, []string{first, second}) {
		t.Fatalf("jnotes' head: %v", got)
	}
	// A note with more subjects than the query was planned for is BUDGET, never cut.
	ref := w.refused(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(first), Fields: []string{}, Subjects: 1})
	if ref.Code != codeBudget || ref.Detail.Budget != "subjects" {
		t.Fatalf("%+v", ref)
	}
	// A note id the log does not have, one that is not a note's line, and one of
	// another epoch.
	if ref := w.refused(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids("n999"), Fields: []string{}, Subjects: 1}); ref.Code != codeDrift {
		t.Fatalf("a seq the log lacks: %+v", ref)
	}
	if ref := w.refused(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids("n1"), Fields: []string{}, Subjects: 1}); ref.Code != codeDrift {
		t.Fatalf("a line that is no note: %+v", ref)
	}
	if ref := w.refused(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids("n5~3"), Fields: []string{}, Subjects: 1}); ref.Code != CodeRequest {
		t.Fatalf("a note of another epoch: %+v", ref)
	}
	// A hold is what a subject's jopen holds for the note's own field.
	w.seed(w.hset("jopen:p1", "blocked|c1", "h"+first))
	res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(first), Fields: []string{}, Subjects: 10})
	if got := own(res.(JnoteResult).Items[0].Subjects[0]); got != "h"+first {
		t.Fatalf("a hold: %v", got)
	}
}

// TestQueriesLeaveOutQuarantined: every query leaves out the ids in
// {p}quarantine@e and says which it left out, and front(s) still takes a
// quarantined sentinel as sigma and returns it marked, without its record
// (1.0, 1.3.5).
func TestQueriesLeaveOutQuarantined(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.note("blocked", "c1", "p1", "p2")
	noteID := "n" + strconv.Itoa(len(w.log.Lines(testPrefix, "0")))
	w.seed(w.hset("quarantine", "p2", "DRIFT", "f2", "DRIFT", "g1", "DRIFT", "w1", "DRIFT", "ctl-m2", "x", "st2", "DRIFT", "c2x", "DRIFT",
		"v1.r1.r2", "DRIFT", "k1.w1", "DRIFT", "q1", "DRIFT"))
	left := func(l []string) []string { sort.Strings(l); return l }

	t.Run("related", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p1", "p2", "p3"), Fields: []string{},
			Follow: []string{sprint.FollowRCards}})
		r := asRelated(t, res)
		if !reflect.DeepEqual(r.IDs, []string{"p1", "p3"}) || !reflect.DeepEqual(r.LeftOut, []string{"p2"}) {
			t.Fatalf("ids %v, left out %v", r.IDs, r.LeftOut)
		}
		// A follow's target is left out too: v1's read card v1.r1.r2 is quarantined.
		res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("v1"), Fields: []string{},
			Follow: []string{sprint.FollowRCards, sprint.FollowWork, sprint.FollowMerge}})
		r = asRelated(t, res)
		if got := recordIDs(r.Items[0].Follows.RCards); !reflect.DeepEqual(got, []string{"v1.r1.r1"}) {
			t.Fatalf("rcards %v", got)
		}
		if !reflect.DeepEqual(left(r.LeftOut), []string{"v1.r1.r2"}) {
			t.Fatalf("left out %v", r.LeftOut)
		}
		// The head of an index: a quarantined id is out of it.
		res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s1", 10), Fields: []string{}})
		if r := asRelated(t, res); !reflect.DeepEqual(r.IDs, []string{"p1", "p3"}) {
			t.Fatalf("elig's head %v", r.IDs)
		}
	})
	t.Run("front keeps a quarantined sentinel", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"kind"}, Heads: []sprint.HeadQ{
			{Index: sprint.HeadEligBelow, Limit: 10}, {Index: sprint.HeadFreshAbove, Limit: 10}}})
		f := res.(FrontResult)
		if f.G != "g1" || f.Sigma != "50" || !f.GQuarantined || f.GRecord != nil {
			t.Fatalf("a quarantined sentinel is still sigma: %+v", f)
		}
		// Its line is still counted and the heads still cut at it; its head leaves the quarantined out.
		if f.NBefore != 7 || !reflect.DeepEqual(f.Heads[0].IDs, []string{"p1"}) || len(f.Heads[1].IDs) != 0 {
			t.Fatalf("n_before %d, heads %+v", f.NBefore, f.Heads)
		}
		if !reflect.DeepEqual(left(f.LeftOut), []string{"f2", "p2"}) {
			t.Fatalf("left out %v", f.LeftOut)
		}
		a := f.Project(sprint.SprintQ{Kind: sprint.QueryFront})
		if a.Front == nil || !a.Front.GQuarantined || a.Front.G != "g1" || a.Front.Sigma != 50 {
			t.Fatalf("the projection keeps it marked: %+v", a.Front)
		}
	})
	t.Run("waiters", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost", "p2"), Limit: 5, Fields: []string{}})
		r := res.(WaitersResult)
		if !reflect.DeepEqual(r.IDs, []string{"ghost"}) || !reflect.DeepEqual(r.LeftOut, []string{"p2"}) {
			t.Fatalf("%+v", r)
		}
		// w1 is quarantined: out of wait:ghost.
		if wt := r.Items[0].Wait; !reflect.DeepEqual(wt.IDs, []string{"w2"}) || !reflect.DeepEqual(wt.LeftOut, []string{"w1"}) {
			t.Fatalf("wait:ghost %+v", wt)
		}
	})
	t.Run("streams", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 5})
		st := res.(StreamsResult).Items[1].Stuck
		if !reflect.DeepEqual(st.IDs, []string{"st1", "st3"}) || !reflect.DeepEqual(st.LeftOut, []string{"st2"}) {
			t.Fatalf("stuck %+v", st)
		}
	})
	t.Run("needchain", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: ids("w1"), Limit: 10, Fields: []string{}})
		r := res.(NeedchainResult)
		// w1 is itself quarantined: nothing is read.
		if len(r.Items) != 0 || !reflect.DeepEqual(r.LeftOut, []string{"w1"}) {
			t.Fatalf("%+v", r)
		}
		res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: ids("w2"), Limit: 10, Fields: []string{}})
		if r := res.(NeedchainResult); len(r.Items) != 2 || r.Items[1].ID != "ghost" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("jnote", func(t *testing.T) {
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(noteID), Fields: []string{}, Subjects: 10})
		n := res.(JnoteResult).Items[0]
		if len(n.Subjects) != 1 || n.Subjects[0].ID != "p1" || n.LeftOut != 1 {
			t.Fatalf("%+v", n)
		}
	})
	t.Run("a listing's control cards are not cards of the work", func(t *testing.T) {
		// ctl-m2 is in the quarantine hash and the fleet listing still reads it: a
		// control card is read by the row it belongs to, not taken from an index.
		res, _ := w.query(sprint.SprintQ{Kind: sprint.QueryFleet, Fields: []string{"status"}})
		if l := res.(ListingResult); l.Items[1].Control == nil {
			t.Fatalf("%+v", l.Items[1])
		}
	})
}

// TestListings: `fleet` and `readers` read every member's (reader's) control
// card and the counts of its cells (1.0); IT05's Answer takes them as rows,
// counts and records.
func TestListings(t *testing.T) {
	t.Parallel()
	w := standard(t)
	res, c := w.query(sprint.SprintQ{Kind: sprint.QueryFleet, Fields: []string{"status"}})
	l := res.(ListingResult)
	if !reflect.DeepEqual(l.Rows, []string{"m1", "m2"}) || l.HasMore {
		t.Fatalf("rows %v", l.Rows)
	}
	m1, m2 := l.Items[0], l.Items[1]
	if m1.Control == nil || fieldValue(*m1.Control, "status") != "up" || fieldValue(*m2.Control, "status") != "down" {
		t.Fatalf("control cards %+v %+v", m1.Control, m2.Control)
	}
	want := map[string]map[string]int{"m1": {"ready": 1, "working": 1}, "m2": {"withdrawn": 1}}
	// The cells are the definition's columns but the control's, in its order.
	snap, err := w.m.Snapshot(testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	var wantCols []string
	for _, c := range snap.Definitions[sprint.Fleet].Columns {
		if c != sprint.Ctl {
			wantCols = append(wantCols, c)
		}
	}
	for _, it := range l.Items {
		got := map[string]int{}
		cols := []string{}
		for _, cn := range it.Counts {
			cols = append(cols, cn.Col)
			if cn.N != 0 {
				got[cn.Col] = cn.N
			}
		}
		if !reflect.DeepEqual(got, want[it.Row]) || !reflect.DeepEqual(cols, wantCols) {
			t.Fatalf("%s: counts %v of columns %v", it.Row, got, cols)
		}
	}
	// Two members, their control cards, and five cells each.
	if c.Records != 2 || c.Probes != 1+10 || c.RowIDs != 2 {
		t.Fatalf("charged %+v", c)
	}
	a := l.Project(sprint.SprintQ{Kind: sprint.QueryFleet})
	if !reflect.DeepEqual(a.Rows, []string{"m1", "m2"}) || len(a.Counts) != 10 || len(a.Records) != 2 || a.Records[0].Table != sprint.Fleet {
		t.Fatalf("projection %+v", a)
	}
	// The readers: no control column, so no control card; four cells a reader.
	res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryReaders, Fields: []string{}})
	l = res.(ListingResult)
	if !reflect.DeepEqual(l.Rows, []string{"r1", "r2"}) || l.Items[0].Control != nil || len(l.Items[0].Counts) != 4 {
		t.Fatalf("%+v", l)
	}
	if n := l.Items[0].Counts[0]; n.Col != "asked" || n.N != 1 {
		t.Fatalf("r1's asked cell %+v", n)
	}
	// Units cut the listing, and it says so.
	res, _ = w.query(sprint.SprintQ{Kind: sprint.QueryFleet, Fields: []string{}, Units: 1})
	if l := res.(ListingResult); !l.HasMore || len(l.Rows) != 1 {
		t.Fatalf("%+v", l)
	}
}

// TestQueryCostHolds: no answer exceeds its declared cost (1.0): for every
// kind, over random queries on random worlds, the records a query read, the
// range ids it returned (less the rows a listing read) and the estimate of its
// bytes from those, are at most sprint.QueryCost of the query (IT05); and the
// probes it made are at most the probes it declares (QueryProbes), which the
// design's cost table states beside records (errata 1, the addendum).
func TestQueryCostHolds(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.note("blocked", "c1", "p1", "p2")
	note := "n" + strconv.Itoa(len(w.log.Lines(testPrefix, "0")))
	lineSeq := uint64(len(w.log.Lines(testPrefix, "0")))
	allFollows := sprint.Follows
	queries := []sprint.SprintQ{
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p1", "w1", "v1", "k1", "q1", "x1", "nobody"), Fields: []string{"attempt", "open"}, Follow: allFollows},
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s1", 3), Fields: []string{}, Follow: []string{sprint.FollowNeeds, sprint.FollowJOpen}},
		{Kind: sprint.QueryRelated, Table: sprint.Fleet, Source: head("m1:working", 5), Fields: []string{}, Follow: []string{sprint.FollowDue, sprint.FollowMember}},
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: lineSeq, About: true}, Fields: []string{}},
		{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"attempt"}, Heads: []sprint.HeadQ{
			{Index: sprint.HeadEligBelow, Limit: 4, Follow: []string{sprint.FollowNeeds}}, {Index: sprint.HeadFreshBelow, Limit: 3},
			{Index: sprint.HeadFreshAbove, Limit: 3}, {Index: sprint.HeadAgain, Limit: 3, Follow: []string{sprint.FollowWithdrawn, sprint.FollowWork}}}},
		{Kind: sprint.QueryFront, Stream: "s2", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadAgain, Limit: 2}}},
		{Kind: sprint.QueryWaiters, Source: ids("ghost", "p1", "p2"), Limit: 3, Fields: []string{"open"}},
		{Kind: sprint.QueryWaiters, Source: head("missing", 4), Limit: 2, Fields: []string{}},
		{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 2},
		{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 3, Units: 1},
		{Kind: sprint.QueryFleet, Fields: []string{"status"}},
		{Kind: sprint.QueryReaders, Fields: []string{}, Units: 5},
		{Kind: sprint.QueryNeedchain, Source: ids("w1", "w2"), Limit: 5, Fields: []string{"open"}},
		{Kind: sprint.QueryNeedchain, Source: head("missing", 2), Limit: 1, Fields: []string{}},
		{Kind: sprint.QueryJnote, Source: ids(note), Fields: []string{}, Subjects: 5},
		{Kind: sprint.QueryJnote, Source: head("jnotes", 3), Fields: []string{}, Subjects: 4},
	}
	for i, q := range queries {
		res, c, err := w.tw.QueryFull(q)
		if err != nil {
			t.Fatalf("query %d (%s): %v", i, q.Kind, err)
		}
		cost := sprint.QueryCost(q)
		if c.Records > cost.Records {
			t.Errorf("query %d (%s): read %d records, declared %d", i, q.Kind, c.Records, cost.Records)
		}
		if got := c.RangeIDs - c.RowIDs; got > cost.RangeIDs {
			t.Errorf("query %d (%s): returned %d range ids (less %d rows), declared %d", i, q.Kind, c.RangeIDs, c.RowIDs, cost.RangeIDs)
		}
		est := c.Records*sprint.RecordEnvelopeBytes + c.Records*sprint.FieldBytes*len(q.Fields) + (c.RangeIDs-c.RowIDs)*sprint.RangeIDBytes
		if est > cost.Bytes {
			t.Errorf("query %d (%s): %d bytes by IT05's estimate of what it read, declared %d", i, q.Kind, est, cost.Bytes)
		}
		if p := QueryProbes(q); c.Probes > p {
			t.Errorf("query %d (%s): made %d probes, declared %d", i, q.Kind, c.Probes, p)
		}
		_ = res
	}
}

// TestQueryCostHoldsRandom: the same holds for 600 random queries on the four
// worlds, refused ones included (a refusal has read less than it declared).
func TestQueryCostHoldsRandom(t *testing.T) {
	t.Parallel()
	for name, w := range luaWorlds(t) {
		for i, rq := range randomQueries(w, int64(len(name))*104729, 150) {
			q := rq.Q
			_, c, _ := w.tw.QueryFull(q)
			cost := sprint.QueryCost(q)
			if c.Records > cost.Records || c.RangeIDs-c.RowIDs > cost.RangeIDs {
				t.Errorf("%s #%d (%s %s): read %+v, declared %+v", name, i, q.Kind, rq.Enc.Query, c, cost)
			}
			if p := QueryProbes(q); c.Probes > p {
				t.Errorf("%s #%d (%s %s): made %d probes, declared %d", name, i, q.Kind, rq.Enc.Query, c.Probes, p)
			}
		}
	}
}

// TestQueryAnswersLoadIntoAPartialSnapshot: what Twin.Query returns is what
// IT05's LoadPartial reads: a plan of composite queries answered by the twin,
// each projected onto sprint.Answer, loads without a misalignment, and the
// snapshot has the sentinel and the count before it (front), the members and
// their cells' counts (fleet), the readers, the streams, the read cards of a
// primary (related with rcards) and the needs' records.
func TestQueryAnswersLoadIntoAPartialSnapshot(t *testing.T) {
	t.Parallel()
	w := standard(t)
	qs := []sprint.SprintQ{
		{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"attempt", "kind"}, Heads: []sprint.HeadQ{
			{Index: sprint.HeadEligBelow, Limit: 5}, {Index: sprint.HeadAgain, Limit: 5, Follow: []string{sprint.FollowWork}}}},
		{Kind: sprint.QueryFleet, Fields: []string{"status"}},
		{Kind: sprint.QueryReaders, Fields: []string{}},
		{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 3},
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("v1"), Fields: []string{"primary", "rcards"}, Follow: []string{sprint.FollowRCards}},
		{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s1", 3), Fields: []string{"open"}},
		{Kind: sprint.QueryWaiters, Source: ids("ghost", "p1"), Limit: 3, Fields: []string{"open"}},
		{Kind: sprint.QueryNeedchain, Source: ids("w1"), Limit: 5, Fields: []string{"needs"}},
	}
	var answers []sprint.Answer
	for _, q := range qs {
		a, err := w.tw.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q.Kind, err)
		}
		answers = append(answers, a)
	}
	rp := sprint.ReadPlan{Sprint: qs}
	snap, err := sprint.LoadPartial(rp, sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0", TimeMS: "1790000000123", Sprint: answers})
	if err != nil {
		t.Fatalf("the twin's answers do not load: %v", err)
	}
	if g := sprint.FirstSentinel(snap, "s1"); g == nil || g.ID != "g1" || g.Score != 50 {
		t.Fatalf("the first sentinel of s1: %+v", g)
	}
	if n := sprint.OpenBefore(snap, "s1", 50); n != 7 {
		t.Fatalf("the open cards before it: %d", n)
	}
	if rows := snap.Fleet.Rows(); !reflect.DeepEqual(rows, []string{"m1", "m2"}) {
		t.Fatalf("fleet rows %v", rows)
	}
	if n := snap.Fleet.Count("m1", "working"); n != 1 {
		t.Fatalf("m1's working cell counts %d", n)
	}
	if snap.Fleet.Card("ctl-m1") == nil || snap.Fleet.Card("ctl-m1").Fields["status"] != "up" {
		t.Fatalf("m1's control card: %+v", snap.Fleet.Card("ctl-m1"))
	}
	if rows := snap.Readers.Rows(); !reflect.DeepEqual(rows, []string{"r1", "r2"}) {
		t.Fatalf("reader rows %v", rows)
	}
	if rows := snap.Streams(); !reflect.DeepEqual(rows, []string{"s1", "s2"}) {
		t.Fatalf("streams %v", rows)
	}
	if reads := snap.Readers.Of("v1"); len(reads) != 2 {
		t.Fatalf("the read cards of v1: %+v", reads)
	}
	// w1's chain: p1, which has a record, and ghost, which has none and is left
	// out of the cards (a record that does not exist is not a card).
	if c := snap.Work.Card("p1"); c == nil || c.Col != "waiting" {
		t.Fatalf("a need's record: %+v", c)
	}
	if c := snap.Work.Card("ghost"); c != nil {
		t.Fatalf("ghost has no record: %+v", c)
	}
	if c := snap.Fleet.Card("a1.w2"); c == nil || c.Row != "m1" || c.Col != "ready" {
		t.Fatalf("the live work card of a1 (an again head's follow): %+v", c)
	}
	// An answer that does not align with its plan is refused by the loader, not loaded.
	if _, err := sprint.LoadPartial(rp, sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0", TimeMS: "1", Sprint: answers[:3]}); err == nil {
		t.Fatal("a short answer loaded")
	}
}

// TestQueryCostOfFollows: the follow costs the twin's bounds use are IT05's:
// 1 a follow, 15 for rcards and 64 for needs (1.0's table), so that a card
// with the most needs and read cards costs what QueryCost declares.
func TestQueryCostOfFollows(t *testing.T) {
	t.Parallel()
	one := func(follow ...string) int {
		return sprint.QueryCost(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("a"), Fields: []string{}, Follow: follow}).Records
	}
	if one() != 1 || one(sprint.FollowWork) != 2 || one(sprint.FollowRCards) != 1+followMaxRCards || one(sprint.FollowNeeds) != 1+followMaxNeeds {
		t.Fatalf("records a card: none %d, work %d, rcards %d, needs %d", one(), one(sprint.FollowWork), one(sprint.FollowRCards), one(sprint.FollowNeeds))
	}
	// The most a card can ask: every follow at its bound, the twin reads exactly
	// that many and no more.
	w := newQWorld(t)
	w.rows(sprint.Work, "s1")
	w.rows(sprint.Readers, "r1")
	var needs, rcards []string
	var cs []card
	for i := 0; i < followMaxNeeds; i++ {
		id := "n" + strconv.Itoa(i)
		needs = append(needs, id)
		cs = append(cs, card{sprint.Work, "s1", "waiting", id, strconv.Itoa(i + 1), fields("kind", "work", "open", "0")})
	}
	var rc []card
	for i := 0; i < followMaxRCards; i++ {
		id := "p.r1." + strconv.Itoa(i)
		rcards = append(rcards, id)
		rc = append(rc, card{sprint.Readers, "r1", "asked", id, strconv.Itoa(i + 1), fields("primary", "p")})
	}
	cs = append(cs, card{sprint.Work, "s1", "review", "p", "100", fields("kind", "work", "attempt", "1", "needs", join(needs), "rcards", join(rcards))})
	w.cards(cs...)
	w.cards(rc...)
	q := sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p"), Fields: []string{}, Follow: []string{sprint.FollowRCards, sprint.FollowNeeds}}
	_, c := w.query(q)
	if c.Records != 1+followMaxRCards+followMaxNeeds || c.Records != sprint.QueryCost(q).Records {
		t.Fatalf("read %d records, declared %d", c.Records, sprint.QueryCost(q).Records)
	}
	// One need past the bound is a refusal, never a cut answer.
	w.cards(card{sprint.Work, "s1", "review", "p2", "101", fields("kind", "work", "needs", join(append(needs, "extra")))})
	q.Source = ids("p2")
	if ref := w.refused(q); ref.Code != codeDrift || !reflect.DeepEqual(ref.Detail.IDs, []string{"p2"}) {
		t.Fatalf("%+v", ref)
	}
}

func join(list []string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// TestTwinEqualsLuaQueries (IT30): the twin's answer to each query is the Lua
// kind's on the same state, over 10,000 random reads. It waits on gate G0: no
// store loads the Lua before it, and the twin cannot stand for a store that is
// not running one.
func TestTwinEqualsLuaQueries(t *testing.T) {
	t.Parallel()
	t.Skip("waits on gate G0: Layer 1's revision 4 pinned, Layer 2 accepted again against its hash, and a store that loads sprint_queries.lua; the Lua is checked against stubs of Layer 1's helpers in TestLuaQueriesOnStubbedHelpers")
}

// TestUseQueriesAnswersAReadRequest: a twin that has UseQueries answers the
// sprint queries of an atomic ReadRequest in their slots, from the read's
// snapshot, as the JSON the Lua returns; a twin that has not refuses a sprint
// query, and a malformed query is refused with the index of its slot.
func TestUseQueriesAnswersAReadRequest(t *testing.T) {
	t.Parallel()
	w := standard(t)
	rel, ref := EncodeSprintQ(sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p1"), Fields: []string{"open"}, Follow: []string{sprint.FollowJOpen}})
	if ref != nil {
		t.Fatal(ref)
	}
	key, ref := EncodeKeyQ(KeyQ{Kind: KeyTick})
	if ref != nil {
		t.Fatal(ref)
	}
	rr := &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "count", Table: sprint.Work, Cells: []string{"s1:waiting"}}},
		Sprint: []SprintQuery{rel, key}}
	got, err := Read(context.Background(), w.tw, rr)
	if err != nil || got.Read == nil {
		t.Fatalf("%+v %v", got, err)
	}
	if len(got.Read.Sprint) != 2 || got.Read.Tset[0].Counts[0] != 6 {
		t.Fatalf("%+v", got.Read)
	}
	dec, err := DecodeResult(sprint.QueryRelated, got.Read.Sprint[0])
	if err != nil {
		t.Fatal(err)
	}
	if r := dec.(RelatedResult); r.Items[0].ID != "p1" || r.Items[0].Follows.JOpen == nil {
		t.Fatalf("%+v", r)
	}
	dec, err = DecodeResult(KeyTick, got.Read.Sprint[1])
	if err != nil || *dec.(TickResult).Cur != "77" {
		t.Fatalf("%+v %v", dec, err)
	}

	// A twin that never called UseQueries refuses.
	plain, _, _ := newTestTwin(t, passX())
	res, err := Read(context.Background(), plain, &ReadRequest{Epoch: "0", Sprint: []SprintQuery{key}})
	if err != nil || res.Refusal == nil || res.Refusal.Code != CodeRequest {
		t.Fatalf("a twin without the queries: %+v %v", res, err)
	}
	// A query the validation refuses comes back REQUEST at its slot's index.
	bad := SprintQuery{Kind: sprint.QueryRelated, Query: json.RawMessage(`{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":["a b"]}}`)}
	res, err = Read(context.Background(), w.tw, &ReadRequest{Epoch: "0", Sprint: []SprintQuery{key, bad}})
	if err != nil || res.Refusal == nil || res.Refusal.Code != CodeRequest || res.Refusal.Detail.QueryIndex == nil || *res.Refusal.Detail.QueryIndex != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}
