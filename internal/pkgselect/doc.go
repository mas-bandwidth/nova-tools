// Package pkgselect chooses the Go packages a CI run tests and deals them to
// the legs that test them. It is the one home of four rules that every place
// choosing packages reads:
//
//   - a deprecated package is never tested: deprecated/PACKAGES names the
//     packages that are deprecated and still in the tree because living tools
//     import them, and Deprecated.Live drops them from any list (live.go);
//   - a selection is never silently nothing: a failed `go list`, or a Go diff
//     that selects zero packages, is an error or an announced whole-tree
//     fallback, never an empty answer (select.go);
//   - the heavy packages are dealt first, one to a shard, and every other live
//     package round-robin after them (deal.go);
//   - the CI fan-out is the selection dealt onto the runner groups, with the
//     macOS legs kept for the code that differs on macOS (matrix.go).
//
// Everything that starts a process goes through a Runner, so a test answers
// `git` and `go list` from a table and never starts either. The verbs in
// tools/ci and the local run in cmd/nova-ci both import this package.
package pkgselect

import (
	"os"
	"path/filepath"
	"strings"
)

// ModulePath reads the module path from root's go.mod: the import-path prefix
// of every package of this module. A package listed by import path is written
// ./<rest> everywhere else. A root with no go.mod, or one with no module line,
// has no prefix ("").
func ModulePath(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}

// Result is what one command printed and how it ended.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Runner runs argv in dir with env (KEY=VALUE, added to the process's own) and
// returns its output and exit status. The error is only for a command that
// could not be started; a non-zero exit is a Result with that Code.
type Runner func(dir string, env []string, argv ...string) (Result, error)

// lines splits s into its lines, dropping none: a trailing newline does not
// make a final empty line.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}
