package swarm

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// CardPrompt is the message a native run hands its harness for a card: the
// card text byte for byte and, for a typed card (a KIND: one of
// typedrec.Kinds), the RESULT-FORMAT paragraph typedrec.ResultFormat renders
// from the Contract table card end validates against (nova-tools#3651). A card
// of any other kind is its own prompt, unchanged.
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
