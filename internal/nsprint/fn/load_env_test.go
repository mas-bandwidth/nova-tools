package fn

import (
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// A library's body runs at FUNCTION LOAD in an environment whose only global
// is `redis` (TestFactF12LoadTimeSandboxHasNoSetmetatable, the functional
// probe of it: ipairs, pairs, type, error, tostring, math, string, table and
// the rest are all "nonexistent global variable" there; a value's own string
// methods, ("x"):rep(2), work, being the string metatable's). The twin tests
// run the sprint's Lua under gopher-lua with every builtin present, so a
// fragment that names one at its top level, or in a function it calls while it
// loads (a registration), passes them and is refused by the store. This test
// runs the whole assembled source of each profile in that load environment, so
// the refusal shows at the unit tier.

// loadEnv is a Lua state standing for the load-time environment: no library
// global, the `redis` global with the two calls and the constants a load has,
// reads and writes of any other global refused as the store refuses them, and
// the string metatable kept so a string's own methods work.
func loadEnv(t *testing.T) (*lua.LState, *[]string) {
	t.Helper()
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	t.Cleanup(L.Close)
	L.Push(L.NewFunction(lua.OpenString))
	L.Push(lua.LString(lua.StringLibName))
	L.Call(1, 0)
	L.SetGlobal(lua.StringLibName, lua.LNil)
	var registered []string
	redis := L.NewTable()
	redis.RawSetString("register_function", L.NewFunction(func(L *lua.LState) int {
		if spec, ok := L.Get(1).(*lua.LTable); ok {
			registered = append(registered, spec.RawGetString("function_name").String())
		} else {
			registered = append(registered, L.Get(1).String())
		}
		return 0
	}))
	redis.RawSetString("log", L.NewFunction(func(L *lua.LState) int { return 0 }))
	L.SetGlobal("redis", redis)
	meta := L.NewTable()
	meta.RawSetString("__index", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("Script attempted to access nonexistent global variable '%s'", L.CheckString(2))
		return 0
	}))
	meta.RawSetString("__newindex", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("Attempt to modify a readonly table: global '%s'", L.CheckString(2))
		return 0
	}))
	L.SetMetatable(L.Get(lua.GlobalsIndex), meta)
	return L, &registered
}

// TestEveryProfileLoadsInTheLoadTimeEnvironment: the source of every profile
// runs to its end with only `redis` global, and registers functions.
func TestEveryProfileLoadsInTheLoadTimeEnvironment(t *testing.T) {
	t.Parallel()
	for _, profile := range []TSetProfile{TSetStandalone, TSetComposed, TSetSprint} {
		t.Run(string(profile), func(t *testing.T) {
			t.Parallel()
			src, err := TSetSource(profile)
			if err != nil {
				t.Skipf("the profile does not assemble: %v", err)
			}
			_, body, _ := strings.Cut(src, "\n") // the shebang line is the store's, not Lua's
			L, registered := loadEnv(t)
			if err := L.DoString(body); err != nil {
				t.Fatalf("the %s profile does not load: %v", profile, err)
			}
			if len(*registered) == 0 {
				t.Fatalf("the %s profile registered no function", profile)
			}
		})
	}
}
