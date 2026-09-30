package sprintfn

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The cost of a step grows with its input: the cards its needs walk can reach,
// the needs of those cards, the ids of the request's entries. It does not grow
// with the number of admissions, however many name the same needs. These tests
// count work (deriveWork, and the store commands the Lua sends) and pin the
// counts; they do not time anything.

// chainRecs is n open waiting cards, each needing the next: c0 to c<n-1>.
func chainRecs(n int) map[string]fixRec {
	recs := map[string]fixRec{}
	for i := 0; i < n; i++ {
		needs := ""
		if i+1 < n {
			needs = "c" + strconv.Itoa(i+1)
		}
		recs["c"+strconv.Itoa(i)] = fix("s1", "waiting", "1", "needs", needs, "open", "1")
	}
	return recs
}

// bulkAdmit is n admissions, w0 to w<n-1>, each waiting for the one need, as
// an add of n ids with --needs <need> is planned: one create entry with the
// needs and the open the builder read, and a waitfor a card.
func bulkAdmit(n int, need, open string) ([]tset.Entry, []Intent) {
	e := tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting", Set: map[string]string{"kind": "work", "needs": need, "open": open}}
	intents := make([]Intent, n)
	for i := 0; i < n; i++ {
		id := "w" + strconv.Itoa(i)
		e.IDs = append(e.IDs, id)
		e.Scores = append(e.Scores, strconv.Itoa(1000+i))
		e.About = append(e.About, id)
		intents[i] = Intent{Kind: IntentWaitFor, Card: id, Needs: sprint.Split(need)}
	}
	return []tset.Entry{e}, intents
}

// runDerive is the derive phase over a fixture world; the world is returned to
// read what was asked of it.
func runDerive(sc diffScenario) (derivePlan, *Refusal, *fixWorld) {
	ks := newKeyspace()
	ks.apply(sc.keys)
	world := &fixWorld{recs: sc.recs, entryList: sc.entries}
	plan, ref := deriveOver(fixState(ks, sc.now), world, sc.intents)
	return plan, ref, world
}

// nowMS is the call's time the scenarios of this file use.
func nowMS() string { return strconv.FormatInt(testTime.UnixMilli(), 10) }

// TestWalkWorkIsLinearInTheReachableCards: admissions over one chain of 2,000
// open waiting cards expand each card once, however many admissions there are:
// the work of the walk is the chain's (2,000 expansions, 1,999 needs looked at)
// for 1 admission and for 2,000, the records asked of the store are the
// chain's and no more, and the index of the request's entries is read once.
func TestWalkWorkIsLinearInTheReachableCards(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 10, 2000} {
		t.Run(fmt.Sprintf("%d admissions", n), func(t *testing.T) {
			t.Parallel()
			entries, intents := bulkAdmit(n, "c0", "1")
			plan, ref, world := runDerive(diffScenario{now: nowMS(), recs: chainRecs(2000), entries: entries, intents: intents})
			if ref != nil {
				t.Fatalf("refused: %v", ref)
			}
			want := deriveWork{Expanded: 2000, EdgesSeen: 1999, Indexed: n}
			if plan.Work != want {
				t.Fatalf("work %+v, want %+v: each of the 2,000 cards expanded once and each of the 1,999 needs looked at once, whatever the admissions", plan.Work, want)
			}
			if world.asked != 2000 {
				t.Fatalf("%d records asked of the store, want the chain's 2000", world.asked)
			}
			if wantCmds := (n + maxPieces - 1) / maxPieces; len(plan.Cmds) != wantCmds {
				t.Fatalf("%d commands, want %d ZADD in pieces of at most %d", len(plan.Cmds), wantCmds, maxPieces)
			}
		})
	}

	t.Run("every edge is looked at once", func(t *testing.T) {
		t.Parallel()
		// a card that names the chain's head and a card inside it, and a diamond:
		// 1 expansion a card reached and 1 look at each of its needs.
		recs := chainRecs(100)
		recs["d"] = fix("s1", "waiting", "1", "needs", "c0,c50,c99", "open", "3")
		entries, intents := bulkAdmit(300, "d,c0,c50", "3")
		plan, ref, _ := runDerive(diffScenario{now: nowMS(), recs: recs, entries: entries, intents: intents})
		if ref != nil {
			t.Fatalf("refused: %v", ref)
		}
		// d expands once (3 needs); c0..c99 once (99 chain edges).
		if want := (deriveWork{Expanded: 101, EdgesSeen: 99 + 3, Indexed: 300}); plan.Work != want {
			t.Fatalf("work %+v, want %+v", plan.Work, want)
		}
	})
}

