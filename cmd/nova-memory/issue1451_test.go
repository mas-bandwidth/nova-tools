package main

import (
	"strings"
	"testing"
)

// #1451: nova-memory's missing-flag and bad-value refusals bypassed refuse() and
// stopped after "refusing to guess", so a reader learned what was wrong and not
// where to look it up. ONBOARDING.md point 1 says an invocation the tool cannot
// run prints one line per problem, each ending at the door: `<tool>[ <verb>]:
// <what was wrong>; run: <tool> help`. Asserting only that "help" appears
// somewhere in stderr would pass a "refusing to guess" line followed by the
// whole usage, so every refusal line must end at the literal door; a bare verb
// may report several missing flags, one line each, and that is correct.
func TestIssue1451EveryRefusalNamesTheDoor(t *testing.T) {
	const door = "; run: nova-memory help"

	exit, banner, stderr := runCLI(t, "", "help")
	if exit != 0 {
		t.Fatalf("`nova-memory help` exited %d, want 0; stderr: %s", exit, stderr)
	}
	// The verbs come from the tool's own help output, not a list typed here, so a
	// verb added to the banner later is covered the day its row appears.
	var verbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(banner, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nova-memory" || strings.HasPrefix(fields[1], "-") {
			continue
		}
		if !seen[fields[1]] {
			seen[fields[1]] = true
			verbs = append(verbs, fields[1])
		}
	}
	if len(verbs) == 0 {
		t.Fatal("no verbs enumerated from the help banner; the test would check nothing")
	}

	for _, verb := range verbs {
		if verb == "version" {
			// `nova-memory version` takes no flags and exits 0: a usage row, not
			// a bare verb that refuses.
			continue
		}
		t.Run(verb, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", verb)
			if exit != 2 {
				t.Fatalf("`nova-memory %s` with no flags exited %d, want 2; stderr: %s", verb, exit, stderr)
			}
			if stdout != "" {
				t.Errorf("`nova-memory %s` refused but wrote to stdout: %q", verb, stdout)
			}
			refusals := 0
			for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
				if line == "" || strings.HasPrefix(line, "  ") {
					continue // an indented hint is guidance on its own line, not the refusal
				}
				refusals++
				if !strings.HasSuffix(line, door) {
					t.Errorf("`nova-memory %s` refusal does not end at the door: %q", verb, line)
				}
			}
			if refusals == 0 {
				t.Errorf("`nova-memory %s` with no flags printed no refusal: %q", verb, stderr)
			}
		})
	}
}
