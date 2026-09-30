package main

import (
	"strings"
	"testing"
)

// `lint -h` explains every flag it lists: the third cold rating found four (--base-check,
// --legs, --p95, --repo) with a name and nothing else. A flag line is `--name <type>  text`;
// a line with no text is the defect.
func TestLintHelpExplainsEveryFlag(t *testing.T) {
	t.Parallel()
	exit, stdout, _ := runSwarm(t, "lint", "-h")
	if exit != 0 {
		t.Fatalf("lint -h: exit %d", exit)
	}
	in, flags := false, 0
	for _, line := range strings.Split(stdout, "\n") {
		if line == "flags:" {
			in = true
			continue
		}
		if !in || !strings.HasPrefix(line, "  --") {
			if in && strings.HasPrefix(line, "exit codes") {
				break
			}
			continue
		}
		flags++
		name, rest, _ := strings.Cut(strings.TrimSpace(line), "  ")
		if strings.TrimSpace(rest) == "" {
			t.Errorf("lint -h lists %s and does not say what it does", name)
		}
	}
	if flags < 12 {
		t.Errorf("lint -h lists %d flags, want at least the twelve lint has:\n%s", flags, stdout)
	}
	// a flag's placeholder is its type word, not the first backquoted words of its text
	if !strings.Contains(stdout, "--child-rules-file <file>  ") || strings.Contains(stdout, "<[name]") {
		t.Errorf("--child-rules-file's placeholder is not <file>:\n%s", stdout)
	}
	for _, want := range []string{"--base-check", "--legs", "--p95", "--repo", "--child-rules-file"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("lint -h does not list %s", want)
		}
	}
	// the banner line lint -h quotes offers the base check with its three inputs
	for _, want := range []string{"[--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]]", "[--child-rules | --child-rules-file <file>]", "the four checks of a coding card"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("lint -h does not quote %q", want)
		}
	}
}
