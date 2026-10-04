package redisfn

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// spell writes tokens as "kind:text@line", so a test reads what the lexer
// read.
func spell(toks []token) string {
	kinds := map[tokenKind]string{tokenName: "name", tokenString: "string", tokenNumber: "number", tokenSymbol: "symbol"}
	var out []string
	for _, tok := range toks {
		out = append(out, fmt.Sprintf("%s:%q@%d", kinds[tok.kind], tok.text, tok.line))
	}
	return strings.Join(out, " ")
}

// The lexer reads what Lua 5.1's reads (llex.c): each case is a text and the
// tokens Lua makes of it.
func TestTokensAreLuasTokens(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src, want string }{
		{"nothing", "", ""},
		{"white space", " \t\f\v\n ", ""},
		{"names", "a _b c1 end_ Do", `name:"a"@1 name:"_b"@1 name:"c1"@1 name:"end_"@1 name:"Do"@1`},
		{"lines", "a\n\nb\nc", `name:"a"@1 name:"b"@3 name:"c"@4`},
		{"a comment to the end of its line", "a -- b 'c\nd", `name:"a"@1 name:"d"@2`},
		{"a comment that ends the text", "a --", `name:"a"@1`},
		{"a long comment", "a --[[ b\n c ]] d", `name:"a"@1 name:"d"@2`},
		{"a long comment with a level", "a --[=[ b ]] c ]=] d", `name:"a"@1 name:"d"@1`},
		{"a comment that only starts like a long one", "a --[ b ]] c\nd --[=x\ne", `name:"a"@1 name:"d"@2 name:"e"@3`},
		{"a minus is not a comment", "a - b", `name:"a"@1 symbol:"-"@1 name:"b"@1`},
		{"strings", `'a' "b" '' ""`, `string:"a"@1 string:"b"@1 string:""@1 string:""@1`},
		{"the other quote inside a string", `'a"b' "c'd"`, `string:"a\"b"@1 string:"c'd"@1`},
		{"escapes", `'\a\b\f\n\r\t\v\\\"\'\q'`, `string:"\a\b\f\n\r\t\v\\\"'q"@1`},
		{"decimal escapes", `'\65\066\0067\1'`, `string:"AB\x067\x01"@1`},
		{"a decimal escape that ends the string", `'\65'x`, `string:"A"@1 name:"x"@1`},
		{"a backslash before a line break", "'a\\\nb' c", `string:"a\nb"@1 name:"c"@2`},
		{"a long string", "[[a'b\"c]] d", `string:"a'b\"c"@1 name:"d"@1`},
		{"a long string drops its first line break", "[[\na\n]] b", `string:"a\n"@1 name:"b"@3`},
		{"a long string with a level", "[==[a]]b]=]c]==] d", `string:"a]]b]=]c"@1 name:"d"@1`},
		{"a long string keeps a backslash", `[[a\n]]`, `string:"a\\n"@1`},
		{"brackets that open no string", "a[1] [=x [", `name:"a"@1 symbol:"["@1 number:"1"@1 symbol:"]"@1 symbol:"["@1 symbol:"="@1 name:"x"@1 symbol:"["@1`},
		{"numerals", "1 12.5 .5 1e5 1E+5 1e-5 0x1F 1..2", `number:"1"@1 number:"12.5"@1 number:".5"@1 number:"1e5"@1 number:"1E+5"@1 number:"1e-5"@1 number:"0x1F"@1 number:"1..2"@1`},
		{"a numeral takes the letters after it", "1end 2_x", `number:"1end"@1 number:"2_x"@1`},
		{"a numeral that ends the text", "a 1e", `name:"a"@1 number:"1e"@1`},
		{"an exponent sign that ends the text", "1e+", `number:"1e+"@1`},
		{"dots", "a.b a..b ... .", `name:"a"@1 symbol:"."@1 name:"b"@1 name:"a"@1 symbol:".."@1 name:"b"@1 symbol:"..."@1 symbol:"."@1`},
		{"signs of two characters", "== ~= <= >= = < > ~", `symbol:"=="@1 symbol:"~="@1 symbol:"<="@1 symbol:">="@1 symbol:"="@1 symbol:"<"@1 symbol:">"@1 symbol:"~"@1`},
		{"signs of one character", "(){};:,+*/%^#", `symbol:"("@1 symbol:")"@1 symbol:"{"@1 symbol:"}"@1 symbol:";"@1 symbol:":"@1 symbol:","@1 symbol:"+"@1 symbol:"*"@1 symbol:"/"@1 symbol:"%"@1 symbol:"^"@1 symbol:"#"@1`},
		{"a byte outside ASCII", "a\xc3\xa9b", `name:"a"@1 symbol:"\xc3"@1 symbol:"\xa9"@1 name:"b"@1`},
		{"a call", "redis.register_function('f', g)", `name:"redis"@1 symbol:"."@1 name:"register_function"@1 symbol:"("@1 string:"f"@1 symbol:","@1 name:"g"@1 symbol:")"@1`},
	} {
		toks, bad := tokens(c.src)
		if bad != nil {
			assert.Nil(t, bad, "%s: %q is refused: line %d: %s", c.name, c.src, bad.line, bad.why)
			continue
		}
		if got := spell(toks); got != c.want {
			assert.Equal(t, c.want, got, "%s: %q\n got %s\nwant %s", c.name, c.src, got, c.want)
		}
	}
}

