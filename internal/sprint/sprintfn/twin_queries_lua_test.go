package sprintfn

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The Lua kinds of sprint_queries.lua run here, under gopher-lua, against
// stubs of Layer 1's checked helpers (S.read_record, S.read_probe,
// S.read_range_head, S.ensure_read_table, S.emit_read_item) and of Layer 2's
// L.read_line_at, which read the very state a twin holds: the Mem's tables,
// the sprint's keys and the log stub's lines. This checks the Lua's own
// logic (its validation, its ids, its order of reads, its answers) against
// the twin's, key for key and charge for charge. It does not check Layer 1's
// helpers, which the stubs stand in for, nor a store: that is
// TestTwinEqualsLuaQueries, which waits on gate G0.

// luaGlue is what the file expects of its surroundings: the prelude's NS with
// the tset profile set, Layer 1's S with the pure helpers it has in the store
// (refuse, array, is_array, is_object, json) and Layer 2's L, the context a
// read gets, and the JSON null. The helpers that read are set from Go.
const luaGlue = `
NS = {tset_profile = 'sprint'}
cjson = {null = JSON_NULL}
local S = {}
NS.tset = S
NS.tlog = {}
S.json = {encode = json_encode, decode = json_decode}
S.array = new_array
function S.is_array(v)
  if type(v) ~= 'table' then return false end
  local mt = getmetatable(v)
  return type(mt) == 'table' and mt.__is_cjson_array == true
end
function S.is_object(v) return type(v) == 'table' and not S.is_array(v) end
function S.refuse(code, detail, message)
  detail = detail or {}
  if not detail.ids or next(detail.ids) == nil then detail.ids = S.array() end
  if not detail.cells or next(detail.cells) == nil then detail.cells = S.array() end
  if not detail.rows or next(detail.rows) == nil then detail.rows = S.array() end
  return {status = 'refused', code = code, detail = detail, message = (message or code) .. '; nothing was changed'}
end
function new_ctx(space, epoch, now_ms)
  local ctx = {space = space, request_epoch = epoch, active_epoch = epoch, now_ms = now_ms, operation = 'read',
    query_index = 0, read_emit = {index = 0}}
  local function tp(t, e) return space .. 'table:' .. t .. (e == '0' and '' or ':' .. e) end
  ctx.rows_key = function(t, e) return tp(t, e) .. ':rows' end
  ctx.cell_key = function(t, e, row, col) return tp(t, e) .. ':cell:' .. row .. ':' .. col end
  return ctx
end
`

// luaHarness runs the Lua kinds over a world's state.
type luaHarness struct {
	t      *testing.T
	w      *qworld
	L      *lua.LState
	null   *lua.LUserData
	epoch  string
	charge QueryCharge
	zsets  map[string]map[string]float64
	hashes map[string]map[string]string
	// answered and refused count the queries compared, so that a test of the
	// harness can say it compared answers and refusals of every kind.
	answered, refused map[string]int
	defs              map[string]tset.TableDefinition
	lines             []json.RawMessage
	// carry keeps the charge of the queries before, as one read's queries share a
	// budget (Layer 1's ctx.budget): prepare does not start it over.
	carry bool
}

func newLuaHarness(t *testing.T, w *qworld) *luaHarness {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	h := &luaHarness{t: t, w: w, L: L, epoch: "0", answered: map[string]int{}, refused: map[string]int{}}
	h.null = L.NewUserData()
	L.SetGlobal("JSON_NULL", h.null)
	L.SetGlobal("json_encode", L.NewFunction(func(L *lua.LState) int {
		b, err := json.Marshal(h.goValue(L.CheckAny(1)))
		if err != nil {
			L.RaiseError("encode: %v", err)
		}
		L.Push(lua.LString(b))
		return 1
	}))
	L.SetGlobal("json_decode", L.NewFunction(func(L *lua.LState) int {
		var v any
		if err := json.Unmarshal([]byte(L.CheckString(1)), &v); err != nil {
			L.RaiseError("decode: %v", err)
		}
		L.Push(h.luaValue(v))
		return 1
	}))
	L.SetGlobal("new_array", L.NewFunction(func(L *lua.LState) int {
		L.Push(h.newArray())
		return 1
	}))
	if err := L.DoString(luaGlue); err != nil {
		t.Fatalf("glue: %v", err)
	}
	S := L.GetGlobal("NS").(*lua.LTable).RawGetString("tset").(*lua.LTable)
	tlog := L.GetGlobal("NS").(*lua.LTable).RawGetString("tlog").(*lua.LTable)
	L.SetField(S, "read_record", L.NewFunction(h.readRecord))
	L.SetField(S, "read_probe", L.NewFunction(h.readProbe))
	L.SetField(S, "read_range_head", L.NewFunction(h.readRangeHead))
	L.SetField(S, "ensure_read_table", L.NewFunction(h.ensureReadTable))
	L.SetField(S, "emit_read_item", L.NewFunction(func(L *lua.LState) int {
		if _, err := json.Marshal(h.goValue(L.CheckAny(2))); err != nil {
			L.RaiseError("emit: %v", err)
		}
		L.Push(lua.LTrue)
		return 1
	}))
	L.SetField(tlog, "read_line_at", L.NewFunction(h.readLineAt))
	frags, err := fn.SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	queries, err := fn.SprintQueryFragments()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []fn.SprintFragment{frags[0], queries[0]} {
		if err := L.DoString(f.Source); err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
	}
	return h
}

func (h *luaHarness) newArray() *lua.LTable {
	t := h.L.NewTable()
	mt := h.L.NewTable()
	mt.RawSetString("__is_cjson_array", lua.LTrue)
	h.L.SetMetatable(t, mt)
	return t
}

// luaValue converts what encoding/json decodes into a Lua value; arrays carry
// the marker Layer 1's S.is_array reads.
func (h *luaHarness) luaValue(v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return h.null
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case string:
		return lua.LString(x)
	case []any:
		t := h.newArray()
		for _, e := range x {
			t.Append(h.luaValue(e))
		}
		return t
	case map[string]any:
		t := h.L.NewTable()
		for k, e := range x {
			t.RawSetString(k, h.luaValue(e))
		}
		return t
	}
	panic(fmt.Sprintf("no Lua value for %T", v))
}

