package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
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
	// ENGINE only (readtier.go, the owner 2026-10-04). The tick raises "raise the read
	// tier of the stream?" when two readers disagree on one attempt, or a card
	// alternates broken and ok across attempts; the model knows no read tiers and no
	// such judgment. The spec names it (section 6); the model is owed it.
	// An ack of it is the engine's too: the model, which never opens it, refuses the ack.
	// Reads asked together (the interim rule of 2026-10-06) reach two readers disagreeing
	// on one attempt within the short seeds.
	{"ENGINE the read tier's escalation judgment is the engine's, not the model's", func(f dFinding) bool {
		if f.Kind == "refusal" && f.Seq[len(f.Seq)-1].Kind == "ack" && strings.Contains(f.Seq[len(f.Seq)-1].Type, "raise the read tier of the stream?") {
			return true
		}
		return f.Kind == "state" && strings.Contains(f.Sig(), "open.other:raise the read tier of the stream?")
	}},
	// MODEL, found 2026-10-06 when reads went together (the seeds reach an accept sooner),
	// and reachable on the code before it (replayed: CI red on a merging primary, a green
	// landing of it, a tick): the engine's tick leaves no ci judgment open on the landed
	// primary, the model's keeps it open. Owed: the model closes it at the landing, or the
	// spec says the engine must keep it.
	{"MODEL a landed primary's ci judgment stays open in the model", func(f dFinding) bool {
		return f.Kind == "state" && f.Seq[len(f.Seq)-1].Kind == "tick" && f.Sig() == "tick: open.ci=no/yes"
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
	// ENGINE. The tick raises one judgment at each backup edge and none while
	// the predicate holds (docs/SPEC-SPRINT.md, the backup state; sprint.TickBackup).
	// The reference model walks TickParts, which does not include that part, so
	// an open or acknowledged note of the four types, and an ack of one the
	// model has no decision for, differs. The spec names the edges.
	{"ENGINE the backup edges' judgments are the engine's, not the model's", backupEdgeDiff},
}

// backupEdgeDiff matches a finding whose every field is one of the four backup
// judgments, or an ack of one of them that the model refuses because it never
// opened the note.
func backupEdgeDiff(f dFinding) bool {
	field := func(name string) bool {
		for _, typ := range []string{sprint.NReadsBackedUp, sprint.NReadsClear, sprint.NMergesBackedUp, sprint.NMergesClear} {
			if name == "other:"+typ {
				return true
			}
		}
		return false
	}
	named := func(typ string) bool {
		for _, name := range []string{sprint.NReadsBackedUp, sprint.NReadsClear, sprint.NMergesBackedUp, sprint.NMergesClear} {
			if typ == name || typ == "other:"+name {
				return true
			}
		}
		return false
	}
	if len(f.Seq) == 0 {
		return false
	}
	if f.Kind == "refusal" {
		a := f.Seq[len(f.Seq)-1]
		if a.Kind != "ack" || !named(a.Type) {
			return false
		}
		if !strings.Contains(f.Detail, "model refuses") || !strings.Contains(f.Detail, "ack does not answer") {
			return false
		}
		for _, d := range f.Diffs {
			if !field(d.Field) {
				return false
			}
		}
		return true
	}
	if f.Kind != "state" || len(f.Diffs) == 0 {
		return false
	}
	for _, d := range f.Diffs {
		if (d.Table != "open" && d.Table != "acked") || !field(d.Field) {
			return false
		}
	}
	return true
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
			assert.Fail(t, fmt.Sprintf("seed %d: a difference between the engine and the model:\n%s", seed, dShrink(t, f)))
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
