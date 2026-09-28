package fn

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The table sets have one add (nova-tools#4054). Found 2026-09-25: a
// friend's ready set held 32 members that were no record at all (a file
// path glued onto a task id by a grep in an import pipe) and the friend
// table counted them for hours. Every add into a set that drives a table
// (ws:<stream>:<where>, bench:<b>:cards:*, friend:<f>:cards:*) is
// card_add in 02_card_move.lua (NS.card.add elsewhere), which refuses an id
// with no record; a raw ZADD into one of them in any other file bypasses
// that check.

// tableSetAddFile is the file that owns the one add.
const tableSetAddFile = "lua/02_card_move.lua"

// tableSetAddAllowed are the files that still ZADD into a ws set directly,
// each with the reason; the next PR on that path moves its add onto
// NS.card.add and deletes the entry. Empty: ws.lua and task_batch.lua move
// tasks through NS.task.move since #3778.
var tableSetAddAllowed = map[string]string{}

// tableSetZadd is a ZADD whose key expression names a table set: a ws:
// key that is not ws:order, a :cards: key, or the ws key helpers of
// deal_friend.lua (DF.key) and ws.lua (W.key).
var (
	zaddCall     = regexp.MustCompile(`redis\.p?call\(\s*'ZADD'\s*,([^\n]*)`)
	tableSetKey  = regexp.MustCompile(`'ws:'\s*\.\.|':cards:'|'friend:'[^,]*':cards|\bDF\.key\(|\bW\.key\(`)
	notTableSet  = regexp.MustCompile(`^\s*'ws:order'`)
	luaLineNotes = regexp.MustCompile(`--[^\n]*`)
)

func tableSetZadds(src string) []string {
	var out []string
	for _, m := range zaddCall.FindAllStringSubmatch(luaLineNotes.ReplaceAllString(src, ""), -1) {
		if tableSetKey.MatchString(m[1]) && !notTableSet.MatchString(m[1]) {
			out = append(out, strings.TrimSpace(m[0]))
		}
	}
	return out
}

// TestTableSetAddsGoThroughTheOneMove fails when a lua/ file other than
// 02_card_move.lua (and the named, owed exceptions) ZADDs into a ws, bench
// or friend table set directly.
func TestTableSetAddsGoThroughTheOneMove(t *testing.T) {
	t.Parallel()

	names, err := fs.Glob(sources, "lua/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == tableSetAddFile {
			continue
		}
		src, err := sources.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		adds := tableSetZadds(string(src))
		if _, owed := tableSetAddAllowed[name]; owed {
			if len(adds) == 0 {
				t.Errorf("%s no longer ZADDs into a table set: delete its tableSetAddAllowed entry", name)
			}
			continue
		}
		for _, a := range adds {
			t.Errorf("%s: %s adds into a table set directly; add through NS.card.add (02_card_move.lua), which refuses an id with no record", name, a)
		}
	}
}

// TestTableSetAddGuardSeesTheShapes is the guard's own control.
func TestTableSetAddGuardSeesTheShapes(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"redis.call('ZADD', 'ws:' .. stream .. ':ready', at, id)",
		"redis.call('ZADD', DF.key(stream, 'working'), age, id)",
		"redis.call('ZADD', 'friend:' .. f .. ':cards:ready', age, id)",
		"redis.call('ZADD', 'bench:' .. b .. ':cards:' .. w, age, id)",
	} {
		if len(tableSetZadds(bad)) != 1 {
			t.Errorf("guard misses %q", bad)
		}
	}
	for _, ok := range []string{
		"redis.call('ZADD', 'ws:order', rank, stream)",
		"CARD.add('ws:' .. stream .. ':ready', at, id)",
		"redis.call('ZADD', 's:' .. S .. ':pool', priority, label)",
		"-- redis.call('ZADD', 'ws:' .. stream .. ':ready', at, id)",
	} {
		if got := tableSetZadds(ok); len(got) != 0 {
			t.Errorf("guard flags %q: %v", ok, got)
		}
	}
}
