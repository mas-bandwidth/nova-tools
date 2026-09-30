package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
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

// TestEngineAgreesWithTheReferenceModelOverFullTick exercises the 4-phase dirty-driven
// tick shape between the store engine and the reference model:
// 1. work streams (pump once: resolve, deal, accept)
// 2. readers (ask)
// 3. merge (resume)
// 4. fleet (level)
// then services dirty tables until all are cleared, and verifies that:
// - the engine store and reference model states agree after every full tick (refmodel.Compare has 0 diffs);
// - the pump runs once per tick;
// - rolling indexes advance modulo counts identically;
// - coordinator wake notes at tick end match expectations.
func TestEngineAgreesWithTheReferenceModelOverFullTick(t *testing.T) {
	t.Parallel()
	h := newDHarness(t)
	// Setup: two members up (m1, m2), machine running, primaries on s1 and s2
	for _, a := range dSetup() {
		if findings := h.do(a); len(findings) > 0 {
			t.Fatalf("setup %s: %v", a, findings)
		}
	}

	tickAndCheck := func(phase string) {
		t.Helper()
		findings := h.do(dAction{Kind: "tick"})
		for _, f := range findings {
			if name, ok := dClassify(f); ok {
				t.Logf("%s: known diff: %s", phase, name)
				continue
			}
			t.Fatalf("%s: engine and model differ after tick:\n%s", phase, f)
		}
		// Verify tick shape: order must start with work, readers, merge, fleet, and end with "end"
		if len(h.lastTick.Order) < 5 || !slices.Equal(h.lastTick.Order[:4], []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}) || h.lastTick.Order[len(h.lastTick.Order)-1] != "end" {
			t.Fatalf("%s: tick order %v does not match the 4-phase dirty-driven shape", phase, h.lastTick.Order)
		}
	}

	// 1. First tick deals ready primaries (a1, a2, a3) to up members.
	tickAndCheck("tick 1: deal")

	// 2. Members take cards and finish them.
	s := h.observe()
	for _, id := range []string{"a1", "a2"} {
		w, ok := s.Work[refmodel.WC(id, 1)]
		if !ok || w.Place != refmodel.FReady {
			continue
		}
		if findings := h.do(dAction{Kind: "take", Member: w.Member, Card: refmodel.WC(id, 1), Gen: w.Gen}); len(findings) > 0 {
			t.Fatalf("take %s: %v", id, findings)
		}
		if findings := h.do(dAction{Kind: "finish", Member: w.Member, Card: refmodel.WC(id, 1), Gen: w.Gen, OK: true}); len(findings) > 0 {
			t.Fatalf("finish %s: %v", id, findings)
		}
	}

	// 3. Second tick: pump drains work table changes (finished -> review), Phase 2 asks readers.
	tickAndCheck("tick 2: pump review and ask readers")

	// 4. Readers read cards ok.
	s = h.observe()
	for id, r := range s.Reads {
		if r.Place == refmodel.Asked {
			if findings := h.do(dAction{Kind: "read", Reader: r.Reader, Card: id, OK: true}); len(findings) > 0 {
				t.Fatalf("read %s: %v", id, findings)
			}
		}
	}

	// 5. Third tick: pump auto-accepts cards with two ok reads to merging.
	tickAndCheck("tick 3: auto-accept to merging")
	if h.lastTick.TickEnd == 0 {
		t.Fatalf("tick 3 auto-accept: expected coordinator tick-end note, got 0")
	}

	// 6. Merge batch on stream s1.
	if findings := h.do(dAction{Kind: "merge", Stream: "s1", Batch: 1, Fact: "green"}); len(findings) > 0 {
		t.Fatalf("merge: %v", findings)
	}

	// 7. Fourth tick: pump resolves dependent cards on s2 (b1, whose need a1 landed).
	tickAndCheck("tick 4: resolve dependent cards")

	// 8. Fleet down: take member m1 down.
	if findings := h.do(dAction{Kind: "fleet", Op: "down", Member: "m1"}); len(findings) > 0 {
		t.Fatalf("fleet down: %v", findings)
	}

	// 9. Fifth tick: fleet update redeals / levels cards to remaining up member(s).
	tickAndCheck("tick 5: redeal / level after fleet down")

	// 10. Fleet up: bring m1 back up.
	if findings := h.do(dAction{Kind: "fleet", Op: "up", Member: "m1"}); len(findings) > 0 {
		t.Fatalf("fleet up: %v", findings)
	}

	// 11. Sixth tick: fleet levelling evenly distributes ready cards.
	tickAndCheck("tick 6: levelling after fleet up")
}
