package stepbuild

import (
	"fmt"
	"strings"
	"testing"
)

// The planned argv bytes (section 6: 8 MiB across every command of the table
// plan, the log plan and the receipt). The builder counts an upper bound over
// Layer 1's own layout, per changed member and per step (layout.go), and cuts to
// it with a margin of 25 percent on top; the tests count it by two accountings
// written apart from it, in helpers_test.go: the model, which the bound tests
// hold the builder to the byte (with the margin), and the strict count, built
// from real commands and real keys as Layer 1 lays them out, which no step may
// exceed.

// argvCase is an input to count.
type argvCase struct {
	name    string
	cfg     Config
	kc      keyCfg
	entries []Entry
}

func longName(n int, tag string) string { return tag + strings.Repeat("n", n-len(tag)) }

func argvCases() []argvCase {
	ids := func(prefix string, n, size int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = longName(size, fmt.Sprintf("%s%d-", prefix, i))
		}
		return out
	}
	withPrefix := cfg()
	withPrefix.MemberPrefixBytes = 64
	longSpace := Config{Epoch: "1", Header: []Member{{"space", pad(LimitNameBytes)}}}
	longEpoch := Config{Epoch: "18446744073709551615", Header: hdr}
	withOp := cfg()
	withOp.Ident = func(part int) Ident {
		return Ident{Op: fmt.Sprintf("op-%d", part), Intent: "intent", Result: "result"}
	}
	mvAbout := func(n int) Entry {
		e := mv("work", names("m", n))
		e.About = names("p", n)
		return e
	}
	full := mv("work", names("f", 20))
	full.Scores = make([]string, 20)
	for i := range full.Scores {
		full.Scores[i] = fmt.Sprintf("%d.5e-%d", i, i)
	}
	full.Set = map[string]string{"s": pad(30)}
	full.Each = make([]map[string]string, 20)
	for i := range full.Each {
		full.Each[i] = map[string]string{"e": pad(i)}
	}
	full.Unset = []string{"u1", "u2"}
	full.About = names("p", 20)
	full.Meta = map[string]string{"why": "so"}
	longCells := Entry{Kind: KindMove, Table: "work", From: longName(256, "r") + ":" + longName(256, "c"), To: longName(200, "s") + ":" + longName(100, "d"), IDs: names("m", 20)}
	return []argvCase{
		{"plain moves", cfg(), keyCfg{}, []Entry{mv("work", names("m", 20))}},
		{"a member prefix of 64", withPrefix, keyCfg{64}, []Entry{mv("work", names("m", 20))}},
		{"IDs of 256 bytes with about IDs of 256 bytes", cfg(), keyCfg{}, []Entry{func() Entry {
			e := mv("work", ids("m", 20, 256))
			e.About = ids("p", 20, 256)
			return e
		}()}},
		{"an about ID a member", cfg(), keyCfg{}, []Entry{mvAbout(20)}},
		{"scores wider than a store's", cfg(), keyCfg{}, []Entry{{Kind: KindCreate, Table: "work", To: "row:col", IDs: names("m", 20), Scores: func() []string {
			out := make([]string, 20)
			for i := range out {
				out[i] = strings.Repeat("1", 30+i)
			}
			return out
		}()}}},
		{"unset names", cfg(), keyCfg{}, []Entry{func() Entry {
			e := mv("work", names("m", 20))
			e.Unset = []string{"gone", "away", strings.Repeat("u", 200)}
			return e
		}()}},
		{"shared and own fields", cfg(), keyCfg{}, []Entry{full}},
		{"a namespace of 256 bytes", longSpace, keyCfg{}, []Entry{mvAbout(20)}},
		{"an epoch of 20 digits", longEpoch, keyCfg{}, []Entry{mvAbout(20)}},
		{"cells of long rows and columns", cfg(), keyCfg{}, []Entry{longCells}},
		{"a create, a move and a remove", cfg(), keyCfg{}, []Entry{
			{Kind: KindCreate, Table: "work", To: "row:col", IDs: names("c", 8), Scores: names("", 8)},
			mv("merge", names("m", 8)),
			{Kind: KindRemove, Table: "work", From: "row:col", IDs: names("r", 8), Set: map[string]string{"retired": "yes"}},
		}},
		{"rows added and deleted", cfg(), keyCfg{}, []Entry{{Kind: KindRows, Table: "work", Add: names("a", 30), Del: names("d", 30)}}},
		{"notes about primaries", cfg(), keyCfg{}, []Entry{{Kind: KindNote, Notes: []Note{
			{Meta: map[string]string{"n": "1"}, About: names("p", 50)},
			{Meta: map[string]string{"n": "2"}, About: names("q", 50)},
			{About: []string{longName(256, "z")}},
		}}}},
		{"an op, with its receipt", withOp, keyCfg{}, []Entry{mvAbout(20)}},
	}
}

