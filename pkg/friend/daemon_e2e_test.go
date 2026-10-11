package friend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The findings of 2026-10-04 on the Studio, one test each: a friend's turns
// run minutes, so one message per turn queued them for half an hour
// (batching); the daemon's own words and answered pings were turns of their
// own (noise); a fixed ten-minute cap killed real work (no fixed kill); a
// session the provider refused every turn was delivered into for hours and
// nothing said so (a broken session).

func (r *rig) pending(t *testing.T) (pending, fresh []bus.Entry) {
	t.Helper()
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	return pending, fresh
}

// Every message waiting when the session is free goes in as ONE turn,
// oldest first, each with its id, from, subject and body, and the turn's
// exit 0 acks them together.
func TestAllPendingMessagesGoInAsOneTurnAndAreAckedTogether(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	m1 := r.send(t, "ada", "first", "one")
	m2 := r.send(t, "ada", "second", "two")
	m3 := r.send(t, "ada", "third", "three")
	r.run(t, 4)
	require.Len(t, r.delivered, 1, "one turn for the three: %v", r.delivered)
	text := r.delivered[0]
	assert.Contains(t, text, "3 message(s) for you, oldest first")
	i1, i2, i3 := strings.Index(text, "[1/3] "+m1.ID), strings.Index(text, "[2/3] "+m2.ID), strings.Index(text, "[3/3] "+m3.ID)
	assert.True(t, i1 >= 0 && i1 < i2 && i2 < i3, "each message, oldest first: %q", text)
	for _, m := range []bus.Message{m1, m2, m3} {
		assert.Contains(t, text, m.ID+" from=ada at="+m.At.Format(time.RFC3339)+" age=0m subject="+m.Subject+"\n"+m.Body+"\n")
	}
	pending, fresh := r.pending(t)
	assert.Empty(t, pending, "acked together at exit 0")
	assert.Empty(t, fresh)
	assert.Equal(t, 3, r.last().Delivered)
	require.NotEmpty(t, r.records)
	assert.Contains(t, r.records[0], `subject="first | second | third" messages=3`)
	assert.Contains(t, r.records[0], "acked=true")
}

// Messages that land while a long turn runs wait, and all of them go in as
// the next single turn, not one turn each.
func TestMessagesThatLandDuringATurnGoInTogetherAsTheNextTurn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.hold, r.releaseAt = make(chan struct{}), 30
	r.send(t, "ada", "long", "a long task")
	for _, step := range []int{3, 5, 7} {
		r.at[step] = func() { r.send(t, "ada", "later", "news") }
	}
	r.run(t, 40)
	require.Len(t, r.delivered, 2, "the long task, then one turn for the three that waited: %v", r.delivered)
	assert.Equal(t, 3, strings.Count(r.delivered[1], " subject=later\nnews\n"), r.delivered[1])
	pending, fresh := r.pending(t)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
	assert.Equal(t, 4, r.last().Delivered)
}

// A turn's text has a bound: the rest waits for the next turn, oldest first.
func TestABatchIsBoundedAndTheRestGoesInTheNextTurn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Deliver = limited{r, 800}
	for i := 0; i < 3; i++ {
		r.send(t, "ada", "m", strings.Repeat("x", 300))
	}
	r.run(t, 8)
	require.Len(t, r.delivered, 3)
	assert.Contains(t, r.delivered[0], "3 message(s) for you")
	assert.Contains(t, r.delivered[0], "and 2 more: nova-bus recv --as bob --all")
	assert.Contains(t, r.delivered[1], "2 message(s) for you")
	for _, text := range r.delivered {
		assert.LessOrEqual(t, len(text), 800)
	}
	pending, fresh := r.pending(t)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
}

