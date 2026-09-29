package store

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The property test's findings, each its shortest sequence as a test.

// commandsOf is the printed commands of the open judgment group of a type.
func (h *harness) commandsOf(typ string) []sprint.Command {
	h.t.Helper()
	v, err := h.st.Inbox(h.ctx, sprint.DeadlineJudgment, 0, 1000)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, g := range v.Groups {
		if g.Type == typ {
			return g.Commands
		}
	}
	h.t.Fatalf("no %s group in the inbox", typ)
	return nil
}

// Seed 3: a cross stop whose needed card's id sorts before the stuck card's.
// The printed return, drop and rank named the needed card as the stuck one
// (the note's primaries are sorted); dropping it left the stuck card needing
// a dropped card, resume refused for ever, and the sprint never finished.
func TestACrossStopsCommandsNameTheStuckCardAndTheCardItNeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"p8"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p10"}, Needs: []string{"p8"}}))
	h.through("p8")
	h.must(MergeStep(sprint.MergeReq{Stream: "s2", Cross: "p8=p10"}))
	got := map[string]string{}
	for _, c := range h.commandsOf(sprint.NCross) {
		got[c.Decision] = strings.Join(c.Lines, " && ")
	}
	for d, want := range map[string]string{
		"rank that card first": "nova-sprint rank p10 --first",
		"return":               "nova-sprint return p8 --reason",
		"drop":                 "nova-sprint drop p8 --reason",
		"look at both":         "nova-sprint card p8 && nova-sprint card p10",
	} {
		if !strings.HasPrefix(got[d], want) {
			t.Errorf("%s: %q, want it to start %q", d, got[d], want)
		}
	}
	// The printed return, then resume: the stream moves again.
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"p8"}}, Reason: "r"}))
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s2", Did: "returned p8"}))
	h.clean("resumed")
}