// TestRequestIsIndexedOncePerStep: the ids of the request's entries are taken
// into the index once, whatever the number of intents that look a card up:
// the same entries with one waitfor and with 2,000 are indexed alike, and an
// entry of another table, or of a kind that names no card, is not indexed.
func TestRequestIsIndexedOncePerStep(t *testing.T) {
	t.Parallel()
	entries, intents := bulkAdmit(2000, "c0", "1")
	entries = append(entries,
		tset.Entry{Kind: "guard", Table: sprint.Work, IDs: []string{"g1", "g2", "g3", "g4", "g5"}},
		tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:ready", To: "s1:waiting", IDs: []string{"m1", "m2", "m3"}},
		tset.Entry{Kind: "create", Table: sprint.Fleet, To: "s1:ready", IDs: []string{"f1", "f2", "f3", "f4", "f5", "f6", "f7"}},
		// a rows entry names no card, whatever ids it carries (Layer 1 refuses them; the index never reads them)
		tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s9"}, IDs: []string{"r1", "r2"}})
	const indexed = 2000 + 5 + 3
	for _, k := range []int{1, 2000} {
		plan, ref, _ := runDerive(diffScenario{now: nowMS(), recs: chainRecs(3), entries: entries, intents: intents[:k]})
		if ref != nil {
			t.Fatalf("%d intents refused: %v", k, ref)
		}
		if plan.Work.Indexed != indexed {
			t.Fatalf("%d intents: %d ids indexed, want %d, once", k, plan.Work.Indexed, indexed)
		}
	}
}

// TestWalkMemoFindsACycleThroughAnExpandedCard: a card one admission's walk has
// expanded answers for every later admission. w1 and w2 both need c, and c
// (which exists) needs w2, which the step creates: w1's walk reaches c and w2
// and finds no cycle through w1, and w2's walk then meets only cards already
// expanded; the cycle w2 needs c needs w2 must still be found, by what the
// first walk learned of c and w2. A walk that only skipped the cards it had
// seen would admit it.
func TestWalkMemoFindsACycleThroughAnExpandedCard(t *testing.T) {
	t.Parallel()
	sc := diffScenario{now: nowMS(), recs: map[string]fixRec{"c": fix("s1", "waiting", "1", "needs", "w2", "open", "1")}}
	e1, i1 := admit("w1", "c", "1")
	e2, i2 := admit("w2", "c", "1")
	sc.entries, sc.intents = append(e1, e2...), []Intent{i1, i2}
	_, ref, _ := runDerive(sc)
	if ref == nil || ref.Code != CodeXGuard || ref.Message != "c needs w2; a needs cycle; nothing was changed" ||
		!reflect.DeepEqual(ref.Detail.IDs, []string{"c", "w2"}) {
		t.Fatalf("refused %v; want XGUARD naming the cycle c needs w2", ref)
	}
	// The admission that is not on the cycle is admitted alone.
	sc.entries, sc.intents = e1, []Intent{i1}
	if _, ref, _ := runDerive(sc); ref != nil {
		t.Fatalf("w1 alone refused: %v", ref)
	}
}

// TestWalkChainNamesTheFirstPathInTheOrderOfTheNeeds: a cycle reachable by two
// paths is named through the first of the needs, in the order the card names
// them: n needs a and b, which both need w; w needs n; the chain is n, a, w.
func TestWalkChainNamesTheFirstPathInTheOrderOfTheNeeds(t *testing.T) {
	t.Parallel()
	recs := map[string]fixRec{
		"n": fix("s1", "waiting", "1", "needs", "a,b", "open", "2"),
		"a": fix("s1", "waiting", "1", "needs", "w", "open", "1"),
		"b": fix("s1", "waiting", "1", "needs", "w", "open", "1"),
	}
	e, in := admit("w", "n", "1")
	_, ref, _ := runDerive(diffScenario{now: nowMS(), recs: recs, entries: e, intents: []Intent{in}})
	if ref == nil || ref.Message != "n needs w through a; a needs cycle; nothing was changed" || !reflect.DeepEqual(ref.Detail.IDs, []string{"n", "a", "w"}) {
		t.Fatalf("refused %v; want the chain n, a, w", ref)
	}
	// The order of the needs decides it: the same graph with b named first is named through b.
	recs["n"] = fix("s1", "waiting", "1", "needs", "b,a", "open", "2")
	_, ref, _ = runDerive(diffScenario{now: nowMS(), recs: recs, entries: e, intents: []Intent{in}})
	if ref == nil || ref.Message != "n needs w through b; a needs cycle; nothing was changed" || !reflect.DeepEqual(ref.Detail.IDs, []string{"n", "b", "w"}) {
		t.Fatalf("refused %v; want the chain n, b, w", ref)
	}
}

// mixedTree is a needs tree of exactly n cards that a walk from its root r0
// looks up: r0, 40 middle cards that wait, and leaves of every other kind in
// turn (landed, ready, no record, a record with no place). Only the middle
// cards go on; every card counts toward the walk's bound. The leaf list of
// a middle card is given by leaves(i), where i is the middle card's number.
func mixedTree(n int, extra func(mid int, leaves []string) []string) map[string]fixRec {
	const mids = 40
	recs := map[string]fixRec{}
	leafOf := make([][]string, mids)
	for j := 0; j < n-1-mids; j++ {
		id := "x" + strconv.Itoa(j)
		switch j % 4 {
		case 0:
			recs[id] = fix("s1", "landed", "1")
		case 1:
			recs[id] = fix("s1", "ready", "1")
		case 2: // no record
		default:
			recs[id] = fix("", "", "1")
		}
		leafOf[j%mids] = append(leafOf[j%mids], id)
	}
	var midIDs []string
	for i := 0; i < mids; i++ {
		id := "m" + strconv.Itoa(i)
		midIDs = append(midIDs, id)
		leaves := leafOf[i]
		if extra != nil {
			leaves = extra(i, leaves)
		}
		recs[id] = fix("s1", "waiting", "1", "needs", strings.Join(leaves, ","), "open", "1")
	}
	recs["r0"] = fix("s1", "waiting", "1", "needs", strings.Join(midIDs, ","), "open", strconv.Itoa(mids))
	return recs
}

// TestWalkBoundCountsEveryRecordLookedUp: the walk's 2,000 records are every
// card it looks up, whatever the card turns out to be. A tree of 2,000 cards of
// which 41 wait and the rest are landed, ready, without a record or without a
// place is admitted; one more card, of any of those kinds, and it is refused
// naming the head r0. A bound that counted only the waiting cards would admit
// both.
func TestWalkBoundCountsEveryRecordLookedUp(t *testing.T) {
	t.Parallel()
	e, in := admit("w", "r0", "1")
	at := func(n int) (derivePlan, *Refusal) {
		plan, ref, _ := runDerive(diffScenario{now: nowMS(), recs: mixedTree(n, nil), entries: e, intents: []Intent{in},
			keys: []Cmd{hset("clock", "stopped_ms", "0")}})
		return plan, ref
	}
	plan, ref := at(WalkRecordsMax)
	if ref != nil || plan.Work.Expanded != WalkRecordsMax {
		t.Fatalf("a tree of 2,000: refused %v, expanded %d; want it admitted with 2,000 expansions", ref, plan.Work.Expanded)
	}
	_, ref = at(WalkRecordsMax + 1)
	want := "the needs walk from r0 went past 2000 records; a needs cycle could not be ruled out; nothing was changed"
	if ref == nil || ref.Code != CodeXGuard || ref.Message != want || !reflect.DeepEqual(ref.Detail.IDs, []string{"r0"}) ||
		ref.Detail.Limit == nil || *ref.Detail.Limit != 2000 || ref.Detail.Actual == nil || *ref.Detail.Actual != 2001 {
		t.Fatalf("a tree of 2,001: refused %v; want the bound, naming r0", ref)
	}
}

// TestWalkCycleAndBoundRaceOnOneWalk: a cycle and the bound on one walk, the one
// the depth-first search meets first is the refusal. w needs r0, whose middle
// cards m0 to m39 are searched in order; a middle card that needs w closes the
// cycle where the search first expands it.
func TestWalkCycleAndBoundRaceOnOneWalk(t *testing.T) {
	t.Parallel()
	e, in := admit("w", "r0", "1")
	cyclesAt := func(mid int) (*Refusal, deriveWork) {
		recs := mixedTree(WalkRecordsMax+200, func(i int, leaves []string) []string {
			if i == mid {
				return append(append([]string{}, leaves...), "w")
			}
			return leaves
		})
		plan, ref, _ := runDerive(diffScenario{now: nowMS(), recs: recs, entries: e, intents: []Intent{in}})
		return ref, plan.Work
	}
	// The first middle card names w after its leaves: the search is within the bound.
	ref, _ := cyclesAt(0)
	if ref == nil || ref.Code != CodeXGuard || !strings.HasPrefix(ref.Message, "r0 needs w through m0; a needs cycle") {
		t.Fatalf("a cycle through the first middle card: refused %v; want the cycle", ref)
	}
	// The last one is searched after more than 2,000 cards: the bound is met first.
	ref, _ = cyclesAt(39)
	if ref == nil || ref.Code != CodeXGuard || !strings.HasPrefix(ref.Message, "the needs walk from r0 went past 2000 records") {
		t.Fatalf("a cycle through the last middle card: refused %v; want the bound", ref)
	}
}

// randomCycleWorld draws a small world for the cycle reference: existing cards
// that wait (open counted from their needs), landed, ready, dropped or absent,
// and up to four admissions, with needs among all of them. Nothing else can be
// refused: every create entry carries the open its needs give.
func randomCycleWorld(rng *rand.Rand) diffScenario {
	pool := []string{"c0", "c1", "c2", "c3", "c4", "c5"}
	fresh := []string{"n0", "n1", "n2", "n3"}
	ghosts := []string{"g0", "g1"}
	all := append(append(append([]string{}, pool...), fresh...), ghosts...)
	pick := func(from []string) string { return from[rng.IntN(len(from))] }
	some := func(from []string, lo, hi int) []string {
		n := lo + rng.IntN(hi-lo+1)
		var out []string
		for i := 0; i < n; i++ {
			out = append(out, pick(from))
		}
		return dedupIDs(out)
	}
	sc := diffScenario{now: nowMS(), recs: map[string]fixRec{}, keys: []Cmd{hset("clock", "stopped_ms", "0")}}
	made := map[string]bool{}
	var admitted []string
	for i, n := 0, 1+rng.IntN(len(fresh)); i < n; i++ {
		admitted = append(admitted, fresh[i])
		made[fresh[i]] = true
	}
	state := map[string]string{}
	for _, id := range pool {
		switch roll := rng.IntN(10); {
		case roll < 6:
			state[id] = "waiting"
		case roll < 7:
			state[id] = "landed"
		case roll < 8:
			state[id] = "ready"
		case roll < 9:
			state[id] = "dropped"
		default:
			state[id] = "absent"
		}
	}
	unmet := func(n string) bool { return made[n] || state[n] != "landed" }
	for _, id := range pool {
		switch state[id] {
		case "waiting":
			needs := slicesDeleteValue(some(all, 1, 3), id)
			if len(needs) == 0 {
				needs = []string{"g0"}
			}
			open := 0
			for _, n := range needs {
				if unmet(n) {
					open++
				}
			}
			sc.recs[id] = fix("s1", "waiting", "1", "needs", strings.Join(needs, ","), "open", strconv.Itoa(open))
		case "landed":
			sc.recs[id] = fix("s1", "landed", "1")
		case "ready":
			sc.recs[id] = fix("s1", "ready", "1")
		case "dropped":
			sc.recs[id] = fix("", "", "1")
		}
	}
	for _, id := range admitted {
		needs := slicesDeleteValue(some(all, 1, 3), id)
		if len(needs) == 0 {
			needs = []string{"g1"}
		}
		open := 0
		for _, n := range needs {
			if unmet(n) {
				open++
			}
		}
		e := tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{id}, Scores: []string{"50"},
			Set: map[string]string{"needs": strings.Join(needs, ","), "open": strconv.Itoa(open)}, About: []string{id}}
		sc.entries = append(sc.entries, e)
		sc.intents = append(sc.intents, Intent{Kind: IntentWaitFor, Card: id, Needs: needs})
	}
	return sc
}

