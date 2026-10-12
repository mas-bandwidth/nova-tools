package ci

import (
	"go/ast"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: ONE MEMBER. THE FLEET RUNS THE ONE MEMBER LOOP.
//
// A sprint has one member loop: beat, queue, push, judge the finish, take to
// width, each card a child. That loop is owned by nova-tools in pkg/member
// (docs/one-member-ownership.md). `nova-swarm member` is the thin command over
// it: it builds a member.Member and hands it a Runner, a Pusher and a Sprint,
// and carries no loop of its own (cmd/nova-swarm/member.go, memberLoop drives
// pkg/member's Tick and Beat).
//
// The 2026-10-05 split copied the shared packages, pkg/member among them, into
// nova-sprint's own tree; the two copies then drifted, so a member fix in one
// repo never reached the fleet (docs/SPLIT-NOVA-SPRINT.md, section b). This
// rule is the guard on the package graph that keeps the drift from coming back
// into nova-tools: exactly one package of this module owns the loop, and it is
// pkg/member. A second package that declares a member loop is a refusal, naming
// it, so a copy landed back here is red on the change that lands it.

// onememberModule is this module's import path prefix.
const onememberModule = "github.com/mas-bandwidth/nova-tools/"

// onememberLoop is the one package that owns the member loop.
const onememberLoop = "pkg/member"

// onememberDefinesLoop reports whether f, a non-test Go file, declares a member
// loop: a struct type named Member that carries a Tick method (the member's
// pass). A table member (pkg/ntable) has a Member type but no Tick, so it is
// not a loop; a member loop always carries Tick.
func onememberDefinesLoop(f *treeFile) bool {
	if f.AST == nil || f.ParseErr != nil || f.Test {
		return false
	}
	var memberType bool
	for _, decl := range f.AST.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "type" {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Member" {
				continue
			}
			if _, ok := ts.Type.(*ast.StructType); ok {
				memberType = true
			}
		}
	}
	if !memberType {
		return false
	}
	for _, decl := range f.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Tick" || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		recv := fn.Recv.List[0].Type
		star, ok := recv.(*ast.StarExpr)
		if !ok {
			continue
		}
		id, ok := star.X.(*ast.Ident)
		if ok && id.Name == "Member" {
			return true
		}
	}
	return false
}

// onememberPackages is every package directory of this module whose non-test
// files declare a member loop.
func onememberPackages(t *testing.T) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for _, f := range repoTree(t).Files {
		if !f.Go || f.Test || f.HasDirNamed("testdata") {
			continue
		}
		if !onememberDefinesLoop(f) {
			continue
		}
		dir := path.Dir(f.Rel)
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

// TestOneMemberLoop is the guard: exactly one package of this module owns the
// member loop, and it is pkg/member. A second package that declares a member
// loop is named, so a copy of the loop landed back into nova-tools is red on
// the change that lands it.
func TestOneMemberLoop(t *testing.T) {
	t.Parallel()
	packages := onememberPackages(t)
	assert.Equal(t, []string{onememberLoop}, packages,
		"the member loop is owned by exactly one package, %s; found %v: a second member loop is a refusal, delete the copy and keep the one in pkg/member (docs/one-member-ownership.md)",
		onememberLoop, packages)
}

// TestFleetCommandConsumesTheOneLoop pins the thin-command half of the rule:
// cmd/nova-swarm (the fleet's `nova-swarm member`) imports pkg/member, so the
// loop the fleet runs is the one this rule guards, never a loop of its own.
func TestFleetCommandConsumesTheOneLoop(t *testing.T) {
	t.Parallel()
	want := onememberModule + onememberLoop
	found := false
	for _, f := range repoTree(t).Files {
		if !f.Go || f.Test || path.Dir(f.Rel) != "cmd/nova-swarm" || f.AST == nil || f.ParseErr != nil {
			continue
		}
		for _, im := range f.AST.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			require.NoError(t, err, f.Rel)
			if strings.TrimSuffix(p, "/") == want {
				found = true
			}
		}
	}
	assert.True(t, found, "cmd/nova-swarm must run the one member loop by importing %s; a member loop written inside the command is a second copy and is a refusal (docs/one-member-ownership.md)", want)
}
