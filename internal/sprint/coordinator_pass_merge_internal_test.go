package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Merge health carries dev behind as DevBehind computes it, and the base-gate rule's stopped
// stream, beside the forge's lines (docs/SPEC-SPRINT.md section 8, "The coordinator's pass").
func TestTheCoordinatorPassCarriesMergeHealthEveryTenMinutesWithDevLagAndBaseRed(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	health := func(r TickReq) *Note {
		p, _ := TickCoordinatorPass(w.s, r)
		for i := range p.Notes {
			if p.Notes[i].Type == NMergeHealth {
				return &p.Notes[i]
			}
		}
		return nil
	}
	assert.Nil(t, health(TickReq{}), "nothing landed, nothing read")

	c := w.s.Work.Card("s1-1")
	require.NotNil(t, c)
	w.place(w.s.Work, "s1-1", c.Row, Landed)
	c.Fields["landed"], c.Fields["base"] = stamp(w.s.Now), "sprint/mechanical-2026-10-02"
	assert.Nil(t, health(TickReq{}), "one landing just now: dev is not behind")
	w.tick(PromoteAge)
	lag, ok := DevBehind(w.s)
	require.True(t, ok)
	n := health(TickReq{})
	require.NotNil(t, n, "dev behind is a line")
	assert.Contains(t, n.What, lag.Line())
	assert.Contains(t, n.What, "dev is behind: 1 cards landed on sprint/mechanical-2026-10-02 since no promotion recorded")
	assert.True(t, n.StreamLevel)

	w.note(Note{Kind: Judgment, Type: NBaseRed, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1, At: w.s.Now, What: "the base fails its tree gate"})
	n = health(TickReq{Merge: &MergeFacts{Base: "sprint/mechanical-2026-10-02"}})
	require.NotNil(t, n)
	assert.Contains(t, n.What, "stream s1 stopped: its base fails its tree gate")
	assert.Len(t, MergeHealthLines(w.s, &MergeFacts{Base: "sprint/mechanical-2026-10-02"}), 2, "two lines: base red, dev behind")
	assert.True(t, strings.HasPrefix(n.What, "merge health: stream s1 stopped"), "the base first")
}
