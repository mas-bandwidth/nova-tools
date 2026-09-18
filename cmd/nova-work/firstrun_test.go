// First-run tests for nova-work: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` transcript are RUN here rather than read, so an
// example that has drifted out of the flag set is caught by a build.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstRunPlan is the plan the first run writes: one valid node with a known
// kind, small enough to read in one line.
const firstRunPlan = "(:plan :version 1 (:node :id \"n1\" :kind docs))\n"

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func localize(t *testing.T, line string) []string {
	t.Helper()
	plan := filepath.Join(t.TempDir(), "work.work")
	if err := os.WriteFile(plan, []byte(firstRunPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(line, "./work.work", plan))
}

func TestUsageBannerExamplesRun(t *testing.T) {
	code, banner, stderr := runCLI(t, "help")
	if code != 0 {
		t.Fatalf("`nova-work help` must be exit 0, got %d; stderr: %s", code, stderr)
	}
	examples, err := onboarding.ExampleLines(banner, "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range examples {
		fields := localize(t, ex)
		if len(fields) < 2 || fields[0] != "nova-work" {
			t.Fatalf("usage example %q is not a nova-work command", ex)
		}
		code, stdout, stderr := runCLI(t, fields[1:]...)
		if code != 0 {
			t.Fatalf("the usage example %q does not run: exit %d, stderr: %s", ex, code, stderr)
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

func TestTESTSFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	var printed map[string]bool
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-work "); ok {
			fields := localize(t, cmd)
			code, stdout, stderr := runCLI(t, fields...)
			if code != 0 {
				t.Fatalf("the TESTS.md command %q does not run: exit %d, stderr: %s", line, code, stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout, "\n") {
				if s := onboarding.Shape(out); s != "" {
					printed[s] = true
				}
			}
			continue
		}
		s := onboarding.Shape(line)
		if s == "" {
			continue
		}
		if !printed[s] {
			t.Errorf("TESTS.md line\n  %s\nhas shape %q, which this tool never prints", line, s)
		}
	}
}
