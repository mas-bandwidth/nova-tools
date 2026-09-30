package ci

import (
	"go/ast"
	"strings"
	"testing"
)

// TestEveryChildProcessGoesThroughTheSubprocDoor is the class test behind
// internal/subproc: a production child has a bound or a cancellable context, and never
// a bare exec.Command.
//
//   - exec.Command is refused in production code (cmd, internal, tools, tests excluded):
//     a one-shot child goes through internal/subproc (Command, CommandFor) or
//     internal/gitrun, which carry the deadline and WaitDelay, and a long-lived child goes
//     through subproc.Long, which carries a cancellable context and no deadline.
//   - exec.CommandContext is allowed only in a file that also sets WaitDelay, because a
//     kill at the deadline alone does not free a caller whose child's own child holds the
//     pipe open.
//
// internal/subproc and internal/gitrun are the doors and are exempt.
func TestEveryChildProcessGoesThroughTheSubprocDoor(t *testing.T) {
	t.Parallel()

	for _, f := range repoTree(t).GoFilesUnder(false, "cmd", "internal", "tools") {
		if f.AST == nil || f.HasDirNamed("testdata") {
			continue
		}
		if strings.HasPrefix(f.Rel, "internal/subproc/") || strings.HasPrefix(f.Rel, "internal/gitrun/") {
			continue
		}
		setsWaitDelay := strings.Contains(string(f.Src), "WaitDelay")
		ast.Inspect(f.AST, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			switch sel.Sel.Name {
			case "Command":
				t.Errorf("%s: exec.Command starts a child with no deadline and no context; use subproc.Command (one-shot), gitrun (git) or subproc.Long (long-lived)", f.Rel)
			case "CommandContext":
				if !setsWaitDelay {
					t.Errorf("%s: exec.CommandContext in a file that never sets WaitDelay; use subproc.Context, or set cmd.WaitDelay so a killed child cannot hang its caller on a pipe", f.Rel)
				}
			}
			return true
		})
	}
}
