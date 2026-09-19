package main

import (
	"bytes"
	"strings"
	"testing"
)

// #1451: nova-decide's typed refusals never named the door. All four bare
// verbs -- route, tune, log, outcome -- route through refuse, which printed
// "<prefix> REFUSED reason=<r> <detail>" and stopped, so a newcomer learned
// what was wrong but not where to look it up.
// ONBOARDING point 1: an invocation the tool cannot run prints one line,
// "<tool>[ <verb>]: <what was wrong>; run: <tool> help". Asserting only that
// "help" appears somewhere would pass "refusing to guess" plus the whole banner.
func TestIssue1451EveryMissingFlagRefusalNamesTheDoor(t *testing.T) {
	const door = "; run: nova-decide help"

	verbs := usageVerbs(t)
	if len(verbs) == 0 {
		t.Fatal("no verbs parsed from the usage banner; this test checked nothing")
	}
	refusals := 0
	for _, verb := range verbs {
		if verb == "help" {
			// The door itself is not a missing-flag refusal: `nova-decide help`
			// prints the banner on stdout and exits 0.
			continue
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{verb}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("`nova-decide %s` with no flags exits %d, want 2 (could not run; stdout=%q stderr=%q)", verb, code, stdout.String(), stderr.String())
			continue
		}
		refusals++
		if stdout.Len() != 0 {
			t.Errorf("`nova-decide %s`: a refusal belongs on stderr, stdout had %q", verb, stdout.String())
		}
		line := stderr.String()
		if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
			t.Errorf("`nova-decide %s`: a refusal is one line, got %q", verb, line)
		}
		if body := strings.TrimSuffix(line, "\n"); !strings.HasSuffix(body, door) {
			t.Errorf("`nova-decide %s` names no door; the line must end %q, got %q", verb, door, body)
		}
	}
	if refusals == 0 {
		t.Fatal("no bare verb refused; this test checked nothing")
	}
}

// usageVerbs reads the verbs out of the tool's own usage banner, so a verb added
// to the banner later is covered without editing this test.
func usageVerbs(t *testing.T) []string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("`nova-decide help` exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	seen := map[string]bool{}
	var verbs []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nova-decide" {
			continue
		}
		verb := fields[1]
		if strings.HasPrefix(verb, "-") || seen[verb] {
			continue
		}
		seen[verb] = true
		verbs = append(verbs, verb)
	}
	return verbs
}
