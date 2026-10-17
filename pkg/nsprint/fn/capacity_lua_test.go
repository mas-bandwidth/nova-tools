package fn

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// fakeStore is an in-process stand-in for the Redis commands the library's
// files call: strings, hashes, sets, sorted sets and streams in maps, a fixed
// TIME, WRONGTYPE for a key of another type (raised by call, returned by
// pcall), sha1hex, and cjson (fakeCJSON in table_lua_values_test.go). It runs
// a file's own Lua under gopher-lua (Lua 5.1, the Redis dialect), so the
// file's rules are tested with no server, socket or clock.
type fakeStore struct {
	strs    map[string]string
	hashes  map[string]map[string]string
	sets    map[string]map[string]bool
	zsets   map[string]map[string]float64
	streams map[string][][]string // each entry: its id, then its field, value pairs
	lastID  int
	decodes []string // every text cjson.decode was given, in order
	funcs   map[string]*lua.LFunction
	L       *lua.LState
}

func newFakeStore(t *testing.T) *fakeStore { return newFakeStoreOf(t, "lua/capacity.lua") }

// newFakeStoreOf loads one lua/ file, wrapped as the assembled library wraps
// it (the prelude, then the file in its own do-block).
func newFakeStoreOf(t *testing.T, file string) *fakeStore {
	t.Helper()
	s := &fakeStore{strs: map[string]string{}, hashes: map[string]map[string]string{}, sets: map[string]map[string]bool{},
		zsets: map[string]map[string]float64{}, streams: map[string][][]string{}, funcs: map[string]*lua.LFunction{}}
	L := lua.NewState()
	t.Cleanup(L.Close)
	s.L = L
	r := L.NewTable()
	L.SetField(r, "call", L.NewFunction(s.call))
	L.SetField(r, "pcall", L.NewFunction(s.pcall))
	L.SetField(r, "sha1hex", L.NewFunction(func(L *lua.LState) int {
		sum := sha1.Sum([]byte(L.CheckString(1)))
		L.Push(lua.LString(hex.EncodeToString(sum[:])))
		return 1
	}))
	L.SetField(r, "register_function", L.NewFunction(s.register))
	L.SetField(r, "acl_check_cmd", L.NewFunction(func(L *lua.LState) int { L.Push(lua.LTrue); return 1 }))
	L.SetField(r, "error_reply", L.NewFunction(func(L *lua.LState) int {
		e := L.NewTable()
		L.SetField(e, "err", L.Get(1))
		L.Push(e)
		return 1
	}))
	L.SetGlobal("redis", r)
	L.SetGlobal("cjson", fakeCJSON(L, &s.decodes))
	code, err := sources.ReadFile(file)
	require.NoError(t, err)
	require.NoError(t, L.DoString(Prelude+"do\n"+string(code)+"\nend\n"), "%s does not load under the fake", file)
	return s
}

// kind is the type of key as TYPE names it.
func (s *fakeStore) kind(k string) string {
	if _, ok := s.hashes[k]; ok {
		return "hash"
	} else if _, ok := s.sets[k]; ok {
		return "set"
	} else if _, ok := s.zsets[k]; ok {
		return "zset"
	} else if _, ok := s.streams[k]; ok {
		return "stream"
	} else if _, ok := s.strs[k]; ok {
		return "string"
	}
	return "none"
}

func (s *fakeStore) zset(k string) map[string]float64 {
	if s.zsets[k] == nil {
		s.zsets[k] = map[string]float64{}
	}
	return s.zsets[k]
}