// What cannot end inside the file is a fault at the line it opens on.
func TestTokensRefuseWhatDoesNotEndInsideTheFile(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, src string
		line      int
		why       string
	}{
		{"a string open at the line's end", "a\n'b\n'", 2, "the string opened here does not end on its line"},
		{"a string open at the text's end", "\"b", 1, "the string opened here does not end on its line"},
		{"a string that ends on a backslash", "a\n\n'b\\", 3, "the string opened here does not end on its line"},
		{"a string whose quote is escaped", "'b\\'", 1, "the string opened here does not end on its line"},
		{"a string that ends in a decimal escape", "'b\\12", 1, "the string opened here does not end on its line"},
		{"a long string", "a\n[[b\n\n", 2, "the long string opened here never closes"},
		{"a long string closed at another level", "[=[b]]", 1, "the long string opened here never closes"},
		{"a long comment", "a\n\n--[[b\n", 3, "the long comment opened here never closes"},
		{"a long comment closed at another level", "--[==[b]=]", 1, "the long comment opened here never closes"},
	} {
		toks, bad := tokens(c.src)
		if bad == nil {
			assert.NotNil(t, bad, "%s: %q is read as %s, want a fault", c.name, c.src, spell(toks))
			continue
		}
		if bad.line != c.line || !strings.Contains(bad.why, c.why) {
			assert.Failf(t, "", "%s: %q: line %d: %s; want line %d: %s", c.name, c.src, bad.line, bad.why, c.line, c.why)
		}
	}
}

// The locals a file holds at once in the main function, and how many are in
// scope at its end, counted as Lua 5.1 counts them.
func TestScanCountsTheLocalsAsLuaDoes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		src        string
		peak, left int
	}{
		{"none", "x = 1", 0, 0},
		{"one", "local a = 1", 1, 1},
		{"a list", "local a, b, c = 1, 2, 3", 3, 3},
		{"a list over lines", "local a,\n  b\n  , c", 3, 3},
		{"a local function, whose own locals are its own", "local function f(p) local x, y = 1, 2 end", 1, 1},
		{"a function held in a local", "local f = function() local a, b, c end", 1, 1},
		{"a global function", "function g() local a end", 0, 0},
		{"a block's leave at its end", "do local a, b end local c", 2, 1},
		{"a numeric for: three hidden and its own", "for i = 1, 3 do local x end", 5, 0},
		{"a generic for: three hidden and its names", "for k, v in pairs(t) do end", 5, 0},
		{"an if's branches, one at a time", "if x then local a, b elseif y then local c else local d, e, f end", 3, 0},
		{"an if inside a branch, whose else is its own", "if x then local a if y then local b else local c end local d, e else local f end", 3, 0},
		{"a repeat, in scope to its until", "repeat local a until a", 1, 0},
		{"a while", "while x do local a end local b", 1, 1},
		{"nested blocks", "local a local b do local c for i = 1, 2 do local d end end", 8, 2},
		{"a local in a string or a comment", "local s = 'local a, b' -- local c\n--[[ local d ]]", 1, 1},
		{"a for inside a function", "local function f() for i = 1, 2 do local x end end", 1, 1},
		{"a for whose limit calls a function", "for i = 1, f(function() local z end) do local x end", 5, 0},
		{"a for whose iterator is a function with a do of its own", "for k, v in (function() do end return function() return nil end end)() do local x end", 6, 0},
	} {
		read, bad := scan("a.lua", c.src)
		if bad != nil {
			assert.Nil(t, bad, "%s: %q is refused: line %d: %s", c.name, c.src, bad.line, bad.why)
			continue
		}
		if read.peak != c.peak || read.left != c.left {
			assert.Failf(t, "", "%s: %q holds %d at most and %d at its end, want %d and %d", c.name, c.src, read.peak, read.left, c.peak, c.left)
		}
	}
}

