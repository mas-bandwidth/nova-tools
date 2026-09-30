package sprintfn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The Lua half of the parts runs here, under gopher-lua, against stubs of
// Layer 1's helpers (S.rd, S.charge, S.command, S.before, S.refuse and the
// decimal helpers) over a fake keyspace taken from the twin, and is compared
// with the Go parts on the same state and the same requests: the same reply,
// the same commands in the same order, the same refusal. No store is involved,
// and what the stubs stand in for is Layer 1's to test at G0. The stubs keep
// Layer 1's own accounts, as table_set.lua's S.readcmd, S.charge and S.writecmd
// do: a reply larger than its reservation is DRIFT, a cell, a range id or a
// fetched byte past its limit is LIMIT, and every command counts against the
// planned commands and argv bytes; so a cost a part understates fails here as it
// would on the store, and the number of reads a part makes is counted (a test
// holds it to a count that does not grow with the input).

// luaValueOf converts what encoding/json decodes into a Lua value.
func luaValueOf(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case string:
		return lua.LString(x)
	case []any:
		t := L.NewTable()
		for _, e := range x {
			t.Append(luaValueOf(L, e))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, e := range x {
			t.RawSetString(k, luaValueOf(L, e))
		}
		return t
	}
	panic(fmt.Sprintf("no Lua value for %T", v))
}

// goValueOf converts a Lua value into what encoding/json decodes: a table with
// the __arr marker (S.array) or a sequence is an array, any other an object.
func goValueOf(v lua.LValue) any {
	switch x := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(x)
	case lua.LNumber:
		return float64(x)
	case lua.LString:
		return string(x)
	case *lua.LTable:
		if x.RawGetString("__arr") == lua.LTrue || x.Len() > 0 {
			out := make([]any, 0, x.Len())
			for i := 1; i <= x.Len(); i++ {
				out = append(out, goValueOf(x.RawGetInt(i)))
			}
			return out
		}
		out := map[string]any{}
		x.ForEach(func(k, e lua.LValue) { out[k.String()] = goValueOf(e) })
		return out
	}
	panic(fmt.Sprintf("no Go value for %T", v))
}

