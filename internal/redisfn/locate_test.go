package redisfn

import (
	"errors"
	"strings"
	"testing"
)

// Every line of the fixed library (twoSource) and where it came from.
func TestLocateMapsEveryLineOfTheSourceToItsFileAndLine(t *testing.T) {
	t.Parallel()
	lib := two()
	for line, want := range map[int]Origin{
		1:  {},                           // #!lua name=lib_one
		2:  {Line: 1, Prelude: true},     // local NS = {}
		3:  {File: "lua/a.lua"},          // the header of a
		4:  {File: "lua/a.lua"},          // do
		5:  {File: "lua/a.lua", Line: 1}, // a's one line
		6:  {File: "lua/a.lua"},          // end
		7:  {File: "lua/b.lua"},          // the header of b
		8:  {File: "lua/b.lua"},          // do
		9:  {File: "lua/b.lua", Line: 1}, // local b = 1
		10: {File: "lua/b.lua", Line: 2}, // b's register
		11: {File: "lua/b.lua"},          // end
	} {
		got, err := lib.Locate(line)
		if err != nil || got != want {
			t.Errorf("Locate(%d) = %+v %v, want %+v", line, got, err, want)
		}
	}
	if lines := strings.Count(twoSource, "\n"); lines != 11 {
		t.Fatalf("the fixed library has %d lines, and this test maps 11", lines)
	}
}

func TestLocateRefusesALineOutsideTheSource(t *testing.T) {
	t.Parallel()
	for _, line := range []int{-1, 0, 12, 1 << 40} {
		got, err := two().Locate(line)
		if err == nil || got != (Origin{}) {
			t.Errorf("Locate(%d) = %+v %v, want an error", line, got, err)
			continue
		}
		if errors.Is(err, ErrRefused) {
			t.Errorf("Locate(%d): the error says the library is refused, and it is the line that is wrong: %v", line, err)
		}
		if !strings.Contains(err.Error(), "library lib_one has 11 lines") {
			t.Errorf("Locate(%d): the error does not say how many lines there are: %v", line, err)
		}
	}
}

// A library with no prelude and a prelude of several lines: the files move,
// and the mapping with them.
func TestLocateCountsThePreludesLines(t *testing.T) {
	t.Parallel()
	lib := two()
	lib.Prelude = ""
	if got, err := lib.Locate(4); err != nil || got != (Origin{File: "lua/a.lua", Line: 1}) {
		t.Errorf("with no prelude, Locate(4) = %+v %v, want line 1 of a", got, err)
	}
	if got, err := lib.Locate(2); err != nil || got != (Origin{File: "lua/a.lua"}) {
		t.Errorf("with no prelude, Locate(2) = %+v %v, want the header of a", got, err)
	}
	lib.Prelude = "local NS = {}\n\nlocal shared = 1\n"
	if got, err := lib.Locate(4); err != nil || got != (Origin{Line: 3, Prelude: true}) {
		t.Errorf("with a prelude of three lines, Locate(4) = %+v %v, want its line 3", got, err)
	}
	if got, err := lib.Locate(7); err != nil || got != (Origin{File: "lua/a.lua", Line: 1}) {
		t.Errorf("with a prelude of three lines, Locate(7) = %+v %v, want line 1 of a", got, err)
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
			t.Errorf("%+v reads %q, want %q", origin, got, want)
		}
	}
}

type text string

func (e text) Error() string { return string(e) }

// The texts are the store's own (Redis 8.10.2), with the line numbers of the
// fixed library.
func TestExplainWritesTheOriginOfEveryLineTheErrorNames(t *testing.T) {
	t.Parallel()
	lib := two()
	for _, c := range []struct{ name, in, want string }{
		{"an error at run time",
			"ERR user_function:9: attempt to index local 't' (a nil value) script: fb, on @user_function:9.",
			"ERR user_function:9: attempt to index local 't' (a nil value) script: fb, on @user_function:9. [user_function:9 = lua/b.lua:1]"},
		{"an error that names two lines",
			"ERR user_function:5: boom script: fa, on @user_function:10.",
			"ERR user_function:5: boom script: fa, on @user_function:10. [user_function:5 = lua/a.lua:1; user_function:10 = lua/b.lua:2]"},
		{"an error at compile time",
			"ERR Error compiling function: user_function:6: unexpected symbol near 'end'",
			"ERR Error compiling function: user_function:6: unexpected symbol near 'end' [user_function:6 = the loader's lines around lua/a.lua]"},
		{"an error in the prelude",
			"ERR user_function:2: boom",
			"ERR user_function:2: boom [user_function:2 = prelude:1]"},
		{"a line of another version",
			"ERR user_function:400: boom",
			"ERR user_function:400: boom [user_function:400 = no line of this library, which has 11]"},
		{"a line that is no number a library has",
			"ERR user_function:99999999999999999999: boom",
			"ERR user_function:99999999999999999999: boom [user_function:99999999999999999999 = no line of this library, which has 11]"},
		{"line 0",
			"ERR user_function:0: boom",
			"ERR user_function:0: boom [user_function:0 = no line of this library, which has 11]"},
	} {
		in := text(c.in)
		got := lib.Explain(in)
		if got.Error() != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		var cause text
		if !errors.As(got, &cause) || cause != in {
			t.Errorf("%s: the error does not wrap the one it explains", c.name)
		}
	}
}

func TestExplainLeavesAnErrorItHasNothingToSayAboutAsItIs(t *testing.T) {
	t.Parallel()
	if got := two().Explain(nil); got != nil {
		t.Errorf("Explain(nil) = %v", got)
	}
	plain := text("ERR value is not an integer or out of range")
	if got := two().Explain(plain); got != error(plain) {
		t.Errorf("an error that names no line came back as %v", got)
	}
	named := text("ERR user_function:9: boom")
	refused := two()
	refused.Name = "not a name"
	if got := refused.Explain(named); got != error(named) {
		t.Errorf("a refused library explained an error: %v", got)
	}
}
