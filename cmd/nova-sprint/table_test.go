package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints is the comparator
// test SPEC-TOOLWORK §7 rule 7 asks for: docs/CLI.md's `nova-sprint` section
// carries its own `### First run` transcript -- the three `nova-sprint table`
// lines a stranger pastes from the command reference rather than from
// docs/TESTS.md -- and it is executed here, in order, in one directory,
// through the same onboarding.Execute/onboarding.Compare comparator
// TestTESTSFirstRunIsWhatTheToolPrints (firstrun_test.go) uses for
// docs/TESTS.md's copy of the same sitting (#2218).
func TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	runTranscript(t, string(raw), "docs/CLI.md")
}

// runTranscript runs one document's `nova-sprint` `### First run` transcript
// and compares it against what the tool actually prints. Both
// TestTESTSFirstRunIsWhatTheToolPrints (docs/TESTS.md) and
// TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints (docs/CLI.md) call
// it, because #2218's rule 7 wants BOTH documents' pasted examples covered
// and the two transcripts are, byte for byte, the same sitting.
func runTranscript(t *testing.T, doc, name string) {
	t.Helper()
	lines, err := onboarding.FirstRun(doc, "nova-sprint")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-sprint", lines)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(steps) != 3 {
		t.Fatalf("%s runs %d commands, want 3", name, len(steps))
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "table.txt"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "table.txt"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, p := range onboarding.Execute(steps, runDocumented) {
		t.Errorf("%s: %s", name, p)
	}
	got, err := os.ReadFile("sprint-table.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fixture) || len(got) == 0 {
		t.Fatalf("%s: second start left %d bytes, want the fixture (%d)", name, len(got), len(fixture))
	}
}

func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	var stdout, stderr bytes.Buffer
	code := run(s.Args, &stdout, &stderr)
	return onboarding.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}
