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

	"github.com/mas-bandwidth/nova-tools/pkg/provbalance"
)

// THE COST RECONCILIATION (docs/SPEC-SPRINT.md, "What a card cost", the reconciliation; the
// owner, 2026-10-04: "this is a tragedy. we MUST track the complete cost of what we do on the
// fleet and friends on API plans."). `nova-sprint cost reconcile` reads each provider's own
// count of the dollars its key used today (openrouter's GET /api/v1/key, data.usage_daily,
// the UTC day: pkg/provbalance.ReadUsage) and runs this step once; the release's spend
// check sets the same records beside each provider's own over the release's window
// (pkg/release/spendcheck.go), and the run loop's hourly call (CostReconcileEvery) is
// still owed. This
// step sets it beside
// the sprint's own records of that provider for the same UTC day: every consumer record on
// every primary (a work card's take or a read's run, whatever its end) whose provider is
// that one and whose end stamp falls on that day, at its charged figure (the harness's cost,
// else its tokens at the route's prices). Like is compared with like: a day against the same
// day, never a day against all time.
//
//   - the read and the comparison are written to the fleet table's property
//     cost_reconcile_<provider> (CostReconcileRecord), keeping the last read of each day,
//     the days of the last MaxReconcileDays;
//   - a gap over CostGapOver of the provider's figure, and at least CostGapFloor dollars,
//     opens ONE judgment on the provider (NCostGap, filed under ProviderSubject as its funds
//     judgments are), never a
//     second while it is open; a read back within the bound closes it;
//   - what is unreconciled (UnreconciledSpend) is, over the days since the sprint's epoch
//     began, what each provider counted beyond the sprint's records on its last read of the
//     day: the dashboard's cost is the records' total plus that, on a line of its own.
//
// A provider with no usage endpoint, no key, or an answer not of the shape is recorded
// unknown with why; it changes no judgment and no day.

// CostReconcileEvery is how often the run loop is to reconcile the providers' usage.
const CostReconcileEvery = time.Hour

// CostGapOver is the share of the provider's figure a gap must pass to open the judgment.
const CostGapOver = 0.05

// CostGapFloor is the least gap in dollars that opens the judgment: just after midnight UTC
// a day's figures are cents, and a cent's difference is no gap to judge.
const CostGapFloor = 1.0

// MaxReconcileDays bounds the days a provider's record keeps.
const MaxReconcileDays = 62

// NCostGap is the judgment of a provider whose own count of a day's usage and the sprint's
// cost records of that day differ past the bound.
const NCostGap = "a provider's usage and the sprint's cost records disagree"

// PropCostReconcilePrefix is the fleet table property prefix of a provider's reconciliation.
const PropCostReconcilePrefix = "cost_reconcile_"

// CostGapDecisions are NCostGap's decisions, the owner's: the cause is a cost not recorded,
// which no rework of a card fixes. They join Decisions, with the judgment's line in
// nova-sprint's help, in the change that starts the run loop's reconciliation
// (cmd/nova-sprint, owed: docs/SPEC-SPRINT.md, "What a card cost"); until then the judgment
// is opened only by `nova-sprint cost reconcile`, carrying these decisions itself.
var CostGapDecisions = []string{"ack", "wait"}

// PropCostReconcile is the fleet table property of a provider's reconciliation.
func PropCostReconcile(provider string) string { return PropCostReconcilePrefix + provider }

// UsageRead is one provider's own count of a UTC day's usage as the run loop read it: Known
// false with Note saying why when there was none to read.
// It is provbalance's, the transport the reconciliation reads through.
type UsageRead = provbalance.UsageRead

// CostReconcileReq is the reads of one reconciliation.
type CostReconcileReq struct {
	Reads []UsageRead
	Who   string
}

// DayGap is one day's last reconciliation: the provider's count and the sprint's records.
type DayGap struct {
	Provider float64 `json:"provider"`
	Internal float64 `json:"internal"`
}

// CostReconcileRecord is a provider's reconciliation as the fleet table keeps it.
type CostReconcileRecord struct {
	Provider string  `json:"provider"`
	At       string  `json:"at"`
	Known    bool    `json:"known"`
	Note     string  `json:"note,omitempty"`
	Day      string  `json:"day,omitempty"`
	Used     float64 `json:"used"`     // the provider's count of Day
	Internal float64 `json:"internal"` // the sprint's records of Day
	// InternalReads is the reads among Internal: the read share of the records.
	InternalReads float64           `json:"internal_reads"`
	Gap           float64           `json:"gap"`   // Used less Internal
	Share         float64           `json:"share"` // |Gap| over Used
	Days          map[string]DayGap `json:"days,omitempty"`
}

