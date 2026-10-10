//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// proptest_functional_test.go is gate item 3 of the line between layers
// (rowan-new SPEC-COORDINATOR section 9; Glenn 2026-09-27 ~1:30 PM ET: "Where
// is the line? When do we say, OK the foundation is done, now we build on
// top?"): "a state-machine property test drives random verb sequences on a
// real store and checks the layer's invariants after every step". rapid draws
// the sequences, the store is a throwaway redis-server with the library
// loaded, and the oracle is a Go model beside it. There are two oracles in
// one harness. The accepted contract is the default gate; the pinned old
// runtime can be studied explicitly with NOVA_TABLE_CONTRACT=0.
//
// BASELINE (NOVA_TABLE_CONTRACT=0) is a model of the pinned old table.lua
// (pkg/nsprint/fn/lua/table.lua), the behaviour Stella's TLA+ model of
// it describes (rowan-new specs/tla/TABLE-MODEL.md and TableMachine.tla,
// pinned at f77458853): physical owned sets kept apart from the metadata that
// shows them, so a shape change can hide one without deleting it; row-add
// rewriting a row's bindings and keeping its owned sets; bind deleting the
// owned cells of the rows it omits and rewriting every binding of the rows
// it keeps; clear refusing the whole table when any cell is bound; drop
// leaving the external set, and the owned key behind a cell that was bound
// when its row went.
//
// After every action the harness holds, in both modes:
//
//  1. the store is the model: the registry; every live table's definition
//     and rows in order; per cell Bound, Key and Count as Read gives them
//     and the members with their scores as CellMembers gives them (the
//     external set when bound, the owned set otherwise); the external set;
//     and every physical cell key under table:* (the orphans the baseline
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
// What the baseline deliberately does NOT assert: the desired contract the
// layer must reach before anything is built on it, ONE PLACE per table (a
// member in at most one owned cell of a table) and lossless bind (no owned
// member deleted or hidden by a shape change, bind or row-add). Today's
// table.lua holds neither (Stella's OnePlacePerTable and BindPreservesOwned
// counterexamples), so the baseline counts the violations its actions
// introduce and reports them at the end; a run that reaches none is red,
// because the generator no longer reaches the gap or the Lua changed under
// it. Out of scope, as in the TLA+ model: one writer (no interleaving),
// text columns, labels and excludes, bind targets other than the one
// external set (the alias witnesses), scores beyond {1, 2}, ACL, rendering.
//
// CANDIDATE (the default, or NOVA_TABLE_CONTRACT=1) is the accepted contract
// from SPEC-COORDINATOR section 7 (points 8 and 9), section 11 and
// MemberTable.tla, aligned with #4455. This default and #4455 must land in
// the same integration batch: a normal functional test run must exercise
// the contract without relying on a manually supplied environment flag.
// Missing contract machinery fails rather than selecting the old oracle.
//
//   - ONE PLACE per member per table inside the epoch (epoch 0 here, the
//     harness never advances it): the record table::member:<id> carries
//     place:<t> = row:col, written in the same commit as the set; cell add
//     refuses a member that has a place in that table (PLACED); move
//     requires the source to be the recorded place (NOTMEMBER otherwise)
//     and keeps the score; a remove of a member placed elsewhere or nowhere
//     is an accepted no-op: user state stays unchanged while one receipt
//     and revision commit; remove,
//     row-del, clear and drop clear the placements with the sets;
//   - lossless shape: row-add and bind refuse a shape that would delete or
//     hide an occupied owned cell (OCCUPIED), the whole store unchanged;
//   - no table-owned alias: a binding never names a key under table:*
//     (OWNEDALIAS), created or not; candidate mode draws such bindings;
//   - drop removes active-epoch presence, rows, bindings, cells and places.
//     Read returns ErrNoTable and List omits it; the stable template,
//     identity, revision and stream survive and Create can restore presence.
//     The harness does not call drop-definition or advance epochs;
//   - one revision/event per accepted mutation, including identical Create,
//     missing RowDel, absent Remove, empty Clear/Drop and self-Move; none
//     on refusal, whose whole-store snapshot includes the receipt ledger;
//   - read-only Check returns epoch, revision, placed-member count and
//     owned-cell count. Record/set contents are independently checked after
//     every step; missing contract machinery is a failing GAP.
//
// Every candidate check that needs a piece of the implementation is behind
// a presence check, never a silent pass: the member records, the revision
// hash, the change stream and ns_table_check each report a GAP when the
// store lacks them; the gaps ride every failure message and fail the run
// at its end; a mismatch in a piece that is present fails at once. The
// refusal vocabulary candidate mode expects on the wire is candidateTokens,
// one place to change if the implementation spells a reason differently.
//
// A failure prints rapid's shrunk draws, the seed that reproduces them and
// the model's action log. rapid's fail file is turned off from the test
// (rapidKeepsTheTreeClean): nothing a test writes lands in the tree.

// The universe: small, so rapid's default hundred checks, four Repeat
// rounds of some thirty actions each, finish in seconds and reach every
// corner many times over.
const (
	extKey   = "ext:members"    // the one external set a cell can be bound to
	extOwner = "external owner" // the row owner a bound refusal names
	badCol   = "z"              // a column no table has: NOCOL
	// checkFn is the store's read-only check candidate mode compares with
	// (the contract's Check), found by FUNCTION LIST before it is called.
	checkFn = "ns_table_check"
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
	// aliasKeys are binding targets some nova-table owns, created or not,
	// which the contract refuses (ALIAS); candidate mode draws them one
	// binding in four.
	aliasKeys = []string{ntable.CellKey("t1", "r1", "a"), ntable.CellKey("t2", "r2", "c"), ntable.RowsKey("t9")}
)

// The candidate contract's keys (section 7 point 8, section 11).
func recordKey(id string) string      { return "table::member:" + id }
func revisionKey(table string) string { return "table:" + table + ":revision" }
func changesKey(table string) string  { return "table:" + table + ":changes" }

