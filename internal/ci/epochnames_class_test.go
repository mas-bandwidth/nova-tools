package ci

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The epoch name rule (nova-tools#4238; Glenn 2026-09-26 9:25 AM ET: "have a
// uint64 sequence number that increments, thus, if the numbers from the
// async things are not the same sequence as current table view, the display
// is zero"). Every set behind a table is named by the sprint epoch it was
// written under:
//
//	epoch 0  <consumer>:cards:<col>      ws:<s>:<w>      s:<S>:<pool|waiting>
//	epoch e  <consumer>:<e>:cards:<col>  ws:<e>:<s>:<w>  s:<S>:<e>:<pool|waiting>
//
// and the rule is spelled in exactly three places: internal/nsprint/ws
// (KeyAt, ConsumerKeyAt, SprintListAt, key0), fn/lua/02_card_move.lua
// (cm_ckey, cm_wskey, cm_skey, exported as NS.card.ckey|wskey|skey) and the
// stream lander's standalone script (land_stream.lua's wskey, outside the
// library). A literal name anywhere else reads or writes epoch 0 whatever
// the epoch is: after the first clear the dealer saw only old cards and the
// probe guard was off (the 4/10 read of #4377). The retired epoch-0 helpers
// stay named here so a resurrected one is caught before the compiler would
// miss a new spelling.

// epochRuleLines are the only code lines that may spell a table set's name,
// each the rule itself; a row whose line is gone fails, so the list only
// shrinks.
var epochRuleLines = map[string][]string{
	"internal/nsprint/ws/ws.go": {
		`func key0(stream, state string) string { return "ws:" + stream + ":" + state }`,
	},
	"internal/nsprint/ws/epoch.go": {
		`return "ws:" + strconv.FormatUint(e, 10) + ":" + stream + ":" + state`,
		`return consumer + ":cards:" + col`,
		`return consumer + ":" + strconv.FormatUint(e, 10) + ":cards:" + col`,
	},
	"02_card_move.lua": {
		`if s == '0' then return c .. ':cards:' .. col end`,
		`return c .. ':' .. s .. ':cards:' .. col`,
		`if es == '0' then return 'ws:' .. s .. ':' .. w end`,
		`return 'ws:' .. es .. ':' .. s .. ':' .. w`,
	},
	"land/stream/land_stream.lua": {
		`if not e or e == '' or e == '0' then return 'ws:' .. stream .. ':' .. w end`,
		`return 'ws:' .. e .. ':' .. stream .. ':' .. w`,
	},
}

var (
	// a retired epoch-0 helper, by name (Consumer.Key is its method form)
	epochRetired = regexp.MustCompile(`\b(StreamKey|FriendKey|BenchCardsKey|BenchWorkingKey|WSKey|FriendCardsKey|PoolViewKey|keyPool|keyWaiting)\(|` +
		`\bws\.Key\(|func \(\w+ \*?Consumer\) Key\(`)
	// a consumer's set: a literal ':cards:' piece or a bench|friend literal through it
	epochCards = regexp.MustCompile(`(bench|friend):[^"']*:cards:|":cards:|':cards:`)
	// a stream's set: a literal ending ws: concatenated, formatted, or a
	// two-segment ws literal (a fixture's "Z ws:"+s spelling included)
	epochWS = regexp.MustCompile(`\bws:"\s*\+|\bws:%|\bws:[^":\s]+:[^"\s]+"|\bws:'\s*\.\.|\bws:[^':\s]+:[^'\s]+'`)
	// a sprint's dealer list, s:<S>:pool|waiting
	epochList = regexp.MustCompile(`s:"\s*\+.*":(pool|waiting)"|"s:[^"\s]*:(pool|waiting)"|s:'\s*\.\..*':(pool|waiting)'|'s:[^'\s]*:(pool|waiting)'`)
)

// epochNameHits is every line of src (file n, Go or Lua) that spells a table
// set's name outside the rule; comments are not code.
func epochNameHits(n, src string, lua bool) []string {
	var out []string
	for i, line := range strings.Split(src, "\n") {
		code := strings.TrimSpace(line)
		if lua {
			if j := strings.Index(code, "--"); j >= 0 {
				code = strings.TrimSpace(code[:j])
			}
		} else if strings.HasPrefix(code, "//") {
			continue
		} else if j := strings.Index(code, " // "); j >= 0 {
			code = strings.TrimSpace(code[:j])
		}
		if code == "" || !strings.Contains(code, ":") && !strings.Contains(code, "Key") && !strings.Contains(code, "key") {
			continue
		}
		if !epochRetired.MatchString(code) && !epochCards.MatchString(code) && !epochWS.MatchString(code) && !epochList.MatchString(code) {
			continue
		}
		allowed := false
		for _, a := range epochRuleLines[n] {
			if code == a {
				allowed = true
				break
			}
		}
		if !allowed {
			out = append(out, n+":"+strconv.Itoa(i+1)+": "+code)
		}
	}
	return out
}

// TestEveryTableSetIsNamedByTheEpochRule (#4238): no Lua file of the library
// or the lander, and no non-test Go file under cmd/ or internal/ (fixtures
// included: they seed through ws.KeyAt(0, ...) and its twins), names a table
// set but through the rule, and no retired epoch-0 helper is back.
func TestEveryTableSetIsNamedByTheEpochRule(t *testing.T) {
	t.Parallel()
	var bad []string
	seen := map[string]string{}
	for n, src := range luaSources(t) {
		seen[n] = src
		bad = append(bad, epochNameHits(n, src, true)...)
	}
	tree := repoTree(t)
	for _, dir := range []string{"cmd", "internal"} {
		for _, src := range tree.GoFilesUnder(false, dir) {
			s := string(src.Src)
			seen[src.Rel] = s
			bad = append(bad, epochNameHits(src.Rel, s, false)...)
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("a table set named outside the epoch rule (name it through ws.KeyAt|ConsumerKeyAt|SprintListAt, or NS.card.ckey|wskey|skey, keyed by the epoch; nova-tools#4238): %s", b)
	}
	for n, lines := range epochRuleLines {
		src, ok := seen[n]
		if !ok {
			t.Errorf("epochRuleLines names %s, which the sweep did not read: delete the row", n)
			continue
		}
		for _, l := range lines {
			found := false
			for _, got := range strings.Split(src, "\n") {
				if strings.TrimSpace(got) == l {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("epochRuleLines[%q] holds %q, no longer in the file: delete the row (the list only shrinks)", n, l)
			}
		}
	}
}

// TestEpochNameRuleCatchesAnInjectedName: every spelling the #4377 read found
// (and the retired helpers) is caught in a file the rule does not allow, and
// the rule itself, the epoch-keyed calls and the global ws keys are left alone.
func TestEpochNameRuleCatchesAnInjectedName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line string
		lua  bool
	}{
		{"for _, id in ipairs(redis.call('ZRANGE', 'bench:' .. b .. ':cards:working', 0, -1)) do", true},
		{"local fk = 'friend:' .. f .. ':cards:working'", true},
		{"return c .. ':cards:' .. col", true},
		{"local k = 'ws:' .. s .. ':' .. w", true},
		{"redis.call('ZCARD', 'ws:swarm:ready')", true},
		{"local pool = 's:' .. S .. ':pool'", true},
		{"redis.call('SMEMBERS', 's:fix:waiting')", true},
		{`working := pipe.ZCard(ctx, "bench:"+b+":cards:working")`, false},
		{`{"ZADD", "friend:eight:cards:working", "1", "x"},`, false},
		{`k := consumer + ":cards:" + col`, false},
		{`pipe.ZRange(ctx, "ws:"+s+":ready", 0, -1)`, false},
		{`key := fmt.Sprintf("ws:%s:%s", s, w)`, false},
		{`{"ZADD", "ws:s1:ready", "1", "x"},`, false},
		{`c.SMembers(ctx, "s:"+sprint+":waiting")`, false},
		{`add("Z s:"+sprint+":pool", created, id)`, false},
		{`add("Z ws:"+stream+":"+where, created, id)`, false},
		{`k := fmt.Sprintf("bench:%s:cards:%s", b, col)`, false},
		{`{"ZADD", "s:s1:pool", "0", "pool-card"},`, false},
		{`n := c.ZCard(ctx, card.BenchCardsKey(b, "ready"))`, false},
		{`pipe.ZRange(ctx, ws.Key(s, "ready"), 0, -1)`, false},
		{`func (c Consumer) Key(col string) string {`, false},
		{`return client.SMembers(ctx, keyWaiting(sprint))`, false},
	} {
		if got := epochNameHits("internal/nsprint/evil/evil.go", c.line, c.lua); len(got) != 1 {
			t.Errorf("the rule missed a literal table-set name (lua=%t): %s", c.lua, c.line)
		}
	}
	for _, c := range []struct {
		line string
		lua  bool
	}{
		{"local k = NS.card.ckey(e, 'bench:' .. b, 'working')", true},
		{"redis.call('XADD', 'ws:log', '*', 'id', id)", true},
		{"-- ws:<s>:<w> and bench:<b>:cards:<col> are named by the rule", true},
		{`pipe.ZCard(ctx, ws.ConsumerKeyAt(epoch, "bench:"+b, "working"))`, false},
		{`c.SMembers(ctx, ws.SprintListAt(epoch, sprint, "waiting"))`, false},
		{`order := pipe.ZRange(ctx, "ws:order", 0, -1)`, false},
		{`const CheckpointKey = "ws:checkpoint"`, false},
		{`// bench:<b>:cards:working, the one lease ledger`, false},
		{`func WaitingKey(repo, head string) string { return "ci:" + repo + ":" + head + ":waiting" }`, false},
	} {
		if got := epochNameHits("internal/nsprint/evil/evil.go", c.line, c.lua); len(got) != 0 {
			t.Errorf("the rule refused a name through the rule or a key that is not a table set: %v", got)
		}
	}
	for n, lines := range epochRuleLines {
		for _, l := range lines {
			if got := epochNameHits(n, l, strings.HasSuffix(n, ".lua")); len(got) != 0 {
				t.Errorf("the rule refused its own spelling in %s: %v", n, got)
			}
			if got := epochNameHits("internal/nsprint/evil/evil.go", l, strings.HasSuffix(n, ".lua")); len(got) != 1 {
				t.Errorf("the rule's own line %q is not caught outside %s", l, n)
			}
		}
	}
}
