package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The first read of a flash card is a decide read (docs/SPEC-SPRINT.md section 6): its read
// card carries the sprint row's two bars as nova-config applied them, and its packet hands
// them to the reader; any other read of the attempt, and every read drawn on pro, carries
// none and is a strings read. With no bars in the sprint row no read is a decide read. The
// pro card pins its model, so its reads are drawn on its tier, pro, whichever tier its
// work is dealt from.
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
			h.m.SetDecideBars(Bars{Bounce: tc.bounce, Review: tc.review})
			require.NoError(t, h.st.BeatReaders(h.ctx))
			h.addReady("s1", 1, briefOf("pro", "model: prov-pro-a/model-pro-a\ntokens: 1000\ndeadline: 60"))
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
				for _, rc := range reads {
					if rc.F(sprint.FieldTier) == "pro" {
						assert.Empty(t, rc.F(sprint.FieldDecideBounce), "a read drawn on pro is a strings read")
					}
				}
				assert.Equal(t, want, decided, "%s's decide reads", id)
			}
			h.clean("decide read asked")
		})
	}
}

// A flash card's attempt has one decide read standing, whatever the ask places again: a
// read returned with no verdict beside it is asked again as a strings read, and the decide
// read itself returned is asked again as the decide read, never two of them.
func TestAReadAskedAgainKeepsOneDecideReadAtTheAttempt(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"the strings read", "the decide read"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
			h.m.SetDecideBars(Bars{Bounce: "0.5", Review: "0.3"})
			require.NoError(t, h.st.BeatReaders(h.ctx))
			h.addReady("s1", 1, briefOf("flash", ""))
			h.startMachine()
			h.machine()
			h.work("m1")
			h.work("m2")
			h.machine()
			var back *sprint.Card
			for _, rc := range readsAt(h.snap(), h.snap().Work.Card("s1-1")) {
				if (rc.F(sprint.FieldDecideBounce) != "") == (which == "the decide read") {
					back = rc
				}
			}
			if back == nil {
				t.Skipf("the attempt holds no %s to return (one read a flash card)", which)
			}
			h.must(ReadStep(sprint.ReadReq{As: back.Row, Return: true, Reason: "no verdict (ran=false)", Sel: sprint.Sel{IDs: []string{back.ID}}}))
			h.machine()
			decided := 0
			for _, rc := range readsAt(h.snap(), h.snap().Work.Card("s1-1")) {
				if rc.F(sprint.FieldDecideBounce) != "" {
					decided++
				}
			}
			assert.Equal(t, 1, decided, "one decide read stands at the attempt")
			h.clean("asked again")
		})
	}
}
