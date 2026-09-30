package sprintfn

import (
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// loadAnswered asks the twin the query, projects the result onto
// sprint.Answer and loads it into a partial snapshot, which is IT08's
// alignment check of the answer (rules_position_read.go, loadPosition): the
// twin's answer to a query is what the position rules read.
func loadAnswered(t *testing.T, w *qworld, q sprint.SprintQ) sprint.Answer {
	t.Helper()
	a, err := w.tw.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q.Kind, err)
	}
	rp := sprint.ReadPlan{Sprint: []sprint.SprintQ{q}}
	if _, err := sprint.LoadPartial(rp, sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0", TimeMS: "1790000000123", Sprint: []sprint.Answer{a}}); err != nil {
		t.Fatalf("the twin's answer to a %s query does not load: %v", q.Kind, err)
	}
	return a
}

// TestProjectedWaitersAnswerEveryNeed: a `waiters` answer holds one NeedAnswer
// for each id of its source, in order, with the place, the score in
// {p}missing@e and the head of wait:n after the cursor, and loads through
// IT08's alignment check (1.0 `waiters`; IT30, IT08).
func TestProjectedWaitersAnswerEveryNeed(t *testing.T) {
	t.Parallel()
	w := standard(t)
	// a line that created two cards, ghost (a need that waiters named while it had
	// no record, and still has a score in missing) and n2
	w.cards(card{sprint.Work, "s2", "waiting", "ghost", "20", fields("kind", "work", "open", "0")},
		card{sprint.Work, "s2", "waiting", "n2", "21", fields("kind", "work", "open", "0")})
	line := uint64(len(w.log.Lines(testPrefix, "0")))
	w.seed(w.zadd("wait:n2", "0", "w2"))

	t.Run("a list", func(t *testing.T) {
		t.Parallel()
		a := loadAnswered(t, w, sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost", "nothing", "n2"), Limit: 1, Fields: []string{"open"}})
		want := []sprint.NeedAnswer{
			{ID: "ghost", Place: "waiting", Missing: true, Waiters: []string{"w1"}, More: true, Last: "w1"},
			{ID: "nothing"},
			{ID: "n2", Place: "waiting", Waiters: []string{"w2"}, Last: "w2"},
		}
		if !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("needs %+v, want %+v", a.Needs, want)
		}
		if a.IDs != nil || a.MoreIDs {
			t.Fatalf("a list names its ids itself: %v, more %v", a.IDs, a.MoreIDs)
		}
	})
	t.Run("the cursor and the head", func(t *testing.T) {
		t.Parallel()
		q := sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 5, Fields: []string{"open"}, WaiterAfter: "w1"}
		a := loadAnswered(t, w, q)
		if want := []sprint.NeedAnswer{{ID: "ghost", Place: "waiting", Missing: true, Waiters: []string{"w2"}, Last: "w2"}}; !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("after w1: %+v", a.Needs)
		}
		q.WaiterAfter = "w2"
		a = loadAnswered(t, w, q)
		if want := []sprint.NeedAnswer{{ID: "ghost", Place: "waiting", Missing: true}}; !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("after the last waiter: %+v", a.Needs)
		}
		// a head of one after the cursor has more beyond it
		q.WaiterAfter, q.Limit = "", 1
		a = loadAnswered(t, w, q)
		if want := []sprint.NeedAnswer{{ID: "ghost", Place: "waiting", Missing: true, Waiters: []string{"w1"}, More: true, Last: "w1"}}; !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("head of one: %+v", a.Needs)
		}
	})
	t.Run("a cursor with more beyond it", func(t *testing.T) {
		t.Parallel()
		// three waiters: a head of one after the first has the third beyond it
		w3 := standard(t)
		w3.cards(card{sprint.Work, "s1", "waiting", "w3", "90", fields("kind", "work", "open", "1", "needs", "ghost")})
		w3.seed(w3.zadd("wait:ghost", "0", "w3"))
		q := sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 1, Fields: []string{"open"}, WaiterAfter: "w1"}
		a := loadAnswered(t, w3, q)
		if want := []sprint.NeedAnswer{{ID: "ghost", Missing: true, Waiters: []string{"w2"}, More: true, Last: "w2"}}; !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("after w1, a head of one: %+v, want %+v", a.Needs, want)
		}
		q.WaiterAfter = "w2"
		if a = loadAnswered(t, w3, q); !reflect.DeepEqual(a.Needs, []sprint.NeedAnswer{{ID: "ghost", Missing: true, Waiters: []string{"w3"}, Last: "w3"}}) {
			t.Fatalf("after w2, the last head: %+v", a.Needs)
		}
	})
	t.Run("a head all quarantined with more behind", func(t *testing.T) {
		t.Parallel()
		// w1 heads wait:ghost and is quarantined: the head of one is left out
		// whole, and names w1 as the last member read, so the cursor moves past it
		// (M1; 2.3 R4) and the next head finds w2
		qw := newQuarantined(t)
		q := sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 1, Fields: []string{"open"}}
		a := loadAnswered(t, qw, q)
		if want := []sprint.NeedAnswer{{ID: "ghost", Missing: true, More: true, Last: "w1"}}; !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("a quarantined head: %+v, want %+v", a.Needs, want)
		}
		q.WaiterAfter = a.Needs[0].Last
		if a = loadAnswered(t, qw, q); !reflect.DeepEqual(a.Needs, []sprint.NeedAnswer{{ID: "ghost", Missing: true, Waiters: []string{"w2"}, Last: "w2"}}) {
			t.Fatalf("after the quarantined head: %+v", a.Needs)
		}
	})
	t.Run("the made needs", func(t *testing.T) {
		t.Parallel()
		// only an id with a score in {p}missing@e has its head read
		a := loadAnswered(t, w, sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost", "p1"), Limit: 5, Fields: []string{"open"}, Missing: true})
		want := []sprint.NeedAnswer{
			{ID: "ghost", Place: "waiting", Missing: true, Waiters: []string{"w1", "w2"}, Last: "w2"},
			{ID: "p1", Place: "waiting"},
		}
		if !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("needs %+v, want %+v", a.Needs, want)
		}
	})
	t.Run("a line", func(t *testing.T) {
		t.Parallel()
		src := sprint.IDSource{Kind: sprint.SourceLine, Seq: line, Limit: 1}
		a := loadAnswered(t, w, sprint.SprintQ{Kind: sprint.QueryWaiters, Source: src, Limit: 5, Fields: []string{"open"}})
		if !reflect.DeepEqual(a.IDs, []string{"ghost"}) || !a.MoreIDs || len(a.Needs) != 1 || a.Needs[0].ID != "ghost" {
			t.Fatalf("a window of one id of a line of two: ids %v, more %v, needs %+v", a.IDs, a.MoreIDs, a.Needs)
		}
		src.Limit = 2
		a = loadAnswered(t, w, sprint.SprintQ{Kind: sprint.QueryWaiters, Source: src, Limit: 5, Fields: []string{"open"}})
		if !reflect.DeepEqual(a.IDs, []string{"ghost", "n2"}) || a.MoreIDs || len(a.Needs) != 2 {
			t.Fatalf("the whole line: ids %v, more %v, needs %+v", a.IDs, a.MoreIDs, a.Needs)
		}
	})
	t.Run("a head", func(t *testing.T) {
		t.Parallel()
		a := loadAnswered(t, w, sprint.SprintQ{Kind: sprint.QueryWaiters, Source: head("missing", 10), Limit: 5, Fields: []string{"open"}})
		if !reflect.DeepEqual(a.IDs, []string{"ghost"}) || len(a.Needs) != 1 || !a.Needs[0].Missing {
			t.Fatalf("ids %v, needs %+v", a.IDs, a.Needs)
		}
	})
	t.Run("a quarantined id of a list", func(t *testing.T) {
		t.Parallel()
		q := newQuarantined(t)
		a := loadAnswered(t, q, sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("p2", "ghost"), Limit: 5, Fields: []string{"open"}})
		// p2 is left out of the read and still answered, with nothing; w1 is
		// left out of ghost's head.
		want := []sprint.NeedAnswer{{ID: "p2"}, {ID: "ghost", Missing: true, Waiters: []string{"w2"}, Last: "w2"}}
		if !reflect.DeepEqual(a.Needs, want) {
			t.Fatalf("needs %+v, want %+v", a.Needs, want)
		}
	})
}