// candidateTokens is the contract's refusal vocabulary as candidate mode
// reads it off the published Go error text, with raw reason tokens retained
// for a store.go that predates those sentinels. This file must also compile
// on the deliberately broken baseline, which lacks the new Go symbols.
var candidateTokens = []struct{ kind, token string }{
	{"occupied", "shape would delete or hide placed members"},
	{"alias", "binding target is table-owned storage"},
	{"placed", "member already has a place in this table"},
	{"occupied", "occupied"},
	{"alias", "alias"},
	{"stale", "stale"},
	{"revision", "revision"},
	{"opconflict", "opconflict"},
	{"opconflict", "operation id conflict"},
	{"memberexists", "memberexists"},
	{"memberrevision", "memberrevision"},
	{"drift", "drift"},
	{"fieldguard", "fieldguard"},
	{"twice", "twice"},
}

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

// tableOwned says a binding target is a key some nova-table owns, created
// or not: anything under table:*.
func tableOwned(key string) bool { return strings.HasPrefix(key, "table:") }

// rapidKeepsTheTreeClean turns rapid's fail file off for this process:
// rapid writes it under the package's testdata directory by default, and
// nothing a test writes may land in the tree (the test-output rule; a CI
// failure must not mutate the checkout). rapid has no option for another
// directory, only for reading a file back, so the flag is set here, once,
// before any rapid check of this package runs; the seed rapid prints
// reproduces a failure, and every failure message carries the shrunk
// action sequence, so nothing is lost.
var rapidTreeOnce sync.Once