// A ping is answered at once by the daemon and acked: never a turn of its
// own. The session proves itself by the pong line that rides at the head
// of the next turn carrying real messages, and that pong ends the challenge.
func TestAPingIsAnsweredByTheDaemonAndNeverPushedInAlone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.d.PongCommand = func(nonce string) string {
		return "/opt/nova/bin/nova-friend pong --as bob --nonce " + nonce + " --dir /w/bob --redis store:6379"
	}
	var afterPing Status
	r.at[4] = func() {
		afterPing = r.last()
		r.send(t, "ada", "PING n2", PingText("ada", t0, "n2"))
		r.send(t, "ada", "PING n3", PingText("ada", t0, "n3"))
	}
	var work bus.Message
	r.at[8] = func() { work = r.send(t, "ada", "work", "do the thing") }
	r.at[11] = func() { // the session ran the line at the head of its turn
		r.mu.Lock()
		r.pong, r.pongSet = Pong{Nonce: "n3", At: r.now, To: "ada"}, true
		r.mu.Unlock()
	}
	r.run(t, 14)
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1", "daemon-pong: daemon-pong n2", "daemon-pong: daemon-pong n3"}, r.adaGot(t), "each ping answered once, at once")
	require.Len(t, r.delivered, 1, "no ping is a turn; the one turn is the work: %v", r.delivered)
	assert.True(t, strings.HasPrefix(r.delivered[0], "Run this now, first, exactly as written: /opt/nova/bin/nova-friend pong --as bob --nonce n3 --dir /w/bob --redis store:6379\nThen read on.\n\n"), r.delivered[0])
	assert.Contains(t, r.delivered[0], "[1/1] "+work.ID+" from=ada at="+work.At.Format(time.RFC3339)+" age=0m subject=work\ndo the thing\n")
	assert.NotContains(t, r.delivered[0], "PING n", "the pings themselves are not in the turn")
	assert.Equal(t, Challenged, afterPing.Challenge)
	assert.Equal(t, "ada", afterPing.Seat)
	assert.Equal(t, Quiet, r.last().Challenge, "the pong with the current nonce ended it")
	pending, fresh := r.pending(t)
	assert.Empty(t, pending, "the pings were acked by the daemon")
	assert.Empty(t, fresh)
	assert.Equal(t, 1, r.last().Delivered, "only the work counts as delivered")
}

// The coordinator going silent and coming back is never a turn by itself:
// the word collapses to the latest state, and a word the session would not
// need (back, when it never heard silent) is dropped.
func TestTheCoordinatorsSilenceIsNeverATurnAloneAndCollapsesToTheLatest(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	window := int(Window / BeatEvery)
	var silent Status
	r.at[window+5] = func() {
		silent = r.last()
		r.send(t, "ada", "PING n1", PingText("ada", t0, "n1")) // back before anything was said
	}
	r.at[window+8] = func() { r.send(t, "ada", "plain", "after the outage") }
	r.at[2*window+20] = func() { r.send(t, "ada", "news", "during the second outage") }
	r.run(t, 2*window+25)
	require.Len(t, r.delivered, 2, "only the two turns with messages: %v", r.delivered)
	assert.Equal(t, Silent, silent.Connection)
	assert.NotContains(t, r.delivered[0], "coordinator", "silent then back, unheard, is nothing to say")
	assert.True(t, strings.HasPrefix(r.delivered[1], "nova-friend: coordinator silent since "), "the second outage rides with the news: %q", r.delivered[1])
	assert.Contains(t, r.delivered[1], "during the second outage")
	assert.Equal(t, 1, strings.Count(r.delivered[1], "coordinator silent"), "said once")
}

