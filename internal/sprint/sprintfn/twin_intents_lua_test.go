package sprintfn

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The Go phase and the Lua file are two writings of one phase, and this file
// holds them equal: each scenario gives both the same records, the same
// sprint keys, the same request and the same intents, and what comes back (the
// entries, the requests to J, the commands and the refusal) must be the same.
// The Lua runs under gopher-lua against stubs of Layer 1's S that answer from
// the scenario's fixtures: no store is involved, and the stubs are not what is
// tested. What waits for gate G0 is the Lua against a store.

// luaFile reads one of the sprint's Lua files.
func luaFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "nsprint", "fn", "lua", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// luaFrom turns what encoding/json decodes into a Lua value.
func luaFrom(L *lua.LState, v any) lua.LValue {
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
			t.Append(luaFrom(L, e))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, e := range x {
			t.RawSetString(k, luaFrom(L, e))
		}
		return t
	}
	panic(fmt.Sprintf("no Lua value for %T", v))
}

// anyFrom turns a Lua value into what encoding/json decodes: a table with a
// sequence is an array, a table without is an object, and userdata (the null
// of Layer 1's records) is nil.
func anyFrom(v lua.LValue) any {
	switch x := v.(type) {
	case *lua.LNilType, *lua.LUserData:
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
				out = append(out, anyFrom(x.RawGetInt(i)))
			}
			return out
		}
		out := map[string]any{}
		x.ForEach(func(k, e lua.LValue) { out[k.String()] = anyFrom(e) })
		return out
	}
	panic(fmt.Sprintf("no Go value for %T", v))
}

// roundTrip is a Go value as encoding/json decodes its encoding.
func roundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// diffScenario is what both halves of the phase are given.
type diffScenario struct {
	name    string
	recs    map[string]fixRec
	keys    []Cmd
	entries []tset.Entry
	intents []Intent
	now     string // the call's time, ms
}

// outcome is what a half decided, in the form the two are compared in.
type outcome struct {
	Entries any
	Notes   any
	Cmds    any
	Refusal any
	// Work is the counts of the walk and of the index (deriveWork), which the
	// two halves hold equal: the same algorithm does the same work.
	Work any
}

func refusalForm(code string, ids any, message string, budget any, limit, actual any) any {
	if m, ok := ids.(map[string]any); ok && len(m) == 0 {
		ids = []any{}
	}
	if ids == nil {
		ids = []any{}
	}
	return map[string]any{"code": code, "ids": ids, "message": message, "budget": budget, "limit": limit, "actual": actual}
}

func goOutcome(t *testing.T, sc diffScenario) outcome {
	t.Helper()
	ks := newKeyspace()
	ks.apply(sc.keys)
	st := fixState(ks, sc.now)
	plan, ref := deriveOver(st, &fixWorld{recs: sc.recs, entryList: sc.entries}, sc.intents)
	if ref != nil {
		var budget, limit, actual any
		if ref.Detail.Budget != "" {
			budget = ref.Detail.Budget
		}
		if ref.Detail.Limit != nil {
			limit = float64(*ref.Detail.Limit)
		}
		if ref.Detail.Actual != nil {
			actual = float64(*ref.Detail.Actual)
		}
		ids := make([]any, len(ref.Detail.IDs))
		for i, id := range ref.Detail.IDs {
			ids[i] = id
		}
		return outcome{Refusal: refusalForm(ref.Code, ids, ref.Message, budget, limit, actual)}
	}
	var out outcome
	var entries, notes, cmds []any
	for _, e := range plan.Entries {
		entries = append(entries, roundTrip(t, e))
	}
	for _, n := range plan.Notes {
		m := map[string]any{"op": n.Op, "type": n.Type, "cause": n.Cause, "subjects": roundTrip(t, n.Subjects)}
		if n.Text != "" {
			m["text"] = n.Text
		}
		notes = append(notes, m)
	}
	for _, c := range plan.Cmds {
		access := []any{}
		for _, a := range c.Access {
			access = append(access, map[string]any{"key": a.Key, "kind": a.Kind, "mode": a.Mode})
		}
		cmds = append(cmds, map[string]any{"argv": roundTrip(t, c.Argv), "access": access})
	}
	out.Entries, out.Notes, out.Cmds = entries, notes, cmds
	out.Work = map[string]any{"expanded": float64(plan.Work.Expanded), "edges": float64(plan.Work.EdgesSeen), "indexed": float64(plan.Work.Indexed)}
	return out
}

// luaStubs is Layer 1's S as the derive phase uses it, answering from fixtures
// the Go side hands over (FX_BEFORE, FX_READ); the refusals have the shape and
// the message of Layer 1's S.refuse.
const luaStubs = `
NS = {tset_profile = 'sprint'}
cjson = {null = NULL}
local S = {}
NS.tset = S
function S.array() return {} end
function S.refuse(code, detail, message)
  detail = detail or {}
  if not detail.ids or next(detail.ids) == nil then detail.ids = S.array() end
  return {status = 'refused', code = code, detail = detail, message = (message or code) .. '; nothing was changed'}
end
function S.before(ctx, t, ids, fields) return FX_BEFORE(t, ids, fields), nil end
function S.readcmd(ctx, descriptor, reserve, probe) return FX_READ(descriptor.argv), nil end
function S.writecmd(ctx, argv, access) return {argv = argv, access = access}, nil end
function S.command(ctx, command, key, kind, args)
  local argv = {command, key}
  for _, v in ipairs(args or {}) do argv[#argv + 1] = v end
  return S.writecmd(ctx, argv, {{key = key, kind = kind, mode = 'write'}})
end
`

// scoreText is a score as the store replies with it.
func scoreText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// luaRig is the Lua phase loaded once under gopher-lua, with Layer 1's stubs
// answering from the scenario it is run on; running scenarios one after
// another on it also shows the phase keeps nothing between calls.
type luaRig struct {
	t      *testing.T
	L      *lua.LState
	sc     diffScenario
	keys   *Keys
	derive lua.LValue
	sp     *lua.LTable
	// reads counts the store commands of the sprint's own keys the last run
	// sent, by command name, and before counts the ids it asked S.before for:
	// what a test of the cost of a step reads.
	reads  map[string]int
	before int
	// ctx is the call context of the last run.
	ctx *lua.LTable
}

