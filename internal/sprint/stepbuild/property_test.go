package stepbuild

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The property test: random inputs, each cut, each step counted from its own
// encoded request by the measure of helpers_test.go (an accounting that does
// not use the builder's), and the steps read back against the input.
//
// For every input:
//   - every step is inside every bound (the request counted from its bytes);
//   - the members of the steps, in step order, are the input's members in
//     order: none lost, none repeated, each with its own score, revision,
//     fields and about, and each wire entry carrying its entry's shared
//     fields whole;
//   - each entry's guards are in every step that holds a part of it, once,
//     ahead of the first part, and in no other step;
//   - notes are in input order, after every part of their entry;
//   - no member is named twice in a step;
//   - every step is as full as the bounds let it be: the first piece of the
//     next step, added to it, would break a bound or name a member it names;
//   - a build of the same input again gives equal steps and equal bytes;
//   - parts, totals and cursors are consistent, and After resumes from each.
//
// The bounds are the contract's scaled down (smallBounds) until an input of
// a few dozen members crosses each of them; slow_test.go runs the same at
// the contract's own numbers.

// smallBounds is a bound set every one of which a random input reaches.
func smallBounds(ident bool) Bounds {
	argv := 9000
	if ident {
		argv += LimitReceiptBytes
	}
	return Bounds{
		RequestBytes: 5000, Entries: 6, Tables: 3, Candidates: 40, GuardOnly: 60, EntryIDs: 25, RowPairs: 10,
		Notes: 4, AboutIDs: 50, FieldObservations: 250, LineBytes: 3000, LineIDs: 20, PlannedArgvBytes: argv,
	}
}

// gen makes random inputs from one seeded source.
type gen struct{ r *rand.Rand }

var (
	genTables = []string{"work", "merge", "fleet", "readers", "extra"}
	genAlpha  = []rune("abcXYZ019_-. :\"\\\n\té世")
	genScores = []string{"1", "2.5", "-3e2", "0", "1e-3", "0.000000000000000000000000000001", "123456789012345678901234567890"}
)

func (g gen) str(max int) string {
	n := g.r.IntN(max + 1)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteRune(genAlpha[g.r.IntN(len(genAlpha))])
	}
	return sb.String()
}

// pick n distinct names "<prefix><i>" from the first pool of them.
func (g gen) pick(prefix string, n, pool int) []string {
	out := make([]string, n)
	for i, k := range g.r.Perm(pool)[:n] {
		out[i] = prefix + strconv.Itoa(k)
		if k%11 == 0 {
			out[i] += "\"q\\"
		}
	}
	return out
}

func (g gen) fields(max int) map[string]string {
	m := map[string]string{}
	for i, n := 0, g.r.IntN(max+1); i < n; i++ {
		m["f"+strconv.Itoa(g.r.IntN(8))] = g.str(24)
	}
	return m
}

func (g gen) names(prefix string, max int) []string {
	n := g.r.IntN(max + 1)
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + strconv.Itoa(g.r.IntN(6))
	}
	return out
}

func (g gen) coin(percent int) bool { return g.r.IntN(100) < percent }

func (g gen) table() string { return genTables[g.r.IntN(len(genTables))] }

func (g gen) cell() string { return g.str(6) + "r:" + []string{"todo", "done", "a:b"}[g.r.IntN(3)] }

func (g gen) notes() []Note {
	if !g.coin(30) {
		return nil
	}
	out := make([]Note, 1+g.r.IntN(2))
	for i := range out {
		out[i] = Note{About: g.pick("p", g.r.IntN(11), 12)}
		if g.coin(70) {
			out[i].Meta = g.fields(2)
		}
	}
	return out
}

func (g gen) guards() []Entry {
	if !g.coin(22) {
		return nil
	}
	out := make([]Entry, 1+g.r.IntN(2))
	pool := g.pick("g", 8, 12) // distinct across the owner's guards
	at := 0
	for i := range out {
		n := 1 + g.r.IntN(3)
		e := Entry{Kind: KindGuard, Table: g.table(), From: g.cell(), IDs: pool[at : at+n]}
		at += n
		if g.coin(40) {
			e.Revs = make([]string, n)
			for j := range e.Revs {
				e.Revs[j] = strconv.FormatUint(g.r.Uint64(), 10)
			}
		}
		if g.coin(30) {
			e.BeforeFields = g.names("b", 3)
		}
		out[i] = e
	}
	return out
}

