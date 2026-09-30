package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// remedyAllowlistPath is the shrink-only ledger of the refusal sites in cmd/
// that print no remedy yet: one `file:function:kind` per row, a reason after it.
const remedyAllowlistPath = "testdata/remedy_allowlist.txt"

// refusalWordRe is how a refusal line is found: its words.
var refusalWordRe = regexp.MustCompile(`REFUSED|refused|refusing|cannot`)

// refusePrinterRe names a tool's refusal printer: refuse, refused, refuseRan,
// egressRefuse, writeDoctorRefusal and the rest of the family.
var refusePrinterRe = regexp.MustCompile(`(?i)refus`)

// The three kinds of refusal site.
const (
	siteRefuseCall  = "refuse-call"  // a call to a refusal printer
	siteRefusePrint = "refuse-print" // a print whose text holds a refusal word
	siteExit2       = "exit-2"       // `return 2` or os.Exit(2)
)

// TestEveryRefusalCarriesARemedy is the owner's rule (2026-09-30 ~19:20 ET):
// every tool and verb "should never fail silently, and they should always
// provide helpful breadcrumbs how to fix anything going wrong". It reads every
// non-test .go file under cmd/ and finds the refusal sites three ways:
//
//   - a call to the package's refusal printer (a function whose name holds
//     "refus"): it carries a remedy when the printer itself prints one (every
//     `refuse` that ends in `; run: <tool> help`) or when its arguments do;
//   - a fmt print whose text holds REFUSED, refused, refusing or cannot: its
//     text carries the remedy;
//   - an exit-2 path (`return 2`, os.Exit(2)): the prints just before it in the
//     same block carry the remedy, and a `return 2` with no print in front of it
//     is a silent exit.
//
// A remedy is one of the house forms (oneline.HasRemedy): `run: <command>`,
// `remedy:`, `fix:`, `wants <x>`, `see <where>`, `rerun`, `<tool> <verb> -h`.
// A site without one is red unless its `file:function:kind` is in the ledger,
// and a ledger row naming no such site is red too.
func TestEveryRefusalCarriesARemedy(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readReasonedAllowlist(t, remedyAllowlistPath)
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
			for _, s := range pkg.refusalSites(tree.FSet, f.AST) {
				if s.remedied {
					continue
				}
				key := f.Rel + ":" + s.fn + ":" + s.kind
				seen[key] = true
				if allow.Has(key) {
					continue
				}
				violations = append(violations, fmt.Sprintf("%s:%d: %s prints no remedy (%s); end the line with `; run: <command>` (or `remedy:`, `wants <x>`, `see <tool> help <verb>`) so the reader knows the next step", f.Rel, s.line, key, s.text))
			}
		}
	}
	for _, row := range allowlist.Check(t, allow, seen).Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but that site prints its remedy now or is gone; delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)",
			remedyAllowlistPath, row.Key))
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// helpText reports a print of a verb's help rather than a refusal: it says
// "usage", or it runs to three lines or more. Help mentions the refusals a
// verb makes; it is not one.
func helpText(s string) bool {
	return strings.Contains(strings.ToLower(s), "usage") || strings.Count(s, "\n") >= 3
}

// livingCmdFiles are the non-test .go files under cmd/ that ship: a fake under
// testdata/ is a fixture that imitates a tool's refusals, not a tool.
func livingCmdFiles(tree *repoTreeIndex) []*treeFile {
	var out []*treeFile
	for _, f := range tree.GoFilesUnder(false, "cmd") {
		if !f.HasDirNamed("testdata") {
			out = append(out, f)
		}
	}
	return out
}

// goFilesByDir groups files by their directory: one Go package each.
func goFilesByDir(files []*treeFile) map[string][]*treeFile {
	out := map[string][]*treeFile{}
	for _, f := range files {
		d := path.Dir(f.Rel)
		out[d] = append(out[d], f)
	}
	return out
}

// cmdPackage is what the rule knows of one package: its string constants and
// which of its refusal printers print a remedy themselves.
type cmdPackage struct {
	consts   map[string]string
	printers map[string]bool // name -> prints a remedy itself
}

