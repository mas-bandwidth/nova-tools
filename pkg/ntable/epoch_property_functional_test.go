//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The original property harness deliberately stays transplantable to the old
// epoch-zero implementation. This companion exercises EpochMemberTable's two
// observed-epoch writers and three generations against the current Go API.
// Each seed has a fixed bound: 24 random actions per generation, two advances,
// and explicit stale probes of every modeled write at each advance. Seeds and
// the complete action trace appear on failure. Reproduce with -run and the seed
// subtest name; neither random state nor failure artifacts live outside TempDir.
//
// The oracle contains rows and scored placements, not Redis staging commands.
// After EVERY action it checks visible state, score/ID order, both directions
// of member placement, immutable member epochs, and retained historical bytes.
// Accepted receipts must identify the drawn model action, arguments, epoch,
// writer, revision and membership delta. A second owned Redis replays accepted
// commands FROM those receipts, as `tlacheck replay` does; refused
// commands have no receipt and are checked against complete-store snapshots.
// Accepted commands also compare complete-store snapshots, permitting changes
// only to keys named by the model transition, on both source and replay stores.
// Catalog membership, templates and immutable identity hashes must exactly match
// the model, including their first registration; allowed keys are not free-form.
// External bindings, batches, definition edits and row sorting retain their
// separate randomized/functional gates; no claim of concurrent server writers
// or exhaustive TLC exploration is made by this bounded execution test.

const epochPropertyKey = "property:epoch"

var epochActionNames = map[string]string{
	"create": "EpochCreate", "row_add": "EpochRowAdd", "row_del": "EpochRowDelete",
	"bind": "EpochBind", "cell_add": "EpochAdd", "cell_remove": "EpochRemove",
	"cell_move": "EpochMove", "clear": "EpochClear", "drop": "EpochDrop",
	"advance": "Advance", "read_epoch": "ReadEpoch",
}

type epochAction struct {
	verb, table, row, col, to, member string
	score                             int
	actor                             int
	keep                              []string
}

type epochTableState struct {
	present bool
	rows    []string
	cells   map[string]zset // row:column -> member scores
}

func emptyEpochTable(present bool) *epochTableState {
	return &epochTableState{present: present, cells: map[string]zset{}}
}

func (s *epochTableState) copy() *epochTableState {
	n := emptyEpochTable(s.present)
	n.rows = slices.Clone(s.rows)
	for at, members := range s.cells {
		n.cells[at] = zset{}
		for id, score := range members {
			n.cells[at][id] = score
		}
	}
	return n
}

func (s *epochTableState) location(id string) (string, float64) {
	for at, members := range s.cells {
		if score, ok := members[id]; ok {
			return at, score
		}
	}
	return "", 0
}

func (s *epochTableState) removeRow(row string) {
	s.rows = slices.DeleteFunc(s.rows, func(r string) bool { return r == row })
	for at := range s.cells {
		if strings.HasPrefix(at, row+":") {
			delete(s.cells, at)
		}
	}
}

type epochProperty struct {
	t        *testing.T
	ctx      context.Context
	stores   [2]*redis.Client
	active   uint64
	seen     [2]uint64
	state    map[string]*epochTableState
	template map[string]bool
	revision map[string]uint64
	frozen   [2]map[string]string
	trace    []string
	coverage map[string]int
}

func (h *epochProperty) fail(format string, args ...any) {
	h.t.Helper()
	h.t.Fatalf("%s\naction trace:\n%s", fmt.Sprintf(format, args...), strings.Join(h.trace, "\n"))
}

// Errorf, FailNow and Helper make the harness a require.TestingT: a failed
// require.X(h, ...) check exits through fail, with the action trace.
func (h *epochProperty) Errorf(format string, args ...any) { h.t.Helper(); h.fail(format, args...) }
func (h *epochProperty) FailNow()                          { h.t.FailNow() }
func (h *epochProperty) Helper()                           { h.t.Helper() }

