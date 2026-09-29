package card

import "unicode/utf8"

// MaxIDBytes bounds a card ID and an evidence ID.
const MaxIDBytes = 64

// ID is a card ID: nonempty ASCII letters, digits, underscore and hyphen, at
// most MaxIDBytes, and not one of the reserved words. It holds no comma, colon
// or storage prefix.
type ID string

// The reserved words. A card header uses "-" for a DEPENDS-ON that names nothing
// and "none" for a PATHS, DOORS or PROBES that declares nothing, so neither is a
// card ID: a card named "none" would be a dependency that blocks forever.
const (
	WordDash = "-"
	WordNone = "none"
)

// IsReserved reports whether s is one of the reserved words.
func IsReserved(s string) bool { return s == WordDash || s == WordNone }

// IDFault reports why s is not a card ID, as a cause and a reason that quotes
// nothing of s, or "" when it is one. The causes are required (empty), too-long,
// reserved-word and invalid-value.
func IDFault(s string) (Cause, string) {
	switch {
	case s == "":
		return CauseRequired, "an ID is empty"
	case len(s) > MaxIDBytes:
		return CauseTooLong, "an ID is at most 64 bytes"
	case IsReserved(s):
		return CauseReservedWord, `"-" and "none" are reserved words, never IDs`
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !ok {
			return CauseInvalidValue, "an ID is ASCII letters, digits, underscore and hyphen"
		}
	}
	return "", ""
}

// ValidID reports whether s is a card ID.
func ValidID(s string) bool { c, _ := IDFault(s); return c == "" }

// Valid reports whether the ID is valid.
func (i ID) Valid() bool { return ValidID(string(i)) }

// PrintableASCII reports whether s is nonempty printable ASCII: bytes 0x21 to
// 0x7e, no blank, no control character. It is the grammar of every reference
// (source, landing, evidence and operation IDs, issuer, actor).
func PrintableASCII(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// TextFault reports the first fault of a free-text value: invalid UTF-8 or a
// replacement character, a control character, a Unicode line or paragraph
// separator, a bidirectional control, a zero-width or other format character, or
// a length over max bytes. Empty is not a fault here. The returned cause is one
// of invalid-utf8, control-character or too-long, and "" when the text is clean.
func TextFault(s string, max int) Cause {
	if !utf8.ValidString(s) {
		return CauseInvalidUTF8
	}
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			return CauseInvalidUTF8
		case isHidden(r):
			return CauseControlChar
		}
	}
	if len(s) > max {
		return CauseTooLong
	}
	return ""
}

// isHidden says r is a character no one-line text may carry: a control, a line
// or paragraph separator, a bidirectional control, a zero-width character, a
// byte-order mark or another format character.
func isHidden(r rune) bool {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return true
	case r == 0x2028 || r == 0x2029:
		return true
	case r >= 0x200b && r <= 0x200f: // zero-width blank, non-joiner, joiner, LRM, RLM
		return true
	case r >= 0x202a && r <= 0x202e: // bidi embeddings and overrides
		return true
	case r >= 0x2060 && r <= 0x206f: // word joiner, invisible operators, deprecated format
		return true
	case r >= 0x2066 && r <= 0x2069: // bidi isolates
		return true
	case r == 0xfeff || r == 0x00ad || r == 0x061c || r == 0x180e:
		return true
	case r >= 0xfff9 && r <= 0xfffb: // interlinear annotation
		return true
	case r >= 0xe0000 && r <= 0xe007f: // tag characters
		return true
	}
	return false
}
