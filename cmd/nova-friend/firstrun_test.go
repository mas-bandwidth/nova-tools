//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstrun_test.go pins the onboarding standard for this binary
// (docs/ONBOARDING.md): the `### First run` block of docs/TESTS.md is
// EXECUTED, every command in order, on a throwaway redis-server holding the
// roster nova-config apply writes, and each command's whole output is
// compared with the block under it by the one comparator,
// onboarding.CompareTranscript (docs/SPEC-TOOLWORK.md §7 rule 2). The
// banner's `example:` lines are the same three commands, so one sitting
// covers both promises. The documented lines name no --redis: on a bench
// the seat's address is the default; here each step runs with --redis <the
// throwaway server> appended, which changes what the tool dials and nothing
// it prints. Nothing is normalised: --host and --session are given on the
// line, so every value reproduces.

func readTranscriptDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	// The banner's example lines, named in this test's own body so the
	// pasted-examples rule (internal/ci, SPEC-TOOLWORK §7 rule 7) reads
	// the command text here; the transcript below holds the same lines.
	documentedExamples := []string{
		"nova-friend here --as rowan --once --host studio --session s1",
		"nova-friend list",
		"nova-friend bye --as rowan",
	}
	lines, err := onboarding.FirstRun(readTranscriptDoc(t), "nova-friend")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-friend", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-friend command; this test would pass by running nothing")
	}
	var commands []string
	for _, s := range steps {
		commands = append(commands, strings.TrimPrefix(s.Line, "$ "))
	}
	if strings.Join(commands, "\n") != strings.Join(documentedExamples, "\n") {
		t.Fatalf("the transcript runs %q and the banner's examples are %q; they are one list", commands, documentedExamples)
	}
	_, banner, _ := runFriend(nil, "help")
	examples, err := onboarding.ExampleLines(banner, "nova-friend")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(examples, "\n") != strings.Join(documentedExamples, "\n") {
		t.Fatalf("the banner's examples are %q, this test names %q", examples, documentedExamples)
	}
	addr, _ := seededStore(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		code, out, errOut := runFriend(nil, append(append([]string{}, s.Args...), "--redis", addr)...)
		if code != 0 {
			t.Errorf("the documented command %s exits %d; a first run on the fixture store answers 0\nstderr: %s", s.Line, code, errOut)
		}
		got = append(got, onboarding.Result{Code: code, Stdout: out, Stderr: errOut})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}

// TestTESTSNamesNovaFriendOnce: onboarding.Section reads the FIRST `##
// nova-friend` section and stops, so a second would be read by no test.
func TestTESTSNamesNovaFriendOnce(t *testing.T) {
	t.Parallel()

	sections := 0
	for _, name := range onboarding.SectionNames(readTranscriptDoc(t)) {
		if name == "nova-friend" {
			sections++
		}
	}
	if sections != 1 {
		t.Fatalf("docs/TESTS.md heads %d `## nova-friend` sections, want exactly 1", sections)
	}
}
