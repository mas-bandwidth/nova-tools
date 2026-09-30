package sprintfn

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The walk: 10,000 steps on the composed twin with the real X, in which the
// sprint's indexes and due entries, as X's commands leave them in the twin's
// keys, are held equal, after every step, to IT02's definitions recomputed from
// scratch over every card the Mem holds (IndexDefinition). The cards are read
// from the Mem, not from the walk's own notes of them, so the walk checks X
// against the store and not against itself.
//
// The walk makes each change through a verb's step with entries Layer 1 accepts:
// a create, a move to another cell, a change of fields in place, a re-score and a
// remove, one to three cards a step, over all four tables, every field the
// definitions read (kind, attempt, open, refused, bound, the due_* fields, needs)
// set, emptied and unset. Nothing is lawful about it beyond Layer 1's rules.
//
// wait:<n> is the one index X does not write whole: X removes a card that leaves
// waiting from every wait:<n> it names, and the adds are the intents' (IT14). The
// walk holds wait to a model of exactly that: the stand-in sprint part adds
// (n, w) as a waiting card is created naming n, and every step that takes w out
// of waiting must take it out of every wait:<n> of its before-state needs.

const (
	xWalkSteps  = 10_000 // steps of the walk, over all its shards
	xWalkShards = 8
	xWalkCards  = 14 // the most cards of a shard's population
	xWalkScore  = 900

	// xWalkMirrorEvery is how many shards apart the walk runs the Lua half of X
	// beside the twin's: every other shard (0, 2, 4 and 6), which is 5,000 steps.
	xWalkMirrorEvery = 2

	// xWalkMemEvery is how often the walk's model of its cards is held to the
	// Mem's, which copies its whole state to be read.
	xWalkMemEvery = 25
)

// xWalkRows are the rows of each table the walk places cards in.
var xWalkRows = map[string][]string{
	sprint.Work:    {"s1", "s2"},
	sprint.Fleet:   {"m1", "m2"},
	sprint.Readers: {"r1"},
	sprint.Merge:   {"s1", "s2"},
}

type xWalkCard struct {
	table, id, row, col string // col is "" once the card is removed: its record stays, with no place
	score               float64
	fields              map[string]string
}

func (c *xWalkCard) cell() string { return c.row + ":" + c.col }

// xWalkCover counts what the walks did, by index, so that a walk that never
// reaches a case cannot pass for one that does.
type xWalkCover struct {
	mu                       sync.Mutex
	added, removed, rescored map[string]int
	waitRemoved, waitAdded   int
	steps, rewrote           int
	luaPre, luaCmds          int // steps the Lua half of X was held to the twin's on
}

func newXWalkCover() *xWalkCover {
	return &xWalkCover{added: map[string]int{}, removed: map[string]int{}, rescored: map[string]int{}}
}

func (c *xWalkCover) sum(o *xWalkCover) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range [][2]map[string]int{{c.added, o.added}, {c.removed, o.removed}, {c.rescored, o.rescored}} {
		for k, v := range p[1] {
			p[0][k] += v
		}
	}
	c.waitRemoved, c.waitAdded, c.steps, c.rewrote = c.waitRemoved+o.waitRemoved, c.waitAdded+o.waitAdded, c.steps+o.steps, c.rewrote+o.rewrote
	c.luaPre, c.luaCmds = c.luaPre+o.luaPre, c.luaCmds+o.luaCmds
}

func (c *xWalkCover) labels() []string {
	out := []string{sprint.IndexSent, sprint.IndexElig, sprint.IndexFresh, sprint.IndexAgain}
	for _, k := range sprint.DueKinds {
		out = append(out, "due:"+k.Kind)
	}
	return out
}

