package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

// largePage builds the state the audit measured this binary at: six hundred standing
// claims and six hundred dated ones in one file — 1,201 lines and ~78K tokens, of which
// half was the tool congratulating the writer, at length, on the half they got right.
func largePage(t *testing.T, standing, dated int) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < standing; i++ {
		fmt.Fprintf(&b, "I cannot check my own work on the %dth pass, so the second read went to someone else.\n\n", i)
	}
	for i := 0; i < dated; i++ {
		fmt.Fprintf(&b, "On 2026-08-%02d I cannot check the Windows runner from here, and the fix went in with that measurement written beside it.\n\n", i%28+1)
	}
	path := filepath.Join(t.TempDir(), "journal.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanCapsFindingsAndCountsTheDated(t *testing.T) {
	page := largePage(t, 600, 600)
	exit, stdout, stderr := runSelfTalk(t, page)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", exit, stderr)
	}
	if got := countLines(stderr); got != bounded.Default+1 {
		t.Errorf("stderr is %d lines, want %d findings + one MORE line", got, bounded.Default)
	}
	if !strings.Contains(stderr, "SELFTALK MORE kind=standing shown=20 total=600") {
		t.Errorf("no MORE line naming the total:\n%s", stderr)
	}
	// THE WELCOME CASE IS A COUNT. Six hundred dated claims are one line.
	if !strings.Contains(stdout, "SELFTALK DATED n=600 files=1") {
		t.Errorf("the dated claims are not counted: %s", stdout)
	}
	if strings.Contains(stdout, "SELFTALK DATED "+page) {
		t.Errorf("a dated claim is still quoted")
	}
	if !strings.Contains(stdout, "standing=600") {
		t.Errorf("no count line on failure: %s", stdout)
	}
	// stdout: the DATED count, the FAIL count, the NOTE.
	if got := countLines(stdout); got != 3 {
		t.Errorf("stdout is %d lines, want 3 (dated count, count line, NOTE):\n%s", got, stdout)
	}
}

// The two classes are capped separately, for the same reason the classes exist: the
// INSTALLATION finding is the one the first class cannot see, and a flat cap over six
// hundred STANDING claims would eat it.
func TestScanCapsEachClassSeparately(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "I cannot check my own work on the %dth pass, so the second read went to someone else.\n\n", i)
	}
	b.WriteString("It is the worst habit I have, and the reason the checklist exists at all.\n")
	path := filepath.Join(t.TempDir(), "journal.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, stderr := runSelfTalk(t, path)
	if !strings.Contains(stderr, "INSTALLATION") {
		t.Fatalf("the buried class never printed:\n%s", stderr)
	}
	if !strings.Contains(stderr, "SELFTALK MORE kind=standing") {
		t.Errorf("the loud class was not capped:\n%s", stderr)
	}
	if strings.Contains(stderr, "SELFTALK MORE kind=installation") {
		t.Errorf("the quiet class was capped though it had one finding:\n%s", stderr)
	}
}

func TestMaxWidensAndZeroPrintsAll(t *testing.T) {
	page := largePage(t, 600, 0)
	_, _, stderr := runSelfTalk(t, "--max", "5", page)
	if got := countLines(stderr); got != 6 {
		t.Errorf("--max 5 gave %d lines, want 5 + MORE", got)
	}
	_, _, stderr = runSelfTalk(t, "--max", "0", page)
	if got := countLines(stderr); got != 600 {
		t.Errorf("--max 0 gave %d lines, want all 600", got)
	}
	if strings.Contains(stderr, "SELFTALK MORE") {
		t.Errorf("--max 0 elided nothing and must print no MORE line")
	}
}

func TestRefusesANegativeCeiling(t *testing.T) {
	page := largePage(t, 2, 0)
	exit, _, stderr := runSelfTalk(t, "--max", "-1", page)
	if exit != 2 || !strings.Contains(stderr, "--max must be a line ceiling") {
		t.Errorf("exit = %d, stderr = %q", exit, stderr)
	}
}

// A flag typo used to cost the whole 40-line banner. It costs one line, plus the one
// hint line this repo's guidance law requires, and it names the door.
func TestARefusalIsAtMostTwoLinesAndNamesTheDoor(t *testing.T) {
	for _, args := range [][]string{
		{"--skipp", "x", "a.md"},
		nil,
	} {
		exit, stdout, stderr := runSelfTalk(t, args...)
		if exit != 2 {
			t.Errorf("%v: exit = %d, want 2", args, exit)
		}
		if got := countLines(stderr); got > 2 {
			t.Errorf("%v: the refusal is %d lines, want at most 2:\n%s", args, got, stderr)
		}
		if !strings.Contains(stderr, "run: nova-self-talk help") {
			t.Errorf("%v: the refusal names no door: %q", args, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: a refusal wrote to stdout: %q", args, stdout)
		}
	}
	exit, stdout, _ := runSelfTalk(t, "help")
	if exit != 0 || !strings.Contains(stdout, "usage:") {
		t.Errorf("`help` did not print the usage: exit %d", exit)
	}
}