// A ping peeked during a long turn is answered once, steps the machine once
// (no double ping), and is never pushed in when the session is free.
func TestAPingDuringALongTurnIsAnsweredOnceAndNeverPushedIn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	window := int(Window / BeatEvery)
	r.hold, r.releaseAt = make(chan struct{}), window+20
	r.send(t, "ada", "long", "a long task")
	r.at[2] = func() { r.send(t, "ada", "PING n1", PingText("ada", t0, "n1")) }
	var midTurn Status
	r.at[100] = func() {
		midTurn = r.last()
		r.send(t, "ada", "PING n2", PingText("ada", t0, "n2"))
	}
	r.stopAfter = window + 40
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	require.NoError(t, r.d.Run(ctx))
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1", "daemon-pong: daemon-pong n2"}, r.adaGot(t), "answered from a peek while the turn ran, and once only")
	assert.True(t, midTurn.LastPing.After(t0.Add(BeatEvery)) && midTurn.LastPing.Before(t0.Add(10*BeatEvery)), "the machine saw the ping when the daemon did: %s", midTurn.LastPing)
	assert.Equal(t, Challenged, midTurn.Challenge)
	require.Len(t, r.delivered, 1, "the long task only: %v", r.delivered)
	assert.Equal(t, Connected, r.last().Connection, "pings peeked during a long turn keep the connection")
	pending, fresh := r.pending(t)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
}

// worker is a harness whose turn runs until the daemon stops it, printing
// whenever the test says; a turn after the first ends at once, exit 0.
type worker struct {
	mu        sync.Mutex
	calls     int
	seen      func()
	stoppedAt time.Time
	now       func() time.Time
}

func (w *worker) Deliver(ctx context.Context, _ string) (int, error) {
	w.mu.Lock()
	w.calls++
	first := w.calls == 1
	w.seen, _ = ctx.Value(outputKey{}).(func())
	w.mu.Unlock()
	if !first {
		return 0, nil
	}
	<-ctx.Done()
	w.mu.Lock()
	w.stoppedAt = w.now()
	w.mu.Unlock()
	return -1, errors.New("the delivery was stopped with its process group")
}

func (w *worker) print() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen()
}

// A turn that prints keeps running past any fixed cap; only a turn silent
// past SilentStop is stopped, the record says why, and no second turn
// starts while one runs.
func TestATurnThatPrintsRunsOnAndOnlyASilentOneIsStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		w := &worker{now: func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }}
		r.d.Deliver, r.passive = w, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "card dealt", "real work")
		minute := int(time.Minute / BeatEvery)
		for m := 1; m <= 45; m++ { // forty-five minutes of output, once a minute
			r.at[m*minute] = func() { w.print() }
		}
		r.at[50*minute] = func() { r.send(t, "ada", "while busy", "wait your turn") }
		var callsAt60 int
		r.at[60*minute] = func() { w.mu.Lock(); callsAt60 = w.calls; w.mu.Unlock() }
		r.run(t, 70*minute)
		lastOutput := t0.Add(45 * time.Minute)
		require.False(t, w.stoppedAt.IsZero(), "the silent turn was stopped")
		assert.False(t, w.stoppedAt.Before(lastOutput.Add(DefaultSilentStop)), "never before %s of silence: stopped at %s", DefaultSilentStop, w.stoppedAt.Sub(t0))
		assert.True(t, w.stoppedAt.Before(lastOutput.Add(DefaultSilentStop+5*BeatEvery)), "and at once after: stopped at %s", w.stoppedAt.Sub(t0))
		assert.Equal(t, 1, callsAt60, "no second turn while the first ran")
		assert.Equal(t, 2, w.calls, "the waiting message went in once the stopped turn ended")
		var stopping, ended string
		for _, line := range r.records {
			if strings.Contains(line, "stopping: no output for 20m0s") {
				stopping = line
			}
			if strings.Contains(line, `subject="card dealt"`) && strings.Contains(line, "exit=") {
				ended = line
			}
		}
		assert.NotEmpty(t, stopping, "%v", r.records)
		assert.Contains(t, ended, `stopped="no output for 20m0s"`)
		assert.Contains(t, ended, "deliveries=1/3", "a stopped turn is a failure of its messages: pending, handed in again")
	})
}

// refuser is a session whose provider refuses every turn the same way.
type refuser struct {
	mu    sync.Mutex
	calls int
}

