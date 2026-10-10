package sprint

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/big"
	"slices"
	"strings"
	"time"
)

// InternalSpendSplitSince is internalSpendSplit recounting only consumer records whose
// timestamp con.At falls on day and, when since is non-zero, at or after since.
func InternalSpendSplitSince(s *Snapshot, provider, day string, since time.Time) (all, reads float64) {
	if s.Work == nil {
		return 0, 0
	}
	routes := map[string]string{}
	for _, r := range s.Routes {
		routes[r.Name] = r.Provider
	}
	sum, readSum := new(big.Rat), new(big.Rat)
	for _, c := range s.Work.Column(States...) {
		if IsSentinel(c) {
			continue
		}
		for _, con := range CardCostOf(c).Consumers {
			at, err := time.Parse(time.RFC3339, con.At)
			if err != nil || at.UTC().Format(time.DateOnly) != day || !strings.EqualFold(consumerProvider(routes, con), provider) {
				continue
			}
			if !since.IsZero() && at.Before(since) {
				continue
			}
			if usd, err := amountOf(cmp.Or(con.Usage.Actual, con.Usage.Predicted)); err == nil && usd != nil {
				sum.Add(sum, usd)
				if con.Kind == "read" {
					readSum.Add(readSum, usd)
				}
			}
		}
	}
	all, _ = sum.Float64()
	reads, _ = readSum.Float64()
	return all, reads
}

// CostReconcileSince is CostReconcile recounting the window since the tidy when since is
// non-zero, or the whole day when zero.
func CostReconcileSince(s *Snapshot, r CostReconcileReq, since time.Time) Plan {
	if since.IsZero() {
		return CostReconcile(s, r)
	}
	var p Plan
	reads := slices.Clone(r.Reads)
	slices.SortFunc(reads, func(a, b UsageRead) int { return strings.Compare(a.Provider, b.Provider) })
	var said []string
	for _, rd := range reads {
		was, _ := CostReconcileOf(s.Fleet, rd.Provider)
		rec := CostReconcileRecord{Provider: rd.Provider, At: stamp(s.Now), Known: rd.Known, Note: rd.Note, Days: maps.Clone(was.Days)}
		if !rd.Known {
			rec.Day, rec.Used, rec.Internal, rec.InternalReads, rec.Gap, rec.Share = was.Day, was.Used, was.Internal, was.InternalReads, was.Gap, was.Share
		} else {
			rec.Day, rec.Used = rd.Day, rd.Used
			rec.Internal, rec.InternalReads = InternalSpendSplitSince(s, rd.Provider, rd.Day, since)
			rec.Gap = rec.Used - rec.Internal
			switch {
			case rec.Used > 0:
				rec.Share = math.Abs(rec.Gap) / rec.Used
			case rec.Internal > 0:
				rec.Share = 1
			}
			if rec.Days == nil {
				rec.Days = map[string]DayGap{}
			}
			rec.Days[rd.Day] = DayGap{Provider: rec.Used, Internal: rec.Internal}
			for _, d := range slices.Sorted(maps.Keys(rec.Days)) {
				if len(rec.Days) <= MaxReconcileDays {
					break
				}
				delete(rec.Days, d)
			}
		}
		val, _ := json.Marshal(rec)
		prior, had := s.Fleet.Prop(PropCostReconcile(rd.Provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropCostReconcile(rd.Provider), Value: string(val), Was: prior, WasAbsent: !had})
		if !rd.Known {
			said = append(said, rd.Provider+" unknown")
			continue
		}
		said = append(said, fmt.Sprintf("%s day=%s provider=%s records=%s reads=%s gap=%.1f%%", rd.Provider, rd.Day, Dollars(rec.Used), Dollars(rec.Internal), Dollars(rec.InternalReads), rec.Share*100))
		var open []Open
		for _, o := range s.Open {
			if o.Note.Type == NCostGap && o.Note.StreamLevel && o.Note.Stream == ProviderSubject(rd.Provider) {
				open = append(open, o)
			}
		}
		over := rec.Share > CostGapOver && math.Abs(rec.Gap) >= CostGapFloor
		switch {
		case over && len(open) == 0:
			n := judgment(NCostGap, ProviderSubject(rd.Provider), s.Now, 0)
			n.Decisions = append([]string(nil), CostGapDecisions...)
			n.StreamLevel, n.To, n.Who = true, s.Coordinator, r.Who
			n.What = fmt.Sprintf("provider %s counted %s on %s and the sprint's cost records of it hold %s: a gap of %s, %.1f%% (over %.0f%%); %s; a paid call is going unrecorded, a route's prices are under its provider's list, or the key is spent outside the sprint; nova-sprint routes and card <id> show the records",
				rd.Provider, Dollars(rec.Used), rd.Day, Dollars(rec.Internal), Dollars(math.Abs(rec.Gap)), rec.Share*100, CostGapOver*100, readShareOfGap(rec))
			p.Notes = append(p.Notes, n)
		case !over && len(open) > 0:
			p.Closes = append(p.Closes, open...)
		}
	}
	p.Units = append(p.Units, Unit{Key: "cost reconcile", Moved: "provider usage: " + strings.Join(said, "; ")})
	return p
}