func (c *xWalkCover) assert(t *testing.T) {
	t.Helper()
	if c.steps != xWalkSteps {
		t.Errorf("the walk made %d steps, want %d", c.steps, xWalkSteps)
	}
	for _, l := range c.labels() {
		if c.added[l] == 0 || c.removed[l] == 0 || c.rescored[l] == 0 {
			t.Errorf("%s: %d members added, %d removed, %d re-scored; the walk must do each", l, c.added[l], c.removed[l], c.rescored[l])
		}
	}
	if c.waitAdded == 0 || c.waitRemoved == 0 {
		t.Errorf("wait: %d adds, %d removals by X; the walk must do each", c.waitAdded, c.waitRemoved)
	}
	if c.rewrote == 0 {
		t.Error("no card left waiting in the step that rewrote its needs, so a removal by the needs it had before was never made")
	}
	if want := xWalkSteps / xWalkMirrorEvery; c.luaPre < want || c.luaCmds < want {
		t.Errorf("the Lua half of X was held to the twin's on %d X.pre and %d X.plan calls, want at least %d", c.luaPre, c.luaCmds, want)
	}
}

type xWalk struct {
	t      *testing.T
	h      *xh
	rng    *rand.Rand
	cards  map[string]*xWalkCard // by table/id
	order  []string
	issued int
	wait   map[[2]string]bool // the model of wait:<n>: (n, w)
	prev   sprint.Indexes
	cover  *xWalkCover
	say    []string // what the step did, for a failure to name
}

func newXWalk(t *testing.T, seed uint64, mirror bool) *xWalk {
	h := newXHarnessMirrored(t, mirror)
	w := &xWalk{t: t, h: h, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), cards: map[string]*xWalkCard{},
		wait: map[[2]string]bool{}, prev: sprint.Indexes{}, cover: newXWalkCover()}
	h.do(&Request{Epoch: "0", Meta: Meta{Verb: "seed"}, Sprint: &SprintPart{Counter: &CounterChange{Read: map[string]string{"score": "", "streams": ""},
		Set: map[string]string{"score": strconv.Itoa(xWalkScore * 1000), "streams": "1"}}}})
	var rows []tset.Entry
	for _, table := range []string{sprint.Work, sprint.Fleet, sprint.Readers, sprint.Merge} {
		rows = append(rows, tset.Entry{Kind: "rows", Table: table, Add: xWalkRows[table]})
	}
	h.do(xVerb("seed", rows...))
	return w
}

func (w *xWalk) chance(pct int) bool { return w.rng.IntN(100) < pct }

func (w *xWalk) some(xs []string) string { return xs[w.rng.IntN(len(xs))] }

func (w *xWalk) score() string {
	return strconv.FormatFloat(1+w.rng.Float64()*(xWalkScore-1), 'f', 3, 64)
}

// xFieldChange is what a step does to the fields the definitions read, and to
// one that none reads.
type xFieldChange struct {
	set   map[string]string
	unset []string
}

// fields is a random change of the fields a table's cards carry, every change
// one of a value, an emptied field, or an unset one. forNeeds lets it name needs.
func (w *xWalk) fields(table string, have map[string]string, creating, needs bool) xFieldChange {
	fc := xFieldChange{set: map[string]string{}}
	pick := func(name string, values ...string) {
		if !creating && !w.chance(35) {
			return
		}
		switch r := w.rng.IntN(10); {
		case r < 6:
			fc.set[name] = w.some(values)
		case r < 8:
			fc.set[name] = ""
		default:
			if _, ok := have[name]; ok && !creating { // a create has no unset (L1 3)
				fc.unset = append(fc.unset, name)
			}
		}
	}
	number := func() string { return strconv.Itoa(1 + w.rng.IntN(1_000_000)) }
	switch table {
	case sprint.Work:
		pick("kind", "primary", "sentinel", "primary")
		pick("attempt", "0", "0", "1", "2")
		pick("open", "0", "0", "1", "2")
		pick("refused", "x", "y")
		pick("bound", "redeals", "attempts")
		if needs {
			pick("needs", w.someNeeds(), w.someNeeds())
		}
	case sprint.Fleet:
		pick("due_untaken", number())
		pick("due_unfinished", number())
	case sprint.Readers:
		pick("due_unbegun", number())
		pick("due_unreported", number())
	case sprint.Merge:
		pick("due_mergeidle", number())
	}
	if w.chance(30) {
		fc.set["head"] = "h" + strconv.Itoa(w.rng.IntN(100)) // a field no definition reads
	}
	for _, u := range fc.unset {
		delete(fc.set, u)
	}
	return fc
}