// luaStubEnv is Layer 1's S as the parts call it. S.limits, S.payload, S.charge,
// S.rd and S.command follow table_set.lua's S.limits, payload, S.charge,
// S.readcmd and S.writecmd: the bounds, the accounts and the refusals.
const luaStubEnv = `
NS = {tset_profile = 'sprint'}
local S = {}
NS.tset = S
S.limits = {request_bytes = 4194304, field_value = 65536, result = 4096, cell = 20000, range_id = 20000,
  record = 10000, commands = 65536, argv_bytes = 8388608, fetched_bytes = 8388608}
function S.refuse(code, detail, message)
  return {status = 'refused', code = code, detail = detail or {}, message = (message or code) .. '; nothing was changed'}
end
function S.array() return {__arr = true} end
function S.utf8_valid(s) return type(s) == 'string' and go_utf8_valid(s) end
local function payload(v)
  if type(v) == 'string' then return #v end
  if type(v) == 'number' then return #tostring(v) end
  local n = 0
  if type(v) == 'table' then
    for k, x in pairs(v) do
      if type(k) == 'string' then n = n + #k end
      n = n + payload(x)
    end
  end
  return n
end
function S.charge(ctx, unit, count)
  if type(count) ~= 'number' or count < 0 or count ~= math.floor(count) then return nil, S.refuse('REQUEST') end
  local cap = S.limits[unit]
  if not cap then return nil, S.refuse('REQUEST') end
  local n = (ctx.budget[unit] or 0) + count
  if n > cap then return nil, S.refuse('LIMIT', {budget = unit, actual = n, limit = cap}) end
  ctx.budget[unit] = n
  return true, nil
end
function S.rd(ctx, argv, kind, reserve, probe)
  if type(reserve) ~= 'number' or reserve < 0 or reserve ~= math.floor(reserve) then return nil, S.refuse('REQUEST') end
  if ctx.budget.fetched_bytes + reserve > S.limits.fetched_bytes then
    return nil, S.refuse('LIMIT', {budget = 'fetched_bytes', actual = ctx.budget.fetched_bytes + reserve, limit = S.limits.fetched_bytes})
  end
  if probe ~= 'field' and probe ~= 'record' then
    local _, err = S.charge(ctx, 'cell', 1)
    if err then return nil, err end
  end
  ctx.reads[#ctx.reads + 1] = argv[1]
  local value = fake_rd(argv)
  local bytes = payload(value)
  if bytes > reserve then
    return nil, S.refuse('DRIFT', {budget = 'read_reservation', actual = bytes, limit = reserve})
  end
  ctx.budget.fetched_bytes = ctx.budget.fetched_bytes + bytes
  return value, nil
end
function S.before(ctx, t, ids, fields) return fake_before(t, ids), nil end
function S.command(ctx, command, key, kind, args)
  local argv = {command, key}
  for _, v in ipairs(args or {}) do
    assert(type(v) == 'string', 'a non-scalar argv')
    argv[#argv + 1] = v
  end
  if #argv > 2002 then return nil, S.refuse('LIMIT', {budget = 'argv', actual = #argv, limit = 2002}) end
  local bytes = 0
  for i = 1, #argv do bytes = bytes + #argv[i] end
  local commands = ctx.budget.planned_commands + 1
  local total = ctx.budget.planned_argv_bytes + bytes
  if commands > S.limits.commands then return nil, S.refuse('LIMIT', {budget = 'commands', actual = commands, limit = S.limits.commands}) end
  if total > S.limits.argv_bytes then return nil, S.refuse('LIMIT', {budget = 'argv_bytes', actual = total, limit = S.limits.argv_bytes}) end
  ctx.budget.planned_commands, ctx.budget.planned_argv_bytes = commands, total
  return {argv = argv, access = {{key = key, kind = kind, mode = 'write'}}}, nil
end
local U64_MAX = '18446744073709551615'
function S.uint(s)
  return type(s) == 'string' and #s > 0 and #s <= 20 and (s == '0' or s:match('^[1-9][0-9]*$') ~= nil) and (#s < 20 or s <= U64_MAX)
end
function S.cmp(a, b)
  if #a ~= #b then return #a < #b and -1 or 1 end
  if a == b then return 0 end
  return a < b and -1 or 1
end
function S.next(s)
  if not S.uint(s) or s == U64_MAX then return nil end
  local i, carry, digits = #s, 1, {}
  while i > 0 do
    local d = string.byte(s, i) - 48 + carry
    if d == 10 then d, carry = 0, 1 else carry = 0 end
    digits[i] = string.char(d + 48)
    i = i - 1
  end
  local out = table.concat(digits)
  return carry == 1 and '1' .. out or out
end
`

// luaParts is the Lua half of the parts over a fake keyspace.
type luaParts struct {
	t      *testing.T
	L      *lua.LState
	keys   map[string]KeyValue
	before map[string]map[string]tset.MemberRecord
}

func newLuaParts(t *testing.T) *luaParts {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	h := &luaParts{t: t, L: L}
	L.SetGlobal("go_utf8_valid", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LBool(utf8.ValidString(L.CheckString(1))))
		return 1
	}))
	L.SetGlobal("fake_rd", L.NewFunction(func(L *lua.LState) int {
		tab := L.CheckTable(1)
		var argv []string
		for i := 1; i <= tab.Len(); i++ {
			argv = append(argv, tab.RawGetInt(i).String())
		}
		L.Push(h.read(argv))
		return 1
	}))
	L.SetGlobal("fake_before", L.NewFunction(func(L *lua.LState) int {
		table, ids := L.CheckString(1), L.CheckTable(2)
		out := L.NewTable()
		for i := 1; i <= ids.Len(); i++ {
			id := ids.RawGetInt(i).String()
			rec, ok := h.before[table][id]
			if !ok {
				continue
			}
			r := L.NewTable()
			r.RawSetString("exists", lua.LBool(rec.Exists))
			fields := L.NewTable()
			for f, v := range rec.Fields {
				fv := L.NewTable()
				fv.RawSetString("present", lua.LBool(v.Present))
				fv.RawSetString("value", lua.LString(v.Value))
				fields.RawSetString(f, fv)
			}
			r.RawSetString("fields", fields)
			out.RawSetString(id, r)
		}
		L.Push(out)
		return 1
	}))
	if err := L.DoString(luaStubEnv); err != nil {
		t.Fatal(err)
	}
	frags, err := fn.SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	parts, err := fn.SprintPartsFragment()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{frags[0].Source, parts.Source} {
		if err := L.DoString(src); err != nil {
			t.Fatalf("load: %v", err)
		}
	}
	return h
}

