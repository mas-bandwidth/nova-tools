package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
// and the two transcripts are, byte for byte, the same sitting. The sitting
// needs no server: it is the three file-shaped first tries, each refused, in
// an empty directory that stays empty (#3326).
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
	dir := t.TempDir()
	t.Chdir(dir)
	for _, p := range onboarding.Execute(steps, runDocumented) {
		t.Errorf("%s: %s", name, p)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s: the first run left %d files; the table is written nowhere", name, len(entries))
	}
}

func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	var stdout, stderr bytes.Buffer
	code := run(s.Args, &stdout, &stderr)
	return onboarding.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

func TestTableLiveLayoutRefusesWithoutItsInputs(t *testing.T) {
	t.Setenv("NOVA_SPRINT_REDIS", "")
	t.Setenv("NOVA_REDIS_ADDR", "")
	code, _, stderr := runSprint("table", "--layout", "live")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--redis", "NOVA_SPRINT_REDIS"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal lacks %s: %s", want, stderr)
		}
	}
	// --compare keeps the #2674 inputs: the bash layout needs its roster and sprint.
	code, _, stderr = runSprint("table", "--compare", "x.txt")
	if code != 2 {
		t.Fatalf("--compare exit %d, want 2", code)
	}
	for _, want := range []string{"--redis", "--sprint", "--friends"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal lacks %s: %s", want, stderr)
		}
	}
	if code, _, stderr := runSprint("table", "--layout", "tall"); code != 2 || !strings.Contains(stderr, "live or wide") {
		t.Fatalf("--layout tall: exit %d %s", code, stderr)
	}
}