func newLuaRig(t *testing.T) *luaRig {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	g := &luaRig{t: t, L: L, reads: map[string]int{}}
	null := L.NewUserData()
	L.SetGlobal("NULL", null)

	L.SetGlobal("FX_BEFORE", L.NewFunction(func(L *lua.LState) int {
		ids, fields := L.CheckTable(2), L.CheckTable(3)
		out := L.NewTable()
		ids.ForEach(func(_, v lua.LValue) {
			g.before++
			id := v.String()
			r, ok := g.sc.recs[id]
			rec := L.NewTable()
			rec.RawSetString("exists", lua.LBool(ok))
			fs := L.NewTable()
			rec.RawSetString("fields", fs)
			rec.RawSetString("place", null)
			if ok {
				rec.RawSetString("revision", lua.LString(r.rev))
				if r.col != "" {
					p := L.NewTable()
					p.RawSetString("row", lua.LString(r.row))
					p.RawSetString("col", lua.LString(r.col))
					rec.RawSetString("place", p)
				}
				fields.ForEach(func(_, f lua.LValue) {
					v, present := r.fields[f.String()]
					fv := L.NewTable()
					fv.RawSetString("present", lua.LBool(present))
					if present {
						fv.RawSetString("value", lua.LString(v))
					} else {
						fv.RawSetString("value", null)
					}
					fs.RawSetString(f.String(), fv)
				})
			}
			out.RawSetString(id, rec)
		})
		L.Push(out)
		return 1
	}))
	L.SetGlobal("FX_READ", L.NewFunction(func(L *lua.LState) int {
		argv := L.CheckTable(1)
		var a []string
		for i := 1; i <= argv.Len(); i++ {
			a = append(a, argv.RawGetInt(i).String())
		}
		g.reads[a[0]]++
		score := func(k, m string) lua.LValue {
			if s, ok := g.keys.ZScore(k, m); ok {
				return lua.LString(scoreText(s))
			}
			return lua.LFalse
		}
		switch a[0] {
		case "ZSCORE":
			L.Push(score(a[1], a[2]))
		case "ZMSCORE":
			t := L.NewTable()
			for _, m := range a[2:] {
				t.Append(score(a[1], m))
			}
			L.Push(t)
		case "ZCARD":
			L.Push(lua.LNumber(g.keys.ZCard(a[1])))
		case "HLEN":
			L.Push(lua.LNumber(len(g.keys.HGetAll(a[1]))))
		case "HEXISTS":
			n := 0
			if _, ok := g.keys.HGet(a[1], a[2]); ok {
				n = 1
			}
			L.Push(lua.LNumber(n))
		case "HMGET":
			t := L.NewTable()
			for _, f := range a[2:] {
				if v, ok := g.keys.HGet(a[1], f); ok {
					t.Append(lua.LString(v))
				} else {
					t.Append(lua.LFalse)
				}
			}
			L.Push(t)
		default:
			L.RaiseError("the stub answers no %s", a[0])
		}
		return 1
	}))
	if err := L.DoString(luaStubs); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"sprint_00_core.lua", "sprint_intents.lua"} {
		if err := L.DoString(luaFile(t, f)); err != nil {
			t.Fatalf("load %s: %v", f, err)
		}
	}
	g.sp = L.GetGlobal("NS").(*lua.LTable).RawGetString("SP").(*lua.LTable)
	g.derive = g.sp.RawGetString("phases").(*lua.LTable).RawGetString("derive")
	if g.derive == lua.LNil {
		t.Fatal("the Lua registered no derive phase")
	}
	return g
}

func (g *luaRig) call(fn lua.LValue, n int, args ...lua.LValue) []lua.LValue {
	g.t.Helper()
	if err := g.L.CallByParam(lua.P{Fn: fn, NRet: n, Protect: true}, args...); err != nil {
		g.t.Fatalf("lua: %v", err)
	}
	out := make([]lua.LValue, n)
	for i := range out {
		out[i] = g.L.Get(-n + i)
	}
	g.L.Pop(n)
	return out
}

// run is the Lua phase on one scenario, and x_cmds taking the commands it staged.
func (g *luaRig) run(sc diffScenario) outcome { return g.runTaking(sc, true) }

// runTaking is run, with the commands taken (NS.SP.intent_commands, which x_cmds
// calls) or left on the ctx.
func (g *luaRig) runTaking(sc diffScenario, take bool) outcome {
	t, L := g.t, g.L
	t.Helper()
	ks := newKeyspace()
	ks.apply(sc.keys)
	g.sc, g.keys = sc, &Keys{ks: ks}
	g.reads, g.before = map[string]int{}, 0

	entries := make([]any, 0, len(sc.entries))
	for _, e := range sc.entries {
		entries = append(entries, roundTrip(t, e))
	}
	ctx := luaFrom(L, map[string]any{"space": testPrefix, "request_epoch": "0", "now_ms": sc.now}).(*lua.LTable)
	req := L.NewTable()
	req.RawSetString("entries", luaFrom(L, entries))
	ctx.RawSetString("request", req)
	intents := make([]any, 0, len(sc.intents))
	for _, in := range sc.intents {
		m := map[string]any{"kind": in.Kind, "card": in.Card}
		if in.Need != "" {
			m["need"] = in.Need
		}
		if len(in.Needs) > 0 {
			m["needs"] = roundTrip(t, in.Needs)
		}
		if len(in.Waiters) > 0 {
			m["waiters"] = roundTrip(t, in.Waiters)
		}
		intents = append(intents, m)
	}

	res := g.call(g.derive, 3, ctx, luaFrom(L, intents), L.NewTable())
	if res[2] != lua.LNil {
		r := anyFrom(res[2]).(map[string]any)
		detail, _ := r["detail"].(map[string]any)
		return outcome{Refusal: refusalForm(r["code"].(string), detail["ids"], r["message"].(string), detail["budget"], detail["limit"], detail["actual"])}
	}
	g.ctx = ctx
	var cmds lua.LValue = lua.LNil
	if take {
		cmds = g.call(g.sp.RawGetString("intent_commands"), 1, ctx)[0]
	}
	norm := func(v lua.LValue) any {
		a := anyFrom(v)
		if m, ok := a.(map[string]any); ok && len(m) == 0 {
			return []any(nil)
		}
		return a
	}
	return outcome{Entries: norm(res[0]), Notes: norm(res[1]), Cmds: norm(cmds), Work: anyFrom(ctx.RawGetString("intent_work"))}
}

func luaOutcome(t *testing.T, sc diffScenario) outcome {
	t.Helper()
	return newLuaRig(t).run(sc)
}

// nilIfEmpty makes the empty list of the Go side the nil the Lua side has.
func nilIfEmpty(o outcome) outcome {
	for _, p := range []*any{&o.Entries, &o.Notes, &o.Cmds} {
		if l, ok := (*p).([]any); ok && len(l) == 0 {
			*p = []any(nil)
		}
	}
	return o
}

func fix(row, col, rev string, fields ...string) fixRec {
	r := fixRec{row: row, col: col, rev: rev, fields: map[string]string{}}
	for i := 0; i+1 < len(fields); i += 2 {
		r.fields[fields[i]] = fields[i+1]
	}
	return r
}

