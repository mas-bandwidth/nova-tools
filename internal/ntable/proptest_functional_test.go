//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"pgregory.net/rapid"
)

// proptest_functional_test.go is gate item 3 of the line between layers
// (rowan-new SPEC-COORDINATOR section 9; Glenn 2026-09-27 ~1:30 PM ET: "Where
// is the line? When do we say, OK the foundation is done, now we build on
// top?"): "a state-machine property test drives random verb sequences on a
// real store and checks the layer's invariants after every step". rapid draws
// the sequences, the store is a throwaway redis-server with the library
// loaded, and the oracle is a Go model of TODAY's table.lua
// (internal/nsprint/fn/lua/table.lua), the behaviour Stella's TLA+ model of
// it describes (rowan-new specs/tla/TABLE-MODEL.md and TableMachine.tla,
// pinned at f77458853): physical owned sets kept apart from the metadata that
// shows them, so a shape change can hide one without deleting it; row-add
// rewriting a row's bindings and keeping its owned sets; bind deleting the
// owned cells of the rows it omits and rewriting every binding of the rows
// it keeps; clear refusing the whole table when any cell is bound; drop
// leaving the external set, and the owned key behind a cell that was bound
// when its row went.
//
// After every action the harness holds, against today's code:
//
//  1. the store is the model: the registry; every live table's definition
//     and rows in order; per cell Bound, Key and Count as Read gives them
//     and the members with their scores as CellMembers gives them (the
//     external set when bound, the owned set otherwise); the external set;
//     and every physical cell key under table:* (the orphans the model
//     predicts included; an emptied set leaves no key);
//  2. a refusal the model predicts is an error of the predicted kind
//     (ErrNoTable, no such row, no such column, ErrNotMember, a *BoundError
//     naming the first bound cell and its owner) and the store is
//     byte-identical before and after it (KEYS table:*, the registry and
//     the external key, DUMP each);
//  3. a move keeps the member's score;
//  4. clear keeps the definition and leaves the external set;
//  5. drop leaves the external set.
//
// What it deliberately does NOT assert today: the desired contract the layer
// must reach before anything is built on it, ONE PLACE per table (a member
// in at most one owned cell of a table) and lossless bind (no owned member
// deleted or hidden by a shape change, bind or row-add). Today's table.lua
// holds neither (Stella's OnePlacePerTable and BindPreservesOwned
// counterexamples), so the run counts the violations its actions introduce
// and reports them at the end; a run that reaches none is red, because the
// generator no longer reaches the gap or the Lua changed under it. With
// NOVA_TABLE_CONTRACT=1 the first violation is a failure naming the table,
// member and cells and the shrunk action sequence, so the same harness is
// the gate once the corrected contract lands. Out of scope, as in the TLA+
// model: one writer (no interleaving), text columns, labels and excludes,
// bind targets other than the one external set (the alias witnesses),
// scores beyond {1, 2}, ACL, rendering. A failure prints rapid's shrunk
// draws and the model's action log; rapid also writes its fail file under
// the package's testdata directory (-rapid.nofailfile skips that).

// The universe: small, so rapid's default hundred checks, four Repeat
// rounds of some thirty actions each, finish in seconds and reach every
// corner many times over.
const (
	extKey   = "ext:members"    // the one external set a cell can be bound to
	extOwner = "external owner" // the row owner a bound refusal names
	badCol   = "z"              // a column no table has: NOCOL
)

var (
	propTables  = []string{"t1", "t2"}
	propRows    = []string{"r1", "r2"}
	propColumns = []string{"a", "b", "c"}
	propMembers = []string{"m1", "m2", "m3"}
	propScores  = []float64{1, 2}
	// cellColumns is what a cell verb draws its column from: the body
	// columns, and one draw in ten a column no table has.
	cellColumns = []string{"a", "b", "c", "a", "b", "c", "a", "b", "c", badCol}
)

// propDefinition is the one definition every table here has: three count
// columns. A second create of it is a no-op, never EXISTS.
func propDefinition(name string) ntable.Table {
	cols, err := ntable.ParseColumns("a,b,c")
	if err != nil {
		panic(err)
	}
	return ntable.Table{Name: name, Columns: cols}
}

func isColumn(col string) bool {
	for _, c := range propColumns {
		if c == col {
			return true
		}
	}
	return false
}

// zset is an ordered set as the model holds it: member -> score.
type zset map[string]float64

func (s zset) String() string {
	parts := make([]string, 0, len(s))
	for m, sc := range s {
		parts = append(parts, fmt.Sprintf("%s@%g", m, sc))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, " ") + "}"
}

func sameZset(a, b zset) bool {
	if len(a) != len(b) {
		return false
	}
	for m, s := range a {
		if t, ok := b[m]; !ok || t != s {
			return false
		}
	}
	return true
}

