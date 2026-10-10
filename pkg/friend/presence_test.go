package friend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closedApp is a harness whose app is not running: every delivery "works"
// (the daemon is alive and the command exits 0) and nothing inside ever
// answers, the finding of 2026-10-04.
type closedApp struct {
	mu    sync.Mutex
	texts []string
}

func (c *closedApp) Deliver(_ context.Context, text string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return 0, nil
}

func (c *closedApp) got() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

// presenceRig is one SessionCheck over bus's Fake, the closed app and a
// clock the test moves by hand; the check's delivery runs inline.
type presenceRig struct {
	store          *bustest.Fake
	app            *closedApp
	sc             *SessionCheck
	daemon, direct *bus.Bus // what the daemon sends, and what anyone else (the session, a friend) sends
	now            time.Time
	nonces         int
	beats          int
	beat           func(context.Context) error
}

func newPresenceRig(t *testing.T) *presenceRig {
	t.Helper()
	r := &presenceRig{store: bustest.NewFake(t0, "ada", "bob"), app: &closedApp{}, now: t0}
	r.sc = &SessionCheck{
		Friend: "bob", Store: r.store, Now: func() time.Time { return r.now },
		Nonce: func() string { r.nonces++; return "n" + string(rune('0'+r.nonces)) },
		Text: func(nonce string) string {
			return SessionCheckPrefix + nonce + "\nanswer: nova-friend pong --as bob --nonce " + nonce
		},
		Go: func(f func()) { f() },
	}
	r.sc.Deliver = r.sc.Gate(r.app)
	r.daemon, r.direct = &bus.Bus{Store: r.sc.DaemonStore()}, &bus.Bus{Store: r.store}
	r.beat = r.sc.Beat(func(context.Context) error { r.beats++; return nil })
	return r
}

func (r *presenceRig) step(t *testing.T, d time.Duration) {
	t.Helper()
	r.now = r.now.Add(d)
	_ = r.beat(context.Background()) // ignored: the refusal is what Present says, asserted by the caller
}

func (r *presenceRig) send(t *testing.T, b *bus.Bus, from, subject, body string) {
	t.Helper()
	_, err := b.Send(context.Background(), bus.Message{From: from, To: []string{"ada"}, Subject: subject, Body: body})
	require.NoError(t, err)
}

func (r *presenceRig) present(t *testing.T) (bool, string) {
	t.Helper()
	return r.sc.Present()
}

