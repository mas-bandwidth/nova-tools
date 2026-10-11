package redisacl

import (
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/redisfn"
)

// A function runs every command its Lua calls under the caller's ACL, so a
// role that may FCALL a function must also hold each command the function's
// file calls. The commands are read out of the library's own text: every
// quoted upper-case word of a file, single or double quoted, comments aside,
// that names a Redis command, the word after a container command (XINFO
// 'STREAM') making its subcommand. A file builds some of its commands as
// data ({'HSET', ...}, T.stage(d, 'DEL', ...)), so a call site alone would
// miss them; a word that names no command ('OK', 'REFUSED') is a reply, not
// a call. The reading is Lua's own lexing (luaTokens below), so a "--" in a
// quoted value is text of that value and no comment is ever a grant.
// TestEveryLuaCommandIsGrantedToItsCallers holds the rendering to it.

// redisCommands are the Redis commands a library file may name: the data
// commands of Redis 7 and the few server ones a function may call. A word
// a file passes to redis.call that is not here is refused by
// TestEveryDirectLuaCallIsAKnownCommand, so the list cannot hide a command.
var redisCommands = wordSet(`APPEND BITCOUNT BITFIELD BITFIELD_RO BITOP BITPOS COPY DECR DECRBY DEL DUMP EXISTS EXPIRE
EXPIREAT EXPIRETIME GET GETBIT GETDEL GETEX GETRANGE GETSET HDEL HEXISTS HGET HGETALL HINCRBY HINCRBYFLOAT HKEYS HLEN
HMGET HMSET HRANDFIELD HSCAN HSET HSETNX HSTRLEN HVALS INCR INCRBY INCRBYFLOAT KEYS LINDEX LINSERT LLEN LMOVE LMPOP
LPOP LPOS LPUSH LPUSHX LRANGE LREM LSET LTRIM MGET MSET MSETNX OBJECT PERSIST PEXPIRE PEXPIREAT PEXPIRETIME PFADD
PFCOUNT PFMERGE PSETEX PTTL RANDOMKEY RENAME RENAMENX RPOP RPOPLPUSH RPUSH RPUSHX SADD SCAN SCARD SDIFF SDIFFSTORE SET
SETBIT SETEX SETNX SETRANGE SINTER SINTERCARD SINTERSTORE SISMEMBER SMEMBERS SMISMEMBER SMOVE SORT SORT_RO SPOP
SRANDMEMBER SREM SSCAN STRLEN SUNION SUNIONSTORE TIME TOUCH TTL TYPE UNLINK XACK XADD XAUTOCLAIM XCLAIM XDEL XGROUP
XINFO XLEN XPENDING XRANGE XREAD XREADGROUP XREVRANGE XSETID XTRIM ZADD ZCARD ZCOUNT ZDIFF ZDIFFSTORE ZINCRBY ZINTER
ZINTERCARD ZINTERSTORE ZLEXCOUNT ZMPOP ZMSCORE ZPOPMAX ZPOPMIN ZRANDMEMBER ZRANGE ZRANGEBYLEX ZRANGEBYSCORE
ZRANGESTORE ZRANK ZREM ZREMRANGEBYLEX ZREMRANGEBYRANK ZREMRANGEBYSCORE ZREVRANGE ZREVRANGEBYLEX ZREVRANGEBYSCORE
ZREVRANK ZSCAN ZSCORE ZUNION ZUNIONSTORE`)

// containerCommands take their subcommand as the next word; one is granted
// only with the subcommand the file names.
var containerCommands = wordSet(`OBJECT XGROUP XINFO`)

func wordSet(words string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(words) {
		out[w] = true
	}
	return out
}

// reLuaComment strips a comment to the end of its line, as directCalls
// (luacommands_test.go) reads a file's direct calls with it; a "--" inside a
// quoted value is a comment to that reading, so it only ever misses calls.
// commandsOf reads a file as Lua's own lexer does and does not use it.
var reLuaComment = regexp.MustCompile(`(?m)--.*$`)

var (
	reLuaCommandWord = regexp.MustCompile(`^[A-Z][A-Z_]*$`)
	reLuaSubcommand  = regexp.MustCompile(`^[A-Za-z-]+$`)
)

// luaToken is one token of a Lua text commandsOf reads: a string's value, or
// a run of symbols between strings.
type luaToken struct {
	value string
	isStr bool
}

// luaTokens reads a Lua text as Lua 5.1's own lexer does (llex.c, the Lua
// that Redis embeds; pkg/redisfn/scan.go holds the conventions this
// reader follows without calling it, because redisfn exports no function for
// the reading and this repair changes no file outside this package): the
// quoted strings with their values, escapes decoded, and the symbol runs
// between them; a comment, line or long, and a long string are read and
// passed by as Lua passes them, so a fake command inside one crosses no
// boundary. The loader refuses a string that does not end (a quoted one off
// its line, a long one that never closes) before any file runs, so the read
// ends there and what came before stands.
func luaTokens(src string) []luaToken {
	var out []luaToken
	for pos := 0; pos < len(src); {
		switch c := src[pos]; {
		case c == '"' || c == '\'':
			value, end, ok := quotedLua(src, pos)
			if !ok {
				return out
			}
			out = append(out, luaToken{value, true})
			pos = end
		case strings.HasPrefix(src[pos:], "--"):
			end, long := longCommentEnd(src, pos+2)
			if long {
				pos = end
				continue
			}
			for pos += 2; pos < len(src) && src[pos] != '\n'; pos++ {
			}
		case c == '[':
			level, long := longBracketOpen(src, pos)
			if !long {
				out = append(out, luaToken{"[", false})
				pos++
				continue
			}
			value, end, closed := longBracketClose(src, pos+level+2, level)
			if !closed {
				return out
			}
			out = append(out, luaToken{strings.TrimPrefix(value, "\n"), true})
			pos = end
		case c == ',':
			out = append(out, luaToken{",", false})
			pos++
		case c == ' ' || c == '\t' || c == '\f' || c == '\v' || c == '\r' || c == '\n':
			pos++
		default:
			end := pos + 1
			for end < len(src) && !isLuaEdge(src[end]) {
				end++
			}
			out = append(out, luaToken{src[pos:end], false})
			pos = end
		}
	}
	return out
}

