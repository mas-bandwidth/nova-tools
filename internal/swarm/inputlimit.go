package swarm

import (
	"fmt"
	"strings"
)

// PhraseFloor is the shortest a provider's sentence may be, in characters, and it is a FLOOR
// because a phrase is a knife: a job classed `input-limit` is a job that is never retried.
const PhraseFloor = 12

// TooShortForAPhrase is what a description's own phrase must clear, and the reason when it
// does not. `input_limit_phrases: ["limit"]` would class every failed job whose log holds the
// word `limit` and refuse each one its retry, and non-emptiness was the only floor there was
// (Fable's read of #150, finding 4). A provider's refusal is a SENTENCE: PhraseFloor
// characters at least, and a space or a digit in it, so that no single word can be one.
func TooShortForAPhrase(phrase string) (string, bool) {
	trimmed := strings.TrimSpace(phrase)
	switch {
	case trimmed == "":
		return "it is empty", false
	case len([]rune(trimmed)) < PhraseFloor:
		return fmt.Sprintf("it is %d characters and a provider's sentence is at least %d", len([]rune(trimmed)), PhraseFloor), false
	case !strings.ContainsAny(trimmed, " \t") && !strings.ContainsAny(trimmed, "0123456789"):
		return "it is one word, and one word appears in a transcript that quotes it", false
	}
	return "", true
}

// stripPaint removes the ANSI escape sequences a harness writes to a terminal, so the
// provider's sentence reads as a sentence on the one line a person is meant to read. Only
// the sequences are removed; every other byte, printable or not, is left for oneline.Escape
// to render, because this function's job is legibility and never sanitation.
func stripPaint(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		// CSI: ESC [ <parameters> <final byte 0x40-0x7e>. Anything else after ESC is one
		// byte of a shorter sequence and is dropped with the ESC.
		j := i + 1
		if j < len(s) && s[j] == '[' {
			j++
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
		}
		i = j
	}
	return b.String()
}
