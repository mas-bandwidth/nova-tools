package pulse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Funnel lifecycle event constants (Essential 9, issue #2380).
const (
	EventAdmit   = "ADMIT"
	EventLaunch  = "LAUNCH"
	EventHarvest = "HARVEST"
	EventGate    = "GATE"
	EventLand    = "LAND"
	EventVoid    = "VOID"
)

// FunnelFileName is the canonical accounting log under queue/funnel/.
const FunnelFileName = "events.tsv"

const funnelHeader = "# at\tevent\tcard\tattempt\tmodel\tbench\tverdict\tspend\tdetail\n"

// ValidEvents enumerates the allowed card lifecycle stages.
var ValidEvents = []string{
	EventAdmit,
	EventLaunch,
	EventHarvest,
	EventGate,
	EventLand,
	EventVoid,
}

// FunnelRecord is one entry in the append-only card lifecycle accounting log.
type FunnelRecord struct {
	At      string `json:"at"`
	Event   string `json:"event"`
	Card    string `json:"card"`
	Attempt string `json:"attempt"`
	Model   string `json:"model"`
	Bench   string `json:"bench"`
	Verdict string `json:"verdict"`
	Spend   string `json:"spend"`
	Detail  string `json:"detail"`
}

// FunnelSummary aggregates the lifecycle conversion rates and spending metrics.
type FunnelSummary struct {
	TotalRecords     int     `json:"total_records"`
	AdmittedCards    int     `json:"admitted_cards"`
	LaunchedCards    int     `json:"launched_cards"`
	HarvestedCards   int     `json:"harvested_cards"`
	GatedCards       int     `json:"gated_cards"`
	LandedCards      int     `json:"landed_cards"`
	VoidedCards      int     `json:"voided_cards"`
	VoidedAttempts   int     `json:"voided_attempts"`
	MeasuredSpend    float64 `json:"measured_spend"`
	HasMeasuredSpend bool    `json:"has_measured_spend"`
	UnmeasuredEvents int     `json:"unmeasured_events"`
	SpendOverflow    bool    `json:"spend_overflow"`
	// PricedCards is the admitted cards with at least one record carrying a measured
	// spend (#3159). Below AdmittedCards, CostPerLanded is a lower bound.
	PricedCards   int    `json:"priced_cards"`
	CostPerLanded string `json:"cost_per_landed"`
}

// CostPerLandedFigure is cost_per_landed as the lines print it after the name (#3159): the
// number is exact only when every admitted card is priced, `=<x> coverage=100.00% (<n>/<n>)`;
// below that it is a lower bound, `>=<x> coverage=<p>% (<priced>/<cards>)`. The dash stays
// `=-`, with no coverage, because there is nothing to bound.
func (s FunnelSummary) CostPerLandedFigure() string {
	if s.CostPerLanded == "" || s.CostPerLanded == "-" || s.AdmittedCards == 0 {
		return "=-"
	}
	op := ">="
	if s.PricedCards == s.AdmittedCards {
		op = "="
	}
	return fmt.Sprintf("%s%s coverage=%.2f%% (%d/%d)", op, s.CostPerLanded,
		100*float64(s.PricedCards)/float64(s.AdmittedCards), s.PricedCards, s.AdmittedCards)
}

// OneLine formats the funnel summary into the standard one-line console reading.
func (s FunnelSummary) OneLine() string {
	spendStr := "-"
	if s.HasMeasuredSpend {
		spendStr = fmt.Sprintf("%.4f", s.MeasuredSpend)
	}
	line := fmt.Sprintf("FUNNEL admit=%d launch=%d harvest=%d gate=%d land=%d void=%d spend=%s cost_per_landed%s",
		s.AdmittedCards, s.LaunchedCards, s.HarvestedCards, s.GatedCards, s.LandedCards, s.VoidedCards, spendStr, s.CostPerLandedFigure())
	if s.SpendOverflow {
		line += " spend_overflow=true"
	}
	return line
}

// FormatSpend ensures unmeasured spend is recorded as "-", never manufactured zeros.
func FormatSpend(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || trimmed == "-" || strings.EqualFold(trimmed, "unmeasured") ||
		strings.EqualFold(trimmed, "unknown") || strings.EqualFold(trimmed, "none") {
		return "-"
	}
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "-"
	}
	return fmt.Sprintf("%.4f", v)
}

