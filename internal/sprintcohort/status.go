// Package sprintcohort summarizes an explicitly selected set of sprint primaries.
// The status contract is docs/SPEC-SPRINT.md, Cohort inspection.
package sprintcohort

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Selection names one exact stream or an explicit set of primary identities.
type Selection struct {
	Stream string   `json:"stream,omitempty"`
	IDs    []string `json:"ids,omitempty"`
}

// Validate refuses ambiguous selection and non-primary identities.
func (q Selection) Validate() error {
	if (q.Stream == "") == (len(q.IDs) == 0) {
		return fmt.Errorf("give exactly one of --stream, --ids or --manifest")
	}
	if q.Stream != "" && !sprint.ValidID(q.Stream) {
		return fmt.Errorf("--stream wants an exact stream identity")
	}
	if len(q.IDs) > 10000 {
		return fmt.Errorf("a cohort holds at most 10000 primary identities")
	}
	seen := map[string]bool{}
	for _, id := range q.IDs {
		if !sprint.ValidID(id) || seen[id] {
			return fmt.Errorf("invalid or repeated primary identity %q", id)
		}
		seen[id] = true
	}
	return nil
}

// Primaries selects exact identities; sentinel exclusions and missing identities
// remain explicit so they cannot silently change the denominator.
func Primaries(s *sprint.Snapshot, q Selection) (out []*sprint.Card, missing, excluded []string) {
	want := map[string]bool{}
	for _, id := range q.IDs {
		want[id] = true
	}
	for _, p := range s.Work.Column(sprint.States...) {
		if q.Stream != "" && p.Row != q.Stream || q.Stream == "" && !want[p.ID] {
			continue
		}
		delete(want, p.ID)
		if sprint.IsSentinel(p) {
			excluded = append(excluded, p.ID)
			continue
		}
		out = append(out, p)
	}
	for id := range want {
		missing = append(missing, id)
	}
	slices.SortFunc(out, func(a, b *sprint.Card) int { return strings.Compare(a.ID, b.ID) })
	slices.Sort(missing)
	slices.Sort(excluded)
	return
}

// Records adds historical work/read records in batch to a table load. It uses
// exact selected primaries, current reader rows and archived consumer identities.
func Records(q Selection) func(*sprint.Snapshot) map[string][]string {
	return func(s *sprint.Snapshot) map[string][]string {
		ps, _, _ := Primaries(s, q)
		ids := map[string]map[string]bool{sprint.Fleet: {}, sprint.Readers: {}, sprint.Merge: {}}
		for _, p := range ps {
			ids[sprint.Merge][p.ID] = true
			for k := 1; k <= p.Int("attempt"); k++ {
				ids[sprint.Fleet][sprint.WorkCardID(p.ID, k)] = true
				for _, r := range s.Readers.Rows() {
					ids[sprint.Readers][sprint.ReadCardID(p.ID, k, r)] = true
				}
			}
			for _, c := range sprint.CardCostOf(p).Consumers {
				if c.Kind == "work" {
					ids[sprint.Fleet][c.Card] = true
				}
				if c.Kind == "read" {
					ids[sprint.Readers][c.Card] = true
				}
			}
		}
		out := map[string][]string{}
		for table, set := range ids {
			for id := range set {
				out[table] = append(out[table], id)
			}
			slices.Sort(out[table])
		}
		return out
	}
}

// Outcomes counts finished verdicts separately from pending or absent evidence.
type Outcomes struct {
	OK      int `json:"ok"`
	Failed  int `json:"failed"`
	Broken  int `json:"broken"`
	Pending int `json:"pending"`
	Retired int `json:"retired_unread"`
}

// Row retains source records rather than guessing details from table totals.
type Row struct {
	ID            string              `json:"id"`
	State         string              `json:"state"`
	Stream        string              `json:"stream"`
	Attempt       int                 `json:"attempt"`
	Primary       *sprint.Card        `json:"primary"`
	Work          []*sprint.Card      `json:"work"`
	Reads         []*sprint.Card      `json:"reads"`
	Merge         *sprint.Card        `json:"merge,omitempty"`
	FirstWork     string              `json:"first_work"`
	FirstReads    Outcomes            `json:"first_attempt_reads"`
	Cost          sprint.CardCostView `json:"cost"`
	LandingTarget string              `json:"landing_target"`
	BranchStaged  bool                `json:"branch_staged"`
	DevLanded     *bool               `json:"dev_landed"`
	DevPromotion  string              `json:"dev_promotion"`
}

