package sprintfn

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// The twin's copy of the sprint's own keys (errata E3): the agenda, the due
// sets, the indexes, the cursor, the lease, the heartbeat, holds, parked
// keys, quarantine and counters, as typed Redis values. The pre stage reads it
// through Keys; only the twin's commit writes it, and only with the commands
// Layer 1's prepare accepts from an enclosing plan.

// The key types a command may name (L1 1.4).
const (
	kindHash   = "hash"
	kindZSet   = "zset"
	kindList   = "list"
	kindString = "string"
	kindNone   = "none"
)

// writeKinds is Layer 1's write command registry (L1 1.6; table_set.lua
// write_kinds) less XADD, which only the log writes: each command and the
// type of the key it writes. There is no ZADD NX: the registry refuses its
// arity, so a part that wants NX decides it in its pre from the score it read
// and writes a plain ZADD, which the call's atomicity makes the same.
var writeKinds = map[string]string{"HSET": kindHash, "HDEL": kindHash, "ZADD": kindZSet,
	"ZREM": kindZSet, "RPUSH": kindList, "SET": kindString}

// Bounds of one command descriptor (L1 1.4).
const (
	maxArgv   = 2002 // argv values of one command, command and key included
	maxPieces = 1000 // collection elements of one command: a pair for HSET and ZADD
)

// layerKeys are the keys under the prefix's "sprint:" that Layer 1 and Layer 2
// own (L1 1.2): the epoch marker and its snapshots, the log, the histories and
// the receipts. No sprint command may write one.
var layerKeys = []string{"sprint:epoch@", "sprint:log@", "sprint:cl:", "sprint:done@"}

type keyValue struct {
	kind string
	hash map[string]string
	zset map[string]float64
	list []string
	str  string
}

type keyspace struct {
	vals map[string]*keyValue
}

func newKeyspace() *keyspace { return &keyspace{vals: map[string]*keyValue{}} }

func (ks *keyspace) typeOf(key string) string {
	if v := ks.vals[key]; v != nil {
		return v.kind
	}
	return kindNone
}

// cmdCost is what a checked list of commands costs against the step's shared
// bounds (L1 6).
type cmdCost struct{ commands, argvBytes int }

// check validates a list of commands as prepare does for an enclosing plan:
// each command in the registry, with its access declared, on a key of the
// sprint's own under the prefix, of the right arity, pieces and type, the
// types projected through the list so an earlier command cannot make a later
// one ill-typed (L1 1.4, 1.6).
func (ks *keyspace) check(prefix string, cmds []Cmd) (cmdCost, *Refusal) {
	projected := map[string]string{}
	var cost cmdCost
	bad := func(code string) (cmdCost, *Refusal) { return cmdCost{}, refuse(PhasePrepare, code, RefusalDetail{}) }
	for _, c := range cmds {
		n := len(c.Argv)
		if n < 2 {
			return bad(CodeRequest)
		}
		if n > maxArgv {
			return bad(CodeLimit)
		}
		command, key := c.Argv[0], c.Argv[1]
		want, ok := writeKinds[command]
		if !ok || len(c.Access) != 1 || c.Access[0] != (Access{Key: key, Kind: want, Mode: "write"}) {
			return bad(CodeRequest)
		}
		if !ownKey(prefix, key) {
			return bad(CodeRequest)
		}
		switch command {
		case "HSET", "ZADD":
			if n < 4 || n%2 != 0 {
				return bad(CodeRequest)
			}
			if (n-2)/2 > maxPieces {
				return bad(CodeLimit)
			}
			if command == "ZADD" {
				for i := 2; i < n; i += 2 {
					if _, ok := parseScore(c.Argv[i]); !ok {
						return bad(CodeRequest)
					}
				}
			}
		case "HDEL", "ZREM", "RPUSH":
			if n < 3 {
				return bad(CodeRequest)
			}
			if n-2 > maxPieces {
				return bad(CodeLimit)
			}
		case "SET":
			if n != 3 {
				return bad(CodeRequest)
			}
		}
		actual, seen := projected[key]
		if !seen {
			actual = ks.typeOf(key)
		}
		if actual != kindNone && actual != want {
			return bad(CodeWrongType)
		}
		projected[key] = want
		cost.commands++
		for _, a := range c.Argv {
			cost.argvBytes += len(a)
		}
	}
	return cost, nil
}

// ownKey says a key is one of the sprint's own: under the prefix's "sprint:",
// and none of Layer 1's or Layer 2's.
func ownKey(prefix, key string) bool {
	if !strings.HasPrefix(key, prefix+"sprint:") || key == prefix+"sprint:epoch" {
		return false
	}
	for _, k := range layerKeys {
		if strings.HasPrefix(key, prefix+k) {
			return false
		}
	}
	return true
}

