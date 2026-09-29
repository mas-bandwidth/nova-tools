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

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
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
	if err != nil || len(events) != 1 {
		h.fail("missing receipt for %s: %v", table, err)
	}
	return events[0]
}

func (h *epochProperty) receipt(a epochAction, opts ntable.WriteOptions, r ntable.Receipt, event redis.XMessage, before, after *epochTableState) {
	v := event.Values
	want := map[string]string{"verb": a.verb, "epoch": strconv.FormatUint(opts.Epoch, 10), "actor": opts.Actor,
		"fence": opts.Fence, "idem": opts.Idem, "rev_before": strconv.FormatUint(h.revision[a.table], 10),
		"rev_after": strconv.FormatUint(h.revision[a.table]+1, 10)}
	for field, value := range want {
		if v[field] != value {
			h.fail("%s receipt %s=%v, want %s", epochActionNames[a.verb], field, v[field], value)
		}
	}
	if r.ID != event.ID || r.Epoch != opts.Epoch || r.Before != h.revision[a.table] || r.After != r.Before+1 || r.Outcome != v["outcome"] {
		h.fail("returned receipt differs from stream: %+v / %+v", r, event)
	}
	var args []string
	if err := json.Unmarshal([]byte(fmt.Sprint(v["args"])), &args); err != nil {
		h.fail("receipt args: %v", err)
	}
	expected := a.wire()
	if len(args) != len(expected) {
		h.fail("receipt args=%v, want %v", args, expected)
	}
	for i, value := range expected {
		if args[i] == value {
			continue
		}
		var gotJSON, wantJSON any
		if json.Unmarshal([]byte(args[i]), &gotJSON) != nil || json.Unmarshal([]byte(value), &wantJSON) != nil || !reflect.DeepEqual(gotJSON, wantJSON) {
			h.fail("receipt arg %d=%s, want %s", i, args[i], value)
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
	if err := json.Unmarshal([]byte(fmt.Sprint(v["members"])), &changes); err != nil {
		h.fail("receipt members: %v", err)
	}
	slices.SortFunc(changes, func(a, b change) int { return strings.Compare(a.ID, b.ID) })
	if !slices.Equal(changes, expectedChanges) {
		h.fail("receipt member delta=%v, want %v", changes, expectedChanges)
	}
	var cells []string
	if err := json.Unmarshal([]byte(fmt.Sprint(v["cells"])), &cells); err != nil {
		h.fail("receipt cells: %v", err)
	}
	for _, row := range []string{"r1", "r2"} {
		for _, col := range []string{"a", "b"} {
			at := row + ":" + col
			if (slices.Contains(before.rows, row) != slices.Contains(after.rows, row) || !reflect.DeepEqual(before.cells[at], after.cells[at])) && !slices.Contains(cells, at) {
				h.fail("receipt omits affected cell %s", at)
			}
		}
	}
	if v["outcome"] != "changed" && v["outcome"] != "noop" || v["outcome"] == "noop" && !reflect.DeepEqual(before, after) {
		h.fail("invalid outcome %v for model transition", v["outcome"])
	}
}

func (h *epochProperty) step(a epochAction) {
	h.t.Helper()
	h.trace = append(h.trace, fmt.Sprintf("%d %s writer=%d seen=%d active=%d args=%s", len(h.trace), epochActionNames[a.verb], a.actor, h.seen[a.actor], h.active, epochJSON(a.wire())))
	if epochActionNames[a.verb] == "" {
		h.fail("action lacks TLA mapping: %s", a.verb)
	}
	if a.verb == "read_epoch" {
		for i, c := range h.stores {
			epoch, err := c.HGet(h.ctx, epochPropertyKey, "n").Uint64()
			if err != nil || epoch != h.active {
				h.fail("store%d ReadEpoch=%d (%v), want %d", i, epoch, err, h.active)
			}
			h.seen[a.actor] = epoch
		}
		h.coverage[a.verb]++
		h.verify()
		return
	}
	if a.verb == "advance" {
		if h.seen[a.actor] != h.active || h.active >= 3 {
			h.fail("invalid model Advance precondition")
		}
		for i, c := range h.stores {
			image := memberStoreImage(h.t, c)
			for key, value := range image {
				if strings.HasPrefix(key, "table::member:"+fmt.Sprintf("e%d-", h.active)) || strings.Contains(key, fmt.Sprintf(":%d:", h.active)) {
					h.frozen[i][key] = value
				}
			}
			if err := c.HSet(h.ctx, epochPropertyKey, "n", h.active+1).Err(); err != nil {
				h.fail("advance: %v", err)
			}
			delete(image, epochPropertyKey)
			after := memberStoreImage(h.t, c)
			delete(after, epochPropertyKey)
			if !reflect.DeepEqual(image, after) {
				h.fail("Advance changed more than the epoch register")
			}
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
		image := memberStoreImage(h.t, c)
		var err error
		if i == 0 || want != "ok" {
			err = a.call(h.ctx, c, opts)
		} else {
			// Replay only the validated durable receipt; never a.wire().
			v := sourceEvent.Values
			var args []string
			if err := json.Unmarshal([]byte(fmt.Sprint(v["args"])), &args); err != nil {
				h.fail("replay args: %v", err)
			}
			wire := make([]any, len(args), len(args)+1)
			for j := range args {
				wire[j] = args[j]
			}
			wire = append(wire, epochJSON(map[string]any{"epoch": v["epoch"], "actor": v["actor"], "fence": v["fence"], "idem": v["idem"]}))
			reply, replayErr := c.FCall(h.ctx, "ns_table_"+fmt.Sprint(v["verb"]), []string{ntable.DefKey(a.table)}, wire...).Slice()
			if replayErr != nil || len(reply) == 0 || reply[0] == "REFUSED" {
				h.fail("receipt replay refused: %v / %v", reply, replayErr)
			}
		}
		got := classify(err).kind
		if errors.Is(err, ntable.ErrMemberEpoch) {
			got = "member-epoch"
		}
		if got != want {
			h.fail("store%d action=%s result=%s (%v), model=%s", i, a.verb, got, err, want)
		}
		if want != "ok" {
			if !reflect.DeepEqual(image, memberStoreImage(h.t, c)) || receipt.ID != "" {
				h.fail("refusal wrote state or returned a receipt")
			}
		} else {
			allowed := h.writeKeys(a, before, after)
			afterStore := memberStoreImage(h.t, c)
			if image["tables"] != afterStore["tables"] {
				if h.template[a.table] || (a.verb != "create" && a.verb != "bind") {
					h.fail("store%d action %s unexpectedly modified tables registry", i, a.verb)
				}
				keyType, err := c.Type(h.ctx, "tables").Result()
				if err != nil || keyType != "set" {
					h.fail("store%d tables registry type = %s (%v), want set", i, keyType, err)
				}
				members, err := c.SMembers(h.ctx, "tables").Result()
				if err != nil {
					h.fail("store%d read tables registry: %v", i, err)
				}
				var wantMembers []string
				for t, ok := range h.template {
					if ok {
						wantMembers = append(wantMembers, t)
					}
				}
				if !slices.Contains(wantMembers, a.table) {
					wantMembers = append(wantMembers, a.table)
				}
				slices.Sort(wantMembers)
				slices.Sort(members)
				if !slices.Equal(members, wantMembers) {
					h.fail("store%d tables registry membership mismatch: got %v, want %v", i, members, wantMembers)
				}
				delete(image, "tables")
				delete(afterStore, "tables")
			}
			if keys := unexpectedEpochWrites(image, afterStore, allowed); len(keys) != 0 {
				h.fail("store%d accepted action changed keys outside model: %v", i, keys)
			}
			if !h.template[a.table] && (a.verb == "create" || a.verb == "bind") {
				idKey := ntable.DefKey(a.table) + ":identity"
				idHash, err := c.HGetAll(h.ctx, idKey).Result()
				if err != nil || len(idHash) != 3 || idHash["epoch_key"] != epochPropertyKey || idHash["epoch_field"] != "n" || idHash["member_prefix"] != "table::member:" {
					h.fail("store%d table %s identity invalid: %v (%v)", i, a.table, idHash, err)
				}
			}
			event := h.event(c, a.table)
			if i == 0 {
				h.receipt(a, opts, receipt, event, before, after)
				sourceEvent = event
			} else if !reflect.DeepEqual(event.Values, sourceEvent.Values) {
				h.fail("replayed receipt payload differs: %v / %v", event.Values, sourceEvent.Values)
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
		image := memberStoreImage(h.t, c)
		var wantCatalog []string
		for _, table := range propTables {
			if h.template[table] {
				wantCatalog = append(wantCatalog, table)
			}
		}
		slices.Sort(wantCatalog)
		catalog, err := c.SMembers(h.ctx, "tables").Result()
		slices.Sort(catalog)
		if err != nil || !slices.Equal(catalog, wantCatalog) {
			h.fail("store%d catalog=%v (%v), model=%v", i, catalog, err, wantCatalog)
		}
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
		if !reflect.DeepEqual(historical, h.frozen[i]) {
			h.fail("store%d historical namespace changed", i)
		}
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
				if err != nil || !reflect.DeepEqual(got, metadata.want) {
					h.fail("store%d registration %s=%v (%v), model=%v", i, metadata.key, got, err, metadata.want)
				}
			}
			s := h.state[table]
			tb, err := ntable.Read(h.ctx, c, table)
			if !s.present {
				if !errors.Is(err, ntable.ErrNoTable) {
					h.fail("read absent %s = %v", table, err)
				}
			} else if err != nil || tb.Epoch != h.active || !slices.Equal(rowKeys(tb), s.rows) {
				h.fail("read %s = epoch%d rows%v err%v; model epoch%d rows%v", table, tb.Epoch, rowKeys(tb), err, h.active, s.rows)
			}
			placed := map[string]string{}
			for _, row := range []string{"r1", "r2"} {
				for _, col := range []string{"a", "b"} {
					at := row + ":" + col
					key := ntable.CellKeyAt(table, row, col, h.active)
					zs, err := c.ZRangeWithScores(h.ctx, key, 0, -1).Result()
					if err != nil || !sameZset(zsetOf(zs), s.cells[at]) {
						h.fail("cell %s = %v (%v), model %v", key, zs, err, s.cells[at])
					}
					for _, z := range zs {
						id := z.Member.(string)
						if placed[id] != "" || !strings.HasPrefix(id, fmt.Sprintf("e%d-", h.active)) {
							h.fail("duplicate or cross-epoch placement: %s", id)
						}
						placed[id] = at
					}
					if s.present && slices.Contains(s.rows, row) {
						members, err := ntable.CellMembers(h.ctx, c, table, row, col)
						if err != nil || len(members) != len(zs) {
							h.fail("members at %s = %v (%v)", key, members, err)
						}
						for j, member := range members {
							if member.Member != zs[j].Member || member.Score != zs[j].Score || j > 0 && (members[j-1].Score > member.Score || members[j-1].Score == member.Score && members[j-1].Member >= member.Member) {
								h.fail("members not in score/ID order at %s: %v", key, members)
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
					if err != nil || record["epoch"] != strconv.FormatUint(epoch, 10) || record["place:"+table] != placed[id] {
						h.fail("member record %s = %v (%v), placement=%s", id, record, err, placed[id])
					}
				}
			}
			count, err := c.XLen(h.ctx, ntable.ChangesKey(table)).Result()
			if err != nil || count != int64(h.revision[table]) {
				h.fail("receipt count %s = %d (%v), want %d", table, count, err, h.revision[table])
			}
			if s.present {
				report, err := ntable.Check(h.ctx, c, table)
				if err != nil || report.Epoch != h.active || report.Revision != h.revision[table] {
					h.fail("Check %s = %+v (%v)", table, report, err)
				}
			}
		}
		if !reflect.DeepEqual(image, memberStoreImage(h.t, c)) {
			h.fail("verification read mutated store%d", i)
		}
	}
}

func TestTableEpochActionsAndReceiptReplay(t *testing.T) {
	t.Parallel()
	model, err := os.ReadFile("../../tla/EpochMemberTable.tla")
	if err != nil {
		t.Fatal(err)
	}
	for verb, action := range epochActionNames {
		if !strings.Contains(string(model), "\n"+action+"(") {
			t.Fatalf("%s maps to missing model action %s", verb, action)
		}
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
				if err := c.FlushAll(h.ctx).Err(); err != nil {
					t.Fatal(err)
				}
				if err := c.HSet(h.ctx, "fixture:unrelated", "n", "unchanged").Err(); err != nil {
					t.Fatal(err)
				}
				if err := c.HSet(h.ctx, epochPropertyKey, "n", 1).Err(); err != nil {
					t.Fatal(err)
				}
				for epoch := uint64(1); epoch <= 3; epoch++ {
					for n := 1; n <= 3; n++ {
						if err := c.HSet(h.ctx, ntable.MemberKey(epochMember(epoch, n)), "epoch", epoch).Err(); err != nil {
							t.Fatal(err)
						}
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
			if coverage[verb+":"+outcome] == 0 {
				t.Errorf("generator missed %s:%s", verb, outcome)
			}
		}
	}
	if coverage["cell_add:member-epoch"] == 0 || coverage["advance"] != 16 {
		t.Errorf("generator missed epoch boundaries: %v", coverage)
	}
	t.Logf("model-action coverage: %v", coverage)
}
