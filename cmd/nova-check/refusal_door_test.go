package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every refusal of an invocation this tool cannot run ends with the door
// `; run: nova-check help`, the command that says where to look (docs/STANDARD.md,
// "Help and refusal: the six onboarding points"): `<tool>[ <verb>]: <what was wrong>;
// run: <tool> help`. The missing-flag and bad-value refusals carry it too, so a reader
// learns both what was wrong and where to look it up.
//
// Asserting only that the substring "help" appears somewhere in stderr would pass a line
// that says "refusing to guess" and then dumps the whole usage banner: the door has to be
// ON the refusal line a reader scans, so this asserts it line by line, and a bare verb
// that reports several missing flags must carry it on each of those lines.
func TestEveryRefusalNamesTheDoor(t *testing.T) {
	t.Parallel()

	exit, helpOut, helpErr := runCheck(t, "help")
	require.EqualValues(t, 0, exit, "nova-check help: exit %d, want 0; stderr: %s", exit, helpErr)

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
	require.GreaterOrEqual(t, len(verbs), 8, "only %d verbs parsed from help, the parse is not reaching them: %v\n%s", len(verbs), verbs, helpOut)

	const door = "; run: nova-check help"
	refused := 0
	for _, verb := range verbs {
		args := strings.Fields(verb)
		exit, _, stderr := runCheck(t, args...)
		if exit == 0 {
			// The one bare verb with nothing to ask for (version); it is not a refusal.
			continue
		}
		if !assert.EqualValues(t, 2, exit, "%s: bare verb exit %d, want 2; stderr: %s", verb, exit, stderr) {
			continue
		}
		refused++
		for _, line := range strings.Split(strings.TrimRight(stderr, "\n"), "\n") {
			if line == "" || strings.HasPrefix(line, "  ") {
				// An empty line, or the hint that follows a refusal on its own indented
				// line: the door belongs on the refusal line, before the hint.
				continue
			}
			assert.True(t, strings.HasSuffix(line, door), "%s: refusal line without `run: nova-check help`: %q", verb, line)
		}
	}
	require.NotEqualValues(t, 0, refused, "no bare verb refused; this test proved nothing")
}
