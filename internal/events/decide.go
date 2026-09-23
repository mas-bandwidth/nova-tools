package events

// decide.go is the decision record on the one stream (nova-tools #2623). A Jev routing
// decision used to be a row of the decide_log table; it is now one `decide` entry on
// `cards:done`, written by the same Emitter every card transition goes through, and the
// fold keeps it in its own table, `decisions`. The table is retired: the stream is the
// record and the SQLite file is a view of it.
//
// THE WIRE NAMES ARE decide_log's COLUMN NAMES, so the calibration set Johnny named reads
// the same before and after: unit_id, kind, files, packages, lanes, lane, rung_tried, height,
// confidence, floor, stepped_up, escalated, designated, source, rowan_pick, reason, wait,
// awaiting_termination, refusal, outcome, rung_succeeded, calls, tokens_in, tokens_out,
// usage_failed. Two columns do not travel as themselves: `ts` is the entry's own `at`, and
// the BIGSERIAL `id` is the event id. The third, `evidence`, a JSON document, does not
// travel at all: the stream carries ids and counts and refuses a document by construction,
// its four measured columns (files, packages, lanes, lane) are carried, and the whole of it
// stays in the JSON lines log `nova-decide route --log` writes beside the event.
//
// The entry's own `event` field is `decide`; the decision's `kind` field is the UNIT's kind
// (rebase, new-verb, ...), exactly as decide_log spelled it. The two never collide because
// the stream names a transition `event`, not `kind`.
//
// ABSENT IS NOT ZERO. A string the decision did not carry is not written, and the fold stores
// NULL; tokens_in and tokens_out are the Event's own optional counters, and confidence and
// floor are optional too, so a writer that did not know one has not reported a zero.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Decide is a routing decision: one entry per verdict, carrying a Decision.
const Decide Kind = "decide"

// Decision is the decide_log row as stream fields. Every string is an id, a name or a short
// capped reason; every number is a count, a height or a confidence.
type Decision struct {
	UnitID              string
	Kind                string
	Files               int
	Packages            int
	Lanes               int
	Lane                string
	RungTried           string
	Height              int
	Confidence          *float64
	Floor               *float64
	SteppedUp           bool
	Escalated           bool
	Designated          bool
	Source              string
	RowanPick           string
	Reason              string
	Wait                string
	AwaitingTermination bool
	Refusal             string
	Outcome             string
	RungSucceeded       string
	Calls               int
	UsageFailed         bool
}

// decisionFieldNames are the decision's wire names in the order XADD writes them, after the
// event's own. tokens_in and tokens_out are the event's, so they are not repeated here.
var decisionFieldNames = []string{
	"unit_id", "kind", "files", "packages", "lanes", "lane",
	"rung_tried", "height", "confidence", "floor",
	"stepped_up", "escalated", "designated", "source", "rowan_pick", "reason",
	"wait", "awaiting_termination", "refusal",
	"outcome", "rung_succeeded",
	"calls", "usage_failed",
}

// Text makes a free-text value fit for the stream: escaped to one line and capped at the
// field ceiling with oneline's byte mark, so a long reason arrives cut AND SAYS SO rather than
// being refused at the door and losing the whole decision. The uncut text is in the JSON lines
// log the decision was also written to.
func Text(s string) string {
	return oneline.Cap(oneline.Escape(s), maxFieldBytes)
}

// validate holds the decision to the stream's door: id-shaped strings, counts that are counts,
// a confidence and a floor that are numbers.
func (d *Decision) validate() error {
	if strings.TrimSpace(d.UnitID) == "" {
		return fmt.Errorf("a decide event wants unit_id, the unit the decision routed")
	}
	for _, f := range []struct{ name, value string }{
		{"unit_id", d.UnitID}, {"kind", d.Kind}, {"lane", d.Lane},
		{"rung_tried", d.RungTried}, {"source", d.Source}, {"rowan_pick", d.RowanPick},
		{"reason", d.Reason}, {"wait", d.Wait}, {"refusal", d.Refusal},
		{"outcome", d.Outcome}, {"rung_succeeded", d.RungSucceeded},
	} {
		if err := idShaped(f.name, f.value); err != nil {
			return err
		}
	}
	for _, c := range []struct {
		name string
		n    int
	}{{"files", d.Files}, {"packages", d.Packages}, {"lanes", d.Lanes}, {"height", d.Height}, {"calls", d.Calls}} {
		if c.n < 0 {
			return fmt.Errorf("%s is a count, got %d", c.name, c.n)
		}
	}
	for _, c := range []struct {
		name string
		v    *float64
	}{{"confidence", d.Confidence}, {"floor", d.Floor}} {
		if c.v != nil && (math.IsNaN(*c.v) || math.IsInf(*c.v, 0) || *c.v < 0) {
			return fmt.Errorf("%s is a number from 0 up, got %v", c.name, *c.v)
		}
	}
	return nil
}

