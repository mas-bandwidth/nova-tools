package stepbuild

import (
	"errors"
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
//   - every step is inside every bound (the request counted from its bytes,
//     the planned argv bytes by the model and by the strict count);
//   - the entries are placed in the order of the input, except where rows and
//     members need another (refOrder, an accounting written apart from the
//     builder's), and the members of the steps, in step order, are the
//     input's members in that order: none lost, none repeated, each with its
//     own score, revision, fields and about, and each wire entry carrying its
//     entry's shared fields whole;
//   - each entry's guards are in every step that holds a part of it, once,
//     ahead of the first part, and in no other step;
//   - notes are in placement order, after every part of their entry;
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
	argv := 40000
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

// rowName is row k of the rows the inputs name: the rows entries add and
// delete them, and the cells of members read and write them.
func rowName(k int) string {
	if k%11 == 0 {
		return "row-" + strconv.Itoa(k) + "\"q\\"
	}
	return "row-" + strconv.Itoa(k)
}

// cell is a cell reference: in a third of them a row the rows entries name.
func (g gen) cell() string {
	col := []string{"todo", "done", "a:b"}[g.r.IntN(3)]
	if g.coin(33) {
		return rowName(g.r.IntN(20)) + ":" + col
	}
	return g.str(6) + "r:" + col
}

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
	if g.coin(75) { // most rows entries add or delete, few do both
		if g.coin(50) {
			d = 0
		} else {
			a = 0
		}
	}
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
	if e.Kind == KindRows {
		e.Guards = guardsNotOn(e.Guards, e.Table, e.Del)
	}
	return e
}

