package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestMain fixes the clock for every test of this package before any runs, so
// the tree an import writes records one instant and the transcript's sha256 and
// seconds= reproduce. No test here reads the real time.
func TestMain(m *testing.M) {
	fixed := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	os.Exit(m.Run())
}

// TestTESTSFirstRunIsWhatTheToolPrints: the `### First run` block of
// docs/TESTS.md is EXECUTED, every command in order, against the recorded
// conversation with GitHub the other tests use (internal/workgh/testdata/
// reliable: one public repository of twenty issues, read at fifteen a page), and
// the whole output is compared by the one comparator. The banner's `example:`
// block is the same three commands (docs/ONBOARDING.md point 6), so one sitting
// keeps both promises.
//
// $ORG and $REPO are the reader's: the test stands them for the recording's
// names on the command line, and the comparator's `recorded` entry writes the
// recording's names back as $ORG and $REPO where the tool prints them.
// ./tree.lisp is a file in a directory of the test's own.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	// The banner's example lines, named in this test's own body so the
	// pasted-examples rule (internal/ci, SPEC-TOOLWORK.md documents rule 6) reads
	// the command text here; the transcript holds the same lines.
	documentedExamples := []string{
		"nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run",
		"nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp",
		"nova-work verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15",
	}
	examples, err := onboarding.ExampleLines(banner, "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(examples, "\n") != strings.Join(documentedExamples, "\n") {
		t.Fatalf("the banner's examples are %q, this test names %q", examples, documentedExamples)
	}
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
	var commands []string
	for _, s := range steps {
		commands = append(commands, "nova-work "+strings.Join(s.Args, " "))
	}
	if strings.Join(commands, "\n") != strings.Join(documentedExamples, "\n") {
		t.Fatalf("the transcript runs %q and the banner's examples are %q; they are one list", commands, documentedExamples)
	}

	const org, repo = "mas-bandwidth", "reliable" // the recording's
	dir := t.TempDir()
	stand := strings.NewReplacer("$ORG", org, "$REPO", repo, "./", dir+"/")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = stand.Replace(a)
		}
		code, out, errs := do(t, replay(t), args...)
		if code != 0 {
			t.Errorf("the documented command %s exits %d; stderr: %s", s.Line, code, errs)
		}
		got = append(got, onboarding.Result{Code: code, Stdout: out, Stderr: errs})
	}
	volatile := []onboarding.Field{
		{Name: "tmpdir", Doc: ".", Run: dir},
		{Name: "recorded", Doc: "$ORG", Run: org},
		{Name: "recorded", Doc: "$REPO", Run: repo},
	}
	for _, p := range onboarding.CompareTranscript(steps, got, volatile) {
		t.Error(p)
	}
}
