// firstrun_test.go EXECUTES the `### First run` transcript of docs/TESTS.md for
// this binary: the documented command is run and its whole output is compared
// with the block written under it -- same number of lines, same lines, same
// order -- by internal/onboarding's transcript harness.
//
// nova-review is the smallest of the eight transcripts that no test ran, and it
// was wrong: the document showed `nova-review devel` where the binary prints
// `nova-review devel <goos>/<goarch> go<version>`. Two of the four tokens on
// the one line of this tool's quickstart were missing, and the comparison that
// would have caught it did not exist. The document is repaired here from a real
// run rather than the test being loosened to accept two tokens.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestTESTSFirstRunIsWhatTheToolPrints runs the transcript and compares it.
//
// ONE normalisation is declared: the `<goos>/<goarch> go<version>` tail, which
// is the machine the line was recorded on rather than anything nova-review
// promises. The version word in front of it is compared, so a build that stopped
// answering `devel` in a checkout is still red.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Chdir(repoRoot(t))
	raw, err := os.ReadFile(filepath.Join("docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-review")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-review", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-review command; this test would pass by running nothing")
	}
	for _, p := range onboarding.Execute(steps, runDocumented, onboarding.GoBuild()) {
		t.Error(p)
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments. nova-review's first run reads nothing, so a `< path` in its
// transcript would be a line this runner cannot honour, and it says so rather
// than running the command without its input.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb)
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-review reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

// repoRoot is the checkout root: this package sits two directories under it. It
// is resolved rather than assumed so that a failure names a path.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "TESTS.md")); err != nil {
		t.Fatalf("docs/TESTS.md is not under %s: %v", root, err)
	}
	return root
}
