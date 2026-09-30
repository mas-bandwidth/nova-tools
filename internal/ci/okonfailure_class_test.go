package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// okOnFailureAllowlistPath is the shrink-only ledger of the print-then-exit
// pairs in cmd/ whose word and code disagree: one `file:function:kind` per row,
// a reason after it.
const okOnFailureAllowlistPath = "testdata/okonfailure_allowlist.txt"

// okWordRe is the OK word of an event line: a line that starts with OK or with
// upper-case tokens and then OK (`<TOKEN> OK key=value`, docs/CLI-STYLE.md (e)),
// or a line that ends with OK. failWordRe is the same for FAIL and REFUSED. A
// help text that mentions the words in prose is neither.
var (
	okWordRe   = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_-]* )*OK(\s|:|$)|\sOK$`)
	failWordRe = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_-]* )*(FAIL|FAILED|REFUSED)(\s|:|=|$)|\s(FAIL|FAILED|REFUSED)$`)
)

// The two kinds of disagreement.
const (
	kindOKNonZero = "ok-nonzero" // an OK line, then a non-zero exit
	kindFailZero  = "fail-zero"  // a FAIL or REFUSED line, then exit 0
	kindOKCarried = "ok-carried" // an OK line, then an exit code carried in a variable
)

// TestNoOKOnFailure is the owner's rule (2026-09-30 ~19:20 ET) that no tool
// fails silently, read at the one place a reader trusts most: the closing
// word. It reads every non-test .go file under cmd/ and, in every block, pairs
// each exit (`return <n>`, os.Exit(<n>) with an integer literal) with the
// print statements just before it: an OK line followed by a non-zero exit, or
// a FAIL or REFUSED line followed by exit 0, says one thing and does another
// (tonight's case: a quickstart printing QUICKSTART OK over failed checks).
// A pair not in the ledger is red, and a ledger row naming no pair is red too.
func TestNoOKOnFailure(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readReasonedAllowlist(t, okOnFailureAllowlistPath)
	seen := map[string]bool{}
	var violations []string
	for _, files := range goFilesByDir(livingCmdFiles(tree)) {
		var asts []*ast.File
		for _, f := range files {
			if f.ParseErr != nil {
				t.Fatal(f.ParseErr)
			}
			asts = append(asts, f.AST)
		}
		pkg := newCmdPackage(asts)
		for _, f := range files {
			for _, s := range pkg.okOnFailureSites(tree.FSet, f.AST) {
				key := f.Rel + ":" + s.fn + ":" + s.kind
				seen[key] = true
				if allow.Has(key) {
					continue
				}
				violations = append(violations, fmt.Sprintf("%s:%d: %s: %s", f.Rel, s.line, key, s.text))
			}
		}
	}
	for _, row := range allowlist.Check(t, allow, seen).Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but that word and exit agree now or are gone; delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)",
			okOnFailureAllowlistPath, row.Key))
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// okOnFailureSite is one disagreeing pair.
type okOnFailureSite struct {
	fn, kind, text string
	line           int
}

// okOnFailureSites reads one file's blocks for the two disagreements.
func (p *cmdPackage) okOnFailureSites(fset *token.FileSet, f *ast.File) []okOnFailureSite {
	var out []okOnFailureSite
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := removeAllFuncName(fn)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			b, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}
			for i, st := range b.List {
				text, printed := p.fmtPrintsBefore(b.List[:i])
				if !printed {
					continue
				}
				line := fset.Position(st.Pos()).Line
				code, ok := exitCode(st)
				if !ok {
					if v := carriedExit(st); v != "" && okWordRe.MatchString(text) && !failWordRe.MatchString(text) {
						out = append(out, okOnFailureSite{fn: name, kind: kindOKCarried, line: line,
							text: fmt.Sprintf("prints %q on every outcome, then exits %s, which may be non-zero; print OK only when %s is 0, FAIL otherwise", remedyShort(text), v, v)})
					}
					continue
				}
				switch {
				case code != "0" && okWordRe.MatchString(text) && !failWordRe.MatchString(text):
					out = append(out, okOnFailureSite{fn: name, kind: kindOKNonZero, line: line,
						text: fmt.Sprintf("prints %q, then exits %s; a failed run's last word is FAIL or REFUSED, never OK", remedyShort(text), code)})
				case code == "0" && failWordRe.MatchString(text) && !okWordRe.MatchString(text):
					out = append(out, okOnFailureSite{fn: name, kind: kindFailZero, line: line,
						text: fmt.Sprintf("prints %q, then exits 0; a FAIL or REFUSED line exits 1 or 2", remedyShort(text))})
				}
			}
			return true
		})
	}
	return out
}

// fmtPrintsBefore is the text of the run of fmt print statements that ends
// the statement list (the lines a reader sees last before the exit).
func (p *cmdPackage) fmtPrintsBefore(list []ast.Stmt) (string, bool) {
	var parts []string
	for i := len(list) - 1; i >= 0; i-- {
		es, ok := list[i].(*ast.ExprStmt)
		if !ok {
			break
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok || !isFmtPrint(call) {
			break
		}
		parts = append(parts, p.argsText(call))
	}
	return strings.Join(parts, " "), len(parts) > 0
}

// carriedExit is the variable of `return <ident>` in a function returning an
// exit code (the name reads as one: code, worst, exit, rc, status), or "".
func carriedExit(st ast.Stmt) string {
	r, ok := st.(*ast.ReturnStmt)
	if !ok || len(r.Results) != 1 {
		return ""
	}
	id, ok := r.Results[0].(*ast.Ident)
	if !ok || !exitVarRe.MatchString(id.Name) {
		return ""
	}
	return id.Name
}

var exitVarRe = regexp.MustCompile(`(?i)^(code|worst|exit|exitCode|rc|status|worstCode|result)$`)

// exitCode reads `return <int>` and os.Exit(<int>).
func exitCode(st ast.Stmt) (string, bool) {
	var e ast.Expr
	switch x := st.(type) {
	case *ast.ReturnStmt:
		if len(x.Results) != 1 {
			return "", false
		}
		e = x.Results[0]
	case *ast.ExprStmt:
		call, ok := x.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return "", false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Exit" {
			return "", false
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" {
			return "", false
		}
		e = call.Args[0]
	default:
		return "", false
	}
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return "", false
	}
	return lit.Value, true
}

// TestOKOnFailureRuleReadsBothWays proves the rule over source: OK then exit 1
// and FAIL then exit 0 are read; OK then 0, FAIL then 1, and a line that holds
// both words (an item OK before the closing FAIL) are not.
func TestOKOnFailureRuleReadsBothWays(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"io"
)

func a(w io.Writer, bad bool) int {
	if bad {
		fmt.Fprintln(w, "QUICKSTART OK steps=3")
		return 1
	}
	if bad {
		fmt.Fprintf(w, "CHECK FAIL findings=%d\n", 2)
		return 0
	}
	if bad {
		fmt.Fprintln(w, "CHECK FAIL findings=2")
		return 1
	}
	if bad {
		fmt.Fprintln(w, "ITEM OK a")
		fmt.Fprintln(w, "CHECK FAIL findings=1")
		return 1
	}
	fmt.Fprintln(w, "CHECK OK findings=0")
	return 0
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := newCmdPackage([]*ast.File{f})
	got := map[string]int{}
	for _, s := range p.okOnFailureSites(fset, f) {
		got[s.kind]++
	}
	if got[kindOKNonZero] != 1 || got[kindFailZero] != 1 || len(got) != 2 {
		t.Errorf("got %v, want one %s and one %s", got, kindOKNonZero, kindFailZero)
	}
}
