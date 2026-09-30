package fn

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// The sprint's Lua skeleton runs here, under gopher-lua, against stubs of
// Layer 1's S, Layer 2's L and the phases and parts the later items register.
// No store is involved: the stubs record the order they are called in, so the
// test reads the order ns_sprint_step really runs its phases in, not the list
// the file declares. What the stubs stand in for is not tested here; that
// waits for G0.

// luaValue converts what encoding/json decodes into a Lua value.
func luaValue(L *lua.LState, v any) lua.LValue {
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
			t.Append(luaValue(L, e))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, e := range x {
			t.RawSetString(k, luaValue(L, e))
		}
		return t
	}
	panic(fmt.Sprintf("no Lua value for %T", v))
}

// goValue converts a Lua value into what encoding/json encodes: a table with a
// sequence is an array, any other table an object.
func goValue(v lua.LValue) any {
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
		if n := x.Len(); n > 0 {
			out := make([]any, 0, n)
			for i := 1; i <= n; i++ {
				out = append(out, goValue(x.RawGetInt(i)))
			}
			return out
		}
		out := map[string]any{}
		x.ForEach(func(k, e lua.LValue) { out[k.String()] = goValue(e) })
		return out
	}
	panic(fmt.Sprintf("no Go value for %T", v))
}

// luaStubs is the environment the sprint files load into: the prelude's NS
// with the tset profile set, a redis.register_function that keeps the
// functions it is given, Layer 1's S and Layer 2's L as recorders (each call
// leaves its name in the trace), and T, where a test decides what the stubs
// answer.
const luaStubs = `
NS = {tset_profile = 'sprint'}
redis = {registered = {}}
function redis.register_function(a, b)
  if type(a) == 'table' then redis.registered[a.function_name] = a.callback
  else redis.registered[a] = b end
end
T = {}
local S = {limits = {request_bytes = 4194304}, json = {decode = json_decode, encode = json_encode}}
NS.tset = S
NS.tlog = {}
function S.refuse(code, detail) return {status = 'refused', code = code, detail = detail} end
function S.open(version, raw)
  trace('open')
  if T.open_err then return nil, T.open_err end
  if T.ctx then return T.ctx(), nil end
  return {request = {entries = {}}, notes = {}}, nil
end
function S.before(ctx, t, ids, fields) trace('before'); return {}, nil end
function S.plan(ctx) trace('plan'); return {commands = {}}, nil end
function NS.tlog.plan(ctx, tp) trace('log'); return {seq = 1}, nil end
function S.prepare(ctx, tp, lp, others) trace('prepare'); T.prepared = others; return 'commit-list', nil end
function S.commit(commit) trace('commit'); return '{"status":"ok"}' end
function S.fence_prepare(ctx) trace('fence_prepare'); return 'fence-commit', nil end
`

// luaPhases registers every phase through the real registry, each recording
// its call; a phase named in skip is left out.
const luaPhases = `
local SP = NS.SP
local function phase(name, fn) if not (T.skip or {})[name] then SP.phase(name, fn) end end
phase('before', function(ctx, sp) trace('phase:before'); return {{table = 'work', ids = {'p1'}, fields = {'kind'}}} end)
phase('x_pre', function(ctx, sp, obs) trace('x_pre'); return true, nil end)
phase('derive', function(ctx, intents, obs) trace('derive'); return {}, {}, nil end)
phase('j_decide', function(ctx, notes, obs) trace('j_decide'); return {}, {notes = 1}, nil end)
phase('x_cmds', function(ctx, tp, lp) trace('x_cmds'); return {commands = {{'RPUSH', 'order', 'x'}}} end)
phase('j_cmds', function(ctx, jp, lp) trace('j_cmds'); return {commands = {{'RPUSH', 'order', 'j'}}} end)
`

// luaPart registers a part through the real registry, in the order given,
// each recording its calls and writing its name to the list key order.
const luaPart = `
local function part(name)
  NS.SP.part(name, {
    pre = function(ctx, sp, obs) trace('part:' .. name .. ':pre'); return {part = name}, nil end,
    cmds = function(ctx, plan, lp) trace('part:' .. name .. ':cmds'); return {{'RPUSH', 'order', name}}, nil end})
end
for _, name in ipairs(T.parts or {}) do part(name) end
`

type luaSprint struct {
	t     *testing.T
	L     *lua.LState
	trace []string
}

