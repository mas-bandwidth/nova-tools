package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
)

func init() {
	register(verb{
		name:    "test-matrix",
		summary: "deal the selected packages onto the unit and functional test legs",
		help: `ci test-matrix --event <name> [--pull-request-base <sha>] [--merge-group-base <sha>]
               [--target-branch <name>] --linux-group <label> --macos-group <label>

Writes the test legs a workflow event fans out to, as the two GITHUB_OUTPUT keys the
jobs read: packages= (the unit tier's matrix) and functional= (the functional tier's).

The packages are pkgselect's selection: the change's own packages and their dependents
on pull_request (against --pull-request-base) and merge_group (against
--merge-group-base), the whole tree on push, schedule and workflow_dispatch, and on
a pull_request or merge_group whose base is empty. A change
that touches no Go package selects nothing and collapses to ONE leg that prints
"nothing to test for this change" and exits 0. A failed go list fails the job on a
pull request and selects the whole tree, with a warning, on every other event.

The heavy package is dealt first, so the heaviest never share a leg. A pull request's
macOS legs are only for the packages whose code, or whose imports' code, differs
under GOOS=darwin. The nightly schedule runs Linux only. The leg names carry their
platform: two legs under one name are two checks no reader can tell apart.

THE DARWIN LEGS run on schedule, on workflow_dispatch and where --target-branch (the
pull request's base branch, the merge group's base_ref, the push's ref name; a refs/heads/
prefix is cut) is dev or main. For any other target the matrix is Linux only: every
selected package rides the Linux legs, the darwin-only packages (cmd/nova-sandbox,
pkg/sandbox) are dropped from the selection before the nothing-to-test check, and
the darwin-sensitivity analysis does not run. The darwin evidence is taken at dev and
main (pkg/pkgselect DarwinOn).

--linux-group and --macos-group are the runner-group labels the legs carry (the
workflow's own runs-on labels).

Exit 0 written, 1 the selection failed or the fan-out would be empty, 2 bad usage.

example:
  go run ./tools/ci test-matrix --event push --linux-group linux --macos-group mac
`,
		do: func(e env, args []string) int { return testMatrixVerb(e, args, selRealHost()) },
	})
}

func testMatrixVerb(e env, args []string, h selHost) int {
	const name = "test-matrix"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	event := fs.String("event", "", "the workflow event name")
	prBase := fs.String("pull-request-base", "", "the pull request's base sha")
	mgBase := fs.String("merge-group-base", "", "the merge group's base sha")
	target := fs.String("target-branch", "", "the branch the change is bound for (the darwin legs run for dev and main)")
	linux := fs.String("linux-group", "", "the Linux runner-group label")
	mac := fs.String("macos-group", "", "the macOS runner-group label")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *event == "" || *linux == "" || *mac == "" {
		return selRefuse(e, name, "--event, --linux-group and --macos-group are required")
	}
	groups := pkgselect.Groups{Linux: *linux, Mac: *mac}

	base := ""
	switch *event {
	case "pull_request":
		base = *prBase
	case "merge_group":
		base = *mgBase
	}

	// A GO LIST FAILURE NEVER YIELDS `test (nothing)`: pkgselect judges it. On a
	// pull request it FAILS the job with the go list error printed (fast and
	// cheap; the runner is broken, re-run it); on every other event it warns and
	// selects the whole tree (the landing must not stall on one runner's cache).
	out, err := pkgselect.Select(h.run, pkgselect.Options{Root: selRoot(e), All: base == "", Base: base, WholeTreeOnError: *event != "pull_request"})
	if err != nil {
		var le *pkgselect.ListError
		if errors.As(err, &le) {
			fmt.Fprint(e.stderr, le.Text)
		} else {
			fmt.Fprintf(e.stderr, "select-packages: %v\n", err)
		}
		fmt.Fprintln(e.stdout, "select-packages failed; see its error above")
		return 1
	}
	if out.Warning != "" {
		fmt.Fprintln(e.stderr, out.Warning)
	}
	all := out.Packages
	if base != "" {
		shown := "none"
		if len(all) > 0 {
			shown = strings.Join(all, " ")
		}
		fmt.Fprintf(e.stdout, "%s: %d package(s) touched: %s\n", *event, len(all), shown)
	} else if len(all) == 0 {
		fmt.Fprintln(e.stdout, "no packages: the fan-out would be empty and green")
		return 1
	}

	darwin := pkgselect.DarwinOn(*event, *target)
	fmt.Fprintf(e.stdout, "darwin shards: %t (%s -> %s; on for %s)\n", darwin, *event, orNA(*target), strings.Join(pkgselect.DarwinBranches, " "))
	if !darwin {
		all = pkgselect.DropDarwinOnly(all)
		shown := "none"
		if len(all) > 0 {
			shown = strings.Join(all, " ")
		}
		fmt.Fprintf(e.stdout, "darwin shards off: %d package(s) on the Linux shards: %s\n", len(all), shown)
	}
	all = pkgselect.OrderHeavyFirst(all)

	functional := pkgselect.MarshalLegs(pkgselect.Functional(all, groups))
	fmt.Fprintf(e.stdout, "functional: %s\n", functional)
	if err := appendGitHubFile(e.getenv, "GITHUB_OUTPUT", "functional="+functional); err != nil {
		fmt.Fprintf(e.stderr, "test-matrix: %v\n", err)
		return 1
	}

	if len(all) == 0 {
		fmt.Fprintln(e.stdout, "nothing to test for this change")
		packages := pkgselect.MarshalLegs([]pkgselect.Leg{pkgselect.NothingLeg(groups)})
		fmt.Fprintln(e.stdout, packages)
		if err := appendGitHubFile(e.getenv, "GITHUB_OUTPUT", "packages="+packages); err != nil {
			fmt.Fprintf(e.stderr, "test-matrix: %v\n", err)
			return 1
		}
		return 0
	}

	sens := pkgselect.DarwinSensitive{}
	if *event == "pull_request" && darwin {
		var ok bool
		var err error
		sens, ok, err = pkgselect.DetectDarwinSensitive(h.run, selRoot(e))
		if err != nil {
			fmt.Fprintf(e.stderr, "test-matrix: %v\n", err)
			return 1
		}
		if ok {
			fmt.Fprintf(e.stdout, "darwin-specific (own files or an import differ under GOOS=darwin): %s\n", sens.Sorted())
		} else {
			fmt.Fprintln(e.stdout, "go list failed: every touched package keeps its macOS leg")
		}
	}

	legs := pkgselect.Fanout(*event, all, sens, groups, darwin)
	if len(legs) == 0 {
		fmt.Fprintln(e.stdout, "no packages with tests: the fan-out would be empty and green")
		return 1
	}
	packages := pkgselect.MarshalLegs(legs)
	fmt.Fprintln(e.stdout, packages)
	if err := appendGitHubFile(e.getenv, "GITHUB_OUTPUT", "packages="+packages); err != nil {
		fmt.Fprintf(e.stderr, "test-matrix: %v\n", err)
		return 1
	}
	return 0
}

func orNA(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}
