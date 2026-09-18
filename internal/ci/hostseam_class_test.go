package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// hostseam_class_test.go is the class rule bought on 2026-09-18: a unit test in
// the certify verb's first cut ran the REAL workloads on hulk and reached redis
// on space. Nobody wrote a hostname in the test. The test constructed
// production code, production code constructed its own default because nothing
// injected a fake, and the default was `exec.Command("ssh", …)`. The `net`
// class reads test files for a real host and could not see it: the host was
// never in the test's text, it was in a default two packages away.
//
// So the rule is on the PRODUCTION side, where the default lives. Every seam in
// this tree that reaches a host calls internal/testguard's RefuseHosts with the
// command line it is about to run; under NOVA_TEST_NO_HOST, which `make test`
// sets, that call panics. A test that wants a child installs its own fake on
// PATH and declares it with testguard.AllowHosts().
//
// This file is the half that reads the tree: it finds the seams and refuses one
// that does not call the guard. It runs no ssh and writes nothing.

// hostSeamAllowlistPath is the shrink-only list of functions this rule finds and
// does not require a guard from, each with a reason. It is checked in BOTH
// directions -- a seam not listed and not guarded is red, and a listed function
// that is no longer found is also red -- so the file can only get shorter.
const hostSeamAllowlistPath = "testdata/hostseam_allowlist.txt"

// testguardPkgDir is the guard itself. Reading it against itself is circular:
// this is the one place RefuseHosts is declared rather than called.
const testguardPkgDir = "internal/testguard"

// sshFamily is what "reaches a host" means mechanically: the four programs this
// repository spawns to work on another machine. A seam is found by the program
// it names, not by a hostname, because the hostname is the caller's.
var sshFamily = []string{"ssh", "scp", "sftp", "rsync"}

// TestNoTestReachesAHostThroughAnUnfakedSeam holds every host seam in cmd/ and
// internal/ against internal/testguard.
//
// A function is a HOST SEAM when either:
//
//	(a) it starts a child process (exec.Command or exec.CommandContext) and its
//	    body names an ssh-family program in a string literal -- which catches a
//	    seam whose program is built into an argv slice rather than passed
//	    straight to exec; or
//	(b) its own name, or its receiver's type name, carries an ssh-family word --
//	    which catches a seam that hands the work to a helper, where the child is
//	    one call deeper but the function IS the seam a caller reaches for.
//
// Every host seam calls testguard.RefuseHosts, and where the function starts a
// child itself the call stands BEFORE the first one: a guard after the exec
// guards nothing.
func TestNoTestReachesAHostThroughAnUnfakedSeam(t *testing.T) {
	root := repoRoot(t)
	allow := readHostSeamAllowlist(t)
	seen := map[string]bool{}
	var violations []string

	for _, dir := range []string{"cmd", "internal"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, testguardPkgDir+"/") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, raw, 0)
			if err != nil {
				return err
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				why := hostSeamReason(fn)
				if why == "" {
					continue
				}
				key := rel + ":" + removeAllFuncName(fn)
				seen[key] = true
				if allow[key] {
					continue
				}
				guard := guardCallPos(fn)
				if guard == token.NoPos {
					violations = append(violations, fmt.Sprintf(
						"%s:%d: %s reaches a host (%s) and does not call testguard.RefuseHosts; add `testguard.RefuseHosts(<program>, <args>...)` before the child runs, or list it in %s with a reason",
						rel, fset.Position(fn.Pos()).Line, removeAllFuncName(fn), why, hostSeamAllowlistPath))
					continue
				}
				if first := firstExecPos(fn); first != token.NoPos && guard > first {
					violations = append(violations, fmt.Sprintf(
						"%s:%d: %s calls testguard.RefuseHosts AFTER the child is started; a guard that runs once the host has been reached guards nothing",
						rel, fset.Position(guard).Line, removeAllFuncName(fn)))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Both directions: an entry whose function is gone (or no longer a seam) is
	// a red run, so a row parks nothing and the list only shrinks.
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no host seam by that name is there any more; delete the stale entry (the list only shrinks)",
				hostSeamAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// hostSeamReason returns why fn is a host seam, or "" when it is not. The
// reason is printed in the refusal so a friend meeting this red for the first
// time knows which half of the rule caught the function.
func hostSeamReason(fn *ast.FuncDecl) string {
	if name, ok := sshFamilyWord(fn.Name.Name); ok {
		return "its name says " + name
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if name, ok := sshFamilyWord(receiverName(fn.Recv.List[0].Type)); ok {
			return "its receiver says " + name
		}
	}
	if firstExecPos(fn) == token.NoPos {
		return ""
	}
	if prog, ok := sshLiteralIn(fn); ok {
		return "it starts " + strconv.Quote(prog)
	}
	return ""
}

// sshFamilyWord reports whether an identifier carries an ssh-family word,
// case-insensitively: fleetSSH, sshRun, scpFile, ExecSSH, publishOverSSH.
func sshFamilyWord(name string) (string, bool) {
	lower := strings.ToLower(name)
	for _, p := range sshFamily {
		if strings.Contains(lower, p) {
			return p, true
		}
	}
	return "", false
}

// sshLiteralIn returns an ssh-family program named by a string literal anywhere
// in fn's body. The match is the WHOLE literal, so prose mentioning ssh in a
// comment-like string, an error message or a flag description is not a seam.
func sshLiteralIn(fn *ast.FuncDecl) (string, bool) {
	found := ""
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || found != "" {
			return true
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, p := range sshFamily {
			if val == p {
				found = p
				return false
			}
		}
		return true
	})
	return found, found != ""
}

// firstExecPos is the position of the first exec.Command/exec.CommandContext in
// fn, or NoPos when the function starts no child itself.
func firstExecPos(fn *ast.FuncDecl) token.Pos {
	pos := token.NoPos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isExecCommand(call) {
			return true
		}
		if pos == token.NoPos || call.Pos() < pos {
			pos = call.Pos()
		}
		return true
	})
	return pos
}

// isExecCommand reports whether call is exec.Command or exec.CommandContext.
func isExecCommand(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != "exec" {
		return false
	}
	return sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext"
}

// guardCallPos is the position of the first testguard.RefuseHosts call in fn,
// or NoPos when there is none.
func guardCallPos(fn *ast.FuncDecl) token.Pos {
	pos := token.NoPos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RefuseHosts" {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != "testguard" {
			return true
		}
		if pos == token.NoPos || call.Pos() < pos {
			pos = call.Pos()
		}
		return true
	})
	return pos
}

func readHostSeamAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(hostSeamAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// `file:function  # reason`: the reason is required, because an
		// exception nobody explained is one nobody can ever remove.
		i := strings.Index(line, "#")
		if i < 0 || strings.TrimSpace(line[i+1:]) == "" {
			t.Errorf("%s: %q carries no reason; every exception says why, or nobody can ever delete it", hostSeamAllowlistPath, line)
			continue
		}
		allow[strings.TrimSpace(line[:i])] = true
	}
	return allow
}