// read answers the typed reads the parts make, as the store does: a missing
// value is false.
func (h *luaParts) read(argv []string) lua.LValue {
	key := argv[1]
	v := h.keys[key]
	score := func(m string) lua.LValue {
		s, ok := v.ZSet[m]
		if !ok {
			return lua.LFalse
		}
		return lua.LString(strconv.FormatFloat(s, 'f', -1, 64))
	}
	switch argv[0] {
	case "TYPE":
		kind := v.Kind
		if kind == "" {
			kind = "none"
		}
		status := h.L.NewTable() // the store's status reply
		status.RawSetString("ok", lua.LString(kind))
		return status
	case "GET":
		if v.Kind != kindString {
			return lua.LFalse
		}
		return lua.LString(v.String)
	case "HGET", "HMGET":
		get := func(f string) lua.LValue {
			if x, ok := v.Hash[f]; ok {
				return lua.LString(x)
			}
			return lua.LFalse
		}
		if argv[0] == "HGET" {
			return get(argv[2])
		}
		out := h.L.NewTable()
		for _, f := range argv[2:] {
			out.Append(get(f))
		}
		return out
	case "ZMSCORE":
		out := h.L.NewTable()
		for _, m := range argv[2:] {
			out.Append(score(m))
		}
		return out
	case "ZCARD":
		return lua.LNumber(len(v.ZSet))
	case "ZRANGEBYSCORE":
		bound := func(s string) float64 {
			switch s {
			case "-inf":
				return math.Inf(-1)
			case "+inf":
				return math.Inf(1)
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				h.t.Fatalf("bad bound %q", s)
			}
			return f
		}
		lim, err := strconv.Atoi(argv[6])
		if err != nil || argv[4] != "LIMIT" {
			h.t.Fatalf("unexpected ZRANGEBYSCORE %v", argv)
		}
		members := (&Keys{ks: &keyspace{vals: map[string]*keyValue{key: {kind: kindZSet, zset: v.ZSet}}}}).ZRangeByScore(key, bound(argv[2]), bound(argv[3]), lim)
		out := h.L.NewTable()
		for _, m := range members {
			out.Append(lua.LString(m.Member))
		}
		return out
	}
	h.t.Fatalf("the stub does not serve %v", argv)
	return lua.LNil
}

// luaResult is what one part's pre and cmds gave.
type luaResult struct {
	plan    any
	cmds    [][]string
	refusal *Refusal
}

