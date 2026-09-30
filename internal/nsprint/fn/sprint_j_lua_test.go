package fn

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// J's Lua half (sprint_j.lua, item IT15) is written to the interfaces of the
// sprint's core and of Layer 1, and no store loads it before gate G0. Here it is
// parsed, checked for the names Redis lets a function read, loaded with the real
// core, and run under gopher-lua over the golden vectors that hold the twin
// (internal/sprint/sprintfn, twin_j_vectors_test.go), against stubs of Layer 1's
// S. What the stubs stand in for (the typed reads, the write descriptors, the
// limits) is not tested here; that waits for G0.

const (
	sprintJLua     = "lua/sprint_j.lua"
	sprintJVectors = "../../sprint/sprintfn/testdata/j_vectors.json"
)

func sprintJSource(t *testing.T) string {
	t.Helper()
	b, err := sources.ReadFile(sprintJLua)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSprintJLuaParses: sprint_j.lua is Lua in Redis's dialect (5.1, as gopher-lua
// parses and compiles it), and opens with the NS.tset_profile guard, so the legacy
// library, which globs lua/*.lua, holds it inert.
func TestSprintJLuaParses(t *testing.T) {
	t.Parallel()
	src := sprintJSource(t)
	chunk, err := parse.Parse(strings.NewReader(src), sprintJLua)
	if err != nil {
		t.Fatalf("parse %s: %v", sprintJLua, err)
	}
	if _, err := lua.Compile(chunk, sprintJLua); err != nil {
		t.Fatalf("compile %s: %v", sprintJLua, err)
	}
	first := ""
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			first = line
			break
		}
	}
	if first != "if NS.tset_profile then" {
		t.Errorf("%s first statement is %q, want the tset profile guard", sprintJLua, first)
	}
}

// TestSprintJLuaReadsOnlyAllowedNames: the free names sprint_j.lua reads are the
// Redis Function environment's and the builtins the library uses, and it writes
// no global (Redis refuses both). The library's own cross-file test holds every
// file to this and to the order of NS fields, and fails at this base for the
// sprint files' reads of NS.tset_profile and NS.tset, which resolve at call time
// (errata 2, item 8); this holds J's file to the rest.
func TestSprintJLuaReadsOnlyAllowedNames(t *testing.T) {
	t.Parallel()
	reads, writes := freeNames(t, sprintJLua, sprintJSource(t))
	for _, n := range reads {
		if !allowedGlobals[n] {
			t.Errorf("%s reads free name %q", sprintJLua, n)
		}
	}
	if len(writes) != 0 {
		t.Errorf("%s assigns globals %v", sprintJLua, writes)
	}
	// It reads these NS fields and no others: the profile guard, the core's
	// registry, and Layer 1's S at call time.
	code := luaComment.ReplaceAllString(sprintJSource(t), "")
	seen := map[string]bool{}
	for _, m := range nsRead.FindAllStringSubmatch(code, -1) {
		seen[m[1]] = true
	}
	var got []string
	for n := range seen {
		got = append(got, n)
	}
	sort.Strings(got)
	if want := []string{"SP", "tset", "tset_profile"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%s reads NS fields %v, want %v", sprintJLua, got, want)
	}
}

// luaBase is the environment sprint_j.lua loads into: NS with the profile set,
// and the real core, which it registers its two phases with.
func newJLua(t *testing.T, profile bool) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	if profile {
		if err := L.DoString("NS = {tset_profile = 'sprint'}"); err != nil {
			t.Fatal(err)
		}
		core, err := sources.ReadFile("lua/sprint_00_core.lua")
		if err != nil {
			t.Fatal(err)
		}
		if err := L.DoString(string(core)); err != nil {
			t.Fatalf("core: %v", err)
		}
	} else if err := L.DoString("NS = {}"); err != nil {
		t.Fatal(err)
	}
	return L
}

// TestSprintJLuaIsInertWithoutTheProfile: loaded where NS.tset_profile is not
// set, as in the legacy library, the file defines nothing.
func TestSprintJLuaIsInertWithoutTheProfile(t *testing.T) {
	t.Parallel()
	L := newJLua(t, false)
	if err := L.DoString(sprintJSource(t)); err != nil {
		t.Fatal(err)
	}
	if ns := L.GetGlobal("NS").(*lua.LTable); ns.RawGetString("SP") != lua.LNil {
		t.Fatal("the file defined NS.SP without the profile")
	}
}