// Every term of the model is counted by the builder: a step's model count with
// the margin on top (charged), used as the bound, is one step, and one byte
// fewer is more; and the strict count of a step, Layer 1's own layout, is never
// over the model's, or the bound.
func TestPlannedArgvIsTheModelOfEveryShape(t *testing.T) {
	t.Parallel()
	for _, sh := range argvCases() {
		c := sh.cfg
		c.Bounds = Contract()
		steps := must(t, c, sh.entries)
		if len(steps) != 1 {
			t.Errorf("%s: %d steps at the contract's bounds", sh.name, len(steps))
			continue
		}
		m := measureKeys(t, steps[0].Encode(), sh.kc)
		if bad := m.within(Contract()); len(bad) != 0 {
			t.Errorf("%s: %v", sh.name, bad)
		}
		if m.strict > m.argv {
			t.Errorf("%s: the strict count is %d, the model %d: a model that is no upper bound", sh.name, m.strict, m.argv)
		}
		bound := charged(m.argv)
		c.Bounds.PlannedArgvBytes = bound
		if steps := must(t, c, sh.entries); len(steps) != 1 {
			t.Errorf("%s: a bound of exactly the model's %d with its margin, %d: %d steps", sh.name, m.argv, bound, len(steps))
		}
		c.Bounds.PlannedArgvBytes = bound - 1
		steps = must(t, c, sh.entries)
		if len(steps) < 2 {
			t.Errorf("%s: a bound of the model's %d with its margin, %d, less one: %d steps: the builder counts less than the model", sh.name, m.argv, bound, len(steps))
		}
		for _, s := range steps {
			if got := measureKeys(t, s.Encode(), sh.kc); charged(got.argv) > bound-1 || got.strict > bound-1 {
				t.Errorf("%s: step %d plans %d by the model, %d strictly, over %d", sh.name, s.Part, got.argv, got.strict, bound-1)
			}
		}
	}
}

// The reader's demonstration: 2,000 moves whose IDs are 253 bytes, about the
// same ID, and a field of 2,000 bytes shared by all. The keys and the IDs the
// commands repeat are bytes of the planned argv: each step is cut by them, and
// counted by the strict count from real commands it is inside 8 MiB. (Counted
// without them, a step held 1,806 members, and needed at least 10.2 MB.)
func TestPlannedArgvOfIDsOf256BytesIsCutAtTheBound(t *testing.T) {
	t.Parallel()
	const n = 2000
	e := mv("work", make([]string, n))
	e.About = make([]string, n)
	for i := range e.IDs {
		e.IDs[i] = fmt.Sprintf("%04d", i) + strings.Repeat("i", 249)
		e.About[i] = e.IDs[i]
	}
	e.Set = map[string]string{"shared": pad(2000)}
	steps := must(t, cfg(), []Entry{e})
	if len(steps) < 2 {
		t.Fatalf("%d steps", len(steps))
	}
	if held := countMembers(steps[:1]); held >= 1806 {
		t.Fatalf("the first step holds %d members: the keys and IDs are not counted", held)
	}
	pl := identityPlace(1)
	for k, s := range steps {
		m := measure(t, s.Encode())
		if m.strict > LimitPlannedArgvBytes || m.argv > LimitPlannedArgvBytes || len(m.within(Contract())) != 0 {
			t.Fatalf("step %d: %d by the strict count, %d by the model, %v", k+1, m.strict, m.argv, m.within(Contract()))
		}
		// Each step is as full as the argv bound lets it be, and it is the
		// argv bound that closed it.
		if k+1 < len(steps) {
			hyp := s
			hyp.Entries = append([]Placed(nil), s.Entries...)
			if !addFirstPiece(&hyp, s, steps[k+1], pl) {
				t.Fatalf("step %d: nothing to add", k+1)
			}
			bad := measure(t, hyp.Encode()).within(Contract())
			if len(bad) == 0 || !strings.HasPrefix(bad[0], "planned argv") {
				t.Fatalf("step %d was closed by %v, not by the planned argv bytes", k+1, bad)
			}
		}
	}
}

func TestTheMemberPrefixIsCounted(t *testing.T) {
	t.Parallel()
	e := mv("work", names("m", 2000))
	e.Set = map[string]string{"f": pad(2000)}
	small, big := cfg(), cfg()
	small.MemberPrefixBytes = 8
	big.MemberPrefixBytes = DefaultMemberPrefixBytes
	a, b := must(t, small, []Entry{e}), must(t, big, []Entry{e})
	if in8, inBig := countMembers(a[:1]), countMembers(b[:1]); in8 <= inBig {
		t.Fatalf("a prefix of 8 bytes gives a first step of %d members, one of %d gives %d: the prefix is not counted", in8, DefaultMemberPrefixBytes, inBig)
	}
	for _, s := range a {
		if m := measureKeys(t, s.Encode(), keyCfg{8}); len(m.within(Contract())) != 0 {
			t.Fatalf("a prefix of 8, step %d: %v", s.Part, m.within(Contract()))
		}
	}
	for _, s := range b {
		if m := measureKeys(t, s.Encode(), keyCfg{DefaultMemberPrefixBytes}); len(m.within(Contract())) != 0 {
			t.Fatalf("a prefix of %d, step %d: %v", DefaultMemberPrefixBytes, s.Part, m.within(Contract()))
		}
	}
	// Zero is DefaultMemberPrefixBytes, the same steps as it said outright.
	zero := must(t, cfg(), []Entry{e})
	if len(zero) != len(b) || zero[0].Bytes != b[0].Bytes || zero[0].Cursor != b[0].Cursor {
		t.Fatalf("the default is not %d bytes: %d steps against %d", DefaultMemberPrefixBytes, len(zero), len(b))
	}
	for _, bad := range []int{-1, LimitPlannedArgvBytes + 1} {
		c := cfg()
		c.MemberPrefixBytes = bad
		if _, err := Build(c, []Entry{e}); err == nil || !strings.Contains(err.Error(), "member prefix bytes") {
			t.Errorf("a member prefix of %d bytes: %v", bad, err)
		}
	}
}

// The default member prefix is the longest Layer 1 accepts for a definition
// (table_set.lua:443 refuses one over 512 bytes with CONFIG), typed here, so
// that a caller who names none is safe: the planned argv bound holds for every
// prefix Layer 1 takes.
func TestTheDefaultMemberPrefixIsTheLongestLayerOneAccepts(t *testing.T) {
	t.Parallel()
	if DefaultMemberPrefixBytes != 512 {
		t.Fatalf("the default member prefix is %d bytes; Layer 1 accepts up to 512", DefaultMemberPrefixBytes)
	}
}
