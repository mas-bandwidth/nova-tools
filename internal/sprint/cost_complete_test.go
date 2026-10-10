package sprint

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// Per landed is the stream's complete cost over its landed cards, not the landed
// cell. The cell is the work attempt written at land. Reads, the lander's run,
// and a no-result run sit on the card and on cards that have not landed, and
// they count (the owner, 2026-10-04: the complete cost).
func TestPerLandedIsWorkPlusReadsPlusLandingPlusUnanswered(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work)}
	s.Work.SetRows([]string{"s1", "s2"})
	landed := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{}}
	book(landed,
		Consumer{Kind: "work", Card: "s1-1.w1", Key: "w#g1", Tier: "flash", End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage("actual_usd=2 actual_by=harness")},
		Consumer{Kind: "read", Card: "s1-1.r1.a", Key: "r#v", Tier: "flash", End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage("actual_usd=1 actual_by=harness")},
		Consumer{Kind: "land", Card: "s1-1.land", Key: "l#g1", Tier: "pro", End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage("actual_usd=4 actual_by=harness")},
		Consumer{Kind: "work", Card: "s1-1.w0", Key: "w#g0", Tier: "flash", End: "no result", At: stamp(t0), Usage: cardcost.ParseUsage("actual_usd=0.25 actual_by=harness")},
	)
	// the figure the old per-landed divides: the work attempt alone
	landed.Fields[FieldCost] = "2"
	open := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(open,
		Consumer{Kind: "work", Card: "s1-2.w1", Key: "w2#g1", Tier: "pro", End: "no result: child wrote nothing", At: stamp(t0), Usage: cardcost.ParseUsage("predicted_usd=0.75")},
	)
	none := &Card{ID: "s2-1", Row: "s2", Col: Working, Fields: map[string]string{}}
	book(none,
		Consumer{Kind: "work", Card: "s2-1.w1", Key: "s2#g1", Tier: "flash", End: "ok", At: stamp(t0), Usage: cardcost.ParseUsage("actual_usd=9 actual_by=harness")},
	)
	s.Work.Put(landed)
	s.Work.Put(open)
	s.Work.Put(none)

	tc := StreamTierCosts(s)["s1"]
	assert.Equal(t, "$8.00", tc.TotalCost, "work 2, read 1, landing 4, unanswered 0.25 and 0.75")
	assert.Equal(t, "$8.00", tc.PerLanded, "one landed card: work plus reads plus landing plus unanswered, not the landed cell's $2")
	assert.Equal(t, "$2.00", tc.CostWork, "the landed card's work attempt, not its no-result run")
	assert.Equal(t, "$1.00", tc.CostReads)
	assert.Equal(t, "$4.00", tc.CostLand)
	assert.Equal(t, "$1.00", tc.CostUnanswered, "the landed card's no-result run and the open card's")
	assert.Equal(t, "-", StreamTierCosts(s)["s2"].PerLanded, "nothing landed")

	_, tiers := CostSplits(s)
	assert.Equal(t, "$11.00", tiers["flash"].CostWork, "s1's work attempt and s2's, both on flash")
	assert.Equal(t, "$1.00", tiers["flash"].CostReads)
	assert.Equal(t, "$0.25", tiers["flash"].CostUnanswered)
	assert.Equal(t, "$4.00", tiers["pro"].CostLand)
	assert.Equal(t, "$0.75", tiers["pro"].CostUnanswered)
}

// The four displayed parts allocate the total's rounded-up cents across them, so
// rounding fractional cents never makes the split exceed the reported complete cost
// (the owner, 2026-10-04: "we MUST track the complete cost"): a work charge of $0.001
// and a read charge of $0.001 show work $0.01, reads $0.00, total $0.01, not $0.01
// and $0.01, and the four sum to the total in every case.
func TestTheFourPartsAllocateTheRoundedTotal(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		part  map[int]string // part 0 work, 1 reads, 2 land, 3 unanswered -> exact dollars
		want  [4]string      // cost_work, cost_reads, cost_land, cost_unanswered; "" for nothing priced
		total string
	}{
		{"two equal fractions", map[int]string{0: "0.001", 1: "0.001"}, [4]string{"$0.01", "$0.00", "", ""}, "$0.01"},
		{"larger fraction receives the cent", map[int]string{0: "0.001", 1: "0.009"}, [4]string{"$0.00", "$0.01", "", ""}, "$0.01"},
		{"whole cents stay with their part", map[int]string{0: "1.001", 1: "2.009"}, [4]string{"$1.00", "$2.01", "", ""}, "$3.01"},
		{"more than one residual cent", map[int]string{0: "0.009", 1: "0.008", 2: "0.007"}, [4]string{"$0.01", "$0.01", "$0.01", ""}, "$0.03"},
		{"exact cents", map[int]string{0: "0.01", 1: "0.02"}, [4]string{"$0.01", "$0.02", "", ""}, "$0.03"},
		{"zero spend", map[int]string{0: "0", 1: "0"}, [4]string{"$0.00", "$0.00", "", ""}, "$0.00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Snapshot{Now: t0, Work: NewTable(Work)}
			s.Work.SetRows([]string{"s1"})
			pr := &Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{}}
			for part, amount := range tt.part {
				book(pr, partConsumer(part, amount))
			}
			s.Work.Put(pr)
			tc := StreamTierCosts(s)["s1"]
			assert.Equal(t, tt.want[0], tc.CostWork)
			assert.Equal(t, tt.want[1], tc.CostReads)
			assert.Equal(t, tt.want[2], tc.CostLand)
			assert.Equal(t, tt.want[3], tc.CostUnanswered)
			assert.Equal(t, tt.total, tc.TotalCost)
			sum := new(big.Rat)
			for _, v := range []string{tc.CostWork, tc.CostReads, tc.CostLand, tc.CostUnanswered} {
				if v == "" {
					continue
				}
				usd, ok := new(big.Rat).SetString(v[1:])
				require.True(t, ok, v)
				sum.Add(sum, usd)
			}
			assert.Equal(t, tc.TotalCost, "$"+sum.FloatString(2), "the displayed parts sum to the displayed total")
		})
	}
}

// partConsumer is one priced consumer in the part named (0 work, 1 reads, 2 the
// lander's run, 3 a run whose end begins "no result"), all on the flash tier.
func partConsumer(part int, amount string) Consumer {
	kind, end, key := "work", "ok", "w"
	switch part {
	case 1:
		kind, key = "read", "r"
	case 2:
		kind, key = "land", "l"
	case 3:
		end, key = "no result", "u"
	}
	return Consumer{Kind: kind, Card: "s1-1", Key: key + "#v", Tier: "flash", End: end, At: stamp(t0),
		Usage: cardcost.ParseUsage("actual_usd=" + amount + " actual_by=harness")}
}
