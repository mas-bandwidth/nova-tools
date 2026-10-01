package redisacl

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/redisfn"
)

// A function runs every command its Lua calls under the caller's ACL, so a
// role that may FCALL a function must also hold each command the function's
// file calls. The commands are read out of the library's own text: every
// quoted upper-case word of a file (comments aside) that names a Redis
// command, the word after a container command (XINFO 'STREAM') making its
// subcommand. A file builds some of its commands as data ({'HSET', ...},
// T.stage(d, 'DEL', ...)), so a call site alone would miss them; a word that
// names no command ('OK', 'REFUSED') is a reply, not a call.
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

var (
	reLuaComment = regexp.MustCompile(`(?m)--.*$`)
	reLuaWord    = regexp.MustCompile(`'([A-Z][A-Z_]*)'(\s*,\s*'([A-Za-z-]+)')?`)
	reLuaCall    = regexp.MustCompile(`redis\.p?call\(\s*'([A-Za-z_]+)'`)
)

// commandsOf is the commands a Lua text names, as ACL rules spell them:
// lower case, a container's subcommand after a bar, sorted.
func commandsOf(src string) []string {
	src = reLuaComment.ReplaceAllString(src, "")
	set := map[string]bool{}
	for _, m := range reLuaWord.FindAllStringSubmatch(src, -1) {
		cmd := m[1]
		switch {
		case containerCommands[cmd] && m[3] != "":
			set[strings.ToLower(cmd+"|"+m[3])] = true
		case redisCommands[cmd] && !containerCommands[cmd]:
			set[strings.ToLower(cmd)] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
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

// directCalls is every command name a Lua text passes to redis.call or
// redis.pcall as a literal, upper-cased.
func directCalls(src string) []string {
	src = reLuaComment.ReplaceAllString(src, "")
	var out []string
	for _, m := range reLuaCall.FindAllStringSubmatch(src, -1) {
		out = append(out, strings.ToUpper(m[1]))
	}
	return out
}