func epochMember(epoch uint64, index int) string { return fmt.Sprintf("e%d-m%d", epoch, index) }

func epochDefinition(name string) ntable.Table {
	return ntable.Table{Name: name, FooterLabel: "total", EpochKey: epochPropertyKey, Columns: []ntable.Column{
		{Name: "a", Projection: ntable.Count, Fold: "sum"},
		{Name: "b", Projection: ntable.Count, Fold: "sum"},
	}}
}

func epochJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // all callers supply fixed JSON-compatible fixture types
	}
	return string(b)
}

func epochDefinitionFields() map[string]string {
	return map[string]string{"order": "a,b", "footer": "total", "col:a": "count:sum:0:", "col:b": "count:sum:0:",
		"epoch_key": epochPropertyKey, "epoch_field": "n", "created_at": "2026-01-01T00:00:00Z"}
}

// wire is independently specified input for receipt comparison, not the input
// used by the Go API call. JSON payloads are compared as values, not map order.
func (a epochAction) wire() []string {
	args := []string{a.table}
	fields := epochDefinitionFields()
	switch a.verb {
	case "create":
		return append(args, epochJSON(fields))
	case "bind":
		rows := []map[string]string{}
		for _, row := range a.keep {
			rows = append(rows, map[string]string{"key": row, "label": "", "exclude": "", "owner": ""})
		}
		return append(args, epochJSON(map[string]any{"fields": fields, "rows": rows}))
	case "row_add":
		return append(args, a.row, `{"label":"","exclude":"","owner":""}`)
	case "row_del":
		return append(args, a.row)
	case "cell_add":
		return append(args, a.row, a.col, strconv.Itoa(a.score), a.member)
	case "cell_remove":
		return append(args, a.row, a.col, a.member)
	case "cell_move":
		return append(args, a.row, a.col, a.to, a.member)
	}
	return args
}

func (a epochAction) call(ctx context.Context, c *redis.Client, opts ntable.WriteOptions) error {
	tb := epochDefinition(a.table)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var err error
	switch a.verb {
	case "create":
		err = ntable.Create(ctx, c, tb, at, opts)
	case "bind":
		for _, row := range a.keep {
			tb.Rows = append(tb.Rows, ntable.NewRow(tb, row))
		}
		err = ntable.Bind(ctx, c, tb, at, opts)
	case "row_add":
		_, err = ntable.RowAdd(ctx, c, a.table, a.row, ntable.RowSpec{}, opts)
	case "row_del":
		_, err = ntable.RowDel(ctx, c, a.table, a.row, opts)
	case "cell_add":
		_, err = ntable.CellAdd(ctx, c, a.table, a.row, a.col, a.member, float64(a.score), opts)
	case "cell_remove":
		_, err = ntable.CellRemove(ctx, c, a.table, a.row, a.col, a.member, opts)
	case "cell_move":
		_, err = ntable.CellMove(ctx, c, a.table, a.row, a.col, a.to, a.member, opts)
	case "clear":
		_, err = ntable.Clear(ctx, c, a.table, opts)
	case "drop":
		_, err = ntable.Drop(ctx, c, a.table, opts)
	default:
		return fmt.Errorf("unmapped action %q", a.verb)
	}
	return err
}

