package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine executes the
// `### First run` block of docs/TESTS.md for nova-test and compares each
// command's whole output with the block under it.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	t.Chdir(repoRoot(t))
	raw, err := os.ReadFile(filepath.Join("docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-test")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-test", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-test command; this test would pass by running nothing")
	}
	runner := func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, fmt.Errorf("the transcript redirects %q into nova-test, which reads no stdin", s.Stdin)
		}
		var out, errb bytes.Buffer
		code := run(s.Args, &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
	for _, p := range onboarding.Execute(steps, runner) {
		t.Error(p)
	}
}

// TestHelpExamplesRun runs every line of the help banner's example block, as
// printed, from the root of the checkout.
func TestHelpExamplesRun(t *testing.T) {
	t.Chdir(repoRoot(t))
	var banner, errb bytes.Buffer
	if code := run([]string{"help"}, &banner, &errb); code != 0 {
		t.Fatalf("help exits %d: %s", code, errb.String())
	}
	examples, err := onboarding.ExampleLines(banner.String(), "nova-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range examples {
		var out bytes.Buffer
		errb.Reset()
		if code := run(strings.Fields(line)[1:], &out, &errb); code != 0 {
			t.Errorf("%s exits %d: %s", line, code, errb.String())
		}
	}
}

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
