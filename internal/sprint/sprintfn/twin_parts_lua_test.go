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
// Layer 1's helpers (S.rd, S.command, S.before, S.refuse and the decimal
// helpers) over a fake keyspace taken from the twin, and is compared with the
// Go parts on the same state and the same requests: the same reply, the same
// commands in the same order, the same refusal. No store is involved, and what
// the stubs stand in for is Layer 1's to test at G0; this is the twin-equals-Lua
// check of the parts that can run tonight, and it is what found the faults in
// the Lua that parsing alone could not.

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

// luaStubEnv is Layer 1's S as the parts call it.
const luaStubEnv = `
NS = {tset_profile = 'sprint'}
local S = {}
NS.tset = S
function S.refuse(code, detail, message)
  return {status = 'refused', code = code, detail = detail or {}, message = (message or code) .. '; nothing was changed'}
end
function S.array() return {__arr = true} end
function S.utf8_valid(s) return type(s) == 'string' and go_utf8_valid(s) end
function S.charge(ctx, unit, n) return true, nil end
function S.rd(ctx, argv, kind, reserve, probe) return fake_rd(argv), nil end
function S.before(ctx, t, ids, fields) return fake_before(t, ids), nil end
function S.command(ctx, command, key, kind, args)
  local argv = {command, key}
  for _, v in ipairs(args or {}) do
    assert(type(v) == 'string', 'a non-scalar argv')
    argv[#argv + 1] = v
  end
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
		if v.Kind == "" {
			return lua.LString("none")
		}
		return lua.LString(v.Kind)
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
func (h *luaParts) run(name string, req *Request, tick *TickEnd, tw *Twin, clk *stepClock, obs *Before, ctx *lua.LTable) luaResult {
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
	if tick != nil && req.Sprint != nil {
		sp, _ := decoded["sprint"].(map[string]any)
		sp["tickend"] = map[string]any{"backlog": string(tick.Backlog)}
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

// newCtx is the ctx of one step as the parts read it.
func (h *luaParts) newCtx(tw *Twin, clk *stepClock, req *Request) *lua.LTable {
	ctx := h.L.NewTable()
	ctx.RawSetString("space", lua.LString(testPrefix))
	ctx.RawSetString("now_ms", lua.LString(strconv.FormatInt(clk.ms(), 10)))
	ctx.RawSetString("request_epoch", lua.LString(string(req.Epoch)))
	ctx.RawSetString("write_epoch", lua.LString(string(partEpoch(req))))
	return ctx
}

// goResult is the same for the Go part.
func goResult(tw *Twin, clk *stepClock, name string, req *Request, obs *Before) luaResult {
	st := tw.partState(clk, req.Epoch)
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
func randomPartsRequest(rng *rand.Rand, tw *Twin, clk *stepClock, tick **TickEnd, epoch tset.Decimal) *Request {
	keys := tw.SprintKeys()
	gen, _ := strconv.ParseUint(keys[sk("lease")].Hash["gen"], 10, 64)
	cur := keys[ekAt("tick", epoch)].Hash["cur"]
	curN, _ := strconv.ParseUint(cur, 10, 64)
	req := &Request{Epoch: epoch, Meta: Meta{Verb: "random"}}
	pick := func(p float64) bool { return rng.Float64() < p }
	choose := func(xs ...string) string { return xs[rng.Intn(len(xs))] }
	*tick = nil

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
		req.Lease = &LeasePart{Owner: choose("tok-a", "tok-b"), Name: choose("", "loop-a", "loop-b"),
			HoldMS: int64([]int{500, 4000, 60000}[rng.Intn(3)]), Heartbeat: hb, Stopped: pick(0.15)}
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
			sp.Dropping = map[string]string{choose("s1", "s2"): choose("op-1", "op-2", "")}
		}
		if pick(0.25) {
			sp.Park = map[string]string{choose("deal", "ask:p1", "resolve:s1"): choose("n1", "n2", "")}
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
		if pick(0.25) {
			*tick = &TickEnd{Backlog: tset.Decimal(choose("0", "1", "7", "123456"))}
		}
		if pick(0.25) {
			for i := rng.Intn(3) + 1; i > 0; i-- {
				req.Body.Quarantine = append(req.Body.Quarantine, Quarantined{ID: choose("p1", "p2", "p3", "p4"),
					Code: choose("DRIFT", "MISSING"), Stream: choose("", "s1"), Rule: choose("", "deal"),
					Cells: [][]string{nil, {"s1:ready"}, {"s1:ready", "s1:waiting"}}[rng.Intn(3)]})
			}
		}
		req.Sprint = sp
	}
	if req.Sprint == nil && len(req.Body.Quarantine) == 0 && req.Lease == nil && req.Pop == nil && req.Ingest == nil && req.Beat == nil && req.Clock == nil {
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
			var tick *TickEnd
			tw, _, _, clk := partsTwinAt(t, epoch)
			// The sprint part's tick end comes from the test, as IT12's SprintPart
			// will carry it.
			tw.parts = NewPartRegistry()
			for _, name := range PartOrder {
				p, _ := defaultParts.Lookup(name)
				if name == PartSprint {
					p = sprintPart{tickEnd: func(*Request) *TickEnd { return tick }}
				}
				if err := tw.parts.Register(name, p); err != nil {
					t.Fatal(err)
				}
			}
			fleetSeedAt(t, tw, epoch)
			h := newLuaParts(t)
			compared := map[string]int{}
			refused := map[string]int{}
			for step := 0; step < 600; step++ {
				if rng.Float64() < 0.3 {
					now := clk.ms()
					kinds := []string{"untaken:p1.w1", "unbegun:p2.r1.rd", "beat:m1", "seen:m2", "overdue:n5", "idle:s1", "behind", "remind:ann", "hold:n2"}
					seed(tw, Command("ZADD", ekAt("due", epoch), kindZSet, strconv.FormatInt(now-int64(rng.Intn(20000)), 10), kinds[rng.Intn(len(kinds))],
						strconv.FormatInt(now+int64(rng.Intn(20000)), 10), kinds[rng.Intn(len(kinds))]))
					seed(tw, Command("ZADD", ekAt("cut", epoch), kindZSet, strconv.FormatInt(now-int64(rng.Intn(5000)), 10), "cut:op-"+strconv.Itoa(rng.Intn(5))))
					seed(tw, Command("ZADD", ekAt("agenda", epoch), kindZSet, strconv.Itoa(rng.Intn(50)), []string{"deal", "ask:p1", "resolve:s1", "down:m1"}[rng.Intn(4)]))
				}
				if rng.Float64() < 0.1 && tw.SprintKeys()[sk("clock")].Kind == kindHash { // R17's bookkeeping of a span
					seed(tw, Command("HSET", sk("clock"), kindHash, "due_since_ms", strconv.FormatInt(clk.ms()-int64(rng.Intn(9000)), 10),
						"stophold_ms", strconv.FormatInt(clk.ms()+int64(rng.Intn(9000)), 10), "stopraised_ms", strconv.Itoa(rng.Intn(9))))
				}
				req := randomPartsRequest(rng, tw, clk, &tick, epoch)
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
					g := goResult(tw, clk, name, req, obs)
					l := h.run(name, req, tick, tw, clk, obs, ctx)
					if diff := sameResult(g, l); diff != "" {
						b, _ := json.Marshal(sprintWireOf(req))
						t.Fatalf("seed %d step %d part %s: %s\nrequest %s\nstate %s", seedN, step, name, diff, b, dumpKeys(tw.SprintKeys()))
					}
					compared[name]++
					if g.refusal != nil {
						refused[name+" "+g.refusal.Code]++
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
			t.Logf("compared %v; refusals %v", compared, sortedCounts(refused))
		})
	}
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
