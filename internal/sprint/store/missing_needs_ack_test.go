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
	for _, kinds := range []string{"missing"} {
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

// Dropped needs are no longer reachable via drop: drop refuses a needed card
// unless --cascade is used, which drops the dependant too.
func TestDropRefusesNeededCardNoWaiverPath(t *testing.T) {
	t.Parallel()
	for _, sentinel := range []bool{false, true} {
		t.Run(fmt.Sprintf("dropped/sentinel=%v", sentinel), func(t *testing.T) {
			h := newHarness(t)
			h.setup(2)
			h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1", "s1-2"}, Sentinel: sentinel}))
			// dropping s1-1 without cascade should be refused
			res := h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "first obsolete"}))
			require.Len(t, res.Refused, 1, "drop should be refused: %+v", res)
			require.Contains(t, res.Refused[0].Why, "s1-1 is needed by waiter", "refusal should name dependant")
			require.Contains(t, res.Refused[0].Why, "--cascade", "refusal should suggest cascade")
			// dropping with cascade drops waiter too
			h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "first obsolete", Cascade: true}))
			require.Equal(t, "", h.state("s1-1"), "s1-1 should be off the table")
			require.Equal(t, "", h.state("waiter"), "waiter should be off the table")
			h.clean("dropped with cascade")
		})
	}
}