// someNeeds is one to three work card ids, some of them cards that exist.
func (w *xWalk) someNeeds() string {
	var ids []string
	for range 1 + w.rng.IntN(3) {
		ids = append(ids, "p"+strconv.Itoa(1+w.rng.IntN(max(w.issued, 3))))
	}
	return strings.Join(ids, ",")
}

// op is one card's part of a step.
type xOp struct {
	entry tset.Entry
	card  *xWalkCard
	fc    xFieldChange
	out   bool // the card leaves waiting
	from  map[string]string
	wasIn bool // the card was in waiting before the step
	added [][2]string
}

func (w *xWalk) apply(o *xOp) {
	c := o.card
	for k, v := range o.fc.set {
		c.fields[k] = v
	}
	for _, k := range o.fc.unset {
		delete(c.fields, k)
	}
}

// pickPlaced is a card with a place, not among those the step already names.
func (w *xWalk) pickPlaced(used map[string]bool) *xWalkCard {
	var cs []*xWalkCard
	for _, k := range w.order {
		if c := w.cards[k]; c.col != "" && !used[k] {
			cs = append(cs, c)
		}
	}
	if len(cs) == 0 {
		return nil
	}
	return cs[w.rng.IntN(len(cs))]
}

// placed is how many cards of the walk have a place: a removed card keeps its
// record and its id, and is not one of the population the walk moves.
func (w *xWalk) placed() int {
	n := 0
	for _, k := range w.order {
		if w.cards[k].col != "" {
			n++
		}
	}
	return n
}

// cell is a random place for a card of a table: a stream has one control card,
// so no second card goes to a merge row's ctl column, whose due entry is named
// for the row and could not hold two.
func (w *xWalk) cell(table, self string) (row, col string) {
	for {
		row, col = w.some(xWalkRows[table]), w.some(testColumns[table])
		taken := false
		for _, k := range w.order {
			c := w.cards[k]
			if table == sprint.Merge && col == sprint.Ctl && c.table == table && c.id != self && c.row == row && c.col == col {
				taken = true
			}
		}
		if !taken {
			return row, col
		}
	}
}

func (w *xWalk) newCard(used map[string]bool) *xOp {
	if w.placed() >= xWalkCards {
		return nil
	}
	table := w.some([]string{sprint.Work, sprint.Work, sprint.Work, sprint.Fleet, sprint.Fleet, sprint.Readers, sprint.Merge})
	w.issued++
	id := map[string]string{sprint.Work: "p", sprint.Fleet: "f", sprint.Readers: "r", sprint.Merge: "c"}[table] + strconv.Itoa(w.issued)
	row, col := w.cell(table, id)
	card := &xWalkCard{table: table, id: id, row: row, col: col, fields: map[string]string{}}
	fc := w.fields(table, nil, true, card.col == sprint.Waiting && table == sprint.Work)
	e := tset.Entry{Kind: "create", Table: table, To: card.cell(), IDs: []string{id}, Scores: []string{w.score()}, About: []string{id}, Set: fc.set}
	card.score, _ = strconv.ParseFloat(e.Scores[0], 64)
	o := &xOp{entry: e, card: card, fc: fc}
	if table == sprint.Work && card.col == sprint.Waiting {
		for _, n := range strings.Split(fc.set["needs"], ",") {
			if n != "" {
				o.added = append(o.added, [2]string{n, id})
			}
		}
	}
	w.say = append(w.say, fmt.Sprintf("create %s/%s at %s %v", table, id, card.cell(), fc.set))
	w.cards[table+"/"+id] = card
	w.order = append(w.order, table+"/"+id)
	used[table+"/"+id] = true
	return o
}