// goValue converts a Lua value into what encoding/json encodes: a table with
// the array marker, or with a sequence, is an array; any other table an object.
func (h *luaHarness) goValue(v lua.LValue) any {
	switch x := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(x)
	case lua.LNumber:
		return float64(x)
	case lua.LString:
		return string(x)
	case *lua.LUserData:
		if x == h.null {
			return nil
		}
	case *lua.LTable:
		marked := false
		if mt, ok := h.L.GetMetatable(x).(*lua.LTable); ok && mt.RawGetString("__is_cjson_array") == lua.LTrue {
			marked = true
		}
		if n := x.Len(); marked || n > 0 {
			out := make([]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, h.goValue(x.RawGetInt(i)))
			}
			return out
		}
		out := map[string]any{}
		x.ForEach(func(k, e lua.LValue) { out[k.String()] = h.goValue(e) })
		return out
	}
	panic(fmt.Sprintf("no Go value for %T (%v)", v, v))
}

// prepare reads the state a query reads: the Mem's tables as the keys Layer 1
// holds them, and the sprint's own keys.
func (h *luaHarness) prepare() {
	h.t.Helper()
	snap, err := h.w.m.Snapshot(testPrefix)
	if err != nil {
		h.t.Fatal(err)
	}
	h.defs = snap.Definitions
	h.zsets, h.hashes = map[string]map[string]float64{}, map[string]map[string]string{}
	num := func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
	for table, tbl := range snap.Epochs[tset.Decimal(h.epoch)].Tables {
		tp := testPrefix + "table:" + table
		if h.epoch != "0" {
			tp += ":" + h.epoch
		}
		rows := map[string]float64{}
		for row, rank := range tbl.Rows {
			rows[row] = num(string(rank))
		}
		h.zsets[tp+":rows"] = rows
		for row, cols := range tbl.Cells {
			for col, members := range cols {
				z := map[string]float64{}
				for id, score := range members {
					z[id] = num(score)
				}
				h.zsets[tp+":cell:"+row+":"+col] = z
			}
		}
	}
	for key, v := range h.w.tw.SprintKeys() {
		switch v.Kind {
		case kindZSet:
			h.zsets[key] = v.ZSet
		case kindHash:
			h.hashes[key] = v.Hash
		}
	}
	h.lines = h.w.log.Lines(testPrefix, tset.Decimal(h.epoch))
	if !h.carry {
		h.charge = QueryCharge{}
	}
}

// The bounds of one read, which the stubs enforce as Layer 1's S.charge does:
// BUDGET naming the unit, once the read has charged more than the limit.
const (
	stubMaxRecords  = 10000
	stubMaxProbes   = 20000
	stubMaxRangeIDs = 20000
)

// over is the refusal of a read past a bound.
func (h *luaHarness) over(L *lua.LState, unit string, actual, limit int) int {
	return h.refusal(L, "BUDGET", map[string]any{"budget": unit, "actual": float64(actual), "limit": float64(limit)})
}

// payloadBytes is Layer 1's payload(): the bytes of a reply, the lengths of its
// strings, the text of its numbers and the names of its string keys.
func payloadBytes(v lua.LValue) int {
	switch x := v.(type) {
	case lua.LString:
		return len(x)
	case lua.LNumber:
		return len(x.String())
	case *lua.LTable:
		n := 0
		x.ForEach(func(k, e lua.LValue) {
			if ks, ok := k.(lua.LString); ok {
				n += len(ks)
			}
			n += payloadBytes(e)
		})
		return n
	}
	return 0
}

func (h *luaHarness) refusal(L *lua.LState, code string, detail map[string]any) int {
	d := L.NewTable()
	for k, v := range detail {
		d.RawSetString(k, h.luaValue(v))
	}
	S := L.GetGlobal("NS").(*lua.LTable).RawGetString("tset").(*lua.LTable)
	L.Push(lua.LNil)
	L.Push(h.call(S.RawGetString("refuse"), lua.LString(code), d))
	return 2
}

func (h *luaHarness) call(fnv lua.LValue, args ...lua.LValue) lua.LValue {
	if err := h.L.CallByParam(lua.P{Fn: fnv, NRet: 1, Protect: true}, args...); err != nil {
		h.t.Fatalf("lua: %v", err)
	}
	r := h.L.Get(-1)
	h.L.Pop(1)
	return r
}

func (h *luaHarness) strings(v lua.LValue) []string {
	t, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	out := []string{}
	for i := 1; i <= t.Len(); i++ {
		out = append(out, t.RawGetInt(i).String())
	}
	return out
}

func (h *luaHarness) readRecord(L *lua.LState) int {
	table, id := L.CheckString(2), L.CheckString(3)
	fields := h.strings(L.CheckTable(4))
	if fields == nil {
		fields = []string{}
	}
	h.charge.Records++
	if h.charge.Records > stubMaxRecords {
		return h.over(L, "record", h.charge.Records, stubMaxRecords)
	}
	rep, err := h.w.m.Read(context.Background(), newTSetReadPlan(testPrefix, tset.Decimal(h.epoch), "atomic",
		[]tset.ReadQuery{{Kind: "ids", Table: table, IDs: []string{id}, Fields: fields}}))
	if err != nil {
		var ref *tset.Refusal
		if r, ok := err.(*tset.Refusal); ok {
			ref = r
		} else {
			h.t.Fatalf("record read: %v", err)
		}
		return h.refusal(L, ref.Code, map[string]any{"table": table})
	}
	r := rep.Answers[0].Records[0]
	out := L.NewTable()
	out.RawSetString("id", lua.LString(r.ID))
	out.RawSetString("exists", lua.LBool(r.Exists))
	nullable := func(s string) lua.LValue {
		if s == "" {
			return h.null
		}
		return lua.LString(s)
	}
	out.RawSetString("epoch", nullable(string(r.Epoch)))
	out.RawSetString("revision", nullable(string(r.Revision)))
	out.RawSetString("score", nullable(r.Score))
	if r.Place == nil {
		out.RawSetString("place", h.null)
	} else {
		p := L.NewTable()
		p.RawSetString("row", lua.LString(r.Place.Row))
		p.RawSetString("col", lua.LString(r.Place.Col))
		out.RawSetString("place", p)
	}
	fs := L.NewTable()
	for name, v := range r.Fields {
		f := L.NewTable()
		f.RawSetString("present", lua.LBool(v.Present))
		if v.Present {
			f.RawSetString("value", lua.LString(v.Value))
		} else {
			f.RawSetString("value", h.null)
		}
		fs.RawSetString(name, f)
	}
	out.RawSetString("fields", fs)
	L.Push(out)
	L.Push(lua.LNil)
	return 2
}