// diffScenarios are the situations of the phase, one or more of each named
// test of the item and the refusals around them.
func diffScenarios() []diffScenario {
	now := strconv.FormatInt(testTime.UnixMilli(), 10)
	wait := func(n string, ws ...string) Cmd {
		var pairs []string
		for _, w := range ws {
			pairs = append(pairs, "0", w)
		}
		return zadd("wait:"+n+"@0", pairs...)
	}
	w := func(rev, needs, open string, more ...string) fixRec {
		return fix("s1", "waiting", rev, append([]string{"needs", needs, "open", open}, more...)...)
	}
	landed := fix("s1", "landed", "4")
	ready := fix("s1", "ready", "2")
	dropped := fix("", "", "3")
	createW := func(id, needs, open string) []tset.Entry {
		e, _ := admit(id, needs, open)
		return e
	}
	waitfor := func(id string, needs ...string) Intent { return Intent{Kind: IntentWaitFor, Card: id, Needs: needs} }
	chain := func(n int, prefix string) map[string]fixRec {
		recs := map[string]fixRec{}
		for i := 0; i < n; i++ {
			next := ""
			if i+1 < n {
				next = prefix + strconv.Itoa(i+1)
			}
			recs[prefix+strconv.Itoa(i)] = w("1", next, "1")
		}
		return recs
	}
	merge := func(ms ...map[string]fixRec) map[string]fixRec {
		out := map[string]fixRec{}
		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	var manyWaiters []string
	manyRecs := map[string]fixRec{}
	var manyPairs []string
	for i := 0; i < 1200; i++ {
		id := "w" + strconv.Itoa(i)
		manyWaiters = append(manyWaiters, id)
		manyRecs[id] = w("1", "n", "2")
		manyPairs = append(manyPairs, "0", id)
	}
	manyRecs["n"] = landed
	return []diffScenario{
		{name: "needmet two needs one tick", now: now,
			recs: map[string]fixRec{"n1": landed, "n2": landed, "w": w("3", "n1,n2", "2")},
			keys: []Cmd{wait("n1", "w"), wait("n2", "w")},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n1", Waiters: []string{"w"}},
				{Kind: IntentNeedMet, Need: "n2", Waiters: []string{"w"}}}},
		{name: "needmet of a card in no set and a card twice", now: now,
			recs: map[string]fixRec{"n": landed, "a": w("3", "n", "1"), "b": w("5", "n", "1")},
			keys: []Cmd{wait("n", "a")},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"a", "b", "a"}},
				{Kind: IntentNeedMet, Need: "n", Waiters: []string{"a"}}}},
		{name: "needmet in pieces", now: now, recs: manyRecs, keys: []Cmd{zadd("wait:n@0", manyPairs[:1000]...), zadd("wait:n@0", manyPairs[1000:]...)},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: manyWaiters}}},
		{name: "needgone head moves", now: now,
			recs:    map[string]fixRec{"d": dropped, "w1": w("1", "d", "1"), "w2": w("2", "d", "1"), "w3": w("3", "d", "1")},
			keys:    []Cmd{wait("d", "w1", "w2", "w3")},
			intents: []Intent{{Kind: IntentNeedGone, Need: "d", Waiters: []string{"w1", "w2"}}}},
		{name: "quarantined waiters are left out", now: now,
			recs: map[string]fixRec{"n": landed, "g": dropped, "w1": w("1", "n", "1"), "w2": w("2", "n", "1"), "g1": w("1", "g", "1"), "g2": w("1", "g", "1")},
			keys: []Cmd{wait("n", "w1", "w2"), wait("g", "g1", "g2"), hset("quarantine@0", "w1", "DRIFT", "g1", "DRIFT")},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w1", "w2"}},
				{Kind: IntentNeedGone, Need: "g", Waiters: []string{"g1", "g2"}}}},
		{name: "needmet on a card that is not waiting", now: now,
			recs:    map[string]fixRec{"n": landed, "w": fix("s1", "ready", "1", "needs", "n", "open", "1")},
			keys:    []Cmd{wait("n", "w")},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}}},
		{name: "needmet on a card whose open is 0", now: now,
			recs:    map[string]fixRec{"n": landed, "w": w("1", "n", "0")},
			keys:    []Cmd{wait("n", "w")},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}}},

		{name: "waitfor missing", now: now, keys: []Cmd{hset("clock", "stopped_ms", "1000", "stopped_since_ms", "")},
			entries: createW("w1", "ghost", "1"), intents: []Intent{waitfor("w1", "ghost")}},
		{name: "waitfor missing in a stopped clock", now: now, keys: []Cmd{hset("clock", "stopped_ms", "1000", "stopped_since_ms", strconv.FormatInt(testTime.UnixMilli()-4000, 10))},
			entries: createW("w1", "ghost", "1"), intents: []Intent{waitfor("w1", "ghost")}},
		{name: "waitfor missing already in missing", now: now, keys: []Cmd{zadd("missing@0", "77", "ghost")},
			entries: createW("w1", "ghost", "1"), intents: []Intent{waitfor("w1", "ghost")}},
		{name: "waitfor every state", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")},
			recs: map[string]fixRec{"busy": ready, "done": landed, "gone": dropped},
			entries: append(createW("w3", "busy,ghost2,gone,done,mate", "4"),
				tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:ready", IDs: []string{"mate"}, Scores: []string{"102"}, About: []string{"mate"}}),
			intents: []Intent{waitfor("w3", "busy", "ghost2", "gone", "done", "mate")}},
		{name: "waitfor two cards one ghost", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")},
			entries: append(createW("w4", "ghost3", "1"), createW("w5", "ghost3", "1")...),
			intents: []Intent{waitfor("w4", "ghost3"), waitfor("w5", "ghost3")}},
		{name: "waitfor open mismatch", now: now, recs: map[string]fixRec{"done": landed},
			entries: createW("w", "done,ghost", "2"), intents: []Intent{waitfor("w", "done", "ghost")}},
		{name: "waitfor open not a number", now: now, entries: createW("w", "ghost", "many"), intents: []Intent{waitfor("w", "ghost")}},
		{name: "waitfor need not named", now: now, recs: map[string]fixRec{"busy": ready},
			entries: createW("w", "ghost", "1"), intents: []Intent{waitfor("w", "ghost", "busy")}},
		{name: "waitfor created in ready", now: now,
			entries: []tset.Entry{{Kind: "create", Table: sprint.Work, To: "s1:ready", IDs: []string{"w"}, Scores: []string{"1"},
				Set: map[string]string{"needs": "ghost", "open": "1"}, About: []string{"w"}}}, intents: []Intent{waitfor("w", "ghost")}},
		{name: "waitfor with no create entry", now: now, intents: []Intent{waitfor("w", "ghost")}},
		{name: "waitfor with the open in each and the needs in the set", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")},
			entries: []tset.Entry{{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{"x", "w"}, Scores: []string{"1", "2"},
				Set: map[string]string{"needs": "ghost", "open": "9"}, Each: []map[string]string{{}, {"open": "1"}}, About: []string{"x", "w"}}},
			intents: []Intent{waitfor("w", "ghost")}},

		{name: "cycle of two", now: now, recs: map[string]fixRec{"X": w("1", "Y", "1")},
			entries: createW("Y", "X", "1"), intents: []Intent{waitfor("Y", "X")}},
		{name: "cycle through existing cards", now: now,
			recs:    map[string]fixRec{"a": w("1", "b", "1"), "b": w("1", "c", "1"), "c": w("1", "ghost", "1")},
			entries: createW("ghost", "a", "1"), intents: []Intent{waitfor("ghost", "a")}},
		{name: "cycle of one", now: now, entries: createW("me", "me", "1"), intents: []Intent{waitfor("me", "me")}},
		{name: "cycle between the cards of a step", now: now,
			entries: append(createW("X", "Y", "1"), createW("Y", "X", "1")...), intents: []Intent{waitfor("X", "Y"), waitfor("Y", "X")}},
		{name: "cycle with a long chain", now: now,
			recs: func() map[string]fixRec {
				r := map[string]fixRec{}
				for i := 0; i < 12; i++ {
					r["c"+strconv.Itoa(i)] = w("1", "c"+strconv.Itoa(i+1), "1")
				}
				return r
			}(),
			entries: createW("c12", "c0", "1"), intents: []Intent{waitfor("c12", "c0")}},
		{name: "diamond without a cycle", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")},
			recs:    map[string]fixRec{"d": landed, "run": ready, "b": w("1", "d", "0"), "c": w("1", "d,run", "1")},
			entries: createW("a", "b,c", "2"), intents: []Intent{waitfor("a", "b", "c")}},
		{name: "walk stops at a card that is not waiting", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")},
			recs:    map[string]fixRec{"r": fix("s1", "ready", "1", "needs", "X", "open", "1")},
			entries: createW("X", "r", "1"), intents: []Intent{waitfor("X", "r")}},
		{name: "walk meets an open that is not a number", now: now,
			recs:    map[string]fixRec{"a": w("1", "b", "soon")},
			entries: createW("w", "a", "1"), intents: []Intent{waitfor("w", "a")}},
		{name: "walk over 2,001 records", now: now, recs: chain(2001, "c"),
			entries: createW("w", "c0", "1"), intents: []Intent{waitfor("w", "c0")}},
		{name: "walk over 2,000 records", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")}, recs: chain(2000, "c"),
			entries: createW("w", "c0", "1"), intents: []Intent{waitfor("w", "c0")}},
		{name: "walk bound shared by two admissions", now: now, recs: merge(chain(1001, "c"), chain(1000, "o")),
			entries: append(createW("w1", "c0", "1"), createW("w2", "o0", "1")...), intents: []Intent{waitfor("w1", "c0"), waitfor("w2", "o0")}},
		{name: "a chain read once for two admissions", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")}, recs: chain(1500, "c"),
			entries: append(createW("w1", "c0", "1"), createW("w3", "c0", "1")...), intents: []Intent{waitfor("w1", "c0"), waitfor("w3", "c0")}},

		{name: "waive refused once created", now: now,
			recs:    map[string]fixRec{"ghost": ready, "w": w("1", "ghost,d,run", "3")},
			keys:    []Cmd{wait("ghost", "w", "v"), hset("jopen:w@0", "a primary is blocked on something missing|ghost", "n1", "a primary is blocked on something dropped|d", "n2")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"ghost"}}}},
		{name: "waive a missing need and a dropped one", now: now,
			recs: map[string]fixRec{"d": dropped, "run": ready, "w": w("1", "ghost,d,run", "3"), "v": w("1", "ghost", "1")},
			keys: []Cmd{wait("ghost", "w", "v"), wait("run", "w"), zadd("missing@0", "5", "ghost"),
				hset("jopen:w@0", "a primary is blocked on something missing|ghost", "n1", "a primary is blocked on something dropped|d", "n2"),
				hset("jopen:v@0", "a primary is blocked on something missing|ghost", "n3")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"ghost", "d"}}}},
		{name: "waive the last waiter of a missing need", now: now,
			recs:    map[string]fixRec{"v": w("1", "ghost", "1")},
			keys:    []Cmd{wait("ghost", "v"), zadd("missing@0", "5", "ghost"), hset("jopen:v@0", "a primary is blocked on something missing|ghost", "n3")},
			intents: []Intent{{Kind: IntentWaive, Card: "v", Needs: []string{"ghost"}}}},
		{name: "waive with no judgment open", now: now,
			recs:    map[string]fixRec{"w": w("1", "run", "1")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"run"}}}},
		{name: "waive of a card that is quarantined", now: now,
			recs:    map[string]fixRec{"d": dropped, "w": w("1", "d", "1")},
			keys:    []Cmd{hset("quarantine@0", "w", "DRIFT"), hset("jopen:w@0", "a primary is blocked on something dropped|d", "n2")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"d"}}}},
		{name: "waive on a card that is not waiting", now: now,
			recs:    map[string]fixRec{"w": fix("s1", "ready", "1", "needs", "n", "open", "1")},
			keys:    []Cmd{hset("jopen:w@0", "a primary is blocked on something dropped|n", "n1")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"n"}}}},
		{name: "waive of a need already waived", now: now,
			recs:    map[string]fixRec{"d": dropped, "w": w("1", "d", "1", "waived", "d")},
			keys:    []Cmd{hset("jopen:w@0", "a primary is blocked on something dropped|d", "n2")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"d"}}}},
		{name: "waive of a missing need the card is not in the set for", now: now,
			recs:    map[string]fixRec{"w": w("1", "n", "1")},
			keys:    []Cmd{hset("jopen:w@0", "a primary is blocked on something missing|n", "n1")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"n"}}}},
		{name: "two waives of one card, each naming its own needs", now: now,
			recs: map[string]fixRec{"d": dropped, "w": w("1", "ghost,d,run", "3")},
			keys: []Cmd{wait("ghost", "w"), zadd("missing@0", "5", "ghost"),
				hset("jopen:w@0", "a primary is blocked on something missing|ghost", "n1", "a primary is blocked on something dropped|d", "n2")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"ghost"}}, {Kind: IntentWaive, Card: "w", Needs: []string{"d", "run"}}}},
		{name: "waive of a need with both judgments open", now: now,
			recs:    map[string]fixRec{"d": dropped, "w": w("1", "d", "1")},
			keys:    []Cmd{wait("d", "w"), hset("jopen:w@0", "a primary is blocked on something missing|d", "n1", "a primary is blocked on something dropped|d", "n2")},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"d"}}}},

		{name: "fold across cells", now: now,
			recs: map[string]fixRec{"a": landed, "b": landed, "c": dropped, "w": w("1", "a,b,c", "3"), "x": w("2", "a", "1"),
				"y": fix("s2", "waiting", "3", "needs", "a", "open", "1")},
			keys: []Cmd{wait("a", "w", "x", "y"), wait("b", "w"), hset("jopen:w@0", "a primary is blocked on something dropped|c", "n1")},
			intents: []Intent{{Kind: IntentNeedMet, Need: "a", Waiters: []string{"w", "x", "y"}},
				{Kind: IntentNeedMet, Need: "b", Waiters: []string{"w"}},
				{Kind: IntentWaive, Card: "w", Needs: []string{"c"}}}},

		{name: "cycle found through a card an earlier admission expanded", now: now,
			recs:    map[string]fixRec{"c": w("1", "w2", "1")},
			entries: append(createW("w1", "c", "1"), createW("w2", "c", "1")...), intents: []Intent{waitfor("w1", "c"), waitfor("w2", "c")}},
		{name: "cycle reachable by two paths", now: now,
			recs:    map[string]fixRec{"n": w("1", "a,b", "2"), "a": w("1", "w", "1"), "b": w("1", "w", "1")},
			entries: createW("w", "n", "1"), intents: []Intent{waitfor("w", "n")}},
		{name: "cycle reachable by two paths, the needs the other way", now: now,
			recs:    map[string]fixRec{"n": w("1", "b,a", "2"), "a": w("1", "w", "1"), "b": w("1", "w", "1")},
			entries: createW("w", "n", "1"), intents: []Intent{waitfor("w", "n")}},
		{name: "a cycle the second need closes", now: now,
			recs:    map[string]fixRec{"p": w("1", "q", "1"), "q": w("1", "w", "1")},
			entries: createW("w", "done,p", "2"), intents: []Intent{waitfor("w", "done", "p")}},
		{name: "walk at the bound over landed, ready, absent and dropped records", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")},
			recs: mixedTree(WalkRecordsMax, nil), entries: createW("w", "r0", "1"), intents: []Intent{waitfor("w", "r0")}},
		{name: "walk one past the bound over landed, ready, absent and dropped records", now: now,
			recs: mixedTree(WalkRecordsMax+1, nil), entries: createW("w", "r0", "1"), intents: []Intent{waitfor("w", "r0")}},
		{name: "a cycle met before the bound", now: now,
			recs: mixedTree(WalkRecordsMax+200, func(i int, leaves []string) []string {
				if i == 0 {
					return append(append([]string{}, leaves...), "w")
				}
				return leaves
			}), entries: createW("w", "r0", "1"), intents: []Intent{waitfor("w", "r0")}},
		{name: "the bound met before a cycle", now: now,
			recs: mixedTree(WalkRecordsMax+200, func(i int, leaves []string) []string {
				if i == 39 {
					return append(append([]string{}, leaves...), "w")
				}
				return leaves
			}), entries: createW("w", "r0", "1"), intents: []Intent{waitfor("w", "r0")}},
		{name: "a bulk add of 2,000 cards that wait for one card", now: now, recs: chainRecs(50),
			entries: func() []tset.Entry { e, _ := bulkAdmit(2000, "c0", "1"); return e }(),
			intents: func() []Intent { _, in := bulkAdmit(2000, "c0", "1"); return in }()},
		{name: "a bulk add of 2,000 cards that wait for one ghost", now: now, keys: []Cmd{hset("clock", "stopped_ms", "0")},
			entries: func() []tset.Entry { e, _ := bulkAdmit(2000, "ghost", "1"); return e }(),
			intents: func() []Intent { _, in := bulkAdmit(2000, "ghost", "1"); return in }()},

		{name: "an admitted card already in the wait sets and in missing", now: now, keys: []Cmd{wait("c", "w"), wait("ghost", "w"), zadd("missing@0", "9", "ghost")},
			recs:    map[string]fixRec{"c": w("1", "", "1")},
			entries: createW("w", "c,ghost", "2"), intents: []Intent{waitfor("w", "c", "ghost")}},
		{name: "needmet and needgone with no card", now: now,
			recs:    map[string]fixRec{"n": landed, "d": dropped, "a": w("1", "n", "1"), "b": w("1", "d", "1")},
			keys:    []Cmd{wait("n", "a"), wait("d", "b")},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"a"}}, {Kind: IntentNeedGone, Need: "d", Waiters: []string{"b"}}}},
		{name: "needmet of a card the request names", now: now,
			recs: map[string]fixRec{"n": landed, "w": w("1", "n", "1")}, keys: []Cmd{wait("n", "w")},
			entries: []tset.Entry{{Kind: "move", Table: sprint.Work, From: "s1:waiting", To: "s1:ready", IDs: []string{"w"}}},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}}},
		{name: "waive of a card the request guards", now: now,
			recs:    map[string]fixRec{"d": dropped, "w": w("1", "d", "1")},
			keys:    []Cmd{hset("jopen:w@0", "a primary is blocked on something dropped|d", "n2")},
			entries: []tset.Entry{{Kind: "guard", Table: sprint.Work, IDs: []string{"x", "w"}}},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"d"}}}},
		{name: "needmet and waive of a card the request names", now: now,
			recs: map[string]fixRec{"n": landed, "d": dropped, "w": w("1", "n,d", "2")}, keys: []Cmd{wait("n", "w"),
				hset("jopen:w@0", "a primary is blocked on something dropped|d", "n2")},
			entries: []tset.Entry{{Kind: "remove", Table: sprint.Work, From: "s1:waiting", IDs: []string{"w"}}},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}, {Kind: IntentWaive, Card: "w", Needs: []string{"d"}}}},
		{name: "a card the request names that the phase does not change", now: now,
			recs:    map[string]fixRec{"w": w("1", "run", "1")},
			entries: []tset.Entry{{Kind: "guard", Table: sprint.Work, IDs: []string{"w"}}},
			intents: []Intent{{Kind: IntentWaive, Card: "w", Needs: []string{"run"}}}},
		{name: "a card another table's entry names", now: now,
			recs: map[string]fixRec{"n": landed, "w": w("1", "n", "1")}, keys: []Cmd{wait("n", "w")},
			entries: []tset.Entry{{Kind: "guard", Table: sprint.Fleet, IDs: []string{"w"}}},
			intents: []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}}},

		{name: "shape: unknown kind", now: now, intents: []Intent{{Kind: "hold", Card: "w"}}},
		{name: "shape: waitfor with no card", now: now, intents: []Intent{{Kind: IntentWaitFor, Needs: []string{"n"}}}},
		{name: "shape: waitfor with no needs", now: now, intents: []Intent{{Kind: IntentWaitFor, Card: "w"}}},
		{name: "shape: waitfor with 65 needs", now: now, intents: func() []Intent {
			var ns []string
			for i := 0; i < 65; i++ {
				ns = append(ns, "n"+strconv.Itoa(i))
			}
			return []Intent{{Kind: IntentWaitFor, Card: "w", Needs: ns}}
		}()},
		{name: "shape: waitfor with an empty need", now: now, intents: []Intent{{Kind: IntentWaitFor, Card: "w", Needs: []string{"n", ""}}}},
		{name: "shape: two waitfor of one card", now: now, intents: []Intent{waitfor("w", "n"), waitfor("w", "m")}},
		{name: "shape: waive with no needs", now: now, intents: []Intent{{Kind: IntentWaive, Card: "w"}}},
		{name: "shape: needmet with no need", now: now, intents: []Intent{{Kind: IntentNeedMet, Waiters: []string{"w"}}}},
		{name: "shape: needgone with an empty waiter", now: now, intents: []Intent{{Kind: IntentNeedGone, Need: "n", Waiters: []string{"w", ""}}}},
		{name: "shape: more waiters than a chunk", now: now, intents: func() []Intent {
			var ws []string
			for i := 0; i < 2000; i++ {
				ws = append(ws, "w"+strconv.Itoa(i))
			}
			return []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: ws}, {Kind: IntentNeedGone, Need: "m", Waiters: []string{"x"}}}
		}()},
		{name: "clock with a field that is not a number", now: now, keys: []Cmd{hset("clock", "stopped_ms", "soon")},
			entries: createW("w", "ghost", "1"), intents: []Intent{waitfor("w", "ghost")}},
	}
}

