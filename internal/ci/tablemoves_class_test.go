package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The table moves rule (nova-tools #3929; rowan-new specs/table-moves.md):
// nothing but the one move file, internal/nsprint/fn/lua/02_card_move.lua,
// writes a set behind the three tables:
//
//	ws:<stream>:<where>               the stream table (primaries)
//	bench:<b>:cards:<col>             the host table (copies, sprint cards)
//	friend:<f>:cards:<col>            the friend table (copies, friend-queue tasks)
//	bench:<b>:living | :starting      the bench lease ledgers being folded into
//	                                  bench:<b>:cards:working (#3877)
//
// A ZADD, ZREM, SADD, SREM, SMOVE (or a pop, range removal, store, DEL,
// UNLINK or RENAME) of one of them in any other Lua file, or any Go write of
// one outside a fixture, fails here. The legacy writers of the two bench
// lease ledgers are a ratchet (legacyLedgerWriters): a file may keep at most
// its count until the fold lands, a new writer fails, and a count that fell
// must be lowered here in the same change.

// legacyLedgerWriters are the Lua writes of bench:<b>:living|starting that
// predate the rule, per file: they only go down.
var legacyLedgerWriters = map[string]int{
	"bench_reset.lua":        2,
	"card_run.lua":           4,
	"ci.lua":                 2,
	"deal.lua":               2,
	"reconcile_required.lua": 6,
}

// theMoveFile is the one writer.
const theMoveFile = "02_card_move.lua"

var (
	tmLuaWrite = regexp.MustCompile(`redis\.p?call\('(ZADD|ZREM|SADD|SREM|SMOVE|ZPOPMIN|ZPOPMAX|ZREMRANGEBYSCORE|` +
		`ZREMRANGEBYRANK|ZUNIONSTORE|ZINTERSTORE|ZRANGESTORE|DEL|UNLINK|RENAME)',\s*([^,)]+)`)
	tmLuaTable  = regexp.MustCompile(`^('ws:'\s*\.\.|W\.key\(|TM\.key\(|'(bench|friend):'\s*\.\..*':cards:)`)
	tmLuaLedger = regexp.MustCompile(`^'bench:'\s*\.\..*':(living|starting)'`)
	tmLuaBind   = regexp.MustCompile(`local\s+(\w+)\s*=\s*('ws:'\s*\.\..*|'(bench|friend):'\s*\.\..*':cards:.*|'bench:'\s*\.\..*':(living|starting)'.*)$`)

	tmGoWrite = regexp.MustCompile(`\.(ZAdd|ZAddNX|ZAddXX|ZAddArgs|ZIncrBy|ZRem|ZRemRangeByScore|ZRemRangeByRank|ZUnionStore|` +
		`ZInterStore|ZPopMin|ZPopMax|SAdd|SRem|SMove|Del|Unlink|Rename|RenameNX)\(ctx, ([^,)]+)`)
	tmGoKey = regexp.MustCompile(`^("ws:"\s*\+|"(bench|friend):"\s*\+.*":cards:|"bench:"\s*\+.*":(living|starting)"|` +
		`(\w+\.)?(BenchStartingKey|BenchLivingKey|FriendCardsKey|StreamKey|FriendKey|WSKey)\(|\w+\.Key\(("(ready|working|ok|fail)"|col))`)
	tmGoRaw = regexp.MustCompile(`"(ZADD|ZREM|SADD|SREM|SMOVE|DEL|RENAME)", "(ws:|(bench|friend):[^"]*:cards:|bench:[^"]*:(living|starting))`)
)

// tableWrite is one write the rule found: where, and whether it is a bench
// lease ledger write (the ratchet's kind) rather than a table set write.
type tableWrite struct {
	At     string
	Ledger bool
}

// luaTableWrites is the Lua half over one file's source.
func luaTableWrites(name, src string) []tableWrite {
	if name == theMoveFile {
		return nil
	}
	var out []tableWrite
	bound := map[string]bool{}
	ledger := map[string]bool{}
	for i, line := range strings.Split(src, "\n") {
		code := line
		if j := strings.Index(code, "--"); j >= 0 {
			code = code[:j]
		}
		if strings.HasPrefix(code, "local function ") || strings.HasPrefix(code, "function ") {
			bound, ledger = map[string]bool{}, map[string]bool{}
		}
		if m := tmLuaBind.FindStringSubmatch(code); m != nil {
			bound[m[1]] = true
			ledger[m[1]] = tmLuaLedger.MatchString(strings.TrimSpace(m[2]))
		}
		m := tmLuaWrite.FindStringSubmatch(code)
		if m == nil {
			continue
		}
		target := strings.TrimSpace(m[2])
		head := strings.TrimSpace(strings.SplitN(target, "..", 2)[0])
		at := name + ":" + strconv.Itoa(i+1) + ": " + strings.TrimSpace(line)
		switch {
		case tmLuaLedger.MatchString(target):
			out = append(out, tableWrite{At: at, Ledger: true})
		case tmLuaTable.MatchString(target):
			out = append(out, tableWrite{At: at})
		case bound[head]:
			out = append(out, tableWrite{At: at, Ledger: ledger[head]})
		}
	}
	return out
}

// goTableWrites is the Go half over one non-test file's source.
func goTableWrites(rel, src string) []string {
	base := rel[strings.LastIndex(rel, "/")+1:]
	if strings.Contains(base, "fixture") || strings.HasPrefix(rel, "internal/nsprint/ws/wstest/") {
		return nil
	}
	var out []string
	for i, line := range strings.Split(src, "\n") {
		at := rel + ":" + strconv.Itoa(i+1) + ": " + strings.TrimSpace(line)
		if m := tmGoWrite.FindStringSubmatch(line); m != nil {
			k := strings.TrimSpace(m[2])
			if k != `"ws:names"` && k != `"ws:order"` && k != `"ws:checkpoint"` && tmGoKey.MatchString(k) {
				out = append(out, at)
				continue
			}
		}
		if tmGoRaw.MatchString(line) {
			out = append(out, at)
		}
	}
	return out
}

func luaSources(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "internal", "nsprint", "fn", "lua")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".lua") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out[e.Name()] = string(b)
		}
	}
	if _, ok := out[theMoveFile]; !ok || len(out) < 10 {
		t.Fatalf("read %d Lua files from %s, without %s: the rule is reading the wrong tree", len(out), dir, theMoveFile)
	}
	return out
}