// referenceCycle is the cycle check of the first version of this phase, kept as
// the oracle of the test below: for each admission in order, for each of its
// needs in order, a depth-first search of its own from the need, through the
// needs of the cards the step admits and of the open waiting cards, looking for
// the admitted card. The search is quadratic in the admissions; it is right by
// construction and the phase's walk is held to it. It gives the chain of the
// first need that reaches its waiter, or nil.
func referenceCycle(sc diffScenario) []string {
	own := map[string][]string{}
	for _, it := range sc.intents {
		own[it.Card] = dedupIDs(it.Needs)
	}
	edges := func(c string) []string {
		if needs, ok := own[c]; ok {
			return needs
		}
		r, ok := sc.recs[c]
		if !ok || r.col != string(sprint.Waiting) {
			return nil
		}
		if open, _ := strconv.Atoi(r.fields["open"]); open == 0 {
			return nil
		}
		return sprint.Split(r.fields["needs"])
	}
	for _, it := range sc.intents {
		w := it.Card
		for _, start := range dedupIDs(it.Needs) {
			if start == w {
				return []string{start, w}
			}
			parent := map[string]string{start: ""}
			stack := []string{start}
			for len(stack) > 0 {
				c := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				es := edges(c)
				for i := len(es) - 1; i >= 0; i-- {
					e := es[i]
					if _, seen := parent[e]; seen {
						continue
					}
					parent[e] = c
					if e == w {
						chain := []string{w}
						for p := parent[w]; p != ""; p = parent[p] {
							chain = append(chain, p)
						}
						for a, b := 0, len(chain)-1; a < b; a, b = a+1, b-1 {
							chain[a], chain[b] = chain[b], chain[a]
						}
						return chain
					}
					stack = append(stack, e)
				}
			}
		}
	}
	return nil
}