func (h *epochProperty) predict(a epochAction) (*epochTableState, string) {
	s := h.state[a.table].copy()
	if h.seen[a.actor] != h.active {
		return s, "stale"
	}
	if a.verb == "create" {
		s.present = true
		return s, "ok"
	}
	if a.verb == "bind" {
		for at, members := range s.cells {
			if len(members) > 0 && !slices.Contains(a.keep, strings.Split(at, ":")[0]) {
				return s, "occupied"
			}
		}
		s.present, s.rows = true, slices.Clone(a.keep)
		return s, "ok"
	}
	if !s.present {
		return s, "notable"
	}
	switch a.verb {
	case "row_add":
		if !slices.Contains(s.rows, a.row) {
			s.rows = append(s.rows, a.row)
		}
	case "row_del":
		s.removeRow(a.row)
	case "clear", "drop":
		s = emptyEpochTable(a.verb != "drop")
	default:
		if !slices.Contains(s.rows, a.row) {
			return s, "norow"
		}
		if !strings.HasPrefix(a.member, fmt.Sprintf("e%d-", h.active)) {
			return s, "member-epoch"
		}
		at := a.row + ":" + a.col
		placed, score := s.location(a.member)
		switch a.verb {
		case "cell_add":
			if placed != "" {
				return s, "placed"
			}
			if s.cells[at] == nil {
				s.cells[at] = zset{}
			}
			s.cells[at][a.member] = float64(a.score)
		case "cell_remove":
			delete(s.cells[at], a.member)
		case "cell_move":
			if placed != at {
				return s, "notmember"
			}
			delete(s.cells[at], a.member)
			dst := a.row + ":" + a.to
			if s.cells[dst] == nil {
				s.cells[dst] = zset{}
			}
			s.cells[dst][a.member] = score
		}
	}
	for at, members := range s.cells {
		if len(members) == 0 {
			delete(s.cells, at)
		}
	}
	return s, "ok"
}

func (h *epochProperty) event(c *redis.Client, table string) redis.XMessage {
	events, err := c.XRevRangeN(h.ctx, ntable.ChangesKey(table), "+", "-", 1).Result()
	require.NoError(h, err, "missing receipt for %s", table)
	require.Len(h, events, 1, "missing receipt for %s", table)
	return events[0]
}

func (h *epochProperty) receipt(a epochAction, opts ntable.WriteOptions, r ntable.Receipt, event redis.XMessage, before, after *epochTableState) {
	v := event.Values
	want := map[string]string{"verb": a.verb, "epoch": strconv.FormatUint(opts.Epoch, 10), "actor": opts.Actor,
		"fence": opts.Fence, "idem": opts.Idem, "rev_before": strconv.FormatUint(h.revision[a.table], 10),
		"rev_after": strconv.FormatUint(h.revision[a.table]+1, 10)}
	for field, value := range want {
		require.Equal(h, value, v[field], "%s receipt %s", epochActionNames[a.verb], field)
	}
	require.Equal(h, event.ID, r.ID, "returned receipt differs from stream: %+v / %+v", r, event)
	require.Equal(h, opts.Epoch, r.Epoch, "returned receipt differs from stream: %+v / %+v", r, event)
	require.Equal(h, h.revision[a.table], r.Before, "returned receipt differs from stream: %+v / %+v", r, event)
	require.Equal(h, r.Before+1, r.After, "returned receipt differs from stream: %+v / %+v", r, event)
	require.Equal(h, v["outcome"], r.Outcome, "returned receipt differs from stream: %+v / %+v", r, event)
	var args []string
	require.NoError(h, json.Unmarshal([]byte(fmt.Sprint(v["args"])), &args), "receipt args")
	expected := a.wire()
	require.Len(h, args, len(expected), "receipt args=%v, want %v", args, expected)
	for i, value := range expected {
		if args[i] != value {
			require.JSONEq(h, value, args[i], "receipt arg %d", i)
		}
	}
	type change struct{ ID, From, To, Score string }
	expectedChanges := []change{}
	for i := 1; i <= 3; i++ {
		id := epochMember(h.active, i)
		from, oldScore := before.location(id)
		to, score := after.location(id)
		if from != to {
			if to == "" {
				score = oldScore
			}
			expectedChanges = append(expectedChanges, change{id, from, to, strconv.FormatFloat(score, 'g', -1, 64)})
		}
	}
	var changes []change
	require.NoError(h, json.Unmarshal([]byte(fmt.Sprint(v["members"])), &changes), "receipt members")
	slices.SortFunc(changes, func(a, b change) int { return strings.Compare(a.ID, b.ID) })
	require.Equal(h, expectedChanges, changes, "receipt member delta")
	var cells []string
	require.NoError(h, json.Unmarshal([]byte(fmt.Sprint(v["cells"])), &cells), "receipt cells")
	for _, row := range []string{"r1", "r2"} {
		for _, col := range []string{"a", "b"} {
			at := row + ":" + col
			if slices.Contains(before.rows, row) != slices.Contains(after.rows, row) || !reflect.DeepEqual(before.cells[at], after.cells[at]) {
				require.Contains(h, cells, at, "receipt omits affected cell %s", at)
			}
		}
	}
	require.Contains(h, []string{"changed", "noop"}, v["outcome"], "invalid outcome %v for model transition", v["outcome"])
	if v["outcome"] == "noop" {
		require.Equal(h, before, after, "invalid outcome %v for model transition", v["outcome"])
	}
}