// reply turns a count or a list of optional strings into what Redis's Lua
// gets: a number, a string, false for a nil bulk.
func scoreLua(f float64) lua.LValue { return lua.LString(formatScore(f)) }

func (h *luaHarness) readProbe(L *lua.LState) int {
	argv := h.strings(L.CheckTable(2))
	key := L.CheckString(3)
	reserve := int(L.CheckNumber(5))
	// Layer 1's checked probe charges a cell for every name an HMGET or a ZMSCORE
	// asks for (S.read_probe: count - 3 more than the command's own cell), and one
	// for any other probe.
	h.charge.Probes++
	if argv[0] == "HMGET" || argv[0] == "ZMSCORE" {
		h.charge.Probes += len(argv) - 3
	}
	if h.charge.Probes > stubMaxProbes {
		return h.over(L, "cell", h.charge.Probes, stubMaxProbes)
	}
	if argv[1] != key {
		h.t.Fatalf("a probe whose key is not its argv's: %v %s", argv, key)
	}
	z, isZ := h.zsets[key]
	hs, isH := h.hashes[key]
	var reply lua.LValue
	switch argv[0] {
	case "HMGET":
		out := L.NewTable()
		for _, f := range argv[2:] {
			if v, ok := hs[f]; isH && ok {
				out.Append(lua.LString(v))
			} else {
				out.Append(lua.LFalse)
			}
		}
		reply = out
	case "HLEN":
		reply = lua.LNumber(len(hs))
	case "ZSCORE", "ZMSCORE":
		get := func(m string) lua.LValue {
			if s, ok := z[m]; isZ && ok {
				return scoreLua(s)
			}
			return lua.LFalse
		}
		if argv[0] == "ZSCORE" {
			reply = get(argv[2])
		} else {
			out := L.NewTable()
			for _, m := range argv[2:] {
				out.Append(get(m))
			}
			reply = out
		}
	case "ZCARD":
		reply = lua.LNumber(len(z))
	case "ZCOUNT":
		n := 0
		for _, s := range z {
			if inBounds(s, argv[2], argv[3]) {
				n++
			}
		}
		reply = lua.LNumber(n)
	default:
		h.t.Fatalf("a probe the helper does not admit: %v", argv)
	}
	// Layer 1's readcmd refuses DRIFT (read_reservation) a reply larger than the
	// reserve the probe asked for.
	if bytes := payloadBytes(reply); bytes > reserve {
		return h.refusal(L, "DRIFT", map[string]any{"budget": "read_reservation", "actual": float64(bytes), "limit": float64(reserve)})
	}
	L.Push(reply)
	L.Push(lua.LNil)
	return 2
}

func (h *luaHarness) readRangeHead(L *lua.LState) int {
	key := L.CheckString(2)
	bounds := L.CheckTable(3)
	limit := int(L.CheckNumber(4))
	min, max := bounds.RawGetString("min").String(), bounds.RawGetString("max").String()
	h.charge.Probes++
	if h.charge.Probes > stubMaxProbes {
		return h.over(L, "cell", h.charge.Probes, stubMaxProbes)
	}
	z := h.zsets[key]
	type pair struct {
		m string
		s float64
	}
	var ps []pair
	for m, s := range z {
		if inBounds(s, min, max) {
			ps = append(ps, pair{m, s})
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].s != ps[j].s {
			return ps[i].s < ps[j].s
		}
		return ps[i].m < ps[j].m
	})
	more := len(ps) > limit
	if more {
		ps = ps[:limit]
	}
	ids, scores := h.newArray(), h.newArray()
	for _, p := range ps {
		ids.Append(lua.LString(p.m))
		scores.Append(lua.LString(formatScore(p.s)))
	}
	h.charge.RangeIDs += len(ps)
	if h.charge.RangeIDs > stubMaxRangeIDs {
		return h.over(L, "range_id", h.charge.RangeIDs, stubMaxRangeIDs)
	}
	out := L.NewTable()
	out.RawSetString("ids", ids)
	out.RawSetString("scores", scores)
	out.RawSetString("has_more", lua.LBool(more))
	L.Push(out)
	L.Push(lua.LNil)
	return 2
}

func (h *luaHarness) ensureReadTable(L *lua.LState) int {
	name := L.CheckString(2)
	def, ok := h.defs[name]
	if !ok {
		return h.refusal(L, "NOTABLE", map[string]any{"table": name})
	}
	out, cols, set, raw := L.NewTable(), L.NewTable(), L.NewTable(), L.NewTable()
	for _, c := range def.Columns {
		cols.Append(lua.LString(c))
		set.RawSetString(c, lua.LTrue)
		raw.RawSetString("col:"+c, lua.LString("set"))
	}
	out.RawSetString("name", lua.LString(name))
	out.RawSetString("columns", cols)
	out.RawSetString("column_set", set)
	out.RawSetString("raw", raw)
	L.Push(out)
	L.Push(lua.LNil)
	return 2
}

func (h *luaHarness) readLineAt(L *lua.LState) int {
	seq := int(L.CheckNumber(2))
	h.charge.Lines++
	if seq < 1 || seq > len(h.lines) {
		return h.refusal(L, "DRIFT", nil)
	}
	var v any
	if err := json.Unmarshal(h.lines[seq-1], &v); err != nil {
		h.t.Fatal(err)
	}
	L.Push(h.luaValue(v))
	L.Push(lua.LNil)
	return 2
}

// spec is the registered spec of a kind.
func (h *luaHarness) spec(kind string) *lua.LTable {
	sp := h.L.GetGlobal("NS").(*lua.LTable).RawGetString("SP").(*lua.LTable)
	return sp.RawGetString("queries").(*lua.LTable).RawGetString(kind).(*lua.LTable)
}

// refusalOf is a Lua refusal as (code, table, ids).
type refusalView struct {
	Code   string
	Table  string
	IDs    []string
	Cells  []string
	Budget string
}

func (h *luaHarness) view(v lua.LValue) *refusalView {
	t, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	d, _ := t.RawGetString("detail").(*lua.LTable)
	out := &refusalView{Code: t.RawGetString("code").String(), IDs: []string{}, Cells: []string{}}
	if d != nil {
		if tv := d.RawGetString("table"); tv != lua.LNil {
			out.Table = tv.String()
		}
		out.IDs = h.strings(d.RawGetString("ids"))
		out.Cells = h.strings(d.RawGetString("cells"))
		if bv := d.RawGetString("budget"); bv != lua.LNil {
			out.Budget = bv.String()
		}
	}
	return out
}

