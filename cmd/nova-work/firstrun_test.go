// First-run tests for nova-work: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` transcript are RUN here rather than read, so an
// example that has drifted out of the flag set is caught by a build.
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/record"
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

// firstRunDeps is the seam the first-run transcript runs through: a fresh fake store and a
// consumer holding exactly one card result, so the transcript touches no Postgres and no
// Redis. The same deps answers every `$` line, so the `results` call lists the row the
// `record` call wrote.
func firstRunDeps() deps {
	store := record.NewFakeStore()
	return deps{
		openStore: func(string) (record.Store, error) { return store, nil },
		openConsumer: func(context.Context, string, string, string, string) (record.Consumer, error) {
			return &oneMessageConsumer{msg: &record.Message{
				ID: "1-0",
				Fields: map[string]string{
					"label":  "9347",
					"bench":  "space",
					"exit":   "1",
					"result": "RESULT: CARD-9347 card results in Postgres",
					"job":    "/jobs/card-9347",
					"commit": "abc1234",
					"branch": "rowan/postgres-card-results",
				},
			}}, nil
		},
		now: func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
	}
}

type oneMessageConsumer struct{ msg *record.Message }

func (c *oneMessageConsumer) Read(context.Context, int, time.Duration) ([]record.Message, error) {
	if c.msg == nil {
		return nil, nil
	}
	msg := *c.msg
	c.msg = nil
	return []record.Message{msg}, nil
}

func (c *oneMessageConsumer) ReadPending(context.Context, int) ([]record.Message, error) {
	return nil, nil
}

func (c *oneMessageConsumer) Ack(context.Context, string) error { return nil }
func (c *oneMessageConsumer) Close() error                      { return nil }

// recordFirstRun hands onboarding.FirstRun the `## nova-work` section naming `record`.
// docs/TESTS.md now carries two of those sections -- the job-graph transcript that landed
// on dev, and this branch's record/results transcript -- and onboarding.FirstRun reads the
// first. The record transcript is the one whose seams this file supplies, so pick it out.
func recordFirstRun(t *testing.T, md, tool string) []string {
	t.Helper()
	parts := strings.Split(md, "\n## "+tool+"\n")
	for i := len(parts) - 1; i >= 0; i-- {
		if !strings.Contains(parts[i], "record") {
			continue
		}
		lines, err := onboarding.FirstRun("\n## "+tool+"\n"+parts[i], tool)
		if err != nil {
			t.Fatal(err)
		}
		return lines
	}
	t.Fatalf("docs/TESTS.md has no `## %s` section naming `record`", tool)
	return nil
}

func TestExecutableFirstRun(t *testing.T) {
	t.Chdir("../..")
	// The job-graph examples write ./deps.json into the repository root; put nothing
	// back when the sitting is over.
	t.Cleanup(func() { os.Remove("deps.json") })
	var banner bytes.Buffer
	run([]string{"help"}, &banner, &banner, firstRunDeps())
	examples, err := onboarding.ExampleLines(banner.String(), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range examples {
		var out, errs bytes.Buffer
		fields := localize(t, line)
		if code := run(fields[1:], &out, &errs, firstRunDeps()); code == 2 {
			t.Fatalf("%s refused: %s", line, errs.String())
		}
	}

	doc, err := os.ReadFile("docs/TESTS.md")
	if err != nil {
		t.Fatal(err)
	}
	transcript := recordFirstRun(t, string(doc), "nova-work")
	shared := firstRunDeps()
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			if c := run(strings.Fields(line)[2:], &out, &errs, shared); c != 0 {
				t.Fatalf("first run: %d %s", c, errs.String())
			}
		} else if s := onboarding.Shape(line); s != "" {
			wanted = append(wanted, s)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if s := onboarding.Shape(line); s != "" {
			actual = append(actual, s)
		}
	}
	if strings.Join(wanted, "\n") != strings.Join(actual, "\n") {
		t.Fatalf("document shape %v differs from run %v", wanted, actual)
	}
}
