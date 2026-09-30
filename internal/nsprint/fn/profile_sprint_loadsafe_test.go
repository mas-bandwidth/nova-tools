package fn

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// The sprint profile's load is safe when nothing it runs while FUNCTION LOAD
// runs the library's body reads a global Redis does not give a loading
// library. Fact F12 (facts_functional_test.go): at load the only global is
// redis; ipairs, pairs, type, tostring, error, assert, pcall, math, string,
// table, unpack and cjson exist only once a registered function runs. Two
// checks here, and the store's own in profile_sprint_load_functional_test.go:
// a static one over each sprint file's load scope, read from the compiler's
// own bytecode (so a local, a table key or a function body is never mistaken
// for a global read), and the whole library run in a sandbox that answers as
// Redis's load does.

// loadGlobalsAllowed are the globals a sprint file's load scope may read: NS
// (the prelude's local, a global only in a file compiled alone), redis, and
// the load's deliberate stop in the registry (sprint_00_core.lua).
var loadGlobalsAllowed = map[string]bool{"NS": true, "redis": true, "sprint_registration_refused": true}

// loadCalled are the functions the sprint files call while the library loads,
// by the line that defines each: the registry every file registers through,
// the two helpers it calls, and the queries' validator factory. Their own
// bodies are load scope; closures they return are not.
var loadCalled = map[string][]string{
	"lua/sprint_00_core.lua": {"function SP.part(", "function SP.query(", "function SP.phase(",
		"local function shown(", "local function refuse_registration("},
	"lua/sprint_queries.lua": {"function Q.validator("},
}

// loadScopeProblems lists every global the load scope of one sprint file
// reads or writes that loadGlobalsAllowed does not name, as "<file>:<line>
// reads|writes <name>": the file's main chunk (every statement outside a
// function body, in do and if blocks too) and the body of each function
// loadCalled names in it.
func loadScopeProblems(name, src string, called []string) ([]string, error) {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, err
	}
	main, err := lua.Compile(chunk, name)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(src, "\n")
	scope := []*lua.FunctionProto{main}
	for _, def := range called {
		found := false
		var walk func(p *lua.FunctionProto)
		walk = func(p *lua.FunctionProto) {
			for _, c := range p.FunctionPrototypes {
				if l := c.LineDefined; l >= 1 && l <= len(lines) && strings.Contains(lines[l-1], def) {
					scope = append(scope, c)
					found = true
				}
				walk(c)
			}
		}
		walk(main)
		if !found {
			return nil, fmt.Errorf("%s defines no %q, which the load calls", name, def)
		}
	}
	var out []string
	for _, p := range scope {
		for i, inst := range p.Code {
			op := int(inst >> 26) // gopher-lua opGetOpCode
			if op != lua.OP_GETGLOBAL && op != lua.OP_SETGLOBAL {
				continue
			}
			g := p.Constants[inst&0x3ffff].String() // Kst(Bx) is the name
			if loadGlobalsAllowed[g] {
				continue
			}
			verb := "reads"
			if op == lua.OP_SETGLOBAL {
				verb = "writes"
			}
			out = append(out, fmt.Sprintf("%s:%d %s %s", name, p.DbgSourcePositions[i], verb, g))
		}
	}
	sort.Strings(out)
	return out, nil
}

// TestSprintFilesTouchNoGlobalAtLoad: no sprint file's load scope reads or
// writes a standard global (F12).
func TestSprintFilesTouchNoGlobalAtLoad(t *testing.T) {
	t.Parallel()
	frags, err := SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frags {
		problems, err := loadScopeProblems(f.Name, f.Source, loadCalled[f.Name])
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range problems {
			t.Errorf("%s: a loading library has no such global (fact F12)", p)
		}
	}
}

