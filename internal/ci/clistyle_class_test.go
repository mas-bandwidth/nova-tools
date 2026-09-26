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

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// THE CLASS RULE: ONE GRAMMAR FOR EVERY nova-* TOOL (nova-tools#4352 A;
// docs/STYLE-CLI.md).
//
// `<tool> <noun> <verb> [--flags] [positionals]`: the same flag means the
// same thing on every verb (--redis, --sprint, --as, --ids, --stream, --why,
// --n, --to, ...: internal/nsprint/verbflag.Vocabulary, one help line each),
// a spelling the grammar retired (--actor, --by, --id, --reason, --streams,
// --store, ...: verbflag.Retired) is refused naming the new one and never
// defined again, every flag carries a one-line help, no verb defines two
// spellings of one concept, and a named object (a worker, a card, a stream,
// a sprint, a sha) is a flag, never a positional: positionals are files and
// what follows `--`. The morning of 2026-09-26 measured eight refusals from
// spelling before the first card landed.
//
// The test walks every cmd/nova-* package (and the internal packages a tool
// imports that build a flag set for it) and reads every flag definition off
// its flag sets from the source: the flag package's definers have one shape
// (String/Bool/Int/Duration/Func: name, default, help; the *Var forms: target,
// name, default, help; Var: target, name, help), so a definition is found
// without running the tool. A finding is `tool rule verb flag`; the rules:
//
//	empty-help   a flag whose help is ""
//	retired      a flag spelled a retired way (verbflag.Retired)
//	vocabulary   a vocabulary flag whose help is not its one verbflag.Help constant
//	alias        two flag names bound to one variable in one flag set
//	root-flags   flags on the package's default set: a tool with no noun and verb
//	positional   a verb that reads a positional (Args/Arg) and is not in positionalVerbs
//
// nova-sprint is held to the grammar with no exception. The other tools'
// findings of today are in testdata/cli-style_allowlist.txt, one row each, a
// list that only shrinks: an unlisted finding is red, and a listed finding
// that has left is a stale row and also red, so each tool comes over one card
// at a time and never drifts back.
const cliStyleAllowlistPath = "testdata/cli-style_allowlist.txt"

// cliStyleToolsRoot is where the tools live; every nova-* directory is a tool.
const cliStyleToolsRoot = "cmd"

// positionalVerbs are the verbs the grammar lets read positionals, with what
// they are: files, or what follows `--`. Keyed by tool and the function that
// defines the verb's flags (the walker's verb key).
var positionalVerbs = map[string]string{
	"nova-sprint\tcmdCardPush":          "the card files to push (or --dir, or --stdin)",
	"nova-sprint\trunResultCheck":       "the result file, or - for stdin",
	"nova-sprint\trunResultDisposition": "the result file, or - for stdin",
	"nova-sprint\trunFleetBuild":        "set's key=value pairs, after the flags",
	"nova-sprint\tredisRaw":             "the redis command, after the flags (or after --)",
}

// cliStyleOptions: every row is its own key (tool, rule, verb, flag), and the
// list only shrinks (NOVA_CI_UPDATE=1 drops stale rows, never adds one).
var cliStyleOptions = allowlist.Options{Key: allowlist.WholeRow, Ceiling: true}

// cliVocabularyConst names the verbflag constant each vocabulary flag's help
// must be spelled with, so the help is the same on every verb by construction.
var cliVocabularyConst = map[string]string{
	"redis": "HelpRedis", "sprint": "HelpSprint", "as": "HelpAs", "ids": "HelpIDs", "stream": "HelpStream",
	"why": "HelpWhy", "n": "HelpN", "to": "HelpTo", "idem": "HelpIdem", "dry-run": "HelpDryRun",
	"repo": "HelpRepo", "pr": "HelpPR", "from": "HelpFrom", "since": "HelpSince", "json": "HelpJSON",
}

