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
//	bench|friend:<n>:living|starting  the old lease ledgers, folded into
//	                                  <consumer>:cards:working (#3877, #3998)
//
// A ZADD, ZREM, SADD, SREM, SMOVE (or a pop, range removal, store, DEL,
// UNLINK or RENAME) of one of them in any other Lua file, or any Go write of
// one outside a fixture, fails here. The old ledgers' allowlist is empty
// (legacyLedgerWriters, #3998: the fold landed and the ratchet reached zero),
// so any write of one anywhere fails, and TestNoOldLeaseLedgerLeft fails a
// read of one in any Lua or non-test Go file.

// legacyLedgerWriters are the Lua writes of the old lease ledgers allowed
// per file: none (#3998).
var legacyLedgerWriters = map[string]int{}

// knownTableWriters are table-set writes outside the move file the rule
// tolerates, per file, a ratchet that only goes down: the stream lander's
// standalone EVAL script (internal/nsprint/land/stream/land_stream.lua,
// swept since nova-tools#4238) moves a member between ws sets itself. The
// follow-up folds that move into ns_tcard_land_stream and lowers this to 0.
var knownTableWriters = map[string]int{"land/stream/land_stream.lua": 2}

// theMoveFile is the one writer.
const theMoveFile = "02_card_move.lua"