func (h *epochProperty) step(a epochAction) {
	h.t.Helper()
	h.trace = append(h.trace, fmt.Sprintf("%d %s writer=%d seen=%d active=%d args=%s", len(h.trace), epochActionNames[a.verb], a.actor, h.seen[a.actor], h.active, epochJSON(a.wire())))
	require.NotEmpty(h, epochActionNames[a.verb], "action lacks TLA mapping: %s", a.verb)
	if a.verb == "read_epoch" {
		for i, c := range h.stores {
			epoch, err := c.HGet(h.ctx, epochPropertyKey, "n").Uint64()
			require.NoError(h, err, "store%d ReadEpoch", i)
			require.Equal(h, h.active, epoch, "store%d ReadEpoch", i)
			h.seen[a.actor] = epoch
		}
		h.coverage[a.verb]++
		h.verify()
		return
	}
	if a.verb == "advance" {
		require.Equal(h, h.active, h.seen[a.actor], "invalid model Advance precondition")
		require.Less(h, h.active, uint64(3), "invalid model Advance precondition")
		for i, c := range h.stores {
			image := storeImage(h.t, c)
			for key, value := range image {
				if strings.HasPrefix(key, "table::member:"+fmt.Sprintf("e%d-", h.active)) || strings.Contains(key, fmt.Sprintf(":%d:", h.active)) {
					h.frozen[i][key] = value
				}
			}
			require.NoError(h, c.HSet(h.ctx, epochPropertyKey, "n", h.active+1).Err(), "advance")
			delete(image, epochPropertyKey)
			after := storeImage(h.t, c)
			delete(after, epochPropertyKey)
			require.Equal(h, image, after, "Advance changed more than the epoch register")
		}
		h.active++
		for _, table := range propTables {
			h.state[table] = emptyEpochTable(h.template[table])
		}
		h.coverage[a.verb]++
		h.verify()
		return
	}
	before := h.state[a.table]
	after, want := h.predict(a)
	var receipt ntable.Receipt
	opts := ntable.WriteOptions{Epoch: h.seen[a.actor], Actor: fmt.Sprintf("w%d", a.actor+1), Fence: "fixture", Idem: strconv.Itoa(len(h.trace)), Receipt: &receipt}
	var sourceEvent redis.XMessage
	for i, c := range h.stores {
		image := storeImage(h.t, c)
		var err error
		if i == 0 || want != "ok" {
			err = a.call(h.ctx, c, opts)
		} else {
			// Replay only the validated durable receipt; never a.wire().
			v := sourceEvent.Values
			var args []string
			require.NoError(h, json.Unmarshal([]byte(fmt.Sprint(v["args"])), &args), "replay args")
			wire := make([]any, len(args), len(args)+1)
			for j := range args {
				wire[j] = args[j]
			}
			wire = append(wire, epochJSON(map[string]any{"epoch": v["epoch"], "actor": v["actor"], "fence": v["fence"], "idem": v["idem"]}))
			reply, replayErr := c.FCall(h.ctx, "ns_table_"+fmt.Sprint(v["verb"]), []string{ntable.DefKey(a.table)}, wire...).Slice()
			require.NoError(h, replayErr, "receipt replay refused: %v", reply)
			require.NotEmpty(h, reply, "receipt replay refused")
			require.NotEqual(h, "REFUSED", reply[0], "receipt replay refused: %v", reply)
		}
		got := classify(err).kind
		if errors.Is(err, ntable.ErrMemberEpoch) {
			got = "member-epoch"
		}
		require.Equal(h, want, got, "store%d action=%s err=%v", i, a.verb, err)
		if want != "ok" {
			require.Equal(h, image, storeImage(h.t, c), "refusal wrote state")
			require.Empty(h, receipt.ID, "refusal returned a receipt")
		} else {
			allowed := h.writeKeys(a, before, after)
			afterStore := storeImage(h.t, c)
			if image["tables"] != afterStore["tables"] {
				require.False(h, h.template[a.table], "store%d action %s unexpectedly modified tables registry", i, a.verb)
				require.Contains(h, []string{"create", "bind"}, a.verb, "store%d action %s unexpectedly modified tables registry", i, a.verb)
				keyType, err := c.Type(h.ctx, "tables").Result()
				require.NoError(h, err, "store%d tables registry type", i)
				require.Equal(h, "set", keyType, "store%d tables registry type", i)
				members, err := c.SMembers(h.ctx, "tables").Result()
				require.NoError(h, err, "store%d read tables registry", i)
				var wantMembers []string
				for t, ok := range h.template {
					if ok {
						wantMembers = append(wantMembers, t)
					}
				}
				if !slices.Contains(wantMembers, a.table) {
					wantMembers = append(wantMembers, a.table)
				}
				require.ElementsMatch(h, wantMembers, members, "store%d tables registry membership", i)
				delete(image, "tables")
				delete(afterStore, "tables")
			}
			require.Empty(h, unexpectedEpochWrites(image, afterStore, allowed), "store%d accepted action changed keys outside model", i)
			if !h.template[a.table] && (a.verb == "create" || a.verb == "bind") {
				idKey := ntable.DefKey(a.table) + ":identity"
				idHash, err := c.HGetAll(h.ctx, idKey).Result()
				require.NoError(h, err, "store%d table %s identity invalid: %v", i, a.table, idHash)
				require.Len(h, idHash, 3, "store%d table %s identity invalid: %v", i, a.table, idHash)
				require.Equal(h, epochPropertyKey, idHash["epoch_key"], "store%d table %s identity invalid: %v", i, a.table, idHash)
				require.Equal(h, "n", idHash["epoch_field"], "store%d table %s identity invalid: %v", i, a.table, idHash)
				require.Equal(h, "table::member:", idHash["member_prefix"], "store%d table %s identity invalid: %v", i, a.table, idHash)
			}
			event := h.event(c, a.table)
			if i == 0 {
				h.receipt(a, opts, receipt, event, before, after)
				sourceEvent = event
			} else {
				require.Equal(h, sourceEvent.Values, event.Values, "replayed receipt payload differs")
			}
		}
	}
	h.coverage[a.verb+":"+want]++
	if want == "ok" {
		h.state[a.table] = after
		h.template[a.table] = true
		h.revision[a.table]++
	}
	h.verify()
}