// TestTableSetsHaveOneWriter is the rule over the tree.
func TestTableSetsHaveOneWriter(t *testing.T) {
	t.Parallel()
	ledgers := map[string]int{}
	var bad []string
	names := []string{}
	srcs := luaSources(t)
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, w := range luaTableWrites(n, srcs[n]) {
			if w.Ledger {
				ledgers[n]++
				if ledgers[n] <= legacyLedgerWriters[n] {
					continue
				}
			}
			bad = append(bad, w.At)
		}
	}
	for n, allowed := range legacyLedgerWriters {
		if ledgers[n] < allowed {
			t.Errorf("%s now writes bench:<b>:living|starting %d times, allowed %d: lower legacyLedgerWriters[%q] to %d (the ratchet only goes down)",
				n, ledgers[n], allowed, n, ledgers[n])
		}
	}
	tree := repoTree(t)
	checked := 0
	for _, dir := range []string{"cmd", "internal"} {
		for _, src := range tree.GoFilesUnder(false, dir) {
			checked++
			bad = append(bad, goTableWrites(src.Rel, string(src.Src))...)
		}
	}
	if checked == 0 {
		t.Fatal("read no Go source; the rule is reading the wrong tree")
	}
	for _, b := range bad {
		t.Errorf("a second writer of a table set (the one writer is %s; verbs call it: nova-sprint card deal|work|end|land|cancel): %s", theMoveFile, b)
	}
}

// TestTableSetsRuleCatchesAnInjectedWriter feeds the rule a bad writer of
// each kind, in Lua and in Go, and the same lines inside the move file: the
// rule must fail the first and pass the second.
func TestTableSetsRuleCatchesAnInjectedWriter(t *testing.T) {
	t.Parallel()
	bad := []string{
		"redis.call('ZADD', 'bench:' .. b .. ':cards:ready', 1, id)",
		"redis.call('ZREM', 'friend:' .. f .. ':cards:working', id)",
		"redis.call('ZADD', 'ws:' .. s .. ':waiting', 1, id)",
		"redis.call('ZADD', 'bench:' .. bench .. ':living', at, member)",
		"redis.call('SREM', TM.key(c, 'ok'), id)",
		"local k = 'bench:' .. b .. ':cards:working'\n  redis.call('ZADD', k, 1, id)",
	}
	for _, line := range bad {
		src := "local function evil(b, f, s, id, at, member, c)\n  " + line + "\nend\n"
		if got := luaTableWrites("evil.lua", src); len(got) == 0 {
			t.Errorf("the rule missed a Lua writer: %s", line)
		}
		if got := luaTableWrites(theMoveFile, src); len(got) != 0 {
			t.Errorf("the rule refused the move file itself: %v", got)
		}
	}
	for _, line := range []string{
		`c.ZAdd(ctx, "bench:"+b+":cards:working", redis.Z{Score: 1, Member: id})`,
		`pipe.ZRem(ctx, "friend:"+f+":cards:ready", id)`,
		`c.ZRem(ctx, "bench:"+b+":starting", member)`,
		`c.ZAdd(ctx, k.Key("ok"), redis.Z{Score: 1, Member: id})`,
		`{"ZADD", "bench:b:cards:ready", "1", "x"},`,
	} {
		if got := goTableWrites("cmd/nova-sprint/evil.go", line); len(got) == 0 {
			t.Errorf("the rule missed a Go writer: %s", line)
		}
		if got := goTableWrites("internal/nsprint/table/sprint_fixture.go", line); len(got) != 0 {
			t.Errorf("the rule refused a fixture: %v", got)
		}
	}
	// reads are not writes
	for _, line := range []string{`c.ZCard(ctx, "bench:"+b+":cards:working")`, "redis.call('ZCARD', 'bench:' .. b .. ':cards:ok')"} {
		if len(goTableWrites("cmd/x.go", line)) != 0 || len(luaTableWrites("x.lua", line)) != 0 {
			t.Errorf("the rule refused a read: %s", line)
		}
	}
}