func newCmdPackage(files []*ast.File) *cmdPackage {
	p := &cmdPackage{consts: map[string]string{}, printers: map[string]bool{}}
	for _, f := range files {
		for _, decl := range f.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						p.consts[name.Name] = p.text(vs.Values[i])
					}
				}
			}
		}
	}
	bodies := map[string]*ast.BlockStmt{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil || !refusePrinterRe.MatchString(fn.Name.Name) || !takesWriter(fn) {
				continue
			}
			bodies[fn.Name.Name] = fn.Body
			p.printers[fn.Name.Name] = hasRemedy(p.text(fn.Body))
		}
	}
	// A printer that hands its line to a printer that prints a remedy prints
	// one too (nova-table's refuseColumns ends in refuse or refused).
	for changed := true; changed; {
		changed = false
		for name, body := range bodies {
			if p.printers[name] {
				continue
			}
			ast.Inspect(body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok && id.Name != name && p.printers[id.Name] {
						p.printers[name], changed = true, true
					}
				}
				return !p.printers[name]
			})
		}
	}
	return p
}

// takesWriter reports a function with an io.Writer parameter: a printer, not
// a predicate that happens to say "refusal" in its name.
func takesWriter(fn *ast.FuncDecl) bool {
	for _, field := range fn.Type.Params.List {
		if sel, ok := field.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Writer" {
			return true
		}
	}
	return false
}

// text is every string literal and named string constant under n, joined: the
// words a reader of the printed line would see.
func (p *cmdPackage) text(n ast.Node) string {
	var b strings.Builder
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			// oneline.WithRemedy ends its line with "; run: <next>" whenever
			// the line names no remedy of its own.
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithRemedy" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "oneline" {
					b.WriteString("run: ")
				}
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				if v, err := strconv.Unquote(x.Value); err == nil {
					b.WriteString(v)
					b.WriteByte(' ')
				}
			}
		case *ast.Ident:
			if v, ok := p.consts[x.Name]; ok {
				b.WriteString(v)
				b.WriteByte(' ')
			}
		}
		return true
	})
	return b.String()
}

// hasRemedy is the house definition of a remedy (internal/oneline.HasRemedy),
// read over the source text a print would print: the rule and the printers
// agree on what a remedy is because they read one list.
func hasRemedy(s string) bool { return oneline.HasRemedy(s) }

// refusalSite is one refusal the rule found.
type refusalSite struct {
	fn, kind, text string
	line           int
	remedied       bool
}

// refusalSites reads one file's functions. A refusal printer's own body is
// judged at its calls, never inside itself.
func (p *cmdPackage) refusalSites(fset *token.FileSet, f *ast.File) []refusalSite {
	var out []refusalSite
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, printer := p.printers[fn.Name.Name]; printer && fn.Recv == nil {
			continue
		}
		name := removeAllFuncName(fn)
		add := func(n ast.Node, kind, text string, remedied bool) {
			out = append(out, refusalSite{fn: name, kind: kind, text: remedyShort(text), line: fset.Position(n.Pos()).Line, remedied: remedied})
		}
		delegated := map[ast.Stmt]bool{}
		carried := p.printedFlags(fn.Body)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok {
					if self, printer := p.printers[id.Name]; printer {
						add(x, siteRefuseCall, id.Name+"("+p.argsText(x)+")", self || hasRemedy(p.argsText(x)))
					}
				}
				if isFmtPrint(x) {
					if s := p.argsText(x); refusalWordRe.MatchString(s) && !helpText(s) {
						add(x, siteRefusePrint, s, hasRemedy(s))
					}
				}
			case *ast.IfStmt:
				// `if !check(..., stderr) { return 2 }`: the check printed
				// the line, and its prints are read where they stand.
				if len(x.Body.List) == 1 && isExit2(x.Body.List[0]) &&
					(passesWriter(x.Cond) || isFlagParse(x) || carriesPrinted(x.Cond, carried)) {
					delegated[x.Body.List[0]] = true
				}
			case *ast.BlockStmt:
				for i, st := range x.List {
					if !isExit2(st) || delegated[st] {
						continue
					}
					s, printed := p.printsBefore(x.List[:i])
					if !printed {
						add(st, siteExit2, "exit 2 with no line in front of it", false)
						continue
					}
					if !refusalWordRe.MatchString(s) {
						add(st, siteExit2, s, hasRemedy(s))
					}
				}
			}
			return true
		})
	}
	return out
}