// newLuaSprint loads the stubs and the sprint's two files, in load order.
func newLuaSprint(t *testing.T) *luaSprint {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	h := &luaSprint{t: t, L: L}
	L.SetGlobal("trace", L.NewFunction(func(L *lua.LState) int {
		h.trace = append(h.trace, L.CheckString(1))
		return 0
	}))
	L.SetGlobal("json_decode", L.NewFunction(func(L *lua.LState) int {
		var v any
		if err := json.Unmarshal([]byte(L.CheckString(1)), &v); err != nil {
			L.RaiseError("decode: %v", err)
		}
		L.Push(luaValue(L, v))
		return 1
	}))
	L.SetGlobal("json_encode", L.NewFunction(func(L *lua.LState) int {
		b, err := json.Marshal(goValue(L.CheckAny(1)))
		if err != nil {
			L.RaiseError("encode: %v", err)
		}
		L.Push(lua.LString(b))
		return 1
	}))
	h.do(luaStubs)
	frags, err := SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frags {
		h.do(f.Source)
	}
	return h
}

func (h *luaSprint) do(src string) {
	h.t.Helper()
	if err := h.L.DoString(src); err != nil {
		h.t.Fatalf("lua: %v", err)
	}
}

// setup registers the phases (less those in skip) and the parts, in the order
// given, as the later items' files will at load.
func (h *luaSprint) setup(skip []string, parts ...string) {
	h.t.Helper()
	q := func(names []string) string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = "'" + n + "'"
		}
		return "{" + strings.Join(out, ",") + "}"
	}
	h.do("T.skip = {}; for _, n in ipairs(" + q(skip) + ") do T.skip[n] = true end")
	h.do("T.parts = " + q(parts))
	h.do(luaPhases)
	h.do(luaPart)
}

// call runs a registered function with the keys and args given and returns
// its reply; the trace holds only this call's stub calls.
func (h *luaSprint) call(fn string, keys []string, args ...string) string {
	h.t.Helper()
	h.trace = nil
	reg := h.L.GetGlobal("redis").(*lua.LTable).RawGetString("registered").(*lua.LTable)
	f := reg.RawGetString(fn)
	if f == lua.LNil {
		h.t.Fatalf("the Lua registered no %s", fn)
	}
	k, a := h.L.NewTable(), h.L.NewTable()
	for _, s := range keys {
		k.Append(lua.LString(s))
	}
	for _, s := range args {
		a.Append(lua.LString(s))
	}
	if err := h.L.CallByParam(lua.P{Fn: f, NRet: 1, Protect: true}, k, a); err != nil {
		h.t.Fatalf("%s: %v", fn, err)
	}
	ret := h.L.Get(-1)
	h.L.Pop(1)
	return ret.String()
}

// step runs ns_sprint_step on a sprint half, with a stand-in step half.
func (h *luaSprint) step(sprint string) string {
	h.t.Helper()
	return h.call("ns_sprint_step", nil, "tset/1", `{"step":true}`, sprint)
}

// refusalCode is the code of a refusal reply, "" for any other reply.
func refusalCode(reply string) string {
	var r struct {
		Status string `json:"status"`
		Code   string `json:"code"`
	}
	if json.Unmarshal([]byte(reply), &r) != nil || r.Status != "refused" {
		return ""
	}
	return r.Code
}

