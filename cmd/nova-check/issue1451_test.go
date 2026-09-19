package main

import (
	"bytes"
	"strings"
	"testing"
)

// #1451 measured nova-check's refusals and found its checking verbs did not name
// the door. ONBOARDING.md point 1 promises an invocation the tool cannot run prints
// `<tool>[ <verb>]: <what was wrong>; run: <tool> help`, so every bare verb's
// refusal line must end at that literal door, in the exact spelling refuse writes.
//
// The suffix is asserted rather than `strings.Contains(stderr, "help")`: a line
// saying "refusing to guess" that then dumps the whole usage contains "help" and
// leaves the reader nothing to type, and a near miss with the wrong spacing or
// semicolon must fail the literal test.
func TestIssue1451EveryMissingFlagRefusalNamesTheDoor(t *testing.T) {
	var helpOut, helpErr bytes.Buffer
	if code := run([]string{"help"}, &helpOut, &helpErr); code != 0 {
		t.Fatalf("`nova-check help` exited %d, want 0; stderr: %s", code, helpErr.String())
	}

	// The verbs are read from the tool's own help output rather than typed from
	// memory, so a verb added later is covered on the day its usage row appears.
	// `version` is the one usage row that is not a check and takes no flags (it
	// prints the build identity and exits 0), so it is not a bare verb here.
	var verbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(helpOut.String(), "\n") {
		rest, ok := strings.CutPrefix(line, "  nova-check ")
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
		t.Fatal("no verbs enumerated from the help banner; the test would pass by checking nothing")
	}

	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{verb}, &stdout, &stderr); code != 2 {
				t.Errorf("`nova-check %s` with no flags exited %d, want 2; stderr: %s", verb, code, stderr.String())
			}
			if stdout.String() != "" {
				t.Errorf("`nova-check %s` with no flags wrote to stdout: %q; a refusal belongs on stderr", verb, stdout.String())
			}
			refusals := 0
			for _, line := range strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n") {
				if line == "" || strings.HasPrefix(line, "  ") {
					continue // the indented hint is guidance, not the door
				}
				refusals++
				if !strings.HasSuffix(line, "; run: nova-check help") {
					t.Errorf("`nova-check %s` refusal does not end at the door: %q", verb, line)
				}
			}
			if refusals == 0 {
				t.Errorf("`nova-check %s` with no flags printed no refusal: %q", verb, stderr.String())
			}
		})
	}
}
