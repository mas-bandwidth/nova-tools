package main

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The hidden prompt's line is edited as a terminal edits one: it ends at CR, LF or the end-of-file character, erase
// takes back one character, kill the line, and what follows the line's end is left unread.
func TestTheHiddenLineIsEditedAsATerminalWould(t *testing.T) {
	t.Parallel()
	const erase, kill, eof = "\x7f", "\x15", "\x04"
	for in, want := range map[string][2]string{ // the line, and what is left unread
		"plain\n":                         {"plain", ""},
		"enter\rnext":                     {"enter", "next"},
		"crlf\r\n":                        {"crlf", "\n"},
		"no line end":                     {"no line end", ""},
		"ended" + eof + "more":            {"ended", "more"},
		"abx" + erase + "c\n":             {"abc", ""},
		erase + erase + "a\n":             {"a", ""},
		"aé" + erase + "b\n":              {"ab", ""},
		"wrong" + kill + "right\n":        {"right", ""},
		"a" + erase + erase + kill + "\n": {"", ""},
	} {
		r := strings.NewReader(in)
		got, err := readLine(r, erase[0], kill[0], eof[0])
		require.NoError(t, err, "%q", in)
		assert.Equal(t, want[0], string(got), "%q", in)
		rest, _ := io.ReadAll(r) // ignored: a strings reader
		assert.Equal(t, want[1], string(rest), "%q", in)
	}
	for _, in := range []string{"", eof, eof + "late\n"} {
		_, err := readLine(strings.NewReader(in), erase[0], kill[0], eof[0])
		assert.Equal(t, io.EOF, err, "%q: the end of input on an empty line", in)
	}
}

// A line over the maximum is read to its end and comes back one byte over, which set refuses: it is never cut to
// fit, erasing does not bring it back under, and only kill starts it again.
func TestTheHiddenLineOverTheMaximumIsNeverCut(t *testing.T) {
	t.Parallel()
	const erase, kill, eof = "\x7f", "\x15", "\x04"
	for name, c := range map[string]struct {
		in   string
		want int
	}{
		"at the maximum":   {strings.Repeat("v", maxValue) + "\n", maxValue},
		"one over":         {strings.Repeat("v", maxValue+1) + "\n", maxValue + 1},
		"far over":         {strings.Repeat("v", 3*maxValue) + "\n", maxValue + 1},
		"over then erased": {strings.Repeat("v", maxValue+2) + strings.Repeat(erase, 9) + "\n", maxValue + 1},
		"over then killed": {strings.Repeat("v", maxValue+2) + kill + "short\n", len("short")},
	} {
		r := strings.NewReader(c.in + "left for the shell")
		got, err := readLine(r, erase[0], kill[0], eof[0])
		require.NoError(t, err, name)
		assert.Len(t, got, c.want, name)
		rest, _ := io.ReadAll(r) // ignored: a strings reader
		assert.Equal(t, "left for the shell", string(rest), "%s: the whole line was read, and no more", name)
	}
}