// writeKeys describes the model's possible write footprint. It does not read
// receipts, Redis contents or Lua staging commands. In particular, a table
// prefix is never permission to introduce arbitrary keys beneath that prefix.
func (h *epochProperty) writeKeys(a epochAction, before, after *epochTableState) map[string]bool {
	allowed := map[string]bool{
		ntable.EpochPrefix(a.table, h.active) + ":definition": true,
		ntable.RevisionKey(a.table):                           true,
		ntable.ChangesKey(a.table):                            true,
	}
	if !h.template[a.table] && (a.verb == "create" || a.verb == "bind") {
		allowed[ntable.DefKey(a.table)] = true
		allowed[ntable.DefKey(a.table)+":identity"] = true
	}
	var rows []string
	switch a.verb {
	case "row_add", "row_del":
		rows = []string{a.row}
	case "bind":
		rows = append(slices.Clone(before.rows), after.rows...)
	case "clear", "drop":
		rows = before.rows
	}
	if len(rows) > 0 {
		allowed[ntable.RowsKeyAt(a.table, h.active)] = true
		for _, row := range rows {
			allowed[ntable.RowKeyAt(a.table, row, h.active)] = true
		}
	}
	for _, row := range []string{"r1", "r2"} {
		for _, col := range []string{"a", "b"} {
			at := row + ":" + col
			if !reflect.DeepEqual(before.cells[at], after.cells[at]) {
				allowed[ntable.CellKeyAt(a.table, row, col, h.active)] = true
			}
		}
	}
	for n := 1; n <= 3; n++ {
		id := epochMember(h.active, n)
		from, _ := before.location(id)
		to, _ := after.location(id)
		if from != to {
			allowed[ntable.MemberKey(id)] = true
		}
	}
	return allowed
}

