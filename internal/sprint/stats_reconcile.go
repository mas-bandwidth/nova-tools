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

// ReconcileBase is a provider's reconciliation baseline at a tidy: the day and the provider
// figure of its last read before the tidy, and the time of that read. A cost reconcile after
// the tidy counts the provider's figure since the read beside the sprint's records since it
// (CostReconcileSince), so the provider's count and the records are the same window; the
// whole day's provider figure is never set beside only the window's records (the reader's
// finding, 2026-10-06: stats_reconcile.go compared a full day's provider figure with only the
// window's records).
type ReconcileBase struct {
	Day      string    `json:"day,omitempty"`
	At       time.Time `json:"at,omitzero"`
	Provider float64   `json:"provider"`
}

// ReconcileBases is each provider's baseline at a tidy, from its reconciliation record on the
// fleet table (CostReconcileRecord): the day, the read and the provider's figure it counted.
// No provider is named with no read or an unknown one.
func ReconcileBases(s *Snapshot) map[string]ReconcileBase {
	out := map[string]ReconcileBase{}
	if s.Fleet == nil {
		return out
	}
	for name := range s.Fleet.Props() {
		provider, ok := strings.CutPrefix(name, PropCostReconcilePrefix)
		if !ok {
			continue
		}
		rec, ok := CostReconcileOf(s.Fleet, provider)
		if !ok || !rec.Known || rec.Day == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, rec.At)
		if err != nil {
			continue
		}
		out[provider] = ReconcileBase{Day: rec.Day, At: at, Provider: rec.Used}
	}
	return out
}

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

// CostReconcileSince is CostReconcile with the window a tidy opened: for a provider whose
// baseline is here and whose day is the read's, the provider's figure since that read (its
// own count less the baseline's) is set beside the sprint's records since it
// (InternalSpendSplitSince), so both are the same window. A provider with no baseline, or one
// whose baseline is another day's, is the whole day's, as CostReconcile is. The day kept in
// the record's Days is the whole day's either way: the unreconciled line is the epoch's.
func CostReconcileSince(s *Snapshot, r CostReconcileReq, bases map[string]ReconcileBase) Plan {
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
			full, fullReads := internalSpendSplit(s, rd.Provider, rd.Day)
			rec.Day, rec.Used, rec.Internal, rec.InternalReads = rd.Day, rd.Used, full, fullReads
			if b, ok := bases[rd.Provider]; ok && b.Day == rd.Day {
				rec.Used = rd.Used - b.Provider
				rec.Internal, rec.InternalReads = InternalSpendSplitSince(s, rd.Provider, rd.Day, b.At)
			}
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
			rec.Days[rd.Day] = DayGap{Provider: rd.Used, Internal: full}
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
