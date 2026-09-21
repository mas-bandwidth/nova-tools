package main

import (
	"strings"
	"testing"
)

// TestIssue1451EveryMissingFlagRefusalNamesTheDoor enforces docs/ONBOARDING.md
// point 1: an invocation the tool cannot run prints
// `<tool>[ <verb>]: <what was wrong>; run: <tool> help`. #1451 measured that only
// 3 of nova-fuse's six bare verbs named that door, and the missing --box refusal
// was the worst of them: it said what was wrong and stopped, leaving the reader
// to guess where the usage lives. Asserting merely that "help" appears somewhere
// in stderr would pass a line that still says "refusing to guess" and then dumps
// the whole banner, so this pins the door as the literal END of every refusal
// line. The verbs come from the tool's own help output, so a verb added later is
// covered rather than remembered.
func TestIssue1451EveryMissingFlagRefusalNamesTheDoor(t *testing.T) {
	code, help, helpErr := capture(t, []string{"help"}, nowish())
	if code != 0 {
		t.Fatalf("`nova-fuse help` exits %d, want 0; stderr: %s", code, helpErr)
	}

	// Enumerate the verbs from the banner's usage lines rather than typing a list
	// from memory. `version` is skipped on purpose: with no flags it prints the
	// build identity and exits 0, so it has no missing-flag refusal at all.
	var verbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(help, "\n") {
		rest, ok := strings.CutPrefix(line, "  nova-fuse ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || fields[0] == "version" || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		verbs = append(verbs, fields[0])
	}
	if len(verbs) == 0 {
		t.Fatal("no verbs parsed from `nova-fuse help`; the test would pass by checking nothing")
	}

	for _, verb := range verbs {
		code, out, errOut := capture(t, []string{verb}, nowish())
		if code != 2 {
			t.Errorf("bare `nova-fuse %s`: exit = %d, want 2 (could not run); stderr: %q", verb, code, errOut)
			continue
		}
		if out != "" {
			t.Errorf("bare `nova-fuse %s`: a refusal wrote to stdout: %q", verb, out)
		}
		refusals := 0
		for _, line := range strings.Split(strings.TrimSuffix(errOut, "\n"), "\n") {
			if strings.HasPrefix(line, "  ") {
				// The one indented hint line a missing-flag refusal may carry;
				// the door is not the hint, so the hint is not the subject here.
				continue
			}
			refusals++
			if !strings.HasSuffix(line, "; run: nova-fuse help") {
				t.Errorf("bare `nova-fuse %s`: a refusal does not name the door: %q", verb, line)
			}
		}
		if refusals == 0 {
			t.Errorf("bare `nova-fuse %s`: no refusal line at all; stderr: %q", verb, errOut)
		}
		if strings.Contains(errOut, "usage:") {
			t.Errorf("bare `nova-fuse %s`: the refusal dumps the banner instead of naming the door: %q", verb, errOut)
		}
	}
}
