package main

import (
	"strings"
	"testing"
)

// #1451: an invocation this tool cannot run must print, on the refusal line
// itself, `<tool>[ <verb>]: <what was wrong>; run: <tool> help`. `awakeRefused`
// printed `AWAKE REFUSED <what>` and stopped, so a reader learned what was wrong
// and never where to look it up. Asserting only that "help" appears somewhere in
// stderr would pass a line that says "refusing to guess" and then dumps the whole
// usage; the door is the LAST clause of each refusal line, and one bare verb may
// refuse several missing flags, one line each, which this test reads one by one.
func TestIssue1451EveryRefusalNamesTheDoor(t *testing.T) {
	const door = "; run: nova-wake help"

	help := wakeRun(t, "help")
	if help.exit != 0 {
		t.Fatalf("nova-wake help exit = %d, want 0; stderr: %s", help.exit, help.stderr)
	}
	var verbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(help.stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "  nova-wake ") {
			continue
		}
		verb := strings.Fields(line)[1]
		if !seen[verb] {
			seen[verb] = true
			verbs = append(verbs, verb)
		}
	}
	if len(verbs) == 0 {
		t.Fatal("no verbs read from the usage block; the enumeration is looking in the wrong place")
	}

	// version and help are complete invocations and exit 0; every other bare
	// verb is an unusable invocation and must refuse, naming the door per line.
	complete := map[string]bool{"version": true, "help": true}
	for _, verb := range verbs {
		r := wakeRun(t, verb)
		if r.exit == 0 {
			if !complete[verb] {
				t.Errorf("bare %s exited 0; only version and help are complete invocations", verb)
			}
			continue
		}
		if r.exit != 2 {
			t.Errorf("bare %s exit = %d, want 2", verb, r.exit)
			continue
		}
		if strings.TrimSpace(r.stderr) == "" {
			t.Errorf("bare %s refused with nothing on stderr", verb)
			continue
		}
		for _, line := range strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n") {
			if line == "" || strings.HasPrefix(line, "  ") {
				continue // an indented hint under the refusal it belongs to
			}
			if !strings.HasSuffix(line, door) {
				t.Errorf("bare %s refusal line names no door: %q", verb, line)
			}
		}
	}
}
