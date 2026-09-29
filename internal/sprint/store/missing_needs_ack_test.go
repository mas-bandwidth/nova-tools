package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
				if len(notes) != 2 {
					t.Fatalf("need two distinct judgments: %+v", notes)
				}
				ids := []string{notes[0].Note.ID, notes[1].Note.ID}
				h.must(AckStep(sprint.AckReq{Notes: ids, Reason: "both unnecessary", Who: "coordinator"}))
				c := h.snap().Work.Card("waiter")
				for _, n := range wants {
					if !strings.Contains(","+c.F("waived")+",", ","+n+",") {
						t.Fatalf("lost waiver %s: %+v", n, c)
					}
				}
				if sentinel {
					if c.Col != sprint.Waiting || c.F("reached") == "" || len(h.nOpenOf(sprint.NSentinelReached, "waiter")) != 1 {
						t.Fatalf("sentinel: %+v", c)
					}
				} else if c.Col != sprint.Ready {
					t.Fatalf("primary: %+v", c)
				}
				if len(h.nOpenOf(sprint.NMissingNeed, "waiter"))+len(h.nOpenOf(sprint.NBlocked, "waiter")) != 0 {
					t.Fatal("dependency judgment remains")
				}
				h.clean("combined waiver")
			})
		}
	}
}
