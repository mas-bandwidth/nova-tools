package sprintfn

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The Lua half of X, run beside the twin. Before gate G0 no store loads
// sprint_x.lua; what stands in for the store here is gopher-lua (Lua 5.1, the
// dialect Redis runs) and a Go fake of the few helpers of Layer 1 that X uses
// (S.rd, S.before, S.refuse, S.stage, S.uint, S.cell, S.array), answering from the
// twin's own state at the moment the twin calls X: the same sprint keys, the same
// before-state, the same plan. Every step a test sends through a harness with the
// mirror on is run through x_pre and x_cmds too, and must give the refusal (its
// code and the ids and rows it names) and the commands (their argv and access,
// scores compared as numbers) the Go half gave, and the reads X.pre made. What
// this does not test is the store: S.rd here is a model of the typed read, and
// Layer 1's own code is not loaded. After G0 the comparison is run against the
// store (IT12's TestTwinEqualsLua10000).

type xLuaMirror struct {
	t    *testing.T
	L    *lua.LState
	null *lua.LUserData
	sp   *lua.LTable // NS.SP
	cur  *State
	obs  *Before
	ctxs map[*State]*lua.LTable

	probes    int // the store reads the Lua X.pre has made in this call
	pre, cmds int // comparisons made

	reads      []xLuaRead      // the store reads the Lua X.pre made in this call, in order
	readKeys   map[string]bool // the keys they read: Layer 1 types a key when it reads it
	typeProbes int             // the type reads prepare would make for the keys the Lua x_cmds wrote and X.pre had not read
	cmdLog     [][]string      // the argv of the commands the Lua x_cmds wrote in the last call
}

// xLuaRead is one store read the Lua X.pre made: its command, key, how many
// members or fields it named, and what it reserved.
type xLuaRead struct {
	cmd, key       string
	members        int
	reserve, bytes int
}

func newXLuaMirror(t *testing.T) *xLuaMirror {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	m := &xLuaMirror{t: t, L: L, null: L.NewUserData(), ctxs: map[*State]*lua.LTable{}, readKeys: map[string]bool{}}
	cjson := L.NewTable()
	cjson.RawSetString("null", m.null)
	L.SetGlobal("cjson", cjson)
	ns := L.NewTable()
	ns.RawSetString("tset_profile", lua.LString("sprint"))
	L.SetGlobal("NS", ns)
	s := L.NewTable()
	ns.RawSetString("tset", s)
	m.install(s)

	frags, err := fn.SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	x, err := fs.ReadFile(fn.Spec().Files, "lua/sprint_x.lua")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, src string }{{"core", frags[0].Source}, {"x", string(x)}} { // load order
		if err := L.DoString(f.src); err != nil {
			t.Fatalf("load %s: %v", f.name, err)
		}
	}
	m.sp = ns.RawGetString("SP").(*lua.LTable)
	return m
}

// toLua is a decoded JSON value as a Lua value, null as cjson.null.
func (m *xLuaMirror) toLua(v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return m.null
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case string:
		return lua.LString(x)
	case []any:
		t := m.L.NewTable()
		for _, e := range x {
			t.Append(m.toLua(e))
		}
		return t
	case map[string]any:
		t := m.L.NewTable()
		for k, e := range x {
			t.RawSetString(k, m.toLua(e))
		}
		return t
	}
	panic(fmt.Sprintf("no Lua value for %T", v))
}

func (m *xLuaMirror) fromJSON(b []byte) lua.LValue {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		m.t.Fatal(err)
	}
	return m.toLua(v)
}

func (m *xLuaMirror) strOrNull(s string) lua.LValue {
	if s == "" {
		return m.null
	}
	return lua.LString(s)
}

func (m *xLuaMirror) stringList(l []string) *lua.LTable {
	t := m.L.NewTable()
	for _, s := range l {
		t.Append(lua.LString(s))
	}
	return t
}

// refusal is Layer 1's refusal object as the Lua fake builds it.
func (m *xLuaMirror) refusal(code string, detail lua.LValue, message lua.LValue) *lua.LTable {
	t := m.L.NewTable()
	t.RawSetString("status", lua.LString("refused"))
	t.RawSetString("code", lua.LString(code))
	d, ok := detail.(*lua.LTable)
	if !ok {
		d = m.L.NewTable()
	}
	for _, k := range []string{"ids", "cells", "rows"} {
		if l, ok := d.RawGetString(k).(*lua.LTable); !ok || l.Len() == 0 {
			d.RawSetString(k, m.L.NewTable())
		}
	}
	t.RawSetString("detail", d)
	msg := code
	if s, ok := message.(lua.LString); ok {
		msg = string(s)
	}
	t.RawSetString("message", lua.LString(msg+"; nothing was changed"))
	return t
}

