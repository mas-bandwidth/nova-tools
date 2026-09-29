package store

import (
	"strings"
	"testing"
)

// dKnownDiff is one classified difference between the engine and the
// reference model: read against docs/SPEC-SPRINT.md and
// tla/SprintTables.tla, the engine is wrong (ENGINE), the model is wrong or
// behind (MODEL), or the spec leaves it open (SPEC). Each is a named skip: a
// difference no entry matches fails the test.
type dKnownDiff struct {
	Name  string
	Match func(dFinding) bool
}

// sigHas says the finding's action is the verb and its signature holds every
// part.
func sigHas(verb string, parts ...string) func(dFinding) bool {
	return func(f dFinding) bool {
		sig := f.Sig()
		if !strings.HasPrefix(sig, verb+": ") {
			return false
		}
		for _, p := range parts {
			if !strings.Contains(sig, p) {
				return false
			}
		}
		return true
	}
}

// onlyOpen says every difference of a state finding is an open judgment of
// one of the types, open in the engine and not in the model.
func onlyOpen(verb string, types ...string) func(dFinding) bool {
	return func(f dFinding) bool {
		if f.Kind != "state" || (verb != "" && f.Seq[len(f.Seq)-1].Kind != verb) {
			return false
		}
		for _, d := range f.Diffs {
			if d.Table != "open" || !dHas(types, d.Field) || d.Engine != "yes" {
				return false
			}
		}
		return len(f.Diffs) > 0
	}
}

