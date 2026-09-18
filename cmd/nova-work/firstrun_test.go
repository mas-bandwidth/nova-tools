// First-run tests for nova-work: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` transcript are RUN here rather than read, so an
// example that has drifted out of the flag set is caught by a build.
//
// firstrun_test.go pins the onboarding standard (docs/ONBOARDING.md) for this binary:
// the examples at the foot of the usage banner RUN, and a bare command refuses in one
// line that names the door. The example's redis address is pointed at a miniredis, so
// nothing here reaches a real instance.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
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

// TestBareNovaWorkRefusesInOneLine: no verb is not an invocation, and the refusal says
// where the usage is.
func TestBareNovaWorkRefusesInOneLine(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb, production()); code != 2 {
		t.Fatalf("a bare nova-work exits %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("a bare nova-work wrote to stdout: %q", out.String())
	}
	if !strings.Contains(errb.String(), "run: nova-work help") {
		t.Errorf("a bare nova-work names no door: %q", errb.String())
	}
	if lines := strings.Split(strings.TrimSuffix(errb.String(), "\n"), "\n"); len(lines) > 2 {
		t.Errorf("a bare nova-work printed %d lines, want 1: %q", len(lines), errb.String())
	}
}

// TestUsageBannerExamplesRun executes each line under `example:` with the test's deps.
func TestUsageBannerExamplesRun(t *testing.T) {
	var help, errb bytes.Buffer
	if code := run([]string{"help"}, &help, &errb, production()); code != 0 {
		t.Fatalf("nova-work help exit = %d, stderr=%s", code, errb.String())
	}
	examples, err := onboarding.ExampleLines(help.String(), "nova-work")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, help.String())
	}
	mr := miniredis.RunT(t)
	deps := Deps{
		Now:  func() time.Time { return time.Now().UTC() },
		Dial: func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(_, _ string, _ time.Duration) ci.Forge {
			return &fakeForge{}
		},
	}
	for _, ex := range examples {
		args := localize(t, ex)[1:]
		for i, a := range args {
			if a == "127.0.0.1:6379" {
				args[i] = mr.Addr()
			}
		}
		var out, errs bytes.Buffer
		code := run(args, &out, &errs, deps)
		if code == 2 {
			t.Errorf("the usage example %q does not run: exit 2\nstderr: %s", ex, errs.String())
		}
		if out.Len() == 0 && errs.Len() == 0 {
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