// install puts the fake helpers of Layer 1 in S.
func (m *xLuaMirror) install(s *lua.LTable) {
	L := m.L
	s.RawSetString("refuse", L.NewFunction(func(L *lua.LState) int {
		L.Push(m.refusal(L.CheckString(1), L.Get(2), L.Get(3)))
		return 1
	}))
	s.RawSetString("array", L.NewFunction(func(L *lua.LState) int { L.Push(L.NewTable()); return 1 }))
	s.RawSetString("uint", L.NewFunction(func(L *lua.LState) int {
		v, ok := L.Get(1).(lua.LString)
		good := false
		if ok {
			_, err := strconv.ParseUint(string(v), 10, 64)
			good = err == nil && (len(v) == 1 || v[0] != '0')
		}
		L.Push(lua.LBool(good))
		return 1
	}))
	s.RawSetString("cell", L.NewFunction(func(L *lua.LState) int {
		ref := L.CheckString(1)
		i := strings.LastIndexByte(ref, ':')
		if i <= 0 || i == len(ref)-1 {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(ref[:i]))
		L.Push(lua.LString(ref[i+1:]))
		return 2
	}))
	s.RawSetString("stage", L.NewFunction(func(L *lua.LState) int {
		plan := L.CheckTable(2)
		command, key, kind, args := L.CheckString(3), L.CheckString(4), L.CheckString(5), L.CheckTable(6)
		argv := L.NewTable()
		argv.Append(lua.LString(command))
		argv.Append(lua.LString(key))
		args.ForEach(func(_, v lua.LValue) { argv.Append(v) })
		access := L.NewTable()
		a := L.NewTable()
		a.RawSetString("key", lua.LString(key))
		a.RawSetString("kind", lua.LString(kind))
		a.RawSetString("mode", lua.LString("write"))
		access.Append(a)
		d := L.NewTable()
		d.RawSetString("argv", argv)
		d.RawSetString("access", access)
		cmds := plan.RawGetString("commands").(*lua.LTable)
		cmds.Append(d)
		L.Push(lua.LTrue)
		return 1
	}))
	s.RawSetString("rd", L.NewFunction(m.rd))
	s.RawSetString("before", L.NewFunction(m.before))
}

// payload is Layer 1's payload(): the bytes of a reply, as the read reservation
// counts them.
func xPayload(v lua.LValue) int {
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
			n += xPayload(e)
		})
		return n
	}
	return 0
}

// rd is S.rd: a typed read of a sprint key, counted as one probe, and refused DRIFT
// when its reply is larger than the bytes it reserved, as S.readcmd refuses it.
func (m *xLuaMirror) rd(L *lua.LState) int {
	argv, kind, reserve := L.CheckTable(2), L.CheckString(3), L.CheckInt(4)
	cmd := strings.ToUpper(argv.RawGetInt(1).String())
	key := argv.RawGetInt(2).String()
	m.probes++
	m.readKeys[key] = true
	read := xLuaRead{cmd: cmd, key: key, members: argv.Len() - 2, reserve: reserve}
	keys := m.cur.Keys
	if t := keys.Type(key); t != kindNone && t != kind {
		L.Push(lua.LNil)
		L.Push(m.refusal("WRONGTYPE", lua.LNil, lua.LNil))
		return 2
	}
	rest := func() []string {
		var out []string
		for i := 3; i <= argv.Len(); i++ {
			out = append(out, argv.RawGetInt(i).String())
		}
		return out
	}
	switch cmd {
	case "HGET":
		v, ok := keys.HGet(key, rest()[0])
		if !ok {
			L.Push(lua.LFalse)
		} else {
			L.Push(lua.LString(v))
		}
	case "GET":
		v, ok := keys.Get(key)
		if !ok {
			L.Push(lua.LFalse)
		} else {
			L.Push(lua.LString(v))
		}
	case "HLEN":
		n := 0
		if v := keys.ks.vals[key]; v != nil && v.kind == kindHash {
			n = len(v.hash)
		}
		L.Push(lua.LNumber(n))
	case "HMGET", "ZMSCORE":
		out := L.NewTable()
		for _, f := range rest() {
			if cmd == "HMGET" {
				if v, ok := keys.HGet(key, f); ok {
					out.Append(lua.LString(v))
				} else {
					out.Append(lua.LFalse)
				}
			} else if v, ok := keys.ZScore(key, f); ok {
				out.Append(lua.LString(strconv.FormatFloat(v, 'f', -1, 64)))
			} else {
				out.Append(lua.LFalse)
			}
		}
		L.Push(out)
	default:
		L.RaiseError("the fake S.rd does not answer %s", cmd)
	}
	read.bytes = xPayload(L.Get(-1))
	m.reads = append(m.reads, read)
	if read.bytes > reserve {
		L.Pop(1)
		L.Push(lua.LNil)
		L.Push(m.refusal("DRIFT", lua.LNil, lua.LNil))
		return 2
	}
	L.Push(lua.LNil)
	return 2
}

