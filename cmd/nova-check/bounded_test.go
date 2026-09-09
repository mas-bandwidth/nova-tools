package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

// largeSelf builds the state the audit measured this binary at: a self repo with n broken
// markdown links and n code files, which is what an unchecked repo looks like on the day
// a stranger first runs quickstart against it.
func largeSelf(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		md := fmt.Sprintf("# page %d\n\nA [link](./no-such-page-%d.md) that does not resolve.\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("page-%04d.md", i)), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("tool-%04d.py", i)), []byte("print(1)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

func TestLinksCapsFindingsAndAlwaysPrintsTheCount(t *testing.T) {
	dir := largeSelf(t, 500)
	exit, stdout, stderr := runCheck(t, "links", "--dir", dir)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", exit, stderr)
	}
	if got := countLines(stderr); got != bounded.Default+2 {
		t.Errorf("stderr is %d lines, want %d findings + MORE + count", got, bounded.Default)
	}
	if !strings.Contains(stderr, "LINKS MORE kind=broken shown=20 total=500") {
		t.Errorf("no MORE line naming the total:\n%s", stderr)
	}
	// The count line used to print only on PASS, so a run that found 800 broken links
	// said nothing about 800 and stdout was empty.
	if !strings.Contains(stderr, "broken=500 shown=20") {
		t.Errorf("no count line on failure:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("a failing links wrote to stdout: %q", stdout)
	}
}

func TestNoCodeCapsFindingsAndAlwaysPrintsTheCount(t *testing.T) {
	dir := largeSelf(t, 500)
	exit, _, stderr := runCheck(t, "nocode", "--dir", dir)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", exit, stderr)
	}
	if got := countLines(stderr); got != bounded.Default+2 {
		t.Errorf("stderr is %d lines, want %d findings + MORE + count:\n%s", got, bounded.Default, stderr)
	}
	if !strings.Contains(stderr, "NOCODE MORE kind=file shown=20 total=500") {
		t.Errorf("no MORE line naming the total:\n%s", stderr)
	}
	if !strings.Contains(stderr, "findings=500 shown=20") {
		t.Errorf("no count line on failure:\n%s", stderr)
	}
}

// THE FIRST-RUN VERB. Uncapped it was 1,400 lines for two lines of verdict — the most
// expensive thing a stranger could type, on the run where they know the least.
func TestQuickstartInheritsTheCaps(t *testing.T) {
	dir := largeSelf(t, 500)
	exit, stdout, stderr := runCheck(t, "quickstart", "--dir", dir)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", exit, stderr)
	}
	total := countLines(stdout) + countLines(stderr)
	if total > 50 {
		t.Errorf("a first run on a 1,000-file repo costs %d lines; it should cost about forty", total)
	}
	if !strings.Contains(stderr, "LINKS MORE") || !strings.Contains(stderr, "NOCODE MORE") {
		t.Errorf("quickstart did not pass the cap down to both checks:\n%s", stderr)
	}
	if !strings.Contains(stdout, "QUICKSTART OK done=2") {
		t.Errorf("the closing line is missing: %q", stdout)
	}
}

// The remedy has to be true, which means running it.
func TestFailMaxWidensAndZeroPrintsAll(t *testing.T) {
	dir := largeSelf(t, 500)
	_, _, stderr := runCheck(t, "links", "--dir", dir, "--fail-max", "5")
	if got := countLines(stderr); got != 7 {
		t.Errorf("--fail-max 5 gave %d lines, want 5 + MORE + count", got)
	}
	_, _, stderr = runCheck(t, "links", "--dir", dir, "--fail-max", "0")
	if got := countLines(stderr); got != 501 {
		t.Errorf("--fail-max 0 gave %d lines, want all 500 + the count line", got)
	}
	if strings.Contains(stderr, "LINKS MORE") {
		t.Errorf("--fail-max 0 elided nothing and must print no MORE line")
	}
	// quickstart passes its own ceiling down, including 0.
	_, _, stderr = runCheck(t, "quickstart", "--dir", dir, "--fail-max", "0")
	if got := countLines(stderr); got != 1002 {
		t.Errorf("quickstart --fail-max 0 gave %d lines, want 500+1 links and 500+1 nocode", got)
	}
}

func TestRefusesANegativeCeiling(t *testing.T) {
	dir := largeSelf(t, 2)
	for _, verb := range []string{"links", "nocode", "quickstart"} {
		exit, _, stderr := runCheck(t, verb, "--dir", dir, "--fail-max", "-1")
		if exit != 2 || !strings.Contains(stderr, "--fail-max must be a line ceiling") {
			t.Errorf("%s: exit = %d, stderr = %q", verb, exit, stderr)
		}
	}
}

// A flag typo used to cost the whole 38-line banner.
func TestAFlagTypoIsOneLine(t *testing.T) {
	for _, args := range [][]string{
		{"links", "--diir", "x"},
		{"nocode", "--diir", "x"},
		{"frobnicate"},
		nil,
	} {
		exit, stdout, stderr := runCheck(t, args...)
		if exit != 2 {
			t.Errorf("%v: exit = %d, want 2", args, exit)
		}
		if got := countLines(stderr); got != 1 {
			t.Errorf("%v: the refusal is %d lines, want 1:\n%s", args, got, stderr)
		}
		if !strings.Contains(stderr, "run: nova-check help") {
			t.Errorf("%v: the refusal names no door: %q", args, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: a refusal wrote to stdout: %q", args, stdout)
		}
	}
	exit, stdout, _ := runCheck(t, "help")
	if exit != 0 || !strings.Contains(stdout, "usage:") {
		t.Errorf("`help` did not print the usage: exit %d", exit)
	}
}
