package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flagUsageLedgerPath is the shrink-only ledger of the flags registered with no
// description: `file:function <sites> <why>`, one shard per package.
const flagUsageLedgerPath = "testdata/flagusage"

// flagUsageRemedy is what a tool's author does to clear a site and its row.
const flagUsageRemedy = "a flag registered with no description; give it a usage string that says what it wants " +
	"(its unit, its role, an example value), as `f.String(\"store\", \"\", \"the store directory\")`; on internal/tool, " +
	"tool.Problems names every such flag (a row's count only falls)"

// flagRegisters is each flag-registering method of package flag (and of a
// *flag.FlagSet, and of internal/tool's Flags, which embeds one): its
// arity, and the positions of its name and its usage.
var flagRegisters = map[string]struct{ arity, name, usage int }{
	"String": {3, 0, 2}, "Bool": {3, 0, 2}, "Int": {3, 0, 2}, "Int64": {3, 0, 2}, "Uint": {3, 0, 2},
	"Uint64": {3, 0, 2}, "Float64": {3, 0, 2}, "Duration": {3, 0, 2},
	"StringVar": {4, 1, 3}, "BoolVar": {4, 1, 3}, "IntVar": {4, 1, 3}, "Int64Var": {4, 1, 3}, "UintVar": {4, 1, 3},
	"Uint64Var": {4, 1, 3}, "Float64Var": {4, 1, 3}, "DurationVar": {4, 1, 3}, "TextVar": {4, 1, 3},
	"Var": {3, 1, 2}, "Func": {3, 0, 1}, "BoolFunc": {3, 0, 1},
}

// undescribedFlag is one registration whose usage is an empty literal.
type undescribedFlag struct {
	fn, name string
	line     int
}

// undescribedFlags finds every call shaped like a flag registration whose usage
// argument is a string literal with nothing in it. A usage built at run time
// (a variable, a concatenation) is not read: it is the caller's to fill.
func undescribedFlags(fset *token.FileSet, f *ast.File) []undescribedFlag {
	var out []undescribedFlag
	visit := func(fn string, n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			if name, ok := undescribedHandFlag(n); ok {
				out = append(out, undescribedFlag{fn, name, fset.Position(n.Pos()).Line})
				return true
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			reg, ok := flagRegisters[sel.Sel.Name]
			if !ok || len(call.Args) != reg.arity {
				return true
			}
			lit, ok := call.Args[reg.usage].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if usage, err := strconv.Unquote(lit.Value); err != nil || strings.TrimSpace(usage) != "" {
				return true
			}
			name := "?"
			if n, ok := call.Args[reg.name].(*ast.BasicLit); ok {
				if s, err := strconv.Unquote(n.Value); err == nil {
					name = s
				}
			}
			out = append(out, undescribedFlag{fn, name, fset.Position(call.Pos()).Line})
			return true
		})
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			fn := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				fn = exprText(fd.Recv.List[0].Type) + "." + fn
			}
			visit(fn, fd)
			continue
		}
		visit("package", d)
	}
	return out
}

// undescribedHandFlag reads a `verbflag.Flag{...}` literal, the flag a verb
// that reads its flags by hand names for its help (verbflag.HelpIfAsked):
// its name, and whether its Wants is missing or an empty literal.
func undescribedHandFlag(n ast.Node) (string, bool) {
	lit, ok := n.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Flag" || exprText(sel.X) != "verbflag" {
		return "", false
	}
	text := func(e ast.Expr) (string, bool) {
		b, ok := e.(*ast.BasicLit)
		if !ok || b.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(b.Value)
		return s, err == nil
	}
	name, wants, built := "?", "", false
	for i, e := range lit.Elts {
		key, v := "", e
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			key, v = exprText(kv.Key), kv.Value
		} else {
			key = []string{"Name", "Wants", "Bool"}[min(i, 2)]
		}
		s, isLit := text(v)
		switch key {
		case "Name":
			if isLit {
				name = s
			}
		case "Wants":
			wants, built = s, !isLit
		}
	}
	return name, !built && strings.TrimSpace(wants) == ""
}

// TestEveryFlagSaysWhatItWants holds X7 of the tool ledger: every flag a tool
// registers has a description, because `<tool> <verb> -h` is all an AI reads
// before it calls the verb, and `--as <string>` alone does not say what it
// wants. It reads every non-test .go file under cmd/ and internal/ (fixtures
// under testdata/ aside) for a flag registration whose usage is an empty
// literal. A site is red unless its `file:function` row covers it.
func TestEveryFlagSaysWhatItWants(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	ledger := newSiteLedger(t, flagUsageLedgerPath)
	files := 0
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		require.NoError(t, f.ParseErr)
		files++
		for _, s := range undescribedFlags(tree.FSet, f.AST) {
			ledger.add(f.Rel+":"+s.fn, fmt.Sprintf("%s:%d (--%s)", f.Rel, s.line, s.name))
		}
	}
	require.NotZero(t, files, "no Go file under cmd/ or internal/; this test would pass by reading nothing")
	for _, v := range ledger.violations(t, flagUsageRemedy) {
		t.Error(v)
	}
}

// TestFlagUsageRuleReadsEveryShape proves the rule over source: each
// registration shape with an empty usage is found, under its function, and a
// described flag, a usage built at run time and a call of another arity are not.
func TestFlagUsageRuleReadsEveryShape(t *testing.T) {
	t.Parallel()
	src := `package p

import ("flag"; "log/slog"; "time")

var top = flag.String("top", "", "")

type t struct{}

func (t) declare(fs *flag.FlagSet, usage string) {
	var s string
	var d time.Duration
	fs.String("a", "", "")
	fs.StringVar(&s, "b", "", "  ")
	fs.Var(nil, "c", "")
	fs.Func("d", "", nil)
	fs.DurationVar(&d, "e", 0, "")
	fs.String("described", "", "the store directory")
	fs.String("built", "", usage)
	fs.Bool("concat", false, "" + usage)
	_ = slog.String("k", "")
	verbflag.HelpIfAsked(nil, "push", verbflag.Flag{Name: "id"}, verbflag.Flag{Name: "as", Wants: " "},
		verbflag.Flag{Name: "ok", Wants: "who pushes"}, verbflag.Flag{"pos", "", false}, verbflag.Flag{Name: "run", Wants: usage})
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)
	var got []string
	for _, s := range undescribedFlags(fset, f) {
		got = append(got, s.fn+":"+s.name)
	}
	assert.Equal(t, []string{"package:top", "t.declare:a", "t.declare:b", "t.declare:c", "t.declare:d", "t.declare:e",
		"t.declare:id", "t.declare:as", "t.declare:pos"}, got)
}