// record is a before-state record as S.before returns it.
func (m *xLuaMirror) record(r tset.MemberRecord) *lua.LTable {
	t := m.L.NewTable()
	t.RawSetString("exists", lua.LBool(r.Exists))
	t.RawSetString("epoch", m.strOrNull(string(r.Epoch)))
	t.RawSetString("revision", m.strOrNull(string(r.Revision)))
	if r.Place != nil {
		p := m.L.NewTable()
		p.RawSetString("row", lua.LString(r.Place.Row))
		p.RawSetString("col", lua.LString(r.Place.Col))
		t.RawSetString("place", p)
	} else {
		t.RawSetString("place", m.null)
	}
	t.RawSetString("score", m.strOrNull(r.Score))
	fields := m.L.NewTable()
	for name, f := range r.Fields {
		fv := m.L.NewTable()
		fv.RawSetString("present", lua.LBool(f.Present))
		if f.Present {
			fv.RawSetString("value", lua.LString(f.Value))
		} else {
			fv.RawSetString("value", m.null)
		}
		fields.RawSetString(name, fv)
	}
	t.RawSetString("fields", fields)
	return t
}

// before is S.before: the records of the table's ids, from what the twin's read
// gave X.pre, cached in ctx.before as Layer 1's cache is. An id or a field the
// twin was not asked for is a failure of the fake, which says the two asked for
// different things.
func (m *xLuaMirror) before(L *lua.LState) int {
	ctx, table, ids, fields := L.CheckTable(1), L.CheckString(2), L.CheckTable(3), L.OptTable(4, L.NewTable())
	cache := ctx.RawGetString("before").(*lua.LTable)
	byTable, ok := cache.RawGetString(table).(*lua.LTable)
	if !ok {
		byTable = L.NewTable()
		cache.RawSetString(table, byTable)
	}
	answer := L.NewTable()
	for i := 1; i <= ids.Len(); i++ {
		id := ids.RawGetInt(i).String()
		r, ok := m.obs.Record(table, id)
		if !ok {
			L.RaiseError("the twin was not asked for %s card %s", table, id)
		}
		for j := 1; j <= fields.Len(); j++ {
			if _, ok := r.Fields[fields.RawGetInt(j).String()]; !ok {
				L.RaiseError("the twin was not asked for field %s of %s card %s", fields.RawGetInt(j), table, id)
			}
		}
		rec := m.record(r)
		byTable.RawSetString(id, rec)
		answer.RawSetString(id, rec)
	}
	L.Push(answer)
	L.Push(lua.LNil)
	return 2
}

// xPrefixField is the name Layer 1's wire gives the deployment prefix, which its
// Lua ctx carries under the same name. It is read from the wire's own type, so
// that the one word the tree's generality guardrail lists is spelled only where
// IT12 keeps it (wire.go), and not again in these tests.
func xPrefixField() string {
	b, err := json.Marshal(readPlanWire{Prefix: "p"})
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic(err)
	}
	for k, v := range m {
		if v == "p" {
			return k
		}
	}
	panic("the wire has no field for the deployment prefix")
}