// TestSprintJLuaRegistersItsPhases: after the core, the file defines NS.SP.j_decide
// and NS.SP.j_cmds and registers them as the core's two J phases through its own
// registry, which refuses a phase registered twice; and it registers nothing
// else (the before asks, and the parts, are other items').
func TestSprintJLuaRegistersItsPhases(t *testing.T) {
	t.Parallel()
	L := newJLua(t, true)
	if err := L.DoString(sprintJSource(t)); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(`
		local SP = NS.SP
		assert(type(SP.j_decide) == 'function' and type(SP.j_cmds) == 'function')
		assert(SP.phases.j_decide == SP.j_decide and SP.phases.j_cmds == SP.j_cmds)
		local n = 0
		for _ in pairs(SP.phases) do n = n + 1 end
		assert(n == 2, 'registered ' .. n .. ' phases')
		assert(next(SP.parts) == nil and next(SP.queries) == nil)
		assert(SP.j_judgments['cannot ask'] == true and SP.j_judgments['the sprint is done'] == false)
	`); err != nil {
		t.Fatal(err)
	}
	// A second load's registrations are refused: recorded (in Redis the load
	// stops on sprint_registration_refused), and the first phases stay.
	if err := L.DoString(`J_FIRST = NS.SP.phases.j_decide`); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(sprintJSource(t)); err != nil {
		t.Fatalf("a second load raised: %v", err)
	}
	if err := L.DoString(`
		local SP = NS.SP
		assert(#SP.refused == 2, 'refused ' .. #SP.refused)
		assert(string.find(SP.refused[1], 'registered twice', 1, true), SP.refused[1])
		assert(SP.phases.j_decide == J_FIRST, 'a second load replaced the phase')
	`); err != nil {
		t.Fatalf("a second load registered the phases again: %v", err)
	}
}

// The vectors, as the Lua half reads them: the same file as the twin's, in the
// fields this side needs.
type jvKey struct {
	Hash map[string]string `json:"hash"`
	ZSet map[string]string `json:"zset"`
	List []string          `json:"list"`
}

type jvRecord struct {
	Table  string            `json:"table"`
	ID     string            `json:"id"`
	Row    string            `json:"row"`
	Col    string            `json:"col"`
	Fields map[string]string `json:"fields"`
}

type jvEntry struct {
	Kind  string              `json:"kind"`
	Table string              `json:"table"`
	From  string              `json:"from"`
	To    string              `json:"to"`
	IDs   []string            `json:"ids"`
	Set   map[string]string   `json:"set"`
	Unset []string            `json:"unset"`
	Each  []map[string]string `json:"each"`
}

type jvNote struct {
	About []string       `json:"about"`
	Meta  map[string]any `json:"meta"`
}

type jvExpect struct {
	Refusal  string     `json:"refusal"`
	Notes    []jvNote   `json:"notes"`
	Commands [][]string `json:"commands"`
	// Probes is how many typed reads the pre stage made: the readcmd calls the
	// stub counted, held to what the twin's JCost counts for the same vector.
	Probes int `json:"probes,omitempty"`
	// Reads is each typed read the pre stage made, in order, for the tests of the
	// bounds a vector does not reach. It is not part of a vector's expectation.
	Reads []jvRead `json:"-"`
}

// jvRead is one typed read the stub saw: the command, its key, and how many
// arguments it had, the command and the key among them.
type jvRead struct {
	Cmd, Key string
	Argc     int
}

type jvVector struct {
	Name     string                       `json:"name"`
	Epoch    string                       `json:"epoch"`
	NowMS    string                       `json:"now_ms"`
	Keys     map[string]jvKey             `json:"keys"`
	Records  []jvRecord                   `json:"records"`
	Entries  []jvEntry                    `json:"entries"`
	Requests []map[string]json.RawMessage `json:"requests"`
	NoteSeqs []string                     `json:"note_seqs"`
	Expect   *jvExpect                    `json:"expect"`
}

