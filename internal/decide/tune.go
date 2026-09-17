// Tuning a confidence floor from a decisions log (rule 8): a decision is
// logged beside the outcome it predicted, and the floor is re-tuned from those
// rows, never from a feeling about the model.
//
// A decisions log is JSONL, one row per decision. Each row joins the decision
// (the field named by TuneOptions.Choice, default "decision"), its confidence
// (TuneOptions.Conf, default "confidence") and the outcome (TuneOptions.Label,
// default "label"). A row without a label is counted in Lines and skipped as
// unlabeled. For each floor, a row at or above the floor was decided: Tune
// reports how many of those agreed with their label (agree_rate) and how many
// fell below it (escalation_rate, against every labeled row). The best floor
// is the one with the highest agree_rate whose escalation_rate is at most
// TuneOptions.MaxEscalation; a tie keeps the higher (more selective) floor.
// Fewer than MinLabeled labeled rows cannot set a floor, so the caller refuses
// rather than guesses.
package decide

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DefaultFloors are the confidence floors Tune reports on when none are given.
var DefaultFloors = []float64{0.5, 0.7, 0.8, 0.9, 0.95}

// DefaultMaxEscalation is the escalation-rate cap Tune uses to pick the best
// floor when none is given.
const DefaultMaxEscalation = 0.7

// MinLabeled is the fewest labeled rows a floor can be set from.
const MinLabeled = 10

// TuneOptions names the fields a decisions log carries and the floors to try.
// An empty field name or floor list takes the default; an empty label is a
// row to skip, never a field to guess.
type TuneOptions struct {
	Label         string
	Choice        string
	Conf          string
	Floors        []float64
	MaxEscalation float64
}

func (o TuneOptions) withDefaults() TuneOptions {
	if o.Label == "" {
		o.Label = "label"
	}
	if o.Choice == "" {
		o.Choice = "decision"
	}
	if o.Conf == "" {
		o.Conf = "confidence"
	}
	if len(o.Floors) == 0 {
		o.Floors = append([]float64(nil), DefaultFloors...)
	}
	if o.MaxEscalation <= 0 {
		o.MaxEscalation = DefaultMaxEscalation
	}
	return o
}

// FloorStat is one floor's line: how many rows the floor decided, how many of
// those agreed with their label, and how many it escalated.
type FloorStat struct {
	Floor     float64
	Decided   int
	Agree     int
	Escalated int
}

// AgreeRate is the share of decided rows that agreed with their label.
func (s FloorStat) AgreeRate() float64 {
	if s.Decided == 0 {
		return 0
	}
	return float64(s.Agree) / float64(s.Decided)
}

// EscalationRate is the share of labeled rows the floor left below it.
func (s FloorStat) EscalationRate(labeled int) float64 {
	if labeled == 0 {
		return 0
	}
	return float64(s.Escalated) / float64(labeled)
}

// TuneResult is the whole read of a decisions log: the line and labeled
// counts, one stat per floor, and the best floor under the escalation cap.
type TuneResult struct {
	Lines     int
	Labeled   int
	Floors    []FloorStat
	BestFloor float64
}

// tuneRow is one labeled row reduced to what a floor needs: the confidence and
// whether the decision agreed with the label.
type tuneRow struct {
	confidence float64
	agree      bool
}

// Tune parses a decisions log and reports, per floor, the decisions it made
// and the rows it escalated, then the best floor under the escalation cap. A
// line that is not a JSON object, or a labeled line with no confidence, is a
// refusal, never a guess.
func Tune(data []byte, opts TuneOptions) (TuneResult, error) {
	opts = opts.withDefaults()
	res := TuneResult{}
	rows := make([]tuneRow, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		res.Lines++
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			return TuneResult{}, fmt.Errorf("decide: tune: line %d is not a JSON object: %w", res.Lines, err)
		}
		label, ok := scalarField(fields, opts.Label)
		if !ok || strings.TrimSpace(label) == "" {
			continue // unlabeled: counted in Lines, skipped
		}
		confidence, ok := numberField(fields, opts.Conf)
		if !ok {
			return TuneResult{}, fmt.Errorf("decide: tune: line %d has no %s", res.Lines, opts.Conf)
		}
		choice, _ := scalarField(fields, opts.Choice)
		res.Labeled++
		rows = append(rows, tuneRow{confidence: confidence, agree: choice == label})
	}
	if err := scanner.Err(); err != nil {
		return TuneResult{}, fmt.Errorf("decide: tune: read log: %w", err)
	}
	floors := append([]float64(nil), opts.Floors...)
	sort.Float64s(floors)
	best := -1
	for _, floor := range floors {
		stat := FloorStat{Floor: floor}
		for _, row := range rows {
			if row.confidence >= floor {
				stat.Decided++
				if row.agree {
					stat.Agree++
				}
			} else {
				stat.Escalated++
			}
		}
		res.Floors = append(res.Floors, stat)
		if stat.EscalationRate(res.Labeled) > opts.MaxEscalation {
			continue
		}
		if best < 0 || stat.AgreeRate() >= res.Floors[best].AgreeRate() {
			best = len(res.Floors) - 1
		}
	}
	if best >= 0 {
		res.BestFloor = res.Floors[best].Floor
	}
	return res, nil
}

// Render is the tune read, one line per floor then the finish. Every rate is
// printed to two places and every floor verbatim.
func (r TuneResult) Render() string {
	var b strings.Builder
	for _, stat := range r.Floors {
		fmt.Fprintf(&b, "TUNE floor=%g decided=%d agree=%d agree_rate=%.2f escalated=%d escalation_rate=%.2f\n",
			stat.Floor, stat.Decided, stat.Agree, stat.AgreeRate(), stat.Escalated, stat.EscalationRate(r.Labeled))
	}
	fmt.Fprintf(&b, "TUNE OK lines=%d labeled=%d best_floor=%g\n", r.Lines, r.Labeled, r.BestFloor)
	return b.String()
}

// scalarField reads a named field as a string. A string, bool or number is
// accepted; an object or array is not a label or a decision.
func scalarField(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", false
	}
	switch t := v.(type) {
	case bool:
		return strconv.FormatBool(t), true
	case json.Number:
		return t.String(), true
	default:
		return "", false
	}
}

// numberField reads a named field as a float; a JSON string holding a number
// is accepted too.
func numberField(fields map[string]json.RawMessage, name string) (float64, bool) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
