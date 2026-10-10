package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
)

func init() {
	register(verb{
		name:    "perf-tests",
		summary: "find the perf-tagged tests of the live tree",
		help: `ci perf-tests

Finds the tests only the perf build tag adds. Writes "<import path> <run regexp>", one
line per package, to $RUNNER_TEMP/perf-runs and appends PERF_PKGS=<the packages> to
$GITHUB_ENV. When GITHUB_OUTPUT is set, also writes matrix=<JSON array of Package and
Run objects> for one runner per discovered package. A perf-tagged package with no
test behind the tag is reported and skipped;
a tree with no perf-tagged test at all is red, because then the perf job asserts nothing.
Only live packages are considered (pkg/pkgselect/DEPRECATED).

Exit 0 found, 1 none found or go list failed, 2 bad usage.

example:
  go run ./tools/ci perf-tests
`,
		do: func(e env, args []string) int { return perfTestsVerb(e, args, selRealHost()) },
	})
}

func perfTestsVerb(e env, args []string, h selHost) int {
	const name = "perf-tests"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	tmp := e.getenv("RUNNER_TEMP")
	if tmp == "" {
		fmt.Fprintln(e.stderr, "perf-tests: RUNNER_TEMP is not set: there is nowhere to write perf-runs")
		return 1
	}
	runs, notes, err := pkgselect.PerfRuns(h.run, selRoot(e))
	if err != nil {
		fmt.Fprintf(e.stderr, "perf-tests: %v\n", err)
		return 1
	}
	for _, n := range notes {
		fmt.Fprintln(e.stdout, n)
	}
	var file, pkgs strings.Builder
	for _, r := range runs {
		fmt.Fprintf(&file, "%s %s\n", r.Package, r.Run)
		pkgs.WriteString(r.Package + " ")
	}
	if err := os.WriteFile(filepath.Join(tmp, "perf-runs"), []byte(file.String()), 0o644); err != nil {
		fmt.Fprintf(e.stderr, "perf-tests: %v\n", err)
		return 1
	}
	if len(runs) == 0 {
		fmt.Fprintln(e.stdout, "no live package holds a perf-tagged test: this job would assert nothing")
		return 1
	}
	if err := appendGitHubFile(e.getenv, "GITHUB_ENV", "PERF_PKGS="+pkgs.String()); err != nil {
		fmt.Fprintf(e.stderr, "perf-tests: %v\n", err)
		return 1
	}
	if e.getenv("GITHUB_OUTPUT") != "" {
		matrix, err := json.Marshal(runs)
		if err == nil {
			err = appendGitHubFile(e.getenv, "GITHUB_OUTPUT", "matrix="+string(matrix))
		}
		if err != nil {
			fmt.Fprintf(e.stderr, "perf-tests: %v\n", err)
			return 1
		}
	}
	return 0
}
