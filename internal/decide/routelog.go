// The escalation log (Glenn 2026-09-18): every routing decision is written down
// with the evidence that produced it, the rung it tried, what followed, the
// rung that finally succeeded -- and, beside all of it, what Rowan would have
// picked. That last column is the point: it is how we find out whether the
// decision route is better than the coordinator's own hand, one row at a time.
//
// The log is append-only JSON lines, one object per decision, at a path the
// caller names. Nothing here reads a working directory and nothing is rewritten
// in place; the rows are the record.
package decide

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// SourceOutcome marks a row that is not a decision at all: the outcome of a
// unit a decision routed earlier, appended later by whoever watched the work.
// The log is append-only -- a row written is never rewritten -- so an outcome
// is its own row.
const SourceOutcome = "outcome"

// SourceSkipped marks a row for a route a launcher was explicitly told to skip:
// not a decision, and carrying the reason that opened the skip. A skip is a
// fact in the log and not an absence (SPEC-DECIDE housekeeping H4, #1625).
const SourceSkipped = "skipped"

// Entry is one row of the escalation log.
type Entry struct {
	Time      string `json:"time"`
	Unit      string `json:"unit"`
	Kind      string `json:"kind"`
	Evidence  Unit   `json:"evidence"`
	RungTried string `json:"rung_tried"`
	Height    int    `json:"height"`
	// Confidence and Floor are ABSENT rather than zero where the row did not
	// carry them, by the same presence rule as the counters below: a row read
	// back from a log line that omitted either keeps it nil, and the decide
	// event it becomes leaves the field off, so the fold stores NULL, never a
	// 0 nobody measured (Stella, HOLD 7 on #2628).
	Confidence    *float64 `json:"confidence,omitempty"`
	Floor         *float64 `json:"floor,omitempty"`
	SteppedUp     bool     `json:"stepped_up"`
	Escalated     bool     `json:"escalated"`
	Designated    bool     `json:"designated,omitempty"`
	Source        string   `json:"source"`
	RowanPick     string   `json:"rowan_pick"`
	Reason        string   `json:"reason,omitempty"`
	Outcome       string   `json:"outcome,omitempty"`
	RungSucceeded string   `json:"rung_succeeded,omitempty"`

	// Wait is the typed action beside the rung: "-" for a decision the caller
	// may act on, awaiting_termination for one it may not.
	Wait string `json:"wait,omitempty"`
	// Read is the mind -- or minds, comma-joined -- a KIND designation attached
	// to this unit as a READER rather than as the rung: a security read, or a
	// fresh take whose designate is reserved. The work went to RungTried; this
	// is who reads it.
	Read string `json:"read,omitempty"`
	// FloorFrom says where Floor came from: flag, kind or built-in. Two rows
	// carrying floor 0.90 are different claims when one was measured from rows
	// and the other is the default nobody has ever tuned.
	FloorFrom string `json:"floor_from,omitempty"`
	// Refusal is why the route ended in a refusal, where it did. The row is
	// still written: a call that was already made is still a cost, and a
	// decision that could not be made is still evidence.
	Refusal string `json:"refusal,omitempty"`
	// AwaitingTermination is the same fact as a boolean, for a reader that
	// gates on it: the rung named is the one an attempt may still be running
	// on, and the lease rule keeps its expiry UNKNOWN until termination.
	AwaitingTermination bool `json:"awaiting_termination,omitempty"`

	// What the decision spent. Calls is the number of provider calls it made;
	// TokensIn and TokensOut are ABSENT rather than zero where no call was made
	// or the call failed, because a zero is a measurement and an absence is not
	// (SPEC-TOKENS rule 14). UsageFailed marks a call whose cost is unknown.
	Calls       int  `json:"calls,omitempty"`
	TokensIn    *int `json:"tokens_in,omitempty"`
	TokensOut   *int `json:"tokens_out,omitempty"`
	UsageFailed bool `json:"usage_failed,omitempty"`

	// Excluded is the set of down friend rungs excluded from routing (#3397).
	Excluded    string `json:"excluded,omitempty"`
	DownChecked bool   `json:"down_checked"`

	// Ms is the provider round trip in milliseconds, measured by the caller
	// on a monotonic clock around the call alone; WallMs is the verb's start
	// to its line. Each is ABSENT, never zero, where there was no call or no
	// measurement, by the per-counter presence rule: a zero is a measurement
	// and an absence is not (SPEC-TOKENS rule 14).
	Ms     *int `json:"ms,omitempty"`
	WallMs *int `json:"wall_ms,omitempty"`
}

