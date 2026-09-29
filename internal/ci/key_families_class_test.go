package ci

import (
	"go/ast"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/audit"
)

// The key families rule (#4334): every Redis key pattern written or read in
// Lua or Go belongs to a registered key family in internal/nsprint/audit.
//
// A forgotten key family leaves orphan keys persisting in Redis after sprints
// are cleared, corrupting table counts or consuming memory. sprint audit
// lists orphan keys and tallies counts per registered family, and
// sprint audit --purge <family> deletes keys belonging to a registered family
// through the registry.

var (
	luaCallRe = regexp.MustCompile(`redis\.(?:p?call)\(\s*['"]([A-Z]+)['"]\s*,\s*([^,\)]+)`)
	luaBindRe = regexp.MustCompile(`local\s+(\w+)\s*=\s*(['"][^'"]+['"](?:\s*\.\.\s*[^,\n]+)?)`)

	redisCommands = map[string]bool{
		"GET": true, "SET": true, "SETNX": true, "DEL": true, "EXISTS": true, "TYPE": true,
		"EXPIRE": true, "EXPIREAT": true, "TTL": true, "HGET": true, "HSET": true, "HSETNX": true,
		"HMGET": true, "HMSET": true, "HDEL": true, "HEXISTS": true, "HGETALL": true, "HKEYS": true,
		"HVALS": true, "HLEN": true, "HINCRBY": true, "LPUSH": true, "RPUSH": true, "LPOP": true,
		"RPOP": true, "LRANGE": true, "LLEN": true, "LREM": true, "LINDEX": true, "LSET": true,
		"LTRIM": true, "SADD": true, "SREM": true, "SMEMBERS": true, "SISMEMBER": true, "SCARD": true,
		"SPOP": true, "SRANDMEMBER": true, "SMOVE": true, "ZADD": true, "ZREM": true, "ZSCORE": true,
		"ZRANGE": true, "ZREVRANGE": true, "ZCARD": true, "ZCOUNT": true, "ZINCRBY": true, "ZRANK": true,
		"ZREVRANK": true, "ZREMRANGEBYSCORE": true, "ZREMRANGEBYRANK": true, "ZPOPMIN": true, "ZPOPMAX": true,
		"XADD": true, "XRANGE": true, "XREVRANGE": true, "XLEN": true, "XACK": true, "XDEL": true, "XTRIM": true,
		"INCR": true, "INCRBY": true, "DECR": true, "DECRBY": true,
	}

	redisGoMethod = map[string]bool{
		"Get": true, "Set": true, "HGet": true, "HSet": true, "HDel": true, "HGetAll": true,
		"HMGet": true, "HMSet": true, "ZAdd": true, "ZRem": true, "ZRange": true, "ZCard": true,
		"ZScore": true, "Del": true, "XAdd": true,
	}
)

func extractLuaRedisKeys(file, src string) []string {
	var keys []string
	varBindings := make(map[string]string)

	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if commentIdx := strings.Index(trimmed, "--"); commentIdx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:commentIdx])
		}

		if m := luaBindRe.FindStringSubmatch(trimmed); len(m) > 2 {
			varName := m[1]
			rawLit := m[2]
			if quoteStart := strings.IndexAny(rawLit, `'"`); quoteStart >= 0 {
				quoteChar := rawLit[quoteStart]
				if quoteEnd := strings.IndexByte(rawLit[quoteStart+1:], quoteChar); quoteEnd >= 0 {
					litVal := rawLit[quoteStart+1 : quoteStart+1+quoteEnd]
					varBindings[varName] = litVal
				}
			}
		}

		for _, m := range luaCallRe.FindAllStringSubmatch(trimmed, -1) {
			cmd := m[1]
			arg := strings.TrimSpace(m[2])
			if !redisCommands[cmd] {
				continue
			}

			// If arg is a string literal
			if len(arg) > 1 && (arg[0] == '\'' || arg[0] == '"') {
				quoteChar := arg[0]
				if end := strings.IndexByte(arg[1:], quoteChar); end >= 0 {
					lit := arg[1 : end+1]
					if strings.Contains(arg, "..") && !strings.HasSuffix(lit, ":") {
						lit += ":"
					}
					keys = append(keys, lit)
				}
			} else if bound, ok := varBindings[arg]; ok {
				keys = append(keys, bound)
			}
		}
	}
	return keys
}

