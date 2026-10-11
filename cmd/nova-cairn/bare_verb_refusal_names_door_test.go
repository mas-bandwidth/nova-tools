package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1451 measured nova-cairn's refusals and found 0 of its four bare verbs named
// the door. ONBOARDING.md point 1 promises an invocation the tool cannot run
// prints `<tool>[ <verb>]: <what was wrong>; run: <tool> help`, so this asserts
// every refusal line ENDS at that literal door, in the exact spelling the
// existing helper writes.
//
// The suffix is asserted rather than `strings.Contains(stderr, "help")`: a line
// saying `--store is required; refusing to guess` followed by a dumped usage
// banner contains "help" and gives the reader nothing to type, and a near miss
// with the wrong spacing or semicolon must fail the literal test.
func TestIssue1451EveryMissingFlagRefusalNamesTheDoor(t *testing.T) {
	t.Parallel()

	examples, err := onboarding.ExampleLines(cli.OK(t, "help").Stdout, "nova-cairn")
	require.NoError(t, err, "cannot enumerate the bare verbs from the help banner")
	seen := map[string]bool{}
	for _, ex := range examples {
		fields := strings.Fields(ex)
		require.GreaterOrEqual(t, len(fields), 2, "the help example %q names no verb", ex)
		verb := fields[1]
		if seen[verb] {
			continue
		}
		seen[verb] = true
		t.Run(verb, func(t *testing.T) {
			r := cli.Run(verb)
			assert.Equal(t, 2, r.Code, "`nova-cairn %s` with no flags", verb)
			assert.Empty(t, r.Stdout, "`nova-cairn %s` with no flags wrote to stdout; a refusal belongs on stderr", verb)
			lines := strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n")
			require.NotEmpty(t, lines, "`nova-cairn %s` with no flags printed no refusal", verb)
			require.NotEmpty(t, lines[0], "`nova-cairn %s` with no flags printed no refusal", verb)
			for _, line := range lines {
				assert.True(t, strings.HasSuffix(line, "; run: nova-cairn help"), "`nova-cairn %s` refusal does not end at the door: %q", verb, line)
			}
		})
	}
	require.NotEmpty(t, seen, "no verbs enumerated from the help banner; the test would pass by checking nothing")
}