func goView(ref *Refusal) *refusalView {
	if ref == nil {
		return nil
	}
	return &refusalView{Code: ref.Code, Table: ref.Detail.Table, IDs: nonNilStrings(ref.Detail.IDs), Cells: nonNilStrings(ref.Detail.Cells),
		Budget: ref.Detail.Budget}
}

// validate is the Lua's validate of a query's wire object.
func (h *luaHarness) validate(q SprintQuery) *refusalView {
	h.t.Helper()
	h.L.SetTop(0)
	// A kind the registry does not hold is not a sprint query: Layer 1's validate
	// reads it as one of its own, and refuses it REQUEST.
	if sp := h.L.GetGlobal("NS").(*lua.LTable).RawGetString("SP").(*lua.LTable).RawGetString("queries").(*lua.LTable); sp.RawGetString(q.Kind) == lua.LNil {
		return &refusalView{Code: CodeRequest, IDs: []string{}, Cells: []string{}}
	}
	obj := h.call(h.L.GetGlobal("json_decode"), lua.LString(string(q.Query)))
	if err := h.L.CallByParam(lua.P{Fn: h.spec(q.Kind).RawGetString("validate"), NRet: 2, Protect: true}, obj, lua.LNumber(0)); err != nil {
		h.t.Fatalf("validate %s: %v", q.Kind, err)
	}
	refusal := h.L.Get(-1)
	h.L.Pop(2)
	return h.view(refusal)
}

// read runs the Lua's validate and then its read on a query, as ns_sprint_read
// does, and returns the answer as JSON, or the refusal.
func (h *luaHarness) read(q SprintQuery) (json.RawMessage, *refusalView) {
	h.t.Helper()
	if ref := h.validate(q); ref != nil {
		return nil, ref
	}
	h.prepare()
	return h.run(q)
}

// readSeq runs the queries of one read as ns_sprint_read does: every validate
// first, in order, before any store is read (Layer 1's S.validate), and then
// every read in order against one budget, the charge of each carried to the
// next. seed is what the read's other queries charged already. It returns the
// answers of the queries before the refusal, the refusal, and its index (-1
// when none).
func (h *luaHarness) readSeq(qs []SprintQuery, seed QueryCharge) ([]json.RawMessage, *refusalView, int) {
	h.t.Helper()
	for i, q := range qs {
		if ref := h.validate(q); ref != nil {
			return nil, ref, i
		}
	}
	h.carry = true
	defer func() { h.carry = false }()
	h.prepare()
	h.charge = seed
	var answers []json.RawMessage
	for i, q := range qs {
		got, ref := h.run(q)
		if ref != nil {
			return answers, ref, i
		}
		answers = append(answers, got)
	}
	return answers, nil, -1
}

// run is the Lua's read of one query, over the state prepare read, charging h.charge.
func (h *luaHarness) run(q SprintQuery) (json.RawMessage, *refusalView) {
	h.t.Helper()
	obj := h.call(h.L.GetGlobal("json_decode"), lua.LString(string(q.Query)))
	ctx := h.call(h.L.GetGlobal("new_ctx"), lua.LString(testPrefix), lua.LString(h.epoch), lua.LString(strconv.FormatInt(testTime.UnixMilli(), 10)))
	if err := h.L.CallByParam(lua.P{Fn: h.spec(q.Kind).RawGetString("read"), NRet: 2, Protect: true}, ctx, obj, lua.LNumber(0)); err != nil {
		h.t.Fatalf("read %s: %v", q.Kind, err)
	}
	ans, refusal := h.L.Get(-2), h.L.Get(-1)
	h.L.Pop(2)
	if refusal != lua.LNil {
		return nil, h.view(refusal)
	}
	b, err := json.Marshal(h.goValue(ans))
	if err != nil {
		h.t.Fatal(err)
	}
	return b, nil
}

// goRead is the twin's answer to the same wire object, with what it charged.
func (w *qworld) goRead(q SprintQuery) (json.RawMessage, *Refusal, QueryCharge) {
	w.t.Helper()
	w.tw.mu.Lock()
	defer w.tw.mu.Unlock()
	_, nowMS := w.tw.begin()
	e := w.tw.newEval(w.tw.active, nowMS)
	res, ref := e.evalWire(q)
	if ref != nil {
		return nil, ref, e.c
	}
	b, err := json.Marshal(res)
	if err != nil {
		w.t.Fatal(err)
	}
	return b, nil, e.c
}

func normalize(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return v
}

// agree checks the Lua's answer to one query against the twin's: the same
// answer, or the same refusal (its code, table and ids), and the same
// charges. label names the query in a failure.
func (h *luaHarness) agree(label string, q SprintQuery) {
	h.t.Helper()
	got, lref := h.read(q)
	want, gref, gcharge := h.w.goRead(q)
	if gref != nil {
		h.refused[q.Kind]++
	} else {
		h.answered[q.Kind]++
	}
	switch {
	case lref != nil || gref != nil:
		if !reflect.DeepEqual(lref, goView(gref)) {
			h.t.Errorf("%s: the Lua refused %+v, the twin %+v\n%s", label, lref, goView(gref), q.Query)
		}
	case !reflect.DeepEqual(normalize(h.t, got), normalize(h.t, want)):
		h.t.Errorf("%s: answers differ\nquery %s\nLua  %s\ntwin %s", label, q.Query, got, want)
	case !h.chargeEqual(gcharge):
		h.t.Errorf("%s: the Lua charged %+v, the twin %+v\n%s", label, h.charge, gcharge, q.Query)
	}
}

// chargeEqual compares the charges that both count: records, range ids
// (including the rows a listing reads), probes and lines. The twin's RowIDs
// is a subset of its RangeIDs and the stubs do not separate them.
func (h *luaHarness) chargeEqual(g QueryCharge) bool {
	c := h.charge
	return c.Records == g.Records && c.RangeIDs == g.RangeIDs && c.Probes == g.Probes && c.Lines == g.Lines
}

func mustEncode(t *testing.T, q sprint.SprintQ) SprintQuery {
	t.Helper()
	enc, ref := EncodeSprintQ(q)
	if ref != nil {
		t.Fatalf("%s: %v", q.Kind, ref)
	}
	return enc
}

func mustEncodeKey(t *testing.T, q KeyQ) SprintQuery {
	t.Helper()
	enc, ref := EncodeKeyQ(q)
	if ref != nil {
		t.Fatalf("%s: %v", q.Kind, ref)
	}
	return enc
}

