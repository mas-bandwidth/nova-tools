//go:build slow

package stepbuild

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"
)

// The longer tier of the property test (make test-slow): 10,000 random
// inputs at the contract's own bounds, the scaled-down inputs of the unit
// tier again (the same seeds) with the full accounting and the fullness check
// on every one, where the unit tier applies them to one in sampleEvery, the
// rows and members property at 6,000 worlds where the unit tier cuts 2,400, and
// the worst case of every kind of entry against Layer 1's layout at the
// contract's own 8 MiB of planned argv bytes. The inputs at the contract's
// numbers are built to reach them: thousands of members, hundreds of entries,
// megabytes of fields, a hundred notes, the row bound.

// bigFragments are the shapes of input that reach a bound of section 6; an
// input is one to three of them, on ids that may overlap.
var bigFragments = []func(g gen, prefix string) []Entry{
	// candidates: thousands of small members, sometimes guarded and noted
	func(g gen, prefix string) []Entry {
		n := 1800 + g.r.IntN(2500)
		e := mv(g.table(), g.pick(prefix, n, n))
		if g.coin(40) {
			e.Guards = g.guards()
		}
		e.Notes = g.notes()
		return []Entry{e}
	},
	// guard-only members: more than a step's worth
	func(g gen, prefix string) []Entry {
		return []Entry{gd(g.table(), g.pick(prefix, 1500+g.r.IntN(2600), 4200)), gd(g.table(), g.pick(prefix+"x", 1500+g.r.IntN(2600), 4200))}
	},
	// entries: hundreds of small ones
	func(g gen, prefix string) []Entry {
		n := 230 + g.r.IntN(60)
		out := make([]Entry, n)
		for i := range out {
			out[i] = mv(g.table(), []string{prefix + strconv.Itoa(i)})
		}
		return out
	},
	// rows: past the row bound, adds and dels, in a few tables
	func(g gen, prefix string) []Entry {
		var out []Entry
		for i, n := 0, 1+g.r.IntN(3); i < n; i++ {
			out = append(out, Entry{Kind: KindRows, Table: g.table(), Add: g.pick("a", 30+g.r.IntN(90), 200), Del: g.pick("d", g.r.IntN(60), 200)})
		}
		return out
	},
	// notes and about: at or over their bounds
	func(g gen, prefix string) []Entry {
		e := Entry{Kind: KindNote}
		for i, n := 0, 90+g.r.IntN(20); i < n; i++ {
			e.Notes = append(e.Notes, Note{Meta: g.fields(2), About: g.pick("p", g.r.IntN(90), 90)})
		}
		m := mv(g.table(), g.pick(prefix, 1000+g.r.IntN(1200), 2300))
		m.About = g.pick("p", len(m.IDs), len(m.IDs))
		m.Notes = []Note{{About: g.pick("q", 1500+g.r.IntN(501), 3000)}}
		return []Entry{e, m}
	},
	// heavy bytes: members of hundreds of KiB, past the request and the line
	func(g gen, prefix string) []Entry {
		n := 3 + g.r.IntN(14)
		e := mv(g.table(), g.pick(prefix, n, n))
		e.Each = make([]map[string]string, n)
		for i := range e.Each {
			e.Each[i] = fat(1+g.r.IntN(8), 5000+g.r.IntN(40000))
		}
		return []Entry{e}
	},
	// shared expansion: a few large shared fields over many members
	func(g gen, prefix string) []Entry {
		n := 10 + g.r.IntN(150)
		e := mv(g.table(), g.pick(prefix, n, n))
		e.Set = fat(1+g.r.IntN(5), 2000+g.r.IntN(62000))
		e.Unset = g.names("u", 3)
		return []Entry{e}
	},
	// observations: a hundred and twenty-eight of each name, over thousands
	func(g gen, prefix string) []Entry {
		e := mv(g.table(), g.pick(prefix, 900+g.r.IntN(1400), 2300))
		e.Set, e.Unset, e.BeforeFields = map[string]string{}, nil, nil
		for i := 0; i < 128; i++ {
			e.Set[fmt.Sprintf("s%03d", i)] = "v"
			e.Unset = append(e.Unset, fmt.Sprintf("u%03d", i))
			e.BeforeFields = append(e.BeforeFields, fmt.Sprintf("b%03d", i))
		}
		return []Entry{e, gd(g.table(), g.pick("g", 1+g.r.IntN(50), 50))}
	},
	// rows and members: thousands of members that leave a row and enter one,
	// with the rows entries that add the one and delete the other (or the one entry
	// that does both), in any order
	func(g gen, prefix string) []Entry {
		table := g.table()
		e := mv(table, g.pick(prefix, 1500+g.r.IntN(2500), 4000))
		e.From, e.To = "src:c", "dst:c"
		out := []Entry{e, {Kind: KindRows, Table: table, Add: []string{"dst"}}, {Kind: KindRows, Table: table, Del: []string{"src"}}}
		if g.coin(50) {
			// the rename of a row: one rows entry that adds the one and deletes the other
			out = []Entry{e, {Kind: KindRows, Table: table, Add: []string{"dst"}, Del: []string{"src"}}}
		}
		g.r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	},
	// tables: more than four, with guards on others
	func(g gen, prefix string) []Entry {
		var out []Entry
		for i := 0; i < 6; i++ {
			e := mv("t"+strconv.Itoa(i), g.pick(prefix, 1+g.r.IntN(20), 30))
			if g.coin(30) {
				e.Guards = []Entry{gd("t"+strconv.Itoa((i+3)%6), g.pick("g", 1+g.r.IntN(4), 8))}
			}
			out = append(out, e)
		}
		return out
	},
}