func (g gen) change(kind Kind) Entry {
	n := 1 + g.r.IntN(30)
	if g.coin(8) {
		n = 30 + g.r.IntN(25)
	}
	e := Entry{Kind: kind, Table: g.table(), From: g.cell(), IDs: g.pick("id-", n, 60)}
	if kind == KindCreate {
		e.From = ""
	}
	if kind == KindCreate || (kind == KindMove && g.coin(50)) {
		e.To = g.cell()
	}
	if kind == KindCreate || (kind == KindMove && g.coin(40)) {
		e.Scores = make([]string, n)
		for i := range e.Scores {
			e.Scores[i] = genScores[g.r.IntN(len(genScores))]
		}
	}
	if kind != KindCreate && g.coin(35) {
		e.Revs = make([]string, n)
		for i := range e.Revs {
			e.Revs[i] = strconv.FormatUint(g.r.Uint64(), 10)
		}
	}
	if g.coin(55) {
		e.Set = g.fields(3)
	}
	if g.coin(45) {
		e.Each = make([]map[string]string, n)
		for i := range e.Each {
			if g.coin(70) {
				e.Each[i] = g.fields(3)
			}
		}
	}
	if kind != KindCreate && g.coin(30) {
		e.Unset = g.names("u", 3)
	}
	if g.coin(30) {
		e.BeforeFields = g.names("b", 3)
	}
	if g.coin(50) {
		e.About = g.pick("p", n, 60)
		for i := range e.About {
			e.About[i] = "p" + strconv.Itoa(g.r.IntN(12))
		}
	}
	if g.coin(30) {
		e.Meta = g.fields(2)
	}
	return e
}

func (g gen) rows() Entry {
	a, d := g.r.IntN(26), g.r.IntN(26)
	if a+d == 0 {
		a = 1
	}
	all := g.pick("row-", a+d, 60)
	e := Entry{Kind: KindRows, Table: g.table()}
	if a > 0 {
		e.Add = append([]string(nil), all[:a]...)
		if g.coin(20) {
			e.Add = append(e.Add, e.Add[0])
		}
	}
	if d > 0 {
		e.Del = all[a:]
	}
	return e
}

func (g gen) entry() Entry {
	var e Entry
	switch p := g.r.IntN(100); {
	case p < 17:
		e = g.change(KindCreate)
	case p < 42:
		e = g.change(KindMove)
	case p < 52:
		e = g.change(KindRemove)
	case p < 64:
		e = Entry{Kind: KindGuard, Table: g.table(), From: g.cell(), IDs: g.pick("id-", 1+g.r.IntN(30), 60)}
		if g.coin(40) {
			e.BeforeFields = g.names("b", 3)
		}
		return e
	case p < 82:
		e = g.rows()
	default:
		return Entry{Kind: KindNote, Notes: append(g.notes(), Note{About: g.pick("p", g.r.IntN(11), 12)})}
	}
	e.Guards, e.Notes = g.guards(), g.notes()
	return e
}

func (g gen) input() []Entry {
	out := make([]Entry, 1+g.r.IntN(14))
	for i := range out {
		out[i] = g.entry()
	}
	return out
}

// identFor is a step identity that differs per part, of varying size.
func identFor(part int) Ident {
	return Ident{Op: "op-" + strconv.Itoa(part), Intent: "i" + strings.Repeat("i", part%13), Result: strings.Repeat("r", part%5)}
}

// check runs every property on one input.
func check(t testing.TB, seed uint64, bd Bounds, ident, full bool, in []Entry) []Step {
	t.Helper()
	c := cfg()
	c.Bounds = bd
	if ident {
		c.Ident = identFor
	}
	steps, err := Build(c, in)
	if err != nil {
		t.Fatalf("seed %d: Build refused: %v", seed, err)
	}
	fail := func(f string, a ...any) {
		t.Helper()
		t.Fatalf("seed %d: %s", seed, fmt.Sprintf(f, a...))
	}
	for i, s := range steps {
		raw := s.Encode()
		if s.Bytes != len(raw) || s.Part != i+1 || s.Parts != len(steps) {
			fail("step %d: Bytes %d encoded %d, part %d of %d", i+1, s.Bytes, len(raw), s.Part, s.Parts)
		}
		if full {
			if bad := measure(t, raw).within(bd); len(bad) > 0 {
				fail("step %d is outside its bounds: %v", i+1, bad)
			}
		} else if bad := measureLite(s, raw).withinLite(bd); len(bad) > 0 {
			fail("step %d is outside its bounds: %v", i+1, bad)
		}
	}
	checkMembers(t, seed, in, steps)
	checkGuardsAndNotes(t, seed, in, steps)
	if full {
		checkFull(t, seed, bd, steps)
	}
	checkResume(t, seed, steps)
	// A second build of the same input: every input under the slow tag, one
	// in four otherwise (the same input is built twice by TestTheSameInput...
	// as well).
	if full || seed%4 == 0 {
		again, err := Build(c, in)
		if err != nil || !reflect.DeepEqual(again, steps) {
			fail("a second build differs: %v", err)
		}
	}
	return steps
}

