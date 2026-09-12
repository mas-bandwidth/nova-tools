package records

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// Canonicalize writes the RFC 8785 canonical JSON of a parsed value: members sorted by the
// UTF-16 code units of their names, no insignificant whitespace, and the minimal string
// escaping ECMAScript's JSON.stringify produces. There is no trailing newline, because the
// digest is over exactly these bytes.
//
// RFC 8785's number serialisation is deliberately absent. This format's bodies carry no
// raw JSON numbers at all -- every usage value is a string, so no lexeme ever makes a
// float64 round trip -- so a number reaching here is refused by parseStrict before it can
// reach the digest. That removes the shortest-round-trip float printing that is the one
// genuinely hard corner of RFC 8785, and it removes it by contract rather than by hope.
func Canonicalize(v Value) ([]byte, error) {
	var buf bytes.Buffer
	if err := canonicalize(v, nil, "body", &buf); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if !utf8.Valid(b) {
		return nil, refuse(RuleInvalidUTF8, "body", "canonical JSON is not valid UTF-8")
	}
	return b, nil
}

func canonicalize(v Value, skip map[string]bool, field string, buf *bytes.Buffer) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		if !skip[RuleInvalidUTF8] && !utf8.ValidString(t) {
			return refuse(RuleInvalidUTF8, field, "string is not valid UTF-8")
		}
		writeCanonicalString(t, buf)
	case json.Number:
		if !skip[RuleRawJSONNumber] {
			return refuse(RuleRawJSONNumber, field, "usage and identity values are JSON strings, never numbers")
		}
		// Only reachable from the test that proves the raw_json_number rule is
		// load-bearing; the lexeme is emitted unchanged there so the fixture has an ID.
		buf.WriteString(t.String())
	case []Value:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := canonicalize(e, skip, indexPath(field, i), buf); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case *Object:
		keys := append([]string(nil), t.keys...)
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if !skip[RuleInvalidUTF8] && !utf8.ValidString(k) {
				return refuse(RuleInvalidUTF8, field, "member name is not valid UTF-8")
			}
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonicalString(k, buf)
			buf.WriteByte(':')
			if err := canonicalize(t.vals[k], skip, field+"."+k, buf); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return refuse(RuleNotJSON, field, "the value has no canonical form")
	}
	return nil
}

// lessUTF16 orders two member names by their UTF-16 code units, which is what RFC 8785
// specifies and is NOT the same as Go's byte ordering: a code point above U+FFFF sorts
// below U+E000 in UTF-16 because its surrogates begin at U+D800, and above it in UTF-8.
func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

const hexDigits = "0123456789abcdef"

// writeCanonicalString escapes exactly what RFC 8785 escapes: the quote, the backslash,
// the five short forms, and every other control character as \u00xx. Everything else,
// including every non-ASCII character, is written literally as UTF-8.
func writeCanonicalString(s string, buf *bytes.Buffer) {
	buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			buf.WriteString(`\"`)
		case c == '\\':
			buf.WriteString(`\\`)
		case c == '\b':
			buf.WriteString(`\b`)
		case c == '\f':
			buf.WriteString(`\f`)
		case c == '\n':
			buf.WriteString(`\n`)
		case c == '\r':
			buf.WriteString(`\r`)
		case c == '\t':
			buf.WriteString(`\t`)
		case c < 0x20:
			buf.WriteString(`\u00`)
			buf.WriteByte(hexDigits[c>>4])
			buf.WriteByte(hexDigits[c&0xf])
		default:
			buf.WriteByte(c)
		}
	}
	buf.WriteByte('"')
}

// ContentID is the identity the format names: "sha256:" and 64 lowercase hex digits of the
// SHA-256 of the value's canonical JSON. Applied to a body it is the envelope's ID, which
// is therefore excluded from its own digest by construction: nothing but the body is
// hashed, so there is no fixed point to solve and no circular dependency to break.
func ContentID(v Value) (string, error) {
	return contentID(v, nil)
}

func contentID(v Value, skip map[string]bool) (string, error) {
	var buf bytes.Buffer
	if err := canonicalize(v, skip, "body", &buf); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