// cliDefiners is the shape of every flag definer: the index of the name
// argument and the number of arguments.
var cliDefiners = map[string]struct{ nameAt, argc int }{
	"String": {0, 3}, "Bool": {0, 3}, "Int": {0, 3}, "Int64": {0, 3}, "Uint": {0, 3}, "Uint64": {0, 3},
	"Float64": {0, 3}, "Duration": {0, 3}, "Func": {0, 3}, "BoolFunc": {0, 3},
	"Var":       {1, 3},
	"StringVar": {1, 4}, "BoolVar": {1, 4}, "IntVar": {1, 4}, "Int64Var": {1, 4}, "UintVar": {1, 4},
	"Uint64Var": {1, 4}, "Float64Var": {1, 4}, "DurationVar": {1, 4}, "TextVar": {1, 4},
}

type cliFinding struct {
	tool, rule, verb, what string
	site                   string
}

func (f cliFinding) key() string { return f.tool + "\t" + f.rule + "\t" + f.verb + "\t" + f.what }

// cliFlagTable is the flag table the walk builds: one row per definition.
type cliFlagRow struct{ tool, verb, name, help string }

func TestCLIStyleOneGrammar(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	rows, findings := cliStyleWalk(t, root)
	if len(rows) == 0 {
		t.Fatal("no flag definitions found under cmd/nova-*: the walker is broken")
	}
	list := loadAllowlist(t, filepath.Join(root, "internal", "ci", cliStyleAllowlistPath), cliStyleOptions)

	measured := map[string]bool{}
	site := map[string]string{}
	for _, f := range findings {
		if f.tool == "nova-sprint" {
			t.Errorf("outside the one grammar: %s\t# %s (nova-sprint takes no allowlist row: fix the verb)", f.key(), f.site)
			continue
		}
		measured[f.key()] = true
		site[f.key()] = f.site
	}
	res := allowlist.Check(t, list, measured)
	for _, k := range res.Unlisted {
		t.Errorf("outside the one grammar (an unlisted finding; the list only shrinks): %s\t# %s", k, site[k])
	}
	for _, row := range res.Stale {
		t.Errorf("stale allowlist row (the finding has left; delete it, or NOVA_CI_UPDATE=1 drops it): %s", strings.ReplaceAll(row.Key, "\t", " "))
	}
	t.Logf("flag table: %d definitions, %d distinct flag names, %d findings, %d allowlisted",
		len(rows), cliDistinctNames(rows), len(findings), list.Len())
}

// TestCLIStyleAllowlistHasNoSprintRows pins the DONE-WHEN: nova-sprint is on
// the grammar entirely, so a row for it can never be added.
func TestCLIStyleAllowlistHasNoSprintRows(t *testing.T) {
	t.Parallel()
	list := loadAllowlist(t, filepath.Join(repoRoot(t), "internal", "ci", cliStyleAllowlistPath), cliStyleOptions)
	for _, row := range list.Rows() {
		if strings.HasPrefix(row.Key, "nova-sprint\t") {
			t.Errorf("nova-sprint row in the allowlist: %s", strings.ReplaceAll(row.Key, "\t", " "))
		}
	}
}

// TestCLIStyleRetiredSpellingsMapToVocabulary: every retired spelling maps to
// a current one that is either in the vocabulary or a plain flag, never to
// another retired spelling.
func TestCLIStyleRetiredSpellingsMapToVocabulary(t *testing.T) {
	t.Parallel()
	for old, now := range verbflag.Retired {
		if _, again := verbflag.Retired[now]; again {
			t.Errorf("--%s is spelled --%s, itself retired", old, now)
		}
	}
	for name := range verbflag.Vocabulary {
		if _, retired := verbflag.Retired[name]; retired {
			t.Errorf("--%s is both vocabulary and retired", name)
		}
		if _, ok := cliVocabularyConst[name]; !ok {
			t.Errorf("vocabulary flag --%s has no constant name in cliVocabularyConst", name)
		}
	}
}

func cliDistinctNames(rows []cliFlagRow) int {
	names := map[string]bool{}
	for _, r := range rows {
		names[r.name] = true
	}
	return len(names)
}