// run calls the Lua part of one name on a request's sprint half, the state of
// the twin and the before-state the twin read.
func (h *luaParts) run(name string, req *Request, tw *Twin, clk *stepClock, obs *Before, ctx *lua.LTable) luaResult {
	h.t.Helper()
	h.keys, h.before = tw.SprintKeys(), obs.Records
	raw, err := json.Marshal(sprintWireOf(req))
	if err != nil {
		h.t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		h.t.Fatal(err)
	}
	L := h.L
	parts := L.GetGlobal("NS").(*lua.LTable).RawGetString("SP").(*lua.LTable).RawGetString("parts").(*lua.LTable)
	part, ok := parts.RawGetString(name).(*lua.LTable)
	if !ok {
		h.t.Fatalf("the Lua registered no part %s", name)
	}
	sp := luaValueOf(L, decoded)
	call := func(fn lua.LValue, args ...lua.LValue) []lua.LValue {
		if err := L.CallByParam(lua.P{Fn: fn, NRet: 2, Protect: true}, args...); err != nil {
			h.t.Fatalf("lua %s: %v", name, err)
		}
		a, b := L.Get(-2), L.Get(-1)
		L.Pop(2)
		return []lua.LValue{a, b}
	}
	pre := call(part.RawGetString("pre"), ctx, sp, L.NewTable())
	if pre[0] == lua.LNil {
		ref := goValueOf(pre[1]).(map[string]any)
		r := &Refusal{Code: ref["code"].(string), Message: ref["message"].(string)}
		if d, ok := ref["detail"].(map[string]any); ok {
			if cur, ok := d["cur"].(string); ok {
				r.Detail.Cur = tset.Decimal(cur)
			}
			if ids, ok := d["ids"].([]any); ok {
				for _, id := range ids {
					r.Detail.IDs = append(r.Detail.IDs, id.(string))
				}
			}
		}
		return luaResult{refusal: r}
	}
	res := luaResult{plan: goValueOf(pre[0])}
	cmds := call(part.RawGetString("cmds"), ctx, pre[0], L.NewTable())
	if cmds[0] == lua.LNil {
		h.t.Fatalf("lua cmds refused: %v", goValueOf(cmds[1]))
	}
	list := cmds[0].(*lua.LTable)
	for i := 1; i <= list.Len(); i++ {
		var argv []string
		for _, a := range goValueOf(list.RawGetInt(i)).(map[string]any)["argv"].([]any) {
			argv = append(argv, a.(string))
		}
		res.cmds = append(res.cmds, argv)
	}
	return res
}

// newCtx is the ctx of one step as the parts read it: the call's time and
// epochs, and the accounts of Layer 1's S.context (budget) and the list of the
// reads made (reads).
func (h *luaParts) newCtx(tw *Twin, clk *stepClock, req *Request) *lua.LTable {
	ctx := h.L.NewTable()
	ctx.RawSetString("space", lua.LString(testPrefix))
	ctx.RawSetString("now_ms", lua.LString(strconv.FormatInt(clk.ms(), 10)))
	ctx.RawSetString("request_epoch", lua.LString(string(req.Epoch)))
	ctx.RawSetString("write_epoch", lua.LString(string(partEpoch(req))))
	budget := h.L.NewTable()
	for _, f := range []string{"fetched_bytes", "planned_commands", "planned_argv_bytes"} {
		budget.RawSetString(f, lua.LNumber(0))
	}
	ctx.RawSetString("budget", budget)
	ctx.RawSetString("reads", h.L.NewTable())
	return ctx
}

// readsOf counts the reads a ctx recorded, by command.
func readsOf(ctx *lua.LTable) map[string]int {
	out := map[string]int{}
	list, _ := ctx.RawGetString("reads").(*lua.LTable)
	for i := 1; list != nil && i <= list.Len(); i++ {
		out[list.RawGetInt(i).String()]++
	}
	return out
}

// withShares gives a Lua ctx a smaller budget for the parts together, as State's
// shares do for the Go parts.
func withShares(h *luaParts, ctx *lua.LTable, commands, argvBytes int) {
	t := h.L.NewTable()
	t.RawSetString("commands", lua.LNumber(commands))
	t.RawSetString("bytes", lua.LNumber(argvBytes))
	ctx.RawSetString("sp_shares", t)
}

// goResult is the same for the Go part, on a State of its own.
func goResult(tw *Twin, clk *stepClock, name string, req *Request, obs *Before) luaResult {
	return goResultAt(tw, tw.partState(clk, req.Epoch), name, req, obs)
}

// goResultAt is the same on the State given, which the parts of one step share
// (their running total of commands and argv bytes lies in it).
func goResultAt(tw *Twin, st *State, name string, req *Request, obs *Before) luaResult {
	p, _ := tw.parts.Lookup(name)
	plan, ref := p.Pre(st, req, obs)
	if ref != nil {
		return luaResult{refusal: ref}
	}
	b, err := json.Marshal(plan)
	if err != nil {
		panic(err)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		panic(err)
	}
	cmds, ref := p.Cmds(st, plan, LogPlan{})
	if ref != nil {
		panic(ref)
	}
	res := luaResult{plan: decoded}
	for _, c := range cmds {
		res.cmds = append(res.cmds, c.Argv)
	}
	return res
}

