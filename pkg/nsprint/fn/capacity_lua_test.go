package fn

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// fakeStore is an in-process stand-in for the Redis commands capacity.lua
// calls: strings, hashes and sets in maps, a fixed TIME, and XADD that keeps
// nothing. It runs the file's own Lua under gopher-lua (Lua 5.1, the Redis
// dialect), so the capacity rules are tested with no server, socket or clock.
type fakeStore struct {
	strs   map[string]string
	hashes map[string]map[string]string
	sets   map[string]map[string]bool
	funcs  map[string]*lua.LFunction
	L      *lua.LState
}

func newFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	s := &fakeStore{strs: map[string]string{}, hashes: map[string]map[string]string{}, sets: map[string]map[string]bool{}, funcs: map[string]*lua.LFunction{}}
	L := lua.NewState()
	t.Cleanup(L.Close)
	s.L = L
	r := L.NewTable()
	L.SetField(r, "call", L.NewFunction(s.call))
	L.SetField(r, "register_function", L.NewFunction(s.register))
	L.SetField(r, "acl_check_cmd", L.NewFunction(func(L *lua.LState) int { L.Push(lua.LTrue); return 1 }))
	L.SetField(r, "error_reply", L.NewFunction(func(L *lua.LState) int {
		e := L.NewTable()
		L.SetField(e, "err", L.Get(1))
		L.Push(e)
		return 1
	}))
	L.SetGlobal("redis", r)
	code, err := sources.ReadFile("lua/capacity.lua")
	require.NoError(t, err)
	require.NoError(t, L.DoString(Prelude+"do\n"+string(code)+"\nend\n"), "capacity.lua does not load under the fake")
	return s
}

func (s *fakeStore) hash(k string) map[string]string {
	if s.hashes[k] == nil {
		s.hashes[k] = map[string]string{}
	}
	return s.hashes[k]
}

func (s *fakeStore) set(k string) map[string]bool {
	if s.sets[k] == nil {
		s.sets[k] = map[string]bool{}
	}
	return s.sets[k]
}

func (s *fakeStore) register(L *lua.LState) int {
	if tbl, ok := L.Get(1).(*lua.LTable); ok {
		s.funcs[tbl.RawGetString("function_name").String()] = tbl.RawGetString("callback").(*lua.LFunction)
		return 0
	}
	s.funcs[L.CheckString(1)] = L.CheckFunction(2)
	return 0
}