// jStubs is Layer 1's S as far as J calls it, over a keyspace and before-state
// the test sets in T: typed reads that refuse WRONGTYPE for a key of another
// type and answer as Redis does (false for an absent field), write descriptors,
// the before-state, and the refusal and array constructors.
const jStubs = `
local S = {json = {encode = json_encode}}
function S.array() return {} end
function S.refuse(code, detail) return {status = 'refused', code = code, detail = detail} end
function S.uint(s) return type(s) == 'string' and string.match(s, '^[0-9]+$') ~= nil end
function S.writecmd(ctx, argv, access)
  for _, a in ipairs(argv) do assert(type(a) == 'string', 'a command argument is not a string') end
  return {argv = argv, access = access}
end
function S.before(ctx, t, ids, fields)
  local out = {}
  for _, id in ipairs(ids) do
    local r = (T.records[t] or {})[id]
    if r then
      local f = {}
      for _, name in ipairs(fields) do
        local v = r.fields and r.fields[name]
        if v ~= nil then f[name] = {present = true, value = v} else f[name] = {present = false, value = false} end
      end
      out[id] = {exists = true, place = {row = r.row, col = r.col}, fields = f}
    else
      out[id] = {exists = false, place = false, fields = {}}
    end
  end
  return out, nil
end
function S.readcmd(ctx, d, reserve, probe)
  T.probes = T.probes + 1
  local a = d.argv
  local cmd, key = a[1], a[2]
  T.reads[#T.reads + 1] = {cmd = cmd, key = key, argc = #a}
  local kind = d.access[1] and d.access[1].kind
  local v = T.keys[key]
  if v and kind and v.kind ~= kind then return nil, S.refuse('WRONGTYPE', {}) end
  if cmd == 'HGET' then
    local x = v and v.hash[a[3]]
    if x == nil then return false, nil end
    return x, nil
  elseif cmd == 'HMGET' then
    local out = {}
    for i = 3, #a do
      local x = v and v.hash[a[i]]
      if x == nil then out[#out + 1] = false else out[#out + 1] = x end
    end
    return out, nil
  elseif cmd == 'HLEN' then
    local n = 0
    if v then for _ in pairs(v.hash) do n = n + 1 end end
    return n, nil
  elseif cmd == 'HKEYS' then
    local out = {}
    if v then for k in pairs(v.hash) do out[#out + 1] = k end end
    table.sort(out)
    return out, nil
  elseif cmd == 'ZSCORE' then
    local x = v and v.zset[a[3]]
    if x == nil then return false, nil end
    return x, nil
  end
  error('stub: no ' .. tostring(cmd))
end
NS.tset = S
`

