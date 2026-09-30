package verbs

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The cold read's fixes to IT19: an id the table holds refused from the read,
// a named sentinel's needs, rank --score's close of "sentinel reached", and a
// race test for each guard the verbs hold at apply.

// raw creates a primary card of a stream at a cell and score, as another verb
// would between a verb's read and its step (a fixture).
func (w *addWorld) raw(id, stream, col, sc string) {
	w.t.Helper()
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "add", Actor: "coord"}, Body: sprintfn.Body{Entries: []tset.Entry{{Kind: "create",
		Table: sprint.Work, To: stream + ":" + col, IDs: []string{id}, About: []string{id}, Scores: []string{sc},
		Set: map[string]string{"kind": "primary", "stream": stream, "attempt": "0"}}}}})
}

// reach opens "sentinel reached" on g, as R3's reach does (a fixture), with op
// as its identity.
func (w *addWorld) reach(g, op string) {
	w.t.Helper()
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "resolve", Actor: "machine"}, Body: sprintfn.Body{
		Op:    &sprintfn.Op{ID: op, Intent: op},
		Notes: []sprintfn.NoteReq{{Op: "open", Type: sprint.NSentinelReached, Cause: sprint.ReachedCause, Subjects: []string{g}, Text: "reached"}}}})
	if !w.reached(g) {
		w.t.Fatalf("the fixture opened no reached judgment on %s: %v", g, w.jopen(g))
	}
}

// reached says "sentinel reached" is open on g.
func (w *addWorld) reached(g string) bool {
	_, ok := w.jopen(g)[sprint.NSentinelReached+"|"+sprint.ReachedCause]
	return ok
}

// hookedEnv is an Env whose client runs fn just before the at-th step.
func (w *addWorld) hookedEnv(at int, fn func()) *Env {
	return &Env{C: &Counting{C: &hooked{c: w.tw, at: at, fn: fn}}, Names: addNames, Actor: "coord", noWait: true}
}

// TestAddExistingIDRefusedBeforeStep: an add of an id the table holds is
// refused EXISTS from the part's read, naming the id, with no step and no
// retry (section 3: add reads the ids' absence; VGuard "add", col[a] =
// "none"); a generated id a named add took is refused so at the part that
// would create it, the parts before staying applied; in part 1 of a --count
// add, which learns its ids from its own read, the step finds it and the
// retry reads the ids and refuses, naming it.
func TestAddExistingIDRefusedBeforeStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"c1", "c2"}})
	w.cc.Reset()
	_, err := Add(ctx, w.env, sprint.AddReq{Stream: "s1", IDs: []string{"c0", "c1"}})
	rf := refusedAs(t, err, "EXISTS", "c1")
	if !rf.Local || rf.Retries != 0 || w.cc.Steps() != 0 || w.cc.Trips() != 1 {
		t.Fatalf("local %v, retries %d, steps %d, trips %d; want a local refusal from one read, no step", rf.Local, rf.Retries, w.cc.Steps(), w.cc.Trips())
	}
	if _, ok := w.work()["c0"]; ok {
		t.Fatal("the refused add created c0")
	}

	w.add(sprint.AddReq{Stream: "s2", IDs: []string{"s2-2500"}})
	w.cc.Reset()
	_, err = Add(ctx, w.env, sprint.AddReq{Stream: "s2", Count: 3000})
	rf = refusedAs(t, err, "EXISTS", "s2-2500", "stay applied")
	if !rf.Local || rf.Part != 2 || w.cc.Steps() != 1 {
		t.Fatalf("local %v, part %d, steps %d; want part 2 refused from its read after part 1's one step", rf.Local, rf.Part, w.cc.Steps())
	}
	if _, ok := w.work()["s2-2000"]; !ok {
		t.Fatal("part 1 is not applied")
	}

	w.add(sprint.AddReq{Stream: "s3", IDs: []string{"s3-2"}})
	w.cc.Reset()
	_, err = Add(ctx, w.env, sprint.AddReq{Stream: "s3", Count: 5})
	rf = refusedAs(t, err, "EXISTS", "s3-2")
	if !rf.Local || w.cc.Steps() != 1 {
		t.Fatalf("local %v, steps %d; want one refused step, then a local refusal from the retry's read", rf.Local, w.cc.Steps())
	}
	if _, ok := w.work()["s3-1"]; ok {
		t.Fatal("the refused add created s3-1")
	}
}

// TestAddSentinelNamesNeeds: add --sentinel g --needs x gives the sentinel its
// needs, its open and its waitfor (the model's NeedsOf and AddDest name needs
// on any card; R3's reach reads G's open).
func TestAddSentinelNamesNeeds(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"x"}})
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"g"}, Sentinel: true, Needs: []string{"x"}})
	c := w.card("g")
	if c.Column != "waiting" || c.Fields["kind"] != sprint.Sentinel || c.Fields["needs"] != "x" || c.Fields["open"] != "1" {
		t.Fatalf("g: %+v, want a sentinel in waiting naming x, open 1", c)
	}
	if _, ok := w.zset("wait:x")["g"]; !ok {
		t.Fatalf("g is not in wait:x: %v", w.zset("wait:x"))
	}
}