func (w *xWalk) changeCard(used map[string]bool) *xOp {
	c := w.pickPlaced(used)
	if c == nil {
		return nil
	}
	used[c.table+"/"+c.id] = true
	o := &xOp{card: c, from: map[string]string{}, wasIn: c.table == sprint.Work && c.col == sprint.Waiting}
	for k, v := range c.fields {
		o.from[k] = v
	}
	kind := w.rng.IntN(10)
	e := tset.Entry{Kind: "move", Table: c.table, From: c.cell(), IDs: []string{c.id}, About: []string{c.id}}
	switch {
	case kind < 1: // remove
		o.fc = xFieldChange{}
		e = tset.Entry{Kind: "remove", Table: c.table, From: c.cell(), IDs: []string{c.id}, About: []string{c.id}}
		o.out = o.wasIn
		w.say = append(w.say, fmt.Sprintf("remove %s/%s from %s", c.table, c.id, c.cell()))
		o.entry = e
		c.col = ""
		return o
	case kind < 6: // move to another cell, with or without field changes
		row, col := w.cell(c.table, c.id)
		e.To = row + ":" + col
		o.out = o.wasIn && !(c.table == sprint.Work && col == sprint.Waiting)
		o.fc = w.fields(c.table, c.fields, false, o.out) // needs are rewritten only as a card leaves waiting
		if w.chance(25) {
			e.Scores = []string{w.score()}
		}
		w.say = append(w.say, fmt.Sprintf("move %s/%s %s -> %s %v unset %v scores %v", c.table, c.id, c.cell(), e.To, o.fc.set, o.fc.unset, e.Scores))
		c.row, c.col = row, col
	case kind < 9: // fields in place
		o.fc = w.fields(c.table, c.fields, false, false)
		if w.chance(40) {
			e.Scores = []string{w.score()}
		}
		w.say = append(w.say, fmt.Sprintf("set %s/%s in %s %v unset %v scores %v", c.table, c.id, c.cell(), o.fc.set, o.fc.unset, e.Scores))
	default: // a re-score alone
		e.Scores = []string{w.score()}
		w.say = append(w.say, fmt.Sprintf("rescore %s/%s in %s to %v", c.table, c.id, c.cell(), e.Scores))
	}
	e.Set, e.Unset = o.fc.set, o.fc.unset
	if len(e.Set) == 0 {
		e.Set = nil
	}
	if len(e.Scores) != 0 {
		c.score, _ = strconv.ParseFloat(e.Scores[0], 64)
	}
	o.entry = e
	if o.out && o.fc.set["needs"] != "" {
		w.cover.rewrote++ // a card leaves waiting in the step that rewrites its needs
	}
	return o
}

// xIndexesOf is the sorted sets of the twin's keys that are indexes, except
// wait:<n>: the keys {p}sent:s@0, elig, fresh, again and due.
func xIndexesOf(keys map[string]KeyValue) (idx sprint.Indexes, wait map[[2]string]bool) {
	idx, wait = sprint.Indexes{}, map[[2]string]bool{}
	for key, kv := range keys {
		name, ok := strings.CutPrefix(key, xp)
		if !ok {
			continue
		}
		name, ok = strings.CutSuffix(name, "@0")
		if !ok {
			continue
		}
		index, arg, _ := strings.Cut(name, ":")
		switch index {
		case sprint.IndexSent, sprint.IndexElig, sprint.IndexFresh, sprint.IndexAgain, sprint.IndexDue:
			m := map[string]float64{}
			for member, score := range kv.ZSet {
				m[member] = score
			}
			if len(m) != 0 {
				idx[sprint.IndexKey{Index: index, Arg: arg}] = m
			}
		case sprint.IndexWait:
			for member := range kv.ZSet {
				wait[[2]string{arg, member}] = true
			}
		}
	}
	return idx, wait
}

