package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A launch refused at staging, before any child ran, is the member's failure and not the
// card's: it is finished failed with the kind `staging refused` and the stage's own reason,
// never the missing shape's; the sprint deals the card to another member
// (tla/CardContract.tla, StageRefused).
func TestAStagingRefusedLaunchIsFinishedWithTheStagingKindAndReason(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(Result{Report: "no child ran", End: EndStaging, Staging: "no bench mirror for https://example.com/o/quack.git"})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"finish --as m c1@2 --report staging refused: no bench mirror for https://example.com/o/quack.git; no child ran --failed --epoch 7"}, g.s.lines("finish"))
}

// Judge names a staging refusal by its own kind and reason, never the shape's, and never with
// another end in front of it.
func TestJudgeNamesAStagingRefusalByItsKind(t *testing.T) {
	t.Parallel()
	fin, why := Judge(Result{End: EndStaging, Staging: "stage-timeout"}, Push{None: "nothing"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, "staging refused: stage-timeout", why)
}