// forInFunction is a file of that many plain locals and then depth fors
// nested in one another, each iterating over a function whose own body holds
// a do (Stella, #4486): a for's loop locals are taken by its own do, never
// by a do inside a function of its expressions. Each for holds five: three
// hidden, k and v.
func forInFunction(plain, depth int) string {
	text := locals("p", plain)
	for n := 0; n < depth; n++ {
		text += fmt.Sprintf("for k%d, v%d in (function() do end return function() return nil end end)() do\n", n, n)
	}
	return text + strings.Repeat("end\n", depth) + fn("scope_probe")
}

func TestAForsLocalsAreNotTakenByADoInsideItsExpressions(t *testing.T) {
	t.Parallel()
	for depth, want := range map[int]int{5: 199, 6: 204} {
		text := forInFunction(174, depth)
		read, bad := scan("scope.lua", text)
		if bad != nil || read.peak != want {
			assert.Failf(t, "", "depth %d: the count is %d (%v), want %d", depth, read.peak, bad, want)
		}
		lib := Library{Name: "scope_probe", Files: tree(map[string]string{"scope.lua": text}), Glob: "*.lua"}
		if _, err := lib.Source(); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), fmt.Sprintf("would hold %d local variables", want)) {
			assert.Failf(t, "", "depth %d: Source = %v, want the refusal of %d locals over MaxLocals", depth, err, want)
		}
	}
}

// The names a file registers, with the line each is written on, in the
// file's order.
func TestScanReadsTheNamesAFileRegisters(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src, want string }{
		{"none", "local a = 1\n", ""},
		{"a call", "redis.register_function('fa', function() end)", "fa@1"},
		{"a call with double quotes", `redis.register_function("fa", f)`, "fa@1"},
		{"a call over several lines", "redis\n.\nregister_function\n(\n'fa'\n,\nf)", "fa@5"},
		{"a call with a comment inside", "redis.register_function( --[[ the name ]] 'fa' -- then\n, f)", "fa@1"},
		{"two calls", "redis.register_function('fa', f)\nredis.register_function('fb', g)\n", "fa@1 fb@2"},
		{"a table", "redis.register_function{function_name='fa', callback=f}", "fa@1"},
		{"a table in parentheses", "redis.register_function({callback=f, function_name='fa'})", "fa@1"},
		{"a table whose name comes last", "redis.register_function{\ncallback = function(keys)\n  return {keys, {1}}\nend,\nflags = {'no-writes'},\nfunction_name = 'fa'}", "fa@6"},
		{"a table with semicolons", "redis.register_function{function_name='fa'; callback=f}", "fa@1"},
		{"a call inside a function", "local function all()\n  redis.register_function('fa', f)\nend\nall()", "fa@2"},
		{"a name with upper case", "redis.register_function('Fa', f)", "Fa@1"},
		{"a call whose name is computed", "redis.register_function(name, f)\nredis.register_function('f' .. x, f)", ""},
		{"a table whose name is computed", "redis.register_function{function_name = name, callback = f}", ""},
		{"a table whose name is in a nested table", "redis.register_function{inner = {function_name = 'fa'}, callback = f}", ""},
		{"a table whose name is in its callback", "redis.register_function{callback = function() function_name = 'fa' end}", ""},
		{"a table that never closes", "redis.register_function{callback = f", ""},
		{"a call that ends the text", "redis.register_function(", ""},
		{"a name that ends the text", "redis.register_function('fa'", ""},
		{"a table key that ends the text", "redis.register_function{function_name =", ""},
		{"another function of redis", "redis.call('register_function', 'fa')", ""},
		{"redis alone", "local r = redis", ""},
		{"a field called redis", "x.redis.register_function('fa', f)\nx:redis.register_function('fb', f)", ""},
		{"a string, not a call", "redis.register_function 'fa'", ""},
	} {
		read, bad := scan("a.lua", c.src)
		if bad != nil {
			assert.Nil(t, bad, "%s: %q is refused: line %d: %s", c.name, c.src, bad.line, bad.why)
			continue
		}
		var got []string
		for _, reg := range read.regs {
			if reg.file != "a.lua" {
				assert.Equal(t, "a.lua", reg.file, "%s: %s is said to be registered in %q", c.name, reg.name, reg.file)
			}
			got = append(got, fmt.Sprintf("%s@%d", reg.name, reg.line))
		}
		if strings.Join(got, " ") != c.want {
			assert.Equal(t, c.want, strings.Join(got, " "), "%s: %q registers %v, want %s", c.name, c.src, got, c.want)
		}
	}
}