// Costs sums producer totals, including records cut from consumer history.
// It describes recorded completed consumers; live spend and invoices are unknown.
type Costs struct {
	MissingPrimaryTotals int      `json:"primaries_without_cost_total"`
	Source               string   `json:"source"`
	Charged              string   `json:"charged_usd"`
	Predicted            string   `json:"predicted_usd"`
	Actual               string   `json:"actual_usd"`
	ActualBy             []string `json:"actual_by"`
	Records              int      `json:"records"`
	ChargedOf            int      `json:"charged_records"`
	ActualOf             int      `json:"actual_records"`
	PredictedOf          int      `json:"predicted_records"`
	Cut                  int      `json:"history_cut"`
	CompleteInvoice      bool     `json:"complete_invoice"`
	InflightSpend        string   `json:"inflight_spend"`
}

// Status is one value rendered by both the text and JSON paths.
type Status struct {
	DevLandedVerified int            `json:"dev_landed_verified"`
	DevLandingUnknown int            `json:"dev_landing_unknown"`
	BranchStaged      int            `json:"branch_staged"`
	Epoch             uint64         `json:"epoch"`
	Selection         Selection      `json:"selection"`
	Primaries         int            `json:"primaries"`
	Missing           []string       `json:"missing"`
	Excluded          []string       `json:"excluded_sentinels"`
	States            map[string]int `json:"states"`
	FirstWork         Outcomes       `json:"first_attempt_work"`
	AllWork           Outcomes       `json:"all_attempt_work"`
	Reads             Outcomes       `json:"all_read_verdicts"`
	FirstReads        Outcomes       `json:"first_attempt_reads"`
	WorkAttempts      int            `json:"work_attempts"`
	RetryAttempts     int            `json:"retry_attempts"`
	RetryCards        int            `json:"retry_cards"`
	RecordedWorkTakes int            `json:"recorded_work_takes"`
	ProviderReturns   int            `json:"provider_or_no_result_returns"`
	ReadReturns       int            `json:"read_returns"`
	Cost              Costs          `json:"cost"`
	Rows              []Row          `json:"rows"`
}

