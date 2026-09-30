package sprint

import (
	"errors"
	"strings"
	"testing"
)

// The read seam of the position rules (rules_position_read.go): what an answer
// must be to load, what a plan may read of it, and what the sprint keys and
// counts a query also reads cost.

// A read that is complete or it is not used (1.5.1): an answer that does not
// answer its plan is ErrMisaligned, and nothing is planned on it.
func TestPositionAnswersThatDoNotAnswerTheirReadAreRefused(t *testing.T) {
	t.Parallel()
	front := func(tw *posTwin) (ReadPlan, ReadAnswer) {
		rp, _ := posRule(t, ruleResolve).Read([]AgendaKey{posKeyOf("resolve:s1")}, posBounds, 0)
		return rp, tw.answer(rp)
	}
	waiters := func(tw *posTwin) (ReadPlan, ReadAnswer) {
		rp, _ := posRule(t, ruleNeeds).Read([]AgendaKey{posKeyOf("needs:n")}, posBounds, 0)
		return rp, tw.answer(rp)
	}
	// the head of a made need after the waiter w2, of three in wait:n
	after := func(tw *posTwin) (ReadPlan, ReadAnswer) {
		rp, _ := posRule(t, ruleNeeds).Read([]AgendaKey{{Key: "made:n+w2", Seq: 9}}, posBounds, 0)
		return rp, tw.answer(rp)
	}
	threeWaiters := func() *posTwin {
		tw := newPosTwin("s1")
		tw.card("n", "s1", Waiting, 0)
		tw.missing["n"] = true
		for i, id := range []string{"w1", "w2", "w3"} {
			tw.card(id, "s1", Waiting, float64(i+1))
			tw.waiter("n", id)
		}
		return tw
	}
	record := func(id string) TableCard { return TableCard{Work, &Card{ID: id, Row: "s1", Col: Waiting}} }
	streams := func(tw *posTwin) (ReadPlan, ReadAnswer) {
		rp, _ := posRule(t, ruleDone).Read([]AgendaKey{posKeyOf("done")}, posBounds, 0)
		return rp, tw.answer(rp)
	}
	cases := []struct {
		name   string
		build  func(*posTwin) (ReadPlan, ReadAnswer)
		twin   func() *posTwin
		break_ func(*ReadPlan, *ReadAnswer)
	}{
		{"a head of two missing", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) {
			rp.Sprint[0].Heads = append(rp.Sprint[0].Heads, HeadQ{Index: HeadFreshAbove, Limit: 1})
		}},
		{"a head too many", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Heads = append(a.Sprint[0].Heads, HeadAnswer{Index: HeadFreshAbove})
		}},
		{"a head of another index", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) { a.Sprint[0].Heads[0].Index = HeadAgain }},
		{"a head over its limit", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) {
			rp.Sprint[0].Heads[0].Limit = 1
		}},
		{"a head id with no record", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Heads[0].IDs = append(a.Sprint[0].Heads[0].IDs, "ghost")
			rp.Sprint[0].Heads[0].Limit = 10
		}},
		{"a sprint key not answered", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) { a.Sprint[0].Keys = a.Sprint[0].Keys[:1] }},
		{"a sprint key of another name", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) { a.Sprint[0].Keys[1].Key = KeyNextStreams }},
		{"jopen of a card that is not the first sentinel", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) { a.Sprint[0].Keys[0].Subject = "someone" }},
		{"a sprint key nobody reads", front, posResolveTwin, func(rp *ReadPlan, a *ReadAnswer) {
			rp.Sprint[0].Keys = append(rp.Sprint[0].Keys, "weather")
			a.Sprint[0].Keys = append(a.Sprint[0].Keys, KeyAnswer{Key: "weather"})
		}},
		{"a line answer over its window", func(tw *posTwin) (ReadPlan, ReadAnswer) {
			rp, _ := posRule(t, ruleNeeds).Read([]AgendaKey{{Key: "made@9", Seq: 9}}, posBounds, 0)
			return rp, tw.answer(rp)
		}, func() *posTwin {
			tw := newPosTwin("s1")
			tw.lines[9] = []string{"a"}
			return tw
		}, func(rp *ReadPlan, a *ReadAnswer) {
			rp.Sprint[0].Source.Limit = 1
			a.Sprint[0].IDs = []string{"a", "b"}
			a.Sprint[0].Needs = []NeedAnswer{{ID: "a"}, {ID: "b"}}
		}},
		{"ids named that the query did not", waiters, func() *posTwin { return newPosTwin("s1") }, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].IDs = []string{"other"}
		}},
		{"a need missing", waiters, func() *posTwin { return newPosTwin("s1") }, func(rp *ReadPlan, a *ReadAnswer) { a.Sprint[0].Needs = nil }},
		{"a need of another id", waiters, func() *posTwin { return newPosTwin("s1") }, func(rp *ReadPlan, a *ReadAnswer) { a.Sprint[0].Needs[0].ID = "other" }},
		{"a waiter with no record", waiters, func() *posTwin { return newPosTwin("s1") }, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Needs[0].Waiters = []string{"ghost"}
		}},
		{"more waiters than the head", waiters, func() *posTwin {
			tw := newPosTwin("s1")
			tw.card("w1", "s1", Waiting, 1)
			tw.card("w2", "s1", Waiting, 2)
			return tw
		}, func(rp *ReadPlan, a *ReadAnswer) {
			rp.Sprint[0].Limit = 1
			a.Sprint[0].Needs[0].Waiters = []string{"w1", "w2"}
			a.Sprint[0].Records = []TableCard{{Work, &Card{ID: "w1", Row: "s1", Col: Waiting}}, {Work, &Card{ID: "w2", Row: "s1", Col: Waiting}}}
		}},
		{"a head that starts at the cursor", after, threeWaiters, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Needs[0].Waiters = []string{"w2", "w3"}
			a.Sprint[0].Records = append(a.Sprint[0].Records, record("w2"))
		}},
		{"a head from before the cursor", after, threeWaiters, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Needs[0].Waiters = []string{"w1", "w3"}
			a.Sprint[0].Records = append(a.Sprint[0].Records, record("w1"))
		}},
		{"a head out of the order of wait:n", waiters, threeWaiters, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Needs[0].Waiters = []string{"w2", "w1"}
			a.Sprint[0].Records = []TableCard{record("w1"), record("w2")}
		}},
		{"a head that repeats a waiter", waiters, threeWaiters, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Needs[0].Waiters = []string{"w1", "w1"}
			a.Sprint[0].Records = []TableCard{record("w1")}
		}},
		{"more waiters and none given", waiters, threeWaiters, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Needs[0].Waiters = nil
			a.Sprint[0].Needs[0].More = true
			a.Sprint[0].Needs[0].Last = ""
		}},
		{"a head that ends before its waiters", waiters, threeWaiters, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Needs[0].Last = "w0"
		}},
		{"stuck ids of a stream the query does not list", streams, func() *posTwin { return posDoneTwin(2, false) }, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Stuck = []StuckAnswer{{Stream: "elsewhere", IDs: nil}}
		}},
		{"counts of the wrong number of cells", streams, func() *posTwin { return posDoneTwin(2, false) }, func(rp *ReadPlan, a *ReadAnswer) {
			a.Sprint[0].Counts = a.Sprint[0].Counts[1:]
		}},
		{"counts nobody asked for", streams, func() *posTwin { return posDoneTwin(2, false) }, func(rp *ReadPlan, a *ReadAnswer) {
			rp.Sprint[0].Counts = nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tw := c.twin()
			rp, ans := c.build(tw)
			if _, err := LoadPartial(rp, ans); err != nil {
				t.Fatalf("the unbroken answer does not load: %v", err)
			}
			c.break_(&rp, &ans)
			if _, err := LoadPartial(rp, ans); !errors.Is(err, ErrMisaligned) {
				t.Fatalf("a read with %s loaded: %v", c.name, err)
			}
		})
	}
}