// newQuarantined is the standard world with p2 and w1 in {p}quarantine@e.
func newQuarantined(t *testing.T) *qworld {
	t.Helper()
	w := standard(t)
	w.seed(w.hset("quarantine", "p2", "DRIFT", "w1", "DRIFT"))
	return w
}

// TestProjectedFrontListsItsHeads: a `front` answer lists one head for each
// head asked, in order, with the ids read and whether the index had more, so
// that a position rule reads the head from the answer (IT08's posHeadOf).
func TestProjectedFrontListsItsHeads(t *testing.T) {
	t.Parallel()
	w := standard(t)
	q := sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"attempt"}, Heads: []sprint.HeadQ{
		{Index: sprint.HeadEligBelow, Limit: 1}, {Index: sprint.HeadFreshBelow, Limit: 10},
		{Index: sprint.HeadFreshAbove, Limit: 10}, {Index: sprint.HeadAgain, Limit: 10}}}
	a := loadAnswered(t, w, q)
	want := []sprint.HeadAnswer{
		{Index: sprint.HeadEligBelow, IDs: []string{"p1"}, More: true},
		{Index: sprint.HeadFreshBelow, IDs: []string{"f1"}},
		{Index: sprint.HeadFreshAbove, IDs: []string{"f2"}},
		{Index: sprint.HeadAgain, IDs: []string{"a1"}},
	}
	if !reflect.DeepEqual(a.Heads, want) {
		t.Fatalf("heads %+v, want %+v", a.Heads, want)
	}
	// a stream with no sentinel above it lists the head above it, empty
	q = sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s2", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadFreshAbove, Limit: 3}}}
	if a = loadAnswered(t, w, q); len(a.Heads) != 1 || a.Heads[0].Index != sprint.HeadFreshAbove || len(a.Heads[0].IDs) != 0 {
		t.Fatalf("an empty head is listed: %+v", a.Heads)
	}
}

// TestProjectedStreamsListTheStuck: a `streams` answer lists the stuck ids of
// each stream stopped on a cross need, up to the limit, and whether the cell
// has more (IT08's posStuckOf).
func TestProjectedStreamsListTheStuck(t *testing.T) {
	t.Parallel()
	w := standard(t)
	q := sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 2}
	a := loadAnswered(t, w, q)
	if want := []sprint.StuckAnswer{{Stream: "s2", IDs: []string{"st1", "st2"}, More: true}}; !reflect.DeepEqual(a.Stuck, want) {
		t.Fatalf("stuck %+v, want %+v", a.Stuck, want)
	}
	q.Limit = 10
	a = loadAnswered(t, w, q)
	if want := []sprint.StuckAnswer{{Stream: "s2", IDs: []string{"st1", "st2", "st3"}}}; !reflect.DeepEqual(a.Stuck, want) {
		t.Fatalf("stuck %+v, want %+v", a.Stuck, want)
	}
}
