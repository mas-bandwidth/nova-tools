/*
Package audit is the source-level tripwire behind the one-line guarantee, shared by every
binary in this repo. It exists because the first fix of this class was audited by counting
print sites BY HAND and came up nine short; every one of the nine was a refusal or a note
rather than an event line, which is exactly what hand-counting misses. So the count is
mechanical: a binary's test calls PrintedArguments, which reads every non-test file of the
package under test, pairs every fmt print argument with the verb that prints it, and
classifies each one as

	%q, or a numeric verb  -- the verb quotes and escapes it, or it is not text
	a literal              -- a string literal, or a constant declared in the package
	escaped                -- rendered through oneline.Escape, Field or Err, or a
	                          function the config names as an escaper
	exempted               -- named in the config, one entry per site, with the reason

Anything else fails, naming the file and line. A new interpolation is a decision from
then on, never a drive-by: adding one means either escaping it or writing down why it is
safe. A stale exemption fails too, because it is a claim about a site that no longer
exists and would silently cover the next one written in its place.

Bypasses closes the gap the classifier cannot see, since it walks fmt calls and therefore
knows only one way of putting bytes on a stream: io.WriteString, a bare .Write, a fmt
function used as a VALUE rather than called, a local that shadows the escaping path, the
print and println builtins, an aliased or unlisted import, and flag.FlagSet.SetOutput
with anything but io.Discard, which is how package flag came to print an attacker's
argument before any code in the binary ran.

Neither test can see an escape dropped inside a loop that builds a value printed later.
That shape is exempted by name where it exists, and each such exemption has a behavioral
test of its own in the binary that owns it.
*/
package audit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Config is one binary's claims about its own source.
type Config struct {
	// Escapers names calls whose result is safe to print, by the source text of the
	// callee, beyond the three from package oneline that are always accepted. A binary
	// lists its own wrappers here, and each wrapper's body is walked by the same
	// classifier, so the claim is checked rather than taken.
	Escapers []string
	// Exempt maps "file|function|argument source text" to the reason the site is safe.
	// One entry per site. The file is in the key because a package is many files.
	Exempt map[string]string
	// Shadows names identifiers no local may shadow, beyond oneline, Escape, Field and
	// Err, which are always refused.
	Shadows []string
	// Imports lists the import paths the package is allowed, quoted as they appear in
	// source. internal/oneline is always allowed. An import off the list is a writer
	// nobody has read yet, and widening the list is a decision made in the test.
	Imports []string
	// MinClassified is the number of printed arguments the walk must reach, so that a
	// walk looking in the wrong place cannot pass by classifying nothing.
	MinClassified int
}

type source struct {
	name   string
	src    []byte
	fset   *token.FileSet
	parsed *ast.File
}

// sources reads and parses every non-test .go file in the current directory, which under
// go test is the directory of the package under test. Reaching outside t.TempDir() has one
// reason here and it is stated: the subject of these tests IS the source, and a property
// of the code is not provable from its output alone. It reads the directory rather than
// one filename because a helper added in a second file of package main defeated an
// earlier version that guarded one file.
func sources(t *testing.T) []source {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []source
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, source{name, src, fset, parsed})
	}
	if len(files) == 0 {
		t.Fatal("no non-test source found in this package; the walk is looking in the wrong place")
	}
	return files
}

func (f source) text(n ast.Node) string {
	return string(f.src[f.fset.Position(n.Pos()).Offset:f.fset.Position(n.End()).Offset])
}

func (f source) at(n ast.Node) string {
	return fmt.Sprintf("%s:%d", f.name, f.fset.Position(n.Pos()).Line)
}

// packageConsts collects every package-level constant name, so that an identifier naming
// one counts as a literal: usage text, a note, a probe sentence. Only constants; a var
// can be reassigned from anything.
func packageConsts(files []source) map[string]bool {
	consts := map[string]bool{}
	for _, f := range files {
		for _, d := range f.parsed.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, n := range vs.Names {
						consts[n.Name] = true
					}
				}
			}
		}
	}
	return consts
}