// A `front` answer that lists no heads gives them as records only, IT05's
// Answer, which is how the fleet rules' reads are answered (IT07): the loader,
// which runs for every composite query of every rule, takes it, and a position
// rule that reads a head of it is refused at the read. In a test build its plan
// panics there; in a release build it plans nothing, ends no key, and the
// snapshot names the head (UnloadedErr), so the tick refuses the plan. (Found on
// the merge with IT07: the loader refused every front answer that did not list
// its heads, and 30 of IT07's tests with it.)
func TestPositionFrontAnswerWithoutHeadsIsRefusedAtTheRead(t *testing.T) {
	t.Parallel()
	for name, key := range map[string]string{ruleResolve: "resolve:s1", posPullbackRule: "pullback:s1"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tw := posResolveTwin()
			tw.card("f", "s1", Ready, 8)
			keys := []AgendaKey{posKeyOf(key)}
			rp, left := posRule(t, name).Read(keys, posBounds, 0)
			if len(left) != 0 || len(rp.Sprint) != 1 || rp.Sprint[0].Kind != QueryFront || len(rp.Sprint[0].Heads) == 0 {
				t.Fatalf("%s's read is not one front query with heads: %+v left %v", name, rp, left)
			}
			unlisted := func() ReadAnswer {
				a := tw.answer(rp)
				if len(a.Sprint[0].Heads) == 0 {
					t.Fatalf("the twin listed no heads to take away")
				}
				a.Sprint[0].Heads = nil // the records stay, as IT05's Answer gives them
				return a
			}
			s, err := loadPartial(rp, unlisted(), false)
			if err != nil {
				t.Fatalf("a front answer that lists no heads does not load: %v", err)
			}
			if _, ok := posFrontOf(s, "s1"); !ok {
				t.Fatal("the front itself was not read")
			}
			p := posRule(t, name).Plan(s, keys, tw.now)
			if len(p.Done)+len(p.Requeue)+len(p.HeldBack)+len(p.Plan.Units)+len(p.Plan.Refused)+len(p.Notes)+len(p.Intents) != 0 {
				t.Fatalf("%s planned on heads the answer did not list: %+v", name, p)
			}
			if err := s.UnloadedErr(); !errors.Is(err, ErrUnloaded) || !strings.Contains(err.Error(), "the head ") {
				t.Fatalf("%s read a head the answer did not list, and the snapshot says: %v", name, err)
			}
			strict, err := LoadPartial(rp, unlisted())
			if err != nil {
				t.Fatal(err)
			}
			if msg := posPanicOf(func() { posRule(t, name).Plan(strict, keys, tw.now) }); !strings.Contains(msg, "did not load") {
				t.Fatalf("%s in a test build: %q", name, msg)
			}
			// the same read with its heads listed plans
			whole, err := LoadPartial(rp, tw.answer(rp))
			if err != nil {
				t.Fatal(err)
			}
			if p := posRule(t, name).Plan(whole, keys, tw.now); len(p.Done)+len(p.Requeue) == 0 {
				t.Fatalf("%s with its heads listed ends no key: %+v", name, p)
			}
		})
	}
}