func funnelFilePath(dir string) string {
	if filepath.Base(dir) == "funnel" {
		return filepath.Join(dir, FunnelFileName)
	}
	return filepath.Join(dir, "funnel", FunnelFileName)
}

func nonDash(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return "-"
	}
	return t
}

// AppendFunnelRecord appends one event record to the append-only accounting log.
func AppendFunnelRecord(dir string, r FunnelRecord) error {
	filePath := funnelFilePath(dir)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}

	needsHeader := false
	if info, err := os.Stat(filePath); err != nil || info.Size() == 0 {
		needsHeader = true
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if needsHeader {
		if _, err := f.WriteString(funnelHeader); err != nil {
			return err
		}
	}

	at := nonDash(r.At)
	if at == "-" {
		at = time.Now().UTC().Format(time.RFC3339)
	}

	event := strings.ToUpper(strings.TrimSpace(r.Event))
	spend := FormatSpend(r.Spend)

	fields := []string{
		oneline.Escape(at),
		oneline.Escape(event),
		oneline.Escape(nonDash(r.Card)),
		oneline.Escape(nonDash(r.Attempt)),
		oneline.Escape(nonDash(r.Model)),
		oneline.Escape(nonDash(r.Bench)),
		oneline.Escape(nonDash(r.Verdict)),
		oneline.Escape(spend),
		oneline.Escape(nonDash(r.Detail)),
	}

	_, err = fmt.Fprintln(f, strings.Join(fields, "\t"))
	return err
}

// ReadFunnel reads all records from the append-only funnel log.
func ReadFunnel(dir string) ([]FunnelRecord, error) {
	filePath := funnelFilePath(dir)
	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Also check if funnel.tsv exists as fallback
			altPath := filepath.Join(filepath.Dir(filePath), "funnel.tsv")
			if altFile, altErr := os.Open(altPath); altErr == nil {
				f = altFile
			} else {
				return nil, nil
			}
		} else {
			return nil, err
		}
	}
	defer f.Close()

	var records []FunnelRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		r := FunnelRecord{
			At:      "-",
			Event:   "-",
			Card:    "-",
			Attempt: "-",
			Model:   "-",
			Bench:   "-",
			Verdict: "-",
			Spend:   "-",
			Detail:  "-",
		}
		if len(parts) > 0 {
			r.At = parts[0]
		}
		if len(parts) > 1 {
			r.Event = parts[1]
		}
		if len(parts) > 2 {
			r.Card = parts[2]
		}
		if len(parts) > 3 {
			r.Attempt = parts[3]
		}
		if len(parts) > 4 {
			r.Model = parts[4]
		}
		if len(parts) > 5 {
			r.Bench = parts[5]
		}
		if len(parts) > 6 {
			r.Verdict = parts[6]
		}
		if len(parts) > 7 {
			r.Spend = parts[7]
		}
		if len(parts) > 8 {
			r.Detail = parts[8]
		}
		records = append(records, r)
	}

	return records, scanner.Err()
}

