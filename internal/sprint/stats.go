package sprint

import (
	"cmp"
	"maps"
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
type ReaderStat struct {
	Reader    string  `json:"reader"`
	Cards     int     `json:"cards"`
	BeginWait Measure `json:"begin_wait"`
	RunWall   Measure `json:"run_wall"`
	ReportLag Measure `json:"report_lag"`
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

// PassStats is the epoch's numbers, members, readers and routes in name order.
type PassStats struct {
	Epoch     uint64       `json:"epoch"`
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

// Stats is the pass's numbers over the snapshot's work, fleet and readers tables
// (loaded with StatsRecords).
func Stats(s *Snapshot) PassStats {
	var deal, toReads, toLand, total []float64
	work, reads, routes := map[string]*samples{}, map[string]*samples{}, map[string]*samples{}
	ps := PassStats{Epoch: s.Epoch}
	for _, p := range statsPrimaries(s) {
		ps.Primaries++
		admitted, accepted, landed := stampAt(p, "admitted"), stampAt(p, "accepted"), stampAt(p, "landed")
		var lastFinished time.Time
		for k := 1; k <= p.Int("attempt"); k++ {
			if w := s.Fleet.Card(WorkCardID(p.ID, k)); w != nil {
				x := sampleOf(work, cmp.Or(w.F("member"), w.Row, "-"))
				dealt, taken, finished := stampAt(w, "dealt"), stampAt(w, "taken"), stampAt(w, "finished")
				switch w.F("ok") {
				case "yes":
					lastFinished = finished
				case "no":
					x.failed++
				}
				x.timed(dealt, taken, finished, cardcost.ParseUsage(w.F(FieldUsage)).Wall)
				if k == 1 {
					deal = appendSpan(deal, admitted, stampAt(w, "first_dealt"))
				}
			}
			for _, r := range s.Readers.Rows() {
				if rc := s.Readers.Card(ReadCardID(p.ID, k, r)); rc != nil {
					y := sampleOf(reads, cmp.Or(rc.F("reader"), rc.Row, r))
					y.timed(stampAt(rc, "asked"), stampAt(rc, "begun"), stampAt(rc, "read"), cardcost.ParseUsage(rc.F(FieldUsage)).Wall)
				}
			}
		}
		toReads = appendSpan(toReads, lastFinished, accepted)
		toLand = appendSpan(toLand, accepted, landed)
		total = appendSpan(total, admitted, landed)
		for _, c := range CardCostOf(p).Consumers {
			if c.Kind == "work" && strings.HasPrefix(c.End, cardhdr.EndStaging) {
				continue // refused before any child ran: no take of the route
			}
			// a read's record names the route that priced it, and a read returned
			// unpriced names none: then the route its card was dealt on
			own := s.Fleet.Card(c.Card)
			if c.Kind == "read" {
				own = s.Readers.Card(c.Card)
			}
			name := cmp.Or(c.Route, c.Usage.Route, own.F(FieldRoute), "-")
			if name == RoutePin {
				name += ":" + c.Model
			}
			z := sampleOf(routes, name)
			switch {
			case c.Kind == "read" && c.End != "returned", c.Kind == "work" && c.End == "ok":
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
	for _, name := range slices.Sorted(maps.Keys(reads)) {
		y := reads[name]
		ps.Reads = append(ps.Reads, ReaderStat{Reader: name, Cards: y.n, BeginWait: measure(y.wait), RunWall: measure(y.wall), ReportLag: measure(y.lag)})
	}
	for _, name := range slices.Sorted(maps.Keys(routes)) {
		z := routes[name]
		ps.Routes = append(ps.Routes, RouteTakes{Route: name, Takes: z.n, OK: z.ok, Failed: z.failed, Provider: z.provider, RunWall: measure(z.wall)})
	}
	return ps
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