func (g gen) bigInput() []Entry {
	var out []Entry
	for i, n := 0, 1+g.r.IntN(3); i < n; i++ {
		prefix := "m"
		if g.coin(40) {
			prefix = "m" + strconv.Itoa(i)
		}
		out = append(out, bigFragments[g.r.IntN(len(bigFragments))](g, prefix)...)
	}
	return out
}

func TestSlowPropertyAtTheContractsBounds(t *testing.T) {
	t.Parallel()
	const shards = 8
	for shard := 0; shard < shards; shard++ {
		t.Run(strconv.Itoa(shard), func(t *testing.T) {
			t.Parallel()
			for seed := uint64(shard + 1); seed <= propertyInputs; seed += shards {
				g := gen{rand.New(rand.NewPCG(seed, 0xb16))}
				ident := seed%2 == 0
				// The full accounting decodes megabytes: every 200th input.
				check(t, seed, Contract(), ident, seed%200 == 0, seed%200 == 0, g.bigInput())
			}
		})
	}
}

func TestSlowPropertyFullAccountingOnEveryScaledInput(t *testing.T) {
	t.Parallel()
	const shards = 8
	for shard := 0; shard < shards; shard++ {
		t.Run(strconv.Itoa(shard), func(t *testing.T) {
			t.Parallel()
			for seed := uint64(shard + 1); seed <= propertyInputs; seed += shards {
				g := gen{rand.New(rand.NewPCG(seed, 0x5eed))}
				ident := seed%2 == 0
				check(t, seed, smallBounds(ident), ident, true, true, g.input())
			}
		})
	}
}

func TestSlowPropertyEveryBoundAtRandomFullAccounting(t *testing.T) {
	t.Parallel()
	propertyRandomBounds(t, propertyInputs, 8, 1)
}

// The worst case of every kind of entry (argv_layout_test.go) at the
// contract's own bound: no step the builder emits has a count over 8 MiB by
// Layer 1's layout, which is the step Layer 1 would refuse LIMIT at prepare.
func TestSlowPlannedArgvIsInsideLayerOnesOwnCountAtTheContractsBound(t *testing.T) {
	t.Parallel()
	for _, tc := range argvWorstCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Rows and notes are held by their own bounds (100 row names, 100 notes)
			// long before 8 MiB of argv: a step of them cannot reach it, and their
			// case keeps the bound it has in the unit tier.
			n, bound := 4500, LimitPlannedArgvBytes
			if tc.bound != 0 {
				n, bound = 5000, tc.bound
			}
			checkArgvWorstCase(t, tc, n, bound)
		})
	}
}

// The rows and members property at the slow tier's number of worlds and inputs.
func TestSlowRowsAndMembersAtEveryCutPoint(t *testing.T) {
	t.Parallel()
	rowsAndMembersAtEveryCutPoint(t, cutPointInputs)
}

// The second read's scenario at its own size: notes of one meta value each of
// 45,000 bytes cut by the contract's 4 MiB, one step counted in full (the unit
// tier runs it at a sixty-fourth of the sizes, cjson_test.go).
func TestSlowARequestAtTheContractsBoundIsInsideLayerOnesReEncoding(t *testing.T) {
	t.Parallel()
	for i, f := range fills[1:4] { // paths, slashes, DEL: the fills that were over the bound
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			counted := 0
			if i == 0 {
				counted = 1
			}
			checkNotesAtTheRequestBound(t, Contract(), f.fill, 0, 45000, counted)
		})
	}
}

// The same at the reader's 300 notes, every step counted in full, for every fill.
func TestThreeHundredNotesAtTheRequestBoundAreInsideLayerOnesReEncoding(t *testing.T) {
	t.Parallel()
	for _, f := range fills {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			checkNotesAtTheRequestBound(t, Contract(), f.fill, 300, 45000, 1<<30)
		})
	}
}

// A note whose line is exactly 1 MiB by cjson, made of 100,000 DEL and 100,000
// slashes and a padding (the unit tier: 16 KiB, cjson_test.go).
func TestSlowALineAtTheContractsBoundIsCountedAtCJSONWidth(t *testing.T) {
	t.Parallel()
	checkALineAtTheBound(t, Contract(), 100000)
}

// Members with values of 20,000 bytes of paths, slashes and DEL, cut by the
// contract's request, line and planned argv bounds at cjson width (the unit
// tier: values of 312 bytes at a sixty-fourth of the bounds, cjson_test.go).
func TestSlowMembersOfSlashesAndDELAreCutAtCJSONWidth(t *testing.T) {
	t.Parallel()
	checkMembersAtCJSONWidth(t, Contract(), 20000, []int{160, 110, 40})
}
