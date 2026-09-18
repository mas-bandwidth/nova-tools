// The escalation log (Glenn 2026-09-18): every routing decision is written down
// with the evidence that produced it, the rung it tried, what followed, the
// rung that finally succeeded -- and, beside all of it, what Rowan would have
// picked. That last column is the point: it is how we find out whether the
// decision route is better than the coordinator's own hand, one row at a time.
//
// The log is append-only JSON lines, one object per decision, at a path the
// caller names. Nothing here reads a working directory and nothing is rewritten
// in place; the rows are the record, and the summary is a projection of them.
//
// The summary regenerates the STARTING RUNG per kind from the rows: the lowest
// rung that is carrying its own weight -- successes at that height, and at
// least as many successes as failures. A kind with no success keeps the rung
// the table started from, because a starting rung with no rows behind it is not
// regenerated (SPEC-DECIDE rule 8).
package decide

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Entry is one row of the escalation log.
type Entry struct {
	Time          string  `json:"time"`
	Unit          string  `json:"unit"`
	Kind          string  `json:"kind"`
	Evidence      Unit    `json:"evidence"`
	RungTried     string  `json:"rung_tried"`
	Height        int     `json:"height"`
	Confidence    float64 `json:"confidence"`
	Floor         float64 `json:"floor"`
	SteppedUp     bool    `json:"stepped_up"`
	Escalated     bool    `json:"escalated"`
	Designated    bool    `json:"designated,omitempty"`
	Source        string  `json:"source"`
	RowanPick     string  `json:"rowan_pick"`
	Reason        string  `json:"reason,omitempty"`
	Outcome       string  `json:"outcome,omitempty"`
	RungSucceeded string  `json:"rung_succeeded,omitempty"`

	// AwaitingTermination says this decision is an await, not a move: the rung
	// named is the one an attempt may still be running on, and the lease rule
	// keeps its expiry UNKNOWN until there is termination proof.
	AwaitingTermination bool `json:"awaiting_termination,omitempty"`
}

// EntryFor is the row one route decision writes. The outcome and the rung that
// succeeded are filled in later, by the caller that watched the work: a
// decision never writes them for itself.
func EntryFor(res RouteResult, u Unit, now time.Time) Entry {
	return Entry{
		Time:       now.UTC().Format(time.RFC3339),
		Unit:       res.Unit,
		Kind:       res.Kind,
		Evidence:   u,
		RungTried:  res.Rung.Name,
		Height:     res.Rung.Height,
		Confidence: res.Confidence,
		Floor:      res.Floor,
		SteppedUp:  res.SteppedUp,
		Escalated:  res.Escalated,
		Designated: res.Designated,
		Source:     res.Source,
		RowanPick:  res.RulesRung,
		Reason:     res.Reason,

		AwaitingTermination: res.AwaitingTermination,
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

// ReadEntries reads the log back. A missing file, or a line that is not one
// JSON object, is a refusal naming what it could not read.
func ReadEntries(path string) ([]Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("decide: cannot read the log %s: %w", path, err)
	}
	var out []Entry
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			return nil, fmt.Errorf("decide: log %s line %d is not one JSON object: %w", path, line, err)
		}
		out = append(out, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("decide: read the log %s: %w", path, err)
	}
	return out, nil
}

// KindSummary is one kind's read of the log: how many decisions, how many were
// escalations, and the starting rung regenerated from the rows beside the
// default it replaces.
type KindSummary struct {
	Kind        string
	Decisions   int
	Escalations int
	Successes   int
	Failures    int
	StartHeight int
	StartRung   string
	DefaultRung string
	Regenerated bool
}

// Summary is the whole read of the log.
type Summary struct {
	Entries int
	Kinds   []KindSummary
}

// Summarize counts the escalations per kind and regenerates the starting rung
// from the rows. An entry naming a kind the table does not hold is a refusal:
// a summary over evidence nobody can read is not a summary.
func Summarize(reg *Registry, entries []Entry) (Summary, error) {
	if reg == nil || len(reg.Minds) == 0 {
		return Summary{}, fmt.Errorf("decide: no registry; the log's rungs cannot be placed on a ladder")
	}
	type counts struct {
		decisions   int
		escalations int
		success     map[int]int
		failure     map[int]int
	}
	byKind := map[string]*counts{}
	for i, e := range entries {
		kind := strings.TrimSpace(e.Kind)
		if kind == "" {
			kind = strings.TrimSpace(e.Evidence.Kind)
		}
		if !KnownKind(kind) {
			return Summary{}, fmt.Errorf("decide: log row %d names kind %q, which is not one of %s", i+1, e.Kind, strings.Join(Kinds, ", "))
		}
		c := byKind[kind]
		if c == nil {
			c = &counts{success: map[int]int{}, failure: map[int]int{}}
			byKind[kind] = c
		}
		c.decisions++
		if e.SteppedUp || len(e.Evidence.Attempts) > 0 {
			c.escalations++
		}
		for _, a := range e.Evidence.Attempts {
			if m, ok := reg.ByName(a.Rung); ok && a.Failed() {
				c.failure[m.Height]++
			}
		}
		if m, ok := reg.ByName(e.RungSucceeded); ok {
			c.success[m.Height]++
		}
		if e.Outcome == OutcomeFailed || e.Outcome == OutcomeTimeout || e.Outcome == OutcomeAbandoned {
			if m, ok := reg.ByName(e.RungTried); ok {
				c.failure[m.Height]++
			}
		}
	}
	sum := Summary{Entries: len(entries)}
	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		c := byKind[kind]
		def, _ := StartHeight(kind)
		row := KindSummary{
			Kind:        kind,
			Decisions:   c.decisions,
			Escalations: c.escalations,
			StartHeight: def,
			StartRung:   reg.RungName(def),
			DefaultRung: reg.RungName(def),
		}
		for _, h := range c.success {
			row.Successes += h
		}
		for _, h := range c.failure {
			row.Failures += h
		}
		for _, h := range reg.Heights() {
			if c.success[h] > 0 && c.success[h] >= c.failure[h] {
				row.StartHeight = h
				row.StartRung = reg.RungName(h)
				row.Regenerated = h != def
				break
			}
		}
		sum.Kinds = append(sum.Kinds, row)
	}
	return sum, nil
}

// Render is the summary, one line per kind then the finish.
func (s Summary) Render() string {
	var b strings.Builder
	escalations := 0
	for _, k := range s.Kinds {
		escalations += k.Escalations
		fmt.Fprintf(&b, "LOG kind=%s decisions=%d escalations=%d successes=%d failures=%d start_rung=%s start_height=%d default_rung=%s regenerated=%v\n",
			oneline.Field(k.Kind), k.Decisions, k.Escalations, k.Successes, k.Failures,
			oneline.Field(k.StartRung), k.StartHeight, oneline.Field(k.DefaultRung), k.Regenerated)
	}
	fmt.Fprintf(&b, "LOG OK rows=%d kinds=%d escalations=%d\n", s.Entries, len(s.Kinds), escalations)
	return b.String()
}
