package oneline

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guarantees of this package over arbitrary input, not over examples: a
// fixed-seed generator mixes the runes the package exists for (controls, the
// line and paragraph separators, the bidi controls, spaces of every kind,
// "=", quotes, backslashes), ordinary text, and bytes that are not UTF-8.
// Fixed seeds, so a failure names an input that fails again.

var propertyPool = []string{
	// controls, by code point (the characters themselves never appear in this file)
	u(0x0a), u(0x0d), u(0x09), u(0x00), u(0x1b), u(0x7f), u(0x85), u(0x9b),
	// the line and paragraph separators, and the bidi controls
	u(0x2028), u(0x2029),
	u(0x202a), u(0x202b), u(0x202c), u(0x202d), u(0x202e), u(0x2066), u(0x2067), u(0x2068), u(0x2069),
	// spaces of several kinds, "=", quotes, a backslash, an escape spelled out
	u(0x20), u(0xa0), u(0x3000), u(0x2003), "=", u(0x22), bs, bs + "x0a", "'",
	// ordinary text, and format characters that pass through
	"a", "Z", "0", u(0xe9), u(0x65e5) + u(0x672c), u(0x1f642), u(0x200d), u(0xad), u(0xfeff),
	// bytes that are not UTF-8
	string([]byte{0xff}), string([]byte{0xc0}), string([]byte{0xe2, 0x80}), string([]byte{0xf0, 0x9f}),
}

func propertyInput(r *rand.Rand) string {
	var b strings.Builder
	for n := r.IntN(24); n > 0; n-- {
		if r.IntN(8) == 0 {
			b.WriteByte(byte(r.IntN(256)))
			continue
		}
		b.WriteString(propertyPool[r.IntN(len(propertyPool))])
	}
	return b.String()
}

func propertyCases(t *testing.T, seed uint64, check func(t *testing.T, s string)) {
	t.Helper()
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	for i := 0; i < 20000; i++ {
		s := propertyInput(r)
		check(t, s)
		require.False(t, t.Failed(), "seed %d case %d input %q", seed, i, s)
	}
}

// oneLine: valid UTF-8 holding no control character, no line or paragraph
// separator and no bidi control.
func oneLine(t *testing.T, what, out string) {
	t.Helper()
	assert.True(t, utf8.ValidString(out), "%s: not valid UTF-8: %q", what, out)
	for _, r := range out {
		assert.False(t, mustBeEscaped(r), "%s: holds %U: %q", what, r, out)
	}
}

// mustBeEscaped is the oracle's own statement of what may not reach a line,
// by code point, sharing nothing with the package's breaksALine and
// reordersALine: a classification bug there must not be one here as well.
func mustBeEscaped(r rune) bool {
	switch {
	case unicode.IsControl(r): // Cc: C0, DEL, C1
		return true
	case r == 0x2028, r == 0x2029: // the line and paragraph separators
		return true
	case r >= 0x202a && r <= 0x202e: // the bidi embeddings and overrides
		return true
	case r >= 0x2066 && r <= 0x2069: // the bidi isolates
		return true
	}
	return false
}

func TestPropertyEscapeIsOneLineAndLeavesCleanTextAlone(t *testing.T) {
	t.Parallel()
	propertyCases(t, 1, func(t *testing.T, s string) {
		out := Escape(s)
		oneLine(t, "Escape", out)
		assert.Equal(t, Escape(s), out, "Escape is not deterministic on %q", s)
		if utf8.ValidString(s) && !strings.ContainsFunc(s, mustBeEscaped) {
			assert.Equal(t, s, out, "Escape changed text that needed nothing")
		}
		got := Err(errString(s))
		assert.Equal(t, out, got, "Err(%q) = %q, Escape gives %q", s, got, out)
	})
}

func TestPropertyFieldIsOneToken(t *testing.T) {
	t.Parallel()
	propertyCases(t, 2, func(t *testing.T, s string) {
		out := Field(s)
		oneLine(t, "Field", out)
		assert.False(t, strings.ContainsFunc(out, unicode.IsSpace), "Field holds a space: %q", out)
		assert.NotContains(t, out, "=", "Field holds an equals sign")
		if s != "" {
			assert.Len(t, strings.Fields("k="+out), 1, "k=%s is not one token", out)
		}
	})
}

