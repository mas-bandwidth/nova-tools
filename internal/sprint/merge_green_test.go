package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// When the first card of a green batch is refused (queued in merge but not
// merging in work, which only a broken rule 4 allows), the stream's control
// change and the started-merging note ride on the first card that lands, and
// the batch note lists only the cards that landed.
func TestMergeGreenKeepsTheStreamChangeWhenTheFirstCardIsRefused(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	wasWaiting := w.s.StreamCtl("s1").F("state") == StreamWaiting
	w.s.Work.Card("s1-1").Col = Review
	w.s.Work.cells, w.s.Work.byPrimary = nil, nil

	p := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10})
	require.Len(t, p.Refused, 1, "plan: units %d refused %v", len(p.Units), p.Refused)
	require.Equal(t, "s1-1", p.Refused[0].Key, "plan: units %d refused %v", len(p.Units), p.Refused)
	require.Len(t, p.Units, 1, "plan: units %d refused %v", len(p.Units), p.Refused)
	require.Equal(t, "s1-2", p.Units[0].Key, "plan: units %d refused %v", len(p.Units), p.Refused)
	u := p.Units[0]
	var ctl bool
	for _, c := range u.Changes {
		if c.Table == Merge && c.Entry.ID == CtlID("s1") {
			ctl = true
		}
	}
	require.True(t, ctl, "the stream's control change is lost: %+v", u.Changes)
	var started, batch int
	for _, n := range u.Notes {
		switch n.Type {
		case NStartedMerging:
			started++
		case NBatchLanded:
			batch++
			require.Equal(t, []string{"s1-2"}, n.Primaries, "the batch note lists %v, want only what landed", n.Primaries)
		case NStreamLanded:
			t.Fatalf("the stream is landed with s1-1 still open")
		}
	}
	require.Equal(t, 1, batch, "notes: started %d batch %d (was waiting %v)", started, batch, wasWaiting)
	if wasWaiting {
		require.Equal(t, 1, started, "notes: started %d batch %d (was waiting %v)", started, batch, wasWaiting)
	}
}