// runJLua is what the Lua half makes of a vector.
func runJLua(t *testing.T, v jvVector) jvExpect {
	t.Helper()
	L := newJLua(t, true)
	L.SetGlobal("json_encode", L.NewFunction(func(L *lua.LState) int {
		b, err := json.Marshal(goValue(L.CheckAny(1)))
		if err != nil {
			L.RaiseError("encode: %v", err)
		}
		L.Push(lua.LString(b))
		return 1
	}))
	if err := L.DoString(sprintJSource(t)); err != nil {
		t.Fatalf("%s: %v", v.Name, err)
	}
	keys := map[string]any{}
	for k, val := range v.Keys {
		switch {
		case val.Hash != nil:
			h := map[string]any{}
			for f, x := range val.Hash {
				h[f] = x
			}
			keys[k] = map[string]any{"kind": "hash", "hash": h}
		case val.ZSet != nil:
			z := map[string]any{}
			for m, score := range val.ZSet {
				z[m] = score // a score is read as a string, as Redis replies with one
			}
			keys[k] = map[string]any{"kind": "zset", "zset": z}
		case val.List != nil:
			keys[k] = map[string]any{"kind": "list"}
		}
	}
	records := map[string]any{}
	for _, r := range v.Records {
		fields := map[string]any{}
		for f, x := range r.Fields {
			fields[f] = x
		}
		tab, _ := records[r.Table].(map[string]any)
		if tab == nil {
			tab = map[string]any{}
			records[r.Table] = tab
		}
		tab[r.ID] = map[string]any{"row": r.Row, "col": r.Col, "fields": fields}
	}
	T := L.NewTable()
	T.RawSetString("keys", luaValue(L, keys))
	T.RawSetString("records", luaValue(L, records))
	T.RawSetString("probes", lua.LNumber(0))
	T.RawSetString("reads", L.NewTable())
	L.SetGlobal("T", T)
	// redis.sha1hex, which Redis gives a function and J digests a note's text with.
	redisT := L.NewTable()
	redisT.RawSetString("sha1hex", L.NewFunction(func(L *lua.LState) int {
		sum := sha1.Sum([]byte(L.CheckString(1)))
		L.Push(lua.LString(hex.EncodeToString(sum[:])))
		return 1
	}))
	L.SetGlobal("redis", redisT)
	if err := L.DoString(jStubs); err != nil {
		t.Fatalf("%s: stubs: %v", v.Name, err)
	}

	entries := []any{}
	for _, e := range v.Entries {
		m := map[string]any{"kind": e.Kind, "t": e.Table, "ids": jToAny(e.IDs)}
		if e.From != "" {
			m["from"] = e.From
		}
		if e.To != "" {
			m["to"] = e.To
		}
		if e.Set != nil {
			s := map[string]any{}
			for f, x := range e.Set {
				s[f] = x
			}
			m["set"] = s
		}
		if e.Unset != nil {
			m["unset"] = jToAny(e.Unset)
		}
		if e.Each != nil {
			each := []any{}
			for _, x := range e.Each {
				o := map[string]any{}
				for f, y := range x {
					o[f] = y
				}
				each = append(each, o)
			}
			m["each"] = each
		}
		entries = append(entries, m)
	}
	reqs := []any{}
	for _, r := range v.Requests {
		m := map[string]any{}
		for k, raw := range r {
			var x any
			if err := json.Unmarshal(raw, &x); err != nil {
				t.Fatal(err)
			}
			m[k] = x
		}
		reqs = append(reqs, m)
	}
	seqs := jToAny(v.NoteSeqs)
	if err := L.DoString("SEQS = nil"); err != nil {
		t.Fatal(err)
	}
	// A step that advances writes at the next epoch (L1 1.2): Layer 1 gives the
	// Lua half that as ctx.write_epoch, where the twin reads the advance entry.
	writeEpoch := v.Epoch
	for _, e := range v.Entries {
		if e.Kind == "advance" {
			n, err := strconv.ParseUint(v.Epoch, 10, 64)
			if err != nil {
				t.Fatalf("%s: epoch %q: %v", v.Name, v.Epoch, err)
			}
			writeEpoch = strconv.FormatUint(n+1, 10)
		}
	}
	ctx := luaValue(L, map[string]any{"space": "t:", "write_epoch": writeEpoch, "now_ms": v.NowMS,
		"request": map[string]any{"entries": entries}})
	ctx.(*lua.LTable).RawSetString("notes", L.NewTable())
	L.SetGlobal("CTX", ctx)
	L.SetGlobal("REQS", luaValue(L, reqs))
	L.SetGlobal("SEQS", luaValue(L, seqs))
	if err := L.DoString(`
		local notes, jp, err = NS.SP.j_decide(CTX, REQS, {})
		RESULT = {refusal = '', notes = {}, commands = {}, probes = 0}
		if err then
			RESULT.refusal = err.code
		else
			RESULT.probes = T.probes
			RESULT.reads = T.reads
			for i, n in ipairs(notes) do RESULT.notes[i] = n end
			local plan = NS.SP.j_cmds(CTX, jp, {note_seqs = SEQS})
			for i, c in ipairs(plan.commands) do RESULT.commands[i] = c.argv end
		end
	`); err != nil {
		t.Fatalf("%s: %v", v.Name, err)
	}
	res := L.GetGlobal("RESULT").(*lua.LTable)
	out := jvExpect{Refusal: lua.LVAsString(res.RawGetString("refusal")), Notes: []jvNote{}, Commands: [][]string{}}
	if p, ok := res.RawGetString("probes").(lua.LNumber); ok {
		out.Probes = int(p)
	}
	if reads, ok := res.RawGetString("reads").(*lua.LTable); ok {
		for i := 1; i <= reads.Len(); i++ {
			r := reads.RawGetInt(i).(*lua.LTable)
			argc, _ := r.RawGetString("argc").(lua.LNumber)
			out.Reads = append(out.Reads, jvRead{Cmd: lua.LVAsString(r.RawGetString("cmd")), Key: lua.LVAsString(r.RawGetString("key")), Argc: int(argc)})
		}
	}
	notes := res.RawGetString("notes").(*lua.LTable)
	for i := 1; i <= notes.Len(); i++ {
		el := goValue(notes.RawGetInt(i)).(map[string]any)
		b, err := json.Marshal(map[string]any{"about": el["about"], "meta": el["line"].(map[string]any)["meta"]})
		if err != nil {
			t.Fatal(err)
		}
		var n jvNote
		if err := json.Unmarshal(b, &n); err != nil {
			t.Fatal(err)
		}
		if kind := el["line"].(map[string]any)["kind"]; kind != "note" {
			t.Fatalf("%s: a note's line has kind %v, want note", v.Name, kind)
		}
		out.Notes = append(out.Notes, n)
	}
	cmds := res.RawGetString("commands").(*lua.LTable)
	for i := 1; i <= cmds.Len(); i++ {
		argv := cmds.RawGetInt(i).(*lua.LTable)
		var a []string
		for j := 1; j <= argv.Len(); j++ {
			a = append(a, argv.RawGetInt(j).String())
		}
		out.Commands = append(out.Commands, a)
	}
	return out
}

func jToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// jvNormalized passes an expectation through JSON so that what the file holds and
// what the code makes compare as the same kinds of value.
func jvNormalized(t *testing.T, e jvExpect) jvExpect {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var out jvExpect
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	// A refusal has no notes and no commands, whether the file says null or the
	// Lua says an empty list.
	if len(out.Notes) == 0 {
		out.Notes = nil
	}
	if len(out.Commands) == 0 {
		out.Commands = nil
	}
	return out
}

// TestSprintJLuaVectors: the Lua half, run under gopher-lua against stubs of
// Layer 1's S, makes exactly what the golden vectors expect, which the twin makes
// too: the same notes (about ids and meta), the same commands in the same order,
// the same refusal's code. A read of a key of another type is WRONGTYPE as the
// stub's typed read gives it; the real helper's own refusals wait for G0.
func TestSprintJLuaVectors(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sprintJVectors)
	if err != nil {
		t.Fatal(err)
	}
	var vs []jvVector
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) < 40 {
		t.Fatalf("%d vectors; want the twin's suite", len(vs))
	}
	for _, v := range vs {
		if v.Expect == nil {
			t.Fatalf("%s: no expectation", v.Name)
		}
		got, want := jvNormalized(t, runJLua(t, v)), jvNormalized(t, *v.Expect)
		if !reflect.DeepEqual(got, want) {
			gb, _ := json.MarshalIndent(got, "", " ")
			wb, _ := json.MarshalIndent(want, "", " ")
			t.Errorf("%s:\n got %s\nwant %s", v.Name, gb, wb)
		}
	}
}

// jvPieces is n elements in full pieces of 1,000 and the rest: the pieces the
// twin's JCmds makes (TestJPiecesAreAtMostAThousand holds it to the same).
func jvPieces(n int) []int {
	var out []int
	for ; n > 1000; n -= 1000 {
		out = append(out, 1000)
	}
	return append(out, n)
}

// jvElements is how many collection elements a command carries: a pair for ZADD
// and HSET, a member or a field for the others.
func jvElements(argv []string) int {
	if argv[0] == "ZADD" || argv[0] == "HSET" {
		return (len(argv) - 2) / 2
	}
	return len(argv) - 2
}

