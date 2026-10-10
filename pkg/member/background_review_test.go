package member

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reviewHeldPusher struct{ began, release chan struct{} }

func (p *reviewHeldPusher) Push(Packet, Result) Push {
	close(p.began)
	<-p.release
	return Push{Sha: fullSha}
}

func TestBackgroundReviewCleanupWaitsForAnObsoletePush(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"claim moved", "card disappeared"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			pu := &reviewHeldPusher{began: make(chan struct{}), release: make(chan struct{})}
			g, packet := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: fullSha})
			g.m.cfg.Background = true
			g.m.Drain()
			_, err := g.tick(t)
			require.NoError(t, err)
			<-pu.began
			// The pusher is deliberately using the old launch until release. An Ender
			// may remove that launch directory as soon as it receives Ended.
			if change == "claim moved" {
				packet.Gen++
				g.s.set("queue", 0, queueJSON(t, 7, working("c1", packet.Gen, &packet)))
			} else {
				g.s.set("queue", 0, queueJSON(t, 7))
			}
			_, err = g.tick(t)
			assert.NoError(t, err)
			assert.Empty(t, g.r.endedLaunches(), "do not authorize checkout removal while its push is still using it")
			assert.Empty(t, g.s.lines("finish"), "an obsolete claim is never reported")
			close(pu.release)
			<-g.m.Wake()
			_, err = g.tick(t)
			assert.NoError(t, err)
			assert.Empty(t, g.s.lines("finish"), "a late push is not a finish of the replacement claim")
		})
	}
}
