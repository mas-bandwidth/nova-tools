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
	code, stdout, stderr := runSprint("help")
	if code != 0 {
		t.Fatalf("help exit %d; stderr %s", code, stderr)
	}
	examples, err := onboarding.ExampleLines(stdout, "nova-sprint")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 3 {
		t.Fatalf("example block has %d commands, want 3", len(examples))
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
	for _, ex := range examples {
		args := strings.Fields(ex)[1:]
		code, out, stderr := runSprint(args...)
		if code != 0 {
			t.Errorf("%s exit %d; stderr %s", ex, code, stderr)
		}
		if out == "" {
			t.Errorf("%s printed nothing", ex)
		}
	}
	got, err := os.ReadFile("sprint-table.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fixture) {
		t.Fatalf("after the examples the published table is not the fixture (%d bytes, want %d)", len(got), len(fixture))
	}
}

func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	code, _, stderr := runSprint("table", "--refresh", "pending")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--once", "--out", "run: nova-sprint help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in %s", want, stderr)
		}
	}
}

func TestThereIsNoQuickstartVerbAndTheCommandReferenceSaysWhy(t *testing.T) {
	code, _, stderr := runSprint("quickstart")
	if code != 2 {
		t.Fatalf("quickstart exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown verb") {
		t.Fatalf("stderr %q, want unknown verb", stderr)
	}
	cli, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cli), "no `quickstart`") {
		t.Error("docs/CLI.md does not say why there is no quickstart verb")
	}
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	runTranscript(t, string(raw), "docs/TESTS.md")
}

func TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	runTranscript(t, string(raw), "docs/CLI.md")
}

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