// Summarize follows the cohort inspection contract. First work is w1's final
// verdict, not its first provider launch; first reads are all r1 verdicts.
func Summarize(s *sprint.Snapshot, q Selection) Status {
	ps, missing, excluded := Primaries(s, q)
	v := Status{Epoch: s.Epoch, Selection: q, Primaries: len(ps), Missing: nonNil(missing), Excluded: nonNil(excluded), States: map[string]int{}, Rows: []Row{}, Cost: Costs{Source: "primary cost_total: completed consumer records; actual when reported, otherwise predicted", ActualBy: []string{}, InflightSpend: "unknown"}}
	workBy, readBy := byPrimary(s.Fleet), byPrimary(s.Readers)
	for _, p := range ps {
		row := Row{ID: p.ID, State: p.Col, Stream: p.Row, Attempt: p.Int("attempt"), Primary: copyCard(p), Work: []*sprint.Card{}, Reads: []*sprint.Card{}, Cost: sprint.CardCostOf(p), FirstWork: "pending", BranchStaged: p.Col == sprint.Landed, DevPromotion: "unknown: no dev ancestry or promotion receipt checked"}
		row.LandingTarget = landingTarget(p)
		if p.F("staged_base") != "" {
			row.LandingTarget = p.F("staged_base")
			row.BranchStaged = true
		}
		if devReceipt(p) {
			yes := true
			row.DevLanded = &yes
			row.DevPromotion = "verified receipt"
			v.DevLandedVerified++
		} else {
			v.DevLandingUnknown++
		}
		if row.BranchStaged {
			v.BranchStaged++
		}
		if c := s.Merge.Card(p.ID); c != nil {
			row.Merge = copyCard(c)
			if c.F("base") != "" && p.F("staged_base") == "" {
				row.LandingTarget = c.F("base")
			}
		}
		v.States[p.Col]++
		if row.Attempt > 1 {
			v.RetryCards++
			v.RetryAttempts += row.Attempt - 1
		}
		v.WorkAttempts += row.Attempt
		for _, c := range workBy[p.ID] {
			row.Work = append(row.Work, copyCard(c))
			addWork(&v.AllWork, c.F("ok"))
			_, attempt, ok := sprint.ParseWorkCard(c.ID)
			if ok && attempt == 1 {
				row.FirstWork = workWord(c.F("ok"))
			}
		}
		switch row.FirstWork {
		case "ok":
			v.FirstWork.OK++
		case "failed":
			v.FirstWork.Failed++
		default:
			v.FirstWork.Pending++
		}
		for _, c := range readBy[p.ID] {
			row.Reads = append(row.Reads, copyCard(c))
			if c.F("verdict") == "" && c.F("retired") != "" {
				v.Reads.Retired++
				_, attempt, _, ok := sprint.ParseReadCard(c.ID)
				if ok && attempt == 1 {
					row.FirstReads.Retired++
					v.FirstReads.Retired++
				}
				continue
			} // retired unread records are not pending independent verdicts
			addRead(&v.Reads, c.F("verdict"))
			_, attempt, _, ok := sprint.ParseReadCard(c.ID)
			if ok && attempt == 1 {
				addRead(&row.FirstReads, c.F("verdict"))
				addRead(&v.FirstReads, c.F("verdict"))
			}
		}
		slices.SortFunc(row.Work, cardOrder)
		slices.SortFunc(row.Reads, cardOrder)
		for _, c := range row.Cost.Consumers {
			if c.Kind == "work" && c.End != "staging refused" {
				v.RecordedWorkTakes++
				if strings.HasPrefix(c.End, "provider failure") || strings.HasPrefix(c.End, "no result") {
					v.ProviderReturns++
				}
			}
			if c.Kind == "read" && c.End == "returned" {
				v.ReadReturns++
			}
		}
		if !p.Has(sprint.FieldCostTotal) {
			v.Cost.MissingPrimaryTotals++
		}
		total := row.Cost.Total
		v.Cost.Records += total.Records
		v.Cost.ChargedOf += total.ChargedOf
		v.Cost.ActualOf += total.ActualOf
		v.Cost.PredictedOf += total.PredOf
		v.Cost.Cut += row.Cost.Cut
		v.Cost.Charged, _ = cardcost.Sum(v.Cost.Charged, total.Charged)
		v.Cost.Actual, _ = cardcost.Sum(v.Cost.Actual, total.Actual)
		v.Cost.Predicted, _ = cardcost.Sum(v.Cost.Predicted, total.Predicted)
		for _, by := range cardcost.Words(total.ActualBy) {
			if !slices.Contains(v.Cost.ActualBy, by) {
				v.Cost.ActualBy = append(v.Cost.ActualBy, by)
			}
		}
		v.Rows = append(v.Rows, row)
	}
	slices.Sort(v.Cost.ActualBy)
	return v
}

func landingTarget(c *sprint.Card) string {
	if c.F("base") != "" {
		return c.F("base")
	}
	for _, line := range strings.Split(c.F("brief"), "\n") {
		if key, value, ok := cardhdr.KeyValue(line); ok && key == "BASE" {
			return value
		}
	}
	return "unknown"
}
func copyCard(c *sprint.Card) *sprint.Card { out := *c; out.Fields = maps.Clone(c.Fields); return &out }
func cardOrder(a, b *sprint.Card) int      { return strings.Compare(a.ID, b.ID) }
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
func workWord(ok string) string {
	switch ok {
	case "yes":
		return "ok"
	case "no":
		return "failed"
	}
	return "pending"
}
func addWork(o *Outcomes, ok string) {
	switch workWord(ok) {
	case "ok":
		o.OK++
	case "failed":
		o.Failed++
	default:
		o.Pending++
	}
}
func addRead(o *Outcomes, verdict string) {
	switch verdict {
	case "ok":
		o.OK++
	case "broken":
		o.Broken++
	default:
		o.Pending++
	}
}

// devReceipt requires the lifecycle's explicit reviewed dev verification record;
// a legacy table place or a staged base receipt cannot prove promotion.
func devReceipt(p *sprint.Card) bool {
	_, err := time.Parse(time.RFC3339Nano, p.F("dev_verified_at"))
	return p.F("dev_branch") == "dev" && p.F("dev_repo") != "" && p.F("dev_review") != "" && err == nil && fullSHA(p.F("dev_tip")) && fullSHA(p.F("dev_head")) && p.F("dev_head") == p.F("head") && p.Int("dev_attempt") == p.Int("attempt")
}
func fullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func byPrimary(t *sprint.Table) map[string][]*sprint.Card {
	out := map[string][]*sprint.Card{}
	for _, c := range t.Cards() {
		if p := c.F(sprint.PrimaryField); p != "" {
			out[p] = append(out[p], c)
		}
	}
	return out
}
