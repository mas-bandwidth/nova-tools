package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cipriority_class_test.go is the class rule of nova-tools#4293 (Glenn
// 2026-09-26 ~12:00 PM ET: "CI over work is a permanent setting. It's a GOOD
// idea. because work creates more CI, so without this, it is unstable").
// Two halves, both read from the tree and neither runs anything:
//
//   - TestCopiesRunNiced: every path that execs a copy's harness, a
//     coordinator child's local test run, or a sprint card's native launch
//     (nova-swarm native, which members and readers start), steps its own
//     process down to yield.Nice (15) BEFORE the exec, on darwin and on
//     Linux, through the one package internal/yield.
//   - TestSlotsShrinkByCILegs: no bench slot computation ignores the CI
//     legs running on it: every `slots - ...` in live Go and Lua takes the
//     beat's ci off, and the beat writes it.
//
// It holds on every bench and every worker kind; a new exec path or a new
// slot computation that forgets is red here, not on a bench at 5.0 load.

// niceExecPaths are the exec paths and, for each, the call that must stand
// before the first exec in the same function: (file, function, yield call,
// exec call).
var niceExecPaths = []struct{ file, fn, yield, exec string }{
	{"cmd/nova-ci/local.go", "func cmdLocal(", "yield.ToCI()", "localCapture("},
	// a sprint member's or reader's card: native steps itself (and so the wall, the
	// harness and every process the card's child runs) before nativeRun starts any of it
	{"cmd/nova-swarm/main.go", "func cmdNative(", "yieldNative(nativeToCI,", "nativeRun("},
}

func TestCopiesRunNiced(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// 1. The number, and setpriority on both OSes, in the one package.
	y := readFile(t, filepath.Join(root, "internal/yield/yield.go"))
	if !strings.Contains(y, "const Nice = 15") {
		t.Errorf("internal/yield/yield.go: want `const Nice = 15` (nova-tools#4293 names fifteen)")
	}
	// darwin: a nice belongs to the process, so 0 (this process) is the
	// whole of it. Linux: a nice belongs to a THREAD, and a child forked
	// from an un-niced thread inherits 0 (hetzner, 2026-09-26: 31 of 32
	// children at nice 0 under the one-thread form), so every thread in
	// /proc/self/task is set, repeatedly until a pass sets none.
	d := readFile(t, filepath.Join(root, "internal/yield/nice_darwin.go"))
	if !strings.Contains(d, "syscall.Setpriority(syscall.PRIO_PROCESS, 0, n)") {
		t.Errorf("internal/yield/nice_darwin.go: want setpriority(PRIO_PROCESS, 0, n) on this process")
	}
	l := readFile(t, filepath.Join(root, "internal/yield/nice_linux.go"))
	if !strings.Contains(l, `"/proc/self/task"`) || !strings.Contains(l, "syscall.Setpriority(syscall.PRIO_PROCESS, tid, n)") {
		t.Errorf("internal/yield/nice_linux.go: want setpriority(PRIO_PROCESS, tid, n) over every thread in /proc/self/task (a Linux nice is per thread)")
	}
	if strings.Contains(l, "syscall.Setpriority(syscall.PRIO_PROCESS, 0, n)") {
		t.Errorf("internal/yield/nice_linux.go: the one-thread form setpriority(PRIO_PROCESS, 0, n) nices the calling thread only; children forked from the others run at 0")
	}
	if !strings.Contains(readFile(t, filepath.Join(root, "cmd/nova-ci/local.go")), "yield.Nice-15") {
		t.Errorf("cmd/nova-ci/local.go: localNice must be pinned to yield.Nice")
	}
	// strings.Contains, so a failure prints the one line wanted and not all of native.go
	assert.True(t, strings.Contains(readFile(t, filepath.Join(root, "cmd/nova-swarm/native.go")), "\nvar nativeToCI = yield.ToCI\n"),
		"cmd/nova-swarm/native.go: want `var nativeToCI = yield.ToCI`, the step every card's launch takes")

	// 2. Every exec path yields first, in the same function, before the exec.
	for _, p := range niceExecPaths {
		src := readFile(t, filepath.Join(root, p.file))
		body := funcBody(t, p.file, src, p.fn)
		yi, ei := strings.Index(body, p.yield), strings.Index(body, p.exec)
		switch {
		case yi < 0:
			t.Errorf("%s %s: no %s call: a copy or a local test run must yield to CI before it execs", p.file, p.fn, p.yield)
		case ei < 0:
			t.Errorf("%s %s: no %s call: the exec path this rule guards moved; move the rule with it", p.file, p.fn, p.exec)
		case yi > ei:
			t.Errorf("%s %s: %s stands after %s: a yield after the exec yields nothing", p.file, p.fn, p.yield, p.exec)
		}
	}

	// 2b. The reader below sees every shape of a write to nova-swarm's seam.
	t.Run("the nativeToCI reader sees every write", nativeToCIWritesSeesEveryShape)

	// 3. No production caller gives a copy a Yield of its own (the seam is
	// for tests).
	tree := repoTree(t)
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		for i, line := range strings.Split(string(f.Src), "\n") {
			if code := strings.TrimSpace(line); strings.HasPrefix(code, "Yield:") || strings.Contains(code, ".Yield = ") {
				t.Errorf("%s:%d: %q: production never sets a copy's Yield; the real setpriority is the default", f.Rel, i+1, code)
			}
		}
		// nova-swarm native's seam is yield.ToCI in production; only its test binary's
		// TestMain makes it a no-op (that binary is a CI leg running cmdNative in-process).
		// Read on the parsed file, so no spelling of a write gets past a text match.
		if f.AST != nil {
			for _, w := range nativeToCIWrites(tree.FSet, f.AST) {
				assert.Fail(t, "a production write to nativeToCI", "%s:%s: production never writes nova-swarm's nativeToCI; it is yield.ToCI", f.Rel, w)
			}
		}
	}
}

