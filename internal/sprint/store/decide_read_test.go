package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The first read of a flash card is a decide read (docs/SPEC-SPRINT.md section 6): its read
// card carries the sprint row's two bars as nova-config applied them, and its packet hands
// them to the reader; any other read of the attempt, and every read of a pro card, carries
// none and is a strings read. With no bars in the sprint row no read is a decide read.
func TestTheFirstReadOfAFlashCardIsADecideRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		bounce, review string
		decide         int
	}{
		{"bars set", "0.5", "0.3", 1},
		{"no bars", "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"), route("flash-a", "flash"), route("flash-b", "flash"))
			h.m.SetDecideBars(tc.bounce, tc.review)
			require.NoError(t, h.st.BeatReaders(h.ctx))
			h.addReady("s1", 1, briefOf("pro", ""))
			h.addReady("s2", 1, briefOf("flash", ""))
			h.startMachine()
			h.machine()
			h.work("m1")
			h.work("m2")
			h.machine()
			s := h.snap()
			for id, want := range map[string]int{"s1-1": 0, "s2-1": tc.decide} {
				reads := s.Readers.Of(id)
				require.NotEmpty(t, reads, "%s is asked", id)
				decided := 0
				for _, rc := range reads {
					p := sprint.PacketOf("", 0, rc, s.Work.Card(id), nil, nil)
					if rc.F(sprint.FieldDecideBounce) == "" {
						assert.Empty(t, p.DecideBounce+p.DecideReview, "a strings read's packet names no bar")
						continue
					}
					decided++
					assert.Equal(t, []string{tc.bounce, tc.review}, []string{rc.F(sprint.FieldDecideBounce), rc.F(sprint.FieldDecideReview)}, rc.ID)
					assert.Equal(t, []string{tc.bounce, tc.review}, []string{p.DecideBounce, p.DecideReview}, "the packet hands the reader the bars")
					assert.Equal(t, "flash", rc.F(sprint.FieldTier))
				}
				assert.Equal(t, want, decided, "%s's decide reads", id)
			}
			h.clean("decide read asked")
		})
	}
}