func (s *fakeStore) call(L *lua.LState) int {
	cmd := strings.ToUpper(L.CheckString(1))
	arg := func(i int) string { return L.Get(i).String() }
	n := L.GetTop()
	str := func(v string, ok bool) lua.LValue {
		if !ok {
			return lua.LFalse
		}
		return lua.LString(v)
	}
	switch cmd {
	case "TIME":
		t := L.NewTable()
		t.Append(lua.LString("1790000000"))
		t.Append(lua.LString("0"))
		L.Push(t)
	case "XADD":
		L.Push(lua.LString("1-0"))
	case "GET":
		v, ok := s.strs[arg(2)]
		L.Push(str(v, ok))
	case "DEL":
		for i := 2; i <= n; i++ {
			delete(s.strs, arg(i))
			delete(s.hashes, arg(i))
			delete(s.sets, arg(i))
		}
		L.Push(lua.LNumber(1))
	case "EXISTS":
		_, a := s.strs[arg(2)]
		_, b := s.hashes[arg(2)]
		_, c := s.sets[arg(2)]
		if a || b || c {
			L.Push(lua.LNumber(1))
		} else {
			L.Push(lua.LNumber(0))
		}
	case "TYPE":
		t := L.NewTable()
		kind := "none"
		if _, ok := s.hashes[arg(2)]; ok {
			kind = "hash"
		} else if _, ok := s.sets[arg(2)]; ok {
			kind = "set"
		} else if _, ok := s.strs[arg(2)]; ok {
			kind = "string"
		}
		L.SetField(t, "ok", lua.LString(kind))
		L.Push(t)
	case "HGET":
		h, ok := s.hashes[arg(2)]
		v, ok2 := h[arg(3)]
		L.Push(str(v, ok && ok2))
	case "HMGET":
		t := L.NewTable()
		h := s.hashes[arg(2)]
		for i := 3; i <= n; i++ {
			v, ok := h[arg(i)]
			t.Append(str(v, ok))
		}
		L.Push(t)
	case "HSET":
		h := s.hash(arg(2))
		for i := 3; i+1 <= n; i += 2 {
			h[arg(i)] = arg(i + 1)
		}
		L.Push(lua.LNumber(1))
	case "HDEL":
		h := s.hash(arg(2))
		for i := 3; i <= n; i++ {
			delete(h, arg(i))
		}
		L.Push(lua.LNumber(1))
	case "HEXISTS":
		_, ok := s.hashes[arg(2)][arg(3)]
		if ok {
			L.Push(lua.LNumber(1))
		} else {
			L.Push(lua.LNumber(0))
		}
	case "SADD":
		for i := 3; i <= n; i++ {
			s.set(arg(2))[arg(i)] = true
		}
		L.Push(lua.LNumber(1))
	case "SISMEMBER":
		if s.sets[arg(2)][arg(3)] {
			L.Push(lua.LNumber(1))
		} else {
			L.Push(lua.LNumber(0))
		}
	case "SMEMBERS":
		t := L.NewTable()
		var names []string
		for m := range s.sets[arg(2)] {
			names = append(names, m)
		}
		sort.Strings(names)
		for _, m := range names {
			t.Append(lua.LString(m))
		}
		L.Push(t)
	default:
		L.RaiseError("fake store: no command %s", cmd)
		return 0
	}
	return 1
}

// fcall runs one registered function with string args and returns its reply
// as words.
func (s *fakeStore) fcall(t *testing.T, name string, args ...string) []string {
	t.Helper()
	f, ok := s.funcs[name]
	require.True(t, ok, "%s is not registered", name)
	argv := s.L.NewTable()
	for _, a := range args {
		argv.Append(lua.LString(a))
	}
	require.NoError(t, s.L.CallByParam(lua.P{Fn: f, NRet: 1, Protect: true}, s.L.NewTable(), argv))
	ret := s.L.Get(-1)
	s.L.Pop(1)
	tbl, ok := ret.(*lua.LTable)
	require.True(t, ok, "%s returned %v, not a table", name, ret)
	var words []string
	tbl.ForEach(func(_, v lua.LValue) { words = append(words, v.String()) })
	return words
}

func (s *fakeStore) ceiling(m string, slots int) {
	s.hash("machine:" + m + ":ceiling")["slots"] = strconv.Itoa(slots)
}

// TestABenchStillMeetsItsCeiling: the capacity rules a bench had stay. A bench
// on a machine with no ceiling is NOCEILING; a share over the ceiling is
// CEILING; a share within it is written.
func TestABenchStillMeetsItsCeiling(t *testing.T) {
	t.Parallel()

	s := newFakeStore(t)
	require.Equal(t, "NOCEILING", s.fcall(t, "ns_capacity_desired", "bench", "b1", "4", "m1", "a", "i")[0])
	s.ceiling("m1", 4)
	got := s.fcall(t, "ns_capacity_desired", "bench", "b1", "8", "m1", "a", "i")
	require.Equal(t, []string{"CEILING", "m1", "8", "4"}, got)
	require.Equal(t, "SET", s.fcall(t, "ns_capacity_desired", "bench", "b1", "4", "m1", "a", "i")[0])
	require.Equal(t, "4", s.hashes["bench:b1:desired"]["slots"])
}