// TestDeriveLuaEqualsGo: the Lua phase and the Go phase, given one scenario,
// decide the same entries, requests to J and commands, or refuse with the same
// code, message and ids. The scenarios are those of the named tests of the item
// and the refusals around them.
func TestDeriveLuaEqualsGo(t *testing.T) {
	t.Parallel()
	for _, sc := range diffScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			g, l := nilIfEmpty(goOutcome(t, sc)), nilIfEmpty(luaOutcome(t, sc))
			if !reflect.DeepEqual(g, l) {
				gj, _ := json.MarshalIndent(g, "", " ")
				lj, _ := json.MarshalIndent(l, "", " ")
				t.Fatalf("Go and Lua decided differently\nGo:  %s\nLua: %s", clip(gj), clip(lj))
			}
		})
	}
}

func clip(b []byte) string {
	if len(b) > 4000 {
		return string(b[:4000]) + "..."
	}
	return string(b)
}

// TestDeriveScenariosAreNotVacuous: the scenarios decide something, refuse,
// or are the named no-ops, so equality of two empty outcomes cannot pass a
// phase that does nothing. Each refusal code and each kind of effect the item
// names is among them.
func TestDeriveScenariosAreNotVacuous(t *testing.T) {
	t.Parallel()
	codes := map[string]int{}
	var entries, notes, cmds, empty int
	noop := map[string]bool{"waive with no judgment open": true, "waive of a need already waived": true,
		"waive of a card that is quarantined": true, "a card the request names that the phase does not change": true}
	for _, sc := range diffScenarios() {
		o := nilIfEmpty(goOutcome(t, sc))
		if o.Refusal != nil {
			codes[o.Refusal.(map[string]any)["code"].(string)]++
			continue
		}
		e, n, c := nonEmpty(o.Entries), nonEmpty(o.Notes), nonEmpty(o.Cmds)
		entries += b2i(e)
		notes += b2i(n)
		cmds += b2i(c)
		if !e && !n && !c {
			empty++
			if !noop[sc.name] {
				t.Errorf("scenario %q decides nothing and refuses nothing", sc.name)
			}
		}
	}
	for _, code := range []string{CodeRequest, CodeXGuard, CodeConfig, "DRIFT"} {
		if codes[code] == 0 {
			t.Errorf("no scenario is refused %s", code)
		}
	}
	if entries == 0 || notes == 0 || cmds == 0 || empty != len(noop) {
		t.Errorf("scenarios with entries %d, notes %d, commands %d, no decision %d; want some of each and the %d no-ops", entries, notes, cmds, empty, len(noop))
	}
}