// TestOnlyTheSessionCanAnswerTheNonce: a friend whose app is closed is
// down, however well its daemon answers. The daemon delivers a session check
// with a fresh nonce through the adapter; the daemon's own pong, a pong the
// daemon writes, another friend's pong and a wrong nonce all leave it down;
// five minutes without the session's answer is down "no session answer", and
// no beat goes to the sprint server; the session's answer brings it up; a check
// goes in ProveEvery after it whatever the session says, the old nonce no
// answer to it; silence with it unanswered is down; a check the session has not
// read is not asked again before ReaskAfter; a message the session writes
// brings it up as an answer does (docs/SPEC-FRIEND.md, presence).
func TestOnlyTheSessionCanAnswerTheNonce(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	ctx := context.Background()

	r.step(t, BeatEvery)
	require.Len(t, r.app.got(), 1, "the first step delivers a session check through the adapter")
	assert.True(t, strings.HasPrefix(r.app.got()[0], SessionCheckPrefix+"n1"), "the check carries its nonce: %q", r.app.got()[0])
	up, reason := r.present(t)
	assert.False(t, up, "a daemon that started proves nothing about the session")
	assert.Equal(t, NotYetAnswered, reason)
	assert.Equal(t, 0, r.beats, "no beat to the sprint server before the session answers")

	r.send(t, r.daemon, "bob", DaemonPongSubject, "daemon-pong n1\n")
	r.send(t, r.daemon, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.send(t, r.direct, "ada", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.send(t, r.direct, "bob", PongSubject, PongLine("n9", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.False(t, up, "the daemon's pong, a pong the daemon wrote, another friend's and a wrong nonce are no answer")

	r.step(t, SessionBound)
	up, reason = r.present(t)
	assert.False(t, up)
	assert.Equal(t, NoSessionAnswer, reason, "five minutes with no session answer")
	assert.Error(t, r.beat(ctx), "the beat is held back while the session is down")
	assert.Equal(t, 0, r.beats)
	assert.Len(t, r.app.got(), 1, "one check per nonce")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, reason = r.present(t)
	assert.True(t, up, "the session's answer to the nonce brings it back up")
	assert.Empty(t, reason)
	assert.Equal(t, 1, r.beats, "and the beat flows again")

	for range 5 { // a session that talks on the bus is still checked every ProveEvery: only an answer proves it to the server
		r.send(t, r.direct, "bob", "status", "working on it\n")
		r.step(t, 4*time.Minute)
	}
	require.Len(t, r.app.got(), 2, "one check ProveEvery after the answer, whatever the session says")
	assert.True(t, strings.HasPrefix(r.app.got()[1], SessionCheckPrefix+"n2"), "a new nonce: %q", r.app.got()[1])
	up, _ = r.present(t)
	assert.True(t, up, "her messages keep her up while the check waits for its answer")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.send(t, r.daemon, "bob", DaemonPongSubject, "daemon-pong n2\n")
	r.step(t, SessionQuiet+SessionBound)
	up, reason = r.present(t)
	assert.False(t, up, "silent, and the old nonce and the daemon's pong answer nothing")
	assert.Equal(t, NoSessionAnswer, reason)
	assert.Len(t, r.app.got(), 2, "the check the session has not read is not asked again before ReaskAfter")

	r.send(t, r.direct, "bob", "status", "hello\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.True(t, up, "while down, any message the session writes brings it back up: the session is alive (the finding of 2026-10-05)")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n2", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	require.Len(t, r.app.got(), 3, "a late answer: the next check is timed from the ask, so it goes in at once")
	r.step(t, ReaskAfter)
	require.Len(t, r.app.got(), 4, "and the one after on the cadence")
	r.step(t, SessionBound)
	up, _ = r.present(t)
	require.False(t, up, "quiet again, and the next check unanswered")
	r.send(t, r.direct, "bob", PongSubject, PongLine("n4", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.True(t, up, "the next answer brings it back up")
}

// TestASessionCheckWaitsForTheTurnUnderWay: the check is a turn of its own,
// never a second one in the same session; it waits for the daemon's turn to
// end, and its bound runs from when it went in.
func TestASessionCheckWaitsForTheTurnUnderWay(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	release, entered := make(chan struct{}), make(chan struct{})
	held := &heldApp{release: release, entered: entered}
	r.sc.Deliver = r.sc.Gate(held)
	go func() { _, _ = r.sc.Deliver.Deliver(context.Background(), "the daemon's turn") }()
	<-entered

	r.step(t, BeatEvery)
	r.step(t, SessionBound+time.Minute)
	assert.Equal(t, []string{"the daemon's turn"}, held.got(), "no check while the turn runs")
	_, reason := r.present(t)
	assert.Equal(t, NotYetAnswered, reason, "the bound has not started: nothing was asked")

	close(release)
	require.Eventually(t, func() bool { r.step(t, 0); return len(held.got()) == 2 }, 5*time.Second, time.Millisecond)
	assert.True(t, strings.HasPrefix(held.got()[1], SessionCheckPrefix), "the check goes in once the turn ends")
	r.step(t, SessionBound)
	_, reason = r.present(t)
	assert.Equal(t, NoSessionAnswer, reason)
}

// TestAPassiveSessionIsCheckedOnItsOwnStream: a harness with no deliver
// command reads its own stream, so the check goes there, as the daemon's.
func TestAPassiveSessionIsCheckedOnItsOwnStream(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	r.sc.Deliver = r.sc.Gate(Stub{Harness: "claude"})
	r.step(t, BeatEvery)
	got, err := r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, SessionCheckPrefix+"n1", got[0].Message().Subject)
	up, _ := r.present(t)
	assert.False(t, up, "the check on the stream is the daemon's, no answer")
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0)+"\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.True(t, up)
}

type heldApp struct {
	mu      sync.Mutex
	texts   []string
	release chan struct{}
	entered chan struct{}
}

func (h *heldApp) Deliver(_ context.Context, text string) (int, error) {
	h.mu.Lock()
	h.texts = append(h.texts, text)
	first := len(h.texts) == 1
	h.mu.Unlock()
	if first {
		close(h.entered)
		<-h.release
	}
	return 0, nil
}

func (h *heldApp) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.texts...)
}
