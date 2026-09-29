package privacy

import (
	"bytes"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
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