// sameResult compares the Go part's result with the Lua's.
func sameResult(g, l luaResult) string {
	switch {
	case g.refusal != nil && l.refusal != nil:
		if g.refusal.Code != l.refusal.Code || g.refusal.Message != l.refusal.Message || g.refusal.Detail.Cur != l.refusal.Detail.Cur ||
			!reflect.DeepEqual(append([]string{}, g.refusal.Detail.IDs...), append([]string{}, l.refusal.Detail.IDs...)) {
			return fmt.Sprintf("refusals differ:\n go  %s %q cur %q ids %v\n lua %s %q cur %q ids %v", g.refusal.Code, g.refusal.Message,
				g.refusal.Detail.Cur, g.refusal.Detail.IDs, l.refusal.Code, l.refusal.Message, l.refusal.Detail.Cur, l.refusal.Detail.IDs)
		}
		return ""
	case g.refusal != nil:
		return fmt.Sprintf("Go refused %s %q, Lua planned %v", g.refusal.Code, g.refusal.Message, l.plan)
	case l.refusal != nil:
		return fmt.Sprintf("Lua refused %s %q, Go planned %v", l.refusal.Code, l.refusal.Message, g.plan)
	}
	if !reflect.DeepEqual(g.plan, l.plan) {
		return fmt.Sprintf("replies differ:\n go  %v\n lua %v", g.plan, l.plan)
	}
	if !reflect.DeepEqual(g.cmds, l.cmds) {
		return fmt.Sprintf("commands differ:\n go  %v\n lua %v", g.cmds, l.cmds)
	}
	return ""
}

