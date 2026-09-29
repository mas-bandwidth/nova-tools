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
	// ENGINE (or SPEC). The model's merge step needs a merging stream; the
	// engine's merge step on a stream with nothing queued settles its state
	// (steps_merge.go:99), whatever fact it is given: a red or rejected
	// fact on an idle stream whose every primary has landed lands it. It
	// is the one step that mends the stream a release left waiting.
	{"ENGINE a merge step on a stream with nothing queued settles it, whatever its fact", func(f dFinding) bool {
		return f.Kind == "refusal" && f.Seq[len(f.Seq)-1].Kind == "merge" && strings.Contains(f.Detail, "is not merging") &&
			strings.HasSuffix(f.Sig(), "stream.state")
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
	dRun(t, 1, 24, 80)
}
