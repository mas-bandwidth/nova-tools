package testredis

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// OnlyFCALL is a go-redis hook for a test whose code writes to the store
// through FCALL and through nothing else:
//
//	client.AddHook(testredis.OnlyFCALL(t))
//
// A command in the fixed list of this file (writeGroups: every write command of
// Redis 8, by type) fails the test with the command's name, and the command is
// not sent, so the store never sees the write the test has just failed on. The
// caller gets an error that names the command, and so does every command of a
// pipeline or a transaction that held one.
//
// WHAT FAILS. A command that writes and is not FCALL: SET, HSET, XADD, DEL,
// FLUSHALL and the rest of the list, and the two commands that run a script
// that may write (EVAL, EVALSHA), which is a way round the loaded library. A
// command with subcommands is a write by its pair: FUNCTION LOAD and XGROUP
// CREATE fail, FUNCTION LIST and XGROUP HELP do not. The names are read
// without regard to case.
//
// WHAT PASSES. FCALL, whatever the function does inside the server (the hook
// sees the call and not its effects), FCALL_RO, EVAL_RO and EVALSHA_RO, every
// read, and everything else not in the list: the handshake, MULTI, EXEC, WATCH,
// PUBLISH and the administrative commands. The list holds the commands that
// write to the keyspace, not every command that changes something.
//
// THE LIST DECIDES, NEVER THE SERVER. The hook asks the server nothing, sends
// nothing and works on a client that has no server behind it. The source of the
// list and the test that holds it to a real server are on writeGroups.
//
// A pipeline or a transaction is judged whole: when one command in it writes,
// the failure names every write in it, with its place in the batch, and none
// of the batch is sent. A transaction's MULTI and EXEC pass; the commands
// between them are judged one by one.
//
// LOADING THE LIBRARY IS A WRITE. FUNCTION LOAD fails on a client that has this
// hook, so a test loads the library through another client and hands the code
// under test the one that has the hook.
//
// The failure is reported with Errorf, so a client used from a goroutine other
// than the test's reports it there too; the test ends failed when the test
// function returns. Like the rest of the package it panics outside a test
// binary.
func OnlyFCALL(t testing.TB) redis.Hook {
	t.Helper()
	real.refuse()
	return &onlyFCALL{t: t}
}

// onlyFCALL is the hook: it holds the test it fails.
type onlyFCALL struct {
	t testing.TB
}

// DialHook leaves connecting alone.
func (h *onlyFCALL) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook fails the test on a write that is not FCALL, and does not send
// that write.
func (h *onlyFCALL) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := writeName(cmd.Args())
		if name == "" {
			return next(ctx, cmd)
		}
		h.t.Errorf("testredis: OnlyFCALL: %s is a write command other than FCALL; it was not sent", name)
		err := refusal(name)
		cmd.SetErr(err)
		return err
	}
}

// ProcessPipelineHook fails the test when a command of the batch writes and is
// not FCALL, names every such command, and sends none of the batch. It is the
// hook of a transaction too.
func (h *onlyFCALL) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		var found []string
		for i, cmd := range cmds {
			if name := writeName(cmd.Args()); name != "" {
				found = append(found, fmt.Sprintf("%s (command %d of %d)", name, i+onePosition, len(cmds)))
			}
		}
		if len(found) == 0 {
			return next(ctx, cmds)
		}
		what := strings.Join(found, ", ")
		h.t.Errorf("testredis: OnlyFCALL: the pipeline holds a write command other than FCALL: %s; none of it was sent", what)
		err := refusal(what)
		for _, cmd := range cmds {
			cmd.SetErr(err)
		}
		return err
	}
}

// onePosition turns an index into the place a reader counts from: the first
// command of a batch is command 1.
const onePosition = 1

// refusal is what the caller of a refused command is given.
func refusal(what string) error {
	return fmt.Errorf("testredis: OnlyFCALL refused %s: a write command other than FCALL", what)
}