// TestDeriveScenariosSetNoCardOnNeedmetOrNeedgone: the wire sends an 8.0-shaped
// needmet or needgone with an empty Card, and the phase reads the need and the
// waiters, never the card; no scenario gives either kind a Card, so that the
// empty one is the one the two halves are held equal on.
func TestDeriveScenariosSetNoCardOnNeedmetOrNeedgone(t *testing.T) {
	t.Parallel()
	seen := 0
	for _, sc := range diffScenarios() {
		for _, it := range sc.intents {
			if it.Kind != IntentNeedMet && it.Kind != IntentNeedGone {
				continue
			}
			seen++
			if it.Card != "" {
				t.Errorf("scenario %q: a %s sets Card %q", sc.name, it.Kind, it.Card)
			}
		}
	}
	if seen < 10 {
		t.Errorf("the scenarios hold %d needmet and needgone intents; want at least 10", seen)
	}
}

// nonEmpty says a list of an outcome has something in it.
func nonEmpty(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) > 0
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestDeriveLuaIndexFieldsAreGos: the list of the fields the indexes read,
// which every entry of the phase declares as before_fields, is the same in the
// Lua file as sprint.IndexFields() (IT02) gives it, until sprint_defs.lua
// renders it.
func TestDeriveLuaIndexFieldsAreGos(t *testing.T) {
	t.Parallel()
	m := regexp.MustCompile(`INDEX_FIELDS\s*=\s*\{([^}]*)\}`).FindStringSubmatch(luaFile(t, "sprint_intents.lua"))
	if m == nil {
		t.Fatal("no INDEX_FIELDS in the Lua")
	}
	var got []string
	for _, s := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		got = append(got, s[1])
	}
	want := sprint.IndexFields()
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the Lua lists %v; sprint.IndexFields() is %v", got, want)
	}
}

