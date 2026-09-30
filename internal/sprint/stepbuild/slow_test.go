//go:build slow

package stepbuild

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"
)

// The longer tier of the property test (make test-slow): 10,000 random
// inputs at the contract's own bounds, and the scaled-down inputs of the unit
// tier again with the full accounting on every one. The inputs at the
// contract's numbers are built to reach them: thousands of members, hundreds
// of entries, megabytes of fields, a hundred notes, the row bound.

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
		m.Notes = []Note{{About: g.pick("q", 1500+g.r.IntN(1500), 3000)}}
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
				check(t, seed, Contract(), ident, seed%200 == 0, g.bigInput())
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
				check(t, seed, smallBounds(ident), ident, true, g.input())
			}
		})
	}
}

func TestSlowPropertyEveryBoundAtRandomFullAccounting(t *testing.T) {
	t.Parallel()
	propertyRandomBounds(t, propertyInputs, 8, 1)
}
