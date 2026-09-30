package fn

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

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
// every function body. A file compiled alone sees exactly what it sees inside
// its do-block in the assembled library, so a name another file declares as
// a local is free here.
func freeNames(t *testing.T, name, src string) (reads, writes []string) {
	t.Helper()
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	proto, err := lua.Compile(chunk, name)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
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

// nsFieldAvailable keeps profile inputs separate from exports made by earlier
// fragments. These two exact seams do not waive any free-global/write check.
func nsFieldAvailable(name, field string, exported map[string]string) bool {
	if _, ok := exported[field]; ok {
		return true
	}
	if field == "tset_profile" {
		// TSetSource sets this feature key in its prelude. Legacy Source leaves
		// it nil deliberately: the explicit tset fragments remain inert there.
		for _, fragment := range tsetFragments {
			if name == fragment {
				return true
			}
		}
	}
	// The sprint's files (profile_sprint.go) are tset fragments of the sprint
	// profile: each opens with the same guard, which the legacy prelude leaves
	// nil (errata 2 to the upper design, item 8).
	if field == "tset_profile" {
		for _, fragment := range sprintFragments {
			if name == fragment {
				return true
			}
		}
	}
	// ns_sprint_step and ns_sprint_read resolve Layer 1's NS.tset and Layer 2's
	// NS.tlog when a call runs, inside the registered functions (errata 2,
	// item 8): the sprint profile loads them after the composed fragments.
	if name == "lua/sprint_zz_fn.lua" && (field == "tset" || field == "tlog") {
		return true
	}
	// The core resolves this dependency inside registered callbacks, after
	// the composed assembler must have loaded the required Layer 2 provider.
	// TestTSetComposedSourceIsExplicit still refuses an absent provider. This
	// load-order exception is neither provider nor callback execution evidence.
	return name == "lua/table_set.lua" && field == "tlog"
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

	names, err := fs.Glob(sources, "lua/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names) // Source's load order
	exported := map[string]string{}
	for _, name := range names {
		src, err := sources.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		reads, writes := freeNames(t, name, string(src))
		for _, n := range reads {
			if !allowedGlobals[n] {
				t.Errorf("%s reads free name %q: a local of another file is out of scope in the one library (each file is its own do-block); export it as NS.<x> at the end of the defining file and bind `local %s = NS...` at the top of this one", name, n, n)
			}
		}
		for _, n := range writes {
			t.Errorf("%s assigns global %q: Redis Functions refuse global writes; declare it local or hand it over through NS", name, n)
		}
		code := luaComment.ReplaceAllString(string(src), "")
		for _, m := range nsWrite.FindAllStringSubmatch(code, -1) {
			if _, ok := exported[m[1]]; !ok {
				exported[m[1]] = name
			}
		}
		for _, m := range nsRead.FindAllStringSubmatch(code, -1) {
			if !nsFieldAvailable(name, m[1], exported) {
				t.Errorf("%s reads NS.%s, which no file at or before it in load order assigns and no profile seam supplies", name, m[1])
			}
		}
	}
	t.Logf("%d files, NS exports %v", len(names), exported)
}

// TestCrossFileGuardSeesTheBrokenShape is the guard's own control: the
// a42285c5 shape (a bare reference to another file's local) is a free name,
// and the fixed shape (bound from NS) is not.
func TestCrossFileGuardSeesTheBrokenShape(t *testing.T) {
	t.Parallel()

	broken := "local function done() return HD.ingest(nil, {}) end\nredis.register_function('x', done)\n"
	reads, _ := freeNames(t, "broken.lua", broken)
	if strings.Join(reads, ",") != "HD,redis" {
		t.Fatalf("broken shape reads %v, want [HD redis]", reads)
	}
	fixed := "local HD = NS.HD\n" + broken
	reads, _ = freeNames(t, "fixed.lua", fixed)
	if strings.Join(reads, ",") != "NS,redis" {
		t.Fatalf("fixed shape reads %v, want [NS redis]", reads)
	}
}

// Profile exceptions must not turn into exemptions for a file, an NS namespace,
// or a similarly named global. The original broken-shape control stays above.
func TestCrossFileGuardAllowsOnlyProfileSeams(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, field string
		want        bool
	}{
		{"lua/table_set.lua", "tset_profile", true},
		{"lua/table_set_validate.lua", "tset_profile", true},
		{"lua/table_set.lua", "tlog", true},
		{"lua/task.lua", "tset_profile", false},
		{"lua/table_set_unknown.lua", "tset_profile", false},
		{"lua/task.lua", "tlog", false},
		{"lua/table_set_read.lua", "tlog", false},
		{"lua/table_set.lua", "tlog_typo", false},
		{"lua/table_set.lua", "tset_profiel", false},
		{"lua/table_set.lua", "unknown", false},
		{"lua/task.lua", "known", true},
		{"lua/sprint_00_core.lua", "tset_profile", true},
		{"lua/sprint_zz_fn.lua", "tset_profile", true},
		{"lua/sprint_zz_fn.lua", "tset", true},
		{"lua/sprint_zz_fn.lua", "tlog", true},
		{"lua/sprint_00_core.lua", "tset", false},
		{"lua/sprint_00_core.lua", "tlog", false},
		{"lua/sprint_unknown.lua", "tset_profile", false},
		{"lua/sprint_zz_fn.lua", "tset_typo", false},
		{"lua/sprint_zz_fn.lua", "unknown", false},
		{"lua/task.lua", "tset", false},
	} {
		if got := nsFieldAvailable(tc.name, tc.field, map[string]string{"known": "lua/earlier.lua"}); got != tc.want {
			t.Errorf("%s NS.%s available=%v, want %v", tc.name, tc.field, got, tc.want)
		}
	}
	reads, writes := freeNames(t, "builtin.lua", "return getmetatable({}), getmetatabl({})")
	if strings.Join(reads, ",") != "getmetatabl,getmetatable" || len(writes) != 0 {
		t.Fatalf("builtin control reads=%v writes=%v", reads, writes)
	}
	if !allowedGlobals["getmetatable"] || allowedGlobals["getmetatabl"] {
		t.Fatal("builtin allowance must remain exact")
	}
}
