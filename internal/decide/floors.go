// Proposing a confidence floor PER KIND from the escalation log (rule 8, and
// the finding of 2026-09-18).
//
// One floor for every kind is one number standing in for ten different
// questions. Routing the day's twenty real units through live Jev put 13 of 13
// provider answers below the 0.90 floor -- 0.70 to 0.78 -- and every one of
// them stepped up: a 100% escalation rate, against tune's own 0.7 cap, and the
// end of "the lowest rung the evidence supports", because the evidence never
// got to support anything.
//
// The fix is a floor per kind, and the floor is MEASURED. For each kind this
// takes the provider answers the log holds, keeps the ones that STOOD -- no
// failure, no timeout, no abandonment and no refusal recorded against them --
// and proposes their p25: a floor three answers in four would have cleared.
// Fewer than MinFloorRows answers is not a quartile, so that kind is not
// proposed a floor at all and keeps the built-in default, which the log line
// says is a floor with no rows behind it.
//
// And a floor above the provider's observed MAX for a kind is refused, with the
// remedy. That floor cannot be met by anything the provider has ever said: it
// does not gate the decision, it deletes it.
package decide

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// MinFloorRows is the fewest provider answers a kind's floor may be proposed
// from. Two is the fewest a quartile can be read off at all, and a floor from
// two rows is a floor to re-propose next week -- which is why every row carries
// the count it was measured from.
const MinFloorRows = 2

// FloorProposal is one kind's read: the provider answers the log holds for it,
// how many stood, their shape, the floor in force now and the floor proposed
// from the rows.
type FloorProposal struct {
	Kind string
	// Rows is every provider answer for this kind; Stood is those with no
	// failure recorded against them, and Failed the rest.
	Rows   int
	Stood  int
	Failed int
	// Max and P25 describe the answers that stood.
	Max float64
	P25 float64
	// Current is the floor in force now and CurrentFrom where it came from.
	Current     float64
	CurrentFrom string
	// Floor is the proposal, set only when Proposed.
	Floor    float64
	Proposed bool
	// Defeated says the CURRENT floor is above every answer that stood: the
	// finding this verb exists to surface.
	Defeated bool
	// Note is why no floor was proposed, where none was.
	Note string
}

// FloorProposals is the whole read, one row per kind, in kind order.
type FloorProposals struct {
	Rows      int
	Proposals []FloorProposal
}

// Proposed is the proposals that name a floor, as registry rows ready to be
// written. The From field is the measurement, so a floor in the registry can
// always be traced back to the rows behind it.
func (p FloorProposals) Proposed(from string) []KindFloor {
	out := make([]KindFloor, 0, len(p.Proposals))
	for _, row := range p.Proposals {
		if !row.Proposed {
			continue
		}
		out = append(out, KindFloor{
			Kind:  row.Kind,
			Floor: row.Floor,
			From:  fmt.Sprintf("p25 of %d provider answers that stood, %s (max %.2f)", row.Stood, from, row.Max),
		})
	}
	return out
}

// ProposeFloors reads the log and proposes one floor per kind. A row that is
// not a provider answer is not evidence about the provider and is left out: the
// rules' own confidences are the machinery's numbers, and averaging them in
// would hide exactly what this is measuring.
func ProposeFloors(reg *Registry, entries []Entry) (FloorProposals, error) {
	if reg == nil || len(reg.Minds) == 0 {
		return FloorProposals{}, fmt.Errorf("decide: no registry; a floor with no ladder under it gates nothing")
	}
	stood := map[string][]float64{}
	failed := map[string]int{}
	kinds := map[string]bool{}
	for i, e := range entries {
		kind := strings.TrimSpace(e.Kind)
		if kind == "" {
			kind = strings.TrimSpace(e.Evidence.Kind)
		}
		if !KnownKind(kind) {
			return FloorProposals{}, fmt.Errorf("decide: log row %d names kind %q, which is not one of %s", i+1, e.Kind, strings.Join(Kinds, ", "))
		}
		// Every kind the log holds gets a row, including one the provider has
		// never answered: "guard has no floor and here is why" is a reading,
		// and a kind that simply does not appear is a reader guessing.
		kinds[kind] = true
		if e.Source != SourceJev {
			continue
		}
		if entryFailed(e) {
			failed[kind]++
			continue
		}
		stood[kind] = append(stood[kind], e.Confidence)
	}
	names := make([]string, 0, len(kinds))
	for kind := range kinds {
		names = append(names, kind)
	}
	sort.Strings(names)

	out := FloorProposals{Rows: len(entries)}
	for _, kind := range names {
		confs := append([]float64(nil), stood[kind]...)
		sort.Float64s(confs)
		row := FloorProposal{
			Kind:   kind,
			Rows:   len(confs) + failed[kind],
			Stood:  len(confs),
			Failed: failed[kind],
		}
		row.Current, row.CurrentFrom = ResolveFloor(reg, kind, 0, false)
		if len(confs) == 0 {
			row.Note = fmt.Sprintf("no provider answer for %s stood; a floor with no rows behind it is untuned", kind)
			out.Proposals = append(out.Proposals, row)
			continue
		}
		row.Max = confs[len(confs)-1]
		row.P25 = P25(confs)
		row.Defeated = row.Current > row.Max
		if len(confs) < MinFloorRows {
			row.Note = fmt.Sprintf("%d provider answer stood for %s, fewer than the %d a quartile can be read from", len(confs), kind, MinFloorRows)
			out.Proposals = append(out.Proposals, row)
			continue
		}
		row.Floor = round2(row.P25)
		row.Proposed = true
		out.Proposals = append(out.Proposals, row)
	}
	return out, nil
}