// TestWalkVerdictEqualsTheQuadraticReference: on 20,000 small random worlds the
// phase's single walk refuses a cycle exactly when the first version's
// search-per-need does, and names the same chain. The worlds have cycles among
// the cards a step admits and through existing cards, cards that two admissions
// both reach, and cards no admission reaches.
func TestWalkVerdictEqualsTheQuadraticReference(t *testing.T) {
	t.Parallel()
	const chunks, per = 4, 5000
	results := make(chan [2]int, chunks)
	for chunk := 0; chunk < chunks; chunk++ {
		t.Run("chunk "+strconv.Itoa(chunk), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewPCG(uint64(chunk)+1, 0xc1c1e))
			var cycles, clear int
			for i := 0; i < per; i++ {
				sc := randomCycleWorld(rng)
				want := referenceCycle(sc)
				_, ref, _ := runDerive(sc)
				if want == nil {
					clear++
					if ref != nil {
						t.Fatalf("world %d: the reference finds no cycle, the phase refused %v\nrecs %v\nintents %+v", i, ref, sc.recs, sc.intents)
					}
					continue
				}
				cycles++
				if ref == nil || ref.Code != CodeXGuard || !strings.Contains(ref.Message, "a needs cycle") || !reflect.DeepEqual(ref.Detail.IDs, want) {
					t.Fatalf("world %d: the reference names the chain %v, the phase %v\nrecs %v\nintents %+v", i, want, ref, sc.recs, sc.intents)
				}
			}
			results <- [2]int{cycles, clear}
		})
	}
	t.Cleanup(func() {
		close(results)
		var cycles, clear int
		for r := range results {
			cycles, clear = cycles+r[0], clear+r[1]
		}
		if cycles < 2000 || clear < 2000 {
			t.Errorf("the draw had %d worlds with a cycle and %d without; want at least 2,000 of each", cycles, clear)
		}
	})
}

