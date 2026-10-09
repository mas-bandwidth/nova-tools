package member

import (
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPausedMemberKeepsCurrentChildSettlesItAndHoldsNewStarts(t *testing.T) {
	t.Parallel()
	for _, reader := range []bool{false, true} {
		t.Run(map[bool]string{false: "work", true: "reader"}[reader], func(t *testing.T) {
			t.Parallel()
			m, s, r, _ := stopRig(Config{As: "m", Width: 2, Reader: reader})
			p, pending := pk("c1"), pk("c2")
			col := working
			if reader {
				p.Kind, pending.Kind = "read", "read"
				col = func(id string, gen int, p *Packet) queueCard { c := reading(id, p); c.Gen = gen; return c }
			}
			require.True(t, m.start(p))
			child := r.child(p.Card)
			require.NotNil(t, child)
			s.set("queue", 0, queueWith(t, "PAUSED", p.Epoch, col(p.Card, p.Gen, &p), col(pending.Card, pending.Gen, &pending), ready("c3")))
			for range 2 {
				_, err := m.Tick(time.Unix(10, 0))
				require.NoError(t, err)
			}
			assert.Zero(t, child.stops(), "pause never cancels the current child")
			assert.Equal(t, []string{p.Card}, r.started(), "queued/recovery children remain held")
			assert.Empty(t, s.lines("take"))
			assert.Empty(t, s.lines("stop-return"))
			child.end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc", Report: "done"})
			_, err := m.Tick(time.Unix(20, 0))
			require.NoError(t, err)
			verb := "finish"
			if reader {
				verb = "report"
			}
			assert.NotEmpty(t, s.lines(verb), "completion settles while paused")
			assert.Equal(t, []string{p.Card}, r.started())
			assert.Empty(t, s.lines("stop-return"))
			s.set("queue", 0, queueWith(t, "RUNNING", pending.Epoch, col(pending.Card, pending.Gen, &pending)))
			_, err = m.Tick(time.Unix(30, 0))
			require.NoError(t, err)
			assert.Equal(t, []string{p.Card, pending.Card}, r.started(), "unpause recovers the same pending claim")
		})
	}
}

func TestStopWhilePausedStillCancelsCurrentMemberChild(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(Config{As: "m", Width: 1})
	p := pk("c1")
	require.True(t, m.start(p))
	child := r.child(p.Card)
	s.set("queue", 0, queueWith(t, "PAUSED", p.Epoch, working(p.Card, p.Gen, &p)))
	_, err := m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	assert.Zero(t, child.stops())
	s.set("queue", 0, queueWith(t, "STOPPED", p.Epoch, working(p.Card, p.Gen, &p)))
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, child.stops())
	assert.True(t, m.Stopped())
}

func TestPausedReaderStageRetryRetainsItsResultAndClaim(t *testing.T) {
	t.Parallel()
	m, _, r, _ := stopRig(Config{As: "r", Width: 1, Reader: true})
	p := pk("r1")
	p.Kind = "read"
	m.paused = true
	m.running[p.Card] = launch{packet: p, child: unstartedChild{}, res: &Result{End: EndStaging}, retryAt: time.Unix(1, 0)}
	l := m.running[p.Card]
	assert.False(t, m.reportOne(p.Card, l, time.Unix(100, 0), &p, func(string, []byte) {}))
	assert.Empty(t, r.started())
	assert.Equal(t, l, m.running[p.Card], "pause keeps the owed staging retry in place")
}

func TestPauseDuringScriptVerificationSettlesCurrentVerdictAndHoldsFallback(t *testing.T) {
	t.Parallel()
	for _, verified := range []bool{true, false} {
		t.Run(map[bool]string{true: "completed", false: "fallback"}[verified], func(t *testing.T) {
			t.Parallel()
			entered, release := make(chan struct{}), make(chan struct{})
			m, s, r, _ := stopRig(Config{As: "r", Width: 1, Reader: true, Background: true,
				ScriptVerify: func(Packet, cardhdr.Class) (bool, string) {
					close(entered)
					<-release
					return verified, "current script verdict"
				},
			})
			p := pk("r1")
			p.Kind, p.Brief = "read", scriptBrief
			require.True(t, m.start(p))
			<-entered
			q := queueOut{Machine: "PAUSED", Epoch: p.Epoch}
			cards := map[string]queueCard{p.Card: reading(p.Card, &p)}
			m.machineStop(q, cards, time.Now())
			close(release)
			m.WaitLong()
			m.collect()
			assert.Empty(t, r.started(), "pause prohibits a new model fallback")
			s.set("queue", 0, queueWith(t, "PAUSED", p.Epoch, reading(p.Card, &p)))
			_, err := m.Tick(time.Now())
			require.NoError(t, err)
			m.WaitLong() // Background result collection is a current completion, not a new job.
			_, err = m.Tick(time.Now())
			require.NoError(t, err)
			if verified {
				assert.NotEmpty(t, s.lines("report"), "already-completed script result settles")
			} else {
				assert.Empty(t, s.lines("report"))
				assert.Contains(t, m.running, p.Card, "same claim remains held")
			}
			assert.Empty(t, s.lines("stop-return"))
		})
	}
}

func TestPauseObservedAtDelayedStartKeepsTheClaimUntilUnpause(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	m, s, r, _ := stopRig(Config{As: "m", Width: 1, Background: true,
		Admit: func(Packet) error {
			close(entered)
			<-release
			return fmt.Errorf("launch admission refused: machine PAUSED")
		},
	})
	p := pk("c1")
	require.True(t, m.start(p))
	<-entered
	m.machineStop(queueOut{Machine: "PAUSED", Epoch: p.Epoch}, map[string]queueCard{p.Card: working(p.Card, p.Gen, &p)}, time.Now())
	close(release)
	m.WaitLong()
	m.collect()
	assert.Empty(t, r.started())
	assert.True(t, m.running[p.Card].admissionDenied)
	s.set("queue", 0, queueWith(t, "PAUSED", p.Epoch, working(p.Card, p.Gen, &p)))
	_, err := m.Tick(time.Now())
	require.NoError(t, err)
	assert.Contains(t, m.running, p.Card)
	assert.Empty(t, s.lines("finish"))
	assert.Empty(t, s.lines("stop-return"))
	m.cfg.Admit = nil
	s.set("queue", 0, queueWith(t, "RUNNING", p.Epoch, working(p.Card, p.Gen, &p)))
	_, err = m.Tick(time.Now())
	require.NoError(t, err)
	m.WaitLong()
	m.collect()
	assert.Equal(t, []string{p.Card}, r.started())
}
