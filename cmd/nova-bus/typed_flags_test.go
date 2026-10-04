package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typedFlag is one line of a verb's -h for a flag whose value is parsed: a number or a duration.
var typedFlag = regexp.MustCompile(`(?m)^  --([a-z][a-z-]*) <(int|int64|uint|uint64|float64|duration)>  `)

// typedBase is, per verb, the flags that make an invocation otherwise whole, so the one bad
// value given beside them is the only problem the run can name.
var typedBase = map[string][]string{
	"draft":   {"--as", "Ada", "--to", "Bo"},
	"prepare": {"--as", "Ada", "--stdin"},
	"send":    {"--stdin", "--as", "Ada", "--remote", "origin", "--branch", "main"},
	"reply":   {"--as", "Ada", "--re", "x", "--file", "f", "--remote", "origin", "--branch", "main"},
	"inbox":   {"--as", "Ada", "--receipt-max-words", "3"},
	"receipt": {"--as", "Ada", "--note", "x", "--remote", "origin", "--branch", "main"},
	"close":   {"--as", "Ada", "--before", "2026-09-01T00:00:00Z"},
	"wait":    {"--as", "Ada", "--receipt-max-words", "3", "--timeout", "1m", "--remote", "origin", "--branch", "main"},
	"check":   {"--full"},
	"names":   nil,
}

// Every verb refuses a value its typed flag cannot take, names the flag, and does not run:
// exit 2, nothing on stdout, the bus directory byte for byte what it was. Every number and
// duration flag each verb's -h lists is given "abc", and every instant flag a value that
// is no instant; a parse that dropped the error would run the verb on the flag's default.
func TestEveryTypedFlagRefusesABadValueAndTheVerbDoesNotRun(t *testing.T) {
	t.Parallel()
	type tcase struct{ verb, flag, value, want string }
	var cases []tcase
	for _, verb := range verbs {
		_, ok := typedBase[verb]
		if !ok {
			continue // version takes no flags
		}
		help := invoke(t, "", verb, "-h").mustCode(t, 0).stdout
		for _, m := range typedFlag.FindAllStringSubmatch(help, -1) {
			cases = append(cases, tcase{verb, m[1], "abc", "invalid value for --" + m[1]})
		}
	}
	for _, c := range []tcase{
		{"close", "before", "not-an-instant", "--before"},
		{"wait", "until", "not-an-instant", "--until"},
		{"inbox", "legacy-before", "not-an-instant", "--legacy-before"},
		{"wait", "legacy-before", "not-an-instant", "--legacy-before"},
		{"check", "legacy-before", "not-an-instant", "--legacy-before"},
	} {
		cases = append(cases, c)
	}
	covered := map[string]int{}
	for _, c := range cases {
		covered[c.verb]++
	}
	for _, verb := range []string{"draft", "send", "reply", "inbox", "receipt", "close", "wait", "check"} {
		require.NotZero(t, covered[verb], "no typed flag of %s was read from its -h; the reader is broken", verb)
	}
	for _, c := range cases {
		t.Run(c.verb+" --"+c.flag, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "bus")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			roster := `{"participants":[{"name":"Ada","lane":"from-ada","git_name":"Ada","git_email":"ada@example.com"},{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"}]}`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(roster), 0o644))
			before := snapshot(t, dir)
			args := append([]string{c.verb, "--bus", dir}, typedBase[c.verb]...)
			for i := 0; i+1 < len(args); i++ { // the bad value replaces a base value of the same flag
				if args[i] == "--"+c.flag {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			args = append(args, "--"+c.flag, c.value)
			r := invoke(t, "", args...)
			assert.Equal(t, 2, r.code, "exit; stderr:\n%s\nstdout:\n%s", r.stderr, r.stdout)
			assert.Contains(t, r.stderr, strings.ToUpper(c.verb)+" REFUSED: ")
			assert.Contains(t, r.stderr, c.want, "the refusal does not name --%s", c.flag)
			assert.Empty(t, r.stdout, "the verb ran")
			assert.Equal(t, before, snapshot(t, dir), "the verb wrote into the bus")
		})
	}
}

// snapshot is every file under dir with its bytes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	}))
	return out
}

// No return reads a variable beside a call that may change it. Go leaves the order of a
// plain operand and a call in one expression list unspecified: `return bad, parse()`, where
// parse appends to bad through a pointer, may return bad as it was before the call. The
// shape is a return (or an assignment) whose list holds an identifier whose address the
// function stores, or which a closure it stores captures, beside a call; the fix is to call
// first and return after. Every non-test file of this package and internal/bus is read.
func TestNoReturnReadsAVariableBesideACallThatMayChangeIt(t *testing.T) {
	t.Parallel()
	var files []string
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "bus")} {
		got, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		for _, f := range got {
			if !strings.HasSuffix(f, "_test.go") {
				files = append(files, f)
			}
		}
	}
	require.Greater(t, len(files), 20, "the sweep read too few files")
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		require.NoError(t, err)
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			shared := sharedVars(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				var list []ast.Expr
				switch x := n.(type) {
				case *ast.ReturnStmt:
					list = x.Results
				case *ast.AssignStmt:
					list = x.Rhs
				default:
					return true
				}
				if len(list) < 2 {
					return true
				}
				hasCall := false
				for _, e := range list {
					ast.Inspect(e, func(m ast.Node) bool {
						if _, ok := m.(*ast.CallExpr); ok {
							hasCall = true
						}
						return !hasCall
					})
				}
				for _, e := range list {
					if id, ok := e.(*ast.Ident); ok && hasCall && shared[id.Name] {
						assert.Fail(t, "a variable read beside a call that may change it",
							"%s: %s reads %s in the same list as a call, and %s is shared (its address is stored, or a stored closure captures it); call first, then return",
							fset.Position(n.Pos()), fn.Name.Name, id.Name, id.Name)
					}
				}
				return true
			})
		}
	}
}

// sharedVars is the names whose address fn stores (in a struct, a slice, a variable) or
// that a closure fn stores refers to: the variables a later call may change behind a plain
// read of them. An address or a closure handed straight to a call as an argument
// (dec.Decode(&v), sort.Slice(s, func...)) is used by that call and not kept, so it does
// not count; true, false and nil are no variables.
func sharedVars(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	direct := map[ast.Node]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			for _, a := range c.Args {
				direct[a] = true
			}
		}
		return true
	})
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.UnaryExpr:
			if id, ok := x.X.(*ast.Ident); ok && x.Op == token.AND && !direct[x] {
				out[id.Name] = true
			}
		case *ast.FuncLit:
			if direct[x] {
				return true // its body is still read for stored addresses
			}
			ast.Inspect(x.Body, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					out[id.Name] = true
				}
				return true
			})
		}
		return true
	})
	for _, predeclared := range []string{"true", "false", "nil"} {
		delete(out, predeclared)
	}
	return out
}