// TestRankScoreClosesReached: rank --score closes "sentinel reached" in its own
// step when an open card sorts before the sentinel after it (H13 rankclose;
// VEff "rank" with ReachedPassed, tla/SprintEvents.tla): a card ranked before
// G, or G ranked behind an open card; a card ranked after G, or G ranked with
// nothing open before it, leaves it open. Three round trips.
func TestRankScoreClosesReached(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"g"}, Sentinel: true})
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b"}})
	rank := func(id string, x float64) Result {
		t.Helper()
		res, err := Rank(ctx, w.env, sprint.RankReq{IDs: []string{id}, Score: &x})
		if err != nil {
			t.Fatalf("rank %s --score %v: %v", id, x, err)
		}
		return res
	}
	w.reach("g", "reach-1")
	if res := rank("b", 10); res.Trips != 3 || !w.reached("g") {
		t.Fatalf("rank b after g: %d round trips, reached %v; want 3, and g still reached", res.Trips, w.reached("g"))
	}
	rank("b", 0.5)
	if w.reached("g") {
		t.Fatal("b ranked before g left g reached")
	}
	w.reach("g", "reach-2")
	rank("g", 20)
	if w.reached("g") {
		t.Fatal("g ranked behind open cards is still reached")
	}
	w.add(sprint.AddReq{Stream: "s2", IDs: []string{"g2"}, Sentinel: true})
	w.reach("g2", "reach-3")
	rank("g2", 30)
	if !w.reached("g2") {
		t.Fatal("g2 ranked with nothing open before it lost its reached judgment")
	}
}

// TestReleaseRaceGuarded: a card placed before G between release's read and
// its step refuses the step (the rcount of G's five open cells before σ_G, at
// most 0: VGuard "release", the model's W5 witness); the retry reads it and
// refuses, and G stays waiting.
func TestReleaseRaceGuarded(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"g"}, Sentinel: true})
	env := w.hookedEnv(1, func() { w.raw("x", "s1", "ready", "0.5") })
	_, err := Release(context.Background(), env, sprint.ReleaseReq{IDs: []string{"g"}, Reason: "looked"})
	refusedAs(t, err, sprintfn.CodeRequest, "1 open cards", "g")
	if c := w.card("g"); c.Column != "waiting" {
		t.Fatalf("g: %+v, want waiting", c)
	}
}

// TestInsertRaceGuarded: a card placed between an insertion's anchor and its
// neighbour between the part's read and its step refuses the step (the
// rcount of the stream's six cells in (Lo, Hi) equal to the cards placed
// before the part, 1.5.4); the retry reads the new neighbour and places the
// card before it.
func TestInsertRaceGuarded(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b"}})
	env := w.hookedEnv(1, func() { w.raw("x", "s1", "ready", "1.5") })
	res, err := Add(context.Background(), env, sprint.AddReq{Stream: "s1", IDs: []string{"n"}, After: "a"})
	if err != nil || res.Retries != 1 {
		t.Fatalf("add: %+v, %v; want one retry", res, err)
	}
	if v := score(t, w.card("n")); !(v > 1 && v < 1.5) {
		t.Fatalf("n is at %v, want between a (1) and x (1.5)", v)
	}
}

// TestRankInLineRaceGuarded: a card placed between a rank's anchor and its
// neighbour between the read and the step refuses the step (the rcount of
// the stream's six cells between them, as many as the ranked cards there);
// the retry reads the new neighbour and ranks the card before it.
func TestRankInLineRaceGuarded(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b", "c"}})
	env := w.hookedEnv(1, func() { w.raw("x", "s1", "ready", "1.5") })
	res, err := Rank(context.Background(), env, sprint.RankReq{IDs: []string{"c"}, After: "a"})
	if err != nil || res.Retries != 1 {
		t.Fatalf("rank: %+v, %v; want one retry", res, err)
	}
	if v := score(t, w.card("c")); !(v > 1 && v < 1.5) {
		t.Fatalf("c is at %v, want between a (1) and x (1.5)", v)
	}
}

// TestRankScoreTakenGuarded: rank --score x below the counter at a score a card
// of the stream holds is refused from the read, with no step; a card placed
// at x between the read and the step refuses the step (U1's rcount at [x, x],
// at most 0; VGuard "rank", ScoreTaken), and the retry reads it and refuses.
// The ranked card does not move.
func TestRankScoreTakenGuarded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b", "c"}})
	x := 2.5
	if _, err := Rank(ctx, w.env, sprint.RankReq{IDs: []string{"b"}, Score: &x}); err != nil {
		t.Fatal(err)
	}
	w.cc.Reset()
	_, err := Rank(ctx, w.env, sprint.RankReq{IDs: []string{"a"}, Score: &x})
	if rf := refusedAs(t, err, sprintfn.CodeRequest, "taken"); !rf.Local || w.cc.Steps() != 0 {
		t.Fatalf("local %v, steps %d; want a local refusal, no step", rf.Local, w.cc.Steps())
	}
	y := 1.5
	env := w.hookedEnv(1, func() { w.raw("x", "s1", "ready", "1.5") })
	_, err = Rank(ctx, env, sprint.RankReq{IDs: []string{"a"}, Score: &y})
	refusedAs(t, err, sprintfn.CodeRequest, "taken")
	if v := score(t, w.card("a")); v != 1 {
		t.Fatalf("a is at %v, want 1", v)
	}
}
