package oneline

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"
)

// u spells a code point as a string, and esc spells its escaped form, without putting
// either the character or a \u sequence into this file: an invisible bidi control or line
// separator in a source file is exactly the thing a reader could not see, and a \u
// sequence is one editor away from becoming the character.
func u(cp rune) string   { return string(cp) }
func esc(cp rune) string { return fmt.Sprintf("%su%04x", bs, cp) }

const bs = "\x5c" // one backslash

// TestEscapeEveryControlCharacter is the table the one-line guarantee is pinned by. It
// moved here from internal/fuse with its rows intact when the escape became shared, and
// the fuse package keeps its own copy of the table against its delegating OneLine, so a
// drift between the two would fail there.
func TestEscapeEveryControlCharacter(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii is untouched", "lockdown at 3am", "lockdown at 3am"},
		{"newline", "real\nFUSE OK lockdown=clear", `real\x0aFUSE OK lockdown=clear`},
		{"carriage return", "a\rb", `a\x0db`},
		{"tab", "a\tb", `a\x09b`},
		{"nul", "a\x00b", `a\x00b`},
		{"escape", "\x1b[2J", `\x1b[2J`},
		{"delete", "a\x7fb", `a\x7fb`},
		{"C1 next-line", "a" + u(0x85) + "b", "a" + esc(0x85) + "b"},
		{"C1 control string introducer", "a" + u(0x9b) + "b", "a" + esc(0x9b) + "b"},
		{"line separator, which str.splitlines and UAX-14 both break on", "a" + u(0x2028) + "b", "a" + esc(0x2028) + "b"},
		{"paragraph separator, the same hole", "a" + u(0x2029) + "b", "a" + esc(0x2029) + "b"},
		{"left-to-right embedding", "a" + u(0x202a) + "b", "a" + esc(0x202a) + "b"},
		{"right-to-left embedding", "a" + u(0x202b) + "b", "a" + esc(0x202b) + "b"},
		{"pop directional formatting", "a" + u(0x202c) + "b", "a" + esc(0x202c) + "b"},
		{"left-to-right override", "a" + u(0x202d) + "b", "a" + esc(0x202d) + "b"},
		{"right-to-left override, the classic reorder", "a" + u(0x202e) + "b", "a" + esc(0x202e) + "b"},
		{"left-to-right isolate", "a" + u(0x2066) + "b", "a" + esc(0x2066) + "b"},
		{"right-to-left isolate", "a" + u(0x2067) + "b", "a" + esc(0x2067) + "b"},
		{"first strong isolate", "a" + u(0x2068) + "b", "a" + esc(0x2068) + "b"},
		{"pop directional isolate", "a" + u(0x2069) + "b", "a" + esc(0x2069) + "b"},
		{"a zero-width joiner is not a bidi control and passes through", "a" + u(0x200d) + "b", "a" + u(0x200d) + "b"},
		{"a left-to-right mark is not an embedding and passes through", "a" + u(0x200e) + "b", "a" + u(0x200e) + "b"},
		{"a byte that is not valid UTF-8 at all", "a\xffb", `a\xffb`},
		{"printable non-ascii passes through", "café — 日本語", "café — 日本語"},
		{"a space and an equals sign are free text here", "x lockdown=clear", "x lockdown=clear"},
		{"empty stays empty", "", ""},
		{"only control characters, still not shortened to nothing", "\n\n", `\x0a\x0a`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Escape(tc.in)
			if got != tc.want {
				t.Errorf("Escape(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsFunc(got, unicode.IsControl) {
				t.Errorf("Escape(%q) = %q still holds a control character", tc.in, got)
			}
			if strings.ContainsFunc(got, reordersALine) || strings.ContainsFunc(got, breaksALine) {
				t.Errorf("Escape(%q) = %q still holds a separator or a bidi control", tc.in, got)
			}
			if tc.in != "" && got == "" {
				t.Errorf("Escape(%q) emptied the text; a reason must never vanish", tc.in)
			}
			if again := Escape(tc.in); again != got {
				t.Errorf("Escape(%q) is not deterministic: %q then %q", tc.in, got, again)
			}
		})
	}
}