func rapidKeepsTheTreeClean(t *testing.T) {
	t.Helper()
	rapidTreeOnce.Do(func() {
		require.NoError(t, flag.Set("rapid.nofailfile", "true"), "rapid.nofailfile")
	})
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

// place is a member's recorded cell in one table (candidate mode).
type place struct{ row, col string }

func (p place) String() string { return p.row + ":" + p.col }

// model is the oracle: the live tables with their rows and bindings, and,
// apart from them, every physical owned set by its Redis key, which the
// baseline's metadata can hide (a bound column) or orphan (a row removed
// while the column was bound, a dropped table) without deleting. In
// candidate mode it also holds the record's place per member and table and,
// per table, the accepted mutations (the revision, and the change events).
// log is the action log a failure prints.
type model struct {
	live map[string]*modelTable
	phys map[string]zset
	ext  zset
	log  []string

	candidate bool
	place     map[string]map[string]place
	mutations map[string]int
}

func newModel(candidate bool) *model {
	return &model{
		live: map[string]*modelTable{}, phys: map[string]zset{}, ext: zset{},
		candidate: candidate, place: map[string]map[string]place{}, mutations: map[string]int{},
	}
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

// The candidate contract in the model.

func (m *model) placeOf(mem, tn string) (place, bool) {
	p, ok := m.place[mem][tn]
	return p, ok
}

func (m *model) setPlace(mem, tn string, p place) {
	if m.place[mem] == nil {
		m.place[mem] = map[string]place{}
	}
	m.place[mem][tn] = p
}

// clearPlaces clears every placement in a table, or, with a row, every
// placement in that row of it: what a row-del, clear or drop does.
func (m *model) clearPlaces(tn, row string) {
	for mem := range m.place {
		if p, ok := m.place[mem][tn]; ok && (row == "" || p.row == row) {
			delete(m.place[mem], tn)
		}
	}
}

// mutated counts one accepted mutation of a table: the revision moves by
// one and the change stream gains one event.
func (m *model) mutated(tn string) { m.mutations[tn]++ }

// endangered is MemberTable.tla's ShapeKeepsOwned, negated, for one table
// and one proposed shape: the occupied owned cells of the table the shape
// would delete (their row omitted) or hide (their column bound), sorted
// "r.c"; empty when the shape is lossless. shape maps every row the shape
// keeps to its bindings.
func (m *model) endangered(tn string, shape map[string]map[string]string) []string {
	mt := m.live[tn]
	if mt == nil {
		return nil
	}
	var out []string
	for r, mr := range mt.rows {
		for _, col := range propColumns {
			if mr.binds[col] != "" || len(m.phys[ntable.CellKey(tn, r, col)]) == 0 {
				continue
			}
			if nb, kept := shape[r]; !kept || nb[col] != "" {
				out = append(out, r+"."+col)
			}
		}
	}
	sort.Strings(out)
	return out
}

// rowAddShape is the shape a row-add proposes: the current rows with their
// bindings, and the row as the spec writes it.
func (m *model) rowAddShape(tn, r string, binds map[string]string) map[string]map[string]string {
	shape := map[string]map[string]string{}
	for r0, mr := range m.live[tn].rows {
		shape[r0] = mr.binds
	}
	if binds == nil {
		binds = map[string]string{}
	}
	shape[r] = binds
	return shape
}

// shapeVerdict is the contract's answer to a row-add or a bind: ALIAS when
// a binding names a table-owned key, OCCUPIED when the shape would delete
// or hide an occupied owned cell, OK otherwise.
func shapeVerdict(targets, endangered []string) verdict {
	for _, k := range targets {
		if tableOwned(k) {
			if len(endangered) > 0 {
				return verdict{kind: "alias-or-occupied", why: "both an owned alias and a lossy shape; refusal priority is unspecified"}
			}
			return verdict{kind: "alias", why: "binding " + k + " names a table-owned key"}
		}
	}
	if len(endangered) > 0 {
		return verdict{kind: "occupied", why: "occupied owned cells " + strings.Join(endangered, " ") + " would be deleted or hidden"}
	}
	return succeeds
}

// bindTargets is every key a set of rows' bindings names.
func bindTargets(shape map[string]map[string]string) []string {
	var out []string
	for _, binds := range shape {
		for _, k := range binds {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// verdict is what the oracle says a verb does: it succeeds, or it refuses
// for one reason, a bound refusal naming the cell and its owner, or (the
// candidate's remove of a member placed elsewhere) it changes nothing,
// answering OK with one receipt. why is detail for the message, not compared.
type verdict struct {
	kind                 string
	row, col, key, owner string
	why                  string
}

var succeeds = verdict{kind: "ok"}

func refused(kind string) verdict { return verdict{kind: kind} }

func (v verdict) ok() bool { return v.kind == "ok" }

// matches says a verb's answer is the verdict: the kind, and for a bound
// refusal the cell and owner it names.
func (v verdict) matches(got verdict) bool {
	if v.kind == "alias-or-occupied" {
		return got.kind == "alias" || got.kind == "occupied"
	}
	if v.kind != got.kind {
		return false
	}
	if v.kind == "bound" {
		return v.row == got.row && v.col == got.col && v.key == got.key && v.owner == got.owner
	}
	return true
}

func (v verdict) String() string {
	s := v.kind
	if v.kind == "bound" {
		s = fmt.Sprintf("bound(%s.%s shows %s, owner %q)", v.row, v.col, v.key, v.owner)
	}
	if v.why != "" {
		s += " (" + v.why + ")"
	}
	return s
}

func outcome(v verdict) string {
	if v.ok() {
		return "OK"
	}
	if v.kind == "unchanged" {
		return "UNCHANGED"
	}
	return "REFUSED " + v.String()
}

// classify reads a verb's error as the store.go refusal it carries, or as
// one of the candidate contract's.
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
	case errors.Is(err, ntable.ErrOpConflict):
		return refused("opconflict")
	case errors.Is(err, ntable.ErrRevisionMismatch):
		return refused("revision")
	case errors.Is(err, ntable.ErrMemberRevision):
		return refused("memberrevision")
	case errors.Is(err, ntable.ErrMalformedManifest):
		return refused("manifest")
	case strings.Contains(err.Error(), "no such row"):
		return refused("norow")
	case strings.Contains(err.Error(), "no such column"):
		return refused("nocol")
	}
	text := strings.ToLower(err.Error())
	for _, c := range candidateTokens {
		if strings.Contains(text, c.token) {
			return refused(c.kind)
		}
	}
	return verdict{kind: "unexpected: " + err.Error()}
}

// contractTally counts, over the whole run, what the baseline lets through
// of the desired contract, what candidate mode found missing, and the
// actions.
type contractTally struct {
	onePlace, lossless int
	first              string
	// steps, ok and refused count the actions and how the verbs answered
	steps, ok, refused int
	// orphaned counts the non-empty owned sets a row removal left behind a
	// bound column, ghosts the rows added again that showed one of them
	orphaned, ghosts int
	// gaps are the pieces of the contract the store lacks (candidate mode),
	// in the order found, each once
	gaps     map[string]bool
	gapOrder []string
}

// harness is one rapid check: the store, the model beside it, and the tally
// shared by every check of the run.
type harness struct {
	t            *rapid.T
	ctx          context.Context
	c            *redis.Client
	m            *model
	tally        *contractTally
	checkPresent bool
}

// repeatRounds is how many rapid Repeat rounds one check runs; see the loop.
const repeatRounds = 4

// TestTableVerbsAgainstModel drives random sequences of nova-table's verbs
// (and the external owner's raw writes) against a throwaway redis-server
// and holds the store to the model after every one; see the file header.
func TestTableVerbsAgainstModel(t *testing.T) {
	t.Parallel()

	rapidKeepsTheTreeClean(t)
	candidate := true
	switch mode := os.Getenv("NOVA_TABLE_CONTRACT"); mode {
	case "", "1":
	case "0":
		candidate = false
	default:
		t.Fatalf("NOVA_TABLE_CONTRACT wants 0 (pinned baseline) or 1 (accepted contract), got %q", mode)
	}
	_, c := live(t)
	ctx := context.Background()
	tally := contractTally{gaps: map[string]bool{}}
	// the report runs even when rapid ends the test on a failure
	defer func() {
		t.Logf("actions=%d (ok=%d refused=%d)", tally.steps, tally.ok, tally.refused)
		if candidate {
			t.Logf("candidate mode: the accepted contract is the oracle (SPEC-COORDINATOR section 7, MemberTable.tla)")
			assert.Empty(t, tally.gapOrder, "candidate mode cannot pass: the store lacks pieces of the contract")
			return
		}
		t.Logf("desired-contract violations observed: onePlace=%d losslessBind=%d, expected under today's table.lua", tally.onePlace, tally.lossless)
		if tally.first != "" {
			t.Logf("first witness: %s", tally.first)
		}
		t.Logf("owned sets left behind a bound column by a row removal: %d; rows added again showing one: %d", tally.orphaned, tally.ghosts)
		const why = "today's table.lua permits both ONE PLACE and lossless-bind violations, so either the generator no longer reaches the gap or the Lua changed; if the corrected contract landed, run with NOVA_TABLE_CONTRACT=1 and make that the gate"
		assert.NotZero(t, tally.onePlace, "the run reached no ONE PLACE violation: %s", why)
		assert.NotZero(t, tally.lossless, "the run reached no lossless-bind violation: %s", why)
	}()
	checkPresent := false
	if candidate {
		checkPresent = storeHasFunction(t, ctx, c, checkFn)
	}
	rapid.Check(t, func(rt *rapid.T) {
		// every check starts from an empty store; the library survives FLUSHALL
		if err := c.FlushAll(ctx).Err(); err != nil {
			rt.Fatalf("flushall between checks: %v", err)
		}
		h := &harness{t: rt, ctx: ctx, c: c, m: newModel(candidate), tally: &tally, checkPresent: checkPresent}
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
}

// storeHasFunction is the presence check for a store function: FUNCTION
// LIST names it. Read once per test.
func storeHasFunction(t *testing.T, ctx context.Context, c *redis.Client, name string) bool {
	t.Helper()
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	require.NoError(t, err, "FUNCTION LIST")
	for _, lib := range libs {
		for _, fn := range lib.Functions {
			if fn.Name == name {
				return true
			}
		}
	}
	return false
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

// bindTarget is what a binding names: the external set, and in candidate
// mode one draw in four a table-owned key, which the contract refuses.
func (h *harness) bindTarget() string {
	if h.m.candidate && rapid.IntRange(0, 3).Draw(h.t, "alias") == 0 {
		return rapid.SampledFrom(aliasKeys).Draw(h.t, "alias key")
	}
	return extKey
}

// oracle names the model a message speaks for.
func (h *harness) oracle() string {
	if h.m.candidate {
		return "contract"
	}
	return "model"
}

// fail is every assertion's exit: the message, then the model's action log,
// beside the draw log rapid prints for the shrunk sequence, and in
// candidate mode the gaps found so far.
func (h *harness) fail(format string, args ...any) {
	h.t.Helper()
	msg := fmt.Sprintf(format, args...)
	if len(h.tally.gapOrder) > 0 {
		msg += "\ncandidate gaps so far:\n  " + strings.Join(h.tally.gapOrder, "\n  ")
	}
	h.t.Fatalf("%s\nactions so far:\n%s", msg, h.m.trace())
}

// Errorf, FailNow and Helper make the harness a require.TestingT: a failed
// require.X(h, ...) check exits through fail, with the action trace.
func (h *harness) Errorf(format string, args ...any) { h.t.Helper(); h.fail(format, args...) }
func (h *harness) FailNow()                          { h.t.FailNow() }
func (h *harness) Helper()                           { h.t.Helper() }

// gap records a piece of the contract the store lacks, once.
func (h *harness) gap(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if !h.tally.gaps[msg] {
		h.tally.gaps[msg] = true
		h.tally.gapOrder = append(h.tally.gapOrder, msg)
	}
}

// snapshot is every table key, the registry and the external set, DUMPed:
// what a refusal must leave byte-identical. An absent key is absent from
// the map. In candidate mode the records, the revision hash and the change
// stream are table keys too, so a refusal that bumped or appended shows.
func (h *harness) snapshot() map[string]string {
	h.t.Helper()
	keys, err := h.c.Keys(h.ctx, "table:*").Result()
	require.NoError(h, err, "KEYS table:*")
	keys = append(keys, ntable.Registry, extKey)
	pipe := h.c.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Dump(h.ctx, k)
	}
	if _, err := pipe.Exec(h.ctx); !errors.Is(err, redis.Nil) {
		require.NoError(h, err, "DUMP pipeline")
	}
	out := map[string]string{}
	for i, k := range keys {
		v, err := cmds[i].Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		require.NoError(h, err, "DUMP %s", k)
		out[k] = v
	}
	return out
}

// before is the snapshot a predicted refusal (or an unchanged answer) is
// held to, nothing for a predicted success.
func (h *harness) before(want verdict) map[string]string {
	if want.ok() {
		return nil
	}
	if want.kind == "unchanged" {
		return h.userSnapshot()
	}
	return h.snapshot()
}

// An accepted no-op changes only the receipt ledger. Preserve the full
// remaining store comparison, including every other definition field.
func (h *harness) userSnapshot() map[string]string {
	out := h.snapshot()
	for key := range out {
		if strings.HasSuffix(key, ":revision") || strings.HasSuffix(key, ":changes") || strings.HasSuffix(key, ":ops") {
			delete(out, key)
		} else if strings.HasSuffix(key, ":definition") {
			fields, err := h.c.HGetAll(h.ctx, key).Result()
			require.NoError(h, err, "read definition for no-op comparison")
			delete(fields, "_revision")
			body, err := json.Marshal(fields)
			require.NoError(h, err, "encode definition for no-op comparison")
			out[key] = string(body)
		}
	}
	return out
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

// expect holds a verb's answer to the oracle's verdict and, on a predicted
// refusal or an unchanged answer, the store to the snapshot taken before
// the call.
func (h *harness) expect(action string, want verdict, err error, before map[string]string) {
	h.t.Helper()
	got := classify(err)
	if want.kind == "unchanged" {
		require.True(h, got.ok(), "%s: the verb answered %s (%v); the contract says accepted no-op", action, got, err)
		require.Empty(h, diffSnapshots(before, h.userSnapshot()), "%s: accepted no-op changed user state", action)
	} else {
		require.True(h, want.matches(got), "%s: the verb answered %s (%v); the %s says %s", action, got, err, h.oracle(), want)
	}
	h.tally.steps++
	if want.ok() || want.kind == "unchanged" {
		h.tally.ok++
		return
	}
	h.tally.refused++
	require.Empty(h, diffSnapshots(before, h.snapshot()), "%s: %s, and the store changed", action, want)
}

// checkExternal is invariants 4 and 5: the external set is what the owner
// wrote, whatever the table did.
func (h *harness) checkExternal(action string) {
	h.t.Helper()
	zs, err := h.c.ZRangeWithScores(h.ctx, extKey, 0, -1).Result()
	require.NoError(h, err, "%s: ZRANGE %s", action, extKey)
	got := zsetOf(zs)
	require.True(h, sameZset(got, h.m.ext), "%s: the external set %s is %s; its owner wrote %s", action, extKey, got, h.m.ext)
}

// contractAfter is the desired contract, read after an action that can
// break it: ONE PLACE, counted as the placements the action made or
// uncovered that put a member in two owned cells of one table (a cell add
// of a member held elsewhere, a shape change showing a hidden set again),
// and lossless bind, counted as the owned members of the table visible
// before the shape change and not after (deleted or hidden). Counted in
// baseline mode; in candidate mode the oracle refuses these, so a breach
// here is a fault of the model itself and fails.
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
	require.False(h, h.m.candidate, "the candidate model let a desired-contract breach through, %s: %s", kind, witness)
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
// and, for the physical cells, through the keys; then the candidate's own
// invariant in candidate mode.
func (h *harness) verify(*rapid.T) {
	h.t.Helper()
	ctx, c, m := h.ctx, h.c, h.m
	var cells []cellAt
	names, err := ntable.List(ctx, c)
	require.NoError(h, err, "list")
	require.Equal(h, strings.Join(m.liveNames(), ","), strings.Join(names, ","), "the registry lists the tables the %s has", h.oracle())
	for _, tn := range propTables {
		tb, err := ntable.Read(ctx, c, tn)
		mt := m.live[tn]
		if mt == nil {
			require.ErrorIs(h, err, ntable.ErrNoTable, "table %s: read of a table the %s does not have", tn, h.oracle())
			continue
		}
		require.NoError(h, err, "table %s: read; the %s has the table", tn, h.oracle())
		require.Equal(h, "a,b,c", columnNames(tb), "table %s: read gives other columns; the definition is a,b,c", tn)
		order := m.rowsInOrder(tn)
		require.Equal(h, strings.Join(order, ","), strings.Join(rowKeys(tb), ","), "table %s: rows read back; the %s orders %v", tn, h.oracle(), order)
		for _, r := range tb.Rows {
			mr := mt.rows[r.Key]
			require.Equal(h, mr.owner, r.Owner, "table %s row %s: read gives another owner than the model has", tn, r.Key)
			for j, col := range tb.Columns {
				key, set := m.shown(tn, r.Key, col.Name)
				cell := r.Cells[j]
				bound := mr.binds[col.Name] != ""
				require.False(h, cell.Unread, "table %s row %s column %s: read gives an unread cell", tn, r.Key, col.Name)
				require.Equal(h, bound, cell.Bound, "table %s row %s column %s: read gives another bound flag than the %s shows", tn, r.Key, col.Name, h.oracle())
				require.Equal(h, key, cell.Key, "table %s row %s column %s: read gives another key than the %s shows", tn, r.Key, col.Name, h.oracle())
				require.Equal(h, int64(len(set)), cell.Count, "table %s row %s column %s: read gives another count than the %s shows: %s", tn, r.Key, col.Name, h.oracle(), set)
				cells = append(cells, cellAt{tn, r.Key, col.Name})
			}
		}
	}
	if len(cells) > 0 {
		// the step index picks the cell, so the same draws read the same cell
		at := cells[len(m.log)%len(cells)]
		key, set := m.shown(at.table, at.row, at.col)
		ms, err := ntable.CellMembers(ctx, c, at.table, at.row, at.col)
		require.NoError(h, err, "table %s row %s column %s: cell members", at.table, at.row, at.col)
		got := zsetOfMembers(ms)
		require.True(h, sameZset(got, set), "table %s row %s column %s: cell members %s; the %s shows %s at %s", at.table, at.row, at.col, got, h.oracle(), set, key)
	}
	h.checkExternal("verify")
	h.verifyPhysical()
	if m.candidate {
		h.verifyCandidate()
	}
}

// verifyPhysical is the key space under table:*: every cell key is a
// non-empty set the model holds at that key, every non-empty set the model
// holds is a key; row keys require active presence. Candidate template,
// identity, epoch snapshot and receipt keys survive Drop and are checked in
// verifyCandidate. Member identities survive unlinking as well.
func (h *harness) verifyPhysical() {
	h.t.Helper()
	keys, err := h.c.Keys(h.ctx, "table:*").Result()
	require.NoError(h, err, "KEYS table:*")
	sort.Strings(keys)
	var cellKeys []string
	for _, k := range keys {
		parts := strings.Split(k, ":")
		tn := parts[1]
		switch {
		case len(parts) == 5 && parts[2] == "cell":
			cellKeys = append(cellKeys, k)
		case len(parts) == 4 && parts[1] == "" && parts[2] == "member":
			// a member record, table::member:<id>
		case len(parts) == 3 && (parts[2] == "revision" || parts[2] == "changes" || parts[2] == "identity" || parts[2] == "definition"):
			require.False(h, h.m.candidate && h.m.mutations[tn] == 0, "metadata %s belongs to an unknown table", k)
		case len(parts) == 2:
			require.False(h, h.m.live[tn] == nil && !(h.m.candidate && h.m.mutations[tn] > 0), "template %s belongs to an unknown table", k)
		case len(parts) == 3 && parts[2] == "rows":
			require.NotNil(h, h.m.live[tn], "key %s is in the store; the %s has no table %s", k, h.oracle(), tn)
		case len(parts) == 4 && parts[2] == "row":
			require.NotNil(h, h.m.live[tn], "key %s is in the store; the %s has no table %s", k, h.oracle(), tn)
			require.NotNil(h, h.m.live[tn].rows[parts[3]], "key %s is in the store; the %s has no row %s in table %s", k, h.oracle(), parts[3], tn)
		case len(parts) == 3 && parts[2] == "ops":
			// batch operation records: table:<tn>:ops
		default:
			h.fail("key %s is in the store and is no table key", k)
		}
	}
	pipe := h.c.Pipeline()
	cmds := make([]*redis.ZSliceCmd, len(cellKeys))
	for i, k := range cellKeys {
		cmds[i] = pipe.ZRangeWithScores(h.ctx, k, 0, -1)
	}
	_, err = pipe.Exec(h.ctx)
	require.NoError(h, err, "ZRANGE pipeline")
	seen := map[string]bool{}
	for i, k := range cellKeys {
		got, want := zsetOf(cmds[i].Val()), h.m.phys[k]
		require.NotEmpty(h, want, "cell key %s is in the store holding %s; the %s holds no set there", k, got, h.oracle())
		require.True(h, sameZset(got, want), "cell key %s holds %s; the %s holds %s", k, got, h.oracle(), want)
		seen[k] = true
	}
	for _, k := range sortedKeys(h.m.phys) {
		if len(h.m.phys[k]) > 0 {
			require.True(h, seen[k], "the %s holds %s at %s; the store has no such key", h.oracle(), h.m.phys[k], k)
		}
	}
}

// verifyCandidate is candidate mode's own invariant, each piece behind a
// presence check that records a GAP when the store lacks it: per known
// table (even when dropped) the revision counter and the change stream, then the member
// records' places, then the store's check function.
func (h *harness) verifyCandidate() {
	h.t.Helper()
	ctx, c, m := h.ctx, h.c, h.m
	for _, tn := range sortedKeys(m.mutations) {
		want := m.mutations[tn]
		for _, suffix := range []string{"", ":identity", ":definition"} {
			key := ntable.DefKey(tn) + suffix
			hash, err := c.HGetAll(ctx, key).Result()
			require.NoError(h, err, "read retained table metadata %s", key)
			if len(hash) == 0 {
				h.gap("retained table metadata absent: %s", key)
				continue
			}
			if suffix == ":identity" {
				require.Empty(h, hash["epoch_key"], "table %s identity changed: %v", tn, hash)
				require.Equal(h, "n", hash["epoch_field"], "table %s identity changed: %v", tn, hash)
				require.Equal(h, "table::member:", hash["member_prefix"], "table %s identity changed: %v", tn, hash)
				continue
			}
			require.Equal(h, "a,b,c", hash["order"], "table %s retained definition changed: %v", tn, hash)
			if suffix == ":definition" {
				present := "0"
				if m.live[tn] != nil {
					present = "1"
				}
				require.Equal(h, present, hash["_present"], "table %s epoch snapshot=%v; model presence=%s revision=%d", tn, hash, present, want)
				require.Equal(h, strconv.Itoa(want), hash["_revision"], "table %s epoch snapshot=%v; model presence=%s revision=%d", tn, hash, present, want)
			}
		}
		n, err := c.HGet(ctx, revisionKey(tn), "n").Result()
		switch {
		case errors.Is(err, redis.Nil):
			if want > 0 {
				h.gap("revision counter absent: %s field n, wanted from the first accepted mutation of %s", revisionKey(tn), tn)
			}
		case err != nil:
			h.fail("table %s: HGET %s n: %v", tn, revisionKey(tn), err)
		default:
			got, perr := strconv.Atoi(n)
			require.NoError(h, perr, "table %s: revision n=%q; the contract moves it once per accepted mutation, %d so far", tn, n, want)
			require.Equal(h, want, got, "table %s: revision n=%q; the contract moves it once per accepted mutation, %d so far", tn, n, want)
		}
		kind, err := c.Type(ctx, changesKey(tn)).Result()
		switch {
		case err != nil:
			h.fail("table %s: TYPE %s: %v", tn, changesKey(tn), err)
		case kind == "none":
			if want > 0 {
				h.gap("change stream absent: %s, wanted from the first accepted mutation of %s", changesKey(tn), tn)
			}
		case kind != "stream":
			h.fail("table %s: %s is a %s; the contract makes it a stream", tn, changesKey(tn), kind)
		default:
			got, err := c.XLen(ctx, changesKey(tn)).Result()
			require.NoError(h, err, "table %s: XLEN %s", tn, changesKey(tn))
			require.Equal(h, int64(want), got, "table %s: change events on %s; the contract appends one per accepted mutation, %d so far", tn, changesKey(tn), want)
			events, err := c.XRevRangeN(ctx, changesKey(tn), "+", "-", 1).Result()
			require.NoError(h, err, "table %s: latest receipt missing", tn)
			require.Len(h, events, 1, "table %s: latest receipt missing", tn)
			fields := events[0].Values
			require.Equal(h, "0", fmt.Sprint(fields["epoch"]), "table %s: latest receipt has wrong epoch/revision: %v", tn, fields)
			require.Equal(h, strconv.Itoa(want-1), fmt.Sprint(fields["rev_before"]), "table %s: latest receipt has wrong epoch/revision: %v", tn, fields)
			require.Equal(h, strconv.Itoa(want), fmt.Sprint(fields["rev_after"]), "table %s: latest receipt has wrong epoch/revision: %v", tn, fields)
		}
	}
	h.verifyRecords()
	h.verifyCheck()
}

// verifyRecords is the record/place link from the record's side: for every
// member, place:<t> on table::member:<id> is the cell the model placed it
// in, absent where it has none, and the record's epoch is the active one
// (0); the set's side is invariant 1. A store with no record for a placed
// member is a GAP.
func (h *harness) verifyRecords() {
	h.t.Helper()
	ctx, c, m := h.ctx, h.c, h.m
	pipe := c.Pipeline()
	cmds := map[string]*redis.MapStringStringCmd{}
	for _, mem := range propMembers {
		cmds[mem] = pipe.HGetAll(ctx, recordKey(mem))
	}
	_, err := pipe.Exec(ctx)
	require.NoError(h, err, "HGETALL records")
	for _, mem := range propMembers {
		rec, err := cmds[mem].Result()
		require.NoError(h, err, "HGETALL %s", recordKey(mem))
		placed := m.place[mem]
		if len(rec) == 0 {
			if _, known := m.place[mem]; known {
				h.gap("member record absent: %s, identity must survive unlinking", recordKey(mem))
			}
			continue
		}
		require.Equal(h, "0", rec["epoch"], "record %s epoch; the active epoch is 0 and the harness never advances it", recordKey(mem))
		for _, tn := range propTables {
			got, has := rec["place:"+tn]
			p, want := placed[tn]
			if want {
				require.True(h, has, "record %s place:%s is absent; the contract records %s, the owned cell whose set holds %s", recordKey(mem), tn, p, mem)
				require.Equal(h, p.String(), got, "record %s place:%s; the contract records %s, the owned cell whose set holds %s", recordKey(mem), tn, p, mem)
			} else {
				require.Empty(h, got, "record %s place:%s; %s has no place in %s", recordKey(mem), tn, mem, tn)
			}
		}
	}
}

// checkReply is the published read-only wire: CHECK, epoch, revision,
// placed-member count, owned-cell count. Contents are verified independently
// from the actual records and sets, never inferred from these summary counts.
type checkReply struct {
	epoch, revision, members, cells uint64
}

func decodeCheck(reply []any) (checkReply, error) {
	var out checkReply
	if len(reply) != 5 || fmt.Sprint(reply[0]) != "CHECK" {
		return out, fmt.Errorf("want CHECK and four counts, got %v", reply)
	}
	for i, target := range []*uint64{&out.epoch, &out.revision, &out.members, &out.cells} {
		n, err := strconv.ParseUint(fmt.Sprint(reply[i+1]), 10, 64)
		if err != nil {
			return out, fmt.Errorf("check field %d is not uint64: %v", i+1, reply[i+1])
		}
		*target = n
	}
	return out, nil
}

// verifyCheck compares the store's read-only check with the model for
// every live table when the store has the function (checkPresent, read
// once per test); a store without it is a GAP. The call goes to the store
// function by name: swap it for ntable.Check when the package has it.
func (h *harness) verifyCheck() {
	h.t.Helper()
	if !h.checkPresent {
		h.gap("store function %s absent: the Check comparison (epoch, revision, members, cells) is skipped", checkFn)
		return
	}
	ctx, c, m := h.ctx, h.c, h.m
	for _, tn := range m.liveNames() {
		reply, err := c.FCallRO(ctx, checkFn, []string{ntable.DefKey(tn)}, tn).Slice()
		require.NoError(h, err, "table %s: %s", tn, checkFn)
		got, err := decodeCheck(reply)
		require.NoError(h, err, "table %s: %s answered a shape the harness cannot read; align decodeCheck with the API", tn, checkFn)
		require.Zero(h, got.epoch, "table %s: check epoch; the active epoch is 0", tn)
		require.Equal(h, uint64(m.mutations[tn]), got.revision, "table %s: check revision; the contract counts %d accepted mutations", tn, m.mutations[tn])
		var cells uint64
		for _, r := range m.rowsInOrder(tn) {
			for _, col := range propColumns {
				if m.live[tn].rows[r].binds[col] == "" {
					cells++
				}
			}
		}
		members := uint64(len(m.ownedMembers(tn)))
		require.Equal(h, members, got.members, "table %s: check counts members=%d cells=%d; independent model owns members=%d cells=%d", tn, got.members, got.cells, members, cells)
		require.Equal(h, cells, got.cells, "table %s: check counts members=%d cells=%d; independent model owns members=%d cells=%d", tn, got.members, got.cells, members, cells)
	}
}

// The actions. Each draws its arguments, asks the oracle what the verb must
// do, calls the verb, holds the answer to that, then moves the model.

func (h *harness) create(*rapid.T) {
	tn := h.table()
	action := "create " + tn
	absent := h.m.live[tn] == nil
	err := ntable.Create(h.ctx, h.c, propDefinition(tn), time.Now())
	h.expect(action, succeeds, err, nil)
	if absent {
		h.m.live[tn] = &modelTable{rows: map[string]*modelRow{}}
	}
	h.m.mutated(tn)
	h.m.note("%s -> OK", action)
}

// rowAdd adds or rewrites a row, one draw in two binding one column. The
// baseline rewrites the row's metadata whole and keeps its owned sets; the
// contract refuses a table-owned target (ALIAS) and a binding that would
// hide an occupied owned cell (OCCUPIED).
func (h *harness) rowAdd(rt *rapid.T) {
	tn := h.table()
	r := h.row(tn)
	spec := ntable.RowSpec{}
	if rapid.Bool().Draw(rt, "bind") {
		spec.Binds = map[string]string{h.column(): h.bindTarget()}
		spec.Owner = extOwner
	}
	action := fmt.Sprintf("rowAdd %s %s binds=%v", tn, r, spec.Binds)
	want := succeeds
	if h.m.live[tn] == nil {
		want = refused("notable")
	} else if h.m.candidate {
		shape := h.m.rowAddShape(tn, r, spec.Binds)
		want = shapeVerdict(bindTargets(map[string]map[string]string{r: spec.Binds}), h.m.endangered(tn, shape))
	}
	before := h.before(want)
	breaches, visible := h.m.onePlaceBreaches(), h.m.ownedMembers(tn)
	row, err := ntable.RowAdd(h.ctx, h.c, tn, r, spec)
	h.expect(action, want, err, before)
	if want.ok() {
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
			require.Equal(h, r, row.Key, "%s: the verb handed back row %+v; the model wrote owner %q and column %s showing %s", action, row, spec.Owner, col, key)
			require.Equal(h, spec.Owner, row.Owner, "%s: the verb handed back row %+v; the model wrote owner %q and column %s showing %s", action, row, spec.Owner, col, key)
			require.Equal(h, mr.binds[col] != "", row.Cells[j].Bound, "%s: the verb handed back row %+v; the model wrote owner %q and column %s showing %s", action, row, spec.Owner, col, key)
			require.Equal(h, key, row.Cells[j].Key, "%s: the verb handed back row %+v; the model wrote owner %q and column %s showing %s", action, row, spec.Owner, col, key)
		}
		h.m.mutated(tn)
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
	present := want.ok() && h.m.live[tn].rows[r] != nil
	before := h.before(want)
	gone, err := ntable.RowDel(h.ctx, h.c, tn, r)
	h.expect(action, want, err, before)
	if want.ok() {
		require.Equal(h, present, gone, "%s: the verb's removed flag; the %s had the row: %v", action, h.oracle(), present)
		if present {
			h.tally.orphaned += h.m.removeRow(tn, r)
			h.m.clearPlaces(tn, r)
		}
		h.m.mutated(tn)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// bindKeepSubset binds the table to a subset of its current rows in their
// current order, each kept row bound on one column or none. The baseline
// creates a missing table, rewrites every kept row's metadata (rank, owner,
// bindings) and removes the omitted rows with their old bindings, never
// refusing; the contract refuses a table-owned target (ALIAS) and a shape
// that would delete or hide an occupied owned cell (OCCUPIED).
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
			col, target := h.column(), h.bindTarget()
			binds[col] = target
			row.Cells[def.Column(col)] = ntable.Cell{Key: target, Bound: true}
			row.Owner = extOwner
		}
		def.Rows = append(def.Rows, row)
		kept[r], order = binds, append(order, r)
	}
	action := fmt.Sprintf("bind %s keep=%v binds=%v", tn, order, kept)
	want := succeeds
	if h.m.candidate {
		want = shapeVerdict(bindTargets(kept), h.m.endangered(tn, kept))
	}
	before := h.before(want)
	breaches, visible := h.m.onePlaceBreaches(), h.m.ownedMembers(tn)
	err := ntable.Bind(h.ctx, h.c, def, time.Now())
	h.expect(action, want, err, before)
	if want.ok() {
		mt := h.m.live[tn]
		if mt == nil {
			mt = &modelTable{rows: map[string]*modelRow{}}
			h.m.live[tn] = mt
		}
		for _, r := range current {
			if kept[r] == nil {
				h.tally.orphaned += h.m.removeRow(tn, r)
				h.m.clearPlaces(tn, r)
			}
		}
		for i, r := range order {
			mr := mt.rows[r]
			mr.rank, mr.binds, mr.owner = float64(i+1), kept[r], ""
			if len(kept[r]) > 0 {
				mr.owner = extOwner
			}
		}
		h.m.mutated(tn)
		h.contractAfter(action, tn, breaches, visible)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// cellAdd puts a member in a cell. The contract refuses a member that has a
// place in the table (PLACED), the same cell included, and records the
// place with the set otherwise.
func (h *harness) cellAdd(*rapid.T) {
	tn := h.table()
	r, col := h.row(tn), h.cellColumn()
	mem, score := h.member(h.m.heldBy(tn)), h.score()
	action := fmt.Sprintf("cellAdd %s %s %s %s@%g", tn, r, col, mem, score)
	want := h.m.cellWrite(tn, r, col)
	if want.ok() && h.m.candidate {
		if p, placed := h.m.placeOf(mem, tn); placed {
			want = verdict{kind: "placed", why: fmt.Sprintf("%s has a place in %s at %s", mem, tn, p)}
		}
	}
	before := h.before(want)
	breaches := h.m.onePlaceBreaches()
	n, err := ntable.CellAdd(h.ctx, h.c, tn, r, col, mem, score)
	h.expect(action, want, err, before)
	if want.ok() {
		key := ntable.CellKey(tn, r, col)
		set := h.m.phys[key]
		if set == nil {
			set = zset{}
			h.m.phys[key] = set
		}
		set[mem] = score
		require.Equal(h, int64(len(set)), n, "%s: the verb's count in the cell; the %s holds %s", action, h.oracle(), set)
		h.m.setPlace(mem, tn, place{r, col})
		h.m.mutated(tn)
		h.contractAfter(action, tn, breaches, nil)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// cellRemove takes a member out of a cell. The contract clears the place
// with the set; a member placed elsewhere or nowhere leaves the store
// unchanged, with OK and one revision/event as an accepted no-op.
func (h *harness) cellRemove(*rapid.T) {
	tn := h.table()
	r, col := h.row(tn), h.cellColumn()
	key := ntable.CellKey(tn, r, col)
	mem := h.member(h.m.membersAt(key))
	action := fmt.Sprintf("cellRemove %s %s %s %s", tn, r, col, mem)
	want := h.m.cellWrite(tn, r, col)
	if want.ok() && h.m.candidate {
		if p, placed := h.m.placeOf(mem, tn); !placed || p != (place{r, col}) {
			want = verdict{kind: "unchanged", why: fmt.Sprintf("%s is not placed at %s.%s", mem, r, col)}
		}
	}
	before := h.before(want)
	n, err := ntable.CellRemove(h.ctx, h.c, tn, r, col, mem)
	h.expect(action, want, err, before)
	switch {
	case want.ok():
		delete(h.m.phys[key], mem)
		if len(h.m.phys[key]) == 0 {
			delete(h.m.phys, key)
		}
		require.Equal(h, int64(len(h.m.phys[key])), n, "%s: the verb's count in the cell; the %s holds %s", action, h.oracle(), h.m.phys[key])
		delete(h.m.place[mem], tn)
		h.m.mutated(tn)
	case want.kind == "unchanged" && err == nil:
		require.Equal(h, int64(len(h.m.phys[key])), n, "%s: the verb answered OK; the cell holds %s unchanged", action, h.m.phys[key])
		h.m.mutated(tn)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// cellMove moves a member between two columns of one row; selecting the
// same column is an accepted no-op in candidate mode. The Lua checks the source
// cell, then the destination, then that the member is in the source; the
// contract requires the source to be the member's recorded place and moves
// the place with the set.
func (h *harness) cellMove(*rapid.T) {
	tn := h.table()
	r, from := h.row(tn), h.cellColumn()
	to := h.cellColumn()
	fromKey, toKey := ntable.CellKey(tn, r, from), ntable.CellKey(tn, r, to)
	mem := h.member(h.m.membersAt(fromKey))
	action := fmt.Sprintf("cellMove %s %s %s->%s %s", tn, r, from, to, mem)
	want := h.m.cellWrite(tn, r, from)
	if want.ok() {
		want = h.m.cellWrite(tn, r, to)
	}
	if want.ok() {
		if h.m.candidate {
			if p, placed := h.m.placeOf(mem, tn); !placed || p != (place{r, from}) {
				want = verdict{kind: "notmember", why: fmt.Sprintf("%s.%s is not the recorded place of %s", r, from, mem)}
			}
		} else if _, in := h.m.phys[fromKey][mem]; !in {
			want = refused("notmember")
		}
	}
	before := h.before(want)
	n, err := ntable.CellMove(h.ctx, h.c, tn, r, from, to, mem)
	h.expect(action, want, err, before)
	if want.ok() {
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
		require.Equal(h, int64(len(set)), n, "%s: the verb's count in the destination; the %s holds %s", action, h.oracle(), set)
		// 3. a move keeps the member's score
		got, err := h.c.ZScore(h.ctx, toKey, mem).Result()
		require.NoError(h, err, "%s: the moved member %s at %s", action, mem, toKey)
		require.Equal(h, score, got, "%s: the moved member %s at %s keeps its score", action, mem, toKey)
		h.m.setPlace(mem, tn, place{r, to})
		h.m.mutated(tn)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// clear empties a table: refused whole, naming the first bound cell, when
// any cell is bound; otherwise every row and its owned sets go (and, in the
// contract, their placements) and the definition stays.
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
	if want.ok() {
		for _, r := range rows {
			h.tally.orphaned += h.m.removeRow(tn, r)
		}
		h.m.clearPlaces(tn, "")
		h.m.mutated(tn)
		require.Equal(h, int64(len(rows)), n, "%s: the verb's count of rows cleared; the %s had %d", action, h.oracle(), len(rows))
		// 4. the definition stays and the external set is untouched
		tb, err := ntable.Read(h.ctx, h.c, tn)
		require.NoError(h, err, "%s: read after clear", action)
		require.Equal(h, "a,b,c", columnNames(tb), "%s: read after clear; want a,b,c and no rows", action)
		require.Empty(h, tb.Rows, "%s: read after clear; want a,b,c and no rows", action)
		h.checkExternal(action)
	}
	h.m.note("%s -> %s", action, outcome(want))
}

// Drop removes active presence and rows/owned sets in both modes. Only the
// baseline deletes the stable template/registry entry. Candidate identity,
// template and receipt ledger survive, while its placements are unlinked.
// The external set stays in both.
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
	if want.ok() {
		for _, r := range rows {
			h.tally.orphaned += h.m.removeRow(tn, r)
		}
		if h.m.candidate {
			h.m.clearPlaces(tn, "")
			h.m.mutated(tn)
			_, readErr := ntable.Read(h.ctx, h.c, tn)
			require.ErrorIs(h, readErr, ntable.ErrNoTable, "%s: dropped epoch remains readable", action)
		}
		delete(h.m.live, tn)
		require.Equal(h, len(rows), n, "%s: the verb's count of rows dropped; the %s had %d", action, h.oracle(), len(rows))
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
	require.NoError(h, h.c.ZAdd(h.ctx, extKey, redis.Z{Score: score, Member: mem}).Err(), "%s", action)
	h.tally.steps, h.tally.ok = h.tally.steps+1, h.tally.ok+1
	h.m.ext[mem] = score
	h.m.note("%s -> OK", action)
}

func (h *harness) externalRemove(*rapid.T) {
	mem := h.member(sortedKeys(h.m.ext))
	action := "externalRemove " + mem
	require.NoError(h, h.c.ZRem(h.ctx, extKey, mem).Err(), "%s", action)
	h.tally.steps, h.tally.ok = h.tally.steps+1, h.tally.ok+1
	delete(h.m.ext, mem)
	h.m.note("%s -> OK", action)
}