// cliStyleWalk reads every tool's flag definitions and returns the table and
// the findings.
func cliStyleWalk(t *testing.T, root string) ([]cliFlagRow, []cliFinding) {
	t.Helper()
	modulePath := "github.com/mas-bandwidth/nova-tools/"
	dirs, err := filepath.Glob(filepath.Join(root, cliStyleToolsRoot, "nova-*"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []cliFlagRow
	var findings []cliFinding
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		tool := filepath.Base(dir)
		pkgs := []string{dir}
		// the internal packages this tool imports that build a flag set
		for _, imp := range cliImports(t, dir) {
			if !strings.HasPrefix(imp, modulePath+"internal/") {
				continue
			}
			idir := filepath.Join(root, strings.TrimPrefix(imp, modulePath))
			if cliBuildsFlagSet(idir) {
				pkgs = append(pkgs, idir)
			}
		}
		for _, p := range pkgs {
			r, f := cliWalkPackage(t, root, tool, p)
			rows = append(rows, r...)
			findings = append(findings, f...)
		}
	}
	return rows, findings
}

func cliGoFiles(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var files []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, n))
	}
	sort.Strings(files)
	return files
}

func cliImports(t *testing.T, dir string) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	fset := token.NewFileSet()
	for _, path := range cliGoFiles(dir) {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, imp := range f.Imports {
			v, _ := strconv.Unquote(imp.Path.Value)
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

func cliBuildsFlagSet(dir string) bool {
	for _, path := range cliGoFiles(dir) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := string(raw)
		if strings.Contains(s, "verbflag.New(") || strings.Contains(s, "flag.NewFlagSet(") {
			return true
		}
	}
	return false
}

// cliWalkPackage reads one package's non-test files.
func cliWalkPackage(t *testing.T, root, tool, dir string) ([]cliFlagRow, []cliFinding) {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range cliGoFiles(dir) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		files = append(files, f)
	}
	// wrappers: package functions whose body constructs a flag set (taskFlags,
	// lifeFlags, capacityFlags, newWSCmd, ...): a call to one is a construction.
	wrappers := map[string]bool{"verbflag.New": true, "flag.NewFlagSet": true}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			found := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok && (cliCallName(c) == "verbflag.New" || cliCallName(c) == "flag.NewFlagSet") {
					found = true
				}
				return !found
			})
			if found {
				wrappers[fd.Name.Name] = true
			}
		}
	}
	var rows []cliFlagRow
	var findings []cliFinding
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			r, fs := cliWalkFunc(fset, root, tool, fd, wrappers)
			rows = append(rows, r...)
			findings = append(findings, fs...)
		}
	}
	return rows, findings
}

func cliCallName(c *ast.CallExpr) string {
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name + "." + fn.Sel.Name
		}
		return "." + fn.Sel.Name
	}
	return ""
}

func cliExprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return cliExprText(v.X) + "." + v.Sel.Name
	case *ast.UnaryExpr:
		return v.Op.String() + cliExprText(v.X)
	case *ast.BasicLit:
		return v.Value
	case *ast.CallExpr:
		return cliCallName(v) + "(...)"
	case *ast.IndexExpr:
		return cliExprText(v.X) + "[" + cliExprText(v.Index) + "]"
	}
	return fmt.Sprintf("%T", e)
}