func (f *refuser) Deliver(context.Context, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return 1, ProviderRefused{Session: "ses_x", Reason: "invalid_request_error: The request could not be processed"}
}

// The same provider refusal on BrokenAfter turns in a row breaks the
// session: nothing more is delivered into it, every message stays pending
// and none is given up, the status says so, and the seat the last ping
// named is told once.
func TestTheSameRefusalThreeTurnsInARowMarksTheSessionBrokenAndTellsTheSeat(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		f := &refuser{}
		r.d.Deliver, r.passive = f, true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
		r.send(t, "ada", "one", "x")
		r.at[3] = func() { r.send(t, "ada", "two", "x") }
		r.at[6] = func() { r.send(t, "ada", "three", "x") }
		r.at[9] = func() { r.send(t, "ada", "four", "x") }
		r.at[12] = func() { r.send(t, "ada", "PING n2", PingText("ada", t0, "n2")) }
		r.at[15] = func() { r.store.Advance(bus.ClaimAfter) } // claims open: still nothing goes in
		r.run(t, 30)
		assert.Equal(t, DefaultBrokenAfter, f.calls, "three refused turns, then none")
		s := r.last()
		assert.Equal(t, SessionBroken, s.Session)
		assert.Equal(t, "ses_x", s.SessionID)
		assert.Equal(t, "invalid_request_error: The request could not be processed", s.SessionReason)
		got := r.adaGot(t)
		assert.Equal(t, []string{
			"daemon-pong: daemon-pong n1",
			"friend bob: session ses_x broken: invalid_request_error: The request could not be processed: friend bob: session ses_x broken: invalid_request_error: The request could not be processed\nThe provider refused 3 turns in a row the same way. The daemon delivers nothing into the session until it restarts; every message stays pending, none given up. Renew the session, then restart the daemon (nova-friend install again, or launchctl kickstart -k gui/<uid>/com.nova.friend-bob).",
			"daemon-pong: daemon-pong n2",
		}, got, "told once; pings still answered while broken")
		pending, fresh := r.pending(t)
		assert.Len(t, pending, 3, "the refused messages stay pending")
		assert.Len(t, fresh, 2, "the fourth and the second ping wait unread")
		for _, line := range r.records {
			assert.NotContains(t, line, "given_up")
			assert.NotContains(t, line, "acked")
		}
		assert.Contains(t, strings.Join(r.records, "\n"), "refused=3/3 session=broken")
	})
}

// A refusal followed by a turn that answers is no streak; the coordinator
// flag is who is told when no ping has named a seat.
func TestARefusalStreakResetsOnSuccessAndTheFlagNamesTheCoordinator(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		var mu sync.Mutex
		calls := 0
		r.d.Deliver = deliverFunc(func(context.Context, string) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 3 { // the third turn answers; the rest are refused
				return 0, nil
			}
			return 1, ProviderRefused{Session: "ses_y", Reason: "authentication_error"}
		})
		r.passive = true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.d.Coordinator = "ada"
		for i, step := range []int{1, 4, 7, 10, 13, 16} {
			subject := []string{"a", "b", "c", "d", "e", "f"}[i]
			r.at[step] = func() { r.send(t, "ada", subject, "x") }
		}
		var before Status
		r.at[15] = func() { before = r.last() }
		r.run(t, 25)
		assert.Equal(t, SessionOK, before.Session, "refused, refused, answered, refused, refused: no three in a row")
		assert.Equal(t, 6, calls)
		assert.Equal(t, SessionBroken, r.last().Session, "the sixth is the third refusal in a row")
		got := r.adaGot(t)
		require.Len(t, got, 1)
		assert.True(t, strings.HasPrefix(got[0], "friend bob: session ses_y broken: authentication_error"), "no seat named: the flag's coordinator is told: %v", got)
	})
}

type deliverFunc func(context.Context, string) (int, error)

func (f deliverFunc) Deliver(ctx context.Context, text string) (int, error) { return f(ctx, text) }
