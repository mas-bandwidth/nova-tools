package privacy

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ErrNotText marks bytes the screen cannot read as text: a NUL byte, invalid
// UTF-8, or UTF-16 that does not decode. Such input is refused, never
// measured, since measured as bytes it yields no words and would clear.
var ErrNotText = errors.New("not text")

// DecodeText turns bytes into text. UTF-16 that opens with a byte-order mark
// is decoded; a leading UTF-8 byte-order mark is dropped; anything else must
// be valid UTF-8 with no NUL byte. name is what an error calls the input.
func DecodeText(b []byte, name string) (string, error) {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return decodeUTF16(b[2:], false, name)
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return decodeUTF16(b[2:], true, name)
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return "", fmt.Errorf("%s holds a NUL byte at offset %d, so it is %w (UTF-16 needs a byte-order mark to be read)", name, i, ErrNotText)
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("%s is not valid UTF-8 (first bad byte at offset %d), so it is %w", name, firstInvalid(b), ErrNotText)
	}
	return string(b), nil
}

func decodeUTF16(b []byte, bigEndian bool, name string) (string, error) {
	if len(b)%2 != 0 {
		return "", fmt.Errorf("%s is UTF-16 with an odd number of bytes, so it is %w", name, ErrNotText)
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		if bigEndian {
			units[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			units[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u == 0:
			return "", fmt.Errorf("%s holds a NUL character at UTF-16 unit %d, so it is %w", name, i, ErrNotText)
		case u >= 0xD800 && u < 0xDC00:
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] >= 0xE000 {
				return "", fmt.Errorf("%s holds an unpaired UTF-16 surrogate at unit %d, so it is %w", name, i, ErrNotText)
			}
			i++
		case u >= 0xDC00 && u < 0xE000:
			return "", fmt.Errorf("%s holds an unpaired UTF-16 surrogate at unit %d, so it is %w", name, i, ErrNotText)
		}
	}
	return string(utf16.Decode(units)), nil
}

func firstInvalid(b []byte) int {
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n <= 1 {
			return i
		}
		i += n
	}
	return len(b)
}

// CountWords is how many words text holds: runs of letters and digits that
// hold at least one letter, in any script. A payload of zero words was read
// and holds nothing to compare.
func CountWords(text string) int {
	n, inWord, hasLetter := 0, false, false
	end := func() {
		if inWord && hasLetter {
			n++
		}
		inWord, hasLetter = false, false
	}
	for _, c := range text {
		switch {
		case unicode.IsLetter(c):
			inWord, hasLetter = true, true
		case unicode.IsDigit(c) || unicode.Is(unicode.Mn, c):
			inWord = true
		case unicode.Is(unicode.Cf, c):
			// An invisible formatting character joins, never splits.
		default:
			end()
		}
	}
	end()
	return n
}

// isInvisible reports a formatting character that shows nothing: the
// zero-width characters (U+200B to U+200D), the soft hyphen, the byte-order
// mark, and every other character of Unicode category Cf.
func isInvisible(c rune) bool { return unicode.Is(unicode.Cf, c) }

// foldBlanks is how the marker is compared: the text normalised as words are
// (see normalise), any Unicode whitespace read as one blank with runs
// collapsed, and the ends trimmed.
func foldBlanks(s string) string {
	s = normalise(s)
	var b strings.Builder
	blank := false
	for _, c := range s {
		switch {
		case isInvisible(c):
		case unicode.IsSpace(c):
			blank = b.Len() > 0
		default:
			if blank {
				b.WriteByte(' ')
				blank = false
			}
			b.WriteRune(c)
		}
	}
	return b.String()
}

// normalise is what both sides of a comparison go through before words are
// taken from them: invisible formatting characters and variation selectors
// dropped; Unicode NFKC, so fullwidth letters, ligatures and compatibility
// forms read as their plain letters; case folded; accents removed from
// Latin letters (a combining mark after a Latin letter is dropped, and ß, æ,
// œ, ø, ł, đ, ð, þ, ı are spelt out); curly and modifier apostrophes read as
// ', and the Unicode hyphens as -.
func normalise(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	var b strings.Builder
	for _, c := range s {
		if !isInvisible(c) && !unicode.Is(unicode.Variation_Selector, c) {
			b.WriteRune(c)
		}
	}
	s = strings.ToLower(norm.NFKC.String(b.String()))
	b.Reset()
	latin := false
	for _, c := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, c) {
			if !latin {
				b.WriteRune(c)
			}
			continue
		}
		latin = unicode.Is(unicode.Latin, c)
		if r, ok := spelled[c]; ok {
			b.WriteString(r)
			continue
		}
		switch c {
		case '\u2018', '\u2019', '\u02bc', '\u2032':
			c = '\''
		case '\u2010', '\u2011':
			c = '-'
		}
		b.WriteRune(c)
	}
	return norm.NFC.String(b.String())
}

// spelled are the Latin letters with no decomposition, written out.
var spelled = map[rune]string{'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'ł': "l", 'đ': "d", 'ð': "d", 'þ': "th", 'ı': "i"}

// word is one key taken from normalised text: the key the term is counted
// by, and how many runes long the word was as written, before a plural was
// folded.
type word struct {
	key   string
	runes int
}

// words splits normalised text into keys. A word is a run of letters and
// digits in any script, which may hold a single - or ' between two of them.
// A trailing 's or ' is stripped. A hyphenated word is taken whole, with the
// hyphens removed, and as each of its parts. Every key has its plural folded
// (foldPlural). Leading digits are skipped; a run with no letter is no word.
func words(text string, emit func(word)) {
	isWord := func(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) || unicode.Is(unicode.Mn, c) }
	runes := []rune(text)
	for i := 0; i < len(runes); {
		for i < len(runes) && !unicode.IsLetter(runes[i]) {
			i++
		}
		start := i
		for i < len(runes) {
			c := runes[i]
			if isWord(c) {
				i++
				continue
			}
			if (c == '-' || c == '\'') && i > start && i+1 < len(runes) && isWord(runes[i+1]) {
				i++
				continue
			}
			break
		}
		if i == start {
			continue
		}
		tok := string(runes[start:i])
		if strings.HasSuffix(tok, "'s") {
			tok = tok[:len(tok)-2]
		}
		tok = strings.TrimRight(tok, "'")
		parts := strings.Split(tok, "-")
		if len(parts) > 1 {
			whole := strings.Join(parts, "")
			emit(word{key: foldPlural(whole), runes: utf8.RuneCountInString(whole)})
		}
		for _, p := range parts {
			if p != "" {
				emit(word{key: foldPlural(p), runes: utf8.RuneCountInString(p)})
			}
		}
	}
}

// foldPlural folds a simple English plural into its singular, so both reach
// one key: -ies becomes -y; -es is dropped after s, x, z, ch or sh (and a
// singular ending -se, -xe, -ze, -che or -she drops its e, so house and
// houses meet as wumpus and wumpuses do); otherwise a final s is dropped
// unless the word ends -ss, -us or -is. Irregular plurals, -oes, -ves and
// the plural of an -ie noun are not folded.
func foldPlural(w string) string {
	sibilant := func(s string) bool {
		return strings.HasSuffix(s, "s") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "z") ||
			strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh")
	}
	switch {
	case len(w) >= 5 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) >= 4 && strings.HasSuffix(w, "es") && sibilant(w[:len(w)-2]):
		return w[:len(w)-2]
	case len(w) >= 4 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:len(w)-1]
	case len(w) >= 4 && strings.HasSuffix(w, "e") && sibilant(w[:len(w)-1]):
		return w[:len(w)-1]
	}
	return w
}
