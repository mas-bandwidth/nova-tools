package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// ask --instead (the owner, 2026-10-01: "get the verbs in man."): one read
// taken off one reader and asked of another reader instead, in one step.

// askedWithRoute is a world with s1-1 in review asked of two readers, a flash
// route enabled for its reads; it returns the readers holding its reads and
// the one reader free at the attempt.
func askedWithRoute(t *testing.T) (w *world, held []string, free string) {
	t.Helper()
	w = setup(t, 2)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.s.Routes = []Route{{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "m", Enabled: true}}
	for _, rc := range readsAt(w.s, w.s.Work.Card("s1-1"), 1) {
		held = append(held, rc.Row)
	}
	require.Len(t, held, 2)
	for _, rd := range w.s.Readers.Rows() {
		if !contains(held, rd) {
			free = rd
		}
	}
	return w, held, free
}

// requireNothingPlanned is a refused plan that changes nothing.
func requireNothingPlanned(t *testing.T, p Plan, why string) {
	t.Helper()
	require.Len(t, p.Refused, 1, "%+v", p)
	assert.Contains(t, p.Refused[0].Why, why)
	assert.Empty(t, p.Units)
	assert.Empty(t, p.Props)
	assert.Empty(t, p.Notes)
	assert.Empty(t, p.Closes)
}

func TestAskInsteadRetiresTheReadAndAsksAnotherReaderWithARouteInOneStep(t *testing.T) {
	t.Parallel()
	w, held, free := askedWithRoute(t)
	old := ReadCardID("s1-1", 1, held[0])
	p := w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[0]}))
	require.Len(t, p.Units, 1)
	assert.Equal(t, "s1-1 asked of "+free+"; its read taken back from "+held[0]+" (instead)", p.Units[0].Moved)

	gone := w.s.Readers.Card(old)
	require.NotNil(t, gone)
	assert.False(t, gone.Placed())
	assert.Equal(t, RetiredByCoordinator, gone.F("retired_by"))
	assert.NotEmpty(t, gone.F("retired"))

	added := w.s.Readers.Placed(ReadCardID("s1-1", 1, free))
	require.NotNil(t, added)
	assert.Equal(t, Asked, added.Col)
	assert.Equal(t, "flash-a", added.F(FieldRoute))
	assert.Equal(t, "p/m", added.F(FieldModel))
	w.clean("instead")
}

func TestAskInsteadLeavesTwoDifferentReadersHoldingTheAttemptsReads(t *testing.T) {
	t.Parallel()
	w, held, free := askedWithRoute(t)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[1]}))
	var readers []string
	for _, rc := range liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1) {
		readers = append(readers, rc.Row)
	}
	assert.ElementsMatch(t, []string{held[0], free}, readers)
	readOK(w, "s1-1")
	assert.Equal(t, NReadyToAccept, openTypes(w, "s1-1"))
}

func TestAskInsteadTakesBackARead(t *testing.T) {
	t.Parallel()
	w, held, free := askedWithRoute(t)
	old := ReadCardID("s1-1", 1, held[0])
	w.must(Read(w.s, ReadReq{As: held[0], Begin: true, Sel: Sel{IDs: []string{old}}}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[0]}))
	assert.False(t, w.s.Readers.Card(old).Placed())
	assert.NotNil(t, w.s.Readers.Placed(ReadCardID("s1-1", 1, free)))
}

func TestAskInsteadRetiredReadersLateReportIsRefused(t *testing.T) {
	t.Parallel()
	w, held, _ := askedWithRoute(t)
	old := ReadCardID("s1-1", 1, held[0])
	w.must(Read(w.s, ReadReq{As: held[0], Begin: true, Sel: Sel{IDs: []string{old}}}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[0]}))
	p := Read(w.s, ReadReq{As: held[0], Verdict: "ok", Sel: Sel{IDs: []string{old}}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "the coordinator took the read back and asked another reader instead")
	assert.Empty(t, p.Units)
}

func TestAskInsteadRefusesAPrimaryNotInReview(t *testing.T) {
	t.Parallel()
	w, held, _ := askedWithRoute(t)
	requireNothingPlanned(t, Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}, Instead: held[0]}), "not review (it is ready)")
}

func TestAskInsteadRefusesAReaderWithNoReadOfTheAttempt(t *testing.T) {
	t.Parallel()
	w, _, free := askedWithRoute(t)
	requireNothingPlanned(t, Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: free}), free+" holds no read of s1-1 at attempt 1")
}

func TestAskInsteadRefusesAFinishedRead(t *testing.T) {
	t.Parallel()
	w, held, _ := askedWithRoute(t)
	old := ReadCardID("s1-1", 1, held[0])
	w.must(Read(w.s, ReadReq{As: held[0], Verdict: "ok", Sel: Sel{IDs: []string{old}}}))
	requireNothingPlanned(t, Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[0]}), "is finished (ok)")
}

func TestAskInsteadRefusesARetiredRead(t *testing.T) {
	t.Parallel()
	w, held, _ := askedWithRoute(t)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[0]}))
	requireNothingPlanned(t, Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[0]}), "was retired at")
}

func TestAskInsteadRefusesWhenNoOtherReaderIsFree(t *testing.T) {
	t.Parallel()
	w, held, _ := askedWithRoute(t)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Another: true}))
	requireNothingPlanned(t, Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Instead: held[0]}), "needs 1 different readers and 0 is free")
}

func TestAskInsteadRefusesWithAnotherOrMoreThanOnePrimary(t *testing.T) {
	t.Parallel()
	w, held, _ := askedWithRoute(t)
	for name, r := range map[string]AskReq{
		"another":  {Sel: Sel{IDs: []string{"s1-1"}}, Another: true, Instead: held[0]},
		"two":      {Sel: Sel{IDs: []string{"s1-1", "s1-2"}}, Instead: held[0]},
		"none":     {Instead: held[0]},
		"by limit": {Sel: Sel{Limit: 1}, Instead: held[0]},
	} {
		t.Run(name, func(t *testing.T) {
			requireNothingPlanned(t, Ask(w.s, r), "--instead takes back one read of one primary")
		})
	}
}
