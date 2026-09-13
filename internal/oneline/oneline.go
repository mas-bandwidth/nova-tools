/*
Package oneline is the one escape every binary in this repo renders caller-supplied or
stored text through before it reaches an event line. SPEC.md promises one machine-scannable
line per event, and that promise is only true if nothing a path, a reason, a stored key or
an error's text contains can add a second line, repaint a terminal, or pose as a field the
tool did not write. The package is shared so that the promise is made in one place and met
in the same way by nova-check, nova-fuse, nova-memory and nova-self-talk.

Three renderings, one escape form:

	Escape  one line: every control character, the Unicode line and paragraph separators,
	        and the bidi controls become visible escapes. Used for the free-text tail of a
	        line and for positional slots such as a path, which keep their spaces.
	Field   one token: Escape, and then every whitespace character and every "=" as well,
	        so the value of a key=value field is a single whitespace-free token holding no
	        "=" -- a key=value search can only ever match a field the tool wrote.
	Quote   one line, and PASTEABLE: a double-quoted Go string literal. For the values a
	        person copies out of the output and types back in -- a roster name holding a
	        space, which Field would render \x20 and nobody could paste.
	Err     Escape over an error's text, with nil spelled the way fmt would spell it.

The escape form is \xNN for a code point below U+0080 and \uNNNN above it, lower-case hex in
both, and a byte that is not valid UTF-8 is written as \xNN by its own value. The escape is
not injective -- a literal backslash is not escaped, so a stored newline and the four
characters \x0a print the same -- which SPEC.md states: the output proves one line, never
which of the two was stored. Nothing is ever shortened to nothing, because a reason a
person cannot read is not a record, and every rendering is deterministic.
*/
package oneline

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Escape renders free text for an event line, and this is the guarantee: nothing the text
// contains can add a second line or repaint a terminal.
//
// Every control character (Unicode category Cc: the C0 range including \n, \r and \t, DEL,
// and the C1 range) becomes a visible escape. So do U+2028 and U+2029, the line and
// paragraph separators, which are Zl and Zp rather than Cc: they break a line for Python's
// str.splitlines and for every UAX-14 line breaker. And so do the bidi controls, U+202A
// to U+202E (the embeddings and overrides) and U+2066 to U+2069 (the isolates), which are
// format characters rather than controls: a terminal that honors them displays the rest
// of the line with its visible order rearranged, which is the same hole aimed at an
// operator rather than a parser. The other format characters -- the zero-width joiner
// that emoji sequences are built from, the soft hyphen, the byte-order mark -- pass
// through, because they do not reorder what an operator sees.
//
// A byte that is not valid UTF-8 is escaped by its own value in the same \xNN form. Box
// content and file content that arrived through a JSON or UTF-8 decoder has had U+FFFD
// substituted for it already, so that branch is reached only by text that passed through
// neither, such as an error's. Everything else printable passes through untouched,
// including non-ASCII.
func Escape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case breaksALine(r) || reordersALine(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		case !unicode.IsControl(r):
			b.WriteRune(r)
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	return b.String()
}