// TestSprintJLuaPiecesAreAtMostAThousand: the Lua half writes askwait for a note
// of more than 1,000 primaries in pieces of at most 1,000 pairs, and takes more
// than 1,000 out in pieces of at most 1,000 members, and reads the quarantine of
// the primaries of an open in pieces of at most 1,000 fields, so that no command
// passes Layer 1's bound of 1,000 elements; the pieces are full, as the twin's
// are, and the reads are what the twin's JCost counts (a read of the field of
// each primary, one of the quarantine for each piece, and one of the clock). No
// golden vector is so large, so the cases are made here from the vector shape and
// held to what the twin's tests of the same sizes expect.
func TestSprintJLuaPiecesAreAtMostAThousand(t *testing.T) {
	t.Parallel()
	req := func(op string, subjects []string) map[string]json.RawMessage {
		ss, _ := json.Marshal(subjects)
		return map[string]json.RawMessage{"op": json.RawMessage(`"` + op + `"`), "type": json.RawMessage(`"cannot ask"`),
			"cause": json.RawMessage(`"c"`), "subjects": ss}
	}
	for _, n := range []int{1000, 1001, 2000} {
		subjects := make([]string, n)
		keys := map[string]jvKey{"t:sprint:jn@0": {Hash: map[string]string{"n1": strconv.Itoa(n)}}}
		for i := range subjects {
			subjects[i] = "p" + strconv.Itoa(i)
			keys["t:sprint:jopen:"+subjects[i]+"@0"] = jvKey{Hash: map[string]string{"cannot ask|c": "n1"}}
		}
		for _, tc := range []struct {
			name, command, askwait string
			v                      jvVector
		}{
			{"an open", "ZADD", "t:sprint:askwait@0", jvVector{Name: "open", Epoch: "0", NowMS: "1790000000000",
				Requests: []map[string]json.RawMessage{req("open", subjects)}, NoteSeqs: []string{"1"}}},
			{"a close", "ZREM", "t:sprint:askwait@0", jvVector{Name: "close", Epoch: "0", NowMS: "1790000000000", Keys: keys,
				Requests: []map[string]json.RawMessage{req("close", subjects)}, NoteSeqs: []string{"2"}}},
		} {
			got := runJLua(t, tc.v)
			if got.Refusal != "" {
				t.Fatalf("%s of %d: refused %s", tc.name, n, got.Refusal)
			}
			var quarantine []int
			for _, rd := range got.Reads {
				if f := rd.Argc - 2; rd.Cmd == "HMGET" && f > 1000 {
					t.Errorf("%s of %d: %s %s reads %d fields, over 1,000", tc.name, n, rd.Cmd, rd.Key, f)
				}
				if rd.Cmd == "HMGET" && rd.Key == "t:sprint:quarantine@0" {
					quarantine = append(quarantine, rd.Argc-2)
				}
			}
			if tc.name == "an open" {
				if want := jvPieces(n); !reflect.DeepEqual(quarantine, want) {
					t.Errorf("an open of %d: the quarantine read in pieces %v, want %v", n, quarantine, want)
				}
				// A read of the field of each primary, of the quarantine for each piece, and of the clock.
				if want := n + len(jvPieces(n)) + 1; got.Probes != want {
					t.Errorf("an open of %d: %d reads, want %d", n, got.Probes, want)
				}
			}
			var pieces []int
			for _, argv := range got.Commands {
				if e := jvElements(argv); e > 1000 {
					t.Fatalf("%s of %d: %s %s has %d elements, over 1,000", tc.name, n, argv[0], argv[1], e)
				}
				if argv[0] == tc.command && argv[1] == tc.askwait {
					pieces = append(pieces, jvElements(argv))
				}
			}
			if want := jvPieces(n); !reflect.DeepEqual(pieces, want) {
				t.Errorf("%s of %d: askwait in pieces %v, want %v", tc.name, n, pieces, want)
			}
		}
	}
}

// TestSprintLuaCoreRunsJOnEntries (errata 3: the lateness close runs on the
// composed write path): with J registered, ns_sprint_step calls it for a step
// that carries entries and no note request, since J closes the lateness
// judgment of each timed state the entries end; a step with neither entries nor
// a note request does not call it; and with J not registered a step of entries
// is served without it, as the traced phases of the core's own tests are.
func TestSprintLuaCoreRunsJOnEntries(t *testing.T) {
	t.Parallel()
	entries := "T.ctx = function() return {request = {entries = {{kind = 'move', t = 'fleet', ids = {'w1'}}}}, notes = {}} end"

	h := newLuaSprint(t)
	h.setup(nil)
	h.do(entries)
	h.step(`{"meta":{"verb":"take"}}`)
	want := []string{"open", "phase:before", "before", "before", "x_pre", "j_decide", "plan", "log", "x_cmds", "j_cmds", "prepare", "commit"}
	if !reflect.DeepEqual(h.trace, want) {
		t.Fatalf("a step of entries called\n got %v\nwant %v", h.trace, want)
	}

	bare := newLuaSprint(t)
	bare.setup(nil)
	bare.step(`{"meta":{"verb":"tick"}}`)
	if contains(bare.trace, "j_decide") || contains(bare.trace, "j_cmds") {
		t.Fatalf("a step with no entries and no note request called J: %v", bare.trace)
	}

	none := newLuaSprint(t)
	none.setup([]string{"j_decide", "j_cmds"})
	none.do(entries)
	reply := none.step(`{"meta":{"verb":"take"}}`)
	if contains(none.trace, "j_decide") || refusalCode(reply) != "" {
		t.Fatalf("a step of entries with J not registered: trace %v, reply %s", none.trace, reply)
	}
}