// randomPartsRequest builds a request carrying a random subset of the parts,
// mostly valid and sometimes not.
func randomPartsRequest(rng *rand.Rand, tw *Twin, clk *stepClock, epoch tset.Decimal) *Request {
	keys := tw.SprintKeys()
	gen, _ := strconv.ParseUint(keys[sk("lease")].Hash["gen"], 10, 64)
	cur := keys[ekAt("tick", epoch)].Hash["cur"]
	curN, _ := strconv.ParseUint(cur, 10, 64)
	req := &Request{Epoch: epoch, Meta: Meta{Verb: "random"}}
	pick := func(p float64) bool { return rng.Float64() < p }
	choose := func(xs ...string) string { return xs[rng.Intn(len(xs))] }

	if pick(0.35) {
		hb := map[string]string{}
		for _, f := range HeartbeatFields() {
			if pick(0.3) {
				hb[f] = choose("1", "x", "", "a b")
			}
		}
		if pick(0.03) {
			hb[choose("owner", "gen", "nonsense")] = "x"
		}
		req.Lease = &LeasePart{Owner: choose("tok-a", "tok-b"), Name: choose("loop-a", "loop-b"),
			HoldMS: int64([]int{500, 4000, 60000, int(LeaseHoldMaxMS)}[rng.Intn(4)]), Heartbeat: hb, Stopped: pick(0.15)}
		if pick(0.02) {
			req.Lease.Name = "" // the idle loop's name is never empty: REQUEST in both halves
		}
	}
	if pick(0.35) {
		req.Pop = &PopPart{Limit: []int{1, 3, 50, 1000}[rng.Intn(4)]}
	}
	if pick(0.3) {
		from := curN
		if pick(0.1) {
			from += uint64(rng.Intn(3))
		}
		to := from + uint64(rng.Intn(6))
		in := &IngestPart{From: tset.Decimal(strconv.FormatUint(from, 10)), To: tset.Decimal(strconv.FormatUint(to, 10))}
		if to > from {
			for i := rng.Intn(5); i > 0; i-- {
				seq := from + 1 + uint64(rng.Intn(int(to-from)))
				if pick(0.05) {
					seq = to + 1
				}
				in.Keys = append(in.Keys, sprint.AgendaKey{Key: choose("deal", "resolve:s1", "ask:p1", "held:p1", "held@7", "needs:p2", "late:x", "done"), Seq: seq})
			}
		}
		req.Ingest = in
	}
	if pick(0.25) {
		b := &BeatPart{}
		for _, m := range []string{"m1", "m2", "m3", "m4", "m5"} {
			if pick(0.5) {
				b.Members = append(b.Members, BeatMember{Member: m, Load: choose("", "0.5", "1 2 3")})
			}
		}
		if len(b.Members) == 0 {
			b.Members = []BeatMember{{Member: "m1"}}
		}
		req.Beat = b
	}
	if pick(0.12) {
		req.Clock = &ClockPart{Verb: choose(ClockInit, ClockStart, ClockStop, ClockClear, ClockStop, ClockStart)}
	}
	if pick(0.4) {
		sp := &SprintPart{}
		next := keys[ekAt("next", epoch)].Hash
		if pick(0.5) {
			read := map[string]string{}
			set := map[string]string{}
			for _, f := range []string{"score", "streams", "id:s1", "gate:s1"} {
				if pick(0.6) {
					read[f] = next[f]
					if pick(0.1) {
						read[f] = choose("", "1", "5")
					}
					cur := read[f]
					if cur == "" {
						cur = "0"
					}
					n, _ := strconv.ParseUint(cur, 10, 64)
					if pick(0.9) {
						set[f] = strconv.FormatUint(n+uint64(rng.Intn(5)), 10)
					} else if n > 0 {
						set[f] = strconv.FormatUint(n-1, 10)
					}
				}
			}
			sp.Counter = &CounterChange{Read: read, Set: set}
		}
		if pick(0.25) {
			sp.Dropping = map[string]string{choose("s1", "s2"): choose("op-1", "op-2")}
		}
		if pick(0.2) {
			sp.Undrop = map[string]string{choose("s1", "s2", "s3"): choose("op-1", "op-2")}
		}
		if pick(0.25) {
			for i := rng.Intn(2) + 1; i > 0; i-- {
				sp.Park = append(sp.Park, ParkedKey{Key: choose("deal", "ask:p1", "resolve:s1", "overdue:n5"), Rule: choose("", "deal"),
					Code: choose("LIMIT", "REQUEST"), Budget: choose("", "commands"), Actual: choose("", "70000"), Limit: choose("", "65536")})
			}
		}
		if pick(0.15) {
			sp.Unpark = []string{choose("deal", "ask:p1", "resolve:s1", "overdue:n5")}
		}
		if pick(0.2) {
			sp.Coordinator = choose("boss", "other")
		}
		if pick(0.03) {
			sp.Sweep = "s2"
		}
		if pick(0.03) {
			sp.Goals = map[string]string{"ann": "g"}
		}
		if pick(0.4) {
			sp.TickEnd = &TickEnd{Backlog: tset.Decimal(choose("0", "0", "1", "7", "123456"))}
		}
		if pick(0.25) {
			for i := rng.Intn(3) + 1; i > 0; i-- {
				sp.Quarantine = append(sp.Quarantine, Quarantined{ID: choose("p1", "p2", "p3", "p4"),
					Code: choose("DRIFT", "MISSING"), Stream: choose("", "s1"), Rule: choose("", "deal"),
					Cells: [][]string{nil, {"s1:ready"}, {"s1:ready", "s1:waiting"}}[rng.Intn(3)]})
			}
			if pick(0.5) {
				req.Body.Quarantine = append([]Quarantined(nil), sp.Quarantine...) // X acts on the same cards
			}
		}
		req.Sprint = sp
	}
	if req.Sprint == nil && req.Lease == nil && req.Pop == nil && req.Ingest == nil && req.Beat == nil && req.Clock == nil {
		req.Pop = &PopPart{Limit: 10}
	}
	if req.Lease == nil || pick(0.5) {
		g := gen
		if pick(0.2) {
			g = uint64(rng.Intn(4))
		}
		req.Meta.Gen, req.Meta.Tick = g, true
	}
	return req
}

