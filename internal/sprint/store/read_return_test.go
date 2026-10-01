package store

import (
	"fmt"
	"slices"
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
// has no verdict) goes back to asked on its row, stamped returned, and the
// next tick asks it of another reader free at the same attempt, retiring the
// returned card; one happened note names the reader, the card and the
// reason, and no broken read is counted on the primary (docs/SPEC-SPRINT.md
// section 6; tla/DirtyTick.tla, ReadReturn, JudgedOnlyAfterTheBound).
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
	back := s.Readers.Placed(rc.ID)
	require.NotNil(t, back, "a return is not a read: the card stays on its row")
	assert.Equal(t, sprint.Asked, back.Col)
	assert.NotEmpty(t, back.F(sprint.FieldReturned), "stamped returned")
	assert.Empty(t, back.F("begun"), "no longer begun")
	assert.Zero(t, s.Readers.Count(from, sprint.Reading), "the reader is reading nothing")
	notes := h.returnNotes()
	require.Len(t, notes, 1)
	assert.Equal(t, sprint.Happened, notes[0].Kind)
	for _, want := range []string{from, rc.ID, "STAGE FAIL no bench mirror"} {
		assert.Contains(t, notes[0].What, want)
	}
	h.machine()
	s = h.snap()
	assert.Nil(t, s.Readers.Placed(rc.ID), "a free reader took the returned read: its card retired")
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

// A read returned and asked of a reader that then goes away, with no reader
// left free at the attempt: the tick takes the away read back and raises
// "cannot ask" for the primary, never a step that fails every tick with no
// note (tickExtras reads every retired read card of a primary in review,
// whatever is placed beside it).
func TestAReturnedReadWhoseNextReaderGoesAwayIsAskedOrJudgedNeverSilent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-d"}))
	h.beat()
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "coordinator"))
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-d", true, "coordinator"))
	h.asked1(1)
	readersOf := func() []string {
		var out []string
		for _, c := range h.snap().Readers.Of("s1-1") {
			out = append(out, c.Row)
		}
		return out
	}
	require.ElementsMatch(t, []string{"reader-a", "reader-b"}, readersOf())
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", false, "coordinator"))
	h.must(ReadStep(sprint.ReadReq{As: "reader-a", Return: true, Reason: "STAGE FAIL", Sel: sprint.Sel{IDs: []string{"s1-1.r1.reader-a"}}, Who: "reader-a"}))
	h.machine()
	require.ElementsMatch(t, []string{"reader-b", "reader-c"}, readersOf())
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "coordinator"))
	for i := 0; i < 2; i++ {
		res, err := h.st.Tick(h.ctx)
		require.NoError(t, err, "tick %d", i+1)
		for _, p := range res.Parts {
			assert.NotContains(t, strings.Join(p.Moved, " "), "kept changing", "tick %d %s", i+1, p.Name)
		}
	}
	asked := readersOf()
	assert.NotContains(t, asked, "reader-a", "a reader whose returned read another reader took is not asked again at the attempt")
	// asked again of two readers up, or the primary's "cannot ask" judgment
	// is open: a read left on reader-c, away, with nothing said is the silence
	if slices.Contains(asked, "reader-c") || len(asked) < 2 {
		assert.NotEmpty(t, h.openOf(sprint.NCannotAsk), "a read that cannot be asked again is a judgment: %v", asked)
	}
}

// Only a read still asked or reading can be returned: a read reported ok, or
// broken, is refused, and its verdict stands.
func TestAReturnOfAReadAlreadyReportedIsRefused(t *testing.T) {
	t.Parallel()
	for _, verdict := range []string{"ok", "broken"} {
		t.Run(verdict, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.asked1(1)
			rc := h.snap().Readers.Of("s1-1")[0]
			h.must(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: verdict, Finding: "f", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.Row}))
			res := h.run(ReadStep(sprint.ReadReq{As: rc.Row, Return: true, Reason: "late", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.Row}))
			require.NotEmpty(t, res.Refused)
			c := h.snap().Readers.Placed(rc.ID)
			require.NotNil(t, c, "the reported read stays")
			assert.Equal(t, verdict, c.Col)
			assert.Empty(t, h.returnNotes())
		})
	}
}