func unexpectedEpochWrites(before, after map[string]string, allowed map[string]bool) []string {
	keys := map[string]bool{}
	for key, value := range before {
		if next, exists := after[key]; !exists || next != value {
			keys[key] = true
		}
	}
	for key := range after {
		if _, exists := before[key]; !exists {
			keys[key] = true
		}
	}
	var unexpected []string
	for key := range keys {
		if !allowed[key] {
			unexpected = append(unexpected, key)
		}
	}
	slices.Sort(unexpected)
	return unexpected
}

func (h *epochProperty) verify() {
	h.t.Helper()
	for i, c := range h.stores {
		image := storeImage(h.t, c)
		var wantCatalog []string
		for _, table := range propTables {
			if h.template[table] {
				wantCatalog = append(wantCatalog, table)
			}
		}
		catalog, err := c.SMembers(h.ctx, "tables").Result()
		require.NoError(h, err, "store%d catalog", i)
		require.ElementsMatch(h, wantCatalog, catalog, "store%d catalog", i)
		historical := map[string]string{}
		for key, value := range image {
			for epoch := uint64(1); epoch < h.active; epoch++ {
				old := strings.HasPrefix(key, "table::member:"+fmt.Sprintf("e%d-", epoch))
				for _, table := range propTables {
					old = old || strings.HasPrefix(key, ntable.EpochPrefix(table, epoch)+":")
				}
				if old {
					historical[key] = value
				}
			}
		}
		require.Equal(h, h.frozen[i], historical, "store%d historical namespace changed", i)
		for _, table := range propTables {
			wantTemplate, wantIdentity := map[string]string{}, map[string]string{}
			if h.template[table] {
				wantTemplate = epochDefinitionFields()
				wantIdentity = map[string]string{"epoch_key": epochPropertyKey, "epoch_field": "n", "member_prefix": "table::member:"}
			}
			for _, metadata := range []struct {
				key  string
				want map[string]string
			}{{ntable.DefKey(table), wantTemplate}, {ntable.DefKey(table) + ":identity", wantIdentity}} {
				got, err := c.HGetAll(h.ctx, metadata.key).Result()
				require.NoError(h, err, "store%d registration %s", i, metadata.key)
				require.Equal(h, metadata.want, got, "store%d registration %s", i, metadata.key)
			}
			s := h.state[table]
			tb, err := ntable.Read(h.ctx, c, table)
			if !s.present {
				require.ErrorIs(h, err, ntable.ErrNoTable, "read absent %s", table)
			} else {
				require.NoError(h, err, "read %s", table)
				require.Equal(h, h.active, tb.Epoch, "read %s", table)
				// append normalizes a nil model row list: rowKeys is never nil.
				require.Equal(h, append([]string{}, s.rows...), rowKeys(tb), "read %s", table)
			}
			placed := map[string]string{}
			for _, row := range []string{"r1", "r2"} {
				for _, col := range []string{"a", "b"} {
					at := row + ":" + col
					key := ntable.CellKeyAt(table, row, col, h.active)
					zs, err := c.ZRangeWithScores(h.ctx, key, 0, -1).Result()
					require.NoError(h, err, "cell %s", key)
					require.True(h, sameZset(zsetOf(zs), s.cells[at]), "cell %s = %v, model %v", key, zs, s.cells[at])
					for _, z := range zs {
						id := z.Member.(string)
						require.Empty(h, placed[id], "duplicate or cross-epoch placement: %s", id)
						require.True(h, strings.HasPrefix(id, fmt.Sprintf("e%d-", h.active)), "duplicate or cross-epoch placement: %s", id)
						placed[id] = at
					}
					if s.present && slices.Contains(s.rows, row) {
						members, err := ntable.CellMembers(h.ctx, c, table, row, col)
						require.NoError(h, err, "members at %s", key)
						require.Len(h, members, len(zs), "members at %s = %v", key, members)
						for j, member := range members {
							require.Equal(h, zs[j].Member, member.Member, "members not in score/ID order at %s: %v", key, members)
							require.Equal(h, zs[j].Score, member.Score, "members not in score/ID order at %s: %v", key, members)
							if j > 0 {
								require.LessOrEqual(h, members[j-1].Score, member.Score, "members not in score/ID order at %s: %v", key, members)
								if members[j-1].Score == member.Score {
									require.Less(h, members[j-1].Member, member.Member, "members not in score/ID order at %s: %v", key, members)
								}
							}
						}
					}
				}
			}
			// Historical records are frozen above. Current and future records
			// must retain their immutable epoch; future members stay unplaced.
			for epoch := h.active; epoch <= 3; epoch++ {
				for n := 1; n <= 3; n++ {
					id := epochMember(epoch, n)
					record, err := c.HGetAll(h.ctx, ntable.MemberKey(id)).Result()
					require.NoError(h, err, "member record %s", id)
					require.Equal(h, strconv.FormatUint(epoch, 10), record["epoch"], "member record %s = %v, placement=%s", id, record, placed[id])
					require.Equal(h, placed[id], record["place:"+table], "member record %s = %v, placement=%s", id, record, placed[id])
				}
			}
			count, err := c.XLen(h.ctx, ntable.ChangesKey(table)).Result()
			require.NoError(h, err, "receipt count %s", table)
			require.Equal(h, int64(h.revision[table]), count, "receipt count %s", table)
			if s.present {
				report, err := ntable.Check(h.ctx, c, table)
				require.NoError(h, err, "Check %s", table)
				require.Equal(h, h.active, report.Epoch, "Check %s = %+v", table, report)
				require.Equal(h, h.revision[table], report.Revision, "Check %s = %+v", table, report)
			}
		}
		require.Equal(h, image, storeImage(h.t, c), "verification read mutated store%d", i)
	}
}

