package stepbuild

import (
	"fmt"
	"strings"
	"testing"
)

// The planned argv bytes are an upper bound over Layer 1's own command layout
// (layout.go): a step the builder emitted is never refused LIMIT at prepare
// for its argv. These tests hold that against the layout as Layer 1 lays it
// out (the tset-l1 branch at 72b425b6d, table_set.lua), by a counter written apart
// from the builder's (helpers_test.go: strictArgv, which builds every command
// as real strings), at the worst case of every kind of entry.

// The layout table: every row names the section that fixes it and the text
// it spells, and the literal bytes each row carries are the ones typed here
// from Layer 1's own spelling, never taken from the table.
func TestTheLayoutRowsAreTheContractsAndLayerOnesSpelling(t *testing.T) {
	t.Parallel()
	for _, r := range []struct {
		slot  slot
		fixed int
		why   string
	}{
		{cmdCreateHSET, len("HSET") + len("revision") + len("epoch") + len("place:"), `HSET rec revision r epoch e place:<t> <cell>`},
		{cmdMoveHSET, len("HSET") + len("revision") + len("place:"), `HSET rec revision r place:<t> <cell>`},
		{cmdRemoveHSET, len("HSET") + len("revision"), `HSET rec revision r`},
		{cmdHDEL, len("HDEL"), `HDEL rec <unset>... place:<t>`},
		{cmdZREM, len("ZREM"), `ZREM cell id`},
		{cmdZADD, len("ZADD"), `ZADD cell score id`},
		{cmdRPUSH, len("RPUSH"), `RPUSH history seq`},
		{cmdXADD, len("XADD") + len("9007199254740991-0") + 16, `XADD log <seq>-0 <field> line`},
		{cmdRowsZADD, len("ZADD"), `ZADD rows rank row`},
		{cmdRowsZREM, len("ZREM"), `ZREM rows row`},
		{cmdDoneHSET, len("HSET"), `HSET done op receipt`},
		{keyRecord, 0, `<prefix><id>`},
		{keyCell, len("table:") + len(":") + len(":cell:"), `<ns>table:<t>:<e>:cell:<row>:<col>`},
		{keyRows, len("table:") + len(":") + len(":rows"), `<ns>table:<t>:<e>:rows`},
		{keyLog, len("sprint:log@"), `<ns>sprint:log@<e>`},
		{keyHistory, len("sprint:cl:") + len("@"), `<ns>sprint:cl:<about>@<e>`},
		{keyDone, len("sprint:done@"), `<ns>sprint:done@<e>`},
	} {
		got := layout[r.slot]
		if got.fixed != r.fixed {
			t.Errorf("%s: %d literal bytes, Layer 1 spells %d (%s)", got.what, got.fixed, r.fixed, r.why)
		}
		if got.what == "" || got.spelled == "" || got.section == "" {
			t.Errorf("row %d of the layout lacks its name, its text or its section: %+v", r.slot, got)
		}
		if !strings.Contains(got.section, "1.") && !strings.Contains(got.section, "3") && !strings.Contains(got.section, "5") {
			t.Errorf("%s: no section of the contract beside it: %q", got.what, got.section)
		}
		// A line of Layer 1's Lua is a line of one commit, and the row names it: the
		// commit the layout was read at, and every row of it.
		if strings.Contains(got.section, "table_set") && strings.Count(got.section, "72b425b6d") != 1 {
			t.Errorf("%s: cites Layer 1's Lua without the commit it was read at (72b425b6d): %q", got.what, got.section)
		}
		if strings.Contains(got.section, "e269dd7aa") {
			t.Errorf("%s: cites the commit the layout is no longer read at: %q", got.what, got.section)
		}
	}
	if int(slots) != 17 {
		t.Errorf("the layout has %d rows: a row is a command or a key of Layer 1's plan, and each is checked above", slots)
	}
}