// zrange is the sorted set's members in the store's order: by score, then by
// member.
func (s *fakeStore) zrange(k string) []string {
	z := s.zsets[k]
	names := make([]string, 0, len(z))
	for m := range z {
		names = append(names, m)
	}
	sort.Slice(names, func(i, j int) bool {
		if z[names[i]] != z[names[j]] {
			return z[names[i]] < z[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

// score is a sorted-set score as the store prints it.
func score(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// drop removes a container key the command left empty, as the store does.
func (s *fakeStore) drop(k string) {
	if h, ok := s.hashes[k]; ok && len(h) == 0 {
		delete(s.hashes, k)
	}
	if m, ok := s.sets[k]; ok && len(m) == 0 {
		delete(s.sets, k)
	}
	if z, ok := s.zsets[k]; ok && len(z) == 0 {
		delete(s.zsets, k)
	}
}

// commandKind is the key type each typed command wants.
var commandKind = map[string]string{
	"HGET": "hash", "HMGET": "hash", "HSET": "hash", "HDEL": "hash", "HEXISTS": "hash", "HGETALL": "hash", "HLEN": "hash", "HSTRLEN": "hash",
	"SADD": "set", "SREM": "set", "SISMEMBER": "set", "SMEMBERS": "set", "SCARD": "set",
	"ZADD": "zset", "ZREM": "zset", "ZSCORE": "zset", "ZCARD": "zset", "ZRANGE": "zset",
	"XADD": "stream", "XINFO": "stream", "GET": "string",
}

func (s *fakeStore) call(L *lua.LState) int  { return s.run(L, false) }
func (s *fakeStore) pcall(L *lua.LState) int { return s.run(L, true) }

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

func (s *fakeStore) run(L *lua.LState, protected bool) int {
	cmd := strings.ToUpper(L.CheckString(1))
	arg := func(i int) string { return L.Get(i).String() }
	n := L.GetTop()
	str := func(v string, ok bool) lua.LValue {
		if !ok {
			return lua.LFalse
		}
		return lua.LString(v)
	}
	key := arg(2)
	if cmd == "XINFO" {
		key = arg(3)
	}
	if want, typed := commandKind[cmd]; typed && n >= 2 {
		if found := s.kind(key); found != "none" && found != want {
			const wrongtype = "WRONGTYPE Operation against a key holding the wrong kind of value"
			if !protected {
				L.RaiseError("%s", wrongtype)
				return 0
			}
			e := L.NewTable()
			L.SetField(e, "err", lua.LString(wrongtype))
			L.Push(e)
			return 1
		}
	}
	defer s.drop(key)
	switch cmd {
	case "TIME":
		t := L.NewTable()
		t.Append(lua.LString("1790000000"))
		t.Append(lua.LString("0"))
		L.Push(t)
	case "XADD":
		s.lastID++
		id := strconv.Itoa(s.lastID) + "-0"
		entry := []string{id}
		for i := 4; i <= n; i++ {
			entry = append(entry, arg(i))
		}
		s.streams[arg(2)] = append(s.streams[arg(2)], entry)
		L.Push(lua.LString(id))
	case "XINFO":
		t := L.NewTable()
		t.Append(lua.LString("length"))
		t.Append(lua.LNumber(len(s.streams[key])))
		t.Append(lua.LString("last-generated-id"))
		t.Append(lua.LString(strconv.Itoa(s.lastID) + "-0"))
		L.Push(t)
	case "GET":
		v, ok := s.strs[arg(2)]
		L.Push(str(v, ok))
	case "DEL", "UNLINK":
		gone := 0
		for i := 2; i <= n; i++ {
			if s.kind(arg(i)) != "none" {
				gone++
			}
			delete(s.strs, arg(i))
			delete(s.hashes, arg(i))
			delete(s.sets, arg(i))
			delete(s.zsets, arg(i))
			delete(s.streams, arg(i))
		}
		L.Push(lua.LNumber(gone))
	case "EXISTS":
		found := 0
		for i := 2; i <= n; i++ {
			if s.kind(arg(i)) != "none" {
				found++
			}
		}
		L.Push(lua.LNumber(found))
	case "TYPE":
		t := L.NewTable()
		L.SetField(t, "ok", lua.LString(s.kind(arg(2))))
		L.Push(t)
	case "HGETALL":
		t := L.NewTable()
		h := s.hashes[arg(2)]
		var names []string
		for f := range h {
			names = append(names, f)
		}
		sort.Strings(names)
		for _, f := range names {
			t.Append(lua.LString(f))
			t.Append(lua.LString(h[f]))
		}
		L.Push(t)
	case "HLEN":
		L.Push(lua.LNumber(len(s.hashes[arg(2)])))
	case "HSTRLEN":
		L.Push(lua.LNumber(len(s.hashes[arg(2)][arg(3)])))
	case "SCARD":
		L.Push(lua.LNumber(len(s.sets[arg(2)])))
	case "SREM":
		for i := 3; i <= n; i++ {
			delete(s.set(arg(2)), arg(i))
		}
		L.Push(lua.LNumber(1))
	case "ZADD":
		z, added := s.zset(arg(2)), 0
		for i := 3; i+1 <= n; i += 2 {
			f, err := strconv.ParseFloat(arg(i), 64)
			if err != nil {
				L.RaiseError("ERR value is not a valid float")
				return 0
			}
			if _, had := z[arg(i+1)]; !had {
				added++
			}
			z[arg(i+1)] = f
		}
		L.Push(lua.LNumber(added))
	case "ZREM":
		z, gone := s.zset(arg(2)), 0
		for i := 3; i <= n; i++ {
			if _, had := z[arg(i)]; had {
				gone++
			}
			delete(z, arg(i))
		}
		L.Push(lua.LNumber(gone))
	case "ZSCORE":
		f, ok := s.zsets[arg(2)][arg(3)]
		L.Push(str(score(f), ok))
	case "ZCARD":
		L.Push(lua.LNumber(len(s.zsets[arg(2)])))
	case "ZRANGE":
		names := s.zrange(arg(2))
		lo, _ := strconv.Atoi(arg(3))
		hi, _ := strconv.Atoi(arg(4))
		if lo < 0 {
			lo += len(names)
		}
		if hi < 0 {
			hi += len(names)
		}
		lo = max(lo, 0)
		hi = min(hi, len(names)-1)
		t := L.NewTable()
		for i := lo; i <= hi; i++ {
			t.Append(lua.LString(names[i]))
			if n >= 5 && strings.EqualFold(arg(5), "WITHSCORES") {
				t.Append(lua.LString(score(s.zsets[arg(2)][names[i]])))
			}
		}
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
		h, added := s.hash(arg(2)), 0
		for i := 3; i+1 <= n; i += 2 {
			if _, had := h[arg(i)]; !had {
				added++
			}
			h[arg(i)] = arg(i + 1)
		}
		L.Push(lua.LNumber(added))
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
	err := s.L.DoString(`redis.call('LPUSH', 'k', 'm')`)
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("no command %s", "LPUSH"))
}