// writeName is the entry of the list that the command is, in capitals, or ""
// when the command does not write. The command is its first word, and its first
// two words when the pair is the entry (XGROUP CREATE).
func writeName(args []any) string {
	if len(args) == 0 {
		return ""
	}
	name := strings.ToUpper(word(args[0]))
	if writes[name] {
		return name
	}
	if len(args) > 1 {
		pair := name + " " + strings.ToUpper(word(args[1]))
		if writes[pair] {
			return pair
		}
	}
	return ""
}

// word is an argument of a command as text. The typed methods of go-redis send
// strings; a command built by hand may send bytes.
func word(arg any) string {
	switch v := arg.(type) {
	case string:
		return v
	case *string:
		if v != nil {
			return *v
		}
		return ""
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(arg)
	}
}

// origin says where the server stands on a group of the list, which is what
// the functional test compares.
type origin int

const (
	// flagged: the command table of redis-server carries the write flag on
	// every command of the group.
	flagged origin = iota
	// scripted: the commands run a script that may write. The command table
	// does not flag them, and the list names them because they are the way
	// round a loaded library.
	scripted
	// bundled: commands of the modules Redis 8 bundles (JSON, time series,
	// the probabilistic types, search, vector sets). A build of the server
	// without them does not have the commands; one with them flags each.
	bundled
)

// group is the write commands of one type.
type group struct {
	name     string
	origin   origin
	commands []string
}