// verbsOf returns the verbs of a format string in order, skipping the flags, width and
// precision in front of each. It refuses a `*` width or precision, which consumes an
// argument and would mis-pair everything after it, and a classifier that quietly
// mis-pairs arguments would be worse than one that stops.
func verbsOf(t *testing.T, format string) []byte {
	t.Helper()
	var verbs []byte
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		for i < len(format) && strings.IndexByte("+-# 0123456789.", format[i]) >= 0 {
			i++
		}
		if i >= len(format) {
			t.Fatalf("trailing %% in format %q", format)
		}
		switch format[i] {
		case '%':
			continue
		case '*':
			t.Fatalf("format %q uses a * width or precision; this classifier does not model those", format)
		}
		verbs = append(verbs, format[i])
	}
	return verbs
}

// printers are the fmt functions that take a format or a list, with the index of the first
// printed argument: the writer comes first for the F forms, and the S forms have none.
var printers = map[string]struct {
	first     int
	formatted bool
}{
	"Fprintf": {1, true}, "Fprint": {1, false}, "Fprintln": {1, false},
	"Sprintf": {0, true}, "Sprint": {0, false}, "Sprintln": {0, false},
	"Printf": {0, true}, "Print": {0, false}, "Println": {0, false},
}

// PrintedArguments classifies every argument of every fmt print call in the package under
// test, and fails on any that is neither quoted, numeric, literal, escaped nor exempted.
func PrintedArguments(t *testing.T, cfg Config) {
	t.Helper()
	files := sources(t)
	consts := packageConsts(files)

	escapers := map[string]bool{"oneline.Escape": true, "oneline.Field": true, "oneline.Err": true}
	for _, e := range cfg.Escapers {
		escapers[e] = true
	}
	usedExemption := map[string]bool{}

	var literalOnly func(ast.Expr) bool
	literalOnly = func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.BasicLit:
			return true
		case *ast.Ident:
			return consts[e.Name]
		case *ast.BinaryExpr:
			return e.Op == token.ADD && literalOnly(e.X) && literalOnly(e.Y)
		case *ast.ParenExpr:
			return literalOnly(e.X)
		}
		return false
	}

	classified := 0
	for _, f := range files {
		for _, decl := range f.parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			fnName := fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "fmt" {
					return true
				}
				p, ok := printers[sel.Sel.Name]
				if !ok {
					return true // Errorf builds an error; where it is printed is where it is classified
				}
				args := call.Args[p.first:]
				var verbs []byte
				if p.formatted {
					format, err := strconv.Unquote(f.text(args[0]))
					if err != nil {
						// A concatenated or computed format string is itself a finding: the
						// verbs could not be read, so nothing after it can be classified.
						t.Errorf("%s: format string is not a single literal: %s", f.at(call), f.text(args[0]))
						return true
					}
					verbs = verbsOf(t, format)
					args = args[1:]
					if len(verbs) != len(args) {
						t.Errorf("%s: %d verbs but %d arguments; the classifier cannot pair them", f.at(call), len(verbs), len(args))
						return true
					}
				}
				for i, arg := range args {
					classified++
					verb := byte('s')
					if i < len(verbs) {
						verb = verbs[i]
					}
					if verb == 'q' || strings.IndexByte("dbcoxXUeEfFgGtp", verb) >= 0 {
						continue // quoted, or not text
					}
					if literalOnly(arg) {
						continue
					}
					if e, ok := arg.(*ast.CallExpr); ok && escapers[f.text(e.Fun)] {
						continue
					}
					if key := f.name + "|" + fnName + "|" + f.text(arg); cfg.Exempt[key] != "" {
						usedExemption[key] = true
						continue
					}
					t.Errorf("%s in %s: %%%c prints %s raw -- escape it (oneline.Escape, oneline.Field or oneline.Err) or add an exemption naming the reason",
						f.at(call), fnName, verb, f.text(arg))
				}
				return true
			})
		}
	}

	for key, why := range cfg.Exempt {
		if !usedExemption[key] {
			t.Errorf("exemption %q (%s) matches no print site; delete it rather than leave a claim nothing checks", key, why)
		}
	}
	if classified < cfg.MinClassified {
		t.Errorf("only %d printed arguments were classified, below the %d this package is known to have; the walk is not reaching them", classified, cfg.MinClassified)
	}
}

