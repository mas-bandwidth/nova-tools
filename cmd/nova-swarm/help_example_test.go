package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func swarmHelp(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	code := swarmRun(args, &out, &errb)
	require.Equal(t, 0, code, "nova-swarm %s: exit %d, stderr %q", strings.Join(args, " "), code, errb.String())
	require.Zero(t, errb.Len(), "nova-swarm %s: exit %d, stderr %q", strings.Join(args, " "), code, errb.String())
	return out.String()
}

// native and member each show one example line in their -h, above the
// flags, and every flag the example uses is one the verb's own help lists.
func TestTheLaunchVerbsShowAnExampleMadeOfTheirOwnFlags(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"native", "member"} {
		help := swarmHelp(t, verb, "-h")
		ex, ok := verbExamples[verb]
		require.True(t, ok, "%s has no example", verb)
		if !assert.Contains(t, help, "example:\n  "+ex+"\n", "%s -h lacks its example %q:\n%s", verb, ex, help) {
			continue
		}
		flags := help[strings.Index(help, "flags:\n"):]
		assert.LessOrEqual(t, strings.Index(help, "example:"), strings.Index(help, "flags:"), "%s -h shows the example after the flags", verb)
		for _, w := range strings.Fields(ex) {
			assert.False(t, strings.HasPrefix(w, "--") && !strings.Contains(flags, "\n  "+w+" ") && !strings.Contains(flags, "\n  "+w+"\n"), "%s: the example uses %s, which %s -h does not list", verb, w, verb)
		}
		assert.True(t, strings.HasPrefix(ex, "nova-swarm "+verb+" "), "the %s example does not run %s: %q", verb, verb, ex)
	}
}

// template -h lists the lines a card needs, and every line it names is one the
// card template itself writes, so the help and the template cannot part.
func TestTemplateHelpListsTheCardsRequiredLines(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "template", "-h")
	card := swarmHelp(t, "template", "--name", "card")
	for _, anchor := range []string{"RESULT: <label> sha=<sha12>", "Deadline: finish within <n> minutes.", "RULES.", "THE TASK.", "STEP 1.", "RESULT.md"} {
		assert.Contains(t, help, anchor, "template -h does not list %q", anchor)
		assert.Contains(t, card, anchor, "template --name card does not write %q, which template -h lists", anchor)
	}
	for _, typed := range []string{"KIND:", "PATHS:", "TEST:", "DEPENDS-ON:", "DONE-WHEN:"} {
		assert.Contains(t, help, typed, "template -h does not name the typed line %s", typed)
	}
}

// The banner names the way to an example card, and what that verb prints is a
// card the lint's child rules accept.
func TestTheBannerNamesTheExampleCard(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "help")
	assert.Contains(t, help, "Prepare a card: save nova-swarm template --name card to a file", "the banner does not name template --name card as the way to an example card")
}