// One member of each kind, counted from the argv table_set.lua:445-456 at 72b425b6d builds
// for it, spelled out here command by command. The strict count is held to it:
// it is Layer 1's layout, and the model is held to the strict count.
func TestTheStrictCountIsLayerOnesLayoutCommandByCommand(t *testing.T) {
	t.Parallel()
	const ns, ep, prefix, rev, seq = "sprint", "1", "member:", "99999999999999999999", "9007199254740991"
	score := strings.Repeat("9", 24)
	for _, tc := range []struct {
		name  string
		entry Entry
		want  int // the argv of the commands table_set.lua plans for the member, bytes
	}{
		{
			// HSET rec revision rev epoch ep place:t dest f v; ZADD cell score id
			"a create",
			Entry{Kind: KindCreate, Table: "work", To: "row:col", IDs: []string{"m"}, Scores: []string{score}, Set: map[string]string{"f": "v"}},
			lenOf("HSET", prefix+"m", "revision", rev, "epoch", ep, "place:work", "row:col", "f", "v") +
				lenOf("ZADD", ns+"table:work:1:cell:row:col", score, "m"),
		},
		{
			// HSET rec revision rev place:t dest f v; ZREM old id; ZADD new score id
			"a move",
			Entry{Kind: KindMove, Table: "work", From: "a:x", To: "b:y", IDs: []string{"m"}, Set: map[string]string{"f": "v"}},
			lenOf("HSET", prefix+"m", "revision", rev, "place:work", "b:y", "f", "v") +
				lenOf("ZREM", ns+"table:work:1:cell:a:x", "m") +
				lenOf("ZADD", ns+"table:work:1:cell:b:y", score, "m"),
		},
		{
			// a member that stays: dest is its own cell, the placement still written
			"a member that stays",
			Entry{Kind: KindMove, Table: "work", From: "a:x", IDs: []string{"m"}, Set: map[string]string{"f": "v"}},
			lenOf("HSET", prefix+"m", "revision", rev, "place:work", "a:x", "f", "v") +
				lenOf("ZREM", ns+"table:work:1:cell:a:x", "m") +
				lenOf("ZADD", ns+"table:work:1:cell:a:x", score, "m"),
		},
		{
			// HDEL rec unsets
			"a move that unsets",
			Entry{Kind: KindMove, Table: "work", From: "a:x", To: "b:y", IDs: []string{"m"}, Unset: []string{"gone"}},
			lenOf("HSET", prefix+"m", "revision", rev, "place:work", "b:y") +
				lenOf("HDEL", prefix+"m", "gone") +
				lenOf("ZREM", ns+"table:work:1:cell:a:x", "m") +
				lenOf("ZADD", ns+"table:work:1:cell:b:y", score, "m"),
		},
		{
			// a remove: no placement in the HSET, the placement deleted by the HDEL
			// beside the unsets, and no ZADD
			"a remove",
			Entry{Kind: KindRemove, Table: "work", From: "a:x", IDs: []string{"m"}, Set: map[string]string{"retired": "yes"}},
			lenOf("HSET", prefix+"m", "revision", rev, "retired", "yes") +
				lenOf("HDEL", prefix+"m", "place:work") +
				lenOf("ZREM", ns+"table:work:1:cell:a:x", "m"),
		},
		{
			"a remove that unsets",
			Entry{Kind: KindRemove, Table: "work", From: "a:x", IDs: []string{"m"}, Unset: []string{"gone"}},
			lenOf("HSET", prefix+"m", "revision", rev) +
				lenOf("HDEL", prefix+"m", "gone", "place:work") +
				lenOf("ZREM", ns+"table:work:1:cell:a:x", "m"),
		},
	} {
		c := cfg()
		c.MemberPrefixBytes = len(prefix)
		steps := must(t, c, []Entry{tc.entry})
		if len(steps) != 1 {
			t.Fatalf("%s: %d steps", tc.name, len(steps))
		}
		req := decode(t, steps[0].Encode())
		ls := lineSizes{entries: []int{modelLine(t, req.Entries[0])}}
		// The step's log line is one XADD: the log key, the stream ID, a field
		// and the line; every other byte is the member's.
		xadd := lenOf("XADD", ns+"sprint:log@"+ep, seq+"-0", "line") + ls.entries[0]
		m := measureKeys(t, steps[0].Encode(), keyCfg{len(prefix)})
		if got := m.strict - xadd; got != tc.want {
			t.Errorf("%s: the strict count of the member's commands is %d, table_set.lua's argv is %d", tc.name, got, tc.want)
		}
		if m.strict > m.argv {
			t.Errorf("%s: the strict count %d is over the model's %d", tc.name, m.strict, m.argv)
		}
	}
}