// writeGroups IS THE FIXED LIST: every write command of Redis 8, in capitals,
// by type. A command with subcommands is named by its pair ("XGROUP CREATE").
//
// THE SOURCE. A command is a write when its flags in the command table carry
// WRITE: what COMMAND INFO prints as `write` and what the ACL category @write
// holds. The tables are the command definitions of Redis
// (src/commands/*.json, "command_flags", in github.com/redis/redis) and, for
// the modules Redis 8 bundles, their own definitions (each command's page on
// redis.io lists @write). EVAL and EVALSHA are the two additions: the table
// does not flag them, and a script may write.
//
// THE PROOF. The flagged groups are the server's table read on the functional
// tier: TestWriteListIsTheWriteFlagOfTheServer starts the server the image
// carries, reads COMMAND, and fails on a command the server flags that no group
// names, on a flagged group's command the server does not flag, and on a
// scripted command the server flags or does not have. A new Redis that adds a
// write command is a red run there, and the fix is one name in a group here.
// The bundled groups are held to the server only when it has the modules; the
// image builds the server without them.
var writeGroups = []group{
	{"generic", flagged, []string{
		"COPY", "DEL", "EXPIRE", "EXPIREAT", "MIGRATE", "MOVE", "PERSIST",
		"PEXPIRE", "PEXPIREAT", "RENAME", "RENAMENX", "RESTORE", "RESTORE-ASKING",
		"SORT", "UNLINK",
	}},
	{"string", flagged, []string{
		"APPEND", "DECR", "DECRBY", "DELEX", "GETDEL", "GETEX", "GETSET", "INCR",
		"INCRBY", "INCRBYFLOAT", "INCREX", "MSET", "MSETEX", "MSETNX", "PSETEX",
		"SET", "SETEX", "SETNX", "SETRANGE",
	}},
	{"bitmap", flagged, []string{
		"BITFIELD", "BITOP", "SETBIT",
	}},
	{"array", flagged, []string{
		"ARDEL", "ARDELRANGE", "ARINSERT", "ARMSET", "ARRING", "ARSEEK", "ARSET",
	}},
	{"hash", flagged, []string{
		"HDEL", "HEXPIRE", "HEXPIREAT", "HGETDEL", "HGETEX", "HIMPORT SET",
		"HINCRBY", "HINCRBYFLOAT", "HMSET", "HPERSIST", "HPEXPIRE", "HPEXPIREAT",
		"HSET", "HSETEX", "HSETNX",
	}},
	{"list", flagged, []string{
		"BLMOVE", "BLMOVEM", "BLMPOP", "BLPOP", "BRPOP", "BRPOPLPUSH", "LINSERT",
		"LMOVE", "LMOVEM", "LMPOP", "LPOP", "LPUSH", "LPUSHX", "LREM", "LSET", "LTRIM", "RPOP",
		"RPOPLPUSH", "RPUSH", "RPUSHX",
	}},
	{"set", flagged, []string{
		"SADD", "SDIFFSTORE", "SINTERSTORE", "SMOVE", "SPOP", "SREM", "SUNIONSTORE",
	}},
	{"sorted set", flagged, []string{
		"BZMPOP", "BZPOPMAX", "BZPOPMIN", "ZADD", "ZDIFFSTORE", "ZINCRBY",
		"ZINTERSTORE", "ZMPOP", "ZPOPMAX", "ZPOPMIN", "ZRANGESTORE", "ZREM",
		"ZREMRANGEBYLEX", "ZREMRANGEBYRANK", "ZREMRANGEBYSCORE", "ZUNIONSTORE",
	}},
	{"stream", flagged, []string{
		"XACK", "XACKDEL", "XADD", "XAUTOCLAIM", "XCFGSET", "XCLAIM", "XDEL",
		"XDELEX", "XGROUP CREATE", "XGROUP CREATECONSUMER", "XGROUP DELCONSUMER",
		"XGROUP DESTROY", "XGROUP SETID", "XIDMPRECORD", "XNACK", "XREADGROUP",
		"XSETID", "XTRIM",
	}},
	{"HyperLogLog", flagged, []string{
		"PFADD", "PFDEBUG", "PFMERGE",
	}},
	{"geo", flagged, []string{
		"GEOADD", "GEORADIUS", "GEORADIUSBYMEMBER", "GEOSEARCHSTORE",
	}},
	{"function", flagged, []string{
		"FUNCTION DELETE", "FUNCTION FLUSH", "FUNCTION LOAD", "FUNCTION RESTORE",
	}},
	{"server", flagged, []string{
		"FLUSHALL", "FLUSHDB", "SWAPDB", "TRIMSLOTS",
	}},
	{"script", scripted, []string{
		"EVAL", "EVALSHA",
	}},
	{"JSON", bundled, []string{
		"JSON.ARRAPPEND", "JSON.ARRINSERT", "JSON.ARRPOP", "JSON.ARRTRIM",
		"JSON.CLEAR", "JSON.DEL", "JSON.FORGET", "JSON.MERGE", "JSON.MSET",
		"JSON.NUMINCRBY", "JSON.NUMMULTBY", "JSON.SET", "JSON.STRAPPEND",
		"JSON.TOGGLE",
	}},
	{"time series", bundled, []string{
		"TS.ADD", "TS.ALTER", "TS.CREATE", "TS.CREATERULE", "TS.DECRBY", "TS.DEL",
		"TS.DELETERULE", "TS.INCRBY", "TS.MADD",
	}},
	{"Bloom filter", bundled, []string{
		"BF.ADD", "BF.INSERT", "BF.LOADCHUNK", "BF.MADD", "BF.RESERVE",
	}},
	{"Cuckoo filter", bundled, []string{
		"CF.ADD", "CF.ADDNX", "CF.DEL", "CF.INSERT", "CF.INSERTNX", "CF.LOADCHUNK",
		"CF.RESERVE",
	}},
	{"count-min sketch", bundled, []string{
		"CMS.INCRBY", "CMS.INITBYDIM", "CMS.INITBYPROB", "CMS.MERGE",
	}},
	{"top-k", bundled, []string{
		"TOPK.ADD", "TOPK.INCRBY", "TOPK.RESERVE",
	}},
	{"t-digest", bundled, []string{
		"TDIGEST.ADD", "TDIGEST.CREATE", "TDIGEST.MERGE", "TDIGEST.RESET",
	}},
	{"search", bundled, []string{
		"FT.ALIASADD", "FT.ALIASDEL", "FT.ALIASUPDATE", "FT.ALTER", "FT.CREATE",
		"FT.DICTADD", "FT.DICTDEL", "FT.DROPINDEX", "FT.SUGADD", "FT.SUGDEL",
		"FT.SYNUPDATE",
	}},
	{"vector set", bundled, []string{
		"VADD", "VREM", "VSETATTR",
	}},
}

// writes is the list as a set, built once from writeGroups.
var writes = writeSet(writeGroups)

func writeSet(groups []group) map[string]bool {
	set := map[string]bool{}
	for _, g := range groups {
		for _, name := range g.commands {
			set[name] = true
		}
	}
	return set
}
