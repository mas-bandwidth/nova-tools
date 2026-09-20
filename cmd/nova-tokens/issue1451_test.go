package main

import (
	"strings"
	"testing"
)

// This is #1451 in nova-tokens: a bare verb that names what is wrong and not the door.
// ONBOARDING.md point 1: an invocation the tool cannot run prints
// `<tool>[ <verb>]: <what was wrong>; run: <tool> help`. The missing-flag and bad-value
// refusals are collected by the refusals collector and printed at main.go:252 with no
// `; run: nova-tokens help`, so every one of them leaves the reader without a place to
// look. The test reads the verbs from the tool's own help banner and invokes each with no
// flags. Asserting only that "help" appears somewhere in stderr would pass a bad refusal
// that said "refusing to guess" and then dumped the whole usage banner (which names help);
// the door is asserted as the literal end of every refusal line instead.
func TestIssue1451EveryRefusalNamesTheDoor(t *testing.T) {
	help := invoke(t, "help")
	wantExit(t, help, 0)

	// Enumerate the verbs from the usage block: a continuation line does not open with
	// `nova-tokens `, and a verb whose banner line carries no flag (version) is not a
	// refusal when invoked bare. Dedupe report and sum, which share two forms.
	var verbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(help.stdout, "\n") {
		line = strings.TrimRight(line, " \t")
		if !strings.HasPrefix(line, "  nova-tokens ") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		verb := fields[1]
		if !strings.Contains(line, " --") || seen[verb] {
			continue
		}
		seen[verb] = true
		verbs = append(verbs, verb)
	}
	if len(verbs) == 0 {
		t.Fatal("no flag-bearing verbs parsed from the help banner")
	}

	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			r := invoke(t, verb)
			wantExit(t, r, 2)
			lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
			for i, line := range lines {
				if line == "" {
					continue
				}
				if !strings.HasSuffix(line, "; run: nova-tokens help") {
					t.Errorf("%s refusal line %d has no door: %q", verb, i+1, line)
				}
			}
		})
	}
}