func extractGoRedisKeys(f *treeFile) []string {
	if f.AST == nil {
		return nil
	}
	var keys []string

	ast.Inspect(f.AST, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !redisGoMethod[sel.Sel.Name] {
			return true
		}
		if len(call.Args) < 2 {
			return true
		}
		// The key is typically the second argument (after ctx)
		keyArg := call.Args[1]

		extractKeyExpr(keyArg, &keys)
		return true
	})

	return keys
}

func extractKeyExpr(e ast.Expr, keys *[]string) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			if err == nil && s != "" && s != "*" {
				*keys = append(*keys, s)
			}
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			if lit, ok := x.X.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, err := strconv.Unquote(lit.Value)
				if err == nil && s != "" {
					if !strings.HasSuffix(s, ":") && strings.Contains(s, ":") {
						s = s[:strings.Index(s, ":")+1]
					}
					*keys = append(*keys, s)
				}
			}
		}
	}
}

// TestEveryKeyFamilyIsRegistered holds the contract: every Redis key pattern
// written or read in Go and Lua matches a registered family in internal/nsprint/audit.
func TestEveryKeyFamilyIsRegistered(t *testing.T) {
	t.Parallel()

	reg := audit.DefaultRegistry()
	var failures []string

	// 1. Inspect Lua scripts
	luaSrcs := luaSources(t)
	var luaFiles []string
	for f := range luaSrcs {
		luaFiles = append(luaFiles, f)
	}
	sort.Strings(luaFiles)

	for _, file := range luaFiles {
		keys := extractLuaRedisKeys(file, luaSrcs[file])
		for _, k := range keys {
			sample := k
			if strings.HasSuffix(k, ":") {
				sample = k + "sample"
			}
			if _, ok := reg.Match(sample); !ok {
				failures = append(failures, file+": unmatched Redis key "+strconv.Quote(k))
			}
		}
	}

	// 2. Inspect Go files under internal/nsprint and deprecated/cmd/nova-sprint
	tree := repoTree(t)
	for _, f := range tree.Files {
		if !f.Go || f.Test {
			continue
		}
		if !f.InDir("internal/nsprint") && !f.InDir("deprecated/cmd/nova-sprint") {
			continue
		}
		if strings.Contains(f.Rel, "/testutil") || strings.Contains(f.Rel, "/table/testdata") {
			continue
		}
		keys := extractGoRedisKeys(f)
		for _, k := range keys {
			sample := k
			if strings.HasSuffix(k, ":") {
				sample = k + "sample"
			}
			if _, ok := reg.Match(sample); !ok {
				failures = append(failures, f.Rel+": unmatched Redis key "+strconv.Quote(k))
			}
		}
	}

	sort.Strings(failures)
	for _, f := range failures {
		t.Errorf("unregistered Redis key pattern (remedy=register the key family in internal/nsprint/audit.DefaultFamilies; nova-tools#4334): %s", f)
	}
}

// TestEveryKeyFamilyIsRegisteredCatchesUnregisteredKey asserts that an unregistered
// key pattern is caught by the registry matcher.
func TestEveryKeyFamilyIsRegisteredCatchesUnregisteredKey(t *testing.T) {
	t.Parallel()

	reg := audit.DefaultRegistry()
	bogusKeys := []string{
		"unknown_service:item",
		"random_orphan_key",
		"stray:orphan:123",
	}

	for _, key := range bogusKeys {
		if _, ok := reg.Match(key); ok {
			t.Errorf("registry matched bogus key %q, want unmatched", key)
		}
	}
}
