package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// LuaDir is where the nova_sprint function library's files live, relative to
// a repository root. Each file is one do-block of the assembled chunk
// (internal/nsprint/fn), so a file's top-level locals are what the chunk's
// main function holds while that block is open.
const LuaDir = "internal/nsprint/fn/lua"

// LuaLocalsLimit is the most top-level locals one file may declare. It is
// the assembled library's limit (fn.MaxLocals, headroom under Lua's 200):
// the prelude adds one, so a file at the limit is the chunk at the limit.
var LuaLocalsLimit = fn.MaxLocals

var localDecl = regexp.MustCompile(`^local\s+(function\s+[A-Za-z_][A-Za-z0-9_]*|[A-Za-z_][A-Za-z0-9_]*(\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)`)

// LuaLocals counts a file's column-0 `local` declarations, the ones the
// assembled chunk's main function holds while the file's block is open
// (a `local a, b` counts two; `local function f` counts one).
func LuaLocals(src string) int {
	n := 0
	for _, line := range strings.Split(src, "\n") {
		m := localDecl.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.HasPrefix(m[1], "function") {
			n++
		} else {
			n += strings.Count(m[1], ",") + 1
		}
	}
	return n
}

func luaFiles(root string) ([]string, error) {
	names, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(LuaDir), "*.lua"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names) // the loader's load order
	return names, nil
}

func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}

// luaLocals is the guard for dev red 8d12fb19 (#3487): a file whose
// top-level locals pass the limit takes the assembled library over Lua's
// 200 and FUNCTION LOAD refuses it on the fleet.
func luaLocals(_ context.Context, root string) Result {
	names, err := luaFiles(root)
	if err != nil {
		return Result{Err: err}
	}
	if len(names) == 0 {
		return Result{Err: fmt.Errorf("no lua files under %s", LuaDir)}
	}
	most, largest := 0, ""
	for _, name := range names {
		src, err := os.ReadFile(name)
		if err != nil {
			return Result{Err: err}
		}
		n := LuaLocals(string(src))
		if n > most {
			most, largest = n, rel(root, name)
		}
		if n > LuaLocalsLimit {
			return Result{OK: false, File: rel(root, name), Why: fmt.Sprintf("%d top-level locals, over %d (Lua refuses a chunk above 200); move helpers into a table or split the file", n, LuaLocalsLimit)}
		}
	}
	return Result{OK: true, Why: fmt.Sprintf("%d files, largest %s at %d of %d", len(names), largest, most, LuaLocalsLimit)}
}

// allowedGlobals are the only free names a lua file may read: the Redis
// Function environment, the Lua builtins the library uses, and NS, the
// prelude table every file shares (fn.Prelude).
var allowedGlobals = map[string]bool{
	"redis": true, "cjson": true, "bit": true, "KEYS": true, "ARGV": true,
	"string": true, "table": true, "math": true,
	"tonumber": true, "tostring": true, "type": true, "pairs": true, "ipairs": true,
	"next": true, "select": true, "unpack": true, "error": true, "pcall": true, "assert": true,
	"NS": true,
}

// LuaFreeNames compiles one Lua file on its own (Lua 5.1, the Redis dialect)
// and returns the globals its code reads and writes, from GETGLOBAL and
// SETGLOBAL in every function body. A file compiled alone sees exactly what
// it sees inside its do-block in the assembled library, so a name another
// file declares as a local is free here.
func LuaFreeNames(name, src string) (reads, writes []string, err error) {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}
	proto, err := lua.Compile(chunk, name)
	if err != nil {
		return nil, nil, fmt.Errorf("compile: %w", err)
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
	return reads, writes, nil
}

var (
	luaComment = regexp.MustCompile(`--.*`)
	nsWrite    = regexp.MustCompile(`\bNS\.([A-Za-z_][A-Za-z0-9_]*)\s*=[^=]`)
	nsRead     = regexp.MustCompile(`\bNS\.([A-Za-z_][A-Za-z0-9_]*)`)
)

// luaCrossFile is the guard for dev red a42285c5 (#3606 met #3593): a file
// that reads another file's local, writes a global, or reads an NS field no
// earlier file assigns loads fine and fails at run time inside FCALL.
func luaCrossFile(_ context.Context, root string) Result {
	names, err := luaFiles(root)
	if err != nil {
		return Result{Err: err}
	}
	if len(names) == 0 {
		return Result{Err: fmt.Errorf("no lua files under %s", LuaDir)}
	}
	exported := map[string]bool{}
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			return Result{Err: err}
		}
		src := string(raw)
		file := rel(root, name)
		reads, writes, err := LuaFreeNames(filepath.Base(name), src)
		if err != nil {
			return Result{OK: false, File: file, Why: err.Error()}
		}
		for _, n := range reads {
			if !allowedGlobals[n] {
				return Result{OK: false, File: file, Why: fmt.Sprintf("reads free name %q: a local of another file is out of scope in the one library; export it as NS.%s at the end of the defining file and bind `local %s = NS.%s` at the top of this one", n, n, n, n)}
			}
		}
		if len(writes) > 0 {
			return Result{OK: false, File: file, Why: fmt.Sprintf("assigns global %q: Redis Functions refuse global writes; declare it local or hand it over through NS", writes[0])}
		}
		code := luaComment.ReplaceAllString(src, "")
		for _, m := range nsWrite.FindAllStringSubmatch(code, -1) {
			exported[m[1]] = true
		}
		for _, m := range nsRead.FindAllStringSubmatch(code, -1) {
			if !exported[m[1]] {
				return Result{OK: false, File: file, Why: fmt.Sprintf("reads NS.%s, which no file at or before it in load order assigns", m[1])}
			}
		}
	}
	return Result{OK: true, Why: fmt.Sprintf("%d files, %d NS exports", len(names), len(exported))}
}