func (h *luaSprint) prepared() string {
	h.t.Helper()
	h.do("PREPARED = json_encode(T.prepared)")
	return h.L.GetGlobal("PREPARED").String()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestSprintLuaRunsPhasesInOrder: ns_sprint_step calls what the phases are
// registered as, in the order sprintfn.PhaseOrder names: the trace of the
// stubs it really calls (not the SP.phase_order list it declares) has open,
// before, X.pre, derive, J, parts, plan, log, X.plan (X's commands, J's, then
// the parts'), prepare and commit, each once and in that order; prepare gets
// X's plan, then J's, then the parts', in A1 order; and the reply is the
// commit's, with what each part decided.
func TestSprintLuaRunsPhasesInOrder(t *testing.T) {
	t.Parallel()
	h := newLuaSprint(t)
	h.setup(nil, "lease")
	reply := h.step(`{"meta":{"verb":"tick"},"intents":[{"kind":"needmet","card":"p2"}],"notes":[{"op":"open"}],"lease":{"owner":"tok"}}`)

	want := []string{"open", "phase:before", "before", "x_pre", "derive", "j_decide", "part:lease:pre", "plan", "log",
		"x_cmds", "j_cmds", "part:lease:cmds", "prepare", "commit"}
	if !reflect.DeepEqual(h.trace, want) {
		t.Fatalf("ns_sprint_step called\n got %v\nwant %v", h.trace, want)
	}
	phaseOf := map[string]string{
		"open": sprintfn.PhaseOpen, "phase:before": sprintfn.PhaseBefore, "before": sprintfn.PhaseBefore,
		"x_pre": sprintfn.PhaseXPre, "derive": sprintfn.PhaseDerive, "j_decide": sprintfn.PhaseJ,
		"part:lease:pre": sprintfn.PhaseParts, "plan": sprintfn.PhasePlan, "log": sprintfn.PhaseLog,
		"x_cmds": sprintfn.PhaseXPlan, "j_cmds": sprintfn.PhaseXPlan, "part:lease:cmds": sprintfn.PhaseXPlan,
		"prepare": sprintfn.PhasePrepare, "commit": sprintfn.PhaseCommit,
	}
	var phases []string
	for _, ev := range h.trace {
		p, ok := phaseOf[ev]
		if !ok {
			t.Fatalf("the trace has %q, which no phase owns", ev)
		}
		if len(phases) == 0 || phases[len(phases)-1] != p {
			phases = append(phases, p)
		}
	}
	if !reflect.DeepEqual(phases, sprintfn.PhaseOrder) {
		t.Fatalf("the Lua ran the phases %v, sprintfn.PhaseOrder is %v", phases, sprintfn.PhaseOrder)
	}
	if got := h.prepared(); got != `[{"commands":[["RPUSH","order","x"]]},{"commands":[["RPUSH","order","j"]]},{"commands":[["RPUSH","order","lease"]]}]` {
		t.Fatalf("prepare was given %s; want X's plan, J's, then the parts'", got)
	}
	if reply != `{"reply":{"status":"ok"},"parts":{"lease":{"part":"lease"}}}` {
		t.Fatalf("reply %s", reply)
	}
}

// TestSprintLuaPartsRunInFixedOrder: parts registered in the reverse of 1.0's
// order run in 1.0's order (lease, pop, ingest, beat, clock, sprint): their
// pre calls in that order, then their commands, and the commands handed to
// prepare list them in that order.
func TestSprintLuaPartsRunInFixedOrder(t *testing.T) {
	t.Parallel()
	h := newLuaSprint(t)
	reversed := make([]string, len(sprintfn.PartOrder))
	for i, name := range sprintfn.PartOrder {
		reversed[len(reversed)-1-i] = name
	}
	h.setup(nil, reversed...)
	h.step(`{"meta":{},"sprint":{},"clock":{},"beat":{},"ingest":{},"pop":{},"lease":{}}`)

	var pre, cmds []string
	for _, ev := range h.trace {
		switch {
		case strings.HasSuffix(ev, ":pre") && strings.HasPrefix(ev, "part:"):
			pre = append(pre, strings.Split(ev, ":")[1])
		case strings.HasSuffix(ev, ":cmds") && strings.HasPrefix(ev, "part:"):
			cmds = append(cmds, strings.Split(ev, ":")[1])
		}
	}
	if !reflect.DeepEqual(pre, sprintfn.PartOrder) || !reflect.DeepEqual(cmds, sprintfn.PartOrder) {
		t.Fatalf("parts' pre ran %v and commands %v; want %v for both", pre, cmds, sprintfn.PartOrder)
	}
	want := `[{"commands":[["RPUSH","order","x"]]},{"commands":[["RPUSH","order","lease"],["RPUSH","order","pop"],` +
		`["RPUSH","order","ingest"],["RPUSH","order","beat"],["RPUSH","order","clock"],["RPUSH","order","sprint"]]}]`
	if got := h.prepared(); got != want {
		t.Fatalf("prepare was given %s\nwant %s", got, want)
	}
}

// TestSprintLuaFence (errata E5): a fence returns at open. One that carries
// any sprint effect is REQUEST, whichever effect it is, and runs nothing
// after S.open; one that carries none is committed through the fence path and
// runs no sprint phase; a replay is returned as it stands and runs no phase.
func TestSprintLuaFence(t *testing.T) {
	t.Parallel()
	effects := map[string]string{
		"intents":    `[{"kind":"needmet","card":"p2"}]`,
		"guards":     `[{"kind":"due","key":"k","score":"1"}]`,
		"notes":      `[{"op":"open"}]`,
		"quarantine": `[{"id":"p1","code":"PLACE","cells":[]}]`,
		"done":       `["k"]`,
		"requeue":    `["k"]`,
		"lease":      `{"owner":"tok"}`,
		"pop":        `{"limit":1}`,
		"ingest":     `{"from":"0","to":"0","keys":[]}`,
		"beat":       `{"members":[]}`,
		"clock":      `{"verb":"init"}`,
		"sprint":     `{}`,
	}
	for name, value := range effects {
		t.Run("a fence with "+name+" is REQUEST", func(t *testing.T) {
			t.Parallel()
			h := newLuaSprint(t)
			h.setup(nil, sprintfn.PartOrder...)
			h.do("T.ctx = function() return {request = {entries = {}}, notes = {}, original_fence = true} end")
			reply := h.step(`{"meta":{},"` + name + `":` + value + `}`)
			if refusalCode(reply) != "REQUEST" || !reflect.DeepEqual(h.trace, []string{"open"}) {
				t.Fatalf("a fence with %s: reply %s, ran %v; want REQUEST after open and nothing more", name, reply, h.trace)
			}
		})
	}
	// The effects of the Lua are the fields of the Go request that
	// hasSprintField reads: twelve of them.
	if len(effects) != 12 {
		t.Fatalf("%d effects tested; 1.0 has twelve sprint fields", len(effects))
	}

	t.Run("a fence with no effect", func(t *testing.T) {
		t.Parallel()
		h := newLuaSprint(t)
		h.setup(nil, sprintfn.PartOrder...)
		h.do("T.ctx = function() return {request = {entries = {}}, notes = {}, original_fence = true} end")
		reply := h.step(`{"meta":{}}`)
		if reply != `{"reply":{"status":"ok"},"parts":{}}` || !reflect.DeepEqual(h.trace, []string{"open", "fence_prepare", "commit"}) {
			t.Fatalf("a fence: reply %s, ran %v; want open, fence_prepare, commit", reply, h.trace)
		}
	})

	t.Run("a replay runs no phase", func(t *testing.T) {
		t.Parallel()
		h := newLuaSprint(t)
		h.setup(nil, sprintfn.PartOrder...)
		h.do("T.ctx = function() return {request = {entries = {}}, notes = {}, replay = {status = 'ok', replay = true}} end")
		reply := h.step(`{"meta":{},"lease":{"owner":"tok"}}`)
		if reply != `{"reply":{"replay":true,"status":"ok"},"parts":{}}` || !reflect.DeepEqual(h.trace, []string{"open"}) {
			t.Fatalf("a replay: reply %s, ran %v", reply, h.trace)
		}
	})
}

// TestSprintLuaRefusesBeforeAnyWrite: what ns_sprint_step refuses before it
// commits. A wrong argument count, a sprint half that is not an object with a
// meta, and both halves together over 4 MiB are refused in the static phase,
// before S.open reads TIME or anything; a phase, a part or a command phase
// that no file registered is CONFIG, and neither prepare nor commit runs.
func TestSprintLuaRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	t.Run("the arguments", func(t *testing.T) {
		t.Parallel()
		h := newLuaSprint(t)
		h.setup(nil)
		for name, c := range map[string]struct {
			keys []string
			args []string
		}{
			"a key":        {[]string{"k"}, []string{"tset/1", "{}", `{"meta":{}}`}},
			"two args":     {nil, []string{"tset/1", "{}"}},
			"four args":    {nil, []string{"tset/1", "{}", `{"meta":{}}`, "x"}},
			"no arguments": {nil, nil},
		} {
			reply := h.call("ns_sprint_step", c.keys, c.args...)
			if refusalCode(reply) != "ARGS" || len(h.trace) != 0 {
				t.Errorf("%s: reply %s, ran %v; want ARGS and nothing run", name, reply, h.trace)
			}
		}
	})

	t.Run("a sprint half that is not a request", func(t *testing.T) {
		t.Parallel()
		h := newLuaSprint(t)
		h.setup(nil)
		for _, sprint := range []string{"not json", `[]`, `3`, `{}`, `{"meta":3}`, `{"meta":"x"}`} {
			reply := h.step(sprint)
			if refusalCode(reply) != "REQUEST" || len(h.trace) != 0 {
				t.Errorf("sprint half %q: reply %s, ran %v; want REQUEST before open", sprint, reply, h.trace)
			}
		}
	})

	t.Run("both halves together over the request bound", func(t *testing.T) {
		t.Parallel()
		h := newLuaSprint(t)
		h.setup(nil)
		const bound = 4194304
		half := func(n int) string { // a sprint half of exactly n bytes
			base := `{"meta":{"verb":""}}`
			return `{"meta":{"verb":"` + strings.Repeat("y", n-len(base)) + `"}}`
		}
		// Each half is under the bound alone: only their sum is over it.
		sprint := half(1<<20 + 512*1024)
		stepHalf := strings.Repeat("x", bound-len(sprint)+1)
		reply := h.call("ns_sprint_step", nil, "tset/1", stepHalf, sprint)
		if refusalCode(reply) != "LIMIT" || len(h.trace) != 0 || !strings.Contains(reply, "request_bytes") {
			t.Fatalf("halves of %d and %d bytes: reply %.200s, ran %v; want LIMIT (request_bytes) before open", len(stepHalf), len(sprint), reply, h.trace)
		}
		// Exactly the bound is allowed.
		reply = h.call("ns_sprint_step", nil, "tset/1", stepHalf[:len(stepHalf)-1], sprint)
		if h.trace == nil || h.trace[0] != "open" || refusalCode(reply) == "LIMIT" {
			t.Fatalf("halves that total the bound: reply %.200s, ran %v; want the step to open", reply, h.trace)
		}
	})

	t.Run("something no file registered", func(t *testing.T) {
		t.Parallel()
		type c struct {
			name    string
			skip    []string
			parts   []string
			sprint  string
			ranPlan bool
		}
		all := `{"meta":{},"intents":[{"kind":"needmet","card":"p2"}],"notes":[{"op":"open"}],"lease":{"owner":"t"}}`
		for _, tc := range []c{
			{"X.pre", []string{"x_pre"}, []string{"lease"}, all, false},
			{"derive", []string{"derive"}, []string{"lease"}, all, false},
			{"J", []string{"j_decide"}, []string{"lease"}, all, false},
			{"a part", nil, nil, all, false},
			{"X.plan's commands", []string{"x_cmds"}, []string{"lease"}, all, true},
			{"J's commands", []string{"j_cmds"}, []string{"lease"}, all, true},
		} {
			h := newLuaSprint(t)
			h.setup(tc.skip, tc.parts...)
			reply := h.step(tc.sprint)
			if refusalCode(reply) != "CONFIG" {
				t.Errorf("no %s: reply %s; want CONFIG", tc.name, reply)
			}
			if contains(h.trace, "prepare") || contains(h.trace, "commit") || contains(h.trace, "plan") != tc.ranPlan {
				t.Errorf("no %s: ran %v; want CONFIG before prepare and commit (plan reached: %v)", tc.name, h.trace, tc.ranPlan)
			}
		}
	})

	t.Run("a phase that refuses stops the step", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name, refuser, last string
		}{
			{"X.pre", "phase('x_pre', function() trace('x_pre'); return nil, NS.tset.refuse('XGUARD') end)", "x_pre"},
			{"derive", "phase('derive', function() trace('derive'); return nil, nil, NS.tset.refuse('COUNTER') end)", "derive"},
			{"J", "phase('j_decide', function() trace('j_decide'); return nil, nil, NS.tset.refuse('DROPPING') end)", "j_decide"},
			{"a part's pre", "NS.SP.parts.lease.pre = function() trace('part:lease:pre'); return nil, NS.tset.refuse('STALEGEN') end", "part:lease:pre"},
			{"a part's commands", "NS.SP.parts.lease.cmds = function() trace('part:lease:cmds'); return nil, NS.tset.refuse('STALEGEN') end", "part:lease:cmds"},
		} {
			h := newLuaSprint(t)
			h.setup([]string{map[string]string{"X.pre": "x_pre", "derive": "derive", "J": "j_decide"}[tc.name]}, "lease")
			h.do("local function phase(name, fn) NS.SP.phases[name] = fn end; " + tc.refuser)
			reply := h.step(`{"meta":{},"intents":[{"kind":"needmet","card":"p2"}],"notes":[{"op":"open"}],"lease":{"owner":"t"}}`)
			if refusalCode(reply) == "" || h.trace[len(h.trace)-1] != tc.last || contains(h.trace, "prepare") || contains(h.trace, "commit") {
				t.Errorf("%s refusing: reply %s, ran %v; want the refusal, the step stopped at %s, no prepare or commit", tc.name, reply, h.trace, tc.last)
			}
		}
	})
}