var (
	tmLuaWrite = regexp.MustCompile(`redis\.p?call\('(ZADD|ZREM|SADD|SREM|SMOVE|ZPOPMIN|ZPOPMAX|ZREMRANGEBYSCORE|` +
		`ZREMRANGEBYRANK|ZUNIONSTORE|ZINTERSTORE|ZRANGESTORE|DEL|UNLINK|RENAME)',\s*([^,)]+)`)
	// a table set is named by a literal ('ws:' .., 'bench:' .. ':cards:'), by
	// the epoch-keyed helpers of 02_card_move.lua (NS.card.ckey|wskey, a
	// file's CARD alias, cm_ckey|cm_wskey), by W.key, DF.key, TM.key, or by
	// the lander script's own wskey (nova-tools#4238)
	tmLuaHelper = `(NS\.card\.|CARD\.|cm_)(ckey|wskey)\(|(W|DF|TM)\.key\(|wskey\(`
	tmLuaTable  = regexp.MustCompile(`^('ws:'\s*\.\.|` + tmLuaHelper + `|'(bench|friend):'\s*\.\..*':cards:)`)
	tmLuaLedger = regexp.MustCompile(`^'(bench|friend):'\s*\.\..*':(living|starting)'`)
	tmLuaBind   = regexp.MustCompile(`local\s+(\w+)\s*=\s*('ws:'\s*\.\..*|(` + tmLuaHelper + `).*|'(bench|friend):'\s*\.\..*':cards:.*|'(bench|friend):'\s*\.\..*':(living|starting)'.*)$`)

	tmGoWrite = regexp.MustCompile(`\.(ZAdd|ZAddNX|ZAddXX|ZAddArgs|ZIncrBy|ZRem|ZRemRangeByScore|ZRemRangeByRank|ZUnionStore|` +
		`ZInterStore|ZPopMin|ZPopMax|SAdd|SRem|SMove|Del|Unlink|Rename|RenameNX)\(ctx, ([^,)]+)`)
	// a Go key is a literal, a retired helper (kept so a resurrected one is
	// caught) or one of the epoch-keyed helpers of internal/nsprint/ws/epoch.go
	// and their package twins (nova-tools#4238)
	tmGoKey = regexp.MustCompile(`^("ws:"\s*\+|"(bench|friend):"\s*\+.*":cards:|"(bench|friend):"\s*\+.*":(living|starting)"|` +
		`(\w+\.)?(BenchStartingKey|BenchLivingKey|FriendCardsKey|StreamKey|FriendKey|WSKey|BenchCardsKey|BenchWorkingKey|PoolViewKey|` +
		`KeyAt|ConsumerKeyAt|StreamKeyAt|FriendKeyAt|BenchCardsKeyAt|BenchWorkingKeyAt|WSKeyAt|FriendCardsKeyAt|PoolViewKeyAt)\(|` +
		`\w+\.Key\(("(ready|working|ok|fail)"|col)|\w+\.KeyAt\()`)
	tmGoRaw = regexp.MustCompile(`"(ZADD|ZREM|SADD|SREM|SMOVE|DEL|RENAME)", "(ws:|(bench|friend):[^"]*:cards:|(bench|friend):[^"]*:(living|starting))`)
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
	// the stream lander's standalone script, outside the library (#4238)
	landDir := filepath.Join(repoRoot(t), "internal", "nsprint", "land", "stream")
	ents, err = os.ReadDir(landDir)
	if err != nil {
		t.Fatal(err)
	}
	swept := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".lua") {
			b, err := os.ReadFile(filepath.Join(landDir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out["land/stream/"+e.Name()] = string(b)
			swept++
		}
	}
	if swept == 0 {
		t.Fatalf("read no Lua from %s: the lander's script is not swept", landDir)
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
	known := map[string]int{}
	for _, n := range names {
		for _, w := range luaTableWrites(n, srcs[n]) {
			if w.Ledger {
				ledgers[n]++
				if ledgers[n] <= legacyLedgerWriters[n] {
					continue
				}
			} else {
				known[n]++
				if known[n] <= knownTableWriters[n] {
					continue
				}
			}
			bad = append(bad, w.At)
		}
	}
	for n, allowed := range knownTableWriters {
		if known[n] < allowed {
			t.Errorf("%s now writes a table set %d times, allowed %d: lower knownTableWriters[%q] to %d (the ratchet only goes down)",
				n, known[n], allowed, n, known[n])
		}
	}
	for n, allowed := range legacyLedgerWriters {
		if ledgers[n] < allowed {
			t.Errorf("%s now writes an old lease ledger (living|starting) %d times, allowed %d: lower legacyLedgerWriters[%q] to %d (the ratchet only goes down)",
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
		"redis.call('ZREM', 'friend:' .. f .. ':starting', member)",
		"redis.call('SREM', TM.key(c, 'ok'), id)",
		"local k = 'bench:' .. b .. ':cards:working'\n  redis.call('ZADD', k, 1, id)",
		// the epoch-keyed names (nova-tools#4238)
		"redis.call('ZADD', NS.card.ckey(e, 'bench:' .. b, 'ready'), 1, id)",
		"redis.call('ZREM', CARD.wskey(e, s, 'waiting'), id)",
		"redis.call('ZADD', cm_ckey(e, 'friend:' .. f, 'working'), 1, id)",
		"redis.call('ZADD', DF.key(s, 'ready'), 1, id)",
		"redis.call('ZADD', wskey(s, 'merging'), 1, id)",
		"redis.call('ZADD', 'ws:' .. e .. ':' .. s .. ':waiting', 1, id)",
		"local k = NS.card.ckey(e, c, 'working')\n  redis.call('ZADD', k, 1, id)",
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
		`c.ZAdd(ctx, "friend:"+f+":living", redis.Z{Score: 1, Member: member})`,
		`c.ZAdd(ctx, k.Key("ok"), redis.Z{Score: 1, Member: id})`,
		`{"ZADD", "bench:b:cards:ready", "1", "x"},`,
		// the epoch-keyed names (nova-tools#4238)
		`c.ZAdd(ctx, ws.ConsumerKeyAt(e, "bench:"+b, "working"), redis.Z{Score: 1, Member: id})`,
		`pipe.ZRem(ctx, ws.KeyAt(e, s, "ready"), id)`,
		`c.ZAdd(ctx, k.KeyAt(e, "ok"), redis.Z{Score: 1, Member: id})`,
		`c.ZAdd(ctx, taskcard.StreamKeyAt(e, s, "waiting"), redis.Z{Score: 1, Member: id})`,
		`c.ZAdd(ctx, "bench:"+b+":"+e+":cards:working", redis.Z{Score: 1, Member: id})`,
		`{"ZADD", "bench:b:1:cards:ready", "1", "x"},`,
		`{"ZADD", "ws:1:s:ready", "1", "x"},`,
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

// oldLedgerKey is any spelling of the old lease ledger keys in code: a Lua or
// Go string ending ':living' or ':starting' after a bench or friend root.
var oldLedgerKey = regexp.MustCompile(`(bench|friend)[^'"\n]*['"]\s*(\.\.|\+)\s*[^'"\n]*['"]:(living|starting)['"]|['"](bench|friend):[^'"\s]*:(living|starting)['"]`)

// mayNameOldLedger is oldLedgerKey's cheap necessary condition: every match
// carries ":living" or ":starting". Text without either is not handed to the
// regexp (nova-tools#4328).
func mayNameOldLedger(s string) bool {
	return strings.Contains(s, ":living") || strings.Contains(s, ":starting")
}

// TestNoOldLeaseLedgerLeft (#3998): nothing reads or writes
// bench|friend:<n>:living|starting any more, in the Lua library or in any
// non-test Go file; the width in use is ZCARD <consumer>:cards:working.
func TestNoOldLeaseLedgerLeft(t *testing.T) {
	t.Parallel()
	var bad []string
	for n, src := range luaSources(t) {
		for i, line := range strings.Split(src, "\n") {
			code := line
			if j := strings.Index(code, "--"); j >= 0 {
				code = code[:j]
			}
			if mayNameOldLedger(code) && oldLedgerKey.MatchString(code) {
				bad = append(bad, n+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	tree := repoTree(t)
	for _, dir := range []string{"cmd", "internal"} {
		for _, src := range tree.GoFilesUnder(false, dir) {
			if !mayNameOldLedger(string(src.Src)) {
				continue
			}
			for i, line := range strings.Split(string(src.Src), "\n") {
				if oldLedgerKey.MatchString(line) {
					bad = append(bad, src.Rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("an old lease ledger key (folded into <consumer>:cards:working, #3998): %s", b)
	}
	for _, line := range []string{
		"redis.call('ZCARD', 'friend:' .. f .. ':starting')",
		`pipe.ZCard(ctx, "bench:"+b+":living")`,
		`{"ZADD", "friend:eight:living", "1", "l1"},`,
	} {
		if !oldLedgerKey.MatchString(line) {
			t.Errorf("the rule missed an old ledger key: %s", line)
		}
	}
	if oldLedgerKey.MatchString(`pipe.ZCard(ctx, "bench:"+b+":cards:working")`) {
		t.Error("the rule refused the one ledger")
	}
}
