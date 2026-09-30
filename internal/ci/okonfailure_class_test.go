package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
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
	ledger := newSiteLedger(t, okOnFailureAllowlistPath)
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
				ledger.add(f.Rel+":"+s.fn+":"+s.kind, fmt.Sprintf("%s:%d: %s", f.Rel, s.line, s.text))
			}
		}
	}
	for _, v := range ledger.violations(t, "the word and the exit must agree: OK only at 0, FAIL or REFUSED only above it (a row's count only falls)") {
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
				// The word that counts is the last status line the run printed, the
				// one a reader is left with: an OK after a REFUSED, then exit 2, is an
				// OK on a failed run, and a FAIL after an OK, then exit 0, is the
				// reverse. A line holding both words is an item list ending in its verdict
				// and reads as a failure.
				word := lastStatusWord(text)
				line := fset.Position(st.Pos()).Line
				code, ok := exitCode(st)
				if !ok {
					if v := carriedExit(st); v != "" && word == wordOK {
						out = append(out, okOnFailureSite{fn: name, kind: kindOKCarried, line: line,
							text: fmt.Sprintf("prints %q on every outcome, then exits %s, which may be non-zero; print OK only when %s is 0, FAIL otherwise", remedyShort(text), v, v)})
					}
					continue
				}
				switch {
				case code != "0" && word == wordOK:
					out = append(out, okOnFailureSite{fn: name, kind: kindOKNonZero, line: line,
						text: fmt.Sprintf("prints %q, then exits %s; a failed run's last word is FAIL or REFUSED, never OK", remedyShort(text), code)})
				case code == "0" && word == wordFail:
					out = append(out, okOnFailureSite{fn: name, kind: kindFailZero, line: line,
						text: fmt.Sprintf("prints %q, then exits 0; a FAIL or REFUSED line exits 1 or 2", remedyShort(text))})
				}
			}
			return true
		})
	}
	return out
}

// The two status words lastStatusWord reads.
const (
	wordOK   = "ok"
	wordFail = "fail"
)

// lastStatusWord is the status the run's last status line carries: the lines of
// the printed text are read from the end, and the first one that holds an OK
// word or a FAIL or REFUSED word decides (fail when it holds both). "" when no
// line is a status line.
func lastStatusWord(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		switch l := lines[i]; {
		case failWordRe.MatchString(l):
			return wordFail
		case okWordRe.MatchString(l):
			return wordOK
		}
	}
	return ""
}

// fmtPrintsBefore is the text of the run of fmt print statements that ends
// the statement list (the lines a reader sees last before the exit), in the
// order they print.
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
		parts = append([]string{p.argsText(call)}, parts...)
	}
	return strings.Join(parts, "\n"), len(parts) > 0
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
// and FAIL then exit 0 are read, and so are an OK printed after a REFUSED (exit 2) and
// a FAIL printed after an OK (exit 0), because the last status line decides; OK then 0,
// FAIL then 1, and an item OK before the closing FAIL are not.
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

func b(w io.Writer, bad bool) int {
	if bad {
		fmt.Fprintln(w, "CHECK REFUSED reason=bad_flag: x; run: t help")
		fmt.Fprintln(w, "CHECK OK findings=0")
		return 2
	}
	if bad {
		fmt.Fprintln(w, "CHECK OK findings=0")
		fmt.Fprintln(w, "CHECK FAIL findings=1")
		return 0
	}
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
	// a: one of each. b: an OK printed after a REFUSED then exit 2 (the last status
	// line wins, not "any line says FAIL"), and a FAIL after an OK then exit 0.
	if got[kindOKNonZero] != 2 || got[kindFailZero] != 2 || len(got) != 2 {
		t.Errorf("got %v, want two %s and two %s", got, kindOKNonZero, kindFailZero)
	}
}