// TestLoadScopeCheckSeesTheBrokenShape: the reversed witness. A loop over
// ipairs and a type call at the top level, in a do and an if block, and in a
// function the load calls, are each found; the same names in a function body
// the load does not call, as a table key, or as a local are not.
func TestLoadScopeCheckSeesTheBrokenShape(t *testing.T) {
	t.Parallel()
	src := `if NS.tset_profile then
do
  for _, x in ipairs({}) do end
  if true then local n = type(1) end
  local spec = {type = 1, ipairs = 2}
  local tostring = 3
  local y = tostring
  function NS.reg(v) return pairs(v) end
  function NS.later(v) return error(v) end
  pcall = 4
end
end`
	got, err := loadScopeProblems("fixture.lua", src, []string{"function NS.reg("})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fixture.lua:10 writes pcall", "fixture.lua:3 reads ipairs", "fixture.lua:4 reads type", "fixture.lua:8 reads pairs"}
	if !slices.Equal(got, want) {
		t.Fatalf("the check found %q, want %q", got, want)
	}
	if _, err := loadScopeProblems("fixture.lua", src, []string{"function NS.gone("}); err == nil {
		t.Fatal("a load-called function the file does not define passed")
	}
}

// loadSandbox runs source, a library as FUNCTION LOAD receives it, the way
// Redis runs a library's body (F12): the only global is redis, with
// register_function, and reading any other global raises "Script attempted
// to access nonexistent global variable" naming it; writing one raises too.
// The string metatable stays, as it does in Redis's Lua state. It returns
// the names registered, sorted, and the load's error.
func loadSandbox(t *testing.T, source string) ([]string, error) {
	t.Helper()
	L := lua.NewState()
	defer L.Close()
	g := L.G.Global
	var keys []lua.LValue
	g.ForEach(func(k, _ lua.LValue) { keys = append(keys, k) })
	for _, k := range keys {
		g.RawSet(k, lua.LNil)
	}
	var registered []string
	r := L.NewTable()
	r.RawSetString("register_function", L.NewFunction(func(L *lua.LState) int {
		switch a := L.Get(1).(type) {
		case lua.LString:
			registered = append(registered, string(a))
		case *lua.LTable:
			registered = append(registered, a.RawGetString("function_name").String())
		}
		return 0
	}))
	g.RawSetString("redis", r)
	mt := L.NewTable()
	mt.RawSetString("__index", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("Script attempted to access nonexistent global variable '%s'", L.Get(2).String())
		return 0
	}))
	mt.RawSetString("__newindex", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("Attempt to modify a readonly table: global '%s'", L.Get(2).String())
		return 0
	}))
	L.SetMetatable(g, mt)
	body := source
	if i := strings.IndexByte(body, '\n'); strings.HasPrefix(body, "#!") && i >= 0 {
		body = "\n" + body[i+1:] // the line numbers FUNCTION LOAD reports
	}
	err := L.DoString(body)
	sort.Strings(registered)
	return registered, err
}

// TestSprintProfileLoadsInTheLoadSandbox: the whole sprint profile, Layer 1
// and Layer 2 with it, loads as Redis loads a library, every function a
// registration calls included, and registers the sprint's two functions.
func TestSprintProfileLoadsInTheLoadSandbox(t *testing.T) {
	t.Parallel()
	src, err := TSetSource(TSetSprint)
	if err != nil {
		t.Fatal(err)
	}
	names, err := loadSandbox(t, src)
	if err != nil {
		t.Fatalf("the sprint profile does not load: %v", err)
	}
	for _, want := range append(append([]string(nil), TSetFunctions...), SprintFunctions...) {
		if !slices.Contains(names, want) {
			t.Errorf("the load registered %v, not %s", names, want)
		}
	}
}

// TestSprintLoadSandboxIsFaithful: the reversed witnesses. The sandbox refuses
// a library that reads ipairs at load, naming it, as Redis does (F12); and
// the sprint profile with a part registered twice stops its load on
// sprint_registration_refused.
func TestSprintLoadSandboxIsFaithful(t *testing.T) {
	t.Parallel()
	if _, err := loadSandbox(t, "#!lua name=f12\nfor _ in ipairs({}) do end\n"); err == nil ||
		!strings.Contains(err.Error(), "nonexistent global variable 'ipairs'") {
		t.Fatalf("a library reading ipairs at load: %v, want it refused naming ipairs", err)
	}
	src, err := TSetSource(TSetSprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadSandbox(t, src+DoubledSprintPart); err == nil || !strings.Contains(err.Error(), "'sprint_registration_refused'") {
		t.Fatalf("a profile registering a part twice: %v, want its load stopped on sprint_registration_refused", err)
	}
}