// lua worlds: the standard one, one with its notes opened, one with marks in
// the quarantine (control cards and a need card among them) and a corrupt
// index, and two that put s2's cross need where the writer does not: in
// need_card alone, and in both fields with different cards.
func luaWorlds(t *testing.T) map[string]*qworld {
	t.Helper()
	plain := standard(t)
	notes := standard(t)
	notes.note("blocked", "c1", "p1", "p2")
	notes.note("stalled", "c2", "p2", "w1")
	quarantine := standard(t)
	quarantine.note("blocked", "c1", "p1", "p2")
	quarantine.seed(quarantine.hset("quarantine", "p2", "DRIFT", "f2", "DRIFT", "g1", "DRIFT", "w1", "DRIFT", "st2", "DRIFT",
		"v1.r1.r2", "DRIFT", "k1.w1", "DRIFT", "q1", "DRIFT", "d1", "DRIFT", "ctl-s1", "x", "ctl-m2", "x"))
	corrupt := standard(t)
	corrupt.seed(corrupt.zadd("elig:s9", "1", "phantom"), corrupt.zadd("sent:s7", "1", "nobody"))
	needCard := standardWith(t, fields("state", "stopped", "cause", "cross", "need_card", "p2"))
	both := standardWith(t, fields("state", "stopped", "cause", "cross", "other", "p2", "need_card", "p3"))
	return map[string]*qworld{"plain": plain, "notes": notes, "quarantine": quarantine, "corrupt": corrupt,
		"need_card": needCard, "both": both}
}

func lastNote(w *qworld) string { return "n" + strconv.Itoa(len(w.log.Lines(testPrefix, "0"))) }

// TestLuaQueriesOnStubbedHelpers: each query of the list, on four worlds
// (plain, with notes open, with marks in the quarantine, with an index that
// names a card that has no record), is answered by the Lua kinds and by the
// twin alike: the same answer object, key for key, or the same refusal (its
// code, its table and its ids), and the same records, range ids, probes and
// lines charged. The helpers are stubs, so this holds the Lua's logic and not
// Layer 1's.
func TestLuaQueriesOnStubbedHelpers(t *testing.T) {
	t.Parallel()
	for name, w := range luaWorlds(t) {
		w, name := w, name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newLuaHarness(t, w)
			note := lastNote(w)
			lineSeq := uint64(len(w.log.Lines(testPrefix, "0")))
			var list []sprint.SprintQ
			for _, f := range sprint.Follows {
				list = append(list,
					sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p1", "w1", "v1", "k1", "q1", "x1", "h1", "a1", "g1", "nobody"), Fields: []string{"attempt"}, Follow: []string{f}},
					sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Fleet, Source: ids("k1.w1", "a1.w2", "x1.w1"), Fields: []string{}, Follow: []string{f}},
					sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Merge, Source: ids("ctl-s1", "ctl-s2", "q1"), Fields: []string{"state"}, Follow: []string{f}},
					sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Readers, Source: ids("v1.r1.r1", "v1.r1.r2"), Fields: []string{"primary"}, Follow: []string{f}})
			}
			list = append(list,
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids("p1", "w1", "v1"), Fields: []string{"open", "needs"}, Follow: sprint.Follows},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s1", 3), Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s9", 3), Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("missing", 3), Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("s1:ready", 5), Fields: []string{"attempt"}, Follow: []string{sprint.FollowWork}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("s9:ready", 5), Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("s1:nocol", 5), Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Fleet, Source: head("m1:working", 5), Fields: []string{}, Follow: []string{sprint.FollowDue, sprint.FollowMember}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: lineSeq, About: true}, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: 2, Offset: 1, Limit: 3}, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: 99999}, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryRelated, Table: "nosuch", Source: ids("p1"), Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"attempt"}, Heads: []sprint.HeadQ{
					{Index: sprint.HeadEligBelow, Limit: 10, Follow: []string{sprint.FollowNeeds, sprint.FollowIndex}}, {Index: sprint.HeadFreshBelow, Limit: 10},
					{Index: sprint.HeadFreshAbove, Limit: 10}, {Index: sprint.HeadAgain, Limit: 10, Follow: []string{sprint.FollowWork, sprint.FollowWithdrawn, sprint.FollowDue}}}},
				sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadEligBelow, Limit: 1}}},
				sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s2", Fields: []string{"kind"}, Heads: []sprint.HeadQ{{Index: sprint.HeadAgain, Limit: 4}, {Index: sprint.HeadFreshAbove, Limit: 4}}},
				sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s9", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadEligBelow, Limit: 4}, {Index: sprint.HeadFreshAbove, Limit: 4}}},
				sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s7", Fields: []string{}, Heads: []sprint.HeadQ{}},
				sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s9", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadEligBelow, Limit: 4}}},
				sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost", "p1", "p2", "nobody"), Limit: 3, Fields: []string{"open"}},
				sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 1, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryWaiters, Source: head("missing", 4), Limit: 2, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryWaiters, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: 3, Limit: 5}, Limit: 2, Fields: []string{"kind"}},
				// IT08's extensions: the made needs, the sprint keys, a line's more_ids
				sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost", "p1"), Limit: 5, Fields: []string{"open"}, Missing: true, Keys: []string{sprint.KeyDropping}},
				sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 1, Fields: []string{}, Keys: []string{sprint.KeyDropping, sprint.KeyNextStreams}},
				sprint.SprintQ{Kind: sprint.QueryWaiters, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: lineSeq, About: true, Limit: 1}, Limit: 2, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadEligBelow, Limit: 1}}, Keys: []string{sprint.KeyNextStreams, sprint.KeyDropping}},
				sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 2, Counts: []string{"waiting", "ready", "landed"}, Keys: []string{sprint.KeyDropping, sprint.KeyNextStreams}},
				sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 0, Counts: []string{"waiting", "nocol"}},
				sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 2},
				sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 0},
				sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 5, Units: 1},
				sprint.SprintQ{Kind: sprint.QueryFleet, Fields: []string{"status"}},
				sprint.SprintQ{Kind: sprint.QueryFleet, Fields: []string{}, Units: 1},
				sprint.SprintQ{Kind: sprint.QueryReaders, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: ids("w1", "w2"), Limit: 5, Fields: []string{"open"}},
				sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: ids("w1"), Limit: 1, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: head("missing", 2), Limit: 3, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryNeedchain, Source: head("elig:s1", 3), Limit: 9, Fields: []string{}},
				sprint.SprintQ{Kind: sprint.QueryJnote, Source: head("jnotes", 3), Fields: []string{}, Subjects: 10},
				sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(note, "n1", "n5~2", "n7"), Fields: []string{}, Subjects: 10},
				sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(note), Fields: []string{}, Subjects: 1},
				sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(note), Fields: []string{}, Subjects: 5},
			)
			for i, q := range list {
				if ref := ValidateSprintQ(q); ref != nil {
					continue // a query the twin refuses statically is compared with the Lua's validate below
				}
				h.agree(fmt.Sprintf("%s #%d %s", name, i, q.Kind), mustEncode(t, q))
			}
			defer func() { t.Logf("%s: answered %v refused %v", name, h.answered, h.refused) }()
			keys := []KeyQ{{Kind: KeyClock}, {Kind: KeyLease}, {Kind: KeyTick}, {Kind: KeyHeartbeat}, {Kind: KeyDueCount},
				{Kind: KeyDropping, Streams: []string{"s1", "s2"}}, {Kind: KeyDropping, Streams: []string{}},
				{Kind: KeyParked, Keys: []string{"agenda-key-1", "x"}}, {Kind: KeyParked, Keys: []string{}},
				{Kind: KeyMissing, IDs: []string{"ghost", "p1", "x"}}, {Kind: KeyMissing, IDs: []string{}},
				{Kind: KeyBeat, IDs: []string{"m1", "ghost"}}, {Kind: KeyBeat, IDs: []string{}},
				{Kind: KeyJOpen, Subjects: []string{"p1", "p2", "zzz"}, Names: []string{"blocked|c1", "stalled|c2"}},
				{Kind: KeyJOpen, Subjects: []string{"p1"}, Names: []string{}}}
			for i, q := range keys {
				h.agree(fmt.Sprintf("%s key #%d %s", name, i, q.Kind), mustEncodeKey(t, q))
			}
		})
	}
}