// The stranding of 2026-10-01: every reader's launch failed and each returned
// every read with no verdict. A return is not a read, so the readers stay
// eligible: the reads go to the reader still free, then back to the readers
// that returned them, in place, and no "cannot ask" judgment is raised; the
// primary stays at its attempt with nothing spent, and once the readers can
// launch their oks accept it (tla/DirtyTick.tla, JudgedOnlyAfterTheBound;
// before, the returned cards were retired and counted as reads, and the
// primary was stranded: needs 1 different readers and 0 is free).
func TestReadsEveryReaderReturnsAreNeverStranded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	readersOf := func() map[string]string {
		out := map[string]string{}
		for _, c := range h.snap().Readers.Of("s1-1") {
			out[c.Row] = c.Col
		}
		return out
	}
	pr0 := h.snap().Work.Card("s1-1")
	counters := []string{"attempt", "failed", "reworks", "broken_reads", "stuck", "returns", "redeals"}
	ret := func(reader string) {
		h.t.Helper()
		id := sprint.ReadCardID("s1-1", 1, reader)
		h.must(ReadStep(sprint.ReadReq{As: reader, Begin: true, Sel: sprint.Sel{IDs: []string{id}}, Who: reader}))
		h.must(ReadStep(sprint.ReadReq{As: reader, Return: true, Reason: `no verdict (ran=false verdict=""): NATIVE REFUSED`, Sel: sprint.Sel{IDs: []string{id}}, Who: reader}))
		h.machine()
	}
	first := readersOf()
	require.Len(t, first, 2)
	var pair []string
	for r := range first {
		pair = append(pair, r)
	}
	slices.Sort(pair)
	// the first return goes to the third reader, free at the attempt
	ret(pair[0])
	got := readersOf()
	require.Len(t, got, 2)
	assert.NotContains(t, got, pair[0])
	var third string
	for r := range got {
		if r != pair[1] {
			third = r
		}
	}
	require.NotEmpty(t, third)
	// the next two have no free reader: asked again of the readers that
	// returned them, in place
	ret(pair[1])
	ret(third)
	s := h.snap()
	for _, r := range []string{pair[1], third} {
		c := s.Readers.Placed(sprint.ReadCardID("s1-1", 1, r))
		require.NotNil(t, c, "%s is asked again", r)
		assert.Equal(t, sprint.Asked, c.Col, r)
		assert.Empty(t, c.F(sprint.FieldReturned), "%s: asked again, the stamp cleared", r)
	}
	assert.Empty(t, h.openOf(sprint.NCannotAsk), "never stranded in review")
	pr := s.Work.Card("s1-1")
	assert.Equal(t, sprint.Review, pr.Col)
	for _, k := range counters {
		assert.Equal(t, pr0.F(k), pr.F(k), "a return spends no bound of the primary: %s", k)
	}
	assert.Len(t, h.returnNotes(), 3)
	h.clean("every reader returned")
	// the fault fixed: both readers read it, and the tick accepts
	for _, r := range []string{pair[1], third} {
		id := sprint.ReadCardID("s1-1", 1, r)
		h.must(ReadStep(sprint.ReadReq{As: r, Verdict: "ok", Finding: "clean", Sel: sprint.Sel{IDs: []string{id}}, Who: r}))
	}
	h.machine()
	assert.Equal(t, sprint.Merging, h.snap().Work.Card("s1-1").Col)
	h.clean("read and accepted")
}