// TestEveryBidiControlIsEscapedAndNoOtherFormatCharacterIs pins the boundary of the set
// by range rather than by example: the nine code points the issue names, and none of
// their neighbors, so a future widening or narrowing is a decision here.
func TestEveryBidiControlIsEscapedAndNoOtherFormatCharacterIs(t *testing.T) {
	escaped := map[rune]bool{}
	for r := rune(0x202a); r <= 0x202e; r++ {
		escaped[r] = true
	}
	for r := rune(0x2066); r <= 0x2069; r++ {
		escaped[r] = true
	}
	for r := rune(0x2000); r <= 0x20ff; r++ {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 {
			continue
		}
		got := Escape(u(r))
		if escaped[r] && got == u(r) {
			t.Errorf("U+%04X is a bidi control and passed through", r)
		}
		if !escaped[r] && got != u(r) {
			t.Errorf("U+%04X is not a bidi control and was escaped to %q", r, got)
		}
	}
}

// TestFieldIsOneTokenHoldingNoEquals is the specimen from the issue: a stored quarantine
// key of `x lockdown=clear quarantines=0` printed raw inside `quarantine=` let a grep for
// lockdown=clear match a line about a blown lockdown. A field value is one token now.
func TestFieldIsOneTokenHoldingNoEquals(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"a plain name is untouched", "discord", "discord"},
		{"a timestamp is untouched", "2026-08-03T00:00:00Z", "2026-08-03T00:00:00Z"},
		{"a path with slashes and dots is untouched", "corpus/anchors.md", "corpus/anchors.md"},
		{"the specimen", "x lockdown=clear quarantines=0", `x\x20lockdown\x3dclear\x20quarantines\x3d0`},
		{"a space", "dis cord", `dis\x20cord`},
		{"a tab is a control and a space at once", "dis\tcord", `dis\x09cord`},
		{"a newline", "dis\ncord", `dis\x0acord`},
		{"a non-breaking space splits for a Unicode-aware scanner", "dis" + u(0xa0) + "cord", "dis" + esc(0xa0) + "cord"},
		{"an ideographic space", "a" + u(0x3000) + "b", "a" + esc(0x3000) + "b"},
		{"a line separator", "a" + u(0x2028) + "b", "a" + esc(0x2028) + "b"},
		{"a bidi override", "a" + u(0x202e) + "b", "a" + esc(0x202e) + "b"},
		{"an equals sign alone", "a=b", `a\x3db`},
		{"printable non-ascii passes through", "café", "café"},
		{"a byte that is not valid UTF-8", "a\xffb", `a\xffb`},
		{"empty stays empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Field(tc.in)
			if got != tc.want {
				t.Errorf("Field(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsFunc(got, unicode.IsSpace) || strings.ContainsFunc(got, unicode.IsControl) {
				t.Errorf("Field(%q) = %q is not one token", tc.in, got)
			}
			if strings.ContainsRune(got, '=') {
				t.Errorf("Field(%q) = %q still holds an equals sign, so a key=value search could match inside it", tc.in, got)
			}
			if tc.in != "" && got == "" {
				t.Errorf("Field(%q) emptied the text", tc.in)
			}
			if again := Field(tc.in); again != got {
				t.Errorf("Field(%q) is not deterministic: %q then %q", tc.in, got, again)
			}
		})
	}
}

// TestFieldAgreesWithEscapeOnEverythingEscapeTouches: Field is Escape and then more, never
// Escape and then less. Every code point Escape rewrites, Field rewrites to the same
// spelling, so an operator reads one escape form across a whole line.
func TestFieldAgreesWithEscapeOnEverythingEscapeTouches(t *testing.T) {
	for r := rune(0); r <= 0x2100; r++ {
		if r == 0xfffd {
			continue
		}
		e, f := Escape(u(r)), Field(u(r))
		if e != u(r) && f != e {
			t.Errorf("U+%04X: Escape = %q but Field = %q", r, e, f)
		}
	}
}

func TestErrRendersTheTextAndSpellsNilLikeFmt(t *testing.T) {
	if got := Err(nil); got != "<nil>" {
		t.Errorf("Err(nil) = %q, want <nil>", got)
	}
	err := errors.New("open a\nFUSE OK lockdown=clear: no such file")
	if got, want := Err(err), `open a\x0aFUSE OK lockdown=clear: no such file`; got != want {
		t.Errorf("Err = %q, want %q", got, want)
	}
}
