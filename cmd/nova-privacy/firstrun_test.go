package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The docs/TESTS.md `### First run` transcript is run line for line through
// the one comparator, against the fixture the lines copy. The lines type
// ./example; each step runs with that prefix pointed at the fixture's
// absolute path, and the tmpdir entry of the shared table reduces that path
// back to ./example on both sides.
const exampleDir = "testdata/example"

func readDoc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	lines, err := onboarding.FirstRun(readDoc(t, "TESTS.md"), "nova-privacy")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-privacy", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("the `### First run` block runs %d commands, want 3: corpus, a clean screen, a flagged screen", len(steps))
	}
	abs, err := filepath.Abs(exampleDir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			if rest, ok := strings.CutPrefix(a, "./example"); ok {
				a = abs + rest
			}
			args[i] = a
		}
		var out, errb bytes.Buffer
		code := run(args, &bytes.Buffer{}, &out, &errb)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "tmpdir", Doc: "./example", Run: abs}}) {
		t.Error(p)
	}

	// The banner's example block is the same sitting.
	var banner bytes.Buffer
	if code := run([]string{"help"}, &bytes.Buffer{}, &banner, &bytes.Buffer{}); code != 0 {
		t.Fatalf("`nova-privacy help` exits %d", code)
	}
	examples, err := onboarding.ExampleLines(banner.String(), "nova-privacy")
	if err != nil {
		t.Fatal(err)
	}
	documented := make([]string, 0, len(steps))
	for _, s := range steps {
		documented = append(documented, strings.Join(append([]string{"nova-privacy"}, s.Args...), " "))
	}
	if strings.Join(examples, "\n") != strings.Join(documented, "\n") {
		t.Errorf("the banner's example block is not docs/TESTS.md's first run\nbanner:\n  %s\ntranscript:\n  %s",
			strings.Join(examples, "\n  "), strings.Join(documented, "\n  "))
	}
}
