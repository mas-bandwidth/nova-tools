package member

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The member stamps progress on a card while its child prints, at most every ProgressEvery,
// and a child that prints nothing stamps nothing (docs/SPEC-SPRINT.md section 8, the rules
// table's row late; tla/SprintRules.tla, Stamp): the late rule reads the silence. The clock
// is the pass's own, given to each Tick.
func TestTheMemberStampsProgressWhileTheChildPrints(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	p := pk("c1")
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	g.s.set("take", 0, takeJSON(t, p))
	t0 := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err := g.m.Tick(t0)
	require.NoError(t, err)
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	child := g.r.child("c1")
	require.NotNil(t, child)
	stamp := "progress --as m c1@1 --epoch 7"
	for _, step := range []struct {
		name    string
		at      time.Duration // the pass, after t0
		printed time.Duration // the child's last output, after t0; negative: none yet
		stamps  int           // the progress verbs sent so far
	}{
		{"started, printed nothing: no stamp", time.Minute, -1, 0},
		{"printed: stamped", 2 * time.Minute, 2 * time.Minute, 1},
		{"printed again inside ProgressEvery: not yet", 3 * time.Minute, 3 * time.Minute, 1},
		{"printed again past ProgressEvery: stamped", 2*time.Minute + ProgressEvery, 2*time.Minute + ProgressEvery, 2},
		{"silent since: no stamp, however long", 30 * time.Minute, 2*time.Minute + ProgressEvery, 2},
	} {
		if step.printed >= 0 {
			child.print(t0.Add(step.printed))
		}
		_, err := g.m.Tick(t0.Add(step.at))
		require.NoError(t, err, step.name)
		lines := g.s.lines("progress")
		assert.Len(t, lines, step.stamps, step.name)
		for _, l := range lines {
			assert.Equal(t, stamp, l, step.name)
		}
	}
}
