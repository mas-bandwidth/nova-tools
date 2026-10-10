package swarm

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCardPromptIsTheTwoLineContract is nova-tools#3689 (it replaces #3651's
// long grammar): the prompt for a typed card is the card text byte for byte
// and then the two-line contract -- line 1 verbatim, line 2 DONE, ABSTAIN or
// BLOCKED, an optional note -- plus, for a kind whose record needs something
// only the model can know, those fields. It never asks for a field the card
// wrapper writes (typedrec.WrapperOwned). RED ON #3651's PROMPT: it asked for
// SCHEMA, BRANCH, PATHS, CHECK and the rest, and the model's BRANCH
// contradicted the wrapper's.
func TestCardPromptIsTheTwoLineContract(t *testing.T) {
	t.Parallel()

	for _, kind := range typedrec.Kinds {
		t.Run(kind, func(t *testing.T) {
			card := "RESULT: c1 sha=0123456789ab nova-tools " + kind + ": a card\nKIND: " + kind + "\nBASE: dev\n"
			prompt := CardPrompt([]byte(card))
			require.True(t, strings.HasPrefix(prompt, card), "the prompt does not start with the card text byte for byte:\n%s", prompt)
			brief := prompt[len(card):]
			require.Contains(t, brief, "RESULT-FORMAT", "no two-line contract for KIND %s:\n%s", kind, brief)
			require.Contains(t, brief, "line 1: this card's line 1, verbatim", "no two-line contract for KIND %s:\n%s", kind, brief)
			require.Contains(t, brief, "`DONE`, `ABSTAIN <why>` or `BLOCKED <why>`", "no two-line contract for KIND %s:\n%s", kind, brief)
			for _, owned := range typedrec.WrapperOwned {
				assert.NotContains(t, brief, "`"+owned+":", "KIND %s: the brief asks the model for %s, which the wrapper writes:\n%s", kind, owned, brief)
				assert.NotContains(t, brief, "- "+owned+": ", "KIND %s: the brief asks the model for %s, which the wrapper writes:\n%s", kind, owned, brief)
			}
			n := strings.Count(brief, "\n")
			assert.LessOrEqual(t, n, 8, "KIND %s: the brief is %d lines, want the short contract:\n%s", kind, n, brief)
		})
	}
	brief := CardPrompt([]byte("RESULT: c1\nKIND: fix\n"))
	for _, gone := range []string{"SCHEMA: v2", "## Gates", "## Left owed", "Fill in this template"} {
		assert.NotContains(t, brief, gone, "the fix brief still asks for %q", gone)
	}
}

// TestCardPromptLeavesUntypedCardsAlone: a card with no KIND, a runner kind or
// a classification kind is its own prompt, unchanged.
func TestCardPromptLeavesUntypedCardsAlone(t *testing.T) {
	t.Parallel()

	for _, card := range []string{
		"a card\n",
		"RESULT: c1\nKIND: script\n",
		"RESULT: c1\nKIND: go-verb\n",
		"RESULT: c1\nKIND: fix-red\n",
	} {
		got := CardPrompt([]byte(card))
		assert.Equal(t, card, got, "CardPrompt(%q) = %q, want the card unchanged", card, got)
	}
}
