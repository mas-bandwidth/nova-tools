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
	"math"
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

		Wait:                waitOrDash(res.Wait),
		Read:                strings.Join(res.Reads, ","),
		FloorFrom:           res.FloorFrom,
		AwaitingTermination: res.AwaitingTermination(),
		Refusal:             res.Refusal,
		Calls:               res.Usage.Calls,
		TokensIn:            tokens(res.Usage.HasInput, res.Usage.InputTokens),
		TokensOut:           tokens(res.Usage.HasOutput, res.Usage.OutputTokens),
		UsageFailed:         res.Usage.Failed,
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

// ConfBuckets are the buckets the per-kind confidence histogram counts into,
// in order. They are coarse on purpose: the question a reader brings to this
// histogram is "where do the provider's answers actually land, and is the floor
// above all of them", and six buckets answer it on one line.
var ConfBuckets = []string{"0.0-0.5", "0.5-0.6", "0.6-0.7", "0.7-0.8", "0.8-0.9", "0.9-1.0"}

// confBucket puts one confidence in its bucket. The top bucket is closed at
// both ends, so a 1.00 is counted rather than dropped.
func confBucket(conf float64) int {
	switch {
	case conf < 0.5:
		return 0
	case conf < 0.6:
		return 1
	case conf < 0.7:
		return 2
	case conf < 0.8:
		return 3
	case conf < 0.9:
		return 4
	default:
		return 5
	}
}

// KindSummary is one kind's read of the log: how many decisions, how many were
// escalations, the starting rung regenerated from the rows beside the default
// it replaces -- and the shape of the confidences the PROVIDER gave for this
// kind, against the floor those confidences were gated on.
//
// The histogram counts provider rows only. The rules' own confidences are the
// machinery's numbers (1.00 for a designation, 0.90 for a sized unit) and
// mixing them in hides the thing the histogram exists to show: whether the
// floor sits above every answer the provider has ever given for this kind, in
// which case the step-up is not a policy, it is the only outcome.
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

	// ProviderRows is how many rows carried a provider answer for this kind.
	ProviderRows int
	// ConfMin, ConfMax and ConfP25 describe those answers; they mean nothing
	// when ProviderRows is 0 and the line prints a dash there.
	ConfMin float64
	ConfMax float64
	ConfP25 float64
	// Hist counts the provider answers into ConfBuckets.
	Hist []int
	// Floor is the floor this kind is gated on now, and FloorFrom where it came
	// from. BelowFloor is how many provider answers fall under it -- the
	// escalation rate for this kind, counted rather than felt.
	Floor      float64
	FloorFrom  string
	BelowFloor int
	// Defeated is the finding of 2026-09-18 as a flag: the floor is ABOVE every
	// answer the provider has given for this kind, so nothing it says can ever
	// clear it and the step-up is universal.
	Defeated bool
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
		provider    []float64
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
		if e.Source == SourceJev {
			c.provider = append(c.provider, e.Confidence)
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
		row.Floor, row.FloorFrom = ResolveFloor(reg, kind, 0, false)
		row.Hist = make([]int, len(ConfBuckets))
		row.ProviderRows = len(c.provider)
		if len(c.provider) > 0 {
			sorted := append([]float64(nil), c.provider...)
			sort.Float64s(sorted)
			row.ConfMin, row.ConfMax = sorted[0], sorted[len(sorted)-1]
			row.ConfP25 = P25(sorted)
			row.Defeated = row.Floor > row.ConfMax
			for _, conf := range sorted {
				row.Hist[confBucket(conf)]++
				if conf < row.Floor {
					row.BelowFloor++
				}
			}
		}
		sum.Kinds = append(sum.Kinds, row)
	}
	return sum, nil
}

// P25 is the first quartile of an ASCENDING list, by nearest rank: the lowest
// value at or above a quarter of the rows. It is the number a floor is proposed
// from -- a floor that would have accepted three answers in four -- and it is
// here rather than in tune because the log summary reports it too, and one
// quartile computed two ways is two quartiles.
func P25(ascending []float64) float64 {
	if len(ascending) == 0 {
		return 0
	}
	rank := int(math.Ceil(0.25 * float64(len(ascending))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(ascending) {
		rank = len(ascending)
	}
	return ascending[rank-1]
}

// HistField renders the histogram as one field: every bucket, in order, named
// and counted. Fixed tables write every field, so a bucket with nothing in it
// is a zero and not an omission.
func (k KindSummary) HistField() string {
	parts := make([]string, 0, len(ConfBuckets))
	for i, name := range ConfBuckets {
		n := 0
		if i < len(k.Hist) {
			n = k.Hist[i]
		}
		parts = append(parts, fmt.Sprintf("%s:%d", name, n))
	}
	return strings.Join(parts, ",")
}

// confOrDash prints a confidence, or the dash where there is no provider answer
// to describe. A zero here would be a measurement nobody made.
func confOrDash(value float64, rows int) string {
	if rows == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", value)
}

// Render is the summary, one line per kind then the finish. Every kind carries
// the shape of the provider's answers beside the floor they were gated on: how
// many landed under it, and whether the floor is above every answer the
// provider has ever given for that kind, which is the step-up being the only
// outcome rather than a policy.
func (s Summary) Render() string {
	var b strings.Builder
	escalations := 0
	defeated := 0
	for _, k := range s.Kinds {
		escalations += k.Escalations
		if k.Defeated {
			defeated++
		}
		fmt.Fprintf(&b, "LOG kind=%s decisions=%d escalations=%d successes=%d failures=%d start_rung=%s start_height=%d default_rung=%s regenerated=%v floor=%.2f floor_from=%s provider_rows=%d conf_min=%s conf_max=%s conf_p25=%s below_floor=%d defeated=%v hist=%s\n",
			oneline.Field(k.Kind), k.Decisions, k.Escalations, k.Successes, k.Failures,
			oneline.Field(k.StartRung), k.StartHeight, oneline.Field(k.DefaultRung), k.Regenerated,
			k.Floor, oneline.Field(k.FloorFrom), k.ProviderRows,
			confOrDash(k.ConfMin, k.ProviderRows), confOrDash(k.ConfMax, k.ProviderRows),
			confOrDash(k.ConfP25, k.ProviderRows), k.BelowFloor, k.Defeated, k.HistField())
	}
	fmt.Fprintf(&b, "LOG OK rows=%d kinds=%d escalations=%d defeated=%d\n", s.Entries, len(s.Kinds), escalations, defeated)
	return b.String()
}
