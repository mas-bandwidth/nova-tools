package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHelpGroupsTheDogfoodVerbsInsideUsage pins the usage block: ledger, record
// and gate appear contiguously under one dogfood heading, each with its
// one-line effect, and that group stands before the explanation (docs/SPEC.md
// §dogfood). A reader who reads usage first otherwise meets three unrelated verbs.
func TestHelpGroupsTheDogfoodVerbsInsideUsage(t *testing.T) {
	t.Parallel()

	exit, help, stderr := runCheck(t, "help")
	require.EqualValues(t, 0, exit, "nova-check help: %s", stderr)

	rest, ok := strings.CutPrefix(help[strings.Index(help, "usage:\n"):], "usage:\n")
	require.True(t, ok)
	usage, explanation, ok := strings.Cut(rest, "\n\n")
	require.True(t, ok, "the usage block does not end before the explanation:\n%s", help)

	var lines []string
	for _, l := range strings.Split(usage, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	heading := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "nova-check (dogfood)") {
			heading = i
			break
		}
	}
	require.NotEqual(t, -1, heading, "the usage block has no dogfood heading before the three verbs:\n%s", usage)

	verbs := []struct {
		name   string
		effect string
	}{
		{"dogfood ledger", "reads the verb list against the receipts, one row per verb"},
		{"dogfood record", "appends one receipt"},
		{"dogfood gate", "exits 1 with the verbs no non-author has run and the edges nobody has cleared"},
	}
	require.GreaterOrEqual(t, len(lines), heading+1+len(verbs), "the heading is not followed by the three verbs:\n%s", usage)
	for i, v := range verbs {
		line := lines[heading+1+i]
		require.True(t, strings.HasPrefix(line, "nova-check "+v.name+" "), "verb %s is not under the heading:\n%s", v.name, line)
		cmd, effect, found := strings.Cut(line, "  ")
		require.True(t, found, "verb %s has no one-line effect:\n%s", v.name, line)
		require.Contains(t, cmd, v.name)
		require.Equal(t, v.effect, strings.TrimSpace(effect), "verb %s effect", v.name)
	}

	groupAt := strings.Index(help, "nova-check (dogfood)")
	explainAt := strings.Index(help, "An edge is what the run found")
	require.GreaterOrEqual(t, groupAt, 0)
	require.Greater(t, explainAt, groupAt, "the dogfood group is not before the explanation")
	require.NotContains(t, explanation, "dogfood gate exits 1", "the explanation still carries the sentence the gate's effect now holds")
}