// CostReconcileOf reads a provider's record off the fleet table; false when there is none
// or it does not read.
func CostReconcileOf(fleet *Table, provider string) (CostReconcileRecord, bool) {
	var r CostReconcileRecord
	if fleet == nil {
		return r, false
	}
	v, ok := fleet.Prop(PropCostReconcile(provider))
	if !ok || json.Unmarshal([]byte(v), &r) != nil {
		return CostReconcileRecord{}, false
	}
	return r, true
}

// consumerProvider is the provider a consumer record ran on: its route's, else the
// provider/model it reported.
func consumerProvider(routes map[string]string, c Consumer) string {
	if p := routes[cmp.Or(c.Route, c.Usage.Route)]; p != "" {
		return p
	}
	p, _, _ := strings.Cut(cmp.Or(c.Model, c.Usage.Model), "/")
	return p
}

// internalSpendSplit is InternalSpendOn's sum and the reads' part of it.
func internalSpendSplit(s *Snapshot, provider, day string) (all, reads float64) {
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

// readShareOfGap is the judgment's words on the reads: their part of the records and, the
// gap spread as the records are, their part of the gap. The gap is what no record holds, so
// which kind it is cannot be read off the records; spread by the records' own split is the
// estimate, said as one, and a price row set under its provider's list (a read route's
// prices too low) shows as a gap of exactly this kind (docs/SPEC-SPRINT.md, "What a card
// cost", the price rows).
func readShareOfGap(rec CostReconcileRecord) string {
	if rec.Internal <= 0 {
		return "the records hold nothing of the day, so no part of the gap is set against reads"
	}
	share := rec.InternalReads / rec.Internal
	return fmt.Sprintf("reads are %s of the records (%.1f%%, work %s), so spread as the records are, about %s of the gap is reads; nova-sprint where --json readers show each reader's spend",
		Dollars(rec.InternalReads), share*100, Dollars(rec.Internal-rec.InternalReads), Dollars(math.Abs(rec.Gap)*share))
}

// CostReconcile is the reconciliation's step: each read written to its provider's record
// beside the sprint's records of the same day, and the provider's one judgment opened past
// the bound or closed within it (see above).
func CostReconcile(s *Snapshot, r CostReconcileReq) Plan {
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
			rec.Internal, rec.InternalReads = internalSpendSplit(s, rd.Provider, rd.Day)
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

// UnreconciledSpend is what the providers counted beyond the sprint's records, in dollars:
// over every provider's record, each day since the sprint's epoch began, the last read of the
// day's provider figure less the records', where it is more.
func UnreconciledSpend(s *Snapshot) float64 {
	if s.Fleet == nil {
		return 0
	}
	since := ""
	if !s.Cleared.IsZero() {
		since = s.Cleared.UTC().Format(time.DateOnly)
	}
	total := 0.0
	for name := range s.Fleet.Props() {
		provider, ok := strings.CutPrefix(name, PropCostReconcilePrefix)
		if !ok {
			continue
		}
		rec, ok := CostReconcileOf(s.Fleet, provider)
		if !ok {
			continue
		}
		for day, g := range rec.Days {
			if day >= since && g.Provider > g.Internal {
				total += g.Provider - g.Internal
			}
		}
	}
	return total
}

// LatestReconciles is each provider's latest reconciliation off the fleet table, in provider
// order, without its kept days: what the where view shows per provider (where --json
// streams[<s>].reconciles, the sprint's, the same on every stream's record).
func LatestReconciles(s *Snapshot) []CostReconcileRecord {
	if s.Fleet == nil {
		return nil
	}
	var out []CostReconcileRecord
	for _, name := range slices.Sorted(maps.Keys(s.Fleet.Props())) {
		provider, ok := strings.CutPrefix(name, PropCostReconcilePrefix)
		if !ok {
			continue
		}
		if rec, ok := CostReconcileOf(s.Fleet, provider); ok {
			rec.Days = nil
			out = append(out, rec)
		}
	}
	return out
}
