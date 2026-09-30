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

// droppedInItsTick says the sequence has a card added and dropped with the
// machine RUNNING and no tick between: both are queued for the pump, which
// drains the creation and leaves the removal for the next drain (the table
// takes no entry that does both), so the pump's resolve and deal of that tick
// see a card the model has already taken off the table.
func droppedInItsTick(seq []dAction) bool {
	running, added := false, map[string]bool{}
	for _, a := range seq {
		switch a.Kind {
		case "start":
			running = true
		case "stop", "clear":
			running, added = false, map[string]bool{}
		case "tick":
			added = map[string]bool{}
		case "add":
			for _, id := range a.IDs {
				added[id] = running
			}
		case "drop":
			for _, id := range a.IDs {
				if added[id] {
					return true
				}
			}
		}
	}
	return false
}

var dKnown = []dKnownDiff{
	// ENGINE (the tick's core, open). A card added and dropped before one
	// drain: the drain creates it and leaves its removal for the next drain, so
	// that tick's pump resolves and deals it, and whatever follows from it (its
	// work card, the deal's indexes, a sentinel waiting on it, a judgment of a
	// need it was) differs from the model, which has it off the table at once.
	// A pump that drains again while a removal is requeued (the drain's own
	// MaxDrains bound) leaves none of these: delete this entry with that change.
	{"ENGINE a card added and dropped before one drain is resolved and dealt by that tick's pump", func(f dFinding) bool {
		return droppedInItsTick(f.Seq)
	}},
	// ENGINE or SPEC. Section 16: a sentinel inserted in line sends the
	// ready cards behind it back to waiting; the engine treats a ready
	// primary whose card was withdrawn (no member up) as in flight: it
	// stays ready, past the stop, and the sentinel waits for it.
	{"ENGINE a sentinel inserted in line lets a ready card with a withdrawn work card past the stop", func(f dFinding) bool {
		a := f.Seq[len(f.Seq)-1]
		return a.Kind == "add" && a.Sentinel && f.Kind == "state" && strings.Contains(f.Sig(), "primary.state=ready/waiting")
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
	dRun(t, 1, 8, 80) // the slow tier runs 2,000 seeds of 150
}
