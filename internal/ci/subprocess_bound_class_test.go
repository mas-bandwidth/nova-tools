package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The class test behind pkg/subproc: a production child has a bound or a
// cancellable context, never a bare exec.Command. It reads the parse tree, never the
// text, so a comment, a string and an aliased import cannot fool it.
//
//   - os/exec is found by its import path, whatever its local name (an alias is
//     followed, a dot import is refused). exec.Command is refused in production code:
//     a one-shot child goes through pkg/subproc (Command, CommandFor) or
//     pkg/gitrun, and a long-lived child through subproc.Long.
//   - exec.CommandContext is refused unless the same function assigns a WaitDelay to a
//     command (an assignment to a `.WaitDelay` field, or a `WaitDelay:` field of a
//     literal): a kill at the deadline alone does not free a caller whose child's own
//     child holds the pipe open. A comment that says WaitDelay is not an assignment.
//   - subproc.Context and subproc.Long are refused with a context.Background(),
//     context.TODO() or nil first argument (literally, or through a local assigned one),
//     because that drops the deadline and the cancel: the caller's context goes in. The
//     long-lived children that have no caller context stand on subprocBackgroundAllowed,
//     a reason each.
//
// pkg/subproc and pkg/gitrun are the doors and are exempt.
const (
	osExecPath   = "os/exec"
	contextPath  = "context"
	subprocPath  = "github.com/mas-bandwidth/nova-tools/pkg/subproc"
	subprocDoor  = "pkg/subproc/"
	gitrunDoor   = "pkg/gitrun/"
	dotImportMsg = "dot import of os/exec hides its calls from this test; import it by name"
)

// subprocBackgroundAllowed names the sites allowed to hand subproc.Context or
// subproc.Long a context.Background(), as "file:Func" with the reason. It is empty: every
// long-lived child derives its context with context.WithCancel and releases it.
var subprocBackgroundAllowed = map[string]string{}

// subprocFindings is every violation in one production file, as "rel: message".
func subprocFindings(rel string, src []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return []string{rel + ": " + err.Error()}
	}
	names := map[string]string{} // import path -> local name
	var out []string
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "." && path == osExecPath {
			out = append(out, rel+": "+dotImportMsg)
		}
		names[path] = local
	}
	execName, ctxName, subName := names[osExecPath], names[contextPath], names[subprocPath]
	at := func(n ast.Node) string { return rel + ":" + strconv.Itoa(fset.Position(n.Pos()).Line) }

	pkgCall := func(n ast.Node, pkg string) (string, *ast.CallExpr) {
		call, ok := n.(*ast.CallExpr)
		if !ok || pkg == "" {
			return "", nil
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", nil
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg {
			return sel.Sel.Name, call
		}
		return "", nil
	}
	isBackground := func(e ast.Expr, locals map[string]bool) bool {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name == "nil" || locals[x.Name]
		case *ast.CallExpr:
			name, _ := pkgCall(x, ctxName)
			return name == "Background" || name == "TODO"
		}
		return false
	}

	check := func(body ast.Node, fn string) {
		setsWaitDelay := false
		locals := map[string]bool{} // idents assigned context.Background()/TODO() directly
		ast.Inspect(body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range s.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "WaitDelay" {
						setsWaitDelay = true
					}
					if id, ok := lhs.(*ast.Ident); ok && i < len(s.Rhs) && len(s.Lhs) == len(s.Rhs) && isBackground(s.Rhs[i], nil) {
						locals[id.Name] = true
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := s.Key.(*ast.Ident); ok && id.Name == "WaitDelay" {
					setsWaitDelay = true
				}
			case *ast.ValueSpec:
				for i, id := range s.Names {
					if i < len(s.Values) && isBackground(s.Values[i], nil) {
						locals[id.Name] = true
					}
				}
			}
			return true
		})
		ast.Inspect(body, func(n ast.Node) bool {
			if name, call := pkgCall(n, execName); call != nil {
				switch name {
				case "Command":
					out = append(out, at(call)+": exec.Command starts a child with no deadline and no context; use subproc.Command (one-shot), gitrun (git) or subproc.Long (long-lived)")
				case "CommandContext":
					if !setsWaitDelay {
						out = append(out, at(call)+": exec.CommandContext in a function that never assigns WaitDelay; use subproc.Context, or assign cmd.WaitDelay so a killed child cannot hang its caller on a pipe")
					}
				}
			}
			if name, call := pkgCall(n, subName); call != nil && (name == "Context" || name == "Long") && len(call.Args) > 0 {
				if isBackground(call.Args[0], locals) && subprocBackgroundAllowed[rel+":"+fn] == "" {
					out = append(out, at(call)+": subproc."+name+" given a context.Background(), context.TODO() or nil: the deadline and the cancel are dropped; pass the caller's context, or derive one with context.WithCancel")
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				check(d.Body, d.Name.Name)
			}
		case *ast.GenDecl:
			check(d, "")
		}
	}
	sort.Strings(out)
	return out
}

