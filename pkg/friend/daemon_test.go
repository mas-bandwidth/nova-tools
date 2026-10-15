package friend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is one daemon over bus's Fake, a fake harness and a clock that moves
// one second per step: no socket, no real time. The loop runs until
// stopAfter steps (a step is one beat) or the test cancels it. Pause, which
// the loop calls while a turn runs, waits for that turn to end (gate, sent
// once the turn's result is queued, so the loop's next look finds it), so
// every run is the same sequence of steps; with hold set the turn runs on
// until the releaseAt-th pause.
type rig struct {
	mu        sync.Mutex
	store     *bustest.Fake
	bus       *bus.Bus
	now       time.Time
	delivered []string
	hold      chan struct{} // when set, a delivery blocks until it is closed
	releaseAt int
	pauses    int
	gate      chan struct{}
	owed      int  // turns delivered whose gate token is not yet sent
	passive   bool // no worker: a pause returns at once
	exit      int
	beats     int
	actives   []time.Time // what each beat carried as the session's last activity
	beatErr   error
	status    []Status
	records   []string
	cancel    context.CancelFunc
	done      <-chan struct{} // the running Run's ctx.Done
	stopAfter int
	at        map[int]func() // what happens at a step, from the beat
	d         *Daemon
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{store: bustest.NewFake(t0, "ada", "bob"), now: t0, stopAfter: 1 << 20, gate: make(chan struct{}, 1), at: map[int]func(){}}
	r.bus = &bus.Bus{Store: r.store}
	r.d = &Daemon{
		Friend: "bob", Harness: "fake", Dir: t.TempDir(), Store: r.store, StepBeatForTests: true,
		Deliver: r,
		Seat:    func(context.Context) (string, error) { return "ada", nil }, // the seat the daemon tells
		Now: func() time.Time {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.now = r.now.Add(BeatEvery)
			return r.now
		},
		Pause: func(context.Context, time.Duration) {
			r.mu.Lock()
			r.pauses++
			hold, release, passive := r.hold, r.pauses == r.releaseAt, r.passive
			if release {
				close(hold)
				r.hold = nil
			}
			r.mu.Unlock()
			r.mu.Lock()
			owed := r.owed > 0
			r.mu.Unlock()
			if passive || (hold != nil && !release) || !owed {
				return // no turn to wait for, or one still running on purpose
			}
			<-r.gate
		},
		Beat: func(_ context.Context, active time.Time) error {
			r.mu.Lock()
			r.beats++
			r.actives = append(r.actives, active)
			f, stop := r.at[r.beats], r.beats >= r.stopAfter
			r.mu.Unlock()
			if f != nil {
				f()
			}
			if stop {
				r.cancel()
			}
			return r.beatErr
		},
		Record: func(line string) { r.mu.Lock(); r.records = append(r.records, line); r.mu.Unlock() },
		Status: func(s Status) error { r.mu.Lock(); r.status = append(r.status, s); r.mu.Unlock(); return nil },
	}
	r.d.turnEnded = func() {
		r.mu.Lock()
		owed := r.owed > 0
		if owed {
			r.owed--
		}
		done := r.done
		r.mu.Unlock()
		if !owed {
			return
		}
		select {
		case r.gate <- struct{}{}:
		case <-done:
			// the run is over (Run waits for this turn before it returns): no pause waits for
			// the token, so a full gate never holds Run open; room still keeps it, as before
			select {
			case r.gate <- struct{}{}:
			default:
			}
		}
	}
	return r
}

func (r *rig) Deliver(ctx context.Context, text string) (int, error) {
	r.mu.Lock()
	r.delivered = append(r.delivered, text)
	hold := r.hold
	r.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
		}
	}
	r.mu.Lock()
	r.owed++ // the token goes once the result is queued (turnEnded), never before
	r.mu.Unlock()
	return r.exit, nil
}

func (r *rig) run(t *testing.T, steps int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel, r.done = cancel, ctx.Done()
	r.mu.Unlock()
	r.stopAfter = steps
	require.NoError(t, r.d.Run(ctx))
}

func (r *rig) send(t *testing.T, from, subject, body string) bus.Message {
	t.Helper()
	m, err := r.bus.Send(context.Background(), bus.Message{From: from, To: []string{"bob"}, Subject: subject, Body: body})
	require.NoError(t, err)
	return m
}