func zsetOf(zs []redis.Z) zset {
	out := zset{}
	for _, z := range zs {
		out[fmt.Sprint(z.Member)] = z.Score
	}
	return out
}

func zsetOfMembers(ms []ntable.Member) zset {
	out := zset{}
	for _, m := range ms {
		out[m.Member] = m.Score
	}
	return out
}

// modelRow is a row's metadata: its rank in the row order, its owner, and
// the set each bound column shows instead of the row's own (absent: owned).
type modelRow struct {
	rank  float64
	owner string
	binds map[string]string
}

type modelTable struct{ rows map[string]*modelRow }

// model is today's table.lua as Stella's TableMachine.tla describes it: the
// live tables with their rows and bindings, and, apart from them, every
// physical owned set by its Redis key, which the metadata can hide (a bound
// column) or orphan (a row removed while the column was bound, a dropped
// table) without deleting. log is the action log a failure prints.
type model struct {
	live map[string]*modelTable
	phys map[string]zset
	ext  zset
	log  []string
}

func newModel() *model {
	return &model{live: map[string]*modelTable{}, phys: map[string]zset{}, ext: zset{}}
}

func (m *model) note(format string, args ...any) {
	m.log = append(m.log, fmt.Sprintf("#%d %s", len(m.log)+1, fmt.Sprintf(format, args...)))
}

func (m *model) trace() string {
	if len(m.log) == 0 {
		return "(none)"
	}
	return strings.Join(m.log, "\n")
}