// checkMembers reads the steps back into the input: per input entry, the
// concatenation of its parts is its members, in order, with its own aligned
// values, and every part carries its shared fields whole.
func checkMembers(t testing.TB, seed uint64, in []Entry, steps []Step) {
	t.Helper()
	last := -1
	parts := map[int][]Entry{}
	for _, s := range steps {
		for _, p := range s.Entries {
			if p.Guard {
				continue
			}
			if p.Source < last {
				t.Fatalf("seed %d: entry %d's part comes after entry %d's", seed, p.Source, last)
			}
			last = p.Source
			parts[p.Source] = append(parts[p.Source], p.Entry)
		}
	}
	for i, e := range in {
		if e.Kind == KindNote {
			if len(parts[i]) != 0 {
				t.Fatalf("seed %d: a note entry has wire entries", seed)
			}
			continue
		}
		var got Entry
		got.Add, got.Del = []string{}, []string{}
		for _, p := range parts[i] {
			want := e
			want.IDs, want.Scores, want.Revs, want.Each, want.About, want.Add, want.Del = p.IDs, p.Scores, p.Revs, p.Each, p.About, p.Add, p.Del
			want.Guards, want.Notes = nil, nil
			pp := p
			if !reflect.DeepEqual(pp, wantWithout(want)) {
				t.Fatalf("seed %d: entry %d: a part does not carry the entry's shared fields whole:\n%+v\n%+v", seed, i, pp, want)
			}
			got.IDs = append(got.IDs, p.IDs...)
			got.Scores = append(got.Scores, p.Scores...)
			got.Revs = append(got.Revs, p.Revs...)
			got.Each = append(got.Each, p.Each...)
			got.About = append(got.About, p.About...)
			got.Add = append(got.Add, p.Add...)
			got.Del = append(got.Del, p.Del...)
		}
		same := func(name string, a, b []string) {
			if len(a) == 0 && len(b) == 0 {
				return
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("seed %d: entry %d: %s across its parts is %v, the input's %v", seed, i, name, a, b)
			}
		}
		same("ids", got.IDs, e.IDs)
		same("scores", got.Scores, e.Scores)
		same("revs", got.Revs, e.Revs)
		same("about", got.About, e.About)
		same("add", got.Add, e.Add)
		same("del", got.Del, e.Del)
		if len(e.Each) != len(got.Each) || (len(e.Each) > 0 && !reflect.DeepEqual(got.Each, e.Each)) {
			t.Fatalf("seed %d: entry %d: each across its parts differs", seed, i)
		}
	}
}

// wantWithout is the entry with the fields a part does not carry cleared.
func wantWithout(e Entry) Entry { e.Guards, e.Notes = nil, nil; return e }

