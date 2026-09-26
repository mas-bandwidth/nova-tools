package fn

import (
	"regexp"
	"strings"
	"testing"
)

// Probes never ride the consumer sets (nova-tools#4237). Seen 2026-09-26
// 8:45-9:20 AM ET: after sprint clear zeroed both tables, a fleet roll's
// per-bench probe cards retired into bench:<b>:cards:ok and :fail and every
// bench row showed done 2 again. A probe's result goes on the bench beat
// (bench:<b>:beat probe); a record naming a probe is a kind the consumer
// sets <bench|friend>:<name>:cards:<set> refuse. The structure that holds it
// is one ZADD: every add into a table set in 02_card_move.lua is cm_zadd,
// which raises PROBE, and every move that enters a consumer set refuses a
// probe before it writes. The other lua/ files add through NS.card.add
// (TestTableSetAddsGoThroughTheOneMove), which is card_add, which refuses.

// rawZaddKeys are the key expressions 02_card_move.lua may ZADD into with a
// raw redis.call: cm_zadd's own, and keys that are not table sets. Any other
// raw ZADD fails the class test: add through cm_zadd.
var rawZaddKeys = map[string]string{
	"k":                          "cm_zadd itself: the one ZADD into a table set, which raises PROBE",
	"'ws:order'":                 "the streams' rank, not a table set",
	"e.k":                        "cm_add's pool branch: s:<S>:pool holds labels, not ids",
	"'sprint:' .. S .. ':cards'": "the sprint's roster, not a table set",
	"all":                        "card fsck re-scoring the roster sprint:<S>:cards",
	"q(nxt.friend)":              "the sprint ready queue s:<S>:open:<f>, not a table set",
	"'q:blocked'":                "the blocked index, not a table set",
}

// probeGates are the moves that can put an id into a consumer set for the
// first time, each with what its body must call to refuse a probe before
// any write.
var probeGates = map[string]string{
	"local function card_add(":    "cm_probe_refused(",
	"local function card_move(":   "cm_probe_refused(",
	"local function card_create(": "'probe'",
	"function TK.create(":         "'probe'",
	"function TK.move(":           "cm_probe_refused(",
	"function TM.cut(":            "cm_probe_refused(",
	"local function cm_zadd(":     "error(why)",
}

var rawZadd = regexp.MustCompile(`redis\.p?call\(\s*'ZADD'\s*,`)

// zaddKey is the first argument of each raw ZADD in src (comments
// stripped): the text up to the first comma outside parentheses.
func zaddKeys(src string) []string {
	src = luaLineNotes.ReplaceAllString(src, "")
	var out []string
	for _, loc := range rawZadd.FindAllStringIndex(src, -1) {
		rest, depth, end := src[loc[1]:], 0, -1
		for i, r := range rest {
			switch r {
			case '(':
				depth++
			case ')':
				if depth == 0 {
					end = i
				}
				depth--
			case ',':
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			end = len(rest)
		}
		out = append(out, strings.TrimSpace(rest[:end]))
	}
	return out
}

// luaBody is the source of the function whose header line starts with
// head, to its closing end at column 0, comments stripped (a commented-out
// refusal is no refusal); "" when there is none.
func luaBody(src, head string) string {
	src = luaLineNotes.ReplaceAllString(src, "")
	i := strings.Index(src, "\n"+head)
	if i < 0 {
		return ""
	}
	body := src[i+1:]
	if j := strings.Index(body, "\nend\n"); j >= 0 {
		return body[:j]
	}
	return body
}

// TestConsumerSetsRefuseProbes is the class test: no raw ZADD into a table
// set in 02_card_move.lua (every one is cm_zadd, which raises PROBE), and
// every move that enters a consumer set refuses a probe before it writes.
func TestConsumerSetsRefuseProbes(t *testing.T) {
	t.Parallel()

	b, err := sources.ReadFile(tableSetAddFile)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	used := map[string]bool{}
	for _, k := range zaddKeys(src) {
		if _, ok := rawZaddKeys[k]; !ok {
			t.Errorf("%s: redis.call('ZADD', %s, ...) is a raw add; add through cm_zadd, which refuses a probe entering a consumer set (nova-tools#4237)", tableSetAddFile, k)
		}
		used[k] = true
	}
	for k := range rawZaddKeys {
		if !used[k] {
			t.Errorf("%s no longer ZADDs into %s: delete its rawZaddKeys entry", tableSetAddFile, k)
		}
	}
	for head, call := range probeGates {
		body := luaBody(src, head)
		if body == "" {
			t.Errorf("%s: no %s...; the probe gate moved: name the new one in probeGates", tableSetAddFile, head)
			continue
		}
		if !strings.Contains(body, call) {
			t.Errorf("%s: %s... does not refuse a probe (%s); a probe entering a consumer set is refused before any write (nova-tools#4237)", tableSetAddFile, head, call)
		}
	}
	// the probe's own place: the bench beat keeps the fleet's result
	pb, err := sources.ReadFile("lua/presence.lua")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(luaLineNotes.ReplaceAllString(string(pb), ""), "'probe', probe or ''") {
		t.Error("lua/presence.lua: the bench beat blanks probe every beat; the fleet's probe result would not survive one")
	}
}

// TestConsumerSetsRefuseProbesGuardSeesTheShapes is the guard's own control.
func TestConsumerSetsRefuseProbesGuardSeesTheShapes(t *testing.T) {
	t.Parallel()

	for src, want := range map[string]string{
		"redis.call('ZADD', TM.key(c, 'ok'), at, id)":                            "TM.key(c, 'ok')",
		"redis.call('ZADD', 'bench:' .. b .. ':cards:' .. w, age, id)":           "'bench:' .. b .. ':cards:' .. w",
		"redis.pcall('ZADD', k, 'NX', created, id)":                              "k",
		"redis.call('ZADD', 'ws:order', redis.call('ZCARD', 'ws:order') + 1, s)": "'ws:order'",
	} {
		got := zaddKeys(src)
		if len(got) != 1 || got[0] != want {
			t.Errorf("zaddKeys(%q) = %q, want [%q]", src, got, want)
		}
	}
	for _, src := range []string{
		"cm_zadd(TM.key(c, 'ready'), created, cid)",
		"-- redis.call('ZADD', TM.key(c, 'ok'), at, id)",
	} {
		if got := zaddKeys(src); len(got) != 0 {
			t.Errorf("zaddKeys(%q) = %q, want none", src, got)
		}
	}
	src := "x\nfunction TM.cut(c)\n  return cm_probe_refused(k, id)\nend\nfunction TM.deal(c)\nend\n"
	if b := luaBody(src, "function TM.cut("); !strings.Contains(b, "cm_probe_refused(") || strings.Contains(b, "TM.deal") {
		t.Errorf("luaBody = %q", b)
	}
	// a commented-out refusal is no refusal
	src = "x\nlocal function cm_zadd(k, s, id)\n  -- error(why)\n  return redis.call('ZADD', k, s, id)\nend\n"
	if b := luaBody(src, "local function cm_zadd("); strings.Contains(b, "error(why)") {
		t.Errorf("luaBody kept a commented-out refusal: %q", b)
	}
}
