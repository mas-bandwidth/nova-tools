package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The three results a manager has to hand when a routed unit comes back. They
// are the words on a card's receipt, not the log's own vocabulary: the verb
// maps them to the outcomes the ladder already counts, so nobody has to know
// that an abandoned card and a blocked one are the same row.
const (
	resultGreen   = "green"
	resultRed     = "red"
	resultBlocked = "blocked"
	// resultSkipped is H2's fourth word: a unit harvest did not run because a
	// precondition was not met. It is not a failure of a rung that was never
	// asked, and it is not a success either; it is a row so that coverage can
	// see it (SPEC-DECIDE, housekeeping H2, #1623).
	resultSkipped = "skipped"
)

// results is the closed set, in the order the help line names them.
var results = []string{resultGreen, resultRed, resultBlocked, resultSkipped}

// outcomeFor maps a manager's word to the ladder's outcome.
func outcomeFor(result string) (string, bool) {
	switch result {
	case resultGreen:
		return decide.OutcomeOK, true
	case resultRed:
		return decide.OutcomeFailed, true
	case resultBlocked:
		return decide.OutcomeAbandoned, true
	case resultSkipped:
		return decide.OutcomeSkipped, true
	default:
		return "", false
	}
}

// runOutcome is the outcome verb: what HAPPENED to a unit a decision routed.
//
// Rule 8 asks for a decision to be logged beside the outcome it predicted, and
// until this verb existed nothing wrote the second half: on 2026-09-18 the log
// held 78 rows, 73 escalations and zero successes, so `log --summary`
// regenerated no starting rung from anything. The kind and the rung are read
// from the decision this answers rather than taken from the caller, because a
// caller that has to retype them will eventually retype them wrong, and a
// unit no decision routed is a refusal rather than a row.
func runOutcome(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide outcome", flag.ContinueOnError)
	logPath := fs.String("log", "", "the escalation log the decision was written to (JSON lines)")
	unitID := fs.String("unit-id", "", "the unit's id, exactly as the decision carried it")
	result := fs.String("result", "", "what happened: "+strings.Join(results, " | "))
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "OUTCOME", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "OUTCOME", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*logPath) == "" {
		return refuse(stderr, "OUTCOME", "bad-arguments", "--log is required; refusing to guess the log's path")
	}
	if strings.TrimSpace(*unitID) == "" {
		return refuse(stderr, "OUTCOME", "bad-arguments", "--unit-id is required; an outcome with no unit is a row nobody can join to a decision")
	}
	outcome, ok := outcomeFor(strings.TrimSpace(*result))
	if !ok {
		return refuse(stderr, "OUTCOME", "bad-result", fmt.Sprintf(
			"--result %s is not one of %s; pass --result green for a landed unit, --result red for one that came back failing, --result blocked for one nobody could finish, --result skipped for one a precondition stopped before it ran",
			oneline.Field(*result), strings.Join(results, ", ")))
	}
	entries, err := decide.ReadEntries(*logPath)
	if err != nil {
		return refuse(stderr, "OUTCOME", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	dec, found := decide.LastDecision(entries, strings.TrimSpace(*unitID))
	if !found {
		return refuse(stderr, "OUTCOME", "no-decision", fmt.Sprintf(
			"the log holds no decision for unit %s, so there is nothing this outcome is the outcome OF; check the id the route line carried",
			oneline.Field(*unitID)))
	}
	row := decide.OutcomeEntry(dec.Unit, dec.Kind, dec.RungTried, outcome, now())
	if err := decide.AppendEntry(*logPath, row); err != nil {
		return refuse(stderr, "OUTCOME", "bad-record", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintf(stdout, "OUTCOME unit=%s kind=%s rung=%s result=%s outcome=%s\n",
		oneline.Field(row.Unit), oneline.Field(row.Kind), oneline.Field(row.RungTried),
		oneline.Field(strings.TrimSpace(*result)), oneline.Field(row.Outcome))
	return 0
}