// checkGuardsAndNotes: guards once per step that holds a part, ahead of it;
// notes in input order and after every part of their entry.
func checkGuardsAndNotes(t testing.TB, seed uint64, in []Entry, steps []Step) {
	t.Helper()
	lastPartStep := map[int]int{}
	firstPartStep := map[int]int{}
	for k, s := range steps {
		held := map[int][]Placed{}
		firstPart := map[int]int{}
		for pos, p := range s.Entries {
			switch {
			case p.Guard:
				held[p.Source] = append(held[p.Source], p)
			default:
				lastPartStep[p.Source] = k
				if _, ok := firstPartStep[p.Source]; !ok {
					firstPartStep[p.Source] = k
				}
				if _, ok := firstPart[p.Source]; !ok {
					firstPart[p.Source] = pos
				}
			}
		}
		for src, pos := range firstPart {
			g := in[src].Guards
			if len(held[src]) != len(g) {
				t.Fatalf("seed %d: step %d holds %d of entry %d's %d guards", seed, k+1, len(held[src]), src, len(g))
			}
			for gi, p := range held[src] {
				if !reflect.DeepEqual(p.Entry, g[gi]) {
					t.Fatalf("seed %d: step %d: guard %d of entry %d is not the entry's", seed, k+1, gi, src)
				}
			}
			for at, p := range s.Entries {
				if p.Guard && p.Source == src && at > pos {
					t.Fatalf("seed %d: step %d: a guard of entry %d comes after its part", seed, k+1, src)
				}
			}
		}
		for src := range held {
			if _, ok := firstPart[src]; !ok {
				t.Fatalf("seed %d: step %d holds guards of entry %d and no part of it", seed, k+1, src)
			}
		}
	}
	var want []PlacedNote
	for i, e := range in {
		for _, n := range e.Notes {
			want = append(want, PlacedNote{Note: n, Source: i})
		}
	}
	var got []PlacedNote
	for k, s := range steps {
		for _, n := range s.Notes {
			got = append(got, n)
			if last, ok := lastPartStep[n.Source]; ok && last > k {
				t.Fatalf("seed %d: a note of entry %d is in step %d, before its last part in step %d", seed, n.Source, k+1, last+1)
			}
			for src, first := range firstPartStep {
				if src > n.Source && first < k {
					t.Fatalf("seed %d: entry %d starts in step %d, before a note of entry %d in step %d", seed, src, first+1, n.Source, k+1)
				}
			}
		}
	}
	if len(want) != len(got) || (len(want) > 0 && !reflect.DeepEqual(want, got)) {
		t.Fatalf("seed %d: the notes across the steps are not the input's, in order: %d, %d", seed, len(got), len(want))
	}
}

// checkFull holds every step to be as full as the bounds let it be: what the
// next step starts with, added to it, would break a bound or name a member it
// names.
func checkFull(t testing.TB, seed uint64, bd Bounds, steps []Step) {
	t.Helper()
	for k := 0; k+1 < len(steps); k++ {
		cur, next := steps[k], steps[k+1]
		hyp := cur
		hyp.Entries = append([]Placed(nil), cur.Entries...)
		hyp.Notes = append([]PlacedNote(nil), cur.Notes...)
		if !addFirstPiece(&hyp, cur, next) {
			continue
		}
		if bad := measure(t, hyp.Encode()).within(bd); len(bad) == 0 {
			t.Fatalf("seed %d: step %d was closed early: the first piece of step %d fits it", seed, k+1, k+2)
		}
	}
}

// addFirstPiece adds to hyp the first piece the builder placed in next: a
// note that led it, else its first member, with the guards of its entry when
// cur does not hold them. It says false when there is nothing to add.
func addFirstPiece(hyp *Step, cur, next Step) bool {
	var first *Placed
	for i := range next.Entries {
		if !next.Entries[i].Guard {
			first = &next.Entries[i]
			break
		}
	}
	if len(next.Notes) > 0 && (first == nil || next.Notes[0].Source < first.Source) {
		hyp.Notes = append(hyp.Notes, next.Notes[0])
		return true
	}
	if first == nil {
		return false
	}
	held := false
	for _, p := range cur.Entries {
		held = held || (p.Guard && p.Source == first.Source)
	}
	if !held {
		for _, p := range next.Entries {
			if p.Guard && p.Source == first.Source {
				hyp.Entries = append(hyp.Entries, p)
			}
		}
	}
	one := first.Entry
	if one.Kind == KindRows {
		if len(one.Add) > 0 {
			one.Add, one.Del = one.Add[:1], nil
		} else {
			one.Add, one.Del = nil, one.Del[:1]
		}
	} else {
		one.IDs = one.IDs[:1]
		if one.Scores != nil {
			one.Scores = one.Scores[:1]
		}
		if one.Revs != nil {
			one.Revs = one.Revs[:1]
		}
		if one.Each != nil {
			one.Each = one.Each[:1]
		}
		if one.About != nil {
			one.About = one.About[:1]
		}
	}
	hyp.Entries = append(hyp.Entries, Placed{Entry: one, Source: first.Source})
	return true
}

