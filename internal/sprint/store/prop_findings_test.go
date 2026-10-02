package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The property test's findings, each its shortest sequence as a test.

// commandsOf is the printed commands of the open judgment group of a type.
func (h *harness) commandsOf(typ string) []sprint.Command {
	h.t.Helper()
	v, err := h.st.Inbox(h.ctx, sprint.DeadlineJudgment, 0, 1000)
	require.NoError(h.t, err)
	for _, g := range v.Groups {
		if g.Type == typ {
			return g.Commands
		}
	}
	require.FailNow(h.t, fmt.Sprintf("no %s group in the inbox", typ))
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
		assert.True(t, strings.HasPrefix(got[d], want), "%s: %q, want it to start %q", d, got[d], want)
	}
	// The printed return, then resume: the stream moves again.
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"p8"}}, Reason: "r"}))
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s2", Did: "returned p8"}))
	h.clean("resumed")
}

// Seed 1360: a read late past its deadline is the one judgment open on a
// primary when its reader reports; the tick's deadlines part closes it, after
// its check part ran, and wrote nothing in its place: the primary sat in
// review with nothing open until a later tick's check. The tick now writes
// the judgment a primary in review needs when it closes that primary's last.
func TestTheTickClosingALateReadWritesWhatThePrimaryNeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p2"}}))
	h.startMachine()
	h.machine() // deals p2
	h.takeAndFinish(false, "p2")
	h.machine() // asks two readers
	var cards []*sprint.Card
	for _, rc := range h.snap().Readers.Of("p2") {
		cards = append(cards, rc)
	}
	require.Len(t, cards, 2, "asked: %d read cards", len(cards))
	h.must(ReadStep(sprint.ReadReq{As: cards[0].Row, Verdict: "broken", Finding: "f:1", Sel: sprint.Sel{IDs: []string{cards[0].ID}}}))
	h.tick(sprint.DeadlineUnbegun + time.Minute)
	h.machine() // the second read is late
	require.Len(t, h.openOf(sprint.NReadLate), 1, "no late read: %v", h.openOf(sprint.NReadLate))
	// ack does not answer the broken read (it is the coordinator's to rework,
	// ask another or drop): it stays open and names p2 whatever the tick closes
	var broken []string
	for _, o := range h.openOf(sprint.NReadBroken) {
		broken = append(broken, o.Note.ID)
	}
	res := h.run(AckStep(sprint.AckReq{Notes: broken, Reason: "none"}))
	require.NotEmpty(t, res.Refused, "ack of a broken read: %+v", res)
	h.must(ReadStep(sprint.ReadReq{As: cards[1].Row, Verdict: "ok", Finding: "f", Sel: sprint.Sel{IDs: []string{cards[1].ID}}}))
	h.tick(time.Second)
	h.machine() // closes the late read
	if got := h.judgmentsOn("p2"); len(got) == 0 || len(h.openOf(sprint.NReadLate)) != 0 {
		require.Failf(t, "", "after the late read closed, open on p2: %v, late reads %d", got, len(h.openOf(sprint.NReadLate)))
	}
	h.clean("the late read closed")
	h.quiet("the tick again")
}

// The path TestTheTickClosingALateReadWritesWhatThePrimaryNeeds reached
// before broken reads stopped taking an ack (reader finding 5): the late
// read is the one judgment open on a primary in review when its reader
// reports ok after the other read's broken judgment was closed (a store
// written before the rule, closed by a raw step here as the ack then did).
// Nothing is outstanding and one ok is not two: the tick closing the late
// read writes reads exhausted, the judgment the primary needs.
func TestTheTickClosingTheOnlyLateReadWritesWhatThePrimaryNeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p2"}}))
	h.startMachine()
	h.machine() // deals p2
	h.takeAndFinish(false, "p2")
	h.machine() // asks two readers
	cards := h.snap().Readers.Of("p2")
	require.Len(t, cards, 2, "asked: %d read cards", len(cards))
	h.must(ReadStep(sprint.ReadReq{As: cards[0].Row, Verdict: "broken", Finding: "f:1", Sel: sprint.Sel{IDs: []string{cards[0].ID}}}))
	broken := h.openOf(sprint.NReadBroken)
	h.must(Step{Verb: "persisted", Plan: func(*sprint.Snapshot) sprint.Plan { return sprint.Plan{Closes: broken} }})
	h.tick(sprint.DeadlineUnbegun + time.Minute)
	h.machine() // the second read is late
	got := h.judgmentsOn("p2")
	require.Len(t, got, 1, "the late read is not the one judgment open on p2: %v", got)
	require.Len(t, h.openOf(sprint.NReadLate), 1, "the late read is not the one judgment open on p2: %v", got)
	h.must(ReadStep(sprint.ReadReq{As: cards[1].Row, Verdict: "ok", Finding: "f", Sel: sprint.Sel{IDs: []string{cards[1].ID}}}))
	h.tick(time.Second)
	h.machine() // closes the late read
	if got := h.openOf(sprint.NReadsExhausted); len(got) != 1 || len(h.openOf(sprint.NReadLate)) != 0 {
		require.Failf(t, "", "after the late read closed, reads exhausted: %v; open on p2: %v", got, h.judgmentsOn("p2"))
	}
	h.clean("the late read closed")
	h.quiet("the tick again")
}
