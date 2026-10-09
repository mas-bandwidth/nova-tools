package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// Two gone needs of one card detach in the one resolve, one line for the card.
func TestTwoGoneNeedsDetachInOneResolve(t *testing.T) {
	t.Parallel()
	for _, kinds := range []string{"missing", "dropped", "mixed"} {
		for _, sentinel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/sentinel=%v", kinds, sentinel), func(t *testing.T) {
				h := newHarness(t)
				h.setup(2)
				h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1", "s1-2"}, Sentinel: sentinel}))
				switch kinds {
				case "missing":
					seedMissingNeeds(h, "waiter", "first.bad,second.bad")
				case "dropped":
					seedDroppedNeed(h, "s1-1")
					seedDroppedNeed(h, "s1-2")
				case "mixed":
					seedDroppedNeed(h, "s1-2")
					seedMissingNeeds(h, "waiter", "first.bad,s1-2")
				}
				res := h.must(ResolveStep(sprint.ResolveReq{}))
				var moved []string
				for _, m := range res.Moved {
					if strings.Contains(m, "waiter") && !strings.Contains(m, "behind=") {
						moved = append(moved, m)
					}
				}
				require.Len(t, moved, 1, "moved: %v", res.Moved)
				c := h.snap().Work.Card("waiter")
				require.Empty(t, c.F("needs"), "needs left: %+v", c)
				if sentinel {
					require.Equal(t, sprint.Waiting, c.Col, "sentinel: %+v", c)
					require.NotEmpty(t, c.F("reached"), "sentinel: %+v", c)
					require.Len(t, h.nOpenOf(sprint.NSentinelReached, "waiter"), 1, "sentinel: %+v", c)
				} else {
					require.Equal(t, sprint.Ready, c.Col, "primary: %+v", c)
				}
				require.Len(t, detachedStories(h, "waiter"), 2, "story: %+v", detachedStories(h, "waiter"))
				require.Empty(t, h.nOpenOf(sprint.NMissingNeed, "waiter"))
				require.Empty(t, h.nOpenOf(sprint.NBlocked, "waiter"))
				h.clean("two gone needs detached")
			})
		}
	}
}
