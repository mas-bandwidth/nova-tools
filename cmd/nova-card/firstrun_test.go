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
// `### First run` block of docs/TESTS.md for nova-card and compares each
// command's whole output with the block under it. The version word and the
// machine triple are the run's, not the document's.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-card")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-card", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-card command; this test would pass by running nothing")
	}
	runner := func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, fmt.Errorf("the transcript redirects %q into nova-card; the first run reads no stdin", s.Stdin)
		}
		var out, errb bytes.Buffer
		code := run(s.Args, strings.NewReader(""), &out, &errb, func(string) string { return "" })
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
	for _, p := range onboarding.Execute(steps, runner, onboarding.Version(), onboarding.GoBuild()) {
		t.Error(p)
	}
}

// TestExampleLinesRun runs every line of the help banner's example block.
func TestExampleLinesRun(t *testing.T) {
	var banner bytes.Buffer
	if code := run([]string{"help"}, strings.NewReader(""), &banner, &banner, func(string) string { return "" }); code != 0 {
		t.Fatalf("help exits %d", code)
	}
	examples, err := onboarding.ExampleLines(banner.String(), "nova-card")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range examples {
		var out, errb bytes.Buffer
		if code := run(strings.Fields(line)[1:], strings.NewReader(""), &out, &errb, func(string) string { return "" }); code != 0 {
			t.Fatalf("%s exits %d: %s", line, code, errb.String())
		}
	}
}