// entryFailed reports whether the log recorded a failure against this decision.
// A row with no outcome yet has not failed -- the loop fills the outcome in
// later -- and the render prints stood beside rows so a reader can see how much
// of the measurement is still unlabeled.
func entryFailed(e Entry) bool {
	if strings.TrimSpace(e.Refusal) != "" {
		return true
	}
	switch e.Outcome {
	case OutcomeFailed, OutcomeTimeout, OutcomeAbandoned:
		return true
	}
	return false
}

// round2 keeps a proposed floor at the two places every line prints it to, so
// the number written to the registry is the number a reader sees.
func round2(f float64) float64 { return math.Round(f*100) / 100 }

// CheckFloors refuses a floor no answer could ever meet: one above the
// provider's observed maximum for that kind. Such a floor does not gate the
// decision, it deletes it -- every answer steps up, whatever the provider says
// -- and that is the 2026-09-18 defect being written back into the file it came
// from. The message carries the remedy: the number to write instead.
func CheckFloors(floors []KindFloor, proposals FloorProposals) error {
	observed := make(map[string]FloorProposal, len(proposals.Proposals))
	for _, row := range proposals.Proposals {
		if row.Stood > 0 {
			observed[row.Kind] = row
		}
	}
	for _, f := range floors {
		row, ok := observed[f.Kind]
		if !ok {
			continue
		}
		if f.Floor > row.Max {
			return fmt.Errorf("decide: floor %.2f for kind %s is above every answer the provider has given it (max %.2f over %d rows that stood): it would not gate the decision, it would escalate every one; write %.2f, or --floor-for %s=%.2f",
				f.Floor, f.Kind, row.Max, row.Stood, row.P25, f.Kind, row.P25)
		}
	}
	return nil
}

// Render is the proposal, one line per kind then the finish. Every field is on
// every line and a number nobody measured is a dash.
func (p FloorProposals) Render() string {
	var b strings.Builder
	proposed, defeated := 0, 0
	for _, row := range p.Proposals {
		if row.Proposed {
			proposed++
		}
		if row.Defeated {
			defeated++
		}
		fmt.Fprintf(&b, "TUNE FLOOR kind=%s rows=%d stood=%d failed=%d max=%s p25=%s current=%.2f current_from=%s floor=%s proposed=%v defeated=%v note=%s\n",
			oneline.Field(row.Kind), row.Rows, row.Stood, row.Failed,
			dashFloat(row.Max, row.Stood > 0), dashFloat(row.P25, row.Stood > 0),
			row.Current, oneline.Field(row.CurrentFrom),
			dashFloat(row.Floor, row.Proposed), row.Proposed, row.Defeated,
			oneline.Quote(oneline.Escape(row.Note)))
	}
	fmt.Fprintf(&b, "TUNE FLOORS OK rows=%d kinds=%d proposed=%d defeated=%d\n", p.Rows, len(p.Proposals), proposed, defeated)
	return b.String()
}

// dashFloat prints a number that was measured, or the dash for one that was
// not. A zero would be a measurement nobody made.
func dashFloat(f float64, known bool) string {
	if !known {
		return "-"
	}
	return fmt.Sprintf("%.2f", f)
}

// MergeFloors writes the floors into a registry document, leaving every other
// field of the file exactly as it was -- the comments above all, which are
// where the ladder explains itself. The document is decoded as raw fields and
// re-encoded, so a key this package has never heard of survives the write.
func MergeFloors(registryJSON []byte, floors []KindFloor) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(registryJSON, &doc); err != nil {
		return nil, fmt.Errorf("decide: cannot merge floors: the registry is not a JSON object: %w", err)
	}
	if _, ok := doc["minds"]; !ok {
		return nil, fmt.Errorf("decide: cannot merge floors: the registry names no minds")
	}
	// Whatever is written must parse as a registry, floors and all, or it is
	// not written: a file that refuses on the next read is worse than no write.
	encoded, err := json.Marshal(floors)
	if err != nil {
		return nil, fmt.Errorf("decide: cannot merge floors: %w", err)
	}
	doc["floors"] = encoded
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("decide: cannot merge floors: %w", err)
	}
	out = append(out, '\n')
	if _, err := ParseRegistry(out); err != nil {
		return nil, fmt.Errorf("decide: the merged registry would not parse: %w", err)
	}
	return out, nil
}
