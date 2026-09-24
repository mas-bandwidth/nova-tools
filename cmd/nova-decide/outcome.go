package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The four results a manager has to hand when a routed unit comes back, plus
// the fifth word that retracts one. They are the words on a card's receipt,
// not the log's own vocabulary: the verb maps them to the outcomes the ladder
// already counts, so nobody has to know that an abandoned card and a blocked
// one are the same row.
const (
	resultGreen   = "green"
	resultRed     = "red"
	resultBlocked = "blocked"
	// resultSkipped is H2's fourth word: a unit harvest did not run because a
	// precondition was not met. It is not a failure of a rung that was never
	// asked, and it is not a success either; it is a row so that coverage can
	// see it (SPEC-DECIDE, housekeeping H2, #1623).
	resultSkipped = "skipped"
	// resultVoid retracts an earlier outcome row -- the manager's correction
	// of a row that was not true. A void APPENDS its own row keyed to the
	// target's unit and Time stamp (exactly one row); both stand, and the summary excludes the voided
	// row from every count (SPEC-PULSE rule 18, nova-tools #2034).
	resultVoid = "void"
)

const (
	// sourceVoid is the Source value a void record carries. It is not a
	// ladder outcome: a void is a retraction of one, never one itself.
	sourceVoid = "void"
	// voidPrefix marks a void record's Reason field. The target Time stamp
	// follows the prefix so the log reader finds the row the void keys
	// without parsing free text.
	voidPrefix = "void:"
)

// results is the closed set, in the order the help line names them.
var results = []string{resultGreen, resultRed, resultBlocked, resultSkipped, resultVoid}

// outcomeFor maps a manager's word to the ladder's outcome. Void is not a
// ladder outcome; the caller diverts on it before this is asked.
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
//
// --result void is the fifth word (nova-tools #2034, SPEC-PULSE rule 18):
// it appends a SourceVoid row keyed by --unit-id and --of-time to exactly one
// original outcome row (an ambiguous pair is refused). Both rows stand and the summary excludes the voided row from
// every count, so a tuning table the manager could not take back is the
// one the receipt no longer costs a route its floor.
func runOutcome(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide outcome", flag.ContinueOnError)
	logPath := fs.String("log", "", "the escalation log the decision was written to (JSON lines)")
	unitID := fs.String("unit-id", "", "the unit's id, exactly as the decision carried it")
	result := fs.String("result", "", "what happened: "+strings.Join(results, " | "))
	ofTime := fs.String("of-time", "", "with --result void: the RFC3339 Time of the --unit-id outcome row to retract; refuse to guess one")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "outcome") {
			return 0
		}
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
	isVoid := strings.TrimSpace(*result) == resultVoid
	if isVoid && strings.TrimSpace(*ofTime) == "" {
		return refuse(stderr, "OUTCOME", "bad-arguments", "--of-time is required with --result void; a void with no target is a row nobody can join to a mistake")
	}
	if !isVoid {
		outcome, ok := outcomeFor(strings.TrimSpace(*result))
		if !ok || outcome == "" {
			return refuse(stderr, "OUTCOME", "bad-result", fmt.Sprintf(
				"--result %s is not one of %s; pass --result green for a landed unit, --result red for one that came back failing, --result blocked for one nobody could finish, --result skipped for one a precondition stopped before it ran, --result void --of-time <rfc3339> to retract an earlier outcome",
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
	entries, err := decide.ReadEntries(*logPath)
	if err != nil {
		return refuse(stderr, "OUTCOME", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	target := strings.TrimSpace(*ofTime)
	targetUnit, targetKind, matches := findVoidTarget(entries, strings.TrimSpace(*unitID), target)
	if matches == 0 {
		return refuse(stderr, "OUTCOME", "no-target", fmt.Sprintf(
			"--unit-id %s --of-time %s does not key an outcome row in this log; a void is the key of one outcome row, not an id the call may invent",
			oneline.Field(*unitID), oneline.Field(target)))
	}
	if matches > 1 {
		return refuse(stderr, "OUTCOME", "ambiguous-target", fmt.Sprintf(
			"--unit-id %s --of-time %s keys %d outcome rows (the stamp has second precision); a void retracts exactly one row and will not guess which",
			oneline.Field(*unitID), oneline.Field(target), matches))
	}
	row := voidEntry(targetUnit, targetKind, target, now())
	if err := decide.AppendEntry(*logPath, row); err != nil {
		return refuse(stderr, "OUTCOME", "bad-record", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintf(stdout, "OUTCOME unit=%s kind=%s result=%s void_of=%s\n",
		oneline.Field(row.Unit), oneline.Field(row.Kind),
		oneline.Field(strings.TrimSpace(*result)), oneline.Field(target))
	return 0
}

// voidEntry is the row that retracts an earlier OUTCOME row by its Time
// stamp. The log is append-only and the earlier row is left in place, so a
// reader sees the correction rather than a rewrite; the log verb (runLog)
// excludes the row Reason keys from every count (SPEC-PULSE rule 18, #2034).
func voidEntry(unit, kind, targetTime string, now_ time.Time) decide.Entry {
	return decide.Entry{
		Time:   now_.UTC().Format(time.RFC3339),
		Unit:   unit,
		Kind:   kind,
		Source: sourceVoid,
		Reason: voidPrefix + targetTime,
	}
}

// findVoidTarget looks up the outcome row the caller named by unit AND Time
// stamp, returning its unit and kind so the void record can carry the same
// facts the retracted row did, plus how many rows matched. The stamp alone is
// not a key: OutcomeEntry writes RFC3339 at second precision, so two units'
// outcomes can share one (Stella's hold on #2850). Zero matches is no target
// and more than one is ambiguous; the caller refuses both, so a void written
// to the log always names exactly one outcome row.
func findVoidTarget(entries []decide.Entry, unit, target string) (string, string, int) {
	unit = strings.TrimSpace(unit)
	target = strings.TrimSpace(target)
	if unit == "" || target == "" {
		return "", "", 0
	}
	var gotKind string
	matches := 0
	for _, e := range entries {
		if e.Source != decide.SourceOutcome {
			continue
		}
		if strings.TrimSpace(e.Time) != target || outcomeUnit(e) != unit {
			continue
		}
		matches++
		if matches == 1 {
			gotKind = e.Kind
			if gotKind == "" {
				gotKind = strings.TrimSpace(e.Evidence.Kind)
			}
		}
	}
	if matches == 0 {
		return "", "", 0
	}
	return unit, gotKind, matches
}

// outcomeUnit is the unit an outcome row belongs to: Unit, or the evidence id
// on a row written before Unit was carried.
func outcomeUnit(e decide.Entry) string {
	if u := strings.TrimSpace(e.Unit); u != "" {
		return u
	}
	return strings.TrimSpace(e.Evidence.ID)
}
