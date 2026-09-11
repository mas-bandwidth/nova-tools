package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Work list 10 puts the no-/tmp, no-process-scan tripwire in cmd/nova-merge/*_test.go and
// it existed only for internal/merge -- so THIS package, which writes `stop`, `.gitignore`
// and every lane path a flag names, was unscanned. The subject of this test is the source,
// which is the one reason anything here reaches outside t.TempDir(), and it is stated.

func mainPackageSource(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	if len(out) == 0 {
		t.Fatal("no source files found; this test was looking in the wrong place and would have passed by checking nothing")
	}
	return out
}

// Rule 13: nothing under /tmp, and the tool never matches a process by its own command
// line. Every path this binary writes is under --lane, which a person gave it.
func TestTheBinaryReachesNoTmpAndNoProcessTable(t *testing.T) {
	t.Parallel()
	for name, src := range mainPackageSource(t) {
		for _, forbidden := range []string{`"/tmp`, "os.TempDir", "pgrep", `"ps"`, "/proc/", "TMPDIR"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s carries %q; every path this tool writes is under --lane, and a loop that matches a process by its own command line matches itself (19 orphaned shells, 2026-09-09)", name, forbidden)
			}
		}
	}
}

// Rule 7, from this side: the binary writes the lane's own files and nothing in a clone's
// work tree. A new writing site here is a decision rather than a drive-by.
func TestTheBinaryWritesOnlyTheLanesOwnFiles(t *testing.T) {
	t.Parallel()
	allowed := map[string]string{
		"verbs.go": "the lane branch's .gitignore, which init writes, under --lane",
		"pass.go":  "the lane's stop file, which the stop verb writes, under --lane",
	}
	for name, src := range mainPackageSource(t) {
		for i, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "os.WriteFile") && !strings.Contains(line, "os.OpenFile") && !strings.Contains(line, "os.Create") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if _, ok := allowed[name]; !ok {
				t.Errorf("%s:%d writes a file: %s\nthe lane never edits an entry's content, and a new writing site is a decision", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// THE IDENTITY TRIPWIRE FOR THIS PACKAGE. gitops.go's own comment says "every call site
// that can write a commit object goes through here" and names the source test that holds
// it -- and that test read only internal/merge/*.go, so the package the Ubuntu red was
// actually about was unscanned. verbs.go's `commit` does carry merge.Identity today; a
// second one added here would not have been caught by anything.
func TestEveryCommitWritingCommandInThisPackageCarriesTheIdentity(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	for name := range mainPackageSource(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Run" && sel.Sel.Name != "Out") {
				return true
			}
			args := call.Args
			identity := false
			// The wrapper here is merge.Identity(...), a selector rather than a bare
			// name: this package reaches it across the package boundary.
			if len(args) == 1 {
				if inner, ok := args[0].(*ast.CallExpr); ok {
					if wrap, ok := inner.Fun.(*ast.SelectorExpr); ok && wrap.Sel.Name == "Identity" {
						identity, args = true, inner.Args
					}
				}
			}
			words := commandWords(args)
			if len(words) == 0 || !commandWritesACommit(words) {
				return true
			}
			checked++
			if !identity {
				t.Errorf("%s:%d runs `git %s` without merge.Identity; a machine with no git identity -- every CI runner -- answers \"Committer identity unknown\", and this package is the one the Ubuntu leg of #57 went red in",
					name, fset.Position(call.Pos()).Line, strings.Join(words, " "))
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no commit-writing git command found in this package; this test was looking for the wrong shape and would have passed by checking nothing")
	}
}

// commandWords returns the leading string literals of an argument list, which is how every
// git command in this package starts.
func commandWords(args []ast.Expr) []string {
	out := []string(nil)
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			break
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			break
		}
		out = append(out, value)
	}
	return out
}

// commandWritesACommit reports whether the command makes a commit object. `merge --abort`
// and `merge --ff-only` move a ref and write none.
func commandWritesACommit(words []string) bool {
	switch words[0] {
	case "commit":
		return true
	case "merge":
		for _, w := range words[1:] {
			if w == "--abort" || w == "--ff-only" {
				return false
			}
		}
		return true
	}
	return false
}