// The agenda key of a waiters query is the key its rule built it from, for every
// form of key: the plan finds its answer by it.
func TestWaitersQueryKeyIsTheKeyItWasBuiltFrom(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"needs:n", "needs@7", "needs@7+100", "made:n", "made:n+w150", "made@7", "made@7+300"} {
		rp, left := posRule(t, ruleNeeds).Read([]AgendaKey{{Key: key, Seq: 3}}, posBounds, 0)
		if len(left) != 0 || len(rp.Sprint) != 1 {
			t.Fatalf("%s: %+v left %v", key, rp, left)
		}
		if got := posQueryKey(rp.Sprint[0]); got != key {
			t.Errorf("the query read for %q names the key %q", key, got)
		}
	}
}

// The snapshot refuses a read of what the plan's read did not ask for, at every
// accessor, in a test build by panicking (IT05).
func TestPositionAccessorsRefuseWhatWasNotAsked(t *testing.T) {
	t.Parallel()
	tw := posResolveTwin()
	rp, _ := posRule(t, ruleResolve).Read([]AgendaKey{posKeyOf("resolve:s1")}, posBounds, 0)
	s := tw.load(t, rp)
	refused := map[string]func(){
		"the fresh head R3 did not ask":       func() { posHeadOf(s, "s1", HeadFreshAbove) },
		"front of a stream no query is about": func() { posFrontOf(s, "s2") },
		"jopen of the sprint":                 func() { posJudgedSprint(s, NSprintDone) },
		"the version of the stream set":       func() { posNextStreams(s) },
		"the streams":                         func() { posStreamsRead(s) },
		"the waiters of a key no query reads": func() { posKeyViewOf(s, posKeyOf("needs:n")) },
	}
	for what, read := range refused {
		if msg := posPanicOf(read); !strings.Contains(msg, "did not load") {
			t.Errorf("%s: %q", what, msg)
		}
	}
	// what it asked is read
	if f, ok := posFrontOf(s, "s1"); !ok || f.G == nil || f.G.ID != "g" || f.NBefore != 3 {
		t.Fatalf("front: %+v %v", f, ok)
	}
	if head, more, ok := posHeadOf(s, "s1", HeadEligBelow); len(head) != 3 || more || !ok {
		t.Fatalf("head: %d %v %v", len(head), more, ok)
	}
	if posJudgedG(s, "s1", NSentinelReached) || posDropping(s, "s1") {
		t.Fatal("nothing is judged or dropping")
	}
}

// A query's sprint keys and counts are in its declared cost, so that a read is
// sized with them.
func TestSprintKeysAndCountsAreInTheDeclaredCost(t *testing.T) {
	t.Parallel()
	base := SprintQ{Kind: QueryStreams, Limit: 10, Fields: []string{"dropped"}}
	c0 := QueryCost(base)
	withKeys := base
	withKeys.Keys = []string{KeyNextStreams, KeyJOpenSprint, KeyDropping}
	if d := QueryCost(withKeys); d.Bytes != c0.Bytes+3*sprintKeyBytes || d.Records != c0.Records || d.RangeIDs != c0.RangeIDs {
		t.Fatalf("three keys: %+v over %+v", d, c0)
	}
	withCounts := base
	withCounts.Counts = posCountColumns
	if d := QueryCost(withCounts); d.Bytes != c0.Bytes+MaxStreams*len(posCountColumns)*CountBytes {
		t.Fatalf("counts: %+v over %+v", d, c0)
	}
	// the counts are the cells of a work table's streams, which a `streams`
	// answer may carry
	if countsOf[QueryStreams] != Work {
		t.Fatalf("a streams query counts the cells of %q", countsOf[QueryStreams])
	}
}