// EntryFor is the row one route decision writes. The outcome and the rung that
// succeeded are filled in later, by the caller that watched the work: a
// decision never writes them for itself.
func EntryFor(res RouteResult, u Unit, now time.Time) Entry {
	var excludedStr string
	if len(res.Excluded) > 0 {
		excludedStr = strings.Join(res.Excluded, ",")
	}
	return Entry{
		Time:       now.UTC().Format(time.RFC3339),
		Unit:       res.Unit,
		Kind:       res.Kind,
		Evidence:   u,
		RungTried:  res.Rung.Name,
		Height:     res.Rung.Height,
		Confidence: measured(res.Confidence),
		Floor:      measured(res.Floor),
		SteppedUp:  res.SteppedUp,
		Escalated:  res.Escalated,
		Designated: res.Designated,
		Source:     res.Source,
		RowanPick:  res.RulesRung,
		Reason:     res.Reason,

		Wait:                waitOrDash(res.Wait),
		Read:                strings.Join(res.Reads, ","),
		FloorFrom:           res.FloorFrom,
		AwaitingTermination: res.AwaitingTermination(),
		Refusal:             res.Refusal,
		Excluded:            excludedStr,
		DownChecked:         res.DownChecked,
		Calls:               res.Usage.Calls,
		TokensIn:            tokens(res.Usage.HasInput, res.Usage.InputTokens),
		TokensOut:           tokens(res.Usage.HasOutput, res.Usage.OutputTokens),
		UsageFailed:         res.Usage.Failed,
		Ms:                  millis(res.Usage.HasMs, res.Usage.Ms),
		WallMs:              millis(res.HasWallMs, res.WallMs),
	}
}

// waitOrDash renders an unset wait as the dash the line uses, so a row read
// back never has to tell an absent field from an absent wait.
func waitOrDash(wait string) string {
	if wait == "" {
		return WaitNone
	}
	return wait
}

// tokens reports a counter only where the provider reported THAT counter. An
// unmeasured cost is an absence in the row, never a zero; a reported zero is a
// measurement and is written as one.
func tokens(has bool, n int) *int {
	if !has {
		return nil
	}
	v := n
	return &v
}

// millis reports a latency only where it was measured: the provider round
// trip where a call was made, the wall clock where the verb stamped one. An
// unmeasured latency is an absence in the row, never a zero; a measured zero
// is written as one.
func millis(has bool, n int) *int {
	if !has {
		return nil
	}
	v := n
	return &v
}

// measured is a confidence or a floor a route decision computed: a route
// always applies a floor to a number, so its row always carries both.
func measured(v float64) *float64 { return &v }

// SkippedEntry is the row one explicitly skipped route leaves behind: the
// reason travels in the row, because a skip nobody can explain is the habit H4
// exists to end.
func SkippedEntry(unit, reason string, now time.Time) Entry {
	return Entry{
		Time:   now.UTC().Format(time.RFC3339),
		Unit:   unit,
		Source: SourceSkipped,
		Reason: reason,
		Wait:   WaitNone,
	}
}

// AppendEntry appends one row to the log at path, creating it if it is not
// there. The file is the tool's own (0600) and is never rewritten.
func AppendEntry(path string, e Entry) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("decide: no log path; refusing to guess one")
	}
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("decide: encode log row: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("decide: append to the log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("decide: append to the log: %w", err)
	}
	return nil
}
