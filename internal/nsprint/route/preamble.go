package route

import (
	"fmt"
	"strings"
)

// Per-route preambles (#2498 S8). A route row may carry the fault classes the
// attribution (rowan-new reports/attribution-2026-09-21.md) charged to its
// model's sub-8 results; the preamble is one paragraph built from them, one
// sentence per class, for the card to open with on that route. The classes
// are the attribution's levers: only the ones a model can act on have a
// sentence here, and the harness-side ones (a PR title or body the harvester
// wrote, a model-only failure) are named in harnessSide and never preambled.

// FaultsFrom values: where a row's classes came from.
const (
	FromModel = "model" // the attribution charged the row's own model with them
	FromRung  = "rung"  // the model has none; the rung's pooled classes stand in
)

// rungFloor is how often a class must appear across the rung's models to be
// pooled into a FromRung row's preamble.
const rungFloor = 3

// maxPreamble bounds one paragraph: every card on the route pays for it.
const maxPreamble = 1200

// faultSentences is the one sentence per actionable class, in the attribution
// lever's own words turned to the model.
var faultSentences = map[string]string{
	"CARD-WIRE":         "Wire what you build: every new function, check or flag is called from the production path the card names, the diff shows that call site, and a sibling you added and left unused is removed.",
	"CARD-GATE":         "Run the repo's gate yourself before you finish: the formatter prints nothing, vet is clean, the docs and CI class tests pass (in nova-tools: gofmt -l, go vet ./..., go test ./internal/docs/ ./internal/ci/), and git diff --name-only prints exactly the files you meant to change.",
	"CARD-NOCTRL":       "Give every new test a control that bites: show it fails with your change reverted or mutated, and say in the result how you showed it.",
	"CARD-STALE":        "First check that the defect still reproduces at the tip; if the criterion is already met, answer ALREADY-PRESENT with the evidence instead of making a change.",
	"CARD-LINT":         "Run the repo's lint job at its CI-pinned version on every package you touched and fix each finding.",
	"TOOLING-CLEANTREE": "Commit source only: no built binary, no scratch, notes or result file; git status shows nothing you did not mean to add.",
	"CARD-BENCHPIN":     "A measurement names the bench it ran on and refuses to run on another host.",
	"CARD-DOCGUARD":     "A docs, spec or help change ships with a docs-guard test that fails when the text drifts.",
}

// harnessSide are the attribution's levers no preamble can move: the fault is
// in the harvester, the base or the model's own reasoning.
var harnessSide = map[string]bool{
	"NONE":          true, // MODEL-caused: no lever
	"TOOLING-TITLE": true,
	"TOOLING-BODY":  true,
	"TOOLING-PRIOR": true,
	"TOOLING-BASE":  true,
	"BASE-CADENCE":  true,
}

// Reachable lists the routes a card on rung can be sent to under some work
// type: the rung's rows that are not dropped, and the routes an override for
// the rung names, less any route flagged dead or benched (Check refuses those
// whatever lists them). Table order.
func (t *Table) Reachable(rung string) []string {
	named := map[string]bool{}
	for _, o := range t.overrides {
		if o.Rung == rung {
			for _, r := range o.Routes {
				named[r] = true
			}
		}
	}
	var out []string
	for _, r := range t.rows {
		if r.Rung != rung || r.Flag == FlagDead || r.Flag == FlagBenched {
			continue
		}
		if r.State != Dropped || named[r.Route] {
			out = append(out, r.Route)
		}
	}
	return out
}

// Preamble is the route's one paragraph: an opening line naming where its
// classes came from, then one sentence per class in the row's order. A route
// with no classes, or no such route, is an error.
func (t *Table) Preamble(route string) (string, error) {
	i, ok := t.byRoute[route]
	if !ok {
		return "", fmt.Errorf("no route %s in the table", route)
	}
	r := t.rows[i]
	if len(r.Faults) == 0 {
		return "", fmt.Errorf("route %s carries no preamble (no faults in routes.yaml)", route)
	}
	var b strings.Builder
	if r.FaultsFrom == FromModel {
		fmt.Fprintf(&b, "Cards on %s have lost points on %s, so:", r.Model, strings.Join(r.Faults, ", "))
	} else {
		fmt.Fprintf(&b, "Cards on this rung have lost points most on %s, so:", strings.Join(r.Faults, ", "))
	}
	for _, c := range r.Faults {
		b.WriteString(" ")
		b.WriteString(faultSentences[c])
	}
	return b.String(), nil
}

// parseFaults reads a faults [flow, list]; every class needs a sentence.
func parseFaults(raw string) ([]string, error) {
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil, fmt.Errorf("faults wants a [flow, list] of fault classes")
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"), ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := faultSentences[c]; !ok {
			return nil, fmt.Errorf("fault class %s has no preamble sentence", c)
		}
		if seen[c] {
			return nil, fmt.Errorf("fault class %s appears twice", c)
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("faults is empty")
	}
	return out, nil
}
