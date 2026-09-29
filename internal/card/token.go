package card

import (
	"regexp"
	"strconv"
)

var (
	tokenRE   = regexp.MustCompile(`^[A-Za-z0-9.:/@+_-]+$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	counterRE = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	kindRE    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// TokenChars is the alphabet of a token, for a message.
const TokenChars = "letters, digits and . : / @ + _ -"

// ValidToken reports whether s is a token of at most max bytes: nonempty, drawn
// from letters, digits and `. : / @ + _ -`. Every reference of the card layer
// (an issuer, an actor, an operation ID, a source artifact, a landing identity, a
// verifier) is a token, so it is printable ASCII, holds no blank, comma or quote,
// and needs no escaping inside a JSON string or an evidence record line.
func ValidToken(s string, max int) bool {
	return s != "" && len(s) <= max && tokenRE.MatchString(s)
}

// ValidName reports whether s is a table or row name of at most max bytes:
// letters, digits, underscore, dot and hyphen, starting with a letter, digit or
// underscore.
func ValidName(s string, max int) bool {
	return s != "" && len(s) <= max && nameRE.MatchString(s)
}

// ValidKind reports whether s has the form of a card kind: lower-case letters,
// digits and hyphen, starting with a letter, at most 32 bytes. The registry of
// kinds is internal/hygiene/kinds.txt; this checks only the form.
func ValidKind(s string) bool { return s != "" && len(s) <= 32 && kindRE.MatchString(s) }

// ValidCounter reports whether s is a decimal counter bounded as uint64 with no
// sign, no leading zero and no float form.
func ValidCounter(s string) bool {
	if len(s) == 0 || len(s) > MaxCounterDigits || !counterRE.MatchString(s) {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// CounterAtLeastOne reports whether s is a valid counter that is not zero.
func CounterAtLeastOne(s string) bool { return ValidCounter(s) && s != "0" }

// Count reads a valid counter; an empty string reads as zero, the value of an
// absent counter. ok is false for anything that is not a valid counter.
func Count(s string) (n uint64, ok bool) {
	if s == "" {
		return 0, true
	}
	if !ValidCounter(s) {
		return 0, false
	}
	n, _ = strconv.ParseUint(s, 10, 64)
	return n, true
}

// TaggedPrefix is the text prefix of a digest in a stored field or an evidence
// record line: `sha256:` and the 64 hexadecimal characters.
const TaggedPrefix = "sha256:"

// Tagged is the digest as text in a stored field: `sha256:<64 hex>`.
func (d Digest) Tagged() string { return TaggedPrefix + string(d) }

// ParseTagged reads `sha256:<64 hex>`.
func ParseTagged(s string) (Digest, bool) {
	if len(s) != len(TaggedPrefix)+64 || s[:len(TaggedPrefix)] != TaggedPrefix {
		return "", false
	}
	d := Digest(s[len(TaggedPrefix):])
	return d, d.Valid()
}

// ValidHead reports whether s is a code head: a git object id, or for a card
// that has no pull request, the tagged digest of its result artifact.
func ValidHead(s string) bool {
	if ValidObjectID(s) {
		return true
	}
	_, ok := ParseTagged(s)
	return ok
}
