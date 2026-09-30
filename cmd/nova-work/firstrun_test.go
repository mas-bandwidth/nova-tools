package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, nil); code != 0 || errb.Len() != 0 {
		t.Fatalf("help: exit %d, stderr %q", code, errb.String())
	}
	examples, err := onboarding.ExampleLines(out.String(), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 1 || examples[0] != "nova-work help" {
		t.Fatalf("banner examples = %q, want [nova-work help]", examples)
	}
}

func TestBareInvocationNamesHelpInOneLine(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	code := run(nil, &out, &errb, nil)
	if code != 2 || out.Len() != 0 || strings.Count(errb.String(), "\n") != 1 ||
		!strings.Contains(errb.String(), "run: nova-work help") {
		t.Fatalf("bare invocation: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-work", lines)
	if err != nil {
		t.Fatal(err)
	}
	var banner, bannerErr bytes.Buffer
	if code := run([]string{"help"}, &banner, &bannerErr, nil); code != 0 || bannerErr.Len() != 0 {
		t.Fatalf("help: exit %d, stderr %q", code, bannerErr.String())
	}
	examples, err := onboarding.ExampleLines(banner.String(), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || len(examples) != 1 || examples[0] != "nova-work help" {
		t.Fatalf("first run has %d steps and banner examples %q, want one nova-work help", len(steps), examples)
	}
	results := make([]onboarding.Result, 0, len(steps))
	for _, step := range steps {
		if strings.TrimPrefix(step.Line, "$ ") != examples[0] || len(step.Args) != 1 || step.Args[0] != "help" || step.Stdin != "" {
			t.Fatalf("first-run command %q with stdin %q differs from store-free banner example %q", step.Line, step.Stdin, examples[0])
		}
		var out, errb bytes.Buffer
		code := run(step.Args, &out, &errb, nil)
		results = append(results, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, problem := range onboarding.CompareTranscript(steps, results, nil) {
		t.Error(problem)
	}
}