func lenOf(ss ...string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

// The widest a name goes: a table, an ID, an about ID, a cell's row and column.
func wide(tag string) string { return tag + strings.Repeat("w", LimitNameBytes-len(tag)) }

// wideIDs are n IDs of 256 bytes.
func wideIDs(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%05d", prefix, i) + strings.Repeat("i", LimitNameBytes-len(prefix)-5)
	}
	return out
}

// argvWorstCase is one shape of input that plans as many argv bytes as the
// layout lets it, for its kind of entry.
type argvWorstCase struct {
	name    string
	cfg     Config
	kc      keyCfg
	entries func(n int) []Entry
	bound   int // the planned argv bound of the unit tier's run; zero is argvUnitBound
}

// argvUnitBound is the bound of the unit tier's run: 400,000 bytes, so that a
// few hundred members are cut by it, at the ratios of the contract's 8 MiB.
const argvUnitBound = 400000

// argvWorstCases are the worst case of each kind of entry, with the widest of
// everything the layout charges for: a table, IDs and about IDs of 256 bytes,
// cells of a 256-byte row and column, a namespace of 256 bytes, an epoch of 20
// digits, the longest member prefix Layer 1 accepts and a score wider than a
// store's. Each is built of n members, and carries a shared field so that the
// planned argv bytes are what cuts it (the other bounds are far off).
func argvWorstCases() []argvWorstCase {
	table := "t" + strings.Repeat("a", LimitNameBytes-1) // [A-Za-z0-9_][A-Za-z0-9_.-]*, 256 bytes
	src := wide("r") + ":" + wide("c")
	dst := wide("s") + ":" + wide("d")
	widest := Config{Epoch: "18446744073709551615", Header: []Member{{"space", pad(LimitNameBytes)}}, MemberPrefixBytes: 512}
	withPrefix := func(n int) Config { c := cfg(); c.MemberPrefixBytes = n; return c }
	fields := map[string]string{"shared": pad(1500)}
	unsets := func() []string {
		out := make([]string, 8)
		for i := range out {
			out[i] = fmt.Sprintf("u%d", i) + strings.Repeat("u", 200)
		}
		return out
	}()
	wideScores := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = strings.Repeat("1", 40+i%20)
		}
		return out
	}
	change := func(kind Kind, tbl, from, to string, unset []string, scores bool) func(n int) []Entry {
		return func(n int) []Entry {
			e := Entry{Kind: kind, Table: tbl, From: from, To: to, IDs: wideIDs("m", n), About: wideIDs("p", n), Set: fields, Unset: unset}
			if scores {
				e.Scores = wideScores(n)
			}
			return []Entry{e}
		}
	}
	return []argvWorstCase{
		{"creates", widest, keyCfg{512}, change(KindCreate, table, "", dst, nil, true), 0},
		{"moves", widest, keyCfg{512}, change(KindMove, table, src, dst, nil, true), 0},
		{"members that stay", widest, keyCfg{512}, change(KindMove, table, src, "", nil, true), 0},
		{"moves that unset", widest, keyCfg{512}, change(KindMove, table, src, dst, unsets, true), 0},
		{"removes", widest, keyCfg{512}, change(KindRemove, table, src, "", nil, false), 0},
		{"removes that unset", widest, keyCfg{512}, change(KindRemove, table, src, "", unsets, false), 0},
		// The five shapes the second read's counter (Layer 1's layout) found over
		// 8 MiB while the builder's count of them was under it.
		{"moves, a table of 256 bytes, the default prefix", cfg(), keyCfg{}, change(KindMove, table, "r:c", "s:c", nil, false), 0},
		{"moves, a table of 64 bytes, a prefix of 16", withPrefix(16), keyCfg{16}, change(KindMove, "t"+strings.Repeat("a", 63), "r:c", "s:c", nil, false), 0},
		{"removes, a short table, a prefix of exactly 200", withPrefix(200), keyCfg{200}, change(KindRemove, "work", "r:c", "", nil, false), 0},
		{"removes, a short table, a prefix of 256", withPrefix(256), keyCfg{256}, change(KindRemove, "work", "r:c", "", nil, false), 0},
		{"removes, a short table, the default prefix", cfg(), keyCfg{}, change(KindRemove, "work", "r:c", "", nil, false), 0},
		{"rows added and deleted", widest, keyCfg{512}, func(n int) []Entry {
			var out []Entry
			for i := 0; i < n/50; i++ {
				out = append(out, Entry{Kind: KindRows, Table: table, Add: wideIDs(fmt.Sprintf("a%d-", i), 50), Del: wideIDs(fmt.Sprintf("d%d-", i), 50)})
			}
			return out
		}, 90000}, // a step holds at most 100 row names: the bound is set under what they plan
		{"notes about primaries", widest, keyCfg{512}, func(n int) []Entry {
			e := Entry{Kind: KindNote}
			for i := 0; i < n/30; i++ {
				e.Notes = append(e.Notes, Note{Meta: map[string]string{"m": pad(300)}, About: wideIDs(fmt.Sprintf("q%d-", i), 30)})
			}
			return []Entry{e}
		}, 300000}, // a step holds at most 100 notes: the bound is set under what they plan
	}
}

