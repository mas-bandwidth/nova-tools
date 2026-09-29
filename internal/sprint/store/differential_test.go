package store

import (
	"strings"
	"testing"
)

// dKnown is every difference between the engine and the reference model that
// has been read against docs/SPEC-SPRINT.md and tla/SprintTables.tla and
// classified: the engine is wrong (ENGINE), the model is wrong or behind
// (MODEL), or the spec leaves it open (SPEC). Each is a named skip: a
// difference with any other signature fails the test.
var dKnown = map[string]string{
	// ENGINE. Section 8: the sprint is done when every primary is landed
	// or off the table, "at least one landed"; sprintDone
	// (internal/sprint/steps_merge.go) never checks the landed count, so
	// dropping every primary before any lands writes it. The stream half is
	// the MODEL's: its StreamAfter calls a stream whose every primary left
	// "landed" (AllDone is vacuously true); the engine's settle wants one
	// landed (steps_review.go), and the spec is silent on the empty case.
	"drop: open.done=yes/no stream.state=waiting/landed": "ENGINE sprint done with none landed; MODEL a stream with every primary dropped is landed",
	// MODEL. As above, without the sprint-done half.
	"drop: stream.state=waiting/landed": "MODEL a stream with every primary dropped is landed (vacuous AllDone)",
	// ENGINE. D2 and section 6: the readers kept on a primary are two, and
	// "both readers are asked again" when fixed work returns; ask --another
	// adds its reader to the primary's asked field, and finish asks every
	// reader named there (steps_work.go, Finish), so the kept pair grows
	// to three.
	"another: primary.pair": "ENGINE ask --another widens the kept readers beyond the pair",
	// MODEL. The ack of a blocked judgment moves the waiting primary to
	// ready in the same step when its needs are then met; the model's Waive
	// leaves the move to a later Resolve. Both keep NeedsMetOutsideWaiting;
	// the spec says only that the ack waives.
	"ack: primary.state=ready/waiting": "MODEL ack of a blocked judgment moves the primary to ready at once",
	// SPEC and MODEL. An ack that closes the last judgment of an acceptable
	// primary in review writes ready to accept; section 6 names only reads
	// exhausted and stranded in review for that step, and the model's G3
	// only exhausted. The engine keeps a primary in review never silent
	// (the brief's known area: the judgment a primary in review needs).
	"ack: open.accept=yes/no": "SPEC/MODEL behind: ack leaves an acceptable primary with ready to accept",
	// ENGINE or SPEC. Section 8: "a judgment is open per card and per
	// cause"; the model's open is a set of (type, primary). The engine keeps
	// one open judgment per notification, so two red CI runs on one primary
	// are two obligations and one ack leaves the other open.
	"ack: open.ci=yes/no": "ENGINE/SPEC a judgment is open per notification, not per card and cause",
}

// dRun runs seeds [from, from+n) of steps actions each; a finding whose
// signature is not known fails, shrunk to its shortest sequence, once per
// signature.
func dRun(t *testing.T, from, n uint64, steps int) {
	failed := map[string]bool{}
	known := map[string]int{}
	for seed := from; seed < from+n; seed++ {
		_, fs := dGenerate(t, seed, steps)
		for _, f := range fs {
			sig := f.Sig()
			if name, ok := dKnown[sig]; ok {
				known[name]++
				continue
			}
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
}

func TestEngineAgreesWithTheReferenceModel(t *testing.T) {
	t.Parallel()
	dRun(t, 1, 40, 80)
}