// manyWaiters is a needmet scenario: n cards that wait for the landed need n,
// all in wait:n, and the intent that names them.
func manyWaiters(count int, quarantine bool) diffScenario {
	sc := diffScenario{now: nowMS(), recs: map[string]fixRec{"n": fix("s1", "landed", "4")}}
	var pairs, ids []string
	for i := 0; i < count; i++ {
		id := "w" + strconv.Itoa(i)
		sc.recs[id] = fix("s1", "waiting", "1", "needs", "n", "open", "1")
		pairs = append(pairs, "0", id)
		ids = append(ids, id)
	}
	for first := 0; first < len(pairs); first += 2 * maxPieces {
		sc.keys = append(sc.keys, zadd("wait:n@0", pairs[first:min(first+2*maxPieces, len(pairs))]...))
	}
	if quarantine {
		sc.keys = append(sc.keys, hset("quarantine@0", "someone", "DRIFT"))
	}
	sc.intents = []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: ids}}
	return sc
}

// waiveWorld is a waive scenario: cards v0, v1, ... each wait for every need of
// needs and have each of the judgments of judgments open on each need (J's note
// for it). The waive intents name the needs perIntent at a time, one intent for
// each such slice of each card (0 means all the needs in one), and each slice is
// named by repeat intents (0 is one). A need that has the judgment "missing"
// open has no record and is in missing, and every card is in its wait set; the
// judgment "dropped" has neither (needgone took the cards out of the set), and
// with both open on a need the dropped one is the one the waive takes.
type waiveWorld struct {
	cards      int
	needs      []string
	judgments  []string
	perIntent  int
	repeat     int
	quarantine bool // every card is in the quarantine
}

