// Package guard is the merged-tree guard suite (nova-tools #3629): the class
// checks that caught the 2026-09-24 interaction breaks, PRs green alone and
// red together, run on one merged tree BEFORE its batch test. It is a
// library: any lander calls Run with a repository path and prints one
// PASS/FAIL row per guard, the offending file named. Nothing here reads
// Redis or the network; a guard reads the tree and, for the tracked-file
// guard, `git ls-files` in it.
//
// The registry is the list below. The next interaction break is a row here,
// not a hunt.
package guard

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Result is one guard's verdict on one tree.
type Result struct {
	Name string
	OK   bool
	// File is the offending path, repository-relative, or "" on PASS.
	File string
	// Why is the one-line reason on FAIL, or a short measure on PASS.
	Why string
	// Err is set when the guard could not run (a missing tool, an
	// unreadable tree); it is reported as FAIL with the error as Why.
	Err error
}

// Row is the one line a lander prints per guard: `PASS <name> <why>` or
// `FAIL <name> <file>: <why>`.
func (r Result) Row() string {
	if r.Err != nil {
		return fmt.Sprintf("FAIL %s %s: %v", r.Name, orDash(r.File), r.Err)
	}
	if r.OK {
		return strings.TrimRight(fmt.Sprintf("PASS %s %s", r.Name, r.Why), " ")
	}
	return fmt.Sprintf("FAIL %s %s: %s", r.Name, orDash(r.File), r.Why)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Guard is one registered check over a tree at root.
type Guard struct {
	Name string
	// What is the interaction the guard closes, for `guard ls`.
	What string
	Run  func(ctx context.Context, root string) Result
}

// Registry is every guard in run order. Names are stable: a lander's table
// and a stream PR body quote them.
var Registry = []Guard{
	{Name: "lua-locals", What: "every lua file under the active-locals limit (dev red 8d12fb19, #3487)", Run: luaLocals},
	{Name: "lua-crossfile", What: "no bare cross-file Lua name, no global write, NS exports before reads (dev red a42285c5, #3606)", Run: luaCrossFile},
	{Name: "one-parser", What: "typed-line parsers only in the allow list (#3497 met #3092)", Run: oneParser},
	{Name: "catalog", What: "the committed AGENTS.md map matches the tree (make map)", Run: catalog},
	{Name: "named-paths", What: "every repository path a comment or doc names exists", Run: namedPaths},
	{Name: "tracked-files", What: "no tracked scratch file and no tracked file over the size cap", Run: trackedFiles},
}

// Names lists the registry in run order.
func Names() []string {
	out := make([]string, 0, len(Registry))
	for _, g := range Registry {
		out = append(out, g.Name)
	}
	return out
}

// Run runs every guard (or only the named ones) over the tree at root and
// returns one Result per guard in registry order. An unknown name is a
// Result with Err, so a typo in a lander's list is a FAIL row, never a
// silently skipped guard.
func Run(ctx context.Context, root string, only ...string) []Result {
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	some := len(want) > 0
	var out []Result
	for _, g := range Registry {
		if some && !want[g.Name] {
			continue
		}
		delete(want, g.Name)
		if ctx.Err() != nil {
			out = append(out, Result{Name: g.Name, Err: ctx.Err()})
			continue
		}
		r := g.Run(ctx, root)
		r.Name = g.Name
		out = append(out, r)
	}
	unknown := make([]string, 0, len(want))
	for n := range want {
		unknown = append(unknown, n)
	}
	sort.Strings(unknown)
	for _, n := range unknown {
		out = append(out, Result{Name: n, Err: fmt.Errorf("no such guard; want one of %s", strings.Join(Names(), ","))})
	}
	return out
}

// AllOK reports whether every result passed.
func AllOK(rs []Result) bool {
	for _, r := range rs {
		if r.Err != nil || !r.OK {
			return false
		}
	}
	return true
}

// Print writes one row per result and returns the number that failed.
func Print(w io.Writer, rs []Result) int {
	failed := 0
	for _, r := range rs {
		fmt.Fprintln(w, r.Row())
		if r.Err != nil || !r.OK {
			failed++
		}
	}
	return failed
}