func (m *model) liveNames() []string {
	names := make([]string, 0, len(m.live))
	for name := range m.live {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// rowsInOrder is the row order ZRANGE gives: by rank, then by key.
func (m *model) rowsInOrder(tn string) []string {
	mt := m.live[tn]
	if mt == nil {
		return nil
	}
	keys := make([]string, 0, len(mt.rows))
	for key := range mt.rows {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := mt.rows[keys[i]].rank, mt.rows[keys[j]].rank
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// tailRank is the highest rank a table's rows hold, 0 with no rows: a row
// added takes the next one.
func (m *model) tailRank(tn string) float64 {
	tail := 0.0
	for _, mr := range m.live[tn].rows {
		if mr.rank > tail {
			tail = mr.rank
		}
	}
	return tail
}

// shown is the set a cell of a present row displays and its key: the bound
// set when the column is bound, the row's own otherwise.
func (m *model) shown(tn, r, col string) (string, zset) {
	if k := m.live[tn].rows[r].binds[col]; k != "" {
		if k == extKey {
			return k, m.ext
		}
		return k, m.phys[k]
	}
	key := ntable.CellKey(tn, r, col)
	return key, m.phys[key]
}

// removeRow is T.remove: the owned set of every column the row does not
// bind is deleted, and the one behind a bound column stays (another writer
// owns the key the binding names, so the Lua does not touch the row's own
// key for that column either). It returns how many non-empty owned sets
// stayed behind that way, orphans until the row is added again.
func (m *model) removeRow(tn, r string) int {
	mt := m.live[tn]
	mr := mt.rows[r]
	left := 0
	for _, col := range propColumns {
		key := ntable.CellKey(tn, r, col)
		if mr.binds[col] == "" {
			delete(m.phys, key)
		} else if len(m.phys[key]) > 0 {
			left++
		}
	}
	delete(mt.rows, r)
	return left
}

// firstBound is the cell a clear refusal names: the first bound cell in row
// order, then column order.
func (m *model) firstBound(tn string) (verdict, bool) {
	for _, r := range m.rowsInOrder(tn) {
		mr := m.live[tn].rows[r]
		for _, col := range propColumns {
			if k := mr.binds[col]; k != "" {
				return verdict{kind: "bound", row: r, col: col, key: k, owner: mr.owner}, true
			}
		}
	}
	return verdict{}, false
}

// cellWrite is T.cell for a write, the refusals in the order the Lua checks
// them: no table, no row, no column, bound.
func (m *model) cellWrite(tn, r, col string) verdict {
	mt := m.live[tn]
	if mt == nil {
		return refused("notable")
	}
	mr := mt.rows[r]
	if mr == nil {
		return refused("norow")
	}
	if !isColumn(col) {
		return refused("nocol")
	}
	if k := mr.binds[col]; k != "" {
		return verdict{kind: "bound", row: r, col: col, key: k, owner: mr.owner}
	}
	return succeeds
}

// ownedMembers is every member shown in an owned cell of a table: what a
// lossless shape change keeps visible. Empty for a table that is not live.
func (m *model) ownedMembers(tn string) map[string]bool {
	out := map[string]bool{}
	mt := m.live[tn]
	if mt == nil {
		return out
	}
	for r, mr := range mt.rows {
		for _, col := range propColumns {
			if mr.binds[col] != "" {
				continue
			}
			for mem := range m.phys[ntable.CellKey(tn, r, col)] {
				out[mem] = true
			}
		}
	}
	return out
}

// heldBy is every member in an owned cell of a table, sorted: what cellAdd
// prefers, so a member lands in a second cell.
func (m *model) heldBy(tn string) []string { return sortedKeys(m.ownedMembers(tn)) }

// membersAt is the set at a key, sorted: what a remove or a move prefers.
func (m *model) membersAt(key string) []string { return sortedKeys(m.phys[key]) }

// onePlaceBreaches is every placement that breaks ONE PLACE, "table t
// member m in owned cell r.c" for a member in two or more owned cells of
// one table, with the cells it is in.
func (m *model) onePlaceBreaches() map[string][]string {
	out := map[string][]string{}
	for tn, mt := range m.live {
		at := map[string][]string{}
		for r, mr := range mt.rows {
			for _, col := range propColumns {
				if mr.binds[col] != "" {
					continue
				}
				for mem := range m.phys[ntable.CellKey(tn, r, col)] {
					at[mem] = append(at[mem], r+"."+col)
				}
			}
		}
		for mem, cells := range at {
			if len(cells) < 2 {
				continue
			}
			sort.Strings(cells)
			for _, cell := range cells {
				out[fmt.Sprintf("table %s member %s in owned cell %s", tn, mem, cell)] = cells
			}
		}
	}
	return out
}

// verdict is what the model says a verb does: it succeeds, or it refuses
// for one reason, a bound refusal naming the cell and its owner.
type verdict struct {
	kind                 string
	row, col, key, owner string
}

var succeeds = verdict{kind: "ok"}

func refused(kind string) verdict { return verdict{kind: kind} }

func (v verdict) String() string {
	if v.kind == "bound" {
		return fmt.Sprintf("bound(%s.%s shows %s, owner %q)", v.row, v.col, v.key, v.owner)
	}
	return v.kind
}

func outcome(v verdict) string {
	if v == succeeds {
		return "OK"
	}
	return "REFUSED " + v.String()
}

// classify reads a verb's error as the store.go refusal it carries.
func classify(err error) verdict {
	var bound *ntable.BoundError
	switch {
	case err == nil:
		return succeeds
	case errors.As(err, &bound):
		return verdict{kind: "bound", row: bound.Row, col: bound.Col, key: bound.Key, owner: bound.Owner}
	case errors.Is(err, ntable.ErrNoTable):
		return refused("notable")
	case errors.Is(err, ntable.ErrNotMember):
		return refused("notmember")
	case strings.Contains(err.Error(), "no such row"):
		return refused("norow")
	case strings.Contains(err.Error(), "no such column"):
		return refused("nocol")
	}
	return verdict{kind: "unexpected: " + err.Error()}
}

// contractTally counts, over the whole run, the desired-contract violations
// today's code lets through, and keeps the first witness.
type contractTally struct {
	onePlace, lossless int
	first              string
	// steps, ok and refused count the actions and how the verbs answered
	steps, ok, refused int
	// orphaned counts the non-empty owned sets a row removal left behind a
	// bound column, ghosts the rows added again that showed one of them
	orphaned, ghosts int
}

// harness is one rapid check: the store, the model beside it, and the tally
// shared by every check of the run.
type harness struct {
	t        *rapid.T
	ctx      context.Context
	c        *redis.Client
	m        *model
	contract bool
	tally    *contractTally
}

// repeatRounds is how many rapid Repeat rounds one check runs; see the loop.
const repeatRounds = 4

// TestTableVerbsAgainstModel drives random sequences of nova-table's verbs
// (and the external owner's raw writes) against a throwaway redis-server
// and holds the store to the model after every one; see the file header.
func TestTableVerbsAgainstModel(t *testing.T) {
	t.Parallel()

	contract := os.Getenv("NOVA_TABLE_CONTRACT") == "1"
	_, c := live(t)
	ctx := context.Background()
	var tally contractTally
	rapid.Check(t, func(rt *rapid.T) {
		// every check starts from an empty store; the library survives FLUSHALL
		if err := c.FlushAll(ctx).Err(); err != nil {
			rt.Fatalf("flushall between checks: %v", err)
		}
		h := &harness{t: rt, ctx: ctx, c: c, m: newModel(), contract: contract, tally: &tally}
		actions := map[string]func(*rapid.T){
			"":               h.verify,
			"create":         h.create,
			"rowAdd":         h.rowAdd,
			"rowDel":         h.rowDel,
			"bindKeepSubset": h.bindKeepSubset,
			"cellAdd":        h.cellAdd,
			"cellRemove":     h.cellRemove,
			"cellMove":       h.cellMove,
			"clear":          h.clear,
			"drop":           h.drop,
			"externalAdd":    h.externalAdd,
			"externalRemove": h.externalRemove,
		}
		// repeatRounds rounds of rapid's Repeat per check, one model and one
		// store throughout: a round averages thirty actions, the chain
		// create, row add, cell add, cell add takes eleven each on average,
		// and the states behind a rebind, a clear and a drop lie beyond it
		for round := 0; round < repeatRounds; round++ {
			rt.Repeat(actions)
		}
	})
	t.Logf("actions=%d (ok=%d refused=%d)", tally.steps, tally.ok, tally.refused)
	t.Logf("desired-contract violations observed: onePlace=%d losslessBind=%d, expected under today's table.lua", tally.onePlace, tally.lossless)
	if tally.first != "" {
		t.Logf("first witness: %s", tally.first)
	}
	t.Logf("owned sets left behind a bound column by a row removal: %d; rows added again showing one: %d", tally.orphaned, tally.ghosts)
	if !contract && (tally.onePlace == 0 || tally.lossless == 0) {
		t.Errorf("the run reached no ONE PLACE or lossless-bind violation (onePlace=%d losslessBind=%d): today's table.lua permits both, so either the generator no longer reaches the gap or the Lua changed; if the corrected contract landed, run with NOVA_TABLE_CONTRACT=1 and make that the gate", tally.onePlace, tally.lossless)
	}
}

// The draws. An argument comes from the live state three draws in four (a
// live table, a present row of it, a member the table or the cell holds),
// so a sequence of some thirty actions reaches the states behind a rebind,
// a clear and a drop instead of refusing on absent tables; the fourth draw
// is from the whole universe, so every refusal is reached too.
func (h *harness) pick(label string, live, universe []string) string {
	if len(live) > 0 && rapid.IntRange(0, 3).Draw(h.t, label+" from live") != 0 {
		return rapid.SampledFrom(live).Draw(h.t, label)
	}
	return rapid.SampledFrom(universe).Draw(h.t, label)
}

func (h *harness) table() string               { return h.pick("table", h.m.liveNames(), propTables) }
func (h *harness) row(tn string) string        { return h.pick("row", h.m.rowsInOrder(tn), propRows) }
func (h *harness) member(held []string) string { return h.pick("member", held, propMembers) }
func (h *harness) column() string              { return rapid.SampledFrom(propColumns).Draw(h.t, "column") }
func (h *harness) cellColumn() string          { return rapid.SampledFrom(cellColumns).Draw(h.t, "column") }
func (h *harness) score() float64              { return rapid.SampledFrom(propScores).Draw(h.t, "score") }

// fail is every assertion's exit: the message, then the model's action log,
// beside the draw log rapid prints for the shrunk sequence.
func (h *harness) fail(format string, args ...any) {
	h.t.Helper()
	h.t.Fatalf("%s\nactions so far:\n%s", fmt.Sprintf(format, args...), h.m.trace())
}

// snapshot is every table key, the registry and the external set, DUMPed:
// what a refusal must leave byte-identical. An absent key is absent from
// the map.
func (h *harness) snapshot() map[string]string {
	h.t.Helper()
	keys, err := h.c.Keys(h.ctx, "table:*").Result()
	if err != nil {
		h.fail("KEYS table:*: %v", err)
	}
	keys = append(keys, ntable.Registry, extKey)
	pipe := h.c.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Dump(h.ctx, k)
	}
	if _, err := pipe.Exec(h.ctx); err != nil && !errors.Is(err, redis.Nil) {
		h.fail("DUMP pipeline: %v", err)
	}
	out := map[string]string{}
	for i, k := range keys {
		v, err := cmds[i].Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			h.fail("DUMP %s: %v", k, err)
		}
		out[k] = v
	}
	return out
}

// before is the snapshot a predicted refusal is held to, nothing otherwise.
func (h *harness) before(want verdict) map[string]string {
	if want == succeeds {
		return nil
	}
	return h.snapshot()
}

func diffSnapshots(before, after map[string]string) string {
	var out []string
	for k, v := range before {
		if w, ok := after[k]; !ok {
			out = append(out, k+" gone")
		} else if w != v {
			out = append(out, k+" changed")
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			out = append(out, k+" appeared")
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// expect holds a verb's answer to the model's verdict and, on a predicted
// refusal, the store to the snapshot taken before the call.
func (h *harness) expect(action string, want verdict, err error, before map[string]string) {
	h.t.Helper()
	if got := classify(err); got != want {
		h.fail("%s: the verb answered %s (%v); the model says %s", action, got, err, want)
	}
	h.tally.steps++
	if want == succeeds {
		h.tally.ok++
		return
	}
	h.tally.refused++
	if diff := diffSnapshots(before, h.snapshot()); diff != "" {
		h.fail("%s: refused %s, and the refusal changed the store: %s", action, want, diff)
	}
}

// checkExternal is invariants 4 and 5: the external set is what the owner
// wrote, whatever the table did.
func (h *harness) checkExternal(action string) {
	h.t.Helper()
	zs, err := h.c.ZRangeWithScores(h.ctx, extKey, 0, -1).Result()
	if err != nil {
		h.fail("%s: ZRANGE %s: %v", action, extKey, err)
	}
	if got := zsetOf(zs); !sameZset(got, h.m.ext) {
		h.fail("%s: the external set %s is %s; its owner wrote %s", action, extKey, got, h.m.ext)
	}
}

// contractAfter is the desired contract, read after an action that can
// break it: ONE PLACE, counted as the placements the action made or
// uncovered that put a member in two owned cells of one table (a cell add
// of a member held elsewhere, a shape change showing a hidden set again),
// and lossless bind, counted as the owned members of the table visible
// before the shape change and not after (deleted or hidden). Counted by
// default, a failure under NOVA_TABLE_CONTRACT=1.
func (h *harness) contractAfter(action, tn string, breachesBefore map[string][]string, visibleBefore map[string]bool) {
	h.t.Helper()
	breaches := h.m.onePlaceBreaches()
	for _, breach := range sortedKeys(breaches) {
		if breachesBefore[breach] == nil {
			h.violation(&h.tally.onePlace, "onePlace", fmt.Sprintf("%s: %s, and in %v", action, breach, breaches[breach]))
		}
	}
	after := h.m.ownedMembers(tn)
	for _, mem := range sortedKeys(visibleBefore) {
		if !after[mem] {
			h.violation(&h.tally.lossless, "losslessBind", fmt.Sprintf("%s: member %s was in an owned cell of table %s before the shape change and is in none after it (deleted or hidden)", action, mem, tn))
		}
	}
}

func (h *harness) violation(counter *int, kind, witness string) {
	h.t.Helper()
	if h.contract {
		h.fail("desired contract %s violated: %s", kind, witness)
	}
	*counter++
	if h.tally.first == "" {
		h.tally.first = kind + ": " + witness
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func columnNames(tb ntable.Table) string {
	names := make([]string, 0, len(tb.Columns))
	for _, c := range tb.Columns {
		names = append(names, c.Name)
	}
	return strings.Join(names, ",")
}

func rowKeys(tb ntable.Table) []string {
	keys := make([]string, 0, len(tb.Rows))
	for _, r := range tb.Rows {
		keys = append(keys, r.Key)
	}
	return keys
}

// cellAt names one cell.
type cellAt struct{ table, row, col string }

// verify is invariant 1, run before the first action and after every one:
// the store is the model, read through the verbs (List, Read, and
// CellMembers on one present cell per step, rotating, so every cell's
// members are read through the verb over a sequence at one trip a step),
// and, for the physical cells, through the keys.
func (h *harness) verify(*rapid.T) {
	h.t.Helper()
	ctx, c, m := h.ctx, h.c, h.m
	var cells []cellAt
	names, err := ntable.List(ctx, c)
	if err != nil {
		h.fail("list: %v", err)
	}
	if want := m.liveNames(); strings.Join(names, ",") != strings.Join(want, ",") {
		h.fail("the registry lists %v; the model has %v", names, want)
	}
	for _, tn := range propTables {
		tb, err := ntable.Read(ctx, c, tn)
		mt := m.live[tn]
		if mt == nil {
			if !errors.Is(err, ntable.ErrNoTable) {
				h.fail("table %s: read of a table the model does not have: %v; want ErrNoTable", tn, err)
			}
			continue
		}
		if err != nil {
			h.fail("table %s: read: %v", tn, err)
		}
		if got := columnNames(tb); got != "a,b,c" {
			h.fail("table %s: read gives columns %q; the definition is a,b,c", tn, got)
		}
		order := m.rowsInOrder(tn)
		if got := rowKeys(tb); strings.Join(got, ",") != strings.Join(order, ",") {
			h.fail("table %s: rows read back as %v; the model orders %v", tn, got, order)
		}
		for _, r := range tb.Rows {
			mr := mt.rows[r.Key]
			if r.Owner != mr.owner {
				h.fail("table %s row %s: read gives owner %q; the model has %q", tn, r.Key, r.Owner, mr.owner)
			}
			for j, col := range tb.Columns {
				key, set := m.shown(tn, r.Key, col.Name)
				cell := r.Cells[j]
				bound := mr.binds[col.Name] != ""
				if cell.Unread || cell.Bound != bound || cell.Key != key || cell.Count != int64(len(set)) {
					h.fail("table %s row %s column %s: read gives bound=%v key=%s count=%d unread=%v; the model shows bound=%v key=%s %s", tn, r.Key, col.Name, cell.Bound, cell.Key, cell.Count, cell.Unread, bound, key, set)
				}
				cells = append(cells, cellAt{tn, r.Key, col.Name})
			}
		}
	}
	if len(cells) > 0 {
		// the step index picks the cell, so the same draws read the same cell
		at := cells[len(m.log)%len(cells)]
		key, set := m.shown(at.table, at.row, at.col)
		ms, err := ntable.CellMembers(ctx, c, at.table, at.row, at.col)
		if err != nil {
			h.fail("table %s row %s column %s: cell members: %v", at.table, at.row, at.col, err)
		}
		if got := zsetOfMembers(ms); !sameZset(got, set) {
			h.fail("table %s row %s column %s: cell members %s; the model shows %s at %s", at.table, at.row, at.col, got, set, key)
		}
	}
	h.checkExternal("verify")
	h.verifyPhysical()
}

// verifyPhysical is the key space under table:*: every cell key is a
// non-empty set the model holds at that key, every non-empty set the model
// holds is a key, and every other key belongs to a live table and a present
// row.
func (h *harness) verifyPhysical() {
	h.t.Helper()
	keys, err := h.c.Keys(h.ctx, "table:*").Result()
	if err != nil {
		h.fail("KEYS table:*: %v", err)
	}
	sort.Strings(keys)
	var cellKeys []string
	for _, k := range keys {
		parts := strings.Split(k, ":")
		tn := parts[1]
		switch {
		case len(parts) == 5 && parts[2] == "cell":
			cellKeys = append(cellKeys, k)
		case len(parts) == 2, len(parts) == 3 && parts[2] == "rows":
			if h.m.live[tn] == nil {
				h.fail("key %s is in the store; the model has no table %s", k, tn)
			}
		case len(parts) == 4 && parts[2] == "row":
			if h.m.live[tn] == nil || h.m.live[tn].rows[parts[3]] == nil {
				h.fail("key %s is in the store; the model has no row %s in table %s", k, parts[3], tn)
			}
		default:
			h.fail("key %s is in the store and is no table key", k)
		}
	}
	pipe := h.c.Pipeline()
	cmds := make([]*redis.ZSliceCmd, len(cellKeys))
	for i, k := range cellKeys {
		cmds[i] = pipe.ZRangeWithScores(h.ctx, k, 0, -1)
	}
	if _, err := pipe.Exec(h.ctx); err != nil {
		h.fail("ZRANGE pipeline: %v", err)
	}
	seen := map[string]bool{}
	for i, k := range cellKeys {
		got, want := zsetOf(cmds[i].Val()), h.m.phys[k]
		if len(want) == 0 {
			h.fail("cell key %s is in the store holding %s; the model holds no set there", k, got)
		}
		if !sameZset(got, want) {
			h.fail("cell key %s holds %s; the model holds %s", k, got, want)
		}
		seen[k] = true
	}
	for _, k := range sortedKeys(h.m.phys) {
		if len(h.m.phys[k]) > 0 && !seen[k] {
			h.fail("the model holds %s at %s; the store has no such key", h.m.phys[k], k)
		}
	}
}

// The actions. Each draws its arguments, asks the model what the verb must
// do, calls the verb, holds the answer to that, then moves the model.

func (h *harness) create(*rapid.T) {
	tn := h.table()
	action := "create " + tn
	err := ntable.Create(h.ctx, h.c, propDefinition(tn), time.Now())
	h.expect(action, succeeds, err, nil)
	if h.m.live[tn] == nil {
		h.m.live[tn] = &modelTable{rows: map[string]*modelRow{}}
	}
	h.m.note("%s -> OK", action)
}

// rowAdd adds or rewrites a row, one draw in two binding one column to the
// external set. The Lua rewrites the row's metadata whole and keeps its
// owned sets.
func (h *harness) rowAdd(rt *rapid.T) {
	tn := h.table()
	r := h.row(tn)
	spec := ntable.RowSpec{}
	if rapid.Bool().Draw(rt, "bind") {
		spec.Binds = map[string]string{h.column(): extKey}
		spec.Owner = extOwner
	}
	action := fmt.Sprintf("rowAdd %s %s binds=%v", tn, r, spec.Binds)
	want := succeeds
	if h.m.live[tn] == nil {
		want = refused("notable")
	}
	before := h.before(want)
	breaches, visible := h.m.onePlaceBreaches(), h.m.ownedMembers(tn)
	row, err := ntable.RowAdd(h.ctx, h.c, tn, r, spec)
	h.expect(action, want, err, before)
	if want == succeeds {
		mt := h.m.live[tn]
		mr := mt.rows[r]
		if mr == nil {
			mr = &modelRow{rank: h.m.tailRank(tn) + 1}
			mt.rows[r] = mr
			// a set left behind when the row last went shows again
			for _, col := range propColumns {
				if spec.Binds[col] == "" && len(h.m.phys[ntable.CellKey(tn, r, col)]) > 0 {
					h.tally.ghosts++
				}
			}
		}
		mr.owner, mr.binds = spec.Owner, map[string]string{}
		for col, k := range spec.Binds {
			mr.binds[col] = k
		}
		// the row the verb hands back is the row as written
		for j, col := range propColumns {
			key, _ := h.m.shown(tn, r, col)
			if row.Key != r || row.Owner != spec.Owner || row.Cells[j].Bound != (mr.binds[col] != "") || row.Cells[j].Key != key {
				h.fail("%s: the verb handed back row %+v; the model wrote owner %q and column %s showing %s", action, row, spec.Owner, col, key)
			}
		}
		h.contractAfter(action, tn, breaches, visible)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

func (h *harness) rowDel(*rapid.T) {
	tn := h.table()
	r := h.row(tn)
	action := fmt.Sprintf("rowDel %s %s", tn, r)
	want := succeeds
	if h.m.live[tn] == nil {
		want = refused("notable")
	}
	present := want == succeeds && h.m.live[tn].rows[r] != nil
	before := h.before(want)
	gone, err := ntable.RowDel(h.ctx, h.c, tn, r)
	h.expect(action, want, err, before)
	if want == succeeds {
		if gone != present {
			h.fail("%s: the verb says removed=%v; the model had the row: %v", action, gone, present)
		}
		if present {
			h.tally.orphaned += h.m.removeRow(tn, r)
		}
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// bindKeepSubset binds the table to a subset of its current rows in their
// current order, each kept row bound on one column or none. The Lua
// creates a missing table, rewrites every kept row's metadata (rank, owner,
// bindings) and removes the omitted rows with their old bindings; it never
// refuses here.
func (h *harness) bindKeepSubset(rt *rapid.T) {
	tn := h.table()
	def := propDefinition(tn)
	current := h.m.rowsInOrder(tn)
	kept := map[string]map[string]string{}
	var order []string
	for _, r := range current {
		if !rapid.Bool().Draw(rt, "keep "+r) {
			continue
		}
		binds := map[string]string{}
		row := ntable.NewRow(def, r)
		if rapid.Bool().Draw(rt, "bind "+r) {
			col := h.column()
			binds[col] = extKey
			row.Cells[def.Column(col)] = ntable.Cell{Key: extKey, Bound: true}
			row.Owner = extOwner
		}
		def.Rows = append(def.Rows, row)
		kept[r], order = binds, append(order, r)
	}
	action := fmt.Sprintf("bind %s keep=%v binds=%v", tn, order, kept)
	breaches, visible := h.m.onePlaceBreaches(), h.m.ownedMembers(tn)
	err := ntable.Bind(h.ctx, h.c, def, time.Now())
	h.expect(action, succeeds, err, nil)
	mt := h.m.live[tn]
	if mt == nil {
		mt = &modelTable{rows: map[string]*modelRow{}}
		h.m.live[tn] = mt
	}
	for _, r := range current {
		if kept[r] == nil {
			h.tally.orphaned += h.m.removeRow(tn, r)
		}
	}
	for i, r := range order {
		mr := mt.rows[r]
		mr.rank, mr.binds, mr.owner = float64(i+1), kept[r], ""
		if len(kept[r]) > 0 {
			mr.owner = extOwner
		}
	}
	h.contractAfter(action, tn, breaches, visible)
	h.m.note("%s -> OK", action)
}

func (h *harness) cellAdd(*rapid.T) {
	tn := h.table()
	r, col := h.row(tn), h.cellColumn()
	mem, score := h.member(h.m.heldBy(tn)), h.score()
	action := fmt.Sprintf("cellAdd %s %s %s %s@%g", tn, r, col, mem, score)
	want := h.m.cellWrite(tn, r, col)
	before := h.before(want)
	breaches := h.m.onePlaceBreaches()
	n, err := ntable.CellAdd(h.ctx, h.c, tn, r, col, mem, score)
	h.expect(action, want, err, before)
	if want == succeeds {
		key := ntable.CellKey(tn, r, col)
		set := h.m.phys[key]
		if set == nil {
			set = zset{}
			h.m.phys[key] = set
		}
		set[mem] = score
		if n != int64(len(set)) {
			h.fail("%s: the verb counts %d in the cell; the model holds %s", action, n, set)
		}
		h.contractAfter(action, tn, breaches, nil)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

func (h *harness) cellRemove(*rapid.T) {
	tn := h.table()
	r, col := h.row(tn), h.cellColumn()
	mem := h.member(h.m.membersAt(ntable.CellKey(tn, r, col)))
	action := fmt.Sprintf("cellRemove %s %s %s %s", tn, r, col, mem)
	want := h.m.cellWrite(tn, r, col)
	before := h.before(want)
	n, err := ntable.CellRemove(h.ctx, h.c, tn, r, col, mem)
	h.expect(action, want, err, before)
	if want == succeeds {
		key := ntable.CellKey(tn, r, col)
		delete(h.m.phys[key], mem)
		if len(h.m.phys[key]) == 0 {
			delete(h.m.phys, key)
		}
		if n != int64(len(h.m.phys[key])) {
			h.fail("%s: the verb counts %d in the cell; the model holds %s", action, n, h.m.phys[key])
		}
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// cellMove moves a member between two columns of one row (the same column
// twice is a move onto itself). The Lua checks the source cell, then the
// destination, then that the member is in the source.
func (h *harness) cellMove(*rapid.T) {
	tn := h.table()
	r, from, to := h.row(tn), h.cellColumn(), h.cellColumn()
	fromKey, toKey := ntable.CellKey(tn, r, from), ntable.CellKey(tn, r, to)
	mem := h.member(h.m.membersAt(fromKey))
	action := fmt.Sprintf("cellMove %s %s %s->%s %s", tn, r, from, to, mem)
	want := h.m.cellWrite(tn, r, from)
	if want == succeeds {
		want = h.m.cellWrite(tn, r, to)
	}
	if want == succeeds {
		if _, in := h.m.phys[fromKey][mem]; !in {
			want = refused("notmember")
		}
	}
	before := h.before(want)
	n, err := ntable.CellMove(h.ctx, h.c, tn, r, from, to, mem)
	h.expect(action, want, err, before)
	if want == succeeds {
		score := h.m.phys[fromKey][mem]
		delete(h.m.phys[fromKey], mem)
		if len(h.m.phys[fromKey]) == 0 {
			delete(h.m.phys, fromKey)
		}
		set := h.m.phys[toKey]
		if set == nil {
			set = zset{}
			h.m.phys[toKey] = set
		}
		set[mem] = score
		if n != int64(len(set)) {
			h.fail("%s: the verb counts %d in the destination; the model holds %s", action, n, set)
		}
		// 3. a move keeps the member's score
		got, err := h.c.ZScore(h.ctx, toKey, mem).Result()
		if err != nil || got != score {
			h.fail("%s: the moved member %s scores %g at %s (%v); it had %g", action, mem, got, toKey, err, score)
		}
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// clear empties a table: refused whole, naming the first bound cell, when
// any cell is bound; otherwise every row and its owned sets go and the
// definition stays.
func (h *harness) clear(*rapid.T) {
	tn := h.table()
	action := "clear " + tn
	want := succeeds
	if h.m.live[tn] == nil {
		want = refused("notable")
	} else if v, bound := h.m.firstBound(tn); bound {
		want = v
	}
	before := h.before(want)
	rows := h.m.rowsInOrder(tn)
	n, err := ntable.Clear(h.ctx, h.c, tn)
	h.expect(action, want, err, before)
	if want == succeeds {
		for _, r := range rows {
			h.tally.orphaned += h.m.removeRow(tn, r)
		}
		if n != int64(len(rows)) {
			h.fail("%s: the verb counts %d rows cleared; the model had %d", action, n, len(rows))
		}
		// 4. the definition stays and the external set is untouched
		tb, err := ntable.Read(h.ctx, h.c, tn)
		if err != nil || columnNames(tb) != "a,b,c" || len(tb.Rows) != 0 {
			h.fail("%s: read after clear gives columns %q, %d rows (%v); want a,b,c and no rows", action, columnNames(tb), len(tb.Rows), err)
		}
		h.checkExternal(action)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// drop removes a table: its rows with their owned sets (the owned key behind
// a bound column stays, as on any row removal), its definition and its
// registry entry; the external set stays.
func (h *harness) drop(*rapid.T) {
	tn := h.table()
	action := "drop " + tn
	want := succeeds
	if h.m.live[tn] == nil {
		want = refused("notable")
	}
	before := h.before(want)
	rows := h.m.rowsInOrder(tn)
	n, err := ntable.Drop(h.ctx, h.c, tn)
	h.expect(action, want, err, before)
	if want == succeeds {
		for _, r := range rows {
			h.tally.orphaned += h.m.removeRow(tn, r)
		}
		delete(h.m.live, tn)
		if n != len(rows) {
			h.fail("%s: the verb counts %d rows dropped; the model had %d", action, n, len(rows))
		}
		// 5. drop leaves the external set
		h.checkExternal(action)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// externalAdd and externalRemove are the external set's owner writing its
// own set with raw commands; the table only ever shows it.
func (h *harness) externalAdd(*rapid.T) {
	mem, score := h.member(nil), h.score()
	action := fmt.Sprintf("externalAdd %s@%g", mem, score)
	if err := h.c.ZAdd(h.ctx, extKey, redis.Z{Score: score, Member: mem}).Err(); err != nil {
		h.fail("%s: %v", action, err)
	}
	h.tally.steps, h.tally.ok = h.tally.steps+1, h.tally.ok+1
	h.m.ext[mem] = score
	h.m.note("%s -> OK", action)
}

func (h *harness) externalRemove(*rapid.T) {
	mem := h.member(sortedKeys(h.m.ext))
	action := "externalRemove " + mem
	if err := h.c.ZRem(h.ctx, extKey, mem).Err(); err != nil {
		h.fail("%s: %v", action, err)
	}
	h.tally.steps, h.tally.ok = h.tally.steps+1, h.tally.ok+1
	delete(h.m.ext, mem)
	h.m.note("%s -> OK", action)
}