func (w waiveWorld) scenario() diffScenario {
	sc := diffScenario{now: nowMS(), recs: map[string]fixRec{}, keys: []Cmd{hset("clock", "stopped_ms", "0")}}
	needsField, open := strings.Join(w.needs, ","), strconv.Itoa(len(w.needs))
	missingOnly := len(w.judgments) == 1 && w.judgments[0] == sprint.NMissingNeed
	var ids, held []string
	for i := 0; i < w.cards; i++ {
		id := "v" + strconv.Itoa(i)
		ids = append(ids, id)
		held = append(held, id, "DRIFT")
		sc.recs[id] = fix("s1", "waiting", "1", "needs", needsField, "open", open)
		var judged []string
		for _, n := range w.needs {
			for _, typ := range w.judgments {
				judged = append(judged, typ+"|"+n, "n"+strconv.Itoa(i))
			}
		}
		sc.keys = append(sc.keys, hset("jopen:"+id+"@0", judged...))
	}
	if w.quarantine {
		sc.keys = append(sc.keys, hset("quarantine@0", held...))
	}
	if missingOnly {
		var pairs []string
		for _, id := range ids {
			pairs = append(pairs, "0", id)
		}
		for _, n := range w.needs {
			for first := 0; first < len(pairs); first += 2 * maxPieces {
				sc.keys = append(sc.keys, zadd("wait:"+n+"@0", pairs[first:min(first+2*maxPieces, len(pairs))]...))
			}
			sc.keys = append(sc.keys, zadd("missing@0", "5", n))
		}
	}
	per := w.perIntent
	if per == 0 {
		per = len(w.needs)
	}
	for _, id := range ids {
		for first := 0; first < len(w.needs); first += per {
			for range max(w.repeat, 1) {
				sc.intents = append(sc.intents, Intent{Kind: IntentWaive, Card: id, Needs: w.needs[first:min(first+per, len(w.needs))]})
			}
		}
	}
	return sc
}