// printsBefore is the text of the run of print statements (fmt prints and
// refusal printer calls) that ends the statement list, and whether there was
// one. A printer that prints its own remedy lends the run a remedy marker.
func (p *cmdPackage) printsBefore(list []ast.Stmt) (string, bool) {
	var parts []string
	for i := len(list) - 1; i >= 0; i-- {
		if sw, ok := list[i].(*ast.SwitchStmt); ok {
			// `switch kind { case "a": fmt.Fprintf(...) case "b": ... }`: one
			// line from whichever case ran; every case must print one.
			var texts []string
			for _, st := range sw.Body.List {
				if s, ok := p.printsBefore(st.(*ast.CaseClause).Body); ok {
					texts = append(texts, s)
					continue
				}
				texts = nil
				break
			}
			if len(texts) == 0 {
				break
			}
			for _, s := range texts {
				if !hasRemedy(s) {
					return s, true
				}
			}
			parts = append(parts, strings.Join(texts, " "))
			continue
		}
		if loop := loopBody(list[i]); loop != nil {
			// `for _, why := range problems { fmt.Fprintf(...) }`: one
			// line per problem, read as the run's text.
			if s, ok := p.printsBefore(loop.List); ok {
				parts = append(parts, s)
				continue
			}
			break
		}
		if earlyReturn(list[i]) {
			// `if answered(err) { return 1 }` between the line and the exit
			// chooses the code; the line in front of both is the one read.
			continue
		}
		es, ok := list[i].(*ast.ExprStmt)
		if !ok {
			break
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			break
		}
		if isFmtPrint(call) {
			parts = append(parts, p.argsText(call))
			continue
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			if self, printer := p.printers[id.Name]; printer {
				text := p.argsText(call)
				if self {
					text += " run:"
				}
				parts = append(parts, text)
				continue
			}
		}
		break
	}
	return strings.Join(parts, " "), len(parts) > 0
}

func (p *cmdPackage) argsText(c *ast.CallExpr) string {
	var parts []string
	for _, a := range c.Args {
		parts = append(parts, p.text(a))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// earlyReturn reports `if <cond> { return <n> }` with no else: a choice of
// exit code, not a line.
func earlyReturn(st ast.Stmt) bool {
	x, ok := st.(*ast.IfStmt)
	if !ok || x.Else != nil || x.Init != nil || len(x.Body.List) != 1 {
		return false
	}
	_, ok = x.Body.List[0].(*ast.ReturnStmt)
	return ok
}

// loopBody is the body of a for or range statement, or nil.
func loopBody(st ast.Stmt) *ast.BlockStmt {
	switch x := st.(type) {
	case *ast.RangeStmt:
		return x.Body
	case *ast.ForStmt:
		return x.Body
	}
	return nil
}

// writerNames are the parameter names a verb's output writers go by.
var writerNames = map[string]bool{"stderr": true, "stdout": true, "w": true, "errw": true, "out": true, "errOut": true}

// passesWriter reports a condition that calls a function with an output
// writer among its arguments: the call prints its own line.
func passesWriter(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			for _, a := range c.Args {
				if id, ok := a.(*ast.Ident); ok && writerNames[id.Name] {
					found = true
				}
				if sel, ok := a.(*ast.SelectorExpr); ok && writerNames[sel.Sel.Name] {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// printedFlags are the identifiers of body that carry an earlier printed
// line: set from a call that was handed a writer (`given, ok := parse(fs,
// args, stderr, ...)`), or set to a bool in a block that printed (`refuse(...);
// bad = true`). A later `if bad { return 2 }` exits on a line already printed.
func (p *cmdPackage) printedFlags(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Rhs) == 1 && passesWriter(x.Rhs[0]) {
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.BlockStmt:
			if !p.blockPrints(x) {
				return true
			}
			for _, st := range x.List {
				a, ok := st.(*ast.AssignStmt)
				if !ok || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
					continue
				}
				id, ok := a.Lhs[0].(*ast.Ident)
				v, isIdent := a.Rhs[0].(*ast.Ident)
				if ok && isIdent && (v.Name == "true" || v.Name == "false") {
					out[id.Name] = true
				}
			}
		}
		return true
	})
	return out
}

// blockPrints reports a block whose own statements print a line.
func (p *cmdPackage) blockPrints(b *ast.BlockStmt) bool {
	for _, st := range b.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		if isFmtPrint(call) || passesWriter(call) {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			if _, printer := p.printers[id.Name]; printer {
				return true
			}
		}
	}
	return false
}

