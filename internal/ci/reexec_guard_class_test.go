package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reexecGuardRemedy is the one line every refusal ends with.
const reexecGuardRemedy = `remedy="call testbin.Enter(tool, handled) from an init or TestMain in cmd/<tool>/reexec_test.go (docs/TESTS.md, tests-reexec-guard-everywhere)"`

// reexecFinding is one _test.go file under cmd/ that runs its own test binary
// in a package that never calls the guard.
type reexecFinding struct {
	File string
	Line int
	Use  string
}

func (f reexecFinding) render() string {
	return fmt.Sprintf("REEXEC file=%s line=%d use=%s %s", f.File, f.Line, f.Use, reexecGuardRemedy)
}

// checkReexecGuard reads every _test.go under root/cmd and returns each one that
// execs the test binary (os.Executable() or os.Args[0]) in a package whose tests
// never call testbin.Enter from an init or a TestMain. A test binary that runs
// itself with words it does not answer runs the whole suite again in the child
// (289 processes on 2026-10-04); Enter is what refuses that
// (docs/SPEC-CI.md, `reexec-guard`; docs/TESTS.md, tests-reexec-guard-everywhere).
func checkReexecGuard(root string) ([]reexecFinding, error) {
	type file struct {
		rel  string
		uses []reexecFinding
	}
	byDir := map[string][]file{}
	guarded := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "cmd"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dir := filepath.Dir(path)
		f := file{rel: rel}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Body != nil && (fn.Name.Name == "init" || fn.Name.Name == "TestMain") && reexecCallsEnter(fn.Body) {
				guarded[dir] = true
			}
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if reexecIsSel(x.Fun, "os", "Executable") {
					f.uses = append(f.uses, reexecFinding{File: rel, Line: fset.Position(x.Pos()).Line, Use: "os.Executable"})
				}
			case *ast.IndexExpr:
				if sel, ok := x.X.(*ast.SelectorExpr); ok && reexecIsSel(sel, "os", "Args") && reexecIsZero(x.Index) {
					f.uses = append(f.uses, reexecFinding{File: rel, Line: fset.Position(x.Pos()).Line, Use: "os.Args[0]"})
				}
			}
			return true
		})
		byDir[dir] = append(byDir[dir], f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []reexecFinding
	for dir, files := range byDir {
		if guarded[dir] {
			continue
		}
		for _, f := range files {
			out = append(out, f.uses...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

func reexecIsSel(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func reexecIsZero(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}

// reexecCallsEnter reports whether the body calls testbin.Enter.
func reexecCallsEnter(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && reexecIsSel(c.Fun, "testbin", "Enter") {
			found = true
		}
		return !found
	})
	return found
}

// reexecFixture writes one package under <tmp>/cmd/tool and returns the root.
func reexecFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "cmd", "tool")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, src := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644))
	}
	return root
}

const reexecUnguardedSrc = `package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestRunsItself(t *testing.T) {
	self, _ := os.Executable()
	_ = exec.Command(self, "init").Run()
}
`

const reexecArgsSrc = `package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestRunsItselfByArgs(t *testing.T) {
	_ = exec.Command(os.Args[0], "init").Run()
}
`

const reexecGuardSrc = `package main

import "github.com/mas-bandwidth/nova-tools/pkg/testbin"

func init() { testbin.Enter("tool", nil) }
`

const reexecGuardOutsideSrc = `package main

import "github.com/mas-bandwidth/nova-tools/pkg/testbin"

func helper() { testbin.Enter("tool", nil) }
`

// TestEveryReexecOfTheTestBinaryHasTheGuard: a _test.go under cmd/ that runs its
// own test binary (os.Executable() or os.Args[0]) lives in a package that calls
// testbin.Enter from an init or a TestMain, so a child started with words the
// package does not answer is refused instead of running the suite again
// (docs/TESTS.md, tests-reexec-guard-everywhere).
func TestEveryReexecOfTheTestBinaryHasTheGuard(t *testing.T) {
	t.Parallel()
	found, err := checkReexecGuard(repoRoot(t))
	require.NoError(t, err)
	for _, f := range found {
		t.Error(f.render())
	}
}

// TestReexecGuardRefusesAnUnguardedFixture: the witness. A fixture that runs
// os.Executable() or os.Args[0] with no guard in its package is refused naming
// the file and the line; a guard called from an ordinary function does not count.
func TestReexecGuardRefusesAnUnguardedFixture(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		line  int
		use   string
	}{
		{"os.Executable", map[string]string{"a_test.go": reexecUnguardedSrc}, 10, "os.Executable"},
		{"os.Args[0]", map[string]string{"a_test.go": reexecArgsSrc}, 10, "os.Args[0]"},
		{"a guard that is not in init or TestMain", map[string]string{"a_test.go": reexecUnguardedSrc, "guard_test.go": reexecGuardOutsideSrc}, 10, "os.Executable"},
	}
	for _, c := range cases {
		found, err := checkReexecGuard(reexecFixture(t, c.files))
		require.NoError(t, err, c.name)
		require.Len(t, found, 1, c.name)
		assert.Equal(t, "cmd/tool/a_test.go", found[0].File, c.name)
		assert.Equal(t, c.line, found[0].Line, c.name)
		assert.Equal(t, c.use, found[0].Use, c.name)
		assert.Contains(t, found[0].render(), "file=cmd/tool/a_test.go line=")
		assert.Contains(t, found[0].render(), "testbin.Enter")
	}
}

// TestReexecGuardPassesAGuardedPackage: the same re-exec in a package whose
// init calls testbin.Enter is not refused, and neither is a package that never
// runs its binary.
func TestReexecGuardPassesAGuardedPackage(t *testing.T) {
	t.Parallel()
	found, err := checkReexecGuard(reexecFixture(t, map[string]string{"a_test.go": reexecUnguardedSrc, "reexec_test.go": reexecGuardSrc}))
	require.NoError(t, err)
	assert.Empty(t, found)
	found, err = checkReexecGuard(reexecFixture(t, map[string]string{"a_test.go": "package main\n"}))
	require.NoError(t, err)
	assert.Empty(t, found)
}
