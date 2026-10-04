package fn

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// allowedGlobals are the only free names a lua/ file may read: the Redis
// Function environment, the Lua builtins the library uses, and NS, the
// prelude table every file shares (loader.go Prelude).
var allowedGlobals = map[string]bool{
	"redis": true, "cjson": true, "bit": true, "KEYS": true, "ARGV": true,
	"string": true, "table": true, "math": true,
	"tonumber": true, "tostring": true, "type": true, "pairs": true, "ipairs": true,
	"next": true, "select": true, "unpack": true, "error": true, "pcall": true, "assert": true,
	"getmetatable": true, // Lua builtin used to recognize Redis cjson array tables.
	"NS":           true,
}

// freeNames compiles one Lua file on its own (Lua 5.1, the Redis dialect) and
// returns the globals its code reads and writes, from GETGLOBAL/SETGLOBAL in
// every function body; a name another file declares as a local is free here.
func freeNames(t *testing.T, name, src string) (reads, writes []string) {
	t.Helper()
	chunk, err := parse.Parse(strings.NewReader(src), name)
	require.NoError(t, err, "parse %s", name)
	proto, err := lua.Compile(chunk, name)
	require.NoError(t, err, "compile %s", name)
	r, w := map[string]bool{}, map[string]bool{}
	var walk func(p *lua.FunctionProto)
	walk = func(p *lua.FunctionProto) {
		for _, inst := range p.Code {
			switch int(inst >> 26) { // gopher-lua opGetOpCode
			case lua.OP_GETGLOBAL:
				r[p.Constants[inst&0x3ffff].String()] = true // Kst(Bx) is the name
			case lua.OP_SETGLOBAL:
				w[p.Constants[inst&0x3ffff].String()] = true
			}
		}
		for _, c := range p.FunctionPrototypes {
			walk(c)
		}
	}
	walk(proto)
	for n := range r {
		reads = append(reads, n)
	}
	for n := range w {
		writes = append(writes, n)
	}
	sort.Strings(reads)
	sort.Strings(writes)
	return reads, writes
}

var (
	luaComment = regexp.MustCompile(`--.*`)
	nsWrite    = regexp.MustCompile(`\bNS\.([A-Za-z_][A-Za-z0-9_]*)\s*=[^=]`)
	nsRead     = regexp.MustCompile(`\bNS\.([A-Za-z_][A-Za-z0-9_]*)`)
)

// crossFileProblems runs the guard over every lua/*.lua file of fsys in load
// order (sorted) and returns one problem line per file, with names and exports.
func crossFileProblems(t *testing.T, fsys fs.FS) (problems, names []string, exported map[string]string) {
	t.Helper()
	names, err := fs.Glob(fsys, "lua/*.lua")
	require.NoError(t, err)
	sort.Strings(names) // Source's load order
	exported = map[string]string{}
	for _, name := range names {
		src, err := fs.ReadFile(fsys, name)
		require.NoError(t, err)
		reads, writes := freeNames(t, name, string(src))
		for _, n := range reads {
			if !allowedGlobals[n] {
				problems = append(problems, fmt.Sprintf("%s reads free name %q: a local of another file is out of scope in the one library (each file is its own do-block); export it as NS.<x> at the end of the defining file and bind `local %s = NS...` at the top of this one", name, n, n))
			}
		}
		for _, n := range writes {
			problems = append(problems, fmt.Sprintf("%s assigns global %q: Redis Functions refuse global writes; declare it local or hand it over through NS", name, n))
		}
		code := luaComment.ReplaceAllString(string(src), "")
		for _, m := range nsWrite.FindAllStringSubmatch(code, -1) {
			if _, ok := exported[m[1]]; !ok {
				exported[m[1]] = name
			}
		}
		for _, m := range nsRead.FindAllStringSubmatch(code, -1) {
			if _, ok := exported[m[1]]; !ok {
				problems = append(problems, fmt.Sprintf("%s reads NS.%s, which no file at or before it in load order assigns", name, m[1]))
			}
		}
	}
	return problems, names, exported
}

// TestNoBareCrossFileReferences is the guard for dev red at a42285c5: #3606
// wrapped every file in its own do-block, and task_claim.lua still read
// hold.lua's local HD and land.lua still called capacity.lua's local
// cap_budget_take/cap_budget_give, so ns_task_done failed at run time with
// "nonexistent global variable 'HD'" while FUNCTION LOAD succeeded. It fails
// when any file reads a free name outside allowedGlobals, writes any global
// (Redis refuses both), or reads an NS field no file at or before it (in load
// order) assigns, apart from the exact documented profile seams below.
func TestNoBareCrossFileReferences(t *testing.T) {
	t.Parallel()

	problems, names, exported := crossFileProblems(t, sources)
	for _, p := range problems {
		assert.Fail(t, p)
	}
	t.Logf("%d files, NS exports %v", len(names), exported)
}

// TestCrossFileGuardSeesTheBrokenShape is the guard's own control: the
// a42285c5 shape (a bare reference to another file's local) is a free name,
// and the fixed shape (bound from NS) is not.
func TestCrossFileGuardSeesTheBrokenShape(t *testing.T) {
	t.Parallel()

	broken := "local function done() return HD.ingest(nil, {}) end\nredis.register_function('x', done)\n"
	for _, tt := range []struct{ name, src, want string }{
		{"a bare local is a free name", broken, "HD,redis"},
		{"a name bound from NS is not", "local HD = NS.HD\n" + broken, "NS,redis"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads, _ := freeNames(t, "probe.lua", tt.src)
			require.Equal(t, tt.want, strings.Join(reads, ","), "%s reads %v", tt.name, reads)
		})
	}
}

// TestCrossFileGuardBuiltinAllowanceIsExact: the one builtin the library
// reads beyond the base set, getmetatable, is allowed by its exact name and a
// near miss is still a free name.
func TestCrossFileGuardBuiltinAllowanceIsExact(t *testing.T) {
	t.Parallel()

	reads, writes := freeNames(t, "builtin.lua", "return getmetatable({}), getmetatabl({})")
	require.Equal(t, "getmetatabl,getmetatable", strings.Join(reads, ","), "builtin control reads=%v", reads)
	require.Empty(t, writes, "builtin control writes=%v", writes)
	require.True(t, allowedGlobals["getmetatable"], "the builtin allowance must keep getmetatable")
	require.False(t, allowedGlobals["getmetatabl"], "the builtin allowance must refuse a near miss")
}

// TestCrossFileGuardRefusesAnUnassignedNSRead: a file that reads an NS field
// no file at or before it assigns is refused, and the same file after one
// that assigns the field passes.
func TestCrossFileGuardRefusesAnUnassignedNSRead(t *testing.T) {
	t.Parallel()

	reader := "local X = NS.later\nredis.register_function('ns_x', function() return X end)\n"
	for _, tt := range []struct {
		name    string
		fsys    fstest.MapFS
		wantLen int
		wantSub string
	}{
		{"an unassigned NS read", fstest.MapFS{"lua/a.lua": {Data: []byte(reader)}}, 1, "lua/a.lua reads NS.later"},
		{"an NS read after its assignment", fstest.MapFS{
			"lua/a.lua": {Data: []byte("NS.later = {}\n")},
			"lua/b.lua": {Data: []byte(reader)},
		}, 0, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			problems, _, _ := crossFileProblems(t, tt.fsys)
			require.Len(t, problems, tt.wantLen, "problems: %v", problems)
			if tt.wantSub != "" {
				require.Contains(t, problems[0], tt.wantSub)
			}
		})
	}
}