// Bypasses refuses every way of writing to a stream that PrintedArguments cannot see.
func Bypasses(t *testing.T, cfg Config) {
	t.Helper()
	shadows := map[string]bool{"oneline": true, "Escape": true, "Field": true, "Err": true}
	for _, s := range cfg.Shadows {
		shadows[s] = true
	}
	allowed := map[string]bool{`"github.com/mas-bandwidth/nova-tools/internal/oneline"`: true}
	for _, p := range cfg.Imports {
		allowed[p] = true
	}
	for _, f := range sources(t) {
		bypassesIn(t, f, shadows, allowed)
	}
}

func bypassesIn(t *testing.T, f source, shadows, allowed map[string]bool) {
	t.Helper()

	// Every selector that IS the callee of a call. Anything else naming fmt is a function
	// VALUE, which can be stored, passed and called where no classifier will look.
	calledFuns := map[ast.Node]bool{}
	ast.Inspect(f.parsed, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			calledFuns[call.Fun] = true
		}
		return true
	})

	declared := func(n ast.Node, idents []*ast.Ident) {
		for _, id := range idents {
			if shadows[id.Name] {
				t.Errorf("%s: a local named %q shadows the escaping path; rename it", f.at(n), id.Name)
			}
		}
	}

	ast.Inspect(f.parsed, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			// The builtins first: they are a bare identifier rather than a package call,
			// so a check written after the selector assertion below never sees them.
			if id, ok := node.Fun.(*ast.Ident); ok && (id.Name == "print" || id.Name == "println") {
				t.Errorf("%s: the %s builtin writes to stderr outside every check here; use fmt.Fprintf", f.at(node), id.Name)
			}
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "SetOutput":
				// A flag set that can print is a writer the binary does not control,
				// quoting an argument the binary did not author.
				if len(node.Args) != 1 || f.text(node.Args[0]) != "io.Discard" {
					t.Errorf("%s: SetOutput(%s) lets another package write to a stream; only io.Discard is allowed",
						f.at(node), f.text(node.Args[0]))
				}
			case "Write", "WriteString", "WriteByte", "WriteRune":
				t.Errorf("%s: %s writes bytes past the escaping path; print through fmt.Fprintf with an escaped argument",
					f.at(node), f.text(node.Fun))
			}
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); ok && x.Name == "fmt" && !calledFuns[ast.Node(node)] {
				t.Errorf("%s: %s is used as a fmt function VALUE rather than called; it prints where the classifier cannot see it",
					f.at(node), f.text(node))
			}
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				var ids []*ast.Ident
				for _, lhs := range node.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						ids = append(ids, id)
					}
				}
				declared(node, ids)
			}
		case *ast.ValueSpec:
			declared(node, node.Names)
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE {
				var ids []*ast.Ident
				for _, e := range []ast.Expr{node.Key, node.Value} {
					if id, ok := e.(*ast.Ident); ok {
						ids = append(ids, id)
					}
				}
				declared(node, ids)
			}
		case *ast.FuncType:
			var ids []*ast.Ident
			for _, list := range []*ast.FieldList{node.Params, node.Results} {
				if list == nil {
					continue
				}
				for _, field := range list.List {
					ids = append(ids, field.Names...)
				}
			}
			declared(node, ids)
		}
		return true
	})

	// THE IMPORTS ARE PART OF THE FENCE. Everything above classifies what the code does
	// with the packages it has; this refuses the packages that would make the classifying
	// meaningless. An aliased fmt prints under a name no check looks for, log writes to
	// its own output with a timestamp and no escape at all, and anything not on the list
	// is a writer nobody has read yet.
	for _, imp := range f.parsed.Imports {
		path := imp.Path.Value
		if imp.Name != nil {
			t.Errorf("%s: import %s %s is aliased; an aliased package prints under a name no check here looks for",
				f.at(imp), imp.Name.Name, path)
		}
		if path == `"log"` {
			t.Errorf("%s: log writes to its own output, unescaped and outside every check here", f.at(imp))
			continue
		}
		if !allowed[path] {
			t.Errorf("%s: import %s is not on this package's list; if it is wanted, add it to the audit config and say why it cannot write past the escape",
				f.at(imp), path)
		}
	}

	// io.WriteString is a plain call rather than a selector on a writer, so it is named
	// directly, over the source text, because it takes no other form.
	if strings.Contains(string(f.src), "io.WriteString") {
		t.Errorf("%s: io.WriteString writes past the escaping path; print through fmt.Fprintf", f.name)
	}
}
