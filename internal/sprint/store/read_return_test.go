package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// asked1 is a sprint of n primaries in s1, each asked of two of the three
// readers by the machine's tick.
func (h *harness) asked1(n int) {
	h.t.Helper()
	h.setup(n)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
}

// returnNotes is every note of a read returned.
func (h *harness) returnNotes() []sprint.Note {
	h.t.Helper()
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, x := range notes {
		if x.Type == sprint.NReadReturned {
			out = append(out, x)
		}
	}
	return out
}

// A read returned by the reader that holds it (its launch was refused, so it
// has no verdict) is retired, and the same tick asks it of another reader up
// at the same attempt; the reader holds nothing, one happened note names the
// reader, the card and the reason, and no broken read is counted on the
// primary (docs/SPEC-SPRINT.md section 6; tla/DirtyTick.tla, ReadReturn).
func TestAReadReturnedIsAskedOfAnotherReaderAtTheSameAttempt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	rcs := h.snap().Readers.Of("s1-1")
	require.Len(t, rcs, 2)
	rc := rcs[0]
	from := rc.Row
	h.must(ReadStep(sprint.ReadReq{As: from, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: from}))
	h.must(ReadStep(sprint.ReadReq{As: from, Return: true, Reason: "STAGE FAIL no bench mirror", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: from}))
	s := h.snap()
	assert.Nil(t, s.Readers.Placed(rc.ID), "the returned read is retired")
	assert.Zero(t, s.Readers.Count(from, sprint.Reading)+s.Readers.Count(from, sprint.Asked), "the reader holds nothing")
	notes := h.returnNotes()
	require.Len(t, notes, 1)
	assert.Equal(t, sprint.Happened, notes[0].Kind)
	for _, want := range []string{from, rc.ID, "STAGE FAIL no bench mirror"} {
		assert.Contains(t, notes[0].What, want)
	}
	h.machine()
	s = h.snap()
	var readers []string
	for _, c := range s.Readers.Of("s1-1") {
		assert.Equal(t, "1", c.F("attempt"), "the same attempt")
		readers = append(readers, c.Row)
	}
	assert.Len(t, readers, 2, "asked again in the one tick")
	assert.NotContains(t, readers, from, "of another reader")
	pr := s.Work.Card("s1-1")
	assert.Equal(t, sprint.Review, pr.Col)
	assert.Equal(t, 1, pr.Int("attempt"))
	assert.Zero(t, pr.Int("broken_reads"), "a return is no finding against the work")
	assert.Zero(t, h.written(sprint.NReadBroken))
	h.clean("returned and asked again")
	// a report against the returned card is refused, naming the return
	res := h.run(ReadStep(sprint.ReadReq{As: from, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: from}))
	require.NotEmpty(t, res.Refused)
	assert.Contains(t, strings.Join(refusedText(res), " "), "returned")
}

// A return of a read the caller does not hold is refused, and nothing moves.
func TestAReturnOfAReadNotHeldIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	rc := h.snap().Readers.Of("s1-1")[0]
	other := "reader-a"
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		if r != rc.Row && h.snap().Readers.Card(sprint.ReadCardID("s1-1", 1, r)) == nil {
			other = r
		}
	}
	require.NotEqual(t, rc.Row, other)
	res := h.run(ReadStep(sprint.ReadReq{As: other, Return: true, Reason: "not mine", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: other}))
	require.NotEmpty(t, res.Refused)
	assert.NotNil(t, h.snap().Readers.Placed(rc.ID), "the read stays with its reader")
	assert.Empty(t, h.returnNotes())
}

// A reader that returns three reads in a row holds nothing after the third.
func TestAReaderReturningThreeReadsHoldsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(6)
	s := h.snap()
	var mine []string
	for _, c := range s.Readers.Cell("reader-a", sprint.Asked) {
		mine = append(mine, c.ID)
	}
	require.GreaterOrEqual(t, len(mine), 3)
	mine = mine[:3]
	h.must(ReadStep(sprint.ReadReq{As: "reader-a", Begin: true, Sel: sprint.Sel{IDs: mine}, Who: "reader-a"}))
	require.Equal(t, 3, h.snap().Readers.Count("reader-a", sprint.Reading))
	for _, id := range mine {
		h.must(ReadStep(sprint.ReadReq{As: "reader-a", Return: true, Reason: "STAGE FAIL", Sel: sprint.Sel{IDs: []string{id}}, Who: "reader-a"}))
	}
	assert.Zero(t, h.snap().Readers.Count("reader-a", sprint.Reading), "reading after three returns")
	assert.Len(t, h.returnNotes(), 3)
	h.machine()
	assert.Zero(t, h.snap().Readers.Count("reader-a", sprint.Reading))
	h.clean("three returned")
}

// refusedText is each refusal's reason.
func refusedText(res Result) []string {
	var out []string
	for _, r := range res.Refused {
		out = append(out, r.Why)
	}
	return out
}