// nativeToCIWrites is every place in one parsed file that could change nova-swarm's
// nativeToCI: an assignment of any shape (=, :=, op=, one name among several) whose left
// side names it anywhere, and taking its address (a later write through the pointer).
// The one declaration, `var nativeToCI = yield.ToCI`, is a ValueSpec and none of these.
func nativeToCIWrites(fset *token.FileSet, file *ast.File) []string {
	names := func(e ast.Node) bool {
		found := false
		ast.Inspect(e, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "nativeToCI" {
				found = true
			}
			return !found
		})
		return found
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for _, l := range s.Lhs {
				if names(l) {
					out = append(out, fmt.Sprintf("%d: an assignment to it", fset.Position(s.Pos()).Line))
					break
				}
			}
		case *ast.UnaryExpr:
			if s.Op == token.AND && names(s.X) {
				out = append(out, fmt.Sprintf("%d: its address taken", fset.Position(s.Pos()).Line))
			}
		}
		return true
	})
	return out
}

// nativeToCIWritesSeesEveryShape is TestCopiesRunNiced's own check of its reader: the
// #5026 reader's mutations (a multi-assignment in an init, a write through a pointer)
// and the plain forms are each found; the declaration alone, and a call through the
// seam, are not.
func nativeToCIWritesSeesEveryShape(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"the declaration and a call", "var nativeToCI = yield.ToCI\nfunc f() { _ = nativeToCI() }", 0},
		{"plain assignment", "func init() { nativeToCI = func() error { return nil } }", 1},
		{"multi-assignment (the reader's)", "func init() { nativeToCI, _ = func() error { return nil }, 0 }", 1},
		{"through a pointer", "func init() { p := &nativeToCI; *p = nil }", 1},
		{"parenthesised", "func init() { (nativeToCI) = nil }", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "x.go", "package main\n"+c.body+"\n", 0)
			require.NoError(t, err)
			assert.Len(t, nativeToCIWrites(fset, file), c.want, "%s", c.body)
		})
	}
}

// funcBody is the text of one function in src from its declaration to the
// next top-level declaration (Go: a line starting `func ` or `}` at column
// 0; Lua: the next `function` or `local function` at column 0).
func funcBody(t *testing.T, file, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("%s: no %q", file, decl)
	}
	rest := src[i+len(decl):]
	end := len(rest)
	for _, next := range []string{"\nfunc ", "\n}\n", "\nfunction ", "\nlocal function ", "\nend\n"} {
		if j := strings.Index(rest, next); j >= 0 && j < end {
			end = j + len(next)
		}
	}
	return rest[:end]
}