// TestAFriendIsNeverRefusedForCapacityAtAnyWidth: a friend's desired slots are
// her width, recorded with her machine; no ceiling, a ceiling of one, a
// machine named "-", and every width from 1 up are written, never CEILING or
// NOCEILING. A machine's ceiling is never refused because of friends.
func TestAFriendIsNeverRefusedForCapacityAtAnyWidth(t *testing.T) {
	t.Parallel()

	s := newFakeStore(t)
	for _, machine := range []string{"m1", "-"} {
		for _, w := range []int{1, 16, 64, 1000} {
			got := s.fcall(t, "ns_capacity_desired", "friend", "ada", strconv.Itoa(w), machine, "a", "i")
			require.Contains(t, []string{"SET", "SAME"}, got[0], "friend at width %d on %s: %v", w, machine, got)
			require.Equal(t, strconv.Itoa(w), s.hashes["friend:ada:desired"]["slots"], "the desired slots are the width")
			require.Equal(t, machine, s.hashes["friend:ada:desired"]["machine"])
		}
		s.ceiling("m1", 1) // the second machine pass runs with a ceiling of one on m1
	}
	got := s.fcall(t, "ns_capacity_desired", "friend", "bo", "16", "m1", "a", "i")
	require.Equal(t, "SET", got[0], "a second friend at 16 on a one-slot machine: %v", got)
	require.True(t, s.sets["friends"]["ada"] && s.sets["friends"]["bo"], "both friends are registered: %v", s.sets["friends"])

	// The machine's own row: its slots below its friends' widths is written.
	got = s.fcall(t, "ns_capacity_machine", "m1", "1", "", "", "a", "i", "", "")
	require.Equal(t, "SET", got[0], "a machine was refused because of friends: %v", got)
}

// TestASprintPlanChecksBenchRowsOnly: a plan's ceiling rule counts bench rows
// only. Friend rows of any width on a one-slot machine, or on a machine with
// no ceiling at all, are applied; a bench row over its machine's ceiling is
// CEILING and a bench row on a machine with none is NOCEILING.
func TestASprintPlanChecksBenchRowsOnly(t *testing.T) {
	t.Parallel()

	plan := func(s *fakeStore, sha string, rows ...[4]string) []string {
		args := []string{"s1", sha, "{}", "rowan", "r", "0", "0", "0", "0", strconv.Itoa(len(rows))}
		for _, r := range rows {
			args = append(args, r[:]...)
		}
		return s.fcall(t, "ns_sprint_plan", args...)
	}
	setup := func(t *testing.T) *fakeStore {
		s := newFakeStore(t)
		s.set("friends")["ada"] = true
		s.set("friends")["bo"] = true
		s.set("benches")["b1"] = true
		s.ceiling("m1", 1)
		return s
	}

	s := setup(t)
	got := plan(s, "sha1", [4]string{"friend", "ada", "m1", "16"}, [4]string{"friend", "bo", "nomachine", "64"}, [4]string{"bench", "b1", "m1", "1"})
	require.Equal(t, "APPLIED", got[0], "friend rows were checked against a machine: %v", got)
	require.Equal(t, "16", s.hashes["friend:ada:desired"]["slots"])
	require.Equal(t, "64", s.hashes["friend:bo:desired"]["slots"])

	s = setup(t)
	got = plan(s, "sha2", [4]string{"friend", "ada", "m1", "16"}, [4]string{"bench", "b1", "m1", "2"})
	require.Equal(t, []string{"CEILING", "m1", "2", "1"}, got, "a bench row over its ceiling")

	s = setup(t)
	got = plan(s, "sha3", [4]string{"bench", "b1", "nomachine", "1"})
	require.Equal(t, []string{"NOCEILING", "nomachine"}, got, "a bench row on a machine with no ceiling")
	_, wrote := s.hashes["bench:b1:desired"]
	require.False(t, wrote, "a refused plan wrote a row")
}

// TestTheFakeStoreRefusesAnUnknownCommand keeps the fake honest: a command the
// fake does not model fails the call rather than answering something.
func TestTheFakeStoreRefusesAnUnknownCommand(t *testing.T) {
	t.Parallel()

	s := newFakeStore(t)
	err := s.L.DoString(`redis.call('ZADD', 'k', 1, 'm')`)
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("no command %s", "ZADD"))
}
