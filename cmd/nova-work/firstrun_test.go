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
	for _, example := range examples {
		args := strings.Fields(example)[1:]
		if len(args) == 0 || (args[0] != "help" && args[0] != "version") {
			t.Fatalf("example %q could reach GitHub; use a store-free first run", example)
		}
		out.Reset()
		errb.Reset()
		if code := run(args, &out, &errb, nil); code != 0 || out.Len() == 0 || errb.Len() != 0 {
			t.Fatalf("example %q: exit %d, stdout %q, stderr %q", example, code, out.String(), errb.String())
		}
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
	if len(steps) == 0 {
		t.Fatal("the nova-work first-run transcript executes no command")
	}
	helpSeen := false
	for _, step := range steps {
		if len(step.Args) == 0 || (step.Args[0] != "help" && step.Args[0] != "version") {
			t.Fatalf("first-run command %q could reach GitHub; provide a replay fixture before documenting it", step.Line)
		}
		if step.Args[0] == "help" {
			helpSeen = true
		}
	}
	if !helpSeen {
		t.Fatal("the first-run transcript must show nova-work help")
	}
	for _, problem := range onboarding.Execute(steps, func(step onboarding.Step) (onboarding.Result, error) {
		if step.Stdin != "" {
			return onboarding.Result{}, fmt.Errorf("nova-work first-run command %q does not read stdin", step.Line)
		}
		var out, errb bytes.Buffer
		code := run(step.Args, &out, &errb, nil)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}) {
		t.Error(problem)
	}
}
