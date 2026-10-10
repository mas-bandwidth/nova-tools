package friend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dshOnMac is an app check of the DeepSeek Harness app (appAlive, as DSH.Alive
// ran it before 2026-10-06; it now reads the session's turns) read as macOS
// reads it, over a fake process table, so the test is the same on any bench:
// what an app check says is advisory either way.
type dshOnMac struct {
	ps  *processTable
	dir string
}

func (a dshOnMac) Alive(ctx context.Context) Liveness {
	return appAlive(ctx, a.ps.run, a.dir, "darwin", "zhi", dshDesktop)
}

// dshDesktop is the DeepSeek Harness desktop app.
var dshDesktop = App{Bundle: "/Applications/DeepSeek Harness.app", Name: "DeepSeek Harness"}

// The process tables of the finding of 2026-10-05: zhi's session run from the
// dsh command line with no app, and the app open with nothing answering.
const (
	headlessDSH = "zhi 4242 /usr/local/bin/dsh --headless --dir /w/zhi\n"
	dshAppOpen  = "zhi 777 /Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness\n"
)

// livenessRig is zhi's daemon as nova-friend run wires it: the harness watch
// in front of the session check in front of the beat to the sprint server,
// over a twin store (bus's Fake) and a clock the test moves by hand.
type livenessRig struct {
	store   *bustest.Fake
	session *closedApp // what went into the session: the checks
	sc      *SessionCheck
	d       *Daemon
	w       *HarnessWatch
	direct  *bus.Bus // what the session sends: nova-friend pong, nova-bus send

	mu      sync.Mutex
	now     time.Time
	nonces  int
	sprint  int // beats that reached the sprint server
	records []string
}

func newLivenessRig(t *testing.T, listing string) *livenessRig {
	t.Helper()
	r := &livenessRig{store: bustest.NewFake(t0, "ada", "zhi"), session: &closedApp{}, now: t0}
	clock := func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
	record := func(line string) { r.mu.Lock(); r.records = append(r.records, line); r.mu.Unlock() }
	r.sc = &SessionCheck{
		Friend: "zhi", Store: r.store, Now: clock, Record: record,
		Nonce: func() string { r.nonces++; return fmt.Sprintf("z%d", r.nonces) },
		Text: func(nonce string) string {
			return SessionCheckPrefix + nonce + "\nanswer: nova-friend pong --as zhi --nonce " + nonce
		},
		Go: func(f func()) { f() },
	}
	r.sc.Deliver = r.sc.Gate(r.session)
	r.direct = &bus.Bus{Store: r.store}
	r.d = &Daemon{Friend: "zhi", Harness: "dsh", Dir: "/w/zhi", Now: clock, Record: record, noPresent: true,
		Beat: func(ctx context.Context, _ time.Time) error {
			return r.sc.Beat(func(context.Context) error { r.mu.Lock(); r.sprint++; r.mu.Unlock(); return nil })(ctx)
		},
	}
	r.w = WatchHarness(r.d, nil)
	r.w.Alive = dshOnMac{ps: &processTable{listing: listing}, dir: "/w/zhi"}
	return r
}

// step moves the clock by d and beats once, as the daemon's loop does.
func (r *livenessRig) step(d time.Duration) error {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	return r.d.Beat(context.Background(), time.Time{})
}

// answerLatest is zhi's session running the pong line of the newest check it
// was given, on the bus as nova-friend pong sends it (from zhi); false when no
// check went in.
func (r *livenessRig) answerLatest(t *testing.T) bool {
	t.Helper()
	got := r.session.got()
	if len(got) == 0 {
		return false
	}
	first := strings.SplitN(got[len(got)-1], "\n", 2)[0]
	nonce := strings.TrimPrefix(first, SessionCheckPrefix)
	_, err := r.direct.Send(context.Background(), bus.Message{From: "zhi", To: []string{"ada"}, Subject: PongSubject, Body: PongLine(nonce, 0, 1, 2) + "\n"})
	require.NoError(t, err)
	return true
}

func (r *livenessRig) beats() int { r.mu.Lock(); defer r.mu.Unlock(); return r.sprint }

// TestAFriendWhoseSessionPongsIsUpWithNoHarnessProcess: the finding of
// 2026-10-05. Zhi ran from the dsh command line, no DeepSeek Harness app,
// and answered every SESSION CHECK with a pong for three hours; her daemon's
// harness check held the beat back and she read down the whole time. Presence
// is the session's: a session that answers is up whatever the process table
// says, and the app being open makes nobody up. The process table is
// advisory: the status says harness_seen=running|not-seen.
func TestAFriendWhoseSessionPongsIsUpWithNoHarnessProcess(t *testing.T) {
	t.Parallel()

	t.Run("headless dsh, no app, the session pongs: up for three hours", func(t *testing.T) {
		t.Parallel()
		r := newLivenessRig(t, headlessDSH)
		require.Error(t, r.step(BeatEvery), "a daemon that started proves nothing: down until the session answers")
		require.True(t, r.answerLatest(t), "the first beat put a session check into the session")
		answered := 1
		before := r.beats()
		steps := 0
		for elapsed := time.Duration(0); elapsed < 3*time.Hour; elapsed += time.Minute {
			steps++
			require.NoError(t, r.step(time.Minute), "at %s the session has answered every check; the beat goes out", elapsed)
			if n := len(r.session.got()); n > answered {
				require.True(t, r.answerLatest(t))
				answered = n
			}
			up, reason := r.sc.Present()
			require.True(t, up, "at %s: %s", elapsed, reason)
		}
		assert.Equal(t, steps, r.beats()-before, "every beat reached the sprint server")
		assert.GreaterOrEqual(t, answered, 15, "a check every quiet spell, each answered")
		assert.Equal(t, HarnessNotSeen, watchSeen(r.w), "the advisory observation says the app was not seen, and nothing more")
		r.mu.Lock()
		records := strings.Join(r.records, "\n")
		r.mu.Unlock()
		assert.Contains(t, records, "harness: not seen: alive=app the DeepSeek Harness app is not running", "said once, on the record")
		assert.Equal(t, 1, strings.Count(records, "harness: not seen"), "said when it changes, not every check")
		assert.NotContains(t, records, "down: harness not running")
	})

	t.Run("the app open, the session silent: down", func(t *testing.T) {
		t.Parallel()
		r := newLivenessRig(t, dshAppOpen)
		for elapsed := time.Duration(0); elapsed < SessionBound+2*time.Minute; elapsed += 10 * time.Second {
			assert.Error(t, r.step(10*time.Second), "at %s nothing has answered: no beat", elapsed)
		}
		assert.Zero(t, r.beats(), "a running app is no answer: no beat reached the sprint server")
		up, reason := r.sc.Present()
		assert.False(t, up)
		assert.Equal(t, NoSessionAnswer, reason)
		assert.Equal(t, HarnessRunning, watchSeen(r.w), "the advisory observation says the app runs")
	})
}

// TestASessionsOwnBusMessageBringsItUp: a friend is up when her session
// answered the last check or sent any bus message within the window; a
// message the daemon itself sent is still no proof.
func TestASessionsOwnBusMessageBringsItUp(t *testing.T) {
	t.Parallel()
	r := newLivenessRig(t, headlessDSH)
	require.Error(t, r.step(BeatEvery))
	daemon := &bus.Bus{Store: r.sc.DaemonStore()}
	_, err := daemon.Send(context.Background(), bus.Message{From: "zhi", To: []string{"ada"}, Subject: "card x done", Body: "the daemon's\n"})
	require.NoError(t, err)
	require.Error(t, r.step(BeatEvery), "the daemon's own message proves nothing")
	_, err = r.direct.Send(context.Background(), bus.Message{From: "zhi", To: []string{"ada"}, Subject: "card x done", Body: "Verdict: LAND\n"})
	require.NoError(t, err)
	require.NoError(t, r.step(BeatEvery), "the session wrote on the bus: up, and the beat goes out")
	up, _ := r.sc.Present()
	assert.True(t, up)
	r.mu.Lock()
	records := strings.Join(r.records, "\n")
	r.mu.Unlock()
	assert.Contains(t, records, "presence: up: the session wrote on the bus")
}