// needNames is n ids g0, g1, ...
func needNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "g" + strconv.Itoa(i)
	}
	return out
}

// TestDeriveStoreProbesAreBatchedByKey: the Lua reads the sprint's own keys a
// key at a time, in pieces of at most 1,000, and never a card at a time: the
// store commands it sends are pinned by count, on scenarios the Go phase holds
// equal (work counts and all). 2,000 admissions that wait for one card ask
// wait:<n> twice; for one ghost, wait:<n> twice and missing once; a needmet of
// 2,000 waiters asks wait:<n> twice and the quarantine twice, or once at all
// (HLEN) when nothing is quarantined. The waive asks the quarantine once for
// every card, wait:<n> once a key for every card that waives a need with no
// record (2,000 cards of one ghost: twice), and the judgments open once a card
// (one HMGET of its jopen key for both types of every need it names, in pieces
// of 1,000 fields), never a probe a need: 2,000 waives of one ghost are 2,005
// store commands, where a probe a need was 6,003.
func TestDeriveStoreProbesAreBatchedByKey(t *testing.T) {
	t.Parallel()
	waives := diffScenario{now: nowMS(), recs: map[string]fixRec{}, keys: []Cmd{hset("quarantine@0", "someone", "DRIFT")}}
	for i := 0; i < 30; i++ {
		id := "v" + strconv.Itoa(i)
		waives.recs[id] = fix("s1", "waiting", "1", "needs", "run", "open", "1")
		waives.intents = append(waives.intents, Intent{Kind: IntentWaive, Card: id, Needs: []string{"run"}})
	}
	bulkCard, bulkCardIntents := bulkAdmit(2000, "c0", "1")
	bulkGhost, bulkGhostIntents := bulkAdmit(2000, "ghost", "1")
	missing, dropped := sprint.NMissingNeed, sprint.NBlocked
	cases := []struct {
		name    string
		sc      diffScenario
		reads   map[string]int
		before  int  // ids asked of S.before
		decides bool // the step writes something; false is a step of no-ops
	}{
		{"2,000 admissions that wait for one card", diffScenario{now: nowMS(), recs: chainRecs(50), entries: bulkCard, intents: bulkCardIntents},
			map[string]int{"ZMSCORE": 2}, 50, true},
		{"2,000 admissions that wait for one ghost", diffScenario{now: nowMS(), keys: []Cmd{hset("clock", "stopped_ms", "0")}, entries: bulkGhost, intents: bulkGhostIntents},
			map[string]int{"ZMSCORE": 3, "HMGET": 1}, 1, true},
		{"a needmet of 2,000 waiters, something quarantined", manyWaiters(2000, true),
			map[string]int{"ZMSCORE": 2, "HLEN": 1, "HMGET": 2}, 2000, true},
		{"a needmet of 2,000 waiters, nothing quarantined", manyWaiters(2000, false),
			map[string]int{"ZMSCORE": 2, "HLEN": 1}, 2000, true},
		{"30 waives with no judgment open", waives, map[string]int{"HLEN": 1, "HMGET": 31}, 30, false},
		{"2,000 waives of one ghost", waiveWorld{cards: 2000, needs: []string{"ghost"}, judgments: []string{missing}}.scenario(),
			map[string]int{"HLEN": 1, "HMGET": 2000, "ZMSCORE": 2, "ZCARD": 1, "ZSCORE": 1}, 2001, true},
		{"100 waives of 8 ghosts each", waiveWorld{cards: 100, needs: needNames(8), judgments: []string{missing}}.scenario(),
			map[string]int{"HLEN": 1, "HMGET": 100, "ZMSCORE": 8, "ZCARD": 8, "ZSCORE": 8}, 108, true},
		{"50 waives of a dropped need that also has the missing judgment open", waiveWorld{cards: 50, needs: []string{"d"}, judgments: []string{dropped, missing}}.scenario(),
			map[string]int{"HLEN": 1, "HMGET": 50}, 50, true},
		{"one card waived by 20 intents of 64 needs", waiveWorld{cards: 1, needs: needNames(1280), judgments: []string{dropped}, perIntent: 64}.scenario(),
			map[string]int{"HLEN": 1, "HMGET": 3}, 1, true},
		{"one card waived 20 times, by intents that name the same 64 needs", waiveWorld{cards: 1, needs: needNames(64), judgments: []string{dropped}, repeat: 20}.scenario(),
			map[string]int{"HLEN": 1, "HMGET": 1}, 1, true},
		{"2,000 waives of quarantined cards", waiveWorld{cards: 2000, needs: []string{"ghost"}, judgments: []string{missing}, quarantine: true}.scenario(),
			map[string]int{"HLEN": 1, "HMGET": 2}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g := goOutcome(t, c.sc)
			rig := newLuaRig(t)
			l := rig.run(c.sc)
			if !reflect.DeepEqual(nilIfEmpty(g), nilIfEmpty(l)) {
				t.Fatalf("Go and Lua decided differently (Go refusal %v, Lua refusal %v)", g.Refusal, l.Refusal)
			}
			if decided := nonEmpty(g.Entries) || nonEmpty(g.Cmds); decided != c.decides {
				t.Fatalf("the step writes something: %v, want %v (refusal %v)", decided, c.decides, g.Refusal)
			}
			if !reflect.DeepEqual(rig.reads, c.reads) {
				t.Errorf("the Lua sent the commands %v, want %v", rig.reads, c.reads)
			}
			if rig.before != c.before {
				t.Errorf("the Lua asked S.before for %d ids, want %d", rig.before, c.before)
			}
		})
	}
}

// TestNoteNamesASubjectOnce: a request to J names each subject of its step once,
// however many times the phase asks it, and the ones of one operation, type and
// cause are a single request in the order they were first named (1.3.4). The
// subjects are kept beside a set, so that a request of 2,000 subjects is 2,000
// set reads and not 2,000 scans of a list.
func TestNoteNamesASubjectOnce(t *testing.T) {
	t.Parallel()
	st := &State{Prefix: testPrefix, Epoch: "0", NowMS: "1", Names: testNames, Keys: &Keys{ks: newKeyspace()}}
	r := newDeriveRun(st, &fixWorld{})
	for i := 0; i < 3; i++ {
		r.note("open", sprint.NBlocked, "d", "w1")
		r.note("open", sprint.NBlocked, "d", "w2")
		r.note("close", sprint.NBlocked, "d", "w1")
	}
	if len(r.notes) != 2 || !reflect.DeepEqual(r.notes[0].subjects, []string{"w1", "w2"}) || !reflect.DeepEqual(r.notes[1].subjects, []string{"w1"}) {
		t.Fatalf("notes %+v %+v; want an open of w1 and w2 once each and a close of w1", r.notes[0], r.notes[1])
	}
}