// TestLuaBeatReadsPresentEntries: the beat read (fleet up's, 1.4.4) answers a
// member's beat:<m> score from the due set, null for a member with none, in the
// Lua as in the twin, on entries that are present (a whole and a fractional
// score), where TestLuaQueriesOnStubbedHelpers reads only absent ones.
func TestLuaBeatReadsPresentEntries(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.seed(w.zadd("due", "4242", "beat:m1", "5000.5", "beat:m3"))
	h := newLuaHarness(t, w)
	q := mustEncodeKey(t, KeyQ{Kind: KeyBeat, IDs: []string{"m1", "ghost", "m3"}})
	h.agree("beat present", q)
	got, ref := h.read(q)
	if ref != nil || !strings.Contains(string(got), `"4242",null,"5000.5"`) {
		t.Fatalf("the Lua's beat answer %s (refusal %v), want 4242, null, 5000.5", got, ref)
	}
}

// TestLuaValidateEqualsTwin: the Lua's validate and the twin's checks refuse
// the same queries with the same code: every encoded query is accepted by
// both, and every malformed or over-cost object of the wire test is refused by
// both with one code.
func TestLuaValidateEqualsTwin(t *testing.T) {
	t.Parallel()
	w := standard(t)
	h := newLuaHarness(t, w)
	many := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = `"c` + strconv.Itoa(i) + `"`
		}
		return strings.Join(ids, ",")
	}
	cases := []struct{ kind, raw string }{
		{"fleet", `{"kind":"fleet","fields":[],"extra":1}`},
		{"fleet", `{"kind":"fleet"}`},
		{"fleet", `{"kind":"fleet","fields":"a"}`},
		{"fleet", `{"kind":"fleet","fields":null}`},
		{"fleet", `{"kind":"fleet","fields":[],"units":"3"}`},
		{"fleet", `{"kind":"fleet","fields":[],"units":1.5}`},
		{"fleet", `{"kind":"fleet","fields":[],"units":251}`},
		{"fleet", `{"kind":"fleet","fields":[],"units":-1}`},
		{"fleet", `{"kind":"fleet","fields":["revision"]}`},
		{"fleet", `{"kind":"fleet","fields":["place:work"]}`},
		{"fleet", `{"kind":"fleet","fields":["a","a"]}`},
		{"fleet", `{"kind":"fleet","fields":["a b"]}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":5}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"05"}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"0"}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"9007199254740992"}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"9007199254740991"}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"5","offset":2000}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"5","offset":1999}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"5","about":true,"offset":3999}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"line","seq":"5","about":"yes"}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"missing","limit":1,"ids":[]}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"missing","limit":2001}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"wait:n1","limit":5}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"s1:ready","limit":5}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"a:b:c","limit":5}}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"nothing","limit":5}}`},
		{"waiters", `{"kind":"waiters","limit":0,"fields":[],"src":{"kind":"ids","ids":[]}}`},
		{"waiters", `{"kind":"waiters","limit":2001,"fields":[],"src":{"kind":"ids","ids":[]}}`},
		{"waiters", `{"kind":"waiters","limit":100,"fields":[],"src":{"kind":"ids","ids":[` + many(100) + `]}}`},
		{"waiters", `{"kind":"waiters","limit":99,"fields":[],"src":{"kind":"ids","ids":[` + many(100) + `]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":["nothing"],"src":{"kind":"ids","ids":[]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":["work","work"],"src":{"kind":"ids","ids":[]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":["a b"]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":["a","a"]}}`},
		{"related", `{"kind":"related","t":"a b","fields":[],"follow":[],"src":{"kind":"ids","ids":[]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many(10001) + `]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many(10000) + `]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":["work"],"src":{"kind":"ids","ids":[` + many(5001) + `]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":["needs"],"src":{"kind":"ids","ids":[` + many(155) + `]}}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":["needs"],"src":{"kind":"ids","ids":[` + many(154) + `]}}`},
		{"jnote", `{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":["x7"]}}`},
		{"jnote", `{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":["n07"]}}`},
		{"jnote", `{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":["n0"]}}`},
		{"jnote", `{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":["n7~01"]}}`},
		{"jnote", `{"kind":"jnote","fields":[],"subjects":10,"src":{"kind":"ids","ids":["n7","n8~3"]}}`},
		{"jnote", `{"kind":"jnote","fields":[],"src":{"kind":"head","key":"missing","limit":1}}`},
		{"jnote", `{"kind":"jnote","fields":[],"subjects":10,"src":{"kind":"head","key":"jnotes","limit":1}}`},
		{"jnote", `{"kind":"jnote","fields":[],"src":{"kind":"line","seq":"5"}}`},
		{"jnote", `{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":["n1","n2","n3"]}}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"nope","limit":1,"follow":[]}]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"again","limit":1,"follow":[]},{"index":"again","limit":2,"follow":[]}]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"again","limit":0,"follow":[]}]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"again","limit":2,"follow":[],"x":1}]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"elig","limit":1000,"follow":["rcards"]}]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"elig","limit":625,"follow":["rcards"]}]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"elig","limit":626,"follow":["rcards"]}]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"units":251}`},
		{"streams", `{"kind":"streams","fields":[]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0}`},
		{"streams", `{"kind":"streams","fields":[],"limit":"2"}`},
		{"streams", `{"kind":"streams","fields":[],"limit":100}`},
		{"streams", `{"kind":"streams","fields":[],"limit":80}`},
		{"streams", `{"kind":"streams","fields":[],"limit":81}`},
		{"needchain", `{"kind":"needchain","fields":[],"limit":10001,"src":{"kind":"ids","ids":[]}}`},
		{"needchain", `{"kind":"needchain","fields":[],"limit":10000,"src":{"kind":"ids","ids":[]}}`},
		{"needchain", `{"kind":"needchain","fields":[],"limit":0,"src":{"kind":"ids","ids":[]}}`},
		// IT08's extensions
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"after":"w1","missing":true,"keys":["dropping","next.streams"]}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"after":""}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n","m"]},"after":"w1"}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n","m"]},"after":""}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"head","key":"missing","limit":2},"after":"w1"}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"after":"a b"}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"after":7}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"after":null}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"missing":"yes"}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"missing":false}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"keys":["jopen:sprint"]}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"keys":["dropping","dropping"]}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"keys":[]}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"keys":"dropping"}`},
		{"waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":["n"]},"counts":["waiting"]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[],"keys":["jopen:G","dropping"]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[],"keys":["dropping"]}`},
		{"front", `{"kind":"front","fields":[],"stream":"s","heads":[],"missing":true}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"counts":["waiting","landed"],"keys":["next.streams"]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"counts":["waiting","waiting"]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"counts":["a b"]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"counts":null}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"counts":[` + many(33) + `]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"counts":[` + many(32) + `]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"keys":["jopen:sprint"]}`},
		{"streams", `{"kind":"streams","fields":[],"limit":0,"after":"x"}`},
		{"related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[]},"keys":["dropping"]}`},
		{"fleet", `{"kind":"fleet","fields":[],"keys":["next.streams"]}`},
	}
	for _, c := range cases {
		q := SprintQuery{Kind: c.kind, Query: json.RawMessage(c.raw)}
		_, gref := DecodeSprintQ(q)
		lref := h.validate(q)
		var lcode, gcode string
		if lref != nil {
			lcode = lref.Code
		}
		if gref != nil {
			gcode = gref.Code
		}
		if lcode != gcode {
			t.Errorf("%s: the Lua's validate says %q, the twin %q\n%.200s", c.kind, lcode, gcode, c.raw)
		}
	}
	keyCases := []struct{ kind, raw string }{
		{KeyTick, `{"kind":"tick","fields":["a"]}`},
		{KeyTick, `{"kind":"tick","fields":[]}`},
		{KeyTick, `{"kind":"tick","fields":[],"streams":[]}`},
		{KeyDropping, `{"kind":"dropping","fields":[]}`},
		{KeyDropping, `{"kind":"dropping","fields":[],"streams":["a","a"]}`},
		{KeyDropping, `{"kind":"dropping","fields":[],"streams":["a b"]}`},
		{KeyDropping, `{"kind":"dropping","fields":[],"streams":[]}`},
		{KeyParked, `{"kind":"parked","fields":[],"keys":["resolve:s1","deal@5+2000"]}`},
		{KeyParked, `{"kind":"parked","fields":[],"keys":[""]}`},
		{KeyParked, `{"kind":"parked","fields":[],"keys":["a\nb"]}`},
		{KeyMissing, `{"kind":"missing","fields":[],"ids":["a"]}`},
		{KeyMissing, `{"kind":"missing","fields":[]}`},
		{KeyBeat, `{"kind":"beat","fields":[],"ids":["m1"]}`},
		{KeyBeat, `{"kind":"beat","fields":[]}`},
		{KeyBeat, `{"kind":"beat","fields":[],"ids":["m1","m1"]}`},
		{KeyBeat, `{"kind":"beat","fields":[],"streams":["s1"]}`},
		{KeyJOpen, `{"kind":"jopen","fields":[],"subjects":["a"]}`},
		{KeyJOpen, `{"kind":"jopen","fields":[],"subjects":["a"],"names":["t|c"]}`},
		{KeyClock, `{"kind":"clock","fields":[]}`},
		{KeyDueCount, `{"kind":"duecount","fields":[],"ids":[]}`},
	}
	for _, c := range keyCases {
		q := SprintQuery{Kind: c.kind, Query: json.RawMessage(c.raw)}
		_, gref := DecodeKeyQ(q)
		lref := h.validate(q)
		if (lref == nil) != (gref == nil) || (lref != nil && lref.Code != gref.Code) {
			t.Errorf("%s: the Lua's validate says %+v, the twin %v\n%s", c.kind, lref, gref, c.raw)
		}
	}
}