// luaConstants are the literal constants a Lua file declares at its top, by
// name: `local A, B = 'x', 2` gives A "x" and B "2".
func luaConstants(src string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "local ") {
			continue
		}
		lhs, rhs, ok := strings.Cut(strings.TrimPrefix(line, "local "), " = ")
		if !ok || strings.Contains(lhs, "(") {
			continue
		}
		names, values := strings.Split(lhs, ","), strings.Split(strings.SplitN(rhs, " --", 2)[0], ",")
		if len(names) != len(values) {
			continue
		}
		for i, n := range names {
			v := strings.TrimSpace(values[i])
			if strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") && len(v) >= 2 {
				v = v[1 : len(v)-1]
			}
			out[strings.TrimSpace(n)] = v
		}
	}
	return out
}

// TestDeriveLuaConstantsAreGos: the judgment types, the texts, the column
// names and the bounds the Lua repeats are the Go's.
func TestDeriveLuaConstantsAreGos(t *testing.T) {
	t.Parallel()
	got := luaConstants(luaFile(t, "sprint_intents.lua"))
	for name, want := range map[string]string{
		"TYPE_MISSING": sprint.NMissingNeed, "TYPE_DROPPED": sprint.NBlocked,
		"TEXT_MISSING": deriveTextMissing, "TEXT_DROPPED": deriveTextDropped,
		"WAIT_SCORE": deriveWaitScore, "WAITING": string(sprint.Waiting), "LANDED": string(sprint.Landed),
		"WALK_MAX": strconv.Itoa(WalkRecordsMax), "NEEDS_MAX": strconv.Itoa(NeedsPerCardMax),
		"WAITERS_MAX": strconv.Itoa(IntentWaitersMax), "CHAIN_NAMED": strconv.Itoa(deriveChainNamed),
		"PIECE": strconv.Itoa(maxPieces),
	} {
		if v, ok := got[name]; !ok || v != want {
			t.Errorf("Lua %s is %q (declared: %v); Go has %q", name, v, ok, want)
		}
	}
}

