package sprint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The base's health is one fact (docs/SPEC-SPRINT.md section 8, v11-base-red-auto-resume-now;
// the coordinator, 2026-10-04: the base went red for a few minutes, every stream that tried to
// land stopped with a judgment of its own, and eight were resumed by hand once it was fixed).
// The first stream a red base stops carries the one judgment, naming the failing tests; every
// other stream that meets the same red base is refused under it and never stopped, so it
// raises no judgment of its own and lands at its first pass on a green base. Each land
// pass re-checks the tip of a base that stopped streams (cmd/nova-sprint, landbase.go), and a
// green tip is the fact MergeReq.BaseGreen: every stream stopped on that base is marked
// (FieldBaseGatePassed), and the tick's base-gate rule resumes each of them (rules.go,
// TickRuleResume), the judgment answered by rule in the log.

// FieldBaseGatePassed is the base commit the lander found green again, on the control card of
// a stream stopped on its base's red (cause base); the base-gate rule resumes the stream.
const FieldBaseGatePassed = "base_gate_passed"

// failingTestRE is a failing test as go test prints it.
var failingTestRE = regexp.MustCompile(`--- FAIL: (Test[^\s(|]*)`)

// FailingTests are the tests a tree gate's finding names as failing, once each, in order.
func FailingTests(why string) []string {
	var out []string
	for _, m := range failingTestRE.FindAllStringSubmatch(why, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// baseRedHolder is the stream, other than except, stopped on the red of the base and holding
// its one judgment open, "" when none is: the base is already known red.
func baseRedHolder(s *Snapshot, base, except string) string {
	if base == "" {
		return ""
	}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || o.Note.Type != NBaseRed || o.Note.Stream == except {
			continue
		}
		ctl := s.StreamCtl(o.Note.Stream)
		if ctl != nil && ctl.F("state") == StreamStopped && ctl.F("cause") == "base" && ctl.F(FieldBaseGateBase) == base && ctl.F(FieldBaseGatePassed) == "" {
			return o.Note.Stream
		}
	}
	return ""
}

// baseRedRefused counts a refusal on a base whose red already stopped another stream (holder)
// and never stops this one: the holder's judgment stands for every landing on the base, so
// no second judgment is raised, and the stream lands at its first pass after the base is
// green again.
func baseRedRefused(p Plan, s *Snapshot, ctl *Card, r MergeReq, holder string) Plan {
	n, first := 1, stamp(s.Now)
	if m := ctl.Int(FieldBaseGateRefused); m > 0 && ctl.F(FieldBaseGateBase) == r.Base {
		n = m + 1
		if f := ctl.F(FieldBaseGateFirst); f != "" {
			first = f
		}
	}
	set := map[string]string{FieldBaseGateRefused: itoa(n), FieldBaseGateBase: r.Base, FieldBaseGateFirst: first}
	p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, set))},
		Moved: fmt.Sprintf("stream %s: the base %s refused at its tree gate (%d); landing on it is stopped under the judgment on stream %s", r.Stream, r.Base, n, holder)})
	return p
}

// baseRedSaid is the one judgment's text: the base, the failing tests and the finding.
func baseRedSaid(base, what, why string) string {
	if tests := FailingTests(why); len(tests) > 0 {
		what = "failing " + strings.Join(tests, ", ") + "; " + what
	}
	return what + "; every landing on " + orDash(base) + " is refused under this judgment, and its stream resumes by rule when a land pass finds its tip green"
}

// BaseGreenStreams are the streams stopped on a base's red that the lander found green again,
// in stream order: the base-gate rule's to resume.
func BaseGreenStreams(s *Snapshot) []string {
	var out []string
	for _, st := range s.Streams() {
		ctl := s.StreamCtl(st)
		if ctl != nil && ctl.F("state") == StreamStopped && ctl.F("cause") == "base" && ctl.F(FieldBaseGatePassed) != "" {
			out = append(out, st)
		}
	}
	return out
}

// BaseRedStreams are the streams stopped on a base's red and not yet found green: the ones a
// land pass re-checks the base of.
func BaseRedStreams(s *Snapshot) []string {
	var out []string
	for _, st := range s.Streams() {
		ctl := s.StreamCtl(st)
		if ctl != nil && ctl.F("state") == StreamStopped && ctl.F("cause") == "base" && ctl.F(FieldBaseGatePassed) == "" {
			out = append(out, st)
		}
	}
	return out
}

// baseGreenStep is the fact MergeReq.BaseGreen: the base r.Base passed its tree gate at the
// commit r.BaseGreen. Every stream stopped on that base's red is marked with the commit, and
// the tick's base-gate rule resumes it; a stream stopped with no base named (a hand
// merge --base-red) is marked when it is r.Stream. No card moves.
func baseGreenStep(p Plan, s *Snapshot, r MergeReq) Plan {
	if r.Base == "" {
		p.refuse(r.Stream, "a green base wants its branch (Base)")
		return p
	}
	for _, st := range s.Streams() {
		ctl := s.StreamCtl(st)
		if ctl == nil || ctl.F("state") != StreamStopped || ctl.F("cause") != "base" || ctl.F(FieldBaseGatePassed) != "" {
			continue
		}
		if b := ctl.F(FieldBaseGateBase); b != r.Base && (b != "" || st != r.Stream) {
			continue
		}
		set := map[string]string{FieldBaseGatePassed: r.BaseGreen, FieldBaseGateBase: r.Base}
		p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: st, Changes: []Change{change(Merge, setEntry(ctl, set))},
			Moved: fmt.Sprintf("stream %s: the base %s passes its tree gate again at %s", st, r.Base, r.BaseGreen)})
	}
	if len(p.Units) == 0 {
		p.refuse(r.Stream, "no stream is stopped on the red of the base "+r.Base+"; nothing was changed")
	}
	return p
}
