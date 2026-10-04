package fn

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// A library's body runs at FUNCTION LOAD in an environment whose only global
// is `redis` (TestFactF12LoadTimeSandboxHasNoSetmetatable, the functional
// probe of it: ipairs, pairs, type, error, tostring, math, string, table and
// the rest are all "nonexistent global variable" there; a value's own string
// methods, ("x"):rep(2), work, being the string metatable's). A file that
// names one at its top level, or in a function it calls while it loads (a
// registration), is refused by the store. This test runs the whole assembled
// library in that load environment, so the refusal shows at the unit tier.

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

// TestLibraryLoadsInTheLoadTimeEnvironment: the assembled library runs to its
// end with only `redis` global, and the functions it registers there are
// exactly the ones redisfn reads out of its files (Spec().Registered()), which
// nova-redis's TestFnVerbsOnARedisServer expects on the store after fn load.
func TestLibraryLoadsInTheLoadTimeEnvironment(t *testing.T) {
	t.Parallel()
	src, err := Source()
	require.NoError(t, err)
	_, body, _ := strings.Cut(src, "\n") // the shebang line is the store's, not Lua's
	L, registered := loadEnv(t)
	require.NoError(t, L.DoString(body), "the library does not load")
	require.NotEmpty(t, *registered, "the library registered no function")
	funcs, err := Spec().Registered()
	require.NoError(t, err)
	var want []string
	for _, fn := range funcs {
		want = append(want, fn.Name)
	}
	slices.Sort(want)
	got := slices.Clone(*registered)
	slices.Sort(got)
	require.Equal(t, want, got, "the library registers %v at load; its files name %v", got, want)
}
