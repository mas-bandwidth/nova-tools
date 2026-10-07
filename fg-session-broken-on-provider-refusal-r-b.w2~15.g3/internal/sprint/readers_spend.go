package sprint

import (
	"cmp"
	"math/big"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// What each reader spent (docs/SPEC-SPRINT.md, "What a card cost", a reader's spend; the
// owner, 2026-10-05, before funding a provider for reads: "I would ask that you need to
// track spend on readers, can you do this before we start?"). Every read's run is already a
// consumer record on its primary (readConsumer: who read it, its route, its charged figure),
// so a reader's spend is a sum over the records the tick reads anyway, never a tally of the
// log by hand: each stream's where record carries its readers' sums (TierCosts.Readers),
// ReaderSpendsOf adds them over the streams, and Cells are what the readers table of the
// where view is to show beside each reader's counts (spend, spend_1h, per_read, reads_priced;
// drawing them is cmd/nova-sprint's, owed).

// ReaderSpendWindow is the recent window a reader's spend is also summed over: the last hour.
const ReaderSpendWindow = time.Hour

// The readers table's spend columns, as the where view is to show them: what its reads cost all time and in the last hour, dollars and
// cents rounded up ("-" when none of its reads was priced), what a priced read cost on
// average, and how many of its reads were priced.
const (
	ReaderSpendCol     = "spend"
	ReaderSpendHourCol = "spend_1h"
	ReaderPerReadCol   = "per_read"
	ReaderPricedCol    = "reads_priced"
)

// ReaderSpendCols are the spend columns in the order the readers table shows them.
var ReaderSpendCols = []string{ReaderSpendCol, ReaderSpendHourCol, ReaderPerReadCol, ReaderPricedCol}

// ReaderSpend is one reader's reads summed: how many were recorded, how many carried a
// dollar figure, the exact dollars all time and in the last hour (each read's actual cost
// where reported, else its predicted one; "" when none was priced), and a subscription
// reader's tokens (WhySubscription), the reads whose cost is their tokens.
type ReaderSpend struct {
	Reads      int    `json:"reads"`
	Priced     int    `json:"priced,omitempty"`
	USD        string `json:"usd,omitempty"`
	HourPriced int    `json:"hour_priced,omitempty"`
	HourUSD    string `json:"hour_usd,omitempty"`
	Tokens     int64  `json:"tokens,omitempty"`
}

// addRead counts the read record con into the reader's spend at now.
func (r *ReaderSpend) addRead(con Consumer, now time.Time) {
	u := con.Usage
	r.Reads++
	if u.Unpriced == WhySubscription {
		r.Tokens += max(u.Tokens.Total(), 0)
		return
	}
	usd := cmp.Or(u.Actual, u.Predicted)
	if usd == "" {
		return
	}
	if sum, ok := cardcost.Sum(r.USD, usd); ok {
		r.USD, r.Priced = sum, r.Priced+1
	}
	if at, err := time.Parse(time.RFC3339, con.At); err == nil && now.Sub(at) < ReaderSpendWindow && !at.After(now) {
		if sum, ok := cardcost.Sum(r.HourUSD, usd); ok {
			r.HourUSD, r.HourPriced = sum, r.HourPriced+1
		}
	}
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

// PerRead is what one priced read cost on average, dollars and cents rounded up (MoneyText);
// "-" when none was priced.
func (r ReaderSpend) PerRead() string {
	total, err := amountOf(r.USD)
	if err != nil || total == nil || r.Priced == 0 {
		return "-"
	}
	return cardcost.Cents(total.Quo(total, big.NewRat(int64(r.Priced), 1)))
}

// Cells are the reader's spend as the readers table's cells (ReaderSpendCols): text, as every
// cell of the where view is.
func (r ReaderSpend) Cells() map[string]string {
	return map[string]string{
		ReaderSpendCol:     MoneyText(r.USD),
		ReaderSpendHourCol: MoneyText(r.HourUSD),
		ReaderPerReadCol:   r.PerRead(),
		ReaderPricedCol:    itoa(r.Priced),
	}
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
