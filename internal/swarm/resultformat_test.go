package swarm

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
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
			if !strings.HasPrefix(prompt, card) {
				t.Fatalf("the prompt does not start with the card text byte for byte:\n%s", prompt)
			}
			brief := prompt[len(card):]
			if !strings.Contains(brief, "RESULT-FORMAT") || !strings.Contains(brief, "line 1: this card's line 1, verbatim") || !strings.Contains(brief, "`DONE`, `ABSTAIN <why>` or `BLOCKED <why>`") {
				t.Fatalf("no two-line contract for KIND %s:\n%s", kind, brief)
			}
			for _, owned := range typedrec.WrapperOwned {
				if strings.Contains(brief, "`"+owned+":") || strings.Contains(brief, "- "+owned+": ") {
					t.Errorf("KIND %s: the brief asks the model for %s, which the wrapper writes:\n%s", kind, owned, brief)
				}
			}
			if n := strings.Count(brief, "\n"); n > 8 {
				t.Errorf("KIND %s: the brief is %d lines, want the short contract:\n%s", kind, n, brief)
			}
		})
	}
	brief := CardPrompt([]byte("RESULT: c1\nKIND: fix\n"))
	for _, gone := range []string{"SCHEMA: v2", "## Gates", "## Left owed", "Fill in this template"} {
		if strings.Contains(brief, gone) {
			t.Errorf("the fix brief still asks for %q", gone)
		}
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
		if got := CardPrompt([]byte(card)); got != card {
			t.Errorf("CardPrompt(%q) = %q, want the card unchanged", card, got)
		}
	}
}