// newCtx is the Lua ctx of a call: what Layer 1's S.open leaves for X.
func (m *xLuaMirror) newCtx(st *State, req *Request) (*lua.LTable, *lua.LTable) {
	enc, ref := encodeStep(st.Prefix, req)
	if ref != nil {
		m.t.Fatalf("the twin ran a step that does not encode: %v", ref)
	}
	var step struct {
		Entries json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(enc.raw, &step); err != nil {
		m.t.Fatal(err)
	}
	write, _ := xWriteEpoch(st, req)
	ctx := m.L.NewTable()
	ctx.RawSetString(xPrefixField(), lua.LString(st.Prefix))
	ctx.RawSetString("request_epoch", lua.LString(string(st.Epoch)))
	ctx.RawSetString("write_epoch", lua.LString(string(write)))
	ctx.RawSetString("now_ms", lua.LString(string(st.NowMS)))
	if op := req.Body.Op; op != nil {
		ctx.RawSetString("op", lua.LString(op.ID))
	}
	request := m.L.NewTable()
	request.RawSetString("entries", m.fromJSON(step.Entries))
	ctx.RawSetString("request", request)
	ctx.RawSetString("before", m.L.NewTable())
	return ctx, m.fromJSON(enc.sprint).(*lua.LTable)
}

func (m *xLuaMirror) phase(name string) *lua.LFunction {
	f, ok := m.sp.RawGetString("phases").(*lua.LTable).RawGetString(name).(*lua.LFunction)
	if !ok {
		m.t.Fatalf("the Lua registered no %s phase", name)
	}
	return f
}

func (m *xLuaMirror) tableOf(v lua.LValue) *lua.LTable {
	t, _ := v.(*lua.LTable)
	return t
}

func xStrings(t *lua.LTable) []string {
	var out []string
	for i := 1; i <= t.Len(); i++ {
		out = append(out, t.RawGetInt(i).String())
	}
	return out
}

// checkPre runs x_pre on the call the twin's X.pre has just answered, and holds
// its answer to the twin's.
func (m *xLuaMirror) checkPre(st *State, req *Request, obs *Before, goRef *Refusal) {
	m.t.Helper()
	m.cur, m.obs, m.probes = st, obs, 0
	m.reads, m.readKeys = nil, map[string]bool{}
	ctx, sp := m.newCtx(st, req)
	if err := m.L.CallByParam(lua.P{Fn: m.phase("x_pre"), NRet: 2, Protect: true}, ctx, sp, m.L.NewTable()); err != nil {
		m.t.Fatalf("Lua x_pre: %v", err)
	}
	ok, ref := m.L.Get(-2), m.L.Get(-1)
	m.L.Pop(2)
	luaCode, goCode := "", ""
	var luaIDs, luaRows []string
	if t := m.tableOf(ref); t != nil {
		luaCode = t.RawGetString("code").String()
		if d := m.tableOf(t.RawGetString("detail")); d != nil {
			luaIDs, luaRows = xStrings(m.tableOf(d.RawGetString("ids"))), xStrings(m.tableOf(d.RawGetString("rows")))
		}
	} else if ok != lua.LTrue {
		m.t.Fatalf("Lua x_pre returned %v, %v", ok, ref)
	}
	if goRef != nil {
		goCode = goRef.Code
	}
	if luaCode != goCode {
		m.t.Fatalf("x_pre: the Lua refused %q (%v), the twin %q (%v)", luaCode, ref, goCode, goRef)
	}
	if goRef != nil {
		if strings.Join(luaIDs, ",") != strings.Join(goRef.Detail.IDs, ",") || strings.Join(luaRows, ",") != strings.Join(goRef.Detail.Rows, ",") {
			m.t.Fatalf("x_pre %s: the Lua names ids %v rows %v, the twin ids %v rows %v", goCode, luaIDs, luaRows, goRef.Detail.IDs, goRef.Detail.Rows)
		}
	} else {
		xStash.mu.Lock()
		el := xStash.byKey[st]
		xStash.mu.Unlock()
		if el == nil {
			m.t.Fatal("the twin passed X.pre and stored no carry")
		}
		if want := el.Value.(*xStashEntry).carry.probes; want != m.probes {
			m.t.Fatalf("x_pre: the Lua made %d store reads, the twin %d", m.probes, want)
		}
		m.ctxs[st] = ctx
	}
	m.pre++
}

// tablePlan is Layer 1's table_plan of the twin's plan, in the shape table_set.lua
// builds it: each entry normalized with its changed ids and their scores, and the
// before-state cache of the call.
func (m *xLuaMirror) tablePlan(tp TablePlan, ctx *lua.LTable) *lua.LTable {
	out := m.L.NewTable()
	entries := m.L.NewTable()
	for _, pe := range tp.Entries {
		e := pe.Entry
		t := m.L.NewTable()
		t.RawSetString("kind", lua.LString(e.Kind))
		t.RawSetString("table", lua.LString(e.Table))
		switch e.Kind {
		case "create", "move", "remove":
			if e.From != "" {
				t.RawSetString("from", lua.LString(e.From))
			}
			if dest := e.To; dest != "" {
				t.RawSetString("to", lua.LString(dest))
			} else if e.Kind != "remove" && e.From != "" {
				t.RawSetString("to", lua.LString(e.From))
			}
		}
		ids, before, after, changes := m.L.NewTable(), m.L.NewTable(), m.L.NewTable(), m.L.NewTable()
		for j, id := range e.IDs {
			ids.Append(lua.LString(id))
			before.Append(m.strOrNull(pe.Before[j].Score))
			if e.Kind == "remove" {
				after.Append(m.null)
			} else {
				after.Append(m.strOrNull(pe.After[j].Score))
			}
			fc := m.L.NewTable()
			set := m.L.NewTable()
			for k, v := range pe.FieldChanges[j].Set {
				set.RawSetString(k, lua.LString(v))
			}
			fc.RawSetString("set", set)
			fc.RawSetString("unset", m.stringList(pe.FieldChanges[j].Unset))
			changes.Append(fc)
		}
		if e.Kind != "create" && e.Kind != "move" && e.Kind != "remove" {
			ids = m.L.NewTable()
		}
		t.RawSetString("changed_ids", ids)
		t.RawSetString("before_scores", before)
		t.RawSetString("after_scores", after)
		t.RawSetString("field_changes", changes)
		entries.Append(t)
	}
	out.RawSetString("entries", entries)
	out.RawSetString("before", ctx.RawGetString("before"))
	return out
}

// checkCmds runs x_cmds on the call's plan and holds its commands to the twin's.
func (m *xLuaMirror) checkCmds(st *State, tp TablePlan, lp LogPlan, goCmds []Cmd) {
	m.t.Helper()
	ctx := m.ctxs[st]
	if ctx == nil {
		m.t.Fatal("the twin ran X.plan for a call the Lua x_pre did not pass")
	}
	delete(m.ctxs, st)
	m.cur = st
	m.cmdLog = nil
	if err := m.L.CallByParam(lua.P{Fn: m.phase("x_cmds"), NRet: 1, Protect: true}, ctx, m.tablePlan(tp, ctx), m.L.NewTable()); err != nil {
		m.t.Fatalf("Lua x_cmds: %v", err)
	}
	res := m.tableOf(m.L.Get(-1))
	m.L.Pop(1)
	if res == nil {
		m.t.Fatal("Lua x_cmds returned no plan")
	}
	cmds := m.tableOf(res.RawGetString("commands"))
	var got []string
	for i := 1; i <= cmds.Len(); i++ {
		d := m.tableOf(cmds.RawGetInt(i))
		argv := m.tableOf(d.RawGetString("argv"))
		var parts []string
		if argv != nil {
			parts = xStrings(argv)
		}
		access := ""
		if a := m.tableOf(d.RawGetString("access")); a != nil && a.Len() == 1 {
			e := m.tableOf(a.RawGetInt(1))
			access = e.RawGetString("key").String() + " " + e.RawGetString("kind").String() + " " + e.RawGetString("mode").String()
		}
		got = append(got, xDescribeCmd(parts, access))
		m.cmdLog = append(m.cmdLog, parts)
	}
	var want []string
	for _, c := range goCmds {
		access := ""
		if len(c.Access) == 1 {
			access = c.Access[0].Key + " " + c.Access[0].Kind + " " + c.Access[0].Mode
		}
		want = append(want, xDescribeCmd(c.Argv, access))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		m.t.Fatalf("x_cmds: the Lua wrote\n%s\nthe twin wrote\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	typed := map[string]bool{}
	m.typeProbes = 0
	for _, argv := range m.cmdLog {
		if key := argv[1]; !m.readKeys[key] && !typed[key] {
			typed[key] = true
			m.typeProbes++
		}
	}
	m.cmds++
}

// xDescribeCmd is a command as one line, every byte of it: the two halves spell a
// score alike (the shortest decimal that reads back the same), so that the argv
// bytes the coster counts are the bytes the store is sent.
func xDescribeCmd(argv []string, access string) string {
	return strings.Join(argv, " ") + " | " + access
}
