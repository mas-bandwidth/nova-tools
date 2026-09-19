package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// subVerbHelpAllowlistPath is the shrink-only list of functions that parse a
// sub-verb's flags and do not answer a request for help. It is EMPTY, and it is
// checked in both directions -- an unlisted offender is a red run, and a listed
// function that no longer offends is a stale entry and also a red run -- so the
// exception set can only ever get smaller. It lives in testdata so a reader sees
// the whole of it without reading this test.
const subVerbHelpAllowlistPath = "testdata/subverb_help_allowlist.txt"

// cliflagsPkgDir is the one implementation of the rule. The walk skips it
// because reading it against itself would be circular: its own tests prove that
// Help answers flag.ErrHelp and that Answer answers before a flag set exists.
const cliflagsPkgDir = "internal/cliflags"

// TestEverySubVerbFlagSetAnswersHelp is the class rule behind `<tool> <verb>
// --help`.
//
// THE DEFECT. Every verb in this repository parses its flags with a flag set in
// flag.ContinueOnError mode whose output is io.Discard -- io.Discard because
// package flag is not allowed to print an argument the binary did not author,
// which internal/oneline/audit enforces by refusing SetOutput of anything else.
// That pair has a cost nobody chose. Package flag answers `--help` by calling
// the set's usage function and returning the sentinel flag.ErrHelp; with the
// output discarded and the sentinel treated as a parse failure, a person who
// asked a reasonable question got `flag: help requested` -- the flag package's
// own internals -- on stderr, at exit 2. A dogfooder measured it across the
// family on 2026-09-18: nova-merge simulate, nova-pulse cut, nova-pulse fill,
// nova-tokens fold, nova-tokens check, nova-check links and more. #1336 fixed
// it verb by verb inside internal/release and internal/update; this test is why
// it cannot come back anywhere.
//
// THE HEURISTIC, stated so a reader can argue with it. The walk reads every
// non-test .go file under cmd/ and internal/ (internal/cliflags excepted) and:
//
//  1. collects, per directory, the names bound to a *flag.FlagSet -- an
//     identifier assigned from flag.NewFlagSet, and any parameter declared
//     *flag.FlagSet, so a set built in one function and parsed in another is
//     still followed;
//  2. finds every function that calls Parse on one of those names, including
//     through a field (`f.fs.Parse(args)` counts, `time.Parse(...)` does not),
//     because the place that handles the parse error is the place that must
//     recognise the question;
//  3. requires that function to answer help itself -- a call to cliflags.Help
//     or cliflags.Answer, or a reference to flag.ErrHelp -- OR its package to
//     answer before it dispatches, which is a call to cliflags.Answer somewhere
//     in the same directory.
//
// The second limb is not a loophole, it is the other shape this fix takes. Six
// tools (nova-bus, nova-board, nova-merge, nova-pulse, nova-swarm and the four
// with a shared parseFlags) hand every verb's flag set to one helper that
// answers "usable or not" over stderr and has no way to say "answered, exit 0".
// Threading a stdout through them would touch some eighty call sites to change
// one line of behaviour, so those tools answer at the dispatcher instead, where
// the verb and the stdout are both in hand. Each such package proves the
// behaviour with its own TestEverySubVerbAnswersHelp; this test proves nobody
// added a seventh shape that answers nowhere.
//
// What it cannot see: a function that answers help for one of its verbs and not
// another. That is what the per-package tests are for, and they name every verb.
func TestEverySubVerbFlagSetAnswersHelp(t *testing.T) {
	root := repoRoot(t)
	allow := readSubVerbHelpAllowlist(t)
	seen := map[string]bool{}
	var violations []string

	for _, dir := range []string{"cmd", "internal"} {
		base := filepath.Join(root, dir)
		byPkg, err := flagSetPackages(base, root)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range byPkg {
			for _, f := range pkg.files {
				for _, fn := range f.funcs {
					if !fn.parsesAFlagSet(pkg.names) {
						continue
					}
					if fn.answersHelp || pkg.answersBeforeDispatch {
						continue
					}
					key := f.rel + ":" + fn.name
					seen[key] = true
					if !allow[key] {
						violations = append(violations, fmt.Sprintf(
							"%s:%d: %s parses a sub-verb's flags and never answers flag.ErrHelp, so `--help` there prints `flag: help requested` at exit 2; answer it with cliflags.Help(out, err, cliflags.Usage(usage, <verb>)) and return 0, or answer at the dispatcher with cliflags.Answer",
							f.rel, fn.line, fn.name))
					}
				}
			}
		}
	}
	// The list only shrinks: an entry whose function has stopped offending is a
	// red run too, so nobody can widen the exception set and leave it there.
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but it answers help now (or has left); delete the stale entry -- the list only shrinks",
				subVerbHelpAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

type helpFunc struct {
	name        string
	line        int
	answersHelp bool
	parseOn     map[string]bool // the names this function calls Parse on
}

func (fn helpFunc) parsesAFlagSet(names map[string]bool) bool {
	for n := range fn.parseOn {
		if names[n] {
			return true
		}
	}
	return false
}

type helpFile struct {
	rel   string
	funcs []helpFunc
}

type helpPkg struct {
	names                 map[string]bool // identifiers holding a *flag.FlagSet
	answersBeforeDispatch bool
	files                 []helpFile
}

// flagSetPackages walks base and returns one entry per directory, because the
// flag set a verb parses is routinely built in a different file of the same
// package from the one that parses it.
func flagSetPackages(base, root string) (map[string]*helpPkg, error) {
	pkgs := map[string]*helpPkg{}
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
		if strings.HasPrefix(rel, cliflagsPkgDir+"/") {
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
		dir := filepath.ToSlash(filepath.Dir(rel))
		pkg := pkgs[dir]
		if pkg == nil {
			pkg = &helpPkg{names: map[string]bool{}}
			pkgs[dir] = pkg
		}
		hf := helpFile{rel: rel}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			pkg.names = collectFlagSetNames(fn, pkg.names)
			one := helpFunc{name: helpFuncName(fn), line: fset.Position(fn.Pos()).Line, parseOn: map[string]bool{}}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					if name, ok := parseReceiverName(node); ok {
						one.parseOn[name] = true
					}
					if isHelpAnswer(node) {
						one.answersHelp = true
						if callName(node) == "cliflags.Answer" {
							pkg.answersBeforeDispatch = true
						}
					}
				case *ast.SelectorExpr:
					if x, ok := node.X.(*ast.Ident); ok && x.Name == "flag" && node.Sel.Name == "ErrHelp" {
						one.answersHelp = true
					}
				}
				return true
			})
			hf.funcs = append(hf.funcs, one)
		}
		pkg.files = append(pkg.files, hf)
		return nil
	})
	return pkgs, err
}

