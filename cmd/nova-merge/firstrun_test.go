// firstrun_test.go EXECUTES the `### First run` transcript of docs/TESTS.md for
// this binary: the documented command is run and its whole output is compared
// with the block written under it -- same number of lines, same lines, same
// order -- by internal/onboarding's transcript harness. Since the per-PR lander
// role was retired (stream is the unit, 2026-09-24) nova-merge has no lane-making
// quickstart, so its first run is `nova-merge version`, the one line of the help
// banner's `example:` block.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestTESTSFirstRunIsWhatTheToolPrints runs the transcript and compares it.
//
// THREE normalisations are declared, and the document says so above the block:
// the `<goos>/<goarch> go<version>` tail is the machine the line was recorded on,
// the version word is what the build stamped itself with, and build= is the
// sha256 of this binary's own file (rule 16), twelve hex, different for every build.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Chdir(repoRoot(t))
	raw, err := os.ReadFile(filepath.Join("docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-merge")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-merge", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || strings.TrimSpace(strings.TrimPrefix(steps[0].Line, "$ ")) != "nova-merge version" {
		t.Fatalf("the `### First run` block should hold exactly `nova-merge version`, the help banner's example; it holds %+v", steps)
	}
	for _, p := range onboarding.Execute(steps, runDocumented, onboarding.Version(), onboarding.GoBuild(), onboarding.HexID("build", 12)) {
		t.Error(p)
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments and the production deps; version reads nothing but its own file.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb, production())
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

// repoRoot is the checkout root: this package sits two directories under it.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
