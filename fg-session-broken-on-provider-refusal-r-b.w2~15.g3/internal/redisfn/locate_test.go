package redisfn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The note a failed call writes after an error's text maps every line the
// text names back to its file and line: the texts are the store's own (Redis
// 8.10.2), with the line numbers of the fixed library.
func TestTheNoteNamesTheFileAndTheLineTheErrorNames(t *testing.T) {
	t.Parallel()
	b, err := two().build()
	require.NoError(t, err, err)
	for _, c := range []struct{ name, in, want string }{
		{"an error at run time",
			"ERR user_function:10: attempt to index local 't' (a nil value) script: fb, on @user_function:10.",
			"[user_function:10 = lua/b.lua:1]"},
		{"an error that names two lines",
			"ERR user_function:5: boom script: fa, on @user_function:11.",
			"[user_function:5 = lua/a.lua:1; user_function:11 = lua/b.lua:2]"},
		{"an error at compile time",
			"ERR Error compiling function: user_function:6: unexpected symbol near 'end'",
			"[user_function:6 = the loader's lines around lua/a.lua]"},
		{"an error in the prelude",
			"ERR user_function:2: boom",
			"[user_function:2 = prelude:1]"},
		{"a line of another version",
			"ERR user_function:400: boom",
			"[user_function:400 = no line of this library, which has 13]"},
		{"a line that is no number a library has",
			"ERR user_function:99999999999999999999: boom",
			"[user_function:99999999999999999999 = no line of this library, which has 13]"},
		{"line 0",
			"ERR user_function:0: boom",
			"[user_function:0 = no line of this library, which has 13]"},
		{"a text that names no line",
			"ERR value is not an integer or out of range",
			""},
	} {
		if got := b.explain(c.in); got != c.want {
			assert.Equal(t, c.want, got, "%s: the note reads\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

func TestOriginReadsAsOneLine(t *testing.T) {
	t.Parallel()
	for want, origin := range map[string]Origin{
		"lua/a.lua:12":                         {File: "lua/a.lua", Line: 12},
		"prelude:3":                            {Line: 3, Prelude: true},
		"the library's first line":             {},
		"the loader's lines around lua/a.lua":  {File: "lua/a.lua"},
		`lua/a\x0ab.lua:1`:                     {File: "lua/a\nb.lua", Line: 1},
		`the loader's lines around a\x0db.lua`: {File: "a\rb.lua"},
	} {
		if got := origin.String(); got != want {
			assert.Equal(t, want, got, "%+v reads %q, want %q", origin, got, want)
		}
	}
}