// isLuaEdge is the byte that ends a run of symbols between strings: the
// quotes, a bracket that may open a long string, a dash that may open a
// comment, a comma, and the separators Lua reads between words.
func isLuaEdge(c byte) bool {
	switch c {
	case '"', '\'', '[', '-', ',', ' ', '\t', '\f', '\v', '\r', '\n':
		return true
	}
	return false
}

// longBracketOpen reports whether a long bracket opens at pos ("[", any
// number of "=", "["), and its level, the number of "=".
func longBracketOpen(src string, pos int) (level int, ok bool) {
	if pos >= len(src) || src[pos] != '[' {
		return 0, false
	}
	for pos+1+level < len(src) && src[pos+1+level] == '=' {
		level++
	}
	return level, pos+1+level < len(src) && src[pos+1+level] == '['
}

// longBracketClose finds the closing bracket of that level at or after pos:
// the text before it and the position after it.
func longBracketClose(src string, pos, level int) (value string, end int, ok bool) {
	at := strings.Index(src[pos:], "]"+strings.Repeat("=", level)+"]")
	if at < 0 {
		return "", 0, false
	}
	end = pos + at
	return src[pos:end], end + level + 2, true
}

// longCommentEnd is the end of the long comment that opens at pos, when one
// does: "--" followed by a long bracket.
func longCommentEnd(src string, pos int) (end int, ok bool) {
	level, long := longBracketOpen(src, pos)
	if !long {
		return 0, false
	}
	_, end, closed := longBracketClose(src, pos+level+2, level)
	if !closed {
		return len(src), true // Lua runs an unclosed long comment to the end
	}
	return end, true
}

// quotedLua reads the quoted string that opens at pos: its value, escapes
// decoded as Lua decodes them, and the position after its closing quote. It
// follows pkg/redisfn/scan.go's quoted, which this package cannot call
// (redisfn exports no function for it, and the repair changes no file
// outside this package). A string that does not end on its line is refused
// by the loader, so not ok ends the read.
func quotedLua(src string, pos int) (value string, end int, ok bool) {
	quote := src[pos]
	var b strings.Builder
	for i := pos + 1; i < len(src); i++ {
		switch c := src[i]; {
		case c == quote:
			return b.String(), i + 1, true
		case c == '\n':
			return "", 0, false
		case c != '\\':
			b.WriteByte(c)
		case i+1 == len(src):
			return "", 0, false
		default:
			i++
			c = src[i]
			if isLuaDigit(c) {
				code := 0
				for d := 0; d < 3 && isLuaDigit(src[i]); d++ {
					code = code*10 + int(src[i]-'0')
					if i+1 < len(src) && isLuaDigit(src[i+1]) {
						i++
					} else {
						break
					}
				}
				b.WriteByte(byte(code)) // a code over 255 is Lua's error, and then the store's
				break
			}
			if at := strings.IndexByte("abfnrtv", c); at >= 0 {
				c = "\a\b\f\n\r\t\v"[at]
			}
			b.WriteByte(c)
		}
	}
	return "", 0, false
}

func isLuaDigit(c byte) bool { return c >= '0' && c <= '9' }

// commandsOf is the commands a Lua text names, as ACL rules spell them:
// lower case, a container's subcommand after a bar, sorted.
func commandsOf(src string) []string {
	toks := luaTokens(src)
	set := map[string]bool{}
	for i := 0; i < len(toks); i++ {
		if !toks[i].isStr || !reLuaCommandWord.MatchString(toks[i].value) {
			continue
		}
		cmd := toks[i].value
		sub := ""
		if i+2 < len(toks) && !toks[i+1].isStr && toks[i+1].value == "," &&
			toks[i+2].isStr && reLuaSubcommand.MatchString(toks[i+2].value) {
			sub, i = toks[i+2].value, i+2
		}
		switch {
		case containerCommands[cmd] && sub != "":
			set[strings.ToLower(cmd+"|"+sub)] = true
		case redisCommands[cmd] && !containerCommands[cmd]:
			set[strings.ToLower(cmd)] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// LuaCommands is the commands each file of the library names, by the file's
// name as the glob matches it.
func LuaCommands(lib redisfn.Library) (map[string][]string, error) {
	files, err := fs.Glob(lib.Files, lib.Glob)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", lib.Glob, err)
	}
	out := make(map[string][]string, len(files))
	for _, f := range files {
		b, err := fs.ReadFile(lib.Files, f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		out[f] = commandsOf(string(b))
	}
	return out, nil
}
