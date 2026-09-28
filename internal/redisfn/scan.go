package redisfn

import (
	"fmt"
	"strings"
)

// scan.go reads one Lua file as Lua 5.1's own lexer does (llex.c, the Lua
// that Redis embeds), as far as the loader needs it: where comments and
// strings end, which reserved words open and close blocks, and which function
// names the file registers. It is a lexer and a count of blocks, not a parser.
// What it says about a file that is valid Lua is what Lua says; a file that is
// not valid Lua is refused by the store when the library is loaded, whatever
// is said here. The loader refuses a carriage return before it scans, so the
// only line break here is "\n".

// registration is one function name a file registers, where it is written.
type registration struct {
	name string // the name as the file spells it
	file string
	line int
}

type tokenKind int

const (
	tokenName   tokenKind = iota + 1 // an identifier or a reserved word
	tokenString                      // a string; text is its value, escapes decoded
	tokenNumber                      // a numeral, as Lua reads one: malformed ones too
	tokenSymbol                      // an operator or a delimiter
)

type token struct {
	kind tokenKind
	text string
	line int
}

// fault is why a file cannot be one block of a library: the line and the reason.
type fault struct {
	line int
	why  string
}

// tokens reads the whole file. Its only faults are the two that let a file
// reach outside itself: a string that does not end on its line, and a long
// string or long comment that never closes.
func tokens(src string) ([]token, *fault) {
	var out []token
	line := 1
	for pos := 0; pos < len(src); {
		c := src[pos]
		switch {
		case c == '\n':
			line++
			pos++
		case c == ' ' || c == '\t' || c == '\f' || c == '\v':
			pos++
		case strings.HasPrefix(src[pos:], "--"):
			pos += 2
			if level, ok := longOpen(src, pos); ok {
				end, lines, closed := longClose(src, pos+level+2, level)
				if !closed {
					return nil, &fault{line, "the long comment opened here never closes, so it would run on into the next file"}
				}
				pos, line = end, line+lines
				continue
			}
			for pos < len(src) && src[pos] != '\n' {
				pos++
			}
		case c == '"' || c == '\'':
			value, end, lines, ok := quoted(src, pos)
			if !ok {
				return nil, &fault{line, "the string opened here does not end on its line"}
			}
			out = append(out, token{tokenString, value, line})
			pos, line = end, line+lines
		case c == '[':
			level, ok := longOpen(src, pos)
			if !ok {
				out = append(out, token{tokenSymbol, "[", line})
				pos++
				continue
			}
			start := pos + level + 2
			end, lines, closed := longClose(src, start, level)
			if !closed {
				return nil, &fault{line, "the long string opened here never closes, so it would run on into the next file"}
			}
			// Lua drops a line break that follows the opening bracket.
			value := strings.TrimPrefix(src[start:end-level-2], "\n")
			out = append(out, token{tokenString, value, line})
			pos, line = end, line+lines
		case isDigit(c) || (c == '.' && pos+1 < len(src) && isDigit(src[pos+1])):
			end := numeral(src, pos)
			out = append(out, token{tokenNumber, src[pos:end], line})
			pos = end
		case isLetter(c):
			end := pos + 1
			for end < len(src) && (isLetter(src[end]) || isDigit(src[end])) {
				end++
			}
			out = append(out, token{tokenName, src[pos:end], line})
			pos = end
		default:
			end := pos + 1
			for _, symbol := range []string{"...", "..", "==", "~=", "<=", ">="} {
				if strings.HasPrefix(src[pos:], symbol) {
					end = pos + len(symbol)
					break
				}
			}
			out = append(out, token{tokenSymbol, src[pos:end], line})
			pos = end
		}
	}
	return out, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isLetter(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// longOpen reports whether a long bracket opens at pos ("[", any number of
// "=", "["), and its level, the number of "=".
func longOpen(src string, pos int) (int, bool) {
	if pos >= len(src) || src[pos] != '[' {
		return 0, false
	}
	level := 0
	for pos+1+level < len(src) && src[pos+1+level] == '=' {
		level++
	}
	if pos+1+level < len(src) && src[pos+1+level] == '[' {
		return level, true
	}
	return 0, false
}

// longClose finds the closing bracket of that level at or after pos: the
// position after it and the line breaks passed on the way.
func longClose(src string, pos, level int) (end, lines int, closed bool) {
	closer := "]" + strings.Repeat("=", level) + "]"
	at := strings.Index(src[pos:], closer)
	if at < 0 {
		return 0, 0, false
	}
	end = pos + at + len(closer)
	return end, strings.Count(src[pos:end], "\n"), true
}

// quoted reads the string that opens at pos: its value, the position after
// its closing quote and the line breaks inside it (a backslash before a line
// break carries the string onto the next line).
func quoted(src string, pos int) (value string, end, lines int, ok bool) {
	quote := src[pos]
	var b strings.Builder
	for i := pos + 1; i < len(src); i++ {
		c := src[i]
		switch {
		case c == quote:
			return b.String(), i + 1, lines, true
		case c == '\n':
			return "", 0, 0, false
		case c != '\\':
			b.WriteByte(c)
		case i+1 == len(src):
			return "", 0, 0, false
		case isDigit(src[i+1]):
			code, digits := 0, 0
			for digits < 3 && i+1 < len(src) && isDigit(src[i+1]) {
				i++
				digits++
				code = code*10 + int(src[i]-'0')
			}
			b.WriteByte(byte(code)) // a code over 255 is Lua's error, and then the store's
		default:
			i++
			c = src[i]
			if c == '\n' {
				lines++
			}
			if at := strings.IndexByte("abfnrtv", c); at >= 0 {
				c = "\a\b\f\n\r\t\v"[at]
			}
			b.WriteByte(c)
		}
	}
	return "", 0, 0, false
}

// numeral is the end of the numeral at pos, read as Lua reads one: digits and
// dots, an exponent with its sign, and then every letter and digit that
// follows. "1end" is one malformed numeral to Lua, not a 1 and an end.
func numeral(src string, pos int) int {
	end := pos
	for end < len(src) && (isDigit(src[end]) || src[end] == '.') {
		end++
	}
	if end < len(src) && (src[end] == 'E' || src[end] == 'e') {
		end++
		if end < len(src) && (src[end] == '+' || src[end] == '-') {
			end++
		}
	}
	for end < len(src) && (isLetter(src[end]) || isDigit(src[end])) {
		end++
	}
	return end
}

// opens and closes are the reserved words that begin and end a block. while
// and for open theirs with do; then, elseif and else open none.
func opens(word string) bool {
	return word == "do" || word == "if" || word == "function" || word == "repeat"
}

func closes(word string) bool { return word == "end" || word == "until" }

// scanned is what scan reads of one file.
type scanned struct {
	regs []registration // the function names it registers, in its order
	// peak is the most local variables the file holds at once in the
	// function it is part of (the library's main function), and left how
	// many of them are still in scope at its end. A local inside a function
	// the file defines belongs to that function and is not counted.
	peak, left int
}

// scan reads one file and returns what it registers and how many locals it
// holds, or the reason the file cannot be one block of a library:
//
//   - a string or a long comment that does not end inside the file would
//     swallow the loader's own lines after it;
//   - a block word that closes nothing would close the block the loader wraps
//     the file in, and the file's locals would stay in scope after it;
//   - a block left open would take the next file inside it;
//   - a return outside every function ends the library's load at that line,
//     so every later file registers nothing and the store says nothing.
//
// The locals are counted as Lua 5.1's parser counts them against its limit
// of 200 active local variables to a function (lparser.c, new_localvar):
// each name of a local statement, one for a local function, and the hidden
// variables of a for (three and the loop's own names; a numeric for has one),
// each in scope from its declaration to the end of its block. The locals of
// an if's branch leave scope at the next elseif or else.
func scan(file, src string) (scanned, *fault) {
	toks, bad := tokens(src)
	if bad != nil {
		return scanned{}, bad
	}
	var out scanned
	type frame struct {
		word   token
		locals int // the main function's locals declared in this block
	}
	var open []frame // the blocks open at this token
	functions := 0   // how many of them are functions
	active := 0      // the main function's locals in scope, of this file
	outside := 0     // how many of them were declared outside every block
	forLocals := 0   // the locals of a for whose do has not opened yet
	declare := func(n int) {
		if n == 0 {
			return
		}
		if len(open) == 0 {
			outside += n
		} else {
			open[len(open)-1].locals += n
		}
		active += n
		out.peak = max(out.peak, active)
	}
	for i, tok := range toks {
		if tok.kind != tokenName {
			continue
		}
		switch {
		case opens(tok.text):
			open = append(open, frame{word: tok})
			if tok.text == "function" {
				functions++
			}
			if tok.text == "do" && forLocals > 0 {
				declare(forLocals)
				forLocals = 0
			}
		case closes(tok.text):
			if len(open) == 0 {
				return scanned{}, &fault{tok.line, fmt.Sprintf("this %s closes no block of the file, so it would close the block the loader wraps the file in", tok.text)}
			}
			top := open[len(open)-1]
			open = open[:len(open)-1]
			if (tok.text == "until") != (top.word.text == "repeat") {
				return scanned{}, &fault{tok.line, fmt.Sprintf("this %s closes the %s of line %d", tok.text, top.word.text, top.word.line)}
			}
			if top.word.text == "function" {
				functions--
			}
			active -= top.locals
		case (tok.text == "elseif" || tok.text == "else") && len(open) > 0 && open[len(open)-1].word.text == "if":
			active -= open[len(open)-1].locals
			open[len(open)-1].locals = 0
		case tok.text == "local" && functions == 0:
			declare(localNames(toks[i+1:]))
		case tok.text == "for" && functions == 0:
			forLocals = forNames(toks[i+1:])
		case tok.text == "return" && functions == 0:
			return scanned{}, &fault{tok.line, "this return is outside every function, so the library's load would end here and every later file would register nothing; return only inside a function"}
		case tok.text == "redis" && !(i > 0 && toks[i-1].kind == tokenSymbol && (toks[i-1].text == "." || toks[i-1].text == ":")):
			if name, at, ok := registered(toks[i:]); ok {
				out.regs = append(out.regs, registration{name, file, at})
			}
		}
	}
	if len(open) > 0 {
		top := open[len(open)-1]
		return scanned{}, &fault{top.word.line, fmt.Sprintf("the %s opened here never closes, so it would take the next file inside it", top.word.text)}
	}
	out.left = outside
	return out, nil
}

// localNames is how many locals the local statement after toks declares: one
// for "local function f", and one for each name of "local a, b, c".
func localNames(toks []token) int {
	if len(toks) > 0 && toks[0].kind == tokenName && toks[0].text == "function" {
		return 1
	}
	return names(toks)
}

// forNames is how many locals the for after toks declares: a numeric for
// ("for i = 1, n do") has four, the three Lua hides and i, and a generic for
// ("for k, v in pairs(t) do") has three and its names.
func forNames(toks []token) int {
	if len(toks) > 1 && toks[1].kind == tokenSymbol && toks[1].text == "=" {
		return 4
	}
	return 3 + names(toks)
}

// names counts the names of a list "a, b, c" at the head of toks.
func names(toks []token) int {
	n := 0
	for i := 0; i < len(toks) && toks[i].kind == tokenName; i += 2 {
		n++
		if i+1 >= len(toks) || toks[i+1].kind != tokenSymbol || toks[i+1].text != "," {
			break
		}
	}
	return n
}

// registered reads a call of redis.register_function at the head of toks and
// returns the name it registers, when the call spells the name as a string:
//
//	redis.register_function('name', callback)
//	redis.register_function{function_name = 'name', callback = ...}
//
// with or without the parentheses around the table. A name that is computed,
// or a call made through another name for the function, is not read here; the
// store still refuses it when another library holds the name.
func registered(toks []token) (name string, line int, ok bool) {
	is := func(i int, kind tokenKind, text string) bool {
		return i < len(toks) && toks[i].kind == kind && toks[i].text == text
	}
	if !is(1, tokenSymbol, ".") || !is(2, tokenName, "register_function") {
		return "", 0, false
	}
	at := 3
	if is(at, tokenSymbol, "(") {
		at++
		if at < len(toks) && toks[at].kind == tokenString && is(at+1, tokenSymbol, ",") {
			return toks[at].text, toks[at].line, true
		}
	}
	braces, blocks := 0, 0
	for i := at; i < len(toks); i++ {
		tok := toks[i]
		switch {
		case tok.kind == tokenSymbol && tok.text == "{":
			braces++
		case braces == 0:
			return "", 0, false // the call's argument is not a table
		case tok.kind == tokenSymbol && tok.text == "}":
			braces--
			if braces == 0 {
				return "", 0, false // the table names no function_name as a string
			}
		case tok.kind == tokenName && opens(tok.text):
			blocks++
		case tok.kind == tokenName && closes(tok.text):
			blocks--
		case braces == 1 && blocks == 0 && is(i, tokenName, "function_name") && is(i+1, tokenSymbol, "="):
			if i+2 < len(toks) && toks[i+2].kind == tokenString &&
				(is(i+3, tokenSymbol, ",") || is(i+3, tokenSymbol, ";") || is(i+3, tokenSymbol, "}")) {
				return toks[i+2].text, toks[i+2].line, true
			}
			return "", 0, false // the name is computed
		}
	}
	return "", 0, false
}