// Field renders the value of a key=value field, or any other slot a scanner reads as one
// token. It is Escape and then more: every whitespace character (unicode.IsSpace, so a
// non-breaking or ideographic space as well as the ASCII one) and every "=" is escaped in
// the same form, \x20 and \x3d for the two ASCII cases. The value is therefore a single
// whitespace-free token holding no "=", so a whitespace-splitting scanner sees one field
// where the tool wrote one, and a search for key=value can match only a field the tool
// wrote and never text a stored key or a caller's path happens to contain. The specimen
// this exists for is a stored quarantine key of `x lockdown=clear quarantines=0`, which
// printed raw made a grep for lockdown=clear match a line about a blown lockdown.
func Field(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '=' || (r < 0x80 && (unicode.IsSpace(r) || unicode.IsControl(r))):
			fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.IsSpace(r) || unicode.IsControl(r) || breaksALine(r) || reordersALine(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// Quote renders a value a person is meant to COPY: a roster name, which may hold a space
// and is pasted back into a To: line.
//
// FIELD IS WRONG FOR THOSE, and it was used for them. Field escapes every whitespace
// character, so `nova-bus names` printed `Ada\x20Vale` -- one token a scanner can
// read, and a name nobody can paste into the header of a note. The whole purpose of that
// verb is to tell a person how to spell a To line this tool will accept, and it was
// telling them something the tool would refuse.
//
// The one-line guarantee is kept by a different route rather than dropped. strconv.Quote
// escapes every control character and every rune Go considers unprintable -- U+2028 and
// U+2029, which are Zl and Zp and end a line for a Unicode-aware reader, and the bidi
// controls, which are Cf -- as well as the double quote and the backslash themselves. So
// the result is one line whatever the value holds, the delimiters say where the value
// starts and stops even when it holds a space, and unlike Escape it is INJECTIVE: a
// backslash is escaped too, so what is between the quotes is the value and nothing else.
// A list of these is joined with ";" between the closing quote and the next opening one,
// which is what a To line's own separator is.
func Quote(s string) string { return strconv.Quote(s) }

// Err renders an error into the reason slot of an event, a refusal or a note. An error's
// text carries whatever the path that produced it carried, so an argument holding a
// newline would otherwise break the line in two: the caller's own argument rather than
// stored content, and the guarantee covers both. A nil error keeps fmt's own spelling
// rather than becoming a sentence claiming more than is known; callers reach this with
// nil when a write landed and its verification failed for another reason.
func Err(err error) string {
	if err == nil {
		return "<nil>"
	}
	return Escape(err.Error())
}

// breaksALine: the two separators that are not Cc and still end a line for a reader that
// follows Unicode rather than counting newlines.
func breaksALine(r rune) bool { return r == 0x2028 || r == 0x2029 }

// reordersALine: the bidi embeddings, overrides and isolates. Format characters, not
// controls, so unicode.IsControl does not see them, and the ones that let a reason
// display in an order other than the one it was written in.
func reordersALine(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// TailBytes is the ceiling this repo puts on a free-text tail: a subject, a quoted
// sentence, a finding's detail. Five hundred bytes is a long sentence and a short
// paragraph -- enough that a tail is never cut in practice, short enough that one
// pathological value cannot be the whole of a reader's context.
const TailBytes = 500

// Cap shortens a free-text tail to about n bytes and SAYS SO, which is the half that
// makes it safe.
//
// Escape and Field bound a value's LINES and never its LENGTH: they are documented as
// never shortening anything, and that is right for what they are -- a reason a person
// cannot read is not a record. But it leaves the other half of the one-line promise
// unmade. One line is not one bounded line, and a stored subject, a ledger row or an
// embedded git output can be a megabyte on a single line, which is a listing's whole
// budget spent on one entry that nobody chose to read.
//
// So the ceiling is here, separate, applied by the caller to the tails where a runaway
// value is possible, and it leaves a mark: `...+<dropped>B`. The mark is the point. An
// ellipsis alone could be the author's own; the byte count cannot, so a reader who meets
// a cut tail knows that they met a cut tail and knows what it would cost to see the rest.
// The mark holds no whitespace and no "=", so a capped value is still one token through
// Field.
//
// Cap runs BEFORE Escape, never after: it cuts on a rune boundary, so what Escape then
// sees is well-formed wherever the input was, and an escape sequence can never be cut in
// half. Cap never returns nothing from something -- a ceiling too small to hold the mark
// and one rune is raised to hold them, because this package does not shorten a record
// out of existence.
func Cap(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// The mark's width depends on how much is dropped, and how much is dropped depends
	// on the mark's width. The knot is cut with the widest the mark can possibly be,
	// which costs at most a few bytes of the budget and never overruns it.
	widest := len(mark(len(s)))
	budget := n - widest
	if budget < 1 {
		budget = 1
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if cut == 0 {
		// The first rune alone is wider than the budget. Keep it whole: a cut inside a
		// rune is a byte Escape would render \xNN, which reads as corruption rather than
		// as a ceiling.
		_, size := utf8.DecodeRuneInString(s)
		cut = size
	}
	return s[:cut] + mark(len(s)-cut)
}

// mark renders the cut marker. It is one function so that Cap's width arithmetic and the
// bytes it finally writes cannot disagree.
func mark(dropped int) string { return fmt.Sprintf("...+%dB", dropped) }