// The re-ask is bounded (tla/DirtyTick.tla, ReasksBounded, StrandingIsJudged,
// JudgedOnlyAfterTheBound): a reader that can never launch, with no other reader
// free, is asked its returned read again in place MaxReadReasks times; the next
// return is counted as a read, the card retired, and the tick raises "cannot ask"
// for the coordinator, instead of the read coming back every few seconds for ever
// with no judgment (the reader's finding on #5019).
func TestAReadReturnedPastItsReasksIsJudgedNotAskedAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "coordinator"))
	h.asked1(1)
	id := sprint.ReadCardID("s1-1", 1, "reader-a")
	require.NotNil(t, h.snap().Readers.Placed(id), "asked of reader-a and reader-b")
	runs := 0
	ret := func() {
		h.t.Helper()
		runs++
		h.must(ReadStep(sprint.ReadReq{As: "reader-a", Begin: true, Sel: sprint.Sel{IDs: []string{id}}, Who: "reader-a"}))
		h.must(ReadStep(sprint.ReadReq{As: "reader-a", Return: true, Reason: `no verdict (ran=false verdict="")`, Usage: fmt.Sprintf("input=%d", 10*runs),
			Sel: sprint.Sel{IDs: []string{id}}, Who: "reader-a"}))
		h.machine()
	}
	for i := 1; i <= sprint.MaxReadReasks; i++ {
		ret()
		c := h.snap().Readers.Placed(id)
		require.NotNil(t, c, "re-ask %d: still reader-a's", i)
		assert.Equal(t, sprint.Asked, c.Col)
		assert.Equal(t, i, c.Int(sprint.FieldReasked), "re-ask %d is counted", i)
		assert.Empty(t, h.openOf(sprint.NCannotAsk), "no judgment while the re-asks last")
	}
	ret()
	assert.Nil(t, h.snap().Readers.Placed(id), "the return past the bound is counted: the card is retired")
	open := h.openOf(sprint.NCannotAsk)
	require.Len(t, open, 1, "the coordinator is told: cannot ask")
	assert.Contains(t, open[0].Note.What, "needs 1 different readers and 0 is free")
	assert.Len(t, h.returnNotes(), sprint.MaxReadReasks+1)
	h.machine()
	assert.Nil(t, h.snap().Readers.Placed(id), "never asked of reader-a again at the attempt")
	assert.Len(t, h.openOf(sprint.NCannotAsk), 1, "one judgment, not one a tick")
	// every returned run keeps its own cost record on the one card (cost.go,
	// FieldReadTake): two re-asks, three returned runs, three records, all in the
	// producer's total; the bound keeps the card far under MaxTakes
	_, _, _, f, ok := h.m.Record("t-readers", id)
	require.True(t, ok)
	for n := 1; n <= sprint.MaxReadReasks+1; n++ {
		assert.NotEmpty(t, f[sprint.FieldReadTake+fmt.Sprint(n)], "run %d's record is kept", n)
	}
	assert.Empty(t, f[sprint.FieldReadTake+fmt.Sprint(sprint.MaxReadReasks+2)])
	// and the producer card carries every one of them (cost.go: the cost is in the card)
	var runsOf []sprint.Consumer
	for _, c := range sprint.CardCostOf(h.snap().Work.Card("s1-1")).Consumers {
		if c.Card == id {
			runsOf = append(runsOf, c)
		}
	}
	require.Len(t, runsOf, sprint.MaxReadReasks+1)
	var input int64
	for _, c := range runsOf {
		assert.Equal(t, "returned", c.End)
		input += c.Usage.Tokens.Input
	}
	assert.Equal(t, int64(10+20+30), input, "every returned run counts in the producer's total")
	h.clean("judged cannot ask")
}

// A read its reader returned and was asked again in place, whose reader then
// goes away with no other reader up to take it, is a judgment at once, never a
// silent wait (tla/DirtyTick.tla, StrandingIsJudged and the lapse control
// MCDirtyTickHandBackLapse): the tick says fewer than two readers are up; the
// reader back up holds its read again and the judgment closes.
func TestAReturnedReadWhoseReaderGoesAwayIsJudgedAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-c", true, "coordinator"))
	h.asked1(1)
	id := sprint.ReadCardID("s1-1", 1, "reader-a")
	h.must(ReadStep(sprint.ReadReq{As: "reader-a", Begin: true, Sel: sprint.Sel{IDs: []string{id}}, Who: "reader-a"}))
	h.must(ReadStep(sprint.ReadReq{As: "reader-a", Return: true, Reason: "NATIVE REFUSED", Sel: sprint.Sel{IDs: []string{id}}, Who: "reader-a"}))
	h.machine()
	require.Equal(t, 1, h.snap().Readers.Placed(id).Int(sprint.FieldReasked), "asked again of reader-a in place")
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-a", true, "coordinator"))
	h.machine()
	assert.NotEmpty(t, h.openOf(sprint.NFewReaders), "no reader up to take it: the coordinator is told at once")
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-a", false, "coordinator"))
	h.machine()
	c := h.snap().Readers.Placed(id)
	require.NotNil(t, c)
	assert.Equal(t, sprint.Asked, c.Col, "reader-a back up holds its read")
	assert.Empty(t, h.openOf(sprint.NFewReaders), "and the judgment closes")
	h.clean("judged while away, read again when back")
}
