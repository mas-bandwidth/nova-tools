package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sentBeat is one beat as the sprint server got it: when, and the session's last
// evidence it carried (zero: none).
type sentBeat struct{ at, evidence time.Time }

// The beat is the daemon's liveness from its first step to its last, even
// while the session is deaf or the bus refuses (docs/SPEC-FRIEND.md, the beat).
// Session evidence is a separate fact; the fake clock moves one second a step.
func TestTheDaemonBeatsEverySecondWhateverTheSessionSays(t *testing.T) {
	t.Parallel()
	store := bustest.NewFake(t0, "ada", "bob")
	app := &closedApp{}
	const total = 30 * 60 // thirty minutes of steps, one second each
	var mu sync.Mutex
	now, steps, nonces, started := t0, 0, 0, false
	var beats []sentBeat
	at := map[int]func(){}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	sc := &SessionCheck{
		Friend: "bob", Store: store, Now: clock,
		Nonce: func() string { nonces++; return fmt.Sprintf("b%d", nonces) },
		Text: func(nonce string) string {
			return SessionCheckPrefix + nonce + "\nanswer: nova-friend pong --as bob --nonce " + nonce
		},
		Go: func(f func()) { f() },
	}
	sc.Deliver = sc.Gate(app)
	d := &Daemon{
		Friend: "bob", Harness: "fake", Dir: t.TempDir(), Width: 8, Store: sc.DaemonStore(), Deliver: sc.Deliver,
		Seat: func(context.Context) (string, error) { return "ada", nil }, StepBeatForTests: true,
		// the first read of the clock is the daemon starting, at t0; each read after it
		// is one step of the loop, a second each
		Now: func() time.Time {
			mu.Lock()
			if started {
				now = now.Add(BeatEvery)
				steps++
			}
			started = true
			f := at[steps]
			if steps > total {
				cancel()
			}
			n := now
			mu.Unlock()
			if f != nil {
				f()
			}
			return n
		},
		Pause: func(context.Context, time.Duration) {}, // the clock moves by the step alone
		Beat: func(ctx context.Context, _ time.Time) error {
			return sc.Beat(func(context.Context) error {
				b := sentBeat{at: clock(), evidence: func() time.Time {
					sc.mu.Lock()
					defer sc.mu.Unlock()
					if sc.m == nil {
						return time.Time{}
					}
					return sc.m.LastHeard
				}()}
				mu.Lock()
				beats = append(beats, b)
				mu.Unlock()
				return nil
			})(ctx)
		},
		Record: func(string) {},
		Pong:   func() (Pong, bool, error) { return Pong{}, false, nil },
		Status: func(Status) error { return nil },
	}
	direct := &bus.Bus{Store: store}
	answer := func() time.Time { // the session runs the pong line of the newest check it was given
		got := app.got()
		require.NotEmpty(t, got, "a session check went into the session")
		var nonce string
		for i := len(got) - 1; i >= 0; i-- {
			line, _, _ := strings.Cut(got[i], "\n")
			if n, ok := strings.CutPrefix(line, SessionCheckPrefix); ok {
				nonce = n
				break
			}
		}
		require.NotEmpty(t, nonce, "answer the latest delivered check, not a startup note")
		_, err := direct.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: PongSubject, Body: PongLine(nonce, 0, 1, 2) + "\n"})
		require.NoError(t, err)
		return clock()
	}
	var answered time.Time
	storeDown, storeUp := 8*60, 9*60 // the bus store refuses for a minute while the session is deaf
	at[storeDown] = func() { store.Fail = errors.New("connection refused") }
	at[storeUp] = func() { store.Fail = nil }
	at[20*60] = func() { answered = answer() }
	require.NoError(t, d.Run(ctx))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, total, len(beats), "one beat per step, every step: never held for the session, never for the store")
	assert.Equal(t, t0.Add(BeatEvery), beats[0].at, "the first beat is the first step's: the daemon proves itself from the moment it starts")
	for i := 1; i < len(beats); i++ {
		require.Equal(t, BeatEvery, beats[i].at.Sub(beats[i-1].at), "beat %d is a second after the one before", i)
	}
	up, reason := sc.Present()
	assert.True(t, up, "the session answered at the twentieth minute")
	assert.Empty(t, reason)
	for i, b := range beats {
		switch {
		case b.at.Before(answered):
			require.True(t, b.evidence.IsZero(), "beat %d at %s carries no session evidence: the session has not answered", i, b.at.Sub(t0))
		default:
			require.Equal(t, answered, b.evidence, "beat %d at %s carries the session's answer, read in the step it was sent", i, b.at.Sub(t0))
		}
	}
}

// cancelProbe is a harness whose delivery records whether it ran under a live
// context: a delivery that reaches it only after its context is cancelled is a
// killed session check, the finding of this card.
type cancelProbe struct {
	mu    sync.Mutex
	texts []string
	err   error
}

func (p *cancelProbe) Deliver(ctx context.Context, text string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		p.err = err
		return 1, err
	}
	p.texts = append(p.texts, text)
	return 0, nil
}

// TestAHeartbeatSendDoesNotCancelTheSessionCheckDelivery: the heartbeat's
// per-send context is cancelled the moment the send returns, but the session
// check's delivery runs under a context that outlives it, so the check reaches
// the session whatever the beat's deadline (docs/SPEC-FRIEND.md, the beat).
func TestAHeartbeatSendDoesNotCancelTheSessionCheckDelivery(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := bustest.NewFake(t0, "ada", "bob")
		probe := &cancelProbe{}
		sc := &SessionCheck{
			Friend: "bob", Store: store, Now: func() time.Time { return t0 },
			Nonce: func() string { return "n1" },
			Text: func(nonce string) string {
				return SessionCheckPrefix + nonce + "\nanswer: nova-friend pong --as bob --nonce " + nonce
			},
			Ctx: context.Background(), // the daemon's long-lived context, as nova-friend run wires it
		}
		sc.Deliver = sc.Gate(probe)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = Heartbeat(ctx, sc.Beat(func(context.Context) error { return nil })) // ignored: it ends with ctx
		}()
		synctest.Wait()
		probe.mu.Lock()
		defer probe.mu.Unlock()
		require.Len(t, probe.texts, 1, "the session check reached the session")
		assert.NoError(t, probe.err, "the delivery ran under a live context, not the send's cancelled one")
		cancel()
		synctest.Wait()
		<-done
	})
}
