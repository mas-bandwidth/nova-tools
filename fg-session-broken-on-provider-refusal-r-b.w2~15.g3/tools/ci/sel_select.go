package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

func init() {
	register(verb{
		name:    "select-packages",
		summary: "print the Go packages a change touches and their dependents",
		help: `ci select-packages [--on-go-list-error=fail|whole-tree] --all
ci select-packages [--on-go-list-error=fail|whole-tree] <base-sha>

Prints the ./cmd, ./internal and ./tools Go packages a change touches, plus every
in-repo package that imports one of them, one per line, as ./<dir>. The self-hosted
test shards use it so a change pays for the packages it can move and not the whole
fan-out; --all keeps the whole tree (the push to dev, the nightly run, make test).

The diff is read against <base-sha>, the event's own base (pull_request.base.sha or
merge_group.base_sha). A go.mod or go.sum change puts every package in scope.
internal/ci and internal/docs are selected on every run. A package named by
internal/pkgselect/DEPRECATED is never selected.

NEVER SILENTLY NOTHING: a go list that fails, or a diff of Go files that selects zero
packages, is not an empty answer. What it does is the caller's choice:
  --on-go-list-error=fail        (the default) exit 1 with the go list error printed
  --on-go-list-error=whole-tree  print "WARN select-packages: go list failed (<first
                                 line>); testing the whole tree" on stderr and the whole
                                 tree, read from the tracked files, on stdout

Exit 0 selected, 1 the selection failed, 2 bad usage.

example:
  go run ./tools/ci select-packages --all
  go run ./tools/ci select-packages --on-go-list-error=whole-tree "$BASE_SHA"
`,
		do: func(e env, args []string) int { return selectPackagesVerb(e, args, selRealHost()) },
	})
}

func selectPackagesVerb(e env, args []string, h selHost) int {
	const name = "select-packages"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	all := fs.Bool("all", false, "select the whole tree")
	mode := fs.String("on-go-list-error", "fail", "fail or whole-tree")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if *mode != "fail" && *mode != "whole-tree" {
		fmt.Fprintf(e.stderr, "select-packages: unknown --on-go-list-error=%s; want fail or whole-tree\n", *mode)
		return 2
	}
	switch {
	case *all && fs.NArg() > 0:
		return selRefuse(e, name, "--all takes no base sha")
	case !*all && fs.NArg() != 1:
		return selRefuse(e, name, "want --all or exactly one base sha")
	}
	out, err := pkgselect.Select(h.run, pkgselect.Options{Root: selRoot(e), All: *all, Base: fs.Arg(0), WholeTreeOnError: *mode == "whole-tree"})
	if err != nil {
		var le *pkgselect.ListError
		if errors.As(err, &le) {
			fmt.Fprint(e.stderr, le.Text)
		} else {
			fmt.Fprintf(e.stderr, "select-packages: %v\n", err)
		}
		return 1
	}
	if out.Warning != "" {
		fmt.Fprintln(e.stderr, out.Warning)
	}
	if len(out.Packages) > 0 {
		fmt.Fprintln(e.stdout, strings.Join(out.Packages, "\n"))
	}
	return 0
}
