package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// redis_cleanup_class_test.go enforces #4368:
// Every function that starts a throwaway redis-server registers t.Cleanup so
// test redis instances never leak, records the PID, and unlinks the PID file.
//
// Sweeping at startup cleans up any orphaned redis processes left by a hard-killed
// test binary or runner abort.

func TestEveryRedisStartHasCleanup(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	var violations []string
	checkedFiles := 0

	for _, tests := range []bool{false, true} {
		for _, f := range tree.GoFilesUnder(tests, "cmd", "internal") {
			if f.HasDirNamed("testdata") {
				continue
			}
			if f.ParseErr != nil {
				t.Fatal(f.ParseErr)
			}
			v := redisCleanupViolations(tree.FSet, f.AST)
			for _, line := range v {
				violations = append(violations, f.Rel+":"+line)
			}
			if f.Rel == "internal/nsprint/testutil/redis.go" {
				checkedFiles++
				assertTestutilRedisHelperInvariants(t, f)
			}
		}
	}

	if checkedFiles == 0 {
		t.Fatal("internal/nsprint/testutil/redis.go was not checked")
	}

	if len(violations) > 0 {
		t.Fatalf("%d function(s) start redis-server without registering t.Cleanup:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// assertTestutilRedisHelperInvariants verifies that internal/nsprint/testutil/redis.go
// has PID recording, cleanup unlinking the PID file, and calls SweepOrphans at Start.
func assertTestutilRedisHelperInvariants(t *testing.T, f *treeFile) {
	t.Helper()

	var hasSweepOrphansInStart bool
	var hasPIDRecordingInStartOnce bool
	var hasPIDCleanupInStartOnce bool
	var hasKillInStartOnce bool

	for _, decl := range f.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name == "Start" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "SweepOrphans" {
						hasSweepOrphansInStart = true
					}
				}
				return true
			})
		}
		if fn.Name.Name == "startOnce" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "PIDDir" {
						hasPIDRecordingInStartOnce = true
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if sel.Sel.Name == "Cleanup" {
							ast.Inspect(call, func(cn ast.Node) bool {
								if ccall, ok := cn.(*ast.CallExpr); ok {
									if csel, ok := ccall.Fun.(*ast.SelectorExpr); ok {
										if csel.Sel.Name == "Kill" {
											hasKillInStartOnce = true
										}
										if csel.Sel.Name == "Remove" {
											hasPIDCleanupInStartOnce = true
										}
									}
								}
								return true
							})
						}
					}
				}
				return true
			})
		}
	}

	if !hasSweepOrphansInStart {
		t.Error("internal/nsprint/testutil/redis.go: Start() does not call SweepOrphans()")
	}
	if !hasPIDRecordingInStartOnce {
		t.Error("internal/nsprint/testutil/redis.go: startOnce() does not record PID file in PIDDir()")
	}
	if !hasKillInStartOnce {
		t.Error("internal/nsprint/testutil/redis.go: startOnce() t.Cleanup does not call Kill()")
	}
	if !hasPIDCleanupInStartOnce {
		t.Error("internal/nsprint/testutil/redis.go: startOnce() t.Cleanup does not unlink PID file")
	}
}

func redisCleanupViolations(fset *token.FileSet, f *ast.File) []string {
	var violations []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		startsServer := false
		hasCleanup := false

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "Cleanup" {
					hasCleanup = true
				}
				if (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") && len(call.Args) > 0 {
					prog := call.Args[0]
					if lit, ok := prog.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if strings.Contains(lit.Value, "redis-server") {
							startsServer = true
						}
					}
					if id, ok := prog.(*ast.Ident); ok {
						if strings.Contains(strings.ToLower(id.Name), "redis") {
							startsServer = true
						}
					}
				}
			}
			return true
		})

		if fn.Name.Name == "startOnce" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if strings.Contains(lit.Value, "redis.log") {
						startsServer = true
					}
				}
				return true
			})
		}

		if startsServer && !hasCleanup {
			pos := fset.Position(fn.Pos())
			violations = append(violations, fmt.Sprintf("%d: function %s starts redis-server without registering t.Cleanup", pos.Line, fn.Name.Name))
		}
	}
	return violations
}

func TestEveryRedisStartHasCleanupSeesMissingCleanup(t *testing.T) {
	t.Parallel()

	const badSrc = `package main
func badStart(t *testing.T) {
	redisBin := "redis-server"
	cmd := exec.Command(redisBin, "--port", "1234")
	_ = cmd.Start()
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bad.go", badSrc, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := redisCleanupViolations(fset, f)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for badStart, got %d: %v", len(violations), violations)
	}

	const goodSrc = `package main
func goodStart(t *testing.T) {
	redisBin := "redis-server"
	cmd := exec.Command(redisBin, "--port", "1234")
	_ = cmd.Start()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
}
`
	f2, err := parser.ParseFile(fset, "good.go", goodSrc, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations2 := redisCleanupViolations(fset, f2)
	if len(violations2) != 0 {
		t.Fatalf("expected 0 violations for goodStart, got %d: %v", len(violations2), violations2)
	}
}