// ComputeFunnelSummary analyzes all records and computes conversion and spend metrics.
func ComputeFunnelSummary(records []FunnelRecord) FunnelSummary {
	var summary FunnelSummary
	summary.TotalRecords = len(records)

	admittedCards := make(map[string]bool)
	launchedCards := make(map[string]bool)
	harvestedCards := make(map[string]bool)
	gatedCards := make(map[string]bool)
	landedCards := make(map[string]bool)
	voidedCards := make(map[string]bool)
	pricedCards := make(map[string]bool)

	for _, r := range records {
		card := r.Card
		if card == "" || card == "-" {
			continue
		}

		switch r.Event {
		case EventAdmit:
			admittedCards[card] = true
		case EventLaunch:
			admittedCards[card] = true
			launchedCards[card] = true
		case EventHarvest:
			admittedCards[card] = true
			launchedCards[card] = true
			harvestedCards[card] = true
		case EventGate:
			admittedCards[card] = true
			launchedCards[card] = true
			harvestedCards[card] = true
			gatedCards[card] = true
		case EventLand:
			admittedCards[card] = true
			launchedCards[card] = true
			harvestedCards[card] = true
			gatedCards[card] = true
			landedCards[card] = true
		case EventVoid:
			summary.VoidedAttempts++
			voidedCards[card] = true
		}

		spend := r.Spend
		if spend != "" && spend != "-" {
			if v, err := strconv.ParseFloat(spend, 64); err == nil && v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
				// Individually finite values can still overflow float64 when
				// accumulated (e.g. two records near 1e308). Check the candidate
				// sum before committing it, so MeasuredSpend can never become
				// NaN/Inf itself -- that would break JSON encoding of the
				// summary and print "+Inf" through OneLine/the table report.
				next := summary.MeasuredSpend + v
				if math.IsNaN(next) || math.IsInf(next, 0) {
					summary.SpendOverflow = true
					summary.UnmeasuredEvents++
				} else {
					summary.MeasuredSpend = next
					summary.HasMeasuredSpend = true
					pricedCards[card] = true
				}
			} else {
				summary.UnmeasuredEvents++
			}
		} else {
			summary.UnmeasuredEvents++
		}
	}

	summary.AdmittedCards = len(admittedCards)
	summary.LaunchedCards = len(launchedCards)
	summary.HarvestedCards = len(harvestedCards)
	summary.GatedCards = len(gatedCards)
	summary.LandedCards = len(landedCards)
	summary.VoidedCards = len(voidedCards)
	for card := range pricedCards {
		if admittedCards[card] {
			summary.PricedCards++
		}
	}

	if summary.LandedCards > 0 && summary.HasMeasuredSpend {
		summary.CostPerLanded = fmt.Sprintf("%.4f", summary.MeasuredSpend/float64(summary.LandedCards))
	} else {
		// Unmeasured spend recorded as "-", never manufactured zeros.
		summary.CostPerLanded = "-"
	}

	return summary
}

// SprintFunnelInput holds the inputs for the `sprint funnel` command.
type SprintFunnelInput struct {
	Queue   string
	Action  string // "report" (default), "record", "void"
	Event   string // ADMIT, LAUNCH, HARVEST, GATE, LAND, VOID
	Card    string
	Attempt string
	Model   string
	Bench   string
	Verdict string
	Spend   string
	Detail  string
	Oneline bool
	JSON    bool
	Now     func() time.Time
	Stdout  io.Writer
	Stderr  io.Writer
}