func TestEveryChildProcessGoesThroughTheSubprocDoor(t *testing.T) {
	t.Parallel()

	for _, f := range repoTree(t).GoFilesUnder(false, "cmd", "internal", "pkg", "tools") {
		if f.AST == nil || f.HasDirNamed("testdata") {
			continue
		}
		if strings.HasPrefix(f.Rel, subprocDoor) || strings.HasPrefix(f.Rel, gitrunDoor) {
			continue
		}
		for _, finding := range subprocFindings(f.Rel, f.Src) {
			assert.Fail(t, finding)
		}
	}
}

// TestSubprocessClassTestRefusesItsProbes: each shape the rule exists for is red, and
// its neighbour that is fine is green.
func TestSubprocessClassTestRefusesItsProbes(t *testing.T) {
	t.Parallel()

	const head = "package p\n\nimport (\n\t\"context\"\n\t\"os/exec\"\n\n\t\"github.com/mas-bandwidth/nova-tools/pkg/subproc\"\n)\n\n"
	cases := []struct {
		name string
		src  string
		want int // findings
	}{
		{"Context with context.Background", head + "func f() { _ = subproc.Context(context.Background(), \"git\") }", 1},
		{"Long with context.TODO", head + "func f() { _ = subproc.Long(context.TODO(), \"git\") }", 1},
		{"Long with nil", head + "func f() { _ = subproc.Long(nil, \"git\") }", 1},
		{"Long through a local Background", head + "func f() {\n\tctx := context.Background()\n\t_ = subproc.Long(ctx, \"git\")\n}", 1},
		{"Long through a var Background", head + "func f() {\n\tvar ctx = context.Background()\n\t_ = subproc.Long(ctx, \"git\")\n}", 1},
		{"Context with the caller's context", head + "func f(ctx context.Context) { _ = subproc.Context(ctx, \"git\") }", 0},
		{"Long with a derived context", head + "func f() {\n\tctx, stop := context.WithCancel(context.Background())\n\tdefer stop()\n\t_ = subproc.Long(ctx, \"git\")\n}", 0},
		{"Background to subproc.Command is fine", head + "func f() { _, c := subproc.Command(context.Background(), subproc.Git, \"git\"); c() }", 0},
		{"WaitDelay only in a comment", head + "func f(ctx context.Context) {\n\t// WaitDelay is set below\n\t_ = exec.CommandContext(ctx, \"git\")\n}", 1},
		{"WaitDelay only in a string", head + "func f(ctx context.Context) {\n\t_ = \"cmd.WaitDelay = 1\"\n\t_ = exec.CommandContext(ctx, \"git\")\n}", 1},
		{"WaitDelay assigned in another function", head + "func f(ctx context.Context) { _ = exec.CommandContext(ctx, \"git\") }\nfunc g(c *exec.Cmd) { c.WaitDelay = 1 }", 1},
		{"WaitDelay assigned beside it", head + "func f(ctx context.Context) {\n\tc := exec.CommandContext(ctx, \"git\")\n\tc.WaitDelay = 1\n}", 0},
		{"WaitDelay in a literal", head + "func f(ctx context.Context) {\n\tc := exec.CommandContext(ctx, \"git\")\n\t_ = exec.Cmd{WaitDelay: 1}\n\t_ = c\n}", 0},
		{"bare exec.Command", head + "func f() { _ = exec.Command(\"git\") }", 1},
		{"aliased os/exec, Command", strings.Replace(head, "\"os/exec\"", "x \"os/exec\"", 1) + "func f() { _ = x.Command(\"git\") }", 1},
		{"aliased os/exec, CommandContext without WaitDelay", strings.Replace(head, "\"os/exec\"", "x \"os/exec\"", 1) + "func f(ctx context.Context) { _ = x.CommandContext(ctx, \"git\") }", 1},
		{"dot import of os/exec", strings.Replace(head, "\"os/exec\"", ". \"os/exec\"", 1) + "func f() {}", 1},
		{"aliased subproc and context", "package p\n\nimport (\n\tc \"context\"\n\tsp \"github.com/mas-bandwidth/nova-tools/pkg/subproc\"\n)\n\nfunc f() { _ = sp.Long(c.Background(), \"git\") }", 1},
	}
	for _, c := range cases {
		got := subprocFindings("internal/probe/probe.go", []byte(c.src))
		assert.Equal(t, c.want, len(got), "%s: %d findings, want %d: %v", c.name, len(got), c.want, got)
	}
}