// TestSprintLuaRegistriesRefuseAtLoad: a registration is checked when the
// library loads, so a library with a doubled or a stray part, phase or query
// never serves a call. A second writer of a name, a name 1.0 does not have
// (heartbeat and tickend are fields, not parts), a lower layer's query kind and
// a malformed spec are each an error at registration, and the registry keeps
// what it held before: the first registration of a name, or nothing.
func TestSprintLuaRegistriesRefuseAtLoad(t *testing.T) {
	t.Parallel()
	h := newLuaSprint(t)
	h.do(`
OK = {pre = function() end, cmds = function() end}
OTHER = {pre = function() end, cmds = function() end}
QOK = {validate = function() end, read = function() end}
QOTHER = {validate = function() end, read = function() end}
function TRY(f, ...) local ok, err = pcall(f, ...); if ok then return '' end; return tostring(err) end
`)
	// eval evaluates an expression and returns its text.
	eval := func(src string) string {
		h.do("R = tostring(" + src + ")")
		return h.L.GetGlobal("R").String()
	}
	refused := func(what, call string) {
		t.Helper()
		if got := eval(call); !strings.Contains(got, "sprint ") {
			t.Errorf("%s: registration returned %q, want an error", what, got)
		}
	}

	for _, name := range sprintfn.PartOrder {
		if got := eval("TRY(NS.SP.part, '" + name + "', OK)"); got != "" {
			t.Fatalf("part %s: %s", name, got)
		}
		// The second writer is refused and the first stays.
		refused("a second "+name+" part", "TRY(NS.SP.part, '"+name+"', OTHER)")
		if eval("NS.SP.parts."+name+" == OK") != "true" {
			t.Errorf("a second %s part replaced the first", name)
		}
	}
	for _, name := range []string{"heartbeat", "tickend", "nosuch", ""} {
		refused("part "+name, "TRY(NS.SP.part, '"+name+"', OK)")
		if eval("NS.SP.parts['"+name+"']") != "nil" {
			t.Errorf("part %q, which 1.0 does not have, was registered", name)
		}
	}
	h.do("NS.SP.parts.lease = nil")
	for _, spec := range []string{"{pre = 1, cmds = function() end}", "{pre = function() end}", "{cmds = function() end}", "7", "nil"} {
		refused("part spec "+spec, "TRY(NS.SP.part, 'lease', "+spec+")")
		if eval("NS.SP.parts.lease") != "nil" {
			t.Errorf("a malformed part spec %s was registered", spec)
		}
	}

	for _, name := range []string{"before", "x_pre", "x_cmds", "derive", "j_decide", "j_cmds"} {
		refused("phase "+name+" that is not a function", "TRY(NS.SP.phase, '"+name+"', 'not a function')")
		if eval("NS.SP.phases."+name) != "nil" {
			t.Errorf("a phase %s that is not a function was registered", name)
		}
		h.do("FIRST_" + name + " = function() end")
		if got := eval("TRY(NS.SP.phase, '" + name + "', FIRST_" + name + ")"); got != "" {
			t.Fatalf("phase %s: %s", name, got)
		}
		refused("a second "+name+" phase", "TRY(NS.SP.phase, '"+name+"', function() end)")
		if eval("NS.SP.phases."+name+" == FIRST_"+name) != "true" {
			t.Errorf("a second %s phase replaced the first", name)
		}
	}
	for _, name := range []string{"nosuch", "plan", "commit", "X.pre", "log"} {
		refused("phase "+name, "TRY(NS.SP.phase, '"+name+"', function() end)")
		if eval("NS.SP.phases['"+name+"']") != "nil" {
			t.Errorf("phase %q, which has no hook, was registered", name)
		}
	}

	if got := eval("TRY(NS.SP.query, 'related', QOK)"); got != "" {
		t.Fatalf("query related: %s", got)
	}
	refused("a second related query", "TRY(NS.SP.query, 'related', QOTHER)")
	if eval("NS.SP.queries.related == QOK") != "true" {
		t.Error("a second related query replaced the first")
	}
	for _, kind := range []string{"range", "count", "rcount", "ids", "rows", "done", "last", "lines", "cardlines"} {
		refused("query "+kind+", a lower layer's kind", "TRY(NS.SP.query, '"+kind+"', QOK)")
		if eval("NS.SP.queries['"+kind+"']") != "nil" {
			t.Errorf("query %q, a lower layer's kind, was registered", kind)
		}
	}
	for _, spec := range []string{"{validate = function() end}", "{read = function() end}", "7"} {
		refused("query spec "+spec, "TRY(NS.SP.query, 'front', "+spec+")")
		if eval("NS.SP.queries.front") != "nil" {
			t.Errorf("a malformed query spec %s was registered", spec)
		}
	}
}