// xCardsOf is every card the Mem holds, as the indexes see a card.
func xCardsOf(t *testing.T, m *tset.Mem) []*sprint.IndexCard {
	t.Helper()
	snap, err := m.Snapshot(testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	var out []*sprint.IndexCard
	for table, ts := range snap.Epochs["0"].Tables {
		for id, rec := range ts.Records {
			c := &sprint.IndexCard{Table: table, ID: id, Row: rec.Row, Col: rec.Column, Fields: rec.Fields}
			if rec.Column != "" {
				score, err := strconv.ParseFloat(rec.Score, 64)
				if err != nil {
					t.Fatalf("%s card %s has the score %q", table, id, rec.Score)
				}
				c.Score = score
			}
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Table+"/"+out[i].ID < out[j].Table+"/"+out[j].ID })
	return out
}

// modelCards is the walk's own population as the indexes see a card.
func (w *xWalk) modelCards() []*sprint.IndexCard {
	out := make([]*sprint.IndexCard, 0, len(w.order))
	for _, k := range w.order {
		c := w.cards[k]
		ic := &sprint.IndexCard{Table: c.table, ID: c.id, Col: c.col, Fields: c.fields}
		if c.col != "" {
			ic.Row, ic.Score = c.row, c.score
		}
		out = append(out, ic)
	}
	return out
}

// checkModel holds the walk's model of its cards to the Mem's: the same cards,
// places, scores and fields, so that the definitions, computed over the model
// between these checks, are computed over what the store holds.
func (w *xWalk) checkModel() {
	w.t.Helper()
	mem := map[string]*sprint.IndexCard{}
	for _, c := range xCardsOf(w.t, w.h.mem) {
		mem[c.Table+"/"+c.ID] = c
	}
	if len(mem) != len(w.order) {
		w.t.Fatalf("step %d: the Mem holds %d cards, the walk %d", w.cover.steps, len(mem), len(w.order))
	}
	for _, c := range w.modelCards() {
		m := mem[c.Table+"/"+c.ID]
		if m == nil || m.Row != c.Row || m.Col != c.Col || m.Score != c.Score || len(m.Fields) != len(c.Fields) {
			w.t.Fatalf("step %d: the walk thinks %s/%s is %+v, the Mem holds %+v\nthe step:\n%s", w.cover.steps, c.Table, c.ID, c, m, strings.Join(w.say, "\n"))
		}
		for k, v := range c.Fields {
			if got, ok := m.Fields[k]; !ok || got != v {
				w.t.Fatalf("step %d: the walk thinks %s/%s has %s=%q, the Mem holds %q (%v)\nthe step:\n%s", w.cover.steps, c.Table, c.ID, k, v, got, ok, strings.Join(w.say, "\n"))
			}
		}
	}
}

func xLabel(k sprint.IndexKey, member string) string {
	if k.Index != sprint.IndexDue {
		return k.Index
	}
	kind, _, _ := strings.Cut(member, ":")
	return "due:" + kind
}

// count adds to the walk's cover what one step did to the indexes.
func (w *xWalk) count(now sprint.Indexes) {
	for k, m := range now {
		for member, score := range m {
			was, ok := w.prev[k][member]
			switch {
			case !ok:
				w.cover.added[xLabel(k, member)]++
			case was != score:
				w.cover.rescored[xLabel(k, member)]++
			}
		}
	}
	for k, m := range w.prev {
		for member := range m {
			if _, ok := now[k][member]; !ok {
				w.cover.removed[xLabel(k, member)]++
			}
		}
	}
	w.prev = now
}

func (w *xWalk) step() {
	w.say = w.say[:0]
	used := map[string]bool{}
	var ops []*xOp
	for range 1 + w.rng.IntN(3) {
		var o *xOp
		if w.chance(40) || w.placed() < 4 {
			o = w.newCard(used)
		}
		if o == nil {
			o = w.changeCard(used)
		}
		if o != nil {
			ops = append(ops, o)
		}
	}
	if len(ops) == 0 {
		return
	}
	req := &Request{Epoch: "0", Meta: Meta{Verb: "walk"}}
	var adds []Cmd
	for _, o := range ops {
		req.Body.Entries = append(req.Body.Entries, o.entry)
		for _, p := range o.added {
			adds = append(adds, Command("ZADD", xp+"wait:"+p[0]+"@0", kindZSet, "0", p[1]))
			w.wait[p] = true
			w.cover.waitAdded++
		}
	}
	if len(adds) != 0 {
		req.Sprint = &SprintPart{}
		w.h.extra = adds
	}
	res, err := Step(context.Background(), w.h.tw, req)
	if err != nil || res.Refusal != nil || res.Err != nil || res.Step == nil {
		w.t.Fatalf("step %d was not applied: result %+v, err %v\n%s", w.cover.steps, res, err, strings.Join(w.say, "\n"))
	}
	for _, o := range ops {
		w.apply(o)
		if o.out {
			for _, n := range strings.Split(o.from["needs"], ",") {
				if w.wait[[2]string{n, o.card.id}] {
					delete(w.wait, [2]string{n, o.card.id})
					w.cover.waitRemoved++
				}
			}
		}
	}
	w.cover.steps++

	if w.cover.steps%xWalkMemEvery == 0 {
		w.checkModel()
	}
	got, gotWait := xIndexesOf(w.h.keys())
	want, err := sprint.IndexDefinition(w.modelCards())
	if err != nil {
		w.t.Fatalf("step %d: the definitions refused the cards: %v\n%s", w.cover.steps, err, strings.Join(w.say, "\n"))
	}
	for k := range want {
		if k.Index == sprint.IndexWait {
			delete(want, k)
		}
	}
	if diff := got.Diff(want); len(diff) != 0 {
		w.t.Fatalf("step %d: the indexes X left are not their definitions:\n%s\nthe step:\n%s", w.cover.steps, strings.Join(diff, "\n"), strings.Join(w.say, "\n"))
	}
	if len(gotWait) != len(w.wait) {
		w.t.Fatalf("step %d: wait holds %v, the model %v\nthe step:\n%s", w.cover.steps, gotWait, w.wait, strings.Join(w.say, "\n"))
	}
	for p := range gotWait {
		if !w.wait[p] {
			w.t.Fatalf("step %d: wait:%s holds %s, which the model has left\nthe step:\n%s", w.cover.steps, p[0], p[1], strings.Join(w.say, "\n"))
		}
	}
	w.count(got)
}

// TestXIndexEqualsDefinition: after each of 10,000 steps of the walk, on eight
// shards, the indexes and the due entries X's commands left in the twin equal
// IT02's definitions computed from scratch over every card of the Mem, member for
// member and score for score (I1, D1), and wait:<n> holds exactly the waits the
// adds made less the cards that left waiting. Half the shards also run every step
// through the Lua half of X beside the twin's, which must refuse as the twin does
// and write the commands it writes (twin_x_lua_test.go).
func TestXIndexEqualsDefinition(t *testing.T) {
	t.Parallel()
	cover := newXWalkCover()
	t.Run("shards", func(t *testing.T) {
		for shard := 0; shard < xWalkShards; shard++ {
			t.Run(fmt.Sprintf("shard%d", shard), func(t *testing.T) {
				t.Parallel()
				w := newXWalk(t, uint64(shard)+1, shard%xWalkMirrorEvery == 0)
				for attempts := 0; w.cover.steps < xWalkSteps/xWalkShards; attempts++ {
					if attempts > 4*xWalkSteps {
						t.Fatalf("the walk stalled after %d steps", w.cover.steps)
					}
					w.step()
				}
				if w.h.mirror != nil {
					w.cover.luaPre, w.cover.luaCmds = w.h.mirror.pre, w.h.mirror.cmds
				}
				cover.sum(w.cover)
			})
		}
	})
	if !t.Failed() {
		cover.assert(t)
	}
}

// BenchmarkXCmds2000 is X.plan's Go time for a step of 2,000 changed cards, every
// one a move of a waiting primary to ready, into fresh and out of elig: the
// derivation of IT02 folded into one ZREM and one ZADD a key, in pieces of 1,000.
func BenchmarkXCmds2000(b *testing.B) {
	const n = 2000
	e := tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", To: "s1:ready"}
	obs := &Before{Records: map[string]map[string]tset.MemberRecord{sprint.Work: {}}}
	fields := map[string]tset.FieldValue{}
	for _, f := range sprint.IndexFields() {
		fields[f] = tset.FieldValue{Present: false}
	}
	for i := 0; i < n; i++ {
		id := "p" + strconv.Itoa(i)
		e.IDs, e.About = append(e.IDs, id), append(e.About, id)
		obs.Records[sprint.Work][id] = tset.MemberRecord{ID: id, Exists: true, Place: &tset.CellPlace{Row: "s1", Col: "waiting"}, Score: strconv.Itoa(i), Fields: fields}
	}
	req := &Request{Epoch: "0", Meta: Meta{Verb: "bench"}, Body: Body{Entries: []tset.Entry{e}}}
	tp, err := xDryPlan(req, obs)
	if err != nil {
		b.Fatal(err)
	}
	carry := &xCarry{req: req, obs: obs, quarantined: map[string]bool{}, requeue: map[string]float64{}, writeEpoch: "0"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := xCommands(testPrefix, carry, tp); err != nil {
			b.Fatal(err)
		}
	}
}
