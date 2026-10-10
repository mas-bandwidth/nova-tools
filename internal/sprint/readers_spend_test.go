package sprint

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// spendWorld is two streams whose primaries carry read records of three readers and one
// take: reader-a two priced reads on s1 (one two hours ago, one ten minutes ago) and one on
// s2 ten minutes ago; reader-b one priced read on s2 three hours ago and one that reported
// no token; reader-c a subscription reader's two reads, tokens and no dollars.
func spendWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.s.Work.SetRows([]string{"s1", "s2"})
	ago := func(d time.Duration) string { return stamp(w.s.Now.Add(-d)) }
	read := func(key, who, at, usage string) Consumer {
		return Consumer{Kind: "read", Card: key, Attempt: 1, Who: who, End: "ok", At: at, Key: key, Usage: cardcost.ParseUsage(usage)}
	}
	s1 := &Card{ID: "s1-1", Row: "s1", Col: Review, Fields: map[string]string{}}
	book(s1,
		Consumer{Kind: "work", Card: "s1-1.w1", Who: "m1", End: "ok", At: ago(3 * time.Hour), Key: "s1-1#g1", Usage: cardcost.ParseUsage("input=9 actual_usd=7 actual_by=harness")},
		read("s1-1.r1.reader-a#v", "reader-a", ago(2*time.Hour), "input=10 predicted_usd=0.25"),
		read("s1-1.r2.reader-a#v", "reader-a", ago(10*time.Minute), "input=10 actual_usd=0.5 actual_by=harness"),
		read("s1-1.r1.reader-c#v", "reader-c", ago(5*time.Minute), "input=1000 output=200 unpriced=subscription"),
	)
	s2 := &Card{ID: "s2-1", Row: "s2", Col: Landed, Fields: map[string]string{}}
	book(s2,
		read("s2-1.r1.reader-a#v", "reader-a", ago(10*time.Minute), "input=10 predicted_usd=0.125"),
		read("s2-1.r1.reader-b#v", "reader-b", ago(3*time.Hour), "input=10 predicted_usd=2"),
		read("s2-1.r2.reader-b#v", "reader-b", ago(time.Minute), "unpriced=no-tokens"),
		read("s2-1.r1.reader-c#v", "reader-c", ago(time.Minute), "input=300 unpriced=subscription"),
	)
	w.s.Work.Put(s1)
	w.s.Work.Put(s2)
	return w
}

// The readers table carries each reader's spend (the owner, 2026-10-05: "I would ask that
// you need to track spend on readers, can you do this before we start?"): the sum of its
// priced read records, all time and the last hour, and per read, from each stream's where
// record summed over the streams; a take is never a reader's, a read that reported no token
// is counted and never priced, and a subscription reader's cost is its tokens.
func TestTheReadersTableCarriesEachReadersSpend(t *testing.T) {
	t.Parallel()
	w := spendWorld(t)

	streams := StreamTierCosts(w.s)
	assert.Equal(t, map[string]ReaderSpend{
		"reader-a": {Reads: 2, Priced: 2, USD: "0.75", HourPriced: 1, HourUSD: "0.5"},
		"reader-c": {Reads: 1, Tokens: 1200},
	}, streams["s1"].Readers, "s1's where record carries its readers' spend, and the take is no reader's")

	spends := ReaderSpendsOf(streams)
	require.Len(t, spends, 3, "three readers, and no take counted as one: %v", spends)
	assert.Equal(t, ReaderSpend{Reads: 3, Priced: 3, USD: "0.875", HourPriced: 2, HourUSD: "0.625"}, spends["reader-a"], "reader-a over both streams")
	assert.Equal(t, ReaderSpend{Reads: 2, Priced: 1, USD: "2"}, spends["reader-b"], "a read with no token counted, never priced; three hours ago is outside the hour")
	assert.Equal(t, ReaderSpend{Reads: 2, Tokens: 1500}, spends["reader-c"], "a subscription reader's cost is its tokens")

	assert.Equal(t, map[string]string{ReaderSpendCol: "$0.88", ReaderSpendHourCol: "$0.63", ReaderPerReadCol: "$0.30", ReaderPricedCol: "3"},
		spends["reader-a"].Cells(), "dollars and cents rounded up; per read is 0.875 / 3")
	assert.Equal(t, map[string]string{ReaderSpendCol: "$2.00", ReaderSpendHourCol: "-", ReaderPerReadCol: "$2.00", ReaderPricedCol: "1"},
		spends["reader-b"].Cells(), "nothing in the hour is -, never $0.00")
	assert.Equal(t, map[string]string{ReaderSpendCol: "-", ReaderSpendHourCol: "-", ReaderPerReadCol: "-", ReaderPricedCol: "0"},
		spends["reader-c"].Cells())

	all := ReaderSpendTotal(spends)
	assert.Equal(t, ReaderSpend{Reads: 7, Priced: 4, USD: "2.875", HourPriced: 2, HourUSD: "0.625", Tokens: 1500}, all, "the one row sums every reader")
	assert.Equal(t, spends, ReaderSpends(w.s), "the snapshot's sum is the records' sum")

	// the readers' spend is the read cost each stream already splits out: no read lost
	// between the two, and no take in either
	assert.Equal(t, "$0.75", streams["s1"].ReadCost)
	assert.Equal(t, "$2.13", streams["s2"].ReadCost, "0.125 + 2")
}

// plus is the two spends summed: the same reader's on two streams.
func (r ReaderSpend) plus(o ReaderSpend) ReaderSpend {
	out := ReaderSpend{Reads: r.Reads + o.Reads, Priced: r.Priced + o.Priced, HourPriced: r.HourPriced + o.HourPriced, Tokens: r.Tokens + o.Tokens}
	out.USD, out.HourUSD = usdPlus(r.USD, o.USD), usdPlus(r.HourUSD, o.HourUSD)
	return out
}

// usdPlus is two exact dollar figures summed, "" when neither is one: nothing priced stays
// nothing priced, never $0.00.
func usdPlus(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	sum, _ := cardcost.Sum(a, b)
	return sum
}

// ReaderSpendsOf is each reader's spend summed over the streams' where records
// (TierCosts.Readers): what the readers table is to show, read off the tick's
// record, never off the cards at where.
func ReaderSpendsOf(streams map[string]TierCosts) map[string]ReaderSpend {
	out := map[string]ReaderSpend{}
	names := make([]string, 0, len(streams))
	for st := range streams {
		names = append(names, st)
	}
	sort.Strings(names) // the sums are exact; the order only keeps the walk the same
	for _, st := range names {
		for rd, sp := range streams[st].Readers {
			out[rd] = out[rd].plus(sp)
		}
	}
	return out
}

// ReaderSpendTotal is every reader's spend summed: the readers table's one row.
func ReaderSpendTotal(spends map[string]ReaderSpend) ReaderSpend {
	names := make([]string, 0, len(spends))
	for rd := range spends {
		names = append(names, rd)
	}
	sort.Strings(names)
	var all ReaderSpend
	for _, rd := range names {
		all = all.plus(spends[rd])
	}
	return all
}

// ReaderSpends is each reader's spend over every primary of the work table, all streams.
func ReaderSpends(s *Snapshot) map[string]ReaderSpend {
	return ReaderSpendsOf(StreamTierCosts(s))
}