// The random differential: worlds and steps drawn from a seeded generator,
// each given to the Go phase and the Lua phase, which must decide the same.
// The generator draws cards in every state (waiting, ready, landed, dropped,
// absent), needs among them and among ids with no record, wait sets that agree
// with the cards and sometimes do not, quarantined cards, open judgments, a
// clock running or stopped, and steps of one to three intents of every kind,
// with create entries that carry the right open and sometimes the wrong one.

// randomScenario draws one scenario.
func randomScenario(rng *rand.Rand) diffScenario {
	pool := []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"}
	ghosts := []string{"g0", "g1", "g2"}
	fresh := []string{"n0", "n1", "n2"}
	pick := func(from []string) string { return from[rng.IntN(len(from))] }
	subset := func(from []string, lo, hi int) []string {
		n := lo + rng.IntN(hi-lo+1)
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, pick(from))
		}
		return out
	}
	chance := func(p float64) bool { return rng.Float64() < p }

	sc := diffScenario{recs: map[string]fixRec{}, now: strconv.FormatInt(testTime.UnixMilli()+int64(rng.IntN(100000)), 10)}
	state := map[string]string{}
	var waiting []string
	for _, id := range pool {
		roll := rng.IntN(100)
		rev := strconv.Itoa(1 + rng.IntN(9))
		switch {
		case roll < 45:
			state[id] = "waiting"
			waiting = append(waiting, id)
		case roll < 60:
			state[id] = "ready"
			sc.recs[id] = fix("s1", "ready", rev)
		case roll < 75:
			state[id] = "landed"
			sc.recs[id] = fix("s1", "landed", rev)
		case roll < 85:
			state[id] = "dropped"
			sc.recs[id] = fix("", "", rev)
		default:
			state[id] = "absent"
		}
	}
	unmet := func(n string) bool { return state[n] != "landed" }
	var cmds []Cmd
	waiters := map[string][]string{} // need -> the cards the keys say wait for it, in draw order
	var needsWithWaiters []string
	var judged [][2]string // (card, need) with a judgment open
	for _, id := range waiting {
		// An existing card may name a fresh id: a card the step admits can then
		// close a cycle through existing cards, which it did not before.
		needs := dedupIDs(subset(append(append(append([]string{}, pool...), ghosts...), fresh...), 0, 3))
		needs = slicesDeleteValue(needs, id)
		open := 0
		for _, n := range needs {
			if unmet(n) {
				open++
			}
		}
		if chance(0.25) {
			open = rng.IntN(4)
		}
		row := "s1"
		if chance(0.15) {
			row = "s2"
		}
		fields := []string{"needs", strings.Join(needs, ","), "open", strconv.Itoa(open)}
		if chance(0.2) {
			fields = append(fields, "waived", pick(append(append([]string{}, pool...), ghosts...)))
		}
		sc.recs[id] = fix(row, "waiting", strconv.Itoa(1+rng.IntN(9)), fields...)
		for _, n := range needs {
			if state[n] != "landed" && state[n] != "dropped" && chance(0.85) {
				cmds = append(cmds, zadd("wait:"+n+"@0", "0", id))
				if len(waiters[n]) == 0 {
					needsWithWaiters = append(needsWithWaiters, n)
				}
				waiters[n] = append(waiters[n], id)
			}
			if chance(0.3) {
				typ := "a primary is blocked on something missing"
				if chance(0.5) {
					typ = "a primary is blocked on something dropped"
				}
				cmds = append(cmds, hset("jopen:"+id+"@0", typ+"|"+n, "n"+strconv.Itoa(rng.IntN(50))))
				judged = append(judged, [2]string{id, n})
			}
		}
		if chance(0.08) {
			cmds = append(cmds, hset("quarantine@0", id, "DRIFT"))
		}
	}
	for _, id := range pool {
		// a card the keys say waits and whose record says it does not: DRIFT when a step names it
		if state[id] != "waiting" && state[id] != "absent" && chance(0.15) {
			n := pick(append(append([]string{}, pool...), ghosts...))
			cmds = append(cmds, zadd("wait:"+n+"@0", "0", id))
			if len(waiters[n]) == 0 {
				needsWithWaiters = append(needsWithWaiters, n)
			}
			waiters[n] = append(waiters[n], id)
		}
	}
	for _, g := range ghosts {
		if chance(0.5) {
			cmds = append(cmds, zadd("missing@0", strconv.Itoa(rng.IntN(1000)), g))
		}
	}
	switch rng.IntN(3) {
	case 0:
		cmds = append(cmds, hset("clock", "stopped_ms", strconv.Itoa(rng.IntN(5000))))
	case 1:
		cmds = append(cmds, hset("clock", "stopped_ms", strconv.Itoa(rng.IntN(5000)), "stopped_since_ms", strconv.FormatInt(testTime.UnixMilli()-int64(rng.IntN(5000)), 10)))
	}
	sc.keys = cmds

	usedFresh := 0
	for i, n := 0, 1+rng.IntN(3); i < n; i++ {
		switch roll := rng.IntN(100); {
		case roll < 35 && usedFresh < len(fresh):
			id := fresh[usedFresh]
			usedFresh++
			needs := dedupIDs(subset(append(append(append([]string{}, pool...), ghosts...), fresh...), 1, 3))
			needs = slicesDeleteValue(needs, id)
			if len(needs) == 0 {
				needs = []string{pick(pool)}
			}
			open := 0
			for _, n := range needs {
				if unmet(n) {
					open++
				}
			}
			if chance(0.25) {
				open = rng.IntN(4)
			}
			col := "waiting"
			if chance(0.05) {
				col = "ready"
			}
			if !chance(0.04) {
				sc.entries = append(sc.entries, tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:" + col, IDs: []string{id},
					Scores: []string{"50"}, Set: map[string]string{"needs": strings.Join(needs, ","), "open": strconv.Itoa(open)}, About: []string{id}})
			}
			sc.intents = append(sc.intents, Intent{Kind: IntentWaitFor, Card: id, Needs: needs})
		case roll < 60, roll < 75:
			kind := IntentNeedMet
			if roll >= 60 {
				kind = IntentNeedGone
			}
			need, who := pick(append(append([]string{}, pool...), ghosts...)), subset(pool, 0, 4)
			if len(needsWithWaiters) > 0 && chance(0.8) {
				need = pick(needsWithWaiters)
				who = append(subset(waiters[need], 1, 4), subset(pool, 0, 1)...)
			}
			sc.intents = append(sc.intents, Intent{Kind: kind, Need: need, Waiters: who})
		case roll < 82:
			// A plain entry of the request on a card: a card is named once in a step.
			kind := []string{"guard", "move", "remove"}[rng.IntN(3)]
			sc.entries = append(sc.entries, tset.Entry{Kind: kind, Table: sprint.Work, From: "s1:waiting", To: "s1:ready", IDs: []string{pick(pool)}})
		default:
			card, needs := pick(pool), subset(append(append([]string{}, pool...), ghosts...), 1, 2)
			if len(judged) > 0 && chance(0.8) {
				j := judged[rng.IntN(len(judged))]
				card, needs = j[0], append([]string{j[1]}, subset(append(append([]string{}, pool...), ghosts...), 0, 1)...)
			}
			sc.intents = append(sc.intents, Intent{Kind: IntentWaive, Card: card, Needs: needs})
		}
	}
	return sc
}

