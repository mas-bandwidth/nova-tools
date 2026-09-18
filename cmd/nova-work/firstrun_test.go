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

// firstRunDir is the working directory ONE transcript runs in: a temp dir holding
// the plan the `./work.work` lines read. It is made once per test rather than once
// per line because the lines are a sequence -- `dependencies --node b`, then
// `--node a --needs b`, then `ready` -- and the graph they build has to survive
// from one line to the next.
func firstRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "work.work"), []byte(firstRunPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// localize rewrites EVERY `./x` token in an example into dir, rather than the one
// `./work.work` this used to know about. The one it did not know about was
// `./deps.json`, which the `dependencies` verb WRITES: relative to the test's own
// working directory, every run of this package left a deps.json in
// cmd/nova-work/. The examples are still run verbatim in shape -- what changes is
// the directory the paths in them point at, so a relative path added to the usage
// banner or to docs/TESTS.md tomorrow lands in the temp dir too.
func localize(t *testing.T, dir, line string) []string {
	t.Helper()
	fields := strings.Fields(line)
	for i, f := range fields {
		if rest, ok := strings.CutPrefix(f, "./"); ok {
			fields[i] = filepath.Join(dir, rest)
		}
	}
	return fields
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
	dir := firstRunDir(t)
	for _, ex := range examples {
		fields := localize(t, dir, ex)
		if len(fields) < 2 || fields[0] != "nova-work" {
			t.Fatalf("usage example %q is not a nova-work command", ex)
		}
		args := fields[1:]
		// The events example names the default redis address. This run points it at
		// the test's miniredis, so the example runs as written and reaches no network.
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
	dir := firstRunDir(t)
	var printed map[string]bool
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-work "); ok {
			fields := localize(t, dir, cmd)
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
