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

// CardKindFromText reads a card's kind from its typed KIND: header or a :kind
// pull field. An untyped card is "".
func CardKindFromText(text string) string {
	if k := cardFields(text)["kind"]; k != "" {
		return k
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == ":kind" {
				return strings.ToLower(fields[i+1])
			}
		}
	}
	return ""
}

// cardHeaders are the field lines a card may state its own evidence with. The
// set is closed: a line the card carries that is not one of these is the
// card's business and is never read as evidence.
var cardHeaders = []string{"kind", "files", "packages", "lanes", "lane", "platform", "touches", "deadline"}

// cardFields reads the stated evidence lines, first occurrence winning.
func cardFields(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		for _, h := range cardHeaders {
			if name != h || out[h] != "" {
				continue
			}
			out[h] = strings.ToLower(strings.TrimSpace(value))
		}
	}
	return out
}