// checkArgvWorstCase builds the case as n members with the planned argv bytes
// bounded at bound, and holds every step to what Layer 1 would say of it: its
// argv, counted from Layer 1's layout, is inside 8 MiB (else Layer 1 refuses
// LIMIT at prepare), inside the model's, which is inside the builder's bound
// with the margin, and each step but the last was closed by that bound.
func checkArgvWorstCase(t *testing.T, tc argvWorstCase, n, bound int) {
	t.Helper()
	c := tc.cfg
	c.Bounds = Contract()
	c.Bounds.PlannedArgvBytes = bound
	entries := tc.entries(n)
	steps, err := Build(c, entries)
	if err != nil {
		t.Errorf("%s: %v", tc.name, err)
		return
	}
	if len(steps) < 2 {
		t.Errorf("%s: %d step: the planned argv bytes do not cut it", tc.name, len(steps))
		return
	}
	pl := identityPlace(len(entries))
	most := 0
	for k, s := range steps {
		m := measureKeys(t, s.Encode(), tc.kc)
		if m.strict > LimitPlannedArgvBytes {
			t.Errorf("%s: step %d plans %d bytes by Layer 1's layout: over %d, Layer 1 refuses it LIMIT", tc.name, k+1, m.strict, LimitPlannedArgvBytes)
		}
		if bad := m.within(c.Bounds); len(bad) != 0 {
			t.Errorf("%s: step %d: %v", tc.name, k+1, bad)
		}
		most = max(most, m.strict)
		if k+1 == len(steps) {
			continue
		}
		hyp := s
		hyp.Entries = append([]Placed(nil), s.Entries...)
		hyp.Notes = append([]PlacedNote(nil), s.Notes...)
		if !addFirstPiece(&hyp, s, steps[k+1], pl) {
			t.Errorf("%s: step %d: nothing to add", tc.name, k+1)
			continue
		}
		closed := false
		for _, b := range measureKeys(t, hyp.Encode(), tc.kc).within(c.Bounds) {
			closed = closed || strings.HasPrefix(b, "planned argv")
		}
		if !closed {
			t.Errorf("%s: step %d was closed by something other than the planned argv bytes", tc.name, k+1)
		}
	}
	// The margin is a margin, not a waste: the fullest step reaches at least
	// half of what the bound allows by Layer 1's own count.
	t.Logf("%s: %d steps, the fullest plans %d of %d by Layer 1's layout (%d%%)", tc.name, len(steps), most, bound, most*100/bound)
	if most*2 < bound {
		t.Errorf("%s: the fullest step plans %d by Layer 1's layout, under half of %d", tc.name, most, bound)
	}
}

// At a bound of argvUnitBound bytes, so that a few hundred members are cut by
// it: the same ratios as the contract's 8 MiB, at a size a unit test can build.
// The slow tier runs the same cases at the contract's own number.
func TestPlannedArgvIsInsideLayerOnesOwnCountAtTheWorstCaseOfEveryKind(t *testing.T) {
	t.Parallel()
	for _, tc := range argvWorstCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bound := tc.bound
			if bound == 0 {
				bound = argvUnitBound
			}
			checkArgvWorstCase(t, tc, 600, bound)
		})
	}
}