// fields adds the decision's wire fields to f. A string the decision does not carry, and a
// confidence or floor nobody gave, is left out rather than written as "" or 0.
func (d *Decision) fields(f map[string]string) {
	put := func(name, value string) {
		if value != "" {
			f[name] = value
		}
	}
	put("unit_id", d.UnitID)
	put("kind", d.Kind)
	f["files"] = strconv.Itoa(d.Files)
	f["packages"] = strconv.Itoa(d.Packages)
	f["lanes"] = strconv.Itoa(d.Lanes)
	put("lane", d.Lane)
	put("rung_tried", d.RungTried)
	f["height"] = strconv.Itoa(d.Height)
	if d.Confidence != nil {
		f["confidence"] = strconv.FormatFloat(*d.Confidence, 'f', -1, 64)
	}
	if d.Floor != nil {
		f["floor"] = strconv.FormatFloat(*d.Floor, 'f', -1, 64)
	}
	f["stepped_up"] = strconv.FormatBool(d.SteppedUp)
	f["escalated"] = strconv.FormatBool(d.Escalated)
	f["designated"] = strconv.FormatBool(d.Designated)
	put("source", d.Source)
	put("rowan_pick", d.RowanPick)
	put("reason", d.Reason)
	put("wait", d.Wait)
	f["awaiting_termination"] = strconv.FormatBool(d.AwaitingTermination)
	put("refusal", d.Refusal)
	put("outcome", d.Outcome)
	put("rung_succeeded", d.RungSucceeded)
	f["calls"] = strconv.Itoa(d.Calls)
	f["usage_failed"] = strconv.FormatBool(d.UsageFailed)
}

// decisionFromFields reads a decide entry's decision back. A missing string stays "" (and
// folds to NULL), a missing confidence or floor stays nil; a number or a flag that is present
// and unreadable is an error, because that is a writer with a bug.
func decisionFromFields(f map[string]string) (*Decision, error) {
	d := &Decision{
		UnitID:        f["unit_id"],
		Kind:          f["kind"],
		Lane:          f["lane"],
		RungTried:     f["rung_tried"],
		Source:        f["source"],
		RowanPick:     f["rowan_pick"],
		Reason:        f["reason"],
		Wait:          f["wait"],
		Refusal:       f["refusal"],
		Outcome:       f["outcome"],
		RungSucceeded: f["rung_succeeded"],
	}
	for _, c := range []struct {
		name string
		dst  *int
	}{{"files", &d.Files}, {"packages", &d.Packages}, {"lanes", &d.Lanes}, {"height", &d.Height}, {"calls", &d.Calls}} {
		n, err := atoiField(f, c.name)
		if err != nil {
			return nil, err
		}
		*c.dst = n
	}
	for _, c := range []struct {
		name string
		dst  **float64
	}{{"confidence", &d.Confidence}, {"floor", &d.Floor}} {
		if raw := strings.TrimSpace(f[c.name]); raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("%s %q is not a number", c.name, raw)
			}
			*c.dst = &v
		}
	}
	for _, c := range []struct {
		name string
		dst  *bool
	}{{"stepped_up", &d.SteppedUp}, {"escalated", &d.Escalated}, {"designated", &d.Designated},
		{"awaiting_termination", &d.AwaitingTermination}, {"usage_failed", &d.UsageFailed}} {
		if raw := strings.TrimSpace(f[c.name]); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("%s %q is not true or false", c.name, raw)
			}
			*c.dst = v
		}
	}
	return d, nil
}
