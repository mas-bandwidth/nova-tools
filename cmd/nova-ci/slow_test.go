//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// SLOW: 11.3 s on hetzner at dev 64b9bec48, over the five-second line.
func TestNewRuleYieldsABuildingTestingSkeleton(t *testing.T) {
	t.Parallel()
	_, bin, tree := scaffoldTree(t, "./internal/ci")

	// Run new-rule verb
	out := runScaffoldCmd(t, bin, "new-rule", "--root", tree, "sample")
	for _, want := range []string{
		"internal/ci/sample_class_test.go",
		"internal/ci/testdata/sample/fixture.txt",
		"make/rule_sample.mk",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("new-rule did not report writing %s:\n%s", want, out)
		}
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(want))); err != nil {
			t.Errorf("new-rule did not write %s: %v", want, err)
		}
	}

	// BUILDING: the package the rule lands in builds and vets cleanly
	runIn(t, tree, "go", "build", "./internal/ci")
	runIn(t, tree, "go", "vet", "./internal/ci")

	// TESTING: the fixture test passes
	if got := runIn(t, tree, "go", "test", "-v", "-count=1", "-run", "TestNoSampleViolations", "./internal/ci"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("go test of the class rule skeleton did not pass:\n%s", got)
	}

	// Makefile integration: make test-rule-sample runs and passes
	if got := runIn(t, tree, "make", "-f", "Makefile", "test-rule-sample"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("make test-rule-sample did not pass:\n%s", got)
	}

	// Write discipline: second run refuses rather than overwrite
	cmd := exec.Command(bin, "new-rule", "--root", tree, "sample")
	cmd.Env = goenv.Clean(os.Environ())
	if got, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(got), "already exists") {
		t.Errorf("second new-rule run was not refused (err %v):\n%s", err, got)
	}

	// Invalid names are refused
	cmd = exec.Command(bin, "new-rule", "--root", tree, "Invalid Name!")
	cmd.Env = goenv.Clean(os.Environ())
	if got, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(got), "rule name") {
		t.Errorf("new-rule with invalid name was not refused (err %v):\n%s", err, got)
	}
}