// TestPartsLuaMatchesTwin: the Lua parts and the Go parts, given the same
// state and the same request, give the same reply, the same commands in the
// same order, and the same refusal, over a seeded random walk of 600 requests
// that move the twin forward (time, leases, cursors, marks, parks, quarantines,
// beats, clock verbs) and seed due entries, cuts and agenda keys between them.
// The store's half of the same check waits for G0 (TestTwinEqualsLua10000).
func TestPartsLuaMatchesTwin(t *testing.T) {
	t.Parallel()
	for _, seedN := range []int64{1, 2, 3, 4} {
		seedN := seedN
		epoch := tset.Decimal("0")
		if seedN > 2 {
			epoch = "2" // the walk at a later epoch: per-epoch keys and stored ids
		}
		t.Run("seed "+strconv.FormatInt(seedN, 10), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seedN))
			tw, _, _, clk := partsTwinAt(t, epoch)
			fleetSeedAt(t, tw, epoch)
			h := newLuaParts(t)
			compared := map[string]int{}
			refused := map[string]int{}
			branches := map[string]int{}
			for step := 0; step < 600; step++ {
				if rng.Float64() < 0.3 {
					now := clk.ms()
					kinds := []string{"untaken:p1.w1", "unbegun:p2.r1.rd", "beat:m1", "seen:m2", "overdue:n5", "idle:s1", "behind", "remind:ann", "hold:n2"}
					seed(tw, Command("ZADD", ekAt("due", epoch), kindZSet, strconv.FormatInt(now-int64(rng.Intn(20000)), 10), kinds[rng.Intn(len(kinds))],
						strconv.FormatInt(now+int64(rng.Intn(20000)), 10), kinds[rng.Intn(len(kinds))]))
					seed(tw, Command("ZADD", ekAt("cut", epoch), kindZSet, strconv.FormatInt(now-int64(rng.Intn(5000)), 10), "cut:op-"+strconv.Itoa(rng.Intn(5))))
					seed(tw, Command("ZADD", ekAt("agenda", epoch), kindZSet, strconv.Itoa(rng.Intn(50)), []string{"deal", "ask:p1", "resolve:s1", "down:m1"}[rng.Intn(4)]))
				}
				if rng.Float64() < 0.3 {
					for _, k := range []string{"deal", "ask:p1", "overdue:n5"} { // a key the error step parked earlier
						if rng.Float64() < 0.4 {
							seed(tw, Command("HSET", ekAt("parked", epoch), kindHash, k, "LIMIT\t\tcommands\t70000\t65536"))
						}
					}
				}
				if rng.Float64() < 0.1 && tw.SprintKeys()[sk("clock")].Kind == kindHash { // R17's bookkeeping of a span
					seed(tw, Command("HSET", sk("clock"), kindHash, "due_since_ms", strconv.FormatInt(clk.ms()-int64(rng.Intn(9000)), 10),
						"stophold_ms", strconv.FormatInt(clk.ms()+int64(rng.Intn(9000)), 10), "stopraised_ms", strconv.Itoa(rng.Intn(9))))
				}
				req := randomPartsRequest(rng, tw, clk, epoch)
				if _, ref := encodeStep(testPrefix, req); ref != nil {
					continue // the client refuses it before any part runs
				}
				st := tw.partState(clk, req.Epoch)
				obs, ref := tw.before(context.Background(), st, req)
				if ref != nil {
					t.Fatalf("step %d: before refused %v", step, ref)
				}
				ctx := h.newCtx(tw, clk, req)
				for _, name := range PartOrder {
					if !requestPart(req, name) {
						continue
					}
					g := goResultAt(tw, st, name, req, obs) // one State for the parts of the step, as the twin has it
					l := h.run(name, req, tw, clk, obs, ctx)
					if diff := sameResult(g, l); diff != "" {
						b, _ := json.Marshal(sprintWireOf(req))
						t.Fatalf("seed %d step %d part %s: %s\nrequest %s\nstate %s", seedN, step, name, diff, b, dumpKeys(tw.SprintKeys()))
					}
					compared[name]++
					if g.refusal != nil {
						refused[name+" "+g.refusal.Code]++
					}
					for _, c := range branchesOf(name, req, g) {
						branches[c]++
					}
				}
				tw.Pipeline(context.Background(), []Item{{Step: req}})
				clk.advance(time.Duration(rng.Intn(8)) * time.Second / 2)
			}
			for _, name := range PartOrder {
				if compared[name] < 40 {
					t.Errorf("part %s was compared only %d times", name, compared[name])
				}
			}
			for _, b := range walkBranches {
				if branches[b] < 1 {
					t.Errorf("the walk never reached %q", b)
				}
			}
			t.Logf("compared %v; refusals %v", compared, sortedCounts(refused))
		})
	}
}

