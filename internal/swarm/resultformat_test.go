package swarm

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// TestCardPromptNamesTheResultGrammar is nova-tools#3651: a DONE card ended
// `missing` because nothing told the worker the RESULT.md fields card end
// requires. The prompt for a typed card names every field the Contract table
// requires of its kind (R, D and P rows) and every required section, read from
// the same tables typedrec.ParseResult reads. RED WITHOUT CardPrompt: the
// prompt was the card text alone.
func TestCardPromptNamesTheResultGrammar(t *testing.T) {
	for _, kind := range typedrec.Kinds {
		t.Run(kind, func(t *testing.T) {
			card := "RESULT: c1 sha=0123456789ab nova-tools " + kind + ": a card\nKIND: " + kind + "\nBASE: dev\n"
			prompt := CardPrompt([]byte(card))
			if !strings.HasPrefix(prompt, card) {
				t.Fatalf("the prompt does not start with the card text byte for byte:\n%s", prompt)
			}
			brief := prompt[len(card):]
			if !strings.Contains(brief, "RESULT-FORMAT") || !strings.Contains(brief, "SCHEMA: v2") || !strings.Contains(brief, "KIND: "+kind) {
				t.Fatalf("no RESULT-FORMAT paragraph for KIND %s:\n%s", kind, brief)
			}
			for _, field := range typedrec.Contract.FieldKeys() {
				switch typedrec.Contract.RequirementFor(field, kind) {
				case typedrec.ReqRequired, typedrec.ReqDone, typedrec.ReqPass:
					if !strings.Contains(brief, "- "+field+": ") {
						t.Errorf("KIND %s: the brief does not name required field %s:\n%s", kind, field, brief)
					}
				case typedrec.ReqUnknown:
					if strings.Contains(brief, "- "+field+": ") {
						t.Errorf("KIND %s: the brief names %s, a field this kind refuses", kind, field)
					}
				}
			}
			for _, s := range typedrec.RequiredSections(kind) {
				if !strings.Contains(brief, "`## "+s+"`") || !strings.Contains(brief, "\n## "+s+"\n") {
					t.Errorf("KIND %s: the brief does not name and template section ## %s:\n%s", kind, s, brief)
				}
			}
		})
	}

	// The fields #3651 names for a fix card, spelled out, so a Contract edit
	// that drops one is seen here too.
	brief := CardPrompt([]byte("RESULT: c1\nKIND: fix\n"))
	for _, want := range []string{"SCHEMA: v2", "- KIND: ", "- ATTEMPT: ", "- CHECK: ", "- REPO: ", "- BRANCH: ", "- PATHS: ", "- RED: ", "- GREEN: ", "## Gates", "## Left owed"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the fix brief does not name %q", want)
		}
	}
}

// TestCardPromptLeavesUntypedCardsAlone: a card with no KIND, a runner kind or
// a classification kind is its own prompt, unchanged.
func TestCardPromptLeavesUntypedCardsAlone(t *testing.T) {
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
