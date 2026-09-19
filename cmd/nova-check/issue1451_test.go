package main

import (
	"strings"
	"testing"
)

// Defect #1451: every refusal of an invocation this tool cannot run must end with the
// door `; run: nova-check help`, per docs/ONBOARDING.md point 1 -- "an invocation the
// tool cannot run prints one line ... '<tool>[ <verb>]: <what was wrong>; run: <tool>
// help'". The missing-flag and bad-value refusals bypassed the one helper (`refuse`) that
// writes that door, so a reader learned what was wrong and not where to look it up.
//
// Asserting only that the substring "help" appears somewhere in stderr would pass a line
// that says "refusing to guess" and then dumps the whole usage banner: the door has to be
// ON the refusal line a reader scans, so this asserts it line by line, and a bare verb
// that reports several missing flags must carry it on each of those lines.
func TestIssue1451EveryRefusalNamesTheDoor(t *testing.T) {
	exit, helpOut, helpErr := runCheck(t, "help")
	if exit != 0 {
		t.Fatalf("nova-check help: exit %d, want 0; stderr: %s", exit, helpErr)
	}

	// The verbs come from the tool's own `help` output, not from a list typed here: a
	// usage line starts with `  nova-check `, the first word is the verb, and a second
	// word is part of it only when a flag (or the record verb's flag group) follows.
	seen := map[string]bool{}
	var verbs []string
	for _, line := range strings.Split(helpOut, "\n") {
		line = strings.TrimRight(line, " ")
		if !strings.HasPrefix(line, "  nova-check ") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "  nova-check "))
		if len(fields) == 0 {
			continue
		}
		verb := fields[0]
		if len(fields) >= 3 && !strings.HasPrefix(fields[1], "-") && !strings.HasPrefix(fields[1], "(") &&
			(strings.HasPrefix(fields[2], "-") || strings.HasPrefix(fields[2], "(")) {
			verb += " " + fields[1]
		}
		if !seen[verb] {
			seen[verb] = true
			verbs = append(verbs, verb)
		}
	}
	if len(verbs) < 8 {
		t.Fatalf("only %d verbs parsed from help, the parse is not reaching them: %v\n%s", len(verbs), verbs, helpOut)
	}

	const door = "; run: nova-check help"
	refused := 0
	for _, verb := range verbs {
		args := strings.Fields(verb)
		exit, _, stderr := runCheck(t, args...)
		if exit == 0 {
			// The one bare verb with nothing to ask for (version); it is not a refusal.
			continue
		}
		if exit != 2 {
			t.Errorf("%s: bare verb exit %d, want 2; stderr: %s", verb, exit, stderr)
			continue
		}
		refused++
		for _, line := range strings.Split(strings.TrimRight(stderr, "\n"), "\n") {
			if line == "" || strings.HasPrefix(line, "  ") {
				// An empty line, or the hint that follows a refusal on its own indented
				// line: the door belongs on the refusal line, before the hint.
				continue
			}
			if !strings.HasSuffix(line, door) {
				t.Errorf("%s: refusal line without the door: %q", verb, line)
			}
		}
	}
	if refused == 0 {
		t.Fatal("no bare verb refused; this test proved nothing")
	}
}