// walkBranches are the branches of the parts the random walk must reach, each
// named by branchesOf: a walk that stopped reaching one would compare nothing
// there.
var walkBranches = []string{
	"lease held", "lease took", "lease idle", "pop popped", "pop parked key left", "ingest added", "ingest parked key left",
	"ingest held queue", "beat seen", "beat stranger", "clock start", "clock stop", "sprint counter", "sprint marked",
	"sprint unmarked", "sprint parked", "sprint unparked", "sprint quarantined", "sprint coordinator", "sprint coordinator again",
	"sprint tickend armed", "sprint tickend held", "sprint tickend disarmed", "sprint tickend quiet", "sprint skipped",
}

// branchesOf names the branches an applied part's reply shows it took.
func branchesOf(name string, req *Request, g luaResult) []string {
	m, ok := g.plan.(map[string]any)
	if !ok {
		return nil
	}
	n := func(k string) float64 { v, _ := m[k].(float64); return v }
	list := func(k string) bool { v, _ := m[k].([]any); return len(v) != 0 }
	var out []string
	add := func(cond bool, b string) {
		if cond {
			out = append(out, b)
		}
	}
	switch name {
	case PartLease:
		add(m["held"] == true, "lease held")
		add(m["took"] == true, "lease took")
		add(m["held"] == false, "lease idle")
	case PartPop:
		add(n("popped") > 0, "pop popped")
		add(n("parked") > 0, "pop parked key left")
	case PartIngest:
		add(n("added") > 0, "ingest added")
		add(n("parked") > 0, "ingest parked key left")
		for _, c := range g.cmds {
			add(c[0] == "ZADD" && strings.Contains(c[1], "heldq"), "ingest held queue")
		}
	case PartBeat:
		add(list("seen"), "beat seen")
		add(list("strangers"), "beat stranger")
	case PartClock:
		add(m["verb"] == ClockStart, "clock start")
		add(m["verb"] == ClockStop, "clock stop")
	case PartSprint:
		add(m["counter"] == true, "sprint counter")
		add(list("marked"), "sprint marked")
		add(list("unmarked"), "sprint unmarked")
		add(list("parked"), "sprint parked")
		add(list("unparked"), "sprint unparked")
		add(list("quarantined"), "sprint quarantined")
		add(m["coordinator"] != nil, "sprint coordinator")
		add(m["coordinator"] == nil && m["skipped"] == false && req.Sprint.Coordinator != "", "sprint coordinator again")
		add(m["tickend"] == "armed", "sprint tickend armed")
		add(m["tickend"] == "held", "sprint tickend held")
		add(m["tickend"] == "disarmed", "sprint tickend disarmed")
		add(m["tickend"] == "quiet", "sprint tickend quiet")
		add(m["skipped"] == true, "sprint skipped")
	}
	return out
}

func sortedCounts(m map[string]int) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+strconv.Itoa(v))
	}
	sort.Strings(out)
	return out
}

func dumpKeys(m map[string]KeyValue) string {
	var parts []string
	for _, k := range sortedMapKeys(m) {
		v := m[k]
		parts = append(parts, fmt.Sprintf("%s=%s:%v%v%v%q", strings.TrimPrefix(k, testPrefix+"sprint:"), v.Kind, v.Hash, v.ZSet, v.List, v.String))
	}
	return strings.Join(parts, "\n ")
}