func TestPropertyQuoteIsOneLineAndGivesTheValueBack(t *testing.T) {
	t.Parallel()
	propertyCases(t, 3, func(t *testing.T, s string) {
		out := Quote(s)
		oneLine(t, "Quote", out)
		back, err := strconv.Unquote(out)
		assert.NoError(t, err, "Quote(%q) = %s does not unquote: %v", s, out, err)
		// strconv.Quote writes an invalid byte as \xNN, which unquotes to that byte.
		assert.Equal(t, s, back, "Quote(%q) unquotes to %q", s, back)
	})
}

func TestPropertyCapKeepsAPrefixSaysWhatItDroppedAndHoldsTheCeiling(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(4, 0x6e6f7661))
	propertyCases(t, 4, func(t *testing.T, s string) {
		n := r.IntN(40) - 4
		out := Cap(s, n)
		if len(s) <= n {
			assert.Equal(t, s, out, "Cap(%q, %d) changed a value under the ceiling: %q", s, n, out)
			return
		}
		if s != "" {
			assert.NotEmpty(t, out, "Cap(%q, %d) returned nothing", s, n)
		}
		if out == s {
			// Over the ceiling and unchanged: only when the value is one rune,
			// which Cap keeps whole rather than erase. Anything longer must be
			// cut (a Cap that returns its input passes nothing else here).
			_, size := utf8.DecodeRuneInString(s)
			assert.Equal(t, len(s), size, "Cap(%q, %d) left %d bytes over the ceiling uncut", s, n, len(s))
			return
		}
		at := strings.LastIndex(out, "...+")
		if !assert.GreaterOrEqual(t, at, 0, "Cap(%q, %d) = %q carries no mark", s, n, out) ||
			!assert.True(t, strings.HasSuffix(out, "B"), "Cap(%q, %d) = %q carries no mark", s, n, out) {
			return
		}
		kept := out[:at]
		dropped, err := strconv.Atoi(out[at+len("...+") : len(out)-1])
		msg := []any{"Cap(%q, %d) = %q: kept %q dropped %d of %d bytes", s, n, out, kept, dropped, len(s)}
		assert.NoError(t, err, msg...)
		assert.True(t, strings.HasPrefix(s, kept), msg...)
		assert.NotEmpty(t, kept, msg...)
		assert.Equal(t, len(s), len(kept)+dropped, msg...)
		// No cut inside a rune: what was kept escapes to the start of what the
		// whole value escapes to. (A stray continuation byte after the cut is
		// not the inside of a rune; a byte-level check calls it one.)
		assert.True(t, strings.HasPrefix(Escape(s), Escape(kept)), "Cap(%q, %d) cut inside a rune: %q", s, n, out)
		if utf8.ValidString(s) {
			assert.True(t, utf8.ValidString(kept), "Cap(%q, %d) kept a broken prefix: %q", s, n, kept)
		}
		if n >= len(mark(len(s)))+utf8.UTFMax {
			assert.LessOrEqual(t, len(out), n, "Cap(%q, %d) is over the ceiling", s, n)
		}
		oneLine(t, "Escape(Cap)", Escape(out))
	})
}

// The one case where a value over its ceiling comes back whole, its
// neighbours, and the cuts security#30 L13 pinned, as examples beside the
// property: the mark stays truthful, so a value that loses nothing comes back
// unchanged with no zero-dropped mark.
func TestCapExamples(t *testing.T) {
	t.Parallel()
	one, two := u(0x65e5), u(0x65e5)+u(0x672c) // three bytes, six bytes
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 5, "hello"},
		{"hello", 1000, "hello"},
		{u(0x4e2d), 0, u(0x4e2d)},      // too small for the mark and a rune: the first rune is the whole input
		{"hello world", 5, "h...+10B"}, // the mark for 11 bytes is seven, so the budget clamps to one
		{"hello world hello", 12, "hello...+12B"},
		{one, 1, one},
		{one, 0, one},
		{one, -5, one},
		{"a", 0, "a"},
		{two, 1, one + "...+3B"},
		{two, 5, one + "...+3B"},
		{two, 6, two},
		{"ab", 1, "a...+1B"},
		{"abcdefghij", 9, "ab...+8B"}, // the mark is sized for the whole length, seven bytes
		{string([]byte{0xff, 0x80, 0x80}), 1, string([]byte{0xff}) + "...+2B"},
	} {
		t.Run(fmt.Sprintf("%q,%d", c.in, c.n), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, Cap(c.in, c.n))
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