// collectFlagSetNames adds every identifier in fn that holds a *flag.FlagSet:
// one assigned from flag.NewFlagSet, and one declared as a parameter of that
// type. The set is kept per package rather than per function on purpose -- the
// `flags` wrapper in five of these binaries builds the set in newFlags and
// parses it in a method on another line.
func collectFlagSetNames(fn *ast.FuncDecl, into map[string]bool) map[string]bool {
	for _, field := range fn.Type.Params.List {
		if isFlagSetPointer(field.Type) {
			for _, name := range field.Names {
				into[name.Name] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		made := false
		for _, rhs := range assign.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok && callName(call) == "flag.NewFlagSet" {
				made = true
			}
		}
		if !made {
			return true
		}
		for _, lhs := range assign.Lhs {
			switch target := lhs.(type) {
			case *ast.Ident:
				into[target.Name] = true
			case *ast.SelectorExpr:
				into[target.Sel.Name] = true
			}
		}
		return true
	})
	return into
}

func isFlagSetPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "flag" && sel.Sel.Name == "FlagSet"
}

// parseReceiverName reports the name a Parse call was made on: `fs` for
// fs.Parse(args) and for f.fs.Parse(args) alike, so a set reached through a
// field is followed. A package-qualified call like time.Parse yields the
// package name, which is never in the flag-set set.
func parseReceiverName(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Parse" {
		return "", false
	}
	switch recv := sel.X.(type) {
	case *ast.Ident:
		return recv.Name, true
	case *ast.SelectorExpr:
		return recv.Sel.Name, true
	}
	return "", false
}

func isHelpAnswer(call *ast.CallExpr) bool {
	switch callName(call) {
	case "cliflags.Help", "cliflags.Answer":
		return true
	}
	return false
}

func callName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name + "." + sel.Sel.Name
}

// helpFuncName is the allowlist's function key: a method name qualified by its
// receiver type, so a name that reads the same on two types is two entries.
func helpFuncName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func readSubVerbHelpAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(subVerbHelpAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		allow[line] = true
	}
	return allow
}