// checkResume holds the cursors to strictly increase and After to resume.
func checkResume(t testing.TB, seed uint64, steps []Step) {
	t.Helper()
	for k, s := range steps {
		if k > 0 && !cursorLess(steps[k-1].Cursor, s.Cursor) {
			t.Fatalf("seed %d: cursors do not increase: %+v then %+v", seed, steps[k-1].Cursor, s.Cursor)
		}
		rest, err := After(steps, s.Cursor)
		if err != nil || len(rest) != len(steps)-k-1 {
			t.Fatalf("seed %d: After step %d: %d steps, %v", seed, k+1, len(rest), err)
		}
	}
}

func cursorLess(a, b Cursor) bool {
	if a.Entry != b.Entry {
		return a.Entry < b.Entry
	}
	if a.Done != b.Done {
		return a.Done < b.Done
	}
	return a.Notes < b.Notes
}

// propertyInputs is how many random inputs the property test cuts.
const propertyInputs = 10000

// propertyShards split the run over parallel subtests; fullEvery is how often
// an input gets the full accounting (the JSON model of the line and the argv
// bytes, and the fullness check), the rest getting the lite one. The slow tag
// runs every input fully.
const (
	propertyShards = 8
	fullEvery      = 16
)

func TestPropertyEveryStepIsInsideEveryBoundAndTheWholeIsTheSumOfTheSteps(t *testing.T) {
	t.Parallel()
	var (
		mu                     sync.Mutex
		splits, bigSteps, runs int
	)
	for shard := 0; shard < propertyShards; shard++ {
		t.Run(strconv.Itoa(shard), func(t *testing.T) {
			t.Parallel()
			var mySplits, myBig, myRuns int
			for seed := uint64(shard + 1); seed <= propertyInputs; seed += propertyShards {
				g := gen{rand.New(rand.NewPCG(seed, 0x5eed))}
				ident := seed%2 == 0
				steps := check(t, seed, smallBounds(ident), ident, seed%fullEvery == 0, g.input())
				myRuns++
				if len(steps) > 1 {
					mySplits++
				}
				if len(steps) > 3 {
					myBig++
				}
			}
			mu.Lock()
			splits, bigSteps, runs = splits+mySplits, bigSteps+myBig, runs+myRuns
			mu.Unlock()
		})
	}
	t.Cleanup(func() {
		// The inputs must reach the bounds, not just meet them: most are cut.
		if runs != propertyInputs || splits < propertyInputs/2 || bigSteps < propertyInputs/10 {
			t.Errorf("%d inputs run; %d of them became more than one step and %d more than three", runs, splits, bigSteps)
		}
	})
}

// randomBounds is a bound set drawn at random, every field from just above
// what one member of the generator's inputs needs to a few times that, so that
// each bound in turn is the one that closes steps.
func randomBounds(r *rand.Rand, ident bool) Bounds {
	in := func(lo, hi int) int { return lo + r.IntN(hi-lo+1) }
	argv := in(3000, 12000)
	if ident {
		argv += LimitReceiptBytes
	}
	return Bounds{
		RequestBytes: in(3000, 9000), Entries: in(3, 9), Tables: in(3, 4), Candidates: in(1, 60), GuardOnly: in(6, 90),
		EntryIDs: in(3, 30), RowPairs: in(1, 20), Notes: in(1, 6), AboutIDs: in(10, 80), FieldObservations: in(40, 400),
		LineBytes: in(2000, 5000), LineIDs: in(1, 30), PlannedArgvBytes: argv,
	}
}

// propertyRandomBounds runs the property with a different bound set for every
// input: the interplay of the bounds, not each alone.
func propertyRandomBounds(t *testing.T, inputs, shards int, fullEvery uint64) {
	t.Helper()
	for shard := 0; shard < shards; shard++ {
		t.Run(strconv.Itoa(shard), func(t *testing.T) {
			t.Parallel()
			for seed := uint64(shard + 1); seed <= uint64(inputs); seed += uint64(shards) {
				r := rand.New(rand.NewPCG(seed, 0xb0d5))
				ident := seed%2 == 1
				bd := randomBounds(r, ident)
				g := gen{rand.New(rand.NewPCG(seed, 0x5eed))}
				check(t, seed, bd, ident, seed%fullEvery == 0, g.input())
			}
		})
	}
}

func TestPropertyEveryBoundAtRandom(t *testing.T) {
	t.Parallel()
	propertyRandomBounds(t, 1000, 4, 16)
}
