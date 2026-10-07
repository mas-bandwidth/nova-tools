package store

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// Inbox recovery can acknowledge several judgments about the same primary
// at once. Their waivers must form one guarded card change, not duplicate
// member entries or a last-write-wins waiver.
func TestAckCombinesDependencyJudgmentsForOnePrimary(t *testing.T) {
	t.Parallel()
	for _, kinds := range []string{"missing", "dropped", "mixed"} {
		for _, sentinel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/sentinel=%v", kinds, sentinel), func(t *testing.T) {
				h := newHarness(t)
				h.setup(2)
				h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1", "s1-2"}, Sentinel: sentinel}))
				var wants []string
				switch kinds {
				case "missing":
					seedMissingNeeds(h, "waiter", "first.bad")
					h.must(ResolveStep(sprint.ResolveReq{}))
					seedMissingNeeds(h, "waiter", "first.bad,second.bad")
					h.must(ResolveStep(sprint.ResolveReq{}))
					wants = []string{"first.bad", "second.bad"}
				case "dropped":
					h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "first obsolete"}))
					h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Reason: "second obsolete"}))
					wants = []string{"s1-1", "s1-2"}
				case "mixed":
					seedMissingNeeds(h, "waiter", "first.bad,s1-2")
					h.must(ResolveStep(sprint.ResolveReq{}))
					h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
					wants = []string{"first.bad", "s1-2"}
				}
				notes := append(h.nOpenOf(sprint.NMissingNeed, "waiter"), h.nOpenOf(sprint.NBlocked, "waiter")...)
				require.Len(t, notes, 2, "need two distinct judgments: %+v", notes)
				ids := []string{notes[0].Note.ID, notes[1].Note.ID}
				h.must(AckStep(sprint.AckReq{Notes: ids, Reason: "both unnecessary", Who: "tester"}))
				c := h.snap().Work.Card("waiter")
				for _, n := range wants {
					require.Contains(t, ","+c.F("waived")+",", ","+n+",", "lost waiver %s: %+v", n, c)
				}
				if sentinel {
					require.Equal(t, sprint.Waiting, c.Col, "sentinel: %+v", c)
					require.NotEmpty(t, c.F("reached"), "sentinel: %+v", c)
					require.Len(t, h.nOpenOf(sprint.NSentinelReached, "waiter"), 1, "sentinel: %+v", c)
				} else {
					require.Equal(t, sprint.Ready, c.Col, "primary: %+v", c)
				}
				require.Equal(t, 0, len(h.nOpenOf(sprint.NMissingNeed, "waiter"))+len(h.nOpenOf(sprint.NBlocked, "waiter")), "dependency judgment remains")
				h.clean("combined waiver")
			})
		}
	}
}