// slicesDeleteValue is the list without the value.
func slicesDeleteValue(list []string, v string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// existingCard matches the ids of the random draw's existing cards (c0 to c7)
// in a refusal's message, which names the chain of a cycle.
var existingCard = regexp.MustCompile(`\bc[0-7]\b`)

// randomDifferentialRuns is how many random steps the differential runs, in
// randomDifferentialChunks parallel parts: enough that every branch of the
// phase is met many times, and few enough for a unit test.
const (
	randomDifferentialRuns   = 4000
	randomDifferentialChunks = 4
)

// TestDeriveRandomStepsLuaEqualsGo: on random worlds and steps the Lua phase
// and the Go phase decide the same entries, requests to J and commands, or
// refuse with the same code, message and ids; and the draw is not vacuous: it
// reaches every intent's effect and every refusal the phase makes.
func TestDeriveRandomStepsLuaEqualsGo(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	codes := map[string]int{}
	effects := map[string]int{}
	kinds := map[string]int{} // refusals by what they say: a cycle, a card named twice
	for chunk := 0; chunk < randomDifferentialChunks; chunk++ {
		t.Run("chunk "+strconv.Itoa(chunk), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewPCG(uint64(chunk)+1, 0x1714))
			rig := newLuaRig(t)
			for i := 0; i < randomDifferentialRuns/randomDifferentialChunks; i++ {
				sc := randomScenario(rng)
				sc.name = fmt.Sprintf("chunk %d run %d", chunk, i)
				g, l := nilIfEmpty(goOutcome(t, sc)), nilIfEmpty(rig.run(sc))
				if !reflect.DeepEqual(g, l) {
					gj, _ := json.MarshalIndent(g, "", " ")
					lj, _ := json.MarshalIndent(l, "", " ")
					in, _ := json.Marshal(sc.intents)
					t.Fatalf("%s: Go and Lua decided differently on %s\nGo:  %s\nLua: %s", sc.name, in, clip(gj), clip(lj))
				}
				mu.Lock()
				if g.Refusal != nil {
					codes[g.Refusal.(map[string]any)["code"].(string)]++
					msg := g.Refusal.(map[string]any)["message"].(string)
					for _, kind := range []string{"a needs cycle", "named by entry"} {
						if strings.Contains(msg, kind) {
							kinds[kind]++
						}
					}
					if strings.Contains(msg, "a needs cycle") && existingCard.MatchString(msg) {
						kinds["a needs cycle through an existing card"]++
					}
				} else {
					for _, e := range []struct {
						name string
						has  bool
					}{{"entries", nonEmpty(g.Entries)}, {"notes", nonEmpty(g.Notes)}, {"commands", nonEmpty(g.Cmds)}} {
						if e.has {
							effects[e.name]++
						}
					}
				}
				mu.Unlock()
			}
		})
	}
	t.Cleanup(func() {
		t.Logf("refusals by kind %v, codes %v, effects %v", kinds, codes, effects)
		for kind, least := range map[string]int{"a needs cycle": 50, "a needs cycle through an existing card": 30, "named by entry": 5} {
			if kinds[kind] < least {
				t.Errorf("the draw reached a refusal saying %q %d times; want at least %d (%v)", kind, kinds[kind], least, kinds)
			}
		}
		for _, code := range []string{CodeRequest, CodeXGuard, "DRIFT"} {
			if codes[code] < 10 {
				t.Errorf("the draw reached refusal %s %d times; want at least 10 (codes %v)", code, codes[code], codes)
			}
		}
		for _, name := range []string{"entries", "notes", "commands"} {
			if effects[name] < 50 {
				t.Errorf("the draw decided %s %d times; want at least 50 (effects %v)", name, effects[name], effects)
			}
		}
	})
}

// TestDeriveLuaCommandsArePendingUntilTaken: the commands the Lua phase stages
// on the call's ctx are pending until x_cmds takes them (NS.SP.intent_commands),
// which is what lets the core refuse a step that left them behind; a call that
// staged none has none pending.
func TestDeriveLuaCommandsArePendingUntilTaken(t *testing.T) {
	t.Parallel()
	var withCmds, without diffScenario
	for _, sc := range diffScenarios() {
		switch sc.name {
		case "needgone head moves":
			withCmds = sc
		case "waive with no judgment open":
			without = sc
		}
	}
	g := newLuaRig(t)
	pending := func() bool {
		return g.call(g.sp.RawGetString("intent_commands_pending"), 1, g.ctx)[0] == lua.LTrue
	}
	g.runTaking(withCmds, false)
	if !pending() {
		t.Fatal("commands staged and not taken are not pending")
	}
	if cmds := g.call(g.sp.RawGetString("intent_commands"), 1, g.ctx)[0]; cmds.(*lua.LTable).Len() == 0 {
		t.Fatal("x_cmds was given no commands")
	}
	if pending() {
		t.Fatal("commands taken are still pending")
	}
	g.runTaking(without, false)
	if pending() {
		t.Fatal("a call that staged no command has one pending")
	}
}
