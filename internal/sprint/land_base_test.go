package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The base's health is one fact (docs/SPEC-SPRINT.md section 8, v11-base-red-auto-resume-now;
// the coordinator, 2026-10-04: the base went red for a few minutes, every stream that tried to
// land stopped with its own judgment, and eight were resumed by hand once it was fixed). A red
// base stops landing on it under ONE judgment naming the failing test: the first stream to
// meet it stops with it, and every other stream that meets it is refused under it and raises
// none (the lander's refusal, sprint.LandBaseRefused). A land pass that finds the base's tip
// green again records it (the lander's re-check, sprint.BaseGreen), and the tick resumes every stream stopped only by that base, by rule,
// in the log. A stream stopped on another base, or for another cause, stays stopped. On the
// twin store, with the lander's facts in its own words.
func TestStreamsResumeWhenTheBaseGatePassesAgain(t *testing.T) {
	t.Parallel()
	const red = "go test ./internal/x/: exit status 1: --- FAIL: TestPortInUse (0.01s) | x_test.go:9: listen tcp :8080: bind: address already in use | FAIL"
	for _, off := range []bool{false, true} {
		t.Run(map[bool]string{false: "rule on", true: "rule off"}[off], func(t *testing.T) {
			t.Parallel()
			r := newConflictRig(t)
			if off {
				r.m.SetRulesOff(sprint.RuleBaseGate)
			}
			brief := "c: the work tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"
			for _, st := range []string{"s2", "s3", "s4", "s5"} {
				r.must(store.AddStep(sprint.AddReq{Stream: st, Count: 1, Brief: brief}))
			}
			r.toMerging("s5-1")
			state := func(st string) string { return r.snap().StreamCtl(st).F("state") }

			// the lander's steps, as cmd/nova-sprint's land runs them
			refused := func(q sprint.MergeReq) store.Step {
				return store.Step{Verb: "merge", Args: store.ArgsOf(q), Load: []string{sprint.Merge, sprint.Work}, Mirrors: true,
					Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.LandBaseRefused(s, q) }}
			}
			green := func(q sprint.BaseGreenReq) store.Step {
				return store.Step{Verb: "merge base-green", Args: store.ArgsOf(q), Load: []string{sprint.Merge, sprint.Work},
					Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.BaseGreen(s, q) }}
			}
			// the base main is red: the first stream to meet it stops with the one judgment
			r.must(refused(sprint.MergeReq{Stream: "s1", Base: "main", BaseRed: red, Who: "lander"}))
			// a second stream stopped only by main's red, before the one-judgment rule (a hand
			// merge --base-red names no base): it too is resumed when main is green
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s2", BaseRed: red, Who: "coordinator"}))
			// the next stream to meet main red is refused under the judgment, as often as it
			// meets it, and neither stops nor raises a judgment of its own
			for range sprint.BaseGateStops + 1 {
				r.must(refused(sprint.MergeReq{Stream: "s3", Base: "main", BaseRefused: red, Who: "lander"}))
			}
			r.must(refused(sprint.MergeReq{Stream: "s3", Base: "main", BaseRed: red, Who: "lander"}))
			assert.NotEqual(t, sprint.StreamStopped, state("s3"))
			assert.Equal(t, "5", r.snap().StreamCtl("s3").F(sprint.FieldBaseGateRefused))
			// another base is red on its own, and a stream stopped for another cause
			r.must(refused(sprint.MergeReq{Stream: "s4", Base: "other", BaseRed: red, Who: "lander"}))
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s5", Batch: 1, Rejected: true, Note: "the push was rejected", Who: "lander"}))
			for _, st := range []string{"s1", "s2", "s4", "s5"} {
				require.Equal(t, sprint.StreamStopped, state(st), st)
			}
			var onMain []sprint.Open
			for _, o := range r.open(sprint.NBaseRed) {
				if strings.Contains(o.Note.What, "the base main ") {
					onMain = append(onMain, o)
				}
			}
			require.Len(t, onMain, 1, "ONE judgment for the red base main: %v", onMain)
			assert.Equal(t, "s1", onMain[0].Note.Stream)
			assert.Contains(t, onMain[0].Note.What, "failing TestPortInUse")
			assert.Len(t, r.open(sprint.NBaseRed), 3, "one for main, the hand stop's, one for other")

			// a tick while the base is red resumes nothing
			r.tick()
			for _, st := range []string{"s1", "s2", "s4", "s5"} {
				assert.Equal(t, sprint.StreamStopped, state(st), st)
			}

			// a land pass finds main's tip green again
			res := r.must(green(sprint.BaseGreenReq{Stream: "s1", Base: "main", Sha: "abc123", Who: "lander"}))
			assert.Len(t, res.Moved, 1, "every stream stopped on main's red is marked: %v", res.Moved)
			res = r.must(green(sprint.BaseGreenReq{Stream: "s2", Base: "main", Sha: "abc123", Who: "lander"}))
			assert.Len(t, res.Moved, 1, "the hand stop, named, is marked too: %v", res.Moved)
			r.tick()
			if off {
				for _, st := range []string{"s1", "s2"} {
					assert.Equal(t, sprint.StreamStopped, state(st), "with the rule off, %s waits for a mind", st)
				}
				assert.Len(t, r.open(sprint.NBaseRed), 3)
				assert.Empty(t, r.answeredBy(sprint.RuleBaseGate))
				r.clean("rule off")
				return
			}
			for _, st := range []string{"s1", "s2"} {
				assert.NotEqual(t, sprint.StreamStopped, state(st), "%s resumes by rule", st)
				assert.Contains(t, r.snap().StreamCtl(st).F("did"), "answered by rule base-gate: the base main passes its tree gate again at abc123")
				assert.Equal(t, "", r.snap().StreamCtl(st).F(sprint.FieldBaseGatePassed), "the resume clears the mark")
			}
			answered := r.answeredBy(sprint.RuleBaseGate)
			require.Len(t, answered, 2, "main's judgment and the hand stop's are answered by rule, in the log")
			assert.Contains(t, answered[0].What, "the base main passes its tree gate again at abc123")
			assert.Equal(t, sprint.StreamStopped, state("s4"), "another base's red stays")
			assert.Equal(t, sprint.StreamStopped, state("s5"), "another cause stays")
			assert.Len(t, r.open(sprint.NBaseRed), 1)
			r.clean("rule on")

			// a base found green with nothing stopped on it is refused, and changes nothing
			res, err := r.st.Run(r.ctx, green(sprint.BaseGreenReq{Stream: "s1", Base: "main", Sha: "abc124", Who: "lander"}))
			require.NoError(t, err)
			assert.NotEmpty(t, res.Refused)

			// a mark left from an earlier stop is no mark: s4, found green on other and resumed
			// by hand, stops again on other's red and waits for the next green pass
			r.must(green(sprint.BaseGreenReq{Stream: "s4", Base: "other", Sha: "def456", Who: "lander"}))
			r.must(store.ResumeStep(sprint.ResumeReq{Stream: "s4", Who: "coordinator"}))
			assert.Equal(t, "def456", r.snap().StreamCtl("s4").F(sprint.FieldBaseGatePassed), "a hand resume leaves the mark")
			r.tick()
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s4", BaseRed: red, Who: "coordinator"}))
			r.tick()
			assert.Equal(t, sprint.StreamStopped, state("s4"), "the old mark does not resume the new stop")
		})
	}
}

func TestFailingTestsAreNamedFromTheGatesFinding(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"TestA", "TestB/sub"}, sprint.FailingTests("--- FAIL: TestA (0.00s) | --- FAIL: TestB/sub (0.1s) | --- FAIL: TestA (0.00s)"))
	assert.Empty(t, sprint.FailingTests("go vet ./...: exit status 1"))
}