func (r *rig) adaGot(t *testing.T) []string {
	t.Helper()
	got, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 100)
	require.NoError(t, err)
	var out []string
	for _, e := range got {
		m := e.Message()
		out = append(out, m.Subject+": "+strings.TrimSpace(m.Body))
	}
	return out
}

func (r *rig) last() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status[len(r.status)-1]
}

func TestAStatusFileThatCannotBeWrittenIsSaidOnceAMinuteAndTheBeatGoesOn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Status = func(Status) error { return errors.New("operation not permitted") }
	r.run(t, 130)
	assert.Equal(t, 130, r.beats)
	said := 0
	for _, line := range r.records {
		if strings.Contains(line, "status: operation not permitted") {
			said++
		}
	}
	assert.Equal(t, 3, said, "at the start and once a minute: %v", r.records)
}

// A server that refuses her verbs (friend beat and friend cards) with a
// non-zero code and the text "refused" is counted per verb; the third
// consecutive refusal of each is raised once — a down beat naming the reason
// and one word to the coordinator — and a server that answers again clears it
// (one up beat, one recovery line; docs/SPEC-FRIEND.md, "A refused verb").
func TestARefusedVerbIsRaisedOnTheThirdAndClearedWhenItAnswers(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Coordinator = "ada"
	var downs []string
	var ups int
	r.d.RefusalDown = func(_ time.Time, reason string) { downs = append(downs, reason) }
	r.d.RefusalUp = func() { ups++ }
	askErr := &Refused{Why: "friend cards refused: You must hook in first"}
	r.d.Held = HeldVia("bob", func(context.Context, []string) (string, error) {
		if askErr == nil {
			return `{"friend":"bob","cards":[]}`, nil
		}
		return "", askErr
	}, nil, func() time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.now
	})
	r.beatErr = &Refused{Why: "friend beat refused: You must hook in first"}

	steps := int(3*ServedEvery/BeatEvery) + 2
	var cardsAtOne, beatAtOne int
	r.at[int(ServedEvery/BeatEvery/2)] = func() {
		cardsAtOne = r.d.status.Refused[FriendCardsVerb]
		beatAtOne = r.d.status.Refused[FriendBeatVerb]
	}
	var cardsAtRaise, beatAtRaise int
	// the server answers again after three ServedEvery periods; capture the counts first
	r.at[int(3*ServedEvery/BeatEvery)] = func() {
		cardsAtRaise = r.d.status.Refused[FriendCardsVerb]
		beatAtRaise = r.d.status.Refused[FriendBeatVerb]
		askErr, r.beatErr = nil, nil
	}
	r.run(t, steps)

	assert.Less(t, beatAtOne, RefusalRepeats, "a refusal is counted once a ServedEvery, never once a beat: after half a ServedEvery the beat has %d, not one per second", beatAtOne)
	assert.LessOrEqual(t, cardsAtOne, 1, "friend cards is asked once a ServedEvery")
	assert.GreaterOrEqual(t, cardsAtRaise, RefusalRepeats, "friend cards refused, counted per verb")
	assert.GreaterOrEqual(t, beatAtRaise, RefusalRepeats, "friend beat refused, counted per verb")
	assert.Contains(t, r.last().LastRefusal, "You must hook in first")
	require.Len(t, downs, 2, "raised once per verb, not once per refusal: %v", downs)
	assert.Contains(t, strings.Join(downs, " "), "refused")
	got := r.adaGot(t)
	require.Len(t, got, 2, "one word to the coordinator per verb, through tellBroken: %v", got)
	assert.Contains(t, strings.Join(got, " "), "the server refused friend cards")
	assert.Contains(t, strings.Join(got, " "), "the server refused friend beat")
	assert.Contains(t, strings.Join(got, " "), "You must hook in first")
	assert.GreaterOrEqual(t, ups, 1, "the down is lifted once the server answers again")
	assert.Empty(t, r.last().Refused, "the counts clear on recovery")
	var recoveredLines int
	for _, l := range r.records {
		if strings.Contains(l, "recovered:") {
			recoveredLines++
		}
	}
	assert.GreaterOrEqual(t, recoveredLines, 1, "one recovery line")
}