func TestTableEpochActionsAndReceiptReplay(t *testing.T) {
	t.Parallel()
	model, err := os.ReadFile("../../tla/EpochMemberTable.tla")
	require.NoError(t, err)
	for verb, action := range epochActionNames {
		require.Contains(t, string(model), "\n"+action+"(", "%s maps to missing model action %s", verb, action)
	}
	// One owned source/replay pair per test, reset between reproducible seeds.
	_, source := live(t)
	_, replay := live(t)
	coverage := map[string]int{}
	seedsRun := 0
	verbs := []string{"create", "row_add", "row_del", "bind", "cell_add", "cell_remove", "cell_move", "clear", "drop"}
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			seedsRun++
			rng := rand.New(rand.NewSource(seed))
			h := &epochProperty{t: t, ctx: context.Background(), stores: [2]*redis.Client{source, replay}, active: 1, seen: [2]uint64{1, 1},
				state: map[string]*epochTableState{}, template: map[string]bool{}, revision: map[string]uint64{}, coverage: coverage,
				frozen: [2]map[string]string{{}, {}}}
			for _, c := range h.stores {
				require.NoError(t, c.FlushAll(h.ctx).Err())
				require.NoError(t, c.HSet(h.ctx, "fixture:unrelated", "n", "unchanged").Err())
				require.NoError(t, c.HSet(h.ctx, epochPropertyKey, "n", 1).Err())
				for epoch := uint64(1); epoch <= 3; epoch++ {
					for n := 1; n <= 3; n++ {
						require.NoError(t, c.HSet(h.ctx, ntable.MemberKey(epochMember(epoch, n)), "epoch", epoch).Err())
					}
				}
			}
			for _, table := range propTables {
				h.state[table] = emptyEpochTable(false)
			}
			for epoch := uint64(1); epoch <= 3; epoch++ {
				if epoch > 1 {
					h.step(epochAction{verb: "read_epoch"})
					h.step(epochAction{verb: "advance"})
					// Every write must reject the now-stale writer, including
					// declarations that could otherwise re-materialize a table.
					for _, verb := range verbs {
						h.step(epochAction{verb: verb, table: "t1", row: "r1", col: "a", to: "b", member: epochMember(epoch-1, 1), score: 2})
					}
				}
				h.step(epochAction{verb: "read_epoch"})
				for _, table := range propTables {
					h.step(epochAction{verb: "create", table: table})
					for _, row := range []string{"r1", "r2"} {
						h.step(epochAction{verb: "row_add", table: table, row: row})
					}
					for n := 1; n <= 3; n++ {
						h.step(epochAction{verb: "cell_add", table: table, row: "r1", col: "a", member: epochMember(epoch, n), score: 3 - n/2})
					}
					h.step(epochAction{verb: "cell_move", table: table, row: "r1", col: "a", to: "b", member: epochMember(epoch, 1)})
				}
				for step := 0; step < 24; step++ {
					actor := rng.Intn(2)
					if rng.Intn(8) == 0 {
						h.step(epochAction{verb: "read_epoch", actor: actor})
						continue
					}
					memberEpoch := epoch
					if rng.Intn(4) == 0 {
						memberEpoch = uint64(1 + rng.Intn(3))
					}
					a := epochAction{verb: verbs[rng.Intn(len(verbs))], table: propTables[rng.Intn(2)], row: fmt.Sprintf("r%d", 1+rng.Intn(2)),
						col: []string{"a", "b"}[rng.Intn(2)], to: []string{"a", "b"}[rng.Intn(2)], member: epochMember(memberEpoch, 1+rng.Intn(3)), score: rng.Intn(5) - 2, actor: actor}
					for _, row := range []string{"r1", "r2"} {
						if rng.Intn(2) == 0 {
							a.keep = append(a.keep, row)
						}
					}
					h.step(a)
				}
			}
		})
	}
	if t.Failed() {
		return // the action trace already identifies the failed invariant
	}
	// Coverage over the full run is pinned. Selecting a single seed with
	// -run reproduces its actions without requiring other seeds' coverage.
	if seedsRun != 8 {
		return
	}
	for _, verb := range verbs {
		for _, outcome := range []string{"ok", "stale"} {
			assert.NotEqual(t, 0, coverage[verb+":"+outcome], "generator missed %s:%s", verb, outcome)
		}
	}
	assert.NotZero(t, coverage["cell_add:member-epoch"], "generator missed epoch boundaries: %v", coverage)
	assert.Equal(t, 16, coverage["advance"], "generator missed epoch boundaries: %v", coverage)
	t.Logf("model-action coverage: %v", coverage)
}