// cliWalkFunc reads one function: its flag set constructions (the verb name
// when the construction names it with a literal), its flag definitions and
// its positional reads.
func cliWalkFunc(fset *token.FileSet, root, tool string, fd *ast.FuncDecl, wrappers map[string]bool) ([]cliFlagRow, []cliFinding) {
	verbOf := map[string]string{} // receiver text -> verb literal
	constructs := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range as.Rhs {
			c, ok := rhs.(*ast.CallExpr)
			if !ok || !wrappers[cliCallName(c)] {
				continue
			}
			constructs = true
			verb := ""
			if len(c.Args) > 0 {
				if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					verb, _ = strconv.Unquote(lit.Value)
				}
			}
			if i < len(as.Lhs) {
				verbOf[cliExprText(as.Lhs[i])] = verb
			}
		}
		return true
	})
	verbKey := func(recv string) string {
		if v := verbOf[recv]; v != "" {
			return v
		}
		return fd.Name.Name
	}
	site := func(n ast.Node) string {
		pos := fset.Position(n.Pos())
		rel, _ := filepath.Rel(root, pos.Filename)
		return filepath.ToSlash(rel) + ":" + strconv.Itoa(pos.Line)
	}
	var rows []cliFlagRow
	var findings []cliFinding
	add := func(rule, verb, what string, n ast.Node) {
		findings = append(findings, cliFinding{tool: tool, rule: rule, verb: verb, what: what, site: site(n)})
	}
	targets := map[string]string{} // receiver + var-target text -> first flag name bound to it
	defined := false
	// parents lets a positional read see what it sits in.
	var stack []ast.Node
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv := cliExprText(sel.X)
		// positional reads: Args() and Arg(i) used as values
		if (sel.Sel.Name == "Args" && len(c.Args) == 0) || (sel.Sel.Name == "Arg" && len(c.Args) == 1) {
			if (constructs || defined || recv == "flag") && cliIsValueUse(stack) {
				key := tool + "\t" + fd.Name.Name
				if _, ok := positionalVerbs[key]; !ok {
					add("positional", verbKey(recv), fd.Name.Name, n)
				}
			}
			return true
		}
		shape, ok := cliDefiners[sel.Sel.Name]
		if !ok || len(c.Args) != shape.argc {
			return true
		}
		lit, ok := c.Args[shape.nameAt].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if strings.Contains(recv, "strings") || strings.Contains(recv, "strconv") || recv == "fmt" || recv == "time" {
			return true
		}
		name, _ := strconv.Unquote(lit.Value)
		defined = true
		helpExpr := c.Args[len(c.Args)-1]
		help := ""
		if hl, ok := helpExpr.(*ast.BasicLit); ok && hl.Kind == token.STRING {
			help, _ = strconv.Unquote(hl.Value)
		} else {
			help = cliExprText(helpExpr)
		}
		verb := verbKey(recv)
		if recv == "flag" {
			verb = "(root)"
			add("root-flags", verb, name, n)
		}
		rows = append(rows, cliFlagRow{tool: tool, verb: verb, name: name, help: help})
		if hl, ok := helpExpr.(*ast.BasicLit); ok && hl.Value == `""` {
			add("empty-help", verb, name, n)
		}
		if now, retired := verbflag.Retired[name]; retired {
			add("retired", verb, name+" (spelled --"+now+")", n)
		}
		if want, ok := cliVocabularyConst[name]; ok && cliExprText(helpExpr) != "verbflag."+want {
			add("vocabulary", verb, name+" (help is verbflag."+want+")", n)
		}
		if shape.nameAt == 1 {
			tk := recv + "\x00" + cliExprText(c.Args[0])
			if first, dup := targets[tk]; dup && first != name {
				add("alias", verb, name+" (an alias of --"+first+")", n)
			} else if !dup {
				targets[tk] = name
			}
		}
		return true
	})
	return rows, findings
}

// cliIsValueUse says whether the Args()/Arg(i) call at the top of stack is
// read as a value (assigned, ranged, indexed, passed on) rather than only
// measured in a condition or quoted in a refusal.
func cliIsValueUse(stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch p := stack[i].(type) {
		case *ast.IfStmt, *ast.BinaryExpr:
			return false
		case *ast.CallExpr:
			name := cliCallName(p)
			for _, refusal := range []string{"len", "refuse", "Refuse", "Quote", "Join", "Sprintf", "Errorf", "Fprintf", "Fprintln", "Sprint", "append", "Cap", "Escape"} {
				if strings.HasSuffix(name, refusal) || strings.HasSuffix(name, "."+refusal) {
					return false
				}
			}
			return true
		case *ast.AssignStmt, *ast.RangeStmt, *ast.IndexExpr, *ast.ReturnStmt, *ast.CompositeLit, *ast.KeyValueExpr:
			return true
		case *ast.ExprStmt, *ast.BlockStmt, *ast.FuncDecl:
			return false
		}
	}
	return false
}