// TestLuaRandomQueriesEqualTwin: 600 random queries on the four worlds,
// answered by the Lua kinds and by the twin alike (the random form of
// TestLuaQueriesOnStubbedHelpers: what no list of cases thought of).
func TestLuaRandomQueriesEqualTwin(t *testing.T) {
	t.Parallel()
	worlds := luaWorlds(t)
	names := make([]string, 0, len(worlds))
	for n := range worlds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		h := newLuaHarness(t, worlds[name])
		for i, q := range randomQueries(worlds[name], int64(len(name))*7919, 150) {
			h.agree(fmt.Sprintf("%s random #%d %s", name, i, q.Q.Kind), q.Enc)
		}
	}
}

// declared runs the Lua's cost of a query's wire object.
func (h *luaHarness) declared(q SprintQuery) (records, rangeIDs, probes int) {
	h.t.Helper()
	obj := h.call(h.L.GetGlobal("json_decode"), lua.LString(string(q.Query)))
	cost := h.call(h.spec(q.Kind).RawGetString("cost"), obj).(*lua.LTable)
	num := func(name string) int { return int(cost.RawGetString(name).(lua.LNumber)) }
	return num("records"), num("range_ids"), num("probes")
}

// TestLuaDeclaresTheCostsTheGoDoes: every kind declares its cost per query
// (spec.cost): for a composite query the records and range ids of IT05's
// QueryCost and the probes of QueryProbes, for a sprint-key read its probes
// (KeyProbes), equal for the queries of the lists and 600 random ones.
func TestLuaDeclaresTheCostsTheGoDoes(t *testing.T) {
	t.Parallel()
	w := standard(t)
	h := newLuaHarness(t, w)
	n := 0
	for _, rq := range randomQueries(w, 31337, 600) {
		cost := sprint.QueryCost(rq.Q)
		records, ranged, probes := h.declared(rq.Enc)
		if records != cost.Records || ranged != cost.RangeIDs || probes != QueryProbes(rq.Q) {
			t.Errorf("%s %s: the Lua declares %d records, %d range ids, %d probes; Go %+v, %d probes", rq.Q.Kind, rq.Enc.Query,
				records, ranged, probes, cost, QueryProbes(rq.Q))
		}
		n++
	}
	keys := []KeyQ{{Kind: KeyClock}, {Kind: KeyLease}, {Kind: KeyTick}, {Kind: KeyHeartbeat}, {Kind: KeyDueCount},
		{Kind: KeyDropping, Streams: []string{}}, {Kind: KeyDropping, Streams: []string{"a", "b"}},
		{Kind: KeyParked, Keys: []string{}}, {Kind: KeyParked, Keys: []string{"a"}},
		{Kind: KeyMissing, IDs: []string{}}, {Kind: KeyMissing, IDs: []string{"a", "b", "c"}},
		{Kind: KeyBeat, IDs: []string{}}, {Kind: KeyBeat, IDs: []string{"a", "b"}},
		{Kind: KeyJOpen, Subjects: []string{"a", "b"}, Names: []string{}}, {Kind: KeyJOpen, Subjects: []string{"a", "b"}, Names: []string{"t|c"}}}
	for _, q := range keys {
		records, ranged, probes := h.declared(mustEncodeKey(t, q))
		if records != 0 || ranged != 0 || probes != KeyProbes(q) {
			t.Errorf("%s: the Lua declares %d, %d, %d; Go %d probes", q.Kind, records, ranged, probes, KeyProbes(q))
		}
	}
	if n < 500 {
		t.Fatalf("only %d random queries compared", n)
	}
}