// carriesPrinted reports a condition made only of identifiers that carry an
// earlier printed line (`bad`, `!ok`, `given == nil`).
func carriesPrinted(cond ast.Expr, carried map[string]bool) bool {
	any, all := false, true
	ast.Inspect(cond, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			all = false
			return false
		case *ast.Ident:
			if x.Name == "nil" || x.Name == "true" || x.Name == "false" {
				return true
			}
			any = true
			if !carried[x.Name] {
				all = false
			}
		}
		return true
	})
	return any && all
}

// isFlagParse reports `if err := fs.Parse(args); err != nil` and the
// verbflag.Parse form: the flag package prints its message and the verb's
// usage to the set's output.
func isFlagParse(x *ast.IfStmt) bool {
	a, ok := x.Init.(*ast.AssignStmt)
	if !ok || len(a.Rhs) != 1 {
		return false
	}
	c, ok := a.Rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Parse"
}

// isFmtPrint reports fmt.Fprint*, fmt.Print*.
func isFmtPrint(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return false
	}
	return strings.HasPrefix(sel.Sel.Name, "Fprint") || strings.HasPrefix(sel.Sel.Name, "Print")
}

// isExit2 reports `return 2` and `os.Exit(2)`.
func isExit2(st ast.Stmt) bool {
	switch x := st.(type) {
	case *ast.ReturnStmt:
		return len(x.Results) == 1 && isIntLit(x.Results[0], "2")
	case *ast.ExprStmt:
		call, ok := x.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Exit" {
			return false
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" {
			return false
		}
		return isIntLit(call.Args[0], "2")
	}
	return false
}

func isIntLit(e ast.Expr, v string) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == v
}

func remedyShort(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}

// TestRemedyRuleReadsTheThreeSites proves the rule over source: a printer that
// prints its own remedy clears its calls, a refusal print with no remedy is
// read, an exit 2 after a remedied print is clear, and a bare `return 2` is a
// silent exit.
func TestRemedyRuleReadsTheThreeSites(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"io"
	"os"
)

const hint = "; run: tool verb -h"

func refuse(w io.Writer, what string) int {
	fmt.Fprintf(w, "tool: %s; run: tool help\n", what)
	return 2
}

func refuseRan(w io.Writer, what string) int {
	fmt.Fprintf(w, "tool: %s\n", what)
	return 1
}

func a(w io.Writer) int {
	if true {
		return refuse(w, "no --dir")
	}
	if true {
		return refuseRan(w, "the store refused the write")
	}
	if true {
		return refuseRan(w, "the store refused; rerun with --addr")
	}
	if true {
		fmt.Fprintln(w, "VERB REFUSED cannot read the file")
		return 1
	}
	if true {
		fmt.Fprintln(w, "VERB REFUSED cannot read the file"+hint)
		return 2
	}
	if true {
		fmt.Fprintln(w, "the input is empty")
		os.Exit(2)
	}
	return 2
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := newCmdPackage([]*ast.File{f})
	got := map[string]int{}
	for _, s := range p.refusalSites(fset, f) {
		if !s.remedied {
			got[s.kind]++
		}
	}
	want := map[string]int{siteRefuseCall: 1, siteRefusePrint: 1, siteExit2: 2}
	for kind, n := range want {
		if got[kind] != n {
			t.Errorf("%s: %d unremedied sites, want %d (got %v)", kind, got[kind], n, got)
		}
	}
}
