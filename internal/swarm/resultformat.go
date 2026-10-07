package swarm

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// CardPrompt is the message a native run hands its harness for a card: the
// card text byte for byte and, for a typed card (a KIND: one of
// typedrec.Kinds), the RESULT-FORMAT paragraph typedrec.ResultFormat renders.
// Since nova-tools#3689 that is the two-line contract: line 1 verbatim, line 2
// DONE, ABSTAIN or BLOCKED, an optional note (and, for a kind that needs them,
// the fields only the model can know); the card wrapper writes every other
// field (typedrec.Synthesize). A card of any other kind is its own prompt,
// unchanged.
func CardPrompt(card []byte) string {
	text := string(card)
	format := typedrec.ResultFormat(CardKindFromText(text))
	if format == "" {
		return text
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + "\n" + format
}
