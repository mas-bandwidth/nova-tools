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
	// Default is the answer a below-floor row actually gets. Where the
	// escalation is a step UP a rung it is empty, because the row gets a
	// different, more careful answer and no single name covers it. Where
	// the escalation is a fallback to one cheap answer -- the who-reads
	// question falls back to opus-child -- naming it here lets each floor
	// report what that fallback got right and what it MISSED, and the best
	// floor becomes the one that misses fewest rather than the one that
	// agrees most on the rows it kept.
	Default string
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
	// Defaulted is the escalated rows a named default answered, and
	// DefaultAgree how many of those the default got right. Both are zero
	// when no default is named: an escalation with no single answer behind
	// it is a count and not a score.
	Defaulted    int
	DefaultAgree int
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
	// defaultAgrees is whether the NAMED DEFAULT would have been right on
	// this row, which is what a below-floor row is actually scored against.
	defaultAgrees bool
}

// Tune parses a decisions log and reports, per floor, the decisions it made
// and the rows it escalated, then the best floor under the escalation cap. A
// line that is not a JSON object, or a labeled line with no confidence, is a
// refusal, never a guess.
func Tune(data []byte, opts TuneOptions) (TuneResult, error) {
	opts = opts.withDefaults()
	dflt := strings.TrimSpace(opts.Default)
	defaultSeen := false
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
		if dflt != "" && label == dflt {
			defaultSeen = true
		}
		rows = append(rows, tuneRow{confidence: confidence, agree: choice == label, defaultAgrees: dflt != "" && label == dflt})
	}
	if err := scanner.Err(); err != nil {
		return TuneResult{}, fmt.Errorf("decide: tune: read log: %w", err)
	}
	if dflt != "" && !defaultSeen {
		return TuneResult{}, fmt.Errorf("decide: tune: no labeled row is %q, so a floor tuned against that default is tuned against nothing; refusing to guess", dflt)
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
				continue
			}
			stat.Escalated++
			if dflt == "" {
				continue
			}
			stat.Defaulted++
			if row.defaultAgrees {
				stat.DefaultAgree++
			}
		}
		res.Floors = append(res.Floors, stat)
		if stat.EscalationRate(res.Labeled) > opts.MaxEscalation {
			continue
		}
		if best < 0 || betterFloor(stat, res.Floors[best], dflt != "") {
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
		fmt.Fprintf(&b, "TUNE floor=%g decided=%d agree=%d agree_rate=%.2f escalated=%d escalation_rate=%.2f",
			stat.Floor, stat.Decided, stat.Agree, stat.AgreeRate(), stat.Escalated, stat.EscalationRate(r.Labeled))
		if stat.Defaulted > 0 || stat.DefaultAgree > 0 {
			fmt.Fprintf(&b, " defaulted=%d default_agree=%d missed=%d", stat.Defaulted, stat.DefaultAgree, stat.Missed())
		}
		b.WriteString("\n")
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

// Missed is the below-floor rows a named default answered WRONG: the rows the
// floor handed to the cheap answer where the log says the cheap answer was not
// the one the work needed. It is zero where no default is named.
func (s FloorStat) Missed() int { return s.Defaulted - s.DefaultAgree }

// betterFloor is the comparison behind BestFloor. With no default named it is
// the old rule and nothing moves: the highest agree rate wins, and a tie keeps
// the higher, more selective floor. With a default named the cost is not
// symmetric -- a miss lands a defect where a needless escalation costs minutes
// -- so fewest misses wins first, and only a tie there is broken by the agree
// rate and then by the higher floor.
func betterFloor(candidate, best FloorStat, hasDefault bool) bool {
	if !hasDefault {
		return candidate.AgreeRate() >= best.AgreeRate()
	}
	if candidate.Missed() != best.Missed() {
		return candidate.Missed() < best.Missed()
	}
	return candidate.AgreeRate() >= best.AgreeRate()
}
