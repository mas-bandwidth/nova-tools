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

// writeSite is one allowed open-for-writing: the expression, and the thing it writes.
type writeSite struct{ expr, what string }

// Rule 7, from this side: the binary writes the lane's own files and nothing in a clone's
// work tree. A new writing site here is a decision rather than a drive-by.
//
// THE ALLOWANCE IS PER SITE, NOT PER FILE. It used to be a file name with a sentence, so
// every write in verbs.go and pass.go was allowed by the entry that covers the first one
// -- a second open of a record's path in either of them would have passed unread (read 4b,
// finding 6). Each site is named, and each allowance must match exactly one line, so an
// allowance that stops being true is as loud as a site that is not allowed.
func TestTheBinaryWritesOnlyTheLanesOwnFiles(t *testing.T) {
	t.Parallel()
	allowed := map[string][]writeSite{
		"verbs.go": {{`os.WriteFile(filepath.Join(lane, ".gitignore")`, "the lane branch's .gitignore, which init writes, under --lane"}},
		"pass.go":  {{`os.WriteFile(path, []byte(deps.Now()`, "the lane's stop file, which the stop verb writes, under --lane"}},
	}
	used := map[string]int{}
	for name, src := range mainPackageSource(t) {
		for i, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "os.WriteFile") && !strings.Contains(line, "os.OpenFile") && !strings.Contains(line, "os.Create") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			site := ""
			for _, s := range allowed[name] {
				if strings.Contains(line, s.expr) {
					site = s.expr
					break
				}
			}
			if site == "" {
				t.Errorf("%s:%d writes a file: %s\nthe lane never edits an entry's content, and a new writing site is a decision", name, i+1, strings.TrimSpace(line))
				continue
			}
			used[name+" "+site]++
		}
	}
	for name, sites := range allowed {
		for _, s := range sites {
			if n := used[name+" "+s.expr]; n != 1 {
				t.Errorf("the allowance for %q in %s matched %d writing sites, want exactly one (%s)", s.expr, name, n, s.what)
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