// mutate returns a copy of a decoded query object with one random change
// below its kind: a member deleted, a value replaced by one of another type or
// out of range, an unknown key added, an array given a repeat or cut short.
func mutate(rng *rand.Rand, v map[string]any) map[string]any {
	copyTree := func(x any) any {
		b, _ := json.Marshal(x)
		var out any
		_ = json.Unmarshal(b, &out)
		return out
	}
	root := copyTree(v).(map[string]any)
	type slot struct {
		get func() any
		set func(any)
		del func()
	}
	var slots []slot
	var walk func(x any, depth int)
	walk = func(x any, depth int) {
		switch n := x.(type) {
		case map[string]any:
			for k := range n {
				k := k
				if depth == 0 && k == "kind" {
					continue
				}
				slots = append(slots, slot{get: func() any { return n[k] }, set: func(y any) { n[k] = y }, del: func() { delete(n, k) }})
				walk(n[k], depth+1)
			}
			slots = append(slots, slot{get: func() any { return n }, set: func(any) { n["zz_unknown"] = 1 }, del: func() {}})
		case []any:
			for i := range n {
				i := i
				slots = append(slots, slot{get: func() any { return n[i] }, set: func(y any) { n[i] = y }, del: func() {}})
				walk(n[i], depth+1)
			}
		}
	}
	walk(root, 0)
	if len(slots) == 0 {
		return root
	}
	s := slots[rng.Intn(len(slots))]
	switch rng.Intn(9) {
	case 0:
		s.del()
	case 1:
		s.set(nil)
	case 2:
		s.set("a b")
	case 3:
		s.set(float64(-1))
	case 4:
		s.set(float64(2001))
	case 5:
		s.set(1.5)
	case 6:
		s.set([]any{})
	case 7:
		s.set("")
	default:
		if list, ok := s.get().([]any); ok && len(list) > 0 {
			s.set(append(list, list[0]))
		} else {
			s.set(map[string]any{})
		}
	}
	return root
}

// TestLuaValidateEqualsTwinMutated: the same as TestLuaValidateEqualsTwin over
// 3,000 queries that were valid until one member was deleted, retyped, put out
// of range or repeated: the Lua's validate and the twin's checks agree on what
// is refused and with which code.
func TestLuaValidateEqualsTwinMutated(t *testing.T) {
	t.Parallel()
	w := standard(t)
	h := newLuaHarness(t, w)
	rng := rand.New(rand.NewSource(20260930))
	var bases []SprintQuery
	for _, rq := range randomQueries(w, 99, 300) {
		bases = append(bases, rq.Enc)
	}
	for _, q := range []KeyQ{{Kind: KeyClock}, {Kind: KeyDropping, Streams: []string{"s1"}}, {Kind: KeyParked, Keys: []string{"a", "b"}},
		{Kind: KeyMissing, IDs: []string{"a"}}, {Kind: KeyBeat, IDs: []string{"a"}}, {Kind: KeyJOpen, Subjects: []string{"a"}, Names: []string{"t|c"}}, {Kind: KeyDueCount}} {
		bases = append(bases, mustEncodeKey(t, q))
	}
	key := map[string]bool{}
	for _, k := range SprintKeyKinds {
		key[k] = true
	}
	refused, accepted := 0, 0
	for i := 0; i < 3000; i++ {
		base := bases[rng.Intn(len(bases))]
		var obj map[string]any
		if err := json.Unmarshal(base.Query, &obj); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(mutate(rng, obj))
		q := SprintQuery{Kind: base.Kind, Query: raw}
		var gref *Refusal
		if key[q.Kind] {
			_, gref = DecodeKeyQ(q)
		} else {
			_, gref = DecodeSprintQ(q)
		}
		lref := h.validate(q)
		var lcode, gcode string
		if lref != nil {
			lcode = lref.Code
		}
		if gref != nil {
			gcode = gref.Code
		}
		if lcode != gcode {
			t.Errorf("%s: the Lua's validate says %q, the twin %q\n%.300s", q.Kind, lcode, gcode, raw)
		}
		if gref != nil {
			refused++
		} else {
			accepted++
		}
	}
	if refused < 500 || accepted < 100 {
		t.Fatalf("the mutations were refused %d times and accepted %d: the test does not test both", refused, accepted)
	}
}