// parseScore reads a ZADD score as Redis does, refusing NaN.
func parseScore(s string) (float64, bool) {
	switch s {
	case "+inf", "inf":
		return math.Inf(1), true
	case "-inf":
		return math.Inf(-1), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || strings.TrimSpace(s) != s {
		return 0, false
	}
	return f, true
}

// apply runs a checked list. It cannot fail: every check was check's.
func (ks *keyspace) apply(cmds []Cmd) {
	for _, c := range cmds {
		key, args := c.Argv[1], c.Argv[2:]
		v := ks.vals[key]
		if v == nil {
			v = &keyValue{kind: writeKinds[c.Argv[0]]}
			ks.vals[key] = v
		}
		switch c.Argv[0] {
		case "HSET":
			if v.hash == nil {
				v.hash = map[string]string{}
			}
			for i := 0; i+1 < len(args); i += 2 {
				v.hash[args[i]] = args[i+1]
			}
		case "HDEL":
			for _, f := range args {
				delete(v.hash, f)
			}
		case "ZADD":
			if v.zset == nil {
				v.zset = map[string]float64{}
			}
			for i := 0; i+1 < len(args); i += 2 {
				score, _ := parseScore(args[i])
				v.zset[args[i+1]] = score
			}
		case "ZREM":
			for _, m := range args {
				delete(v.zset, m)
			}
		case "RPUSH":
			v.list = append(v.list, args...)
		case "SET":
			v.str = args[0]
		}
		if len(v.hash) == 0 && len(v.zset) == 0 && len(v.list) == 0 && v.kind != kindString {
			delete(ks.vals, key) // Redis deletes an emptied collection
		}
	}
}

// KeyValue is one sprint key as the twin holds it: its type and its value.
type KeyValue struct {
	Kind   string
	Hash   map[string]string
	ZSet   map[string]float64
	List   []string
	String string
}

// dump is a deep copy of every key.
func (ks *keyspace) dump() map[string]KeyValue {
	out := make(map[string]KeyValue, len(ks.vals))
	for k, v := range ks.vals {
		kv := KeyValue{Kind: v.kind, String: v.str, List: append([]string(nil), v.list...)}
		if v.hash != nil {
			kv.Hash = make(map[string]string, len(v.hash))
			for f, x := range v.hash {
				kv.Hash[f] = x
			}
		}
		if v.zset != nil {
			kv.ZSet = make(map[string]float64, len(v.zset))
			for m, s := range v.zset {
				kv.ZSet[m] = s
			}
		}
		out[k] = kv
	}
	return out
}

// Keys is the read view of the sprint's own keys the phases get in State.
type Keys struct {
	ks *keyspace
}

// Type is the key's type: hash, zset, list, string, or none when absent.
func (k *Keys) Type(key string) string { return k.ks.typeOf(key) }

// HGet is one field of a hash, and false when the key or field is absent.
func (k *Keys) HGet(key, field string) (string, bool) {
	v := k.ks.vals[key]
	if v == nil || v.kind != kindHash {
		return "", false
	}
	x, ok := v.hash[field]
	return x, ok
}

// HGetAll is a copy of a hash, empty when absent.
func (k *Keys) HGetAll(key string) map[string]string {
	out := map[string]string{}
	if v := k.ks.vals[key]; v != nil && v.kind == kindHash {
		for f, x := range v.hash {
			out[f] = x
		}
	}
	return out
}

// ZScore is a member's score, and false when absent.
func (k *Keys) ZScore(key, member string) (float64, bool) {
	v := k.ks.vals[key]
	if v == nil || v.kind != kindZSet {
		return 0, false
	}
	s, ok := v.zset[member]
	return s, ok
}

// ZCard is the number of members of a sorted set.
func (k *Keys) ZCard(key string) int {
	if v := k.ks.vals[key]; v != nil && v.kind == kindZSet {
		return len(v.zset)
	}
	return 0
}

// ZMember is one member of a sorted set and its score.
type ZMember struct {
	Member string
	Score  float64
}

// ZRangeByScore is the members scored in [min, max], lowest first and by
// member within a score, at most limit of them (limit <= 0: all).
func (k *Keys) ZRangeByScore(key string, min, max float64, limit int) []ZMember {
	v := k.ks.vals[key]
	if v == nil || v.kind != kindZSet {
		return nil
	}
	var out []ZMember
	for m, s := range v.zset {
		if s >= min && s <= max {
			out = append(out, ZMember{Member: m, Score: s})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].Member < out[j].Member
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// LRange is a copy of a list.
func (k *Keys) LRange(key string) []string {
	if v := k.ks.vals[key]; v != nil && v.kind == kindList {
		return append([]string(nil), v.list...)
	}
	return nil
}

// Get is a string key's value, and false when absent.
func (k *Keys) Get(key string) (string, bool) {
	v := k.ks.vals[key]
	if v == nil || v.kind != kindString {
		return "", false
	}
	return v.str, true
}
