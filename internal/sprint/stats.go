package sprint

import (
	"cmp"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The pass's numbers (`nova-sprint stats`): how long each stage of the epoch's
// cards took, per member, per reader and per route, from the stamps and the usage
// the cards already carry. Pure: the snapshot in, the
// numbers out; the verb reads the snapshot once (StatsRecords) and prints them.

// Measure is the median, the max and the count of one measure's samples, in
// seconds; N 0 is no sample.
type Measure struct {
	Median float64 `json:"median_s"`
	Max    float64 `json:"max_s"`
	N      int     `json:"n"`
}

// Stages are the primary's stages: deal wait (admitted to its first work card's
// first deal), finish to two reads (its last ok take finished to accepted), accept
// to land (accepted to landed) and total (admitted to landed).
type Stages struct {
	DealWait      Measure `json:"deal_wait"`
	FinishToReads Measure `json:"finish_to_2reads"`
	AcceptToLand  Measure `json:"accept_to_land"`
	Total         Measure `json:"total"`
}

// MemberStat is one member's work cards: how many, how many failed (ok=no), the
// take wait (dealt to taken), the run wall (the usage's wall) and the report lag
// (finished - taken - wall: what the member spent around its child).
type MemberStat struct {
	Member    string  `json:"member"`
	Cards     int     `json:"cards"`
	Failed    int     `json:"failed"`
	TakeWait  Measure `json:"take_wait"`
	RunWall   Measure `json:"run_wall"`
	ReportLag Measure `json:"report_lag"`
}

// ReaderStat is one reader's read cards, asked of it (retired ones too): the begin
// wait (asked to begun), the run wall and the report lag (read - begun - wall).
// Cost is the charged sum of its priced reads (actual where the harness reported
// one, else predicted), dollars and cents rounded up, "-" when none was priced.
// CostMedian is the median of those charged figures, the middle one, or the mean
// of the two middle ones when the count is even, rounded up the same way.
// Unpriced is how many of its finished reads carry no dollar figure. A subscription
// read's tokens are not among them (its cost is the tokens).
type ReaderStat struct {
	Reader     string  `json:"reader"`
	Cards      int     `json:"cards"`
	BeginWait  Measure `json:"begin_wait"`
	RunWall    Measure `json:"run_wall"`
	ReportLag  Measure `json:"report_lag"`
	Cost       string  `json:"cost"`
	CostMedian string  `json:"cost_per_read_median"`
	Unpriced   int     `json:"unpriced_reads"`
}

// RouteTakes is one route's takes, work and read, as the primaries' cost records
// hold them (CardCostOf): OK came back with its answer (a work take finished ok, a
// read with its verdict, ok or broken), Provider ended with no work by the provider
// or the child (provider failure, no result), Failed every other end (a work take
// finished failed, a read returned unread). A launch refused at staging ran nothing
// on the route and is not a take of it; a take whose record names no route (a read
// returned unpriced) counts on its card's route. RunWall is over the takes' usage walls.
type RouteTakes struct {
	Route    string  `json:"route"`
	Takes    int     `json:"takes"`
	OK       int     `json:"ok"`
	Failed   int     `json:"failed"`
	Provider int     `json:"provider_failures"`
	RunWall  Measure `json:"run_wall"`
}

// PassStats is the epoch's numbers, members, readers and routes in name order. Since,
// when set, is the last stats tidy (StatsSince): every sample ended at or after it.
type PassStats struct {
	Epoch     uint64       `json:"epoch"`
	Since     time.Time    `json:"since,omitzero"`
	Primaries int          `json:"primaries"`
	Stages    Stages       `json:"stages"`
	Work      []MemberStat `json:"work"`
	Reads     []ReaderStat `json:"reads"`
	Routes    []RouteTakes `json:"routes"`
}

// StatsRecords is the Load extras Stats wants: every work card and read card of
// every primary on the work table, each attempt's, placed or retired, so a read
// asked and moved to another reader is counted against the one first asked.
func StatsRecords(s *Snapshot) map[string][]string {
	var work, reads []string
	for _, p := range statsPrimaries(s) {
		for k := 1; k <= p.Int("attempt"); k++ {
			work = append(work, WorkCardID(p.ID, k))
			for _, r := range s.Readers.Rows() {
				reads = append(reads, ReadCardID(p.ID, k, r))
			}
		}
	}
	return map[string][]string{Fleet: work, Readers: reads}
}

// statsPrimaries is the work table's primaries, sentinels left out.
func statsPrimaries(s *Snapshot) []*Card {
	var out []*Card
	for _, c := range s.Work.Column(States...) {
		if !IsSentinel(c) {
			out = append(out, c)
		}
	}
	return out
}

// StatsSince is Stats from since on (the last stats tidy; zero is the whole epoch): a
// stage counts when it ended at or after since, a work card when its last stamp
// (finished, else taken, else dealt) is, a read card likewise (read, begun, asked), a
// route's take when its record's end is, and a primary when any of its samples counts.
func StatsSince(s *Snapshot, since time.Time) PassStats {
	var deal, toReads, toLand, total []float64
	work, reads, routes := map[string]*samples{}, map[string]*samples{}, map[string]*samples{}
	readCosts := map[string]*readMoney{}
	ps := PassStats{Epoch: s.Epoch, Since: since}
	// span is appendSpan of a stage that ended at or after since
	span := func(xs []float64, a, b time.Time) []float64 {
		if !since.IsZero() && b.Before(since) {
			return xs
		}
		return appendSpan(xs, a, b)
	}
	for _, p := range statsPrimaries(s) {
		if !since.IsZero() && lastStamp(p, "landed", "accepted", "admitted").Before(since) && !anyCardSince(s, p, since) {
			continue
		}
		ps.Primaries++
		admitted, accepted, landed := stampAt(p, "admitted"), stampAt(p, "accepted"), stampAt(p, "landed")
		var lastFinished time.Time
		for k := 1; k <= p.Int("attempt"); k++ {
			if w := s.Fleet.Card(WorkCardID(p.ID, k)); w != nil && !before(w, since, "finished", "taken", "dealt") {
				x := sampleOf(work, cmp.Or(w.F("member"), w.Row, "-"))
				dealt, taken, finished := stampAt(w, "dealt"), stampAt(w, "taken"), stampAt(w, "finished")
				blame := w.F(FieldBlame)
				class := w.F(FieldDefectClass)
				if blame == "" && (w.F("ok") == "no" || w.Col == DoneFailed) {
					blame, class, _, _ = ClassifyAttempt(w.F("report"), true, "")
				}
				switch {
				case w.F("ok") == "yes" || w.Col == DoneOK:
					lastFinished = finished
				case blame == BlameWorker && class != DefectLaunchRefused && !IsProviderFailure(w.F("report")) && !IsNoResult(w.F("report")):
					x.failed++
				}
				x.timed(dealt, taken, finished, RunWall(w))
				if k == 1 {
					deal = span(deal, admitted, stampAt(w, "first_dealt"))
				}
			}
			for _, r := range s.Readers.Rows() {
				if rc := s.Readers.Card(ReadCardID(p.ID, k, r)); rc != nil && !before(rc, since, "read", "begun", "asked") {
					y := sampleOf(reads, cmp.Or(rc.F("reader"), rc.Row, r))
					y.timed(stampAt(rc, "asked"), stampAt(rc, "begun"), stampAt(rc, "read"), cardcost.ParseUsage(rc.F(FieldUsage)).Wall)
				}
			}
		}
		toReads = span(toReads, lastFinished, accepted)
		toLand = span(toLand, accepted, landed)
		total = span(total, admitted, landed)
		for _, c := range CardCostOf(p).Consumers {
			if c.Kind == "work" && strings.HasPrefix(c.End, cardhdr.EndStaging) {
				continue // refused before any child ran: no take of the route
			}
			if at, err := time.Parse(time.RFC3339, c.At); !since.IsZero() && err == nil && at.Before(since) {
				continue // a take that ended before the tidy
			}
			// a read's record names the route that priced it, and a read returned
			// unpriced names none: then the route its card was dealt on
			own := s.Fleet.Card(c.Card)
			if c.Kind == "read" {
				own = s.Readers.Card(c.Card)
				// the reader's money is the primary's record, so a read card retired
				// or gone still counts: who ran it, else the card it was
				row := ""
				if own != nil {
					row = own.Row
				}
				who := cmp.Or(c.Who, own.F("reader"), row, "-")
				m := readCosts[who]
				if m == nil {
					m = &readMoney{}
					readCosts[who] = m
				}
				if usd := cmp.Or(c.Usage.Actual, c.Usage.Predicted); usd != "" && c.Usage.Unpriced != WhySubscription {
					m.costs = append(m.costs, usd)
				} else if c.Usage.Unpriced != WhySubscription {
					m.unpriced++
				}
			}
			name := cmp.Or(c.Route, c.Usage.Route, own.F(FieldRoute), "-")
			if name == RoutePin {
				name += ":" + c.Model
			}
			z := sampleOf(routes, name)
			switch {
			case c.Kind == "read" && (c.End == "ok" || c.End == "broken"), c.Kind == "work" && c.End == "ok":
				z.ok++
			case strings.HasPrefix(c.End, cardhdr.EndProvider), strings.HasPrefix(c.End, cardhdr.EndNoResult):
				z.provider++
			default:
				z.failed++
			}
			if wall, ok := wallSeconds(c.Usage.Wall); ok {
				z.wall = append(z.wall, wall)
			}
		}
	}
	ps.Stages = Stages{DealWait: measure(deal), FinishToReads: measure(toReads), AcceptToLand: measure(toLand), Total: measure(total)}
	ps.Work, ps.Reads, ps.Routes = []MemberStat{}, []ReaderStat{}, []RouteTakes{}
	for _, name := range slices.Sorted(maps.Keys(work)) {
		x := work[name]
		ps.Work = append(ps.Work, MemberStat{Member: name, Cards: x.n, Failed: x.failed, TakeWait: measure(x.wait), RunWall: measure(x.wall), ReportLag: measure(x.lag)})
	}
	readers := map[string]struct{}{}
	for name := range reads {
		readers[name] = struct{}{}
	}
	for name := range readCosts {
		readers[name] = struct{}{}
	}
	for _, name := range slices.Sorted(maps.Keys(readers)) {
		y := reads[name]
		m := readCosts[name]
		st := ReaderStat{Reader: name, Cost: "-", CostMedian: "-"}
		if y != nil {
			st.Cards, st.BeginWait, st.RunWall, st.ReportLag = y.n, measure(y.wait), measure(y.wall), measure(y.lag)
		}
		if m != nil {
			st.Cost, st.CostMedian, st.Unpriced = readCostTotal(m.costs), readCostMedian(m.costs), m.unpriced
		}
		ps.Reads = append(ps.Reads, st)
	}
	for _, name := range slices.Sorted(maps.Keys(routes)) {
		z := routes[name]
		ps.Routes = append(ps.Routes, RouteTakes{Route: name, Takes: z.n, OK: z.ok, Failed: z.failed, Provider: z.provider, RunWall: measure(z.wall)})
	}
	return ps
}

// lastStamp is the first of the card's stamps that is set, zero when none is.
func lastStamp(c *Card, fields ...string) time.Time {
	for _, f := range fields {
		if t := stampAt(c, f); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// before says the card's last stamp (the first of fields set) is before since; never
// with since zero or no stamp.
func before(c *Card, since time.Time, fields ...string) bool {
	t := lastStamp(c, fields...)
	return !since.IsZero() && !t.IsZero() && t.Before(since)
}

// anyCardSince says a work or read card of the primary p ended, or is open, at or after since.
func anyCardSince(s *Snapshot, p *Card, since time.Time) bool {
	for _, c := range CardCostOf(p).Consumers {
		if at, err := time.Parse(time.RFC3339, c.At); err == nil && !at.Before(since) {
			return true // retained costs survive a retired or removed consumer card
		}
	}
	for k := 1; k <= p.Int("attempt"); k++ {
		if w := s.Fleet.Card(WorkCardID(p.ID, k)); w != nil && !before(w, since, "finished", "taken", "dealt") {
			return true
		}
	}
	return false
}

// readMoney is one reader's finished reads, from the primaries' cost records:
// the charged figure of each priced read, and how many carried none.
type readMoney struct {
	costs    []string
	unpriced int
}

// readCostTotal is the charged sum of a reader's priced reads, as a table shows
// money (MoneyText); "-" when none was priced.
func readCostTotal(costs []string) string {
	if len(costs) == 0 {
		return "-"
	}
	sum, ok := cardcost.Sum(costs...)
	if !ok || sum == "" {
		return "-"
	}
	return MoneyText(sum)
}

// readCostMedian is the median charged cost of those reads: the middle one, or
// the mean of the two middle ones when the count is even, the same rule as
// measure, then dollars and cents rounded up. "-" when none was priced.
func readCostMedian(costs []string) string {
	var rats []*big.Rat
	for _, usd := range costs {
		r, err := amountOf(usd)
		if err != nil || r == nil {
			continue
		}
		rats = append(rats, r)
	}
	if len(rats) == 0 {
		return "-"
	}
	slices.SortFunc(rats, func(a, b *big.Rat) int { return a.Cmp(b) })
	n := len(rats)
	med := new(big.Rat).Set(rats[n/2])
	if n%2 == 0 {
		med.Add(rats[n/2-1], rats[n/2])
		med.Quo(med, big.NewRat(2, 1))
	}
	return cardcost.Cents(med)
}

// samples are one member's, reader's or route's cards and their samples, in seconds.
type samples struct {
	n, ok, failed, provider int
	wait, wall, lag         []float64
}

// sampleOf is the name's samples, counted one card more, made on first meeting.
func sampleOf(m map[string]*samples, name string) *samples {
	x := m[name]
	if x == nil {
		x = &samples{}
		m[name] = x
	}
	x.n++
	return x
}

// timed adds a card's wait (from to began), its wall, and its lag (began to ended,
// less the wall), each when its stamps and wall are there.
func (x *samples) timed(from, began, ended time.Time, wall string) {
	x.wait = appendSpan(x.wait, from, began)
	w, ok := wallSeconds(wall)
	if !ok {
		return
	}
	x.wall = append(x.wall, w)
	if !began.IsZero() && !ended.IsZero() {
		x.lag = append(x.lag, ended.Sub(began).Seconds()-w)
	}
}

// stampAt is a stamp field as a time, zero when absent or unreadable.
func stampAt(c *Card, field string) time.Time {
	t, _ := time.Parse(time.RFC3339, c.F(field))
	return t
}

// appendSpan adds the seconds from a to b when both are stamped.
func appendSpan(xs []float64, a, b time.Time) []float64 {
	if a.IsZero() || b.IsZero() {
		return xs
	}
	return append(xs, b.Sub(a).Seconds())
}

// wallSeconds is a usage wall ("19.00s", cardcost.Usage.Wall) in seconds, false when
// there is none.
func wallSeconds(wall string) (float64, bool) {
	d, err := time.ParseDuration(wall)
	return d.Seconds(), err == nil
}

// measure is the samples' median (the mean of the middle two of an even count),
// max and count.
func measure(xs []float64) Measure {
	if len(xs) == 0 {
		return Measure{}
	}
	xs = slices.Sorted(slices.Values(xs))
	n := len(xs)
	med := xs[n/2]
	if n%2 == 0 {
		med = (xs[n/2-1] + xs[n/2]) / 2
	}
	return Measure{Median: med, Max: xs[n-1], N: n}
}

// FieldReported is when a work card's report was written, as the transport knew it (a
// friend's REPORT.md: FinishReq.Reported), no later than its finish.
const FieldReported = "reported"

// RunWall is a work card's run wall as a duration's text: its usage's wall (what the
// member read from its child), else, for a friend's card, which reports no usage, her take
// to her report (FieldReported), so her report lag is her report to its finish; "" when
// neither is there.
func RunWall(c *Card) string {
	if w := cardcost.ParseUsage(c.F(FieldUsage)).Wall; w != "" {
		return w
	}
	taken, reported := stampAt(c, "taken"), stampAt(c, FieldReported)
	if taken.IsZero() || reported.IsZero() || reported.Before(taken) {
		return ""
	}
	return reported.Sub(taken).String()
}