// SprintFunnel executes the sprint funnel accounting operations.
func SprintFunnel(in SprintFunnelInput) int {
	if strings.TrimSpace(in.Queue) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT FUNNEL REFUSED: --queue is required\n")
		return 2
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		action = "report"
	}

	nowFn := in.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	stamp := nowFn().UTC().Format(time.RFC3339)

	switch action {
	case "record":
		event := strings.ToUpper(strings.TrimSpace(in.Event))
		if event == "" {
			fmt.Fprintf(in.Stderr, "SPRINT FUNNEL REFUSED: --event is required (ADMIT, LAUNCH, HARVEST, GATE, LAND, VOID)\n")
			return 2
		}
		valid := false
		for _, e := range ValidEvents {
			if e == event {
				valid = true
				break
			}
		}
		if !valid {
			fmt.Fprintf(in.Stderr, "SPRINT FUNNEL REFUSED: unknown event %q (valid events are %s)\n",
				event, strings.Join(ValidEvents, ", "))
			return 2
		}
		card := strings.TrimSpace(in.Card)
		if card == "" {
			fmt.Fprintf(in.Stderr, "SPRINT FUNNEL REFUSED: --card is required\n")
			return 2
		}

		spend := FormatSpend(in.Spend)
		record := FunnelRecord{
			At:      stamp,
			Event:   event,
			Card:    card,
			Attempt: in.Attempt,
			Model:   in.Model,
			Bench:   in.Bench,
			Verdict: in.Verdict,
			Spend:   spend,
			Detail:  in.Detail,
		}

		if err := AppendFunnelRecord(in.Queue, record); err != nil {
			fmt.Fprintf(in.Stderr, "SPRINT FUNNEL ERROR: append failed: %s\n", oneline.Err(err))
			return 1
		}
		fmt.Fprintf(in.Stdout, "FUNNEL RECORD event=%s card=%s attempt=%s spend=%s\n",
			event, card, nonDash(in.Attempt), spend)
		return 0

	case "void":
		card := strings.TrimSpace(in.Card)
		if card == "" {
			fmt.Fprintf(in.Stderr, "SPRINT FUNNEL REFUSED: --card is required to void an attempt\n")
			return 2
		}
		detail := in.Detail
		if detail == "" {
			detail = "abandoned/retried"
		}
		record := FunnelRecord{
			At:      stamp,
			Event:   EventVoid,
			Card:    card,
			Attempt: in.Attempt,
			Model:   in.Model,
			Bench:   in.Bench,
			Verdict: "VOIDED",
			Spend:   "-",
			Detail:  detail,
		}
		if err := AppendFunnelRecord(in.Queue, record); err != nil {
			fmt.Fprintf(in.Stderr, "SPRINT FUNNEL ERROR: append failed: %s\n", oneline.Err(err))
			return 1
		}
		fmt.Fprintf(in.Stdout, "FUNNEL VOID card=%s attempt=%s detail=%s\n", card, nonDash(in.Attempt), detail)
		return 0

	case "report":
		records, err := ReadFunnel(in.Queue)
		if err != nil {
			fmt.Fprintf(in.Stderr, "SPRINT FUNNEL ERROR: read failed: %s\n", oneline.Err(err))
			return 1
		}

		summary := ComputeFunnelSummary(records)

		if in.JSON {
			enc := json.NewEncoder(in.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(summary); err != nil {
				fmt.Fprintf(in.Stderr, "SPRINT FUNNEL ERROR: json encode failed: %s\n", oneline.Err(err))
				return 1
			}
			return 0
		}

		if in.Oneline {
			fmt.Fprintln(in.Stdout, summary.OneLine())
			return 0
		}

		// Full report table
		fmt.Fprintf(in.Stdout, "SPRINT FUNNEL  %s\n\n", stamp)
		fmt.Fprintf(in.Stdout, "%-10s  %5s  %10s\n", "Stage", "Count", "Conversion")
		fmt.Fprintf(in.Stdout, "%-10s  %5s  %10s\n", "----------", "-----", "----------")

		admitPct := func(count int) string {
			if summary.AdmittedCards == 0 {
				return "-"
			}
			return fmt.Sprintf("%.1f%%", float64(count)*100.0/float64(summary.AdmittedCards))
		}

		fmt.Fprintf(in.Stdout, "%-10s  %5d  %10s\n", EventAdmit, summary.AdmittedCards, admitPct(summary.AdmittedCards))
		fmt.Fprintf(in.Stdout, "%-10s  %5d  %10s\n", EventLaunch, summary.LaunchedCards, admitPct(summary.LaunchedCards))
		fmt.Fprintf(in.Stdout, "%-10s  %5d  %10s\n", EventHarvest, summary.HarvestedCards, admitPct(summary.HarvestedCards))
		fmt.Fprintf(in.Stdout, "%-10s  %5d  %10s\n", EventGate, summary.GatedCards, admitPct(summary.GatedCards))
		fmt.Fprintf(in.Stdout, "%-10s  %5d  %10s\n", EventLand, summary.LandedCards, admitPct(summary.LandedCards))
		fmt.Fprintf(in.Stdout, "%-10s  %5d  %10s\n", EventVoid, summary.VoidedCards, "-")

		fmt.Fprintln(in.Stdout)
		fmt.Fprintln(in.Stdout, "Spend:")
		if summary.HasMeasuredSpend {
			fmt.Fprintf(in.Stdout, "  Measured Spend:      $%.4f\n", summary.MeasuredSpend)
		} else {
			fmt.Fprintf(in.Stdout, "  Measured Spend:      -\n")
		}
		fmt.Fprintf(in.Stdout, "  Unmeasured Events:   %d\n", summary.UnmeasuredEvents)
		if summary.SpendOverflow {
			fmt.Fprintf(in.Stdout, "  Spend Overflow:      true (one or more finite records exceeded float64 range on accumulation; excluded, counted as unmeasured)\n")
		}
		if summary.CostPerLanded != "-" {
			fmt.Fprintf(in.Stdout, "  Cost / Landed Card:  %s\n", summary.CostPerLandedFigure())
		} else {
			fmt.Fprintf(in.Stdout, "  Cost / Landed Card:  -\n")
		}

		fmt.Fprintln(in.Stdout)
		fmt.Fprintln(in.Stdout, summary.OneLine())
		return 0

	default:
		fmt.Fprintf(in.Stderr, "SPRINT FUNNEL REFUSED: unknown action %q (valid actions are report, record, void)\n", in.Action)
		return 2
	}
}
