package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Parallel()

	const door = "; run: nova-memory help"

	exit, banner, stderr := runCLI(t, "", "help")
	require.Equalf(t, 0, exit, "`nova-memory help` exited %d, want 0; stderr: %s", exit, stderr)
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
	require.NotEmpty(t, verbs, "no verbs enumerated from the help banner; the test would check nothing")

	for _, verb := range verbs {
		if verb == "version" || verb == "help" {
			// version takes no flags and exits 0. help is the skeleton's usage
			// row, not a verb that refuses.
			continue
		}
		t.Run(verb, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", verb)
			require.Equalf(t, 2, exit, "`nova-memory %s` with no flags exited %d, want 2; stderr: %s", verb, exit, stderr)
			assert.Equalf(t, "", stdout, "`nova-memory %s` refused but wrote to stdout: %q", verb, stdout)
			refusals := 0
			for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
				if line == "" || strings.HasPrefix(line, "  ") {
					continue // an indented hint is guidance on its own line, not the refusal
				}
				refusals++
				assert.Truef(t, strings.HasSuffix(line, door), "`nova-memory %s` refusal does not end at the door: %q", verb, line)
			}
			assert.NotEqualf(t, 0, refusals, "`nova-memory %s` with no flags printed no refusal: %q", verb, stderr)
		})
	}
}