// guardsNotOn is the guards without those whose source row is one of rows of
// table: a rows entry that deletes a row its own guard names is refused
// (TestARowsEntryThatDeletesARowItsOwnGuardNamesIsRefused).
func guardsNotOn(guards []Entry, table string, rows []string) []Entry {
	var out []Entry
	for _, g := range guards {
		keep := true
		for _, r := range rows {
			if g.Table == table && rowOf(g.From) == r {
				keep = false
			}
		}
		if keep {
			out = append(out, g)
		}
	}
	return out
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

// propKeys is the member prefix the property tests build with and count with.
var propKeys = keyCfg{prefix: 16}

// rowKey is a row of a table.
type rowKey struct{ table, row string }

// srcRowsOf are the rows an entry reads as a source: its own from cell and its
// guards'.
func srcRowsOf(e Entry) []rowKey {
	var out []rowKey
	if e.From != "" {
		out = append(out, rowKey{e.Table, rowOf(e.From)})
	}
	for _, g := range e.Guards {
		out = append(out, rowKey{g.Table, rowOf(g.From)})
	}
	return out
}

// dstRowsOf are the rows an entry writes as a destination: its to cell.
func dstRowsOf(e Entry) []rowKey {
	if e.To != "" {
		return []rowKey{{e.Table, rowOf(e.To)}}
	}
	return nil
}

// membersOf are the (table, id) an entry names, its guards' included.
func membersOf(e Entry) []rowKey {
	var out []rowKey
	if e.Kind != KindRows && e.Kind != KindNote {
		for _, id := range e.IDs {
			out = append(out, rowKey{e.Table, id})
		}
	}
	for _, g := range e.Guards {
		for _, id := range g.IDs {
			out = append(out, rowKey{g.Table, id})
		}
	}
	return out
}

func hasRow(rows []rowKey, k rowKey) bool {
	for _, r := range rows {
		if r == k {
			return true
		}
	}
	return false
}

// refOrder is the order the entries are placed in, counted by an accounting
// written apart from the builder's (order.go): each pair of entries is asked
// whether one must come before the other, by the rules of order.go's comment,
// and the entries are taken lowest index first as each becomes ready. It says
// false when the entries wait on each other.
func refOrder(in []Entry) ([]int, bool) {
	n := len(in)
	first := make([][]bool, n) // first[a][z]: a is placed before z
	for i := range first {
		first[i] = make([]bool, n)
	}
	src, dst := make([]map[rowKey]bool, n), make([]map[rowKey]bool, n)
	named := make([]map[rowKey]int, n) // rows entries: (table, row) -> 1 added, 2 deleted, 3 both
	byMember := map[rowKey][]int{}
	var rows []int // the rows entries
	for i, e := range in {
		src[i], dst[i] = map[rowKey]bool{}, map[rowKey]bool{}
		for _, k := range srcRowsOf(e) {
			src[i][k] = true
		}
		for _, k := range dstRowsOf(e) {
			dst[i][k] = true
		}
		for _, m := range membersOf(e) {
			byMember[m] = append(byMember[m], i)
		}
		if e.Kind == KindRows {
			rows = append(rows, i)
			named[i] = map[rowKey]int{}
			for _, r := range e.Add {
				named[i][rowKey{e.Table, r}] |= 1
			}
			for _, r := range e.Del {
				named[i][rowKey{e.Table, r}] |= 2
			}
		}
	}
	// A member named by two entries: the earlier is placed first.
	for _, list := range byMember {
		for x := 0; x < len(list); x++ {
			for y := x + 1; y < len(list); y++ {
				if list[x] != list[y] {
					first[list[x]][list[y]] = true
				}
			}
		}
	}
	for _, a := range rows {
		for _, z := range rows {
			if a >= z {
				continue
			}
			if len(in[a].Add) > 0 && len(in[z].Add) > 0 {
				first[a][z] = true // ranks are in order of first occurrence
			}
			for k, da := range named[a] {
				if dz, ok := named[z][k]; ok && (da|dz) == 3 {
					first[a][z] = true // a row added by one and deleted by the other: in order
				}
			}
		}
		for z := 0; z < n; z++ {
			if z == a {
				continue
			}
			for _, r := range in[a].Add {
				if dst[z][rowKey{in[a].Table, r}] {
					first[a][z] = true // a row is added before a member is placed in it
				}
			}
			for _, r := range in[a].Del {
				if src[z][rowKey{in[a].Table, r}] {
					first[z][a] = true // a row is deleted after the members leave it
				}
			}
		}
	}
	waiting := make([]int, n) // entries each still waits for
	for a := range first {
		for z, before := range first[a] {
			if before {
				waiting[z]++
			}
		}
	}
	var out []int
	placed := make([]bool, n)
	for len(out) < n {
		next := -1
		for z := 0; z < n && next < 0; z++ {
			if !placed[z] && waiting[z] == 0 {
				next = z
			}
		}
		if next < 0 {
			return nil, false
		}
		placed[next] = true
		out = append(out, next)
		for z, before := range first[next] {
			if before {
				waiting[z]--
			}
		}
	}
	return out, true
}

// check runs every property on one input. acct counts every step by the full
// accounting (the JSON model of the line and the planned argv bytes, by the
// model and by the strict count), else by the lite one; deep adds the
// fullness check and a second build.
func check(t testing.TB, seed uint64, bd Bounds, ident, acct, deep bool, in []Entry) []Step {
	t.Helper()
	c := cfg()
	c.Bounds = bd
	c.MemberPrefixBytes = propKeys.prefix
	if ident {
		c.Ident = identFor
	}
	fail := func(f string, a ...any) {
		t.Helper()
		t.Fatalf("seed %d: %s", seed, fmt.Sprintf(f, a...))
	}
	ord, orderable := refOrder(in)
	steps, err := Build(c, in)
	if !orderable {
		var ie *InputError
		if steps != nil || !errors.As(err, &ie) || ie.Field != "add/del" {
			fail("entries that wait on each other were not refused: %d steps, %v", len(steps), err)
		}
		return nil
	}
	if err != nil {
		fail("Build refused: %v", err)
	}
	pos := make([]int, len(in))
	for p, i := range ord {
		pos[i] = p
	}
	for i, s := range steps {
		raw := s.Encode()
		if s.Bytes != len(raw) || s.Part != i+1 || s.Parts != len(steps) {
			fail("step %d: Bytes %d encoded %d, part %d of %d", i+1, s.Bytes, len(raw), s.Part, s.Parts)
		}
		if acct {
			if bad := measureKeys(t, raw, propKeys).within(bd); len(bad) > 0 {
				fail("step %d is outside its bounds: %v", i+1, bad)
			}
		} else if bad := measureLite(s, raw).withinLite(bd); len(bad) > 0 {
			fail("step %d is outside its bounds: %v", i+1, bad)
		}
	}
	checkMembers(t, seed, in, ord, pos, steps)
	checkGuardsAndNotes(t, seed, in, ord, pos, steps)
	if deep {
		checkFull(t, seed, bd, pos, steps)
	}
	checkResume(t, seed, steps)
	// A second build of the same input: every input that is checked deep, one
	// in four otherwise (the same input is built twice by
	// TestTheSameInputGivesTheSameSteps as well).
	if deep || seed%4 == 0 {
		again, err := Build(c, in)
		if err != nil || !reflect.DeepEqual(again, steps) {
			fail("a second build differs: %v", err)
		}
	}
	return steps
}

// checkMembers reads the steps back into the input: per input entry, the
// concatenation of its parts is its members, in order, with its own aligned
// values, and every part carries its shared fields whole; and the entries come
// in the order ord.
func checkMembers(t testing.TB, seed uint64, in []Entry, ord, pos []int, steps []Step) {
	t.Helper()
	last := -1
	parts := map[int][]Entry{}
	for _, s := range steps {
		for _, p := range s.Entries {
			if p.Guard {
				continue
			}
			if pos[p.Source] < last {
				t.Fatalf("seed %d: entry %d's part comes after the part of the entry placed at %d", seed, p.Source, last)
			}
			last = pos[p.Source]
			parts[p.Source] = append(parts[p.Source], p.Entry)
		}
	}
	for _, i := range ord {
		e := in[i]
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
// notes in placement order and after every part of their entry.
func checkGuardsAndNotes(t testing.TB, seed uint64, in []Entry, ord, pos []int, steps []Step) {
	t.Helper()
	lastPartStep := map[int]int{}
	firstPartStep := map[int]int{}
	for k, s := range steps {
		held := map[int][]Placed{}
		firstPart := map[int]int{}
		for at, p := range s.Entries {
			switch {
			case p.Guard:
				held[p.Source] = append(held[p.Source], p)
			default:
				lastPartStep[p.Source] = k
				if _, ok := firstPartStep[p.Source]; !ok {
					firstPartStep[p.Source] = k
				}
				if _, ok := firstPart[p.Source]; !ok {
					firstPart[p.Source] = at
				}
			}
		}
		for src, at := range firstPart {
			g := in[src].Guards
			if len(held[src]) != len(g) {
				t.Fatalf("seed %d: step %d holds %d of entry %d's %d guards", seed, k+1, len(held[src]), src, len(g))
			}
			for gi, p := range held[src] {
				if !reflect.DeepEqual(p.Entry, g[gi]) {
					t.Fatalf("seed %d: step %d: guard %d of entry %d is not the entry's", seed, k+1, gi, src)
				}
			}
			for i, p := range s.Entries {
				if p.Guard && p.Source == src && i > at {
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
	for _, i := range ord {
		for _, n := range in[i].Notes {
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
				if pos[src] > pos[n.Source] && first < k {
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
func checkFull(t testing.TB, seed uint64, bd Bounds, pos []int, steps []Step) {
	t.Helper()
	for k := 0; k+1 < len(steps); k++ {
		cur, next := steps[k], steps[k+1]
		hyp := cur
		hyp.Entries = append([]Placed(nil), cur.Entries...)
		hyp.Notes = append([]PlacedNote(nil), cur.Notes...)
		if !addFirstPiece(&hyp, cur, next, pos) {
			continue
		}
		if bad := measureKeys(t, hyp.Encode(), propKeys).within(bd); len(bad) == 0 {
			t.Fatalf("seed %d: step %d was closed early: the first piece of step %d fits it", seed, k+1, k+2)
		}
	}
}

// addFirstPiece adds to hyp the first piece the builder placed in next: a
// note that led it, else its first member, with the guards of its entry when
// cur does not hold them. It says false when there is nothing to add.
func addFirstPiece(hyp *Step, cur, next Step, pos []int) bool {
	var first *Placed
	for i := range next.Entries {
		if !next.Entries[i].Guard {
			first = &next.Entries[i]
			break
		}
	}
	if len(next.Notes) > 0 && (first == nil || pos[next.Notes[0].Source] < pos[first.Source]) {
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

// propertyShards split the run over parallel subtests; deepEvery is how often
// an input gets the fullness check and a second build. Every step of every
// input is counted by the full accounting, the JSON model of the line and the
// planned argv bytes by the model and by the strict count. The slow tag runs
// every input deep.
const (
	propertyShards = 8
	deepEvery      = 16
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
				steps := check(t, seed, smallBounds(ident), ident, true, seed%deepEvery == 0, g.input())
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
	argv := in(14000, 60000)
	if ident {
		argv += LimitReceiptBytes
	}
	return Bounds{
		RequestBytes: in(3000, 9000), Entries: in(3, 9), Tables: in(3, 4), Candidates: in(1, 60), GuardOnly: in(6, 90),
		EntryIDs: in(3, 30), RowPairs: in(3, 30), Notes: in(1, 6), AboutIDs: in(10, 80), FieldObservations: in(40, 400),
		LineBytes: in(2000, 5000), LineIDs: in(10, 30), PlannedArgvBytes: argv,
	}
}

// propertyRandomBounds runs the property with a different bound set for every
// input: the interplay of the bounds, not each alone.
func propertyRandomBounds(t *testing.T, inputs, shards int, deepEvery uint64) {
	t.Helper()
	for shard := 0; shard < shards; shard++ {
		t.Run(strconv.Itoa(shard), func(t *testing.T) {
			t.Parallel()
			for seed := uint64(shard + 1); seed <= uint64(inputs); seed += uint64(shards) {
				r := rand.New(rand.NewPCG(seed, 0xb0d5))
				ident := seed%2 == 1
				bd := randomBounds(r, ident)
				g := gen{rand.New(rand.NewPCG(seed, 0x5eed))}
				check(t, seed, bd, ident, true, seed%deepEvery == 0, g.input())
			}
		})
	}
}

func TestPropertyEveryBoundAtRandom(t *testing.T) {
	t.Parallel()
	propertyRandomBounds(t, 1000, 4, 16)
}