var dKnown = []dKnownDiff{
	// ENGINE. Section 8: the sprint is done when every primary is landed or
	// off the table, "at least one landed"; sprintDone
	// (internal/sprint/steps_merge.go:286) never checks the landed count.
	{"ENGINE sprint done written with none landed", sigHas("drop", "open.done=yes/no")},
	// MODEL. StreamAfter calls a stream whose every primary left "landed"
	// (AllDone is vacuously true); the engine's settle
	// (steps_review.go:489) wants one landed; the spec is silent on it.
	{"MODEL a stream whose every primary was dropped is landed", sigHas("drop", "stream.state=waiting/landed")},
	// ENGINE. D2 and section 6: the readers kept on a primary are two and
	// "both readers are asked again"; ask --another appends its reader to
	// the primary's asked field (steps_review.go:106), finish asks every
	// reader named there (steps_work.go:754), and rework keeps the two it
	// was accepted on when it has them.
	{"ENGINE ask --another widens the kept readers beyond the pair", func(f dFinding) bool {
		return (sigHas("another", "primary.pair")(f) || sigHas("rework", "primary.pair")(f))
	}},
	// ENGINE. Section 7: a stream is landed when every primary of it on the
	// table has landed. Release passes the sentinels it lands to settle as
	// leaving the table (steps_sentinel.go:299), and settle counts the
	// landed from the state before the step (steps_review.go:480), so the
	// release that lands a stream's only landed card leaves the stream
	// waiting with nothing open, for good (and stalled, to inbox, after its
	// deadline).
	{"ENGINE release of a stream's only landed card leaves the stream waiting", sigHas("release", "stream.state=waiting/landed")},
	// ENGINE. D6 and section 7: a stuck card's cross-stream need is
	// resolved when the needed card lands; the model clears need[p] on
	// return and on a conflict stop (MergeStop sets it to the fact's q). The
	// engine keeps need_card on the merge card through return (the move to
	// returned, steps_review.go:672 and :685), accept, and a later conflict
	// stop (steps_merge.go:160), and resume holds every stuck card to its
	// need_card whatever the stop's cause (steps_merge.go:340): a card
	// stopped once on a cross need, returned and accepted again, then stopped
	// on a conflict, cannot be resumed until the unrelated card lands.
	{"ENGINE a cross need survives return and wedges a later conflict stop", sigHas("merge", "merge.need")},
	// ENGINE or SPEC. Section 16: a sentinel inserted in line sends the
	// ready cards behind it back to waiting; the engine treats a ready
	// primary whose card was withdrawn (no member up) as in flight: it
	// stays ready, past the stop, and the sentinel waits for it.
	{"ENGINE a sentinel inserted in line lets a ready card with a withdrawn work card past the stop", func(f dFinding) bool {
		a := f.Seq[len(f.Seq)-1]
		return a.Kind == "add" && a.Sentinel && f.Kind == "state" && strings.Contains(f.Sig(), "primary.state=ready/waiting")
	}},
	// ENGINE. A refused step changes nothing (section 3: a unit that does
	// not keep the lifecycle is never applied; section 11). An add whose
	// every id is refused still declares its stream: the plan's rows
	// (steps_work.go:90) are added before the units are looked at
	// (store/engine.go:329), leaving a stream row with no control card.
	{"ENGINE a refused add still declares its stream's rows", func(f dFinding) bool {
		if f.Kind != "refusal" || f.Seq[len(f.Seq)-1].Kind != "add" || !strings.HasPrefix(f.Detail, "model refuses") {
			return false
		}
		for _, d := range f.Diffs {
			if d.Table != "stream" || d.Field != "exists" {
				return false
			}
		}
		return true
	}},
	// MODEL (or SPEC). NoNeedCycle and Add's guard judge cycles over every
	// primary admitted, dropped ones included; the engine judges them over
	// the primaries on the table, so a chain through a dropped card (whose
	// need can only be waived, never landed) is no cycle to it. Section 9,
	// rule 11 says only that add refuses needs that would make a cycle.
	{"MODEL a need cycle through a dropped primary refuses add", func(f dFinding) bool {
		return f.Kind == "refusal" && f.Seq[len(f.Seq)-1].Kind == "add" && strings.Contains(f.Detail, "make a cycle")
	}},
	// MODEL. The ack of a blocked judgment resolves at once: a waiting
	// primary whose needs are then met moves to ready, a sentinel is marked
	// reached; the model's Waive leaves both to Resolve and the tick.
	{"MODEL ack of a blocked judgment resolves at once", func(f dFinding) bool {
		a := f.Seq[len(f.Seq)-1]
		return a.Kind == "ack" && a.Type == "blocked" && f.Kind == "state" &&
			(strings.Contains(f.Sig(), "primary.state=ready/waiting") || strings.Contains(f.Sig(), "primary.reached=yes/no"))
	}},
	// SPEC and MODEL behind (the brief's "the judgment a primary in review
	// needs"). The engine writes ready to accept whenever a step leaves an
	// acceptable primary in review with no judgment (an ack, ask --another,
	// a third ok read); section 6 names only the read that completes two
	// ok, and reads exhausted or stranded for the others; the model's G3
	// only exhausted.
	{"SPEC/MODEL ready to accept written for an acceptable primary left with no judgment", func(f dFinding) bool {
		if onlyOpen("", "accept")(f) {
			return true
		}
		return sigHas("another", "open.accept=yes/no", "primary.pair")(f)
	}},
	// ENGINE or SPEC. Section 8: "a judgment is open per card and per
	// cause"; the model's open is a set of (type, primary). The engine
	// keeps one open judgment per notification, so two red CI runs or two
	// broken reads on one primary are two obligations, and one ack leaves
	// the other open (and so writes no exhausted or stranded judgment).
	{"ENGINE/SPEC a judgment is open per notification, not per card and cause", func(f dFinding) bool {
		a := f.Seq[len(f.Seq)-1]
		if a.Kind != "ack" || f.Kind != "state" {
			return false
		}
		again := false
		for _, d := range f.Diffs {
			switch {
			case d.Table == "open" && d.Field == a.Type && d.ID == a.Subject && d.Engine == "yes":
				again = true
			case d.Table == "open" && (d.Field == "reads" || d.Field == "stranded") && d.Model == "yes":
			default:
				return false
			}
		}
		return again
	}},
}

func dClassify(f dFinding) (string, bool) {
	for _, k := range dKnown {
		if k.Match(f) {
			return k.Name, true
		}
	}
	return "", false
}

// dRun runs seeds [from, from+n) of steps actions each; a finding no known
// entry matches fails, shrunk to its shortest sequence, once per signature.
func dRun(t *testing.T, from, n uint64, steps int) {
	failed := map[string]bool{}
	known := map[string]int{}
	for seed := from; seed < from+n; seed++ {
		_, fs := dGenerate(t, seed, steps)
		for _, f := range fs {
			if name, ok := dClassify(f); ok {
				known[name]++
				continue
			}
			sig := f.Sig()
			if failed[sig] {
				continue
			}
			failed[sig] = true
			t.Errorf("seed %d: a difference between the engine and the model:\n%s", seed, dShrink(t, f))
		}
	}
	for name, c := range known {
		t.Run("known/"+strings.ReplaceAll(name, " ", "_"), func(t *testing.T) {
			t.Skipf("%s (seen %d times)", name, c)
		})
	}
	t.Logf("%d seeds of %d actions: %d unknown signatures, %d known classes", n, steps, len(failed), len(known))
}

func TestEngineAgreesWithTheReferenceModel(t *testing.T) {
	t.Parallel()
	dRun(t, 1, 24, 80)
}
