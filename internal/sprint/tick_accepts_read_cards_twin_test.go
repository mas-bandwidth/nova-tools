package sprint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
	"github.com/nova-tools/internal/sprint/store"
)

// The read-card path of TestTheTickAcceptsAPrimaryWhoseReadsAreAllOk
// (internal/sprint/store/tick_accepts_test.go), on the twin with read cards on: a
// pro card's two read cards say ok (LAND), on a RUNNING machine or a STOPPED one;
// no ready to accept opens, the first tick after (after start, when STOPPED) moves
// it to merging with its readers named, the seat is told once (ready to merge), the
// tick's check finds no rule broken, and accept --read-ok then finds nothing waiting.
func TestTheTickAcceptsAPrimaryWhoseReadsAreAllOkOnReadCards(t *testing.T) {
	t.Parallel()
	for _, stopped := range []bool{false, true} {
		name := "running"
		if stopped {
			name = "stopped"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newReadCardsRig(t)
			r.toReview("s1-1", proBrief)
			reads := r.readCards("s1-1")
			require.Len(t, reads, 2)
			if stopped {
				_, _, _, err := r.st.SetMachine(r.ctx, false)
				require.NoError(t, err)
			}
			for _, c := range reads {
				r.read(c, sprint.ReadReq{Verdict: "ok", Finding: "clean", Usage: "input=10 output=1", Who: c.Row})
			}
			open := func(typ string) int {
				n := 0
				for _, o := range r.snap().Open {
					if o.Note.Type == typ {
						n++
					}
				}
				return n
			}
			require.Zero(t, open(sprint.NReadyToAccept), "the last ok read card opened ready to accept")
			if stopped {
				require.Equal(t, sprint.Review, r.snap().Work.Card("s1-1").Col, "a STOPPED machine moves nothing")
				_, _, _, err := r.st.SetMachine(r.ctx, true)
				require.NoError(t, err)
			}
			r.tick()
			pr := r.snap().Work.Card("s1-1")
			require.Equal(t, sprint.Merging, pr.Col, "the tick accepts it")
			require.Len(t, sprint.Split(pr.F("readers")), 2, "the readers it was accepted on")
			require.Zero(t, open(sprint.NInvariant), "the tick's check found the accept broke a rule")
			rep, _, err := r.st.Check(r.ctx, 3)
			require.NoError(t, err)
			require.Empty(t, rep.Violations, "check after the accept")
			notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
			require.NoError(t, err)
			told := 0
			for _, n := range notes {
				if n.Type == sprint.NReadyToMerge {
					told++
					require.Equal(t, []string{"s1-1"}, n.Primaries)
				}
			}
			require.Equal(t, 1, told, "the seat is told once")
			res := r.must(store.AcceptStep(sprint.AcceptReq{ReadOK: true, Who: "coordinator"}))
			require.Contains(t, res.Said, sprint.NothingWaits)
		})
	}
}
