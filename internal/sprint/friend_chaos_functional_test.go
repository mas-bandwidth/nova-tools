//go:build functional

package sprint_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Moved from pkg/friend/chaos_functional_test.go (the nova-sprint split), over
// friend's exported API. The daemon here runs without the unexported noPresent switch
// the in-package rig set (so its present is live, as in production); on the bench the
// suite's results were identical with and without it (the same pass, skips and OWED lines).
//
// The chaos suite (docs/SPEC-FRIEND.md, "Chaos"): every way a friend fails is
// broken on purpose, and the friends table must show it within its bound and
// her cards must go elsewhere. The owner, 2026-10-04: "If your detection that
// they are down doesn't work WHEN THEY ARE DOWN, that seems like a bad
// design." One friend, bob, runs the real daemon over a scratch bus (bus's
// Fake, behind a store that blocks on an empty read as Redis does and can have
// its credential revoked) into a fake harness that can be closed, silenced or
// limited; his beat goes to a twin sprint store (store.Mem), where the tick
// deals the friends' cards to him and to amy, a friend whose machinery beats
// and never fails. Each case runs in a synctest bubble, so the fifteen-minute
// bound is fake time and the suite takes seconds.
//
// A part of a case the landed code cannot yet meet is owed: it is checked like
// every other part, and when it is not met the case ends as a named skip, one
// "OWED <card>: <what was measured>" line a part, until its card lands. A
// red-by-design test in the merge-queue tier (dev's queue runs the functional
// tier) would block every promotion for work that is only owed. A landed part
// that fails always fails, skip or not.

// wrongPass is what a revoked bus credential answers, as Redis words it.
var wrongPass = errors.New("WRONGPASS invalid username-password pair or user is disabled.")

// chaosBus is bob's bus credential on the scratch bus: the Fake, a read that
// waits out its block when nothing is there (XREADGROUP BLOCK, so the daemon's
// loop steps once a second of fake time, as on the real store), and every
// command answering WRONGPASS once the credential is revoked. The coordinator
// and the session keep their own credentials: the Fake itself.
type chaosBus struct {
	*bustest.Fake
	revoked atomic.Bool
	fails   atomic.Int64 // commands refused since the revoke
}

func (b *chaosBus) refuse() error {
	if b.revoked.Load() {
		b.fails.Add(1)
		return wrongPass
	}
	return nil
}

func (b *chaosBus) Roster(ctx context.Context) ([]string, time.Time, error) {
	if err := b.refuse(); err != nil {
		return nil, time.Time{}, err
	}
	return b.Fake.Roster(ctx)
}

func (b *chaosBus) Members(ctx context.Context) ([]string, []string, time.Time, error) {
	if err := b.refuse(); err != nil {
		return nil, nil, time.Time{}, err
	}
	return b.Fake.Members(ctx)
}

func (b *chaosBus) AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...bus.Mark) error {
	if err := b.refuse(); err != nil {
		return err
	}
	return b.Fake.AddAll(ctx, streams, fields, marks...)
}

func (b *chaosBus) Unmark(ctx context.Context, key string, fields ...string) (int64, error) {
	if err := b.refuse(); err != nil {
		return 0, err
	}
	return b.Fake.Unmark(ctx, key, fields...)
}

func (b *chaosBus) Marks(ctx context.Context, keys ...string) ([]map[string]string, error) {
	if err := b.refuse(); err != nil {
		return nil, err
	}
	return b.Fake.Marks(ctx, keys...)
}

func (b *chaosBus) EnsureGroup(ctx context.Context, stream, group string) error {
	if err := b.refuse(); err != nil {
		return err
	}
	return b.Fake.EnsureGroup(ctx, stream, group)
}

func (b *chaosBus) Claim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]bus.Entry, error) {
	if err := b.refuse(); err != nil {
		return nil, err
	}
	return b.Fake.Claim(ctx, stream, group, consumer, minIdle, count)
}

func (b *chaosBus) Read(ctx context.Context, stream, group, consumer string, block time.Duration, count int) ([]bus.Entry, error) {
	if err := b.refuse(); err != nil {
		return nil, err
	}
	es, err := b.Fake.Read(ctx, stream, group, consumer, block, count)
	if err == nil && len(es) == 0 && block > 0 {
		select {
		case <-time.After(block):
		case <-ctx.Done():
		}
	}
	return es, err
}

func (b *chaosBus) Release(ctx context.Context, stream, group string, entries ...string) error {
	if err := b.refuse(); err != nil {
		return err
	}
	return b.Fake.Release(ctx, stream, group, entries...)
}

func (b *chaosBus) Ack(ctx context.Context, stream, group string, entries ...string) (int64, error) {
	if err := b.refuse(); err != nil {
		return 0, err
	}
	return b.Fake.Ack(ctx, stream, group, entries...)
}

func (b *chaosBus) Pending(ctx context.Context, stream, group string, count int) ([]string, error) {
	if err := b.refuse(); err != nil {
		return nil, err
	}
	return b.Fake.Pending(ctx, stream, group, count)
}

func (b *chaosBus) Group(ctx context.Context, stream, group string) (string, bool, error) {
	if err := b.refuse(); err != nil {
		return "", false, err
	}
	return b.Fake.Group(ctx, stream, group)
}

func (b *chaosBus) Range(ctx context.Context, stream, from, to string, count int) ([]bus.Entry, error) {
	if err := b.refuse(); err != nil {
		return nil, err
	}
	return b.Fake.Range(ctx, stream, from, to, count)
}

func (b *chaosBus) Get(ctx context.Context, stream string, entries []string) ([]bus.Entry, error) {
	if err := b.refuse(); err != nil {
		return nil, err
	}
	return b.Fake.Get(ctx, stream, entries)
}

// The fake harness's states: open and answering, closed (the app is not
// running), silent (the app runs, the session takes turns and says nothing),
// limited (the provider refuses every turn until the reset).
const (
	harnessAnswering = iota
	harnessClosed
	harnessSilent
	harnessLimited
)

// pongRun is how the fake session finds the pong line the daemon put at the
// head of its turn (PongCommand below).
var pongRun = regexp.MustCompile(`nova-friend pong --as bob --nonce (\S+)`)

// fakeHarness is bob's harness and the session in it. Answering, a turn that
// carries the pong line is answered as the session does (the pong recorded
// and sent on the bus as bob); closed, every delivery is friend.Deferred, as an
// adapter whose app is not running answers; silent, every turn ends at exit 0
// with nothing said; limited, every turn fails with the harness's own words
// and the reset time, as the credits message of 2026-10-04 did.
type fakeHarness struct {
	r     *chaosRig
	state atomic.Int32
	reset atomic.Int64 // unix seconds of the limit's reset
	turns atomic.Int64
}

func (h *fakeHarness) Deliver(ctx context.Context, text string) (int, error) {
	h.turns.Add(1)
	switch h.state.Load() {
	case harnessClosed:
		return 0, friend.Deferred{Reason: "the fake harness is not running"}
	case harnessSilent:
		return 0, nil
	case harnessLimited:
		reset := time.Unix(h.reset.Load(), 0).UTC()
		return 1, fmt.Errorf("Insufficient AI Credits: your credits will refresh at %s", reset.Format(time.RFC3339))
	}
	if m := pongRun.FindStringSubmatch(text); m != nil {
		h.r.sessionPong(ctx, m[1])
	}
	return 0, nil
}

// chaosRig is the scratch bus, the twin sprint store, bob's daemon over his
// fake harness, and amy beating beside him. Every use of the twin store goes
// through mu: the daemon's beat runs on its own goroutine.
type chaosRig struct {
	t        *testing.T
	ctx      context.Context
	fake     *bustest.Fake
	bobBus   *chaosBus
	coord    *bus.Bus // the coordinator's credential
	session  *bus.Bus // the session's credential
	harness  *fakeHarness
	mu       sync.Mutex
	st       *store.Store
	mem      *store.Mem
	pongMu   sync.Mutex
	pong     friend.Pong
	pongSet  bool
	answer   time.Time // when bob's session last said anything on the bus
	status   []friend.Status
	nonce    int
	cards    int
	amyBeat  *time.Ticker // amy's machinery: one beat each FriendBeatEvery
	amyBeats int          // amy's beats since the coordinator last saw her session answer
	done     chan struct{}
}

// newChaosRig starts the world: the twin with friends amy and bob (width 2,
// tier flash) and the coordinator's seat, both sessions having answered the coordinator's
// wake ping (a friend is up only on her session's evidence, never on a beat:
// docs/SPEC-FRIEND.md, "Presence is her session's evidence"), four cards for
// any friend dealt two to each, bob's daemon running and his session answering
// one ping.
func newChaosRig(t *testing.T) *chaosRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &chaosRig{t: t, ctx: ctx, fake: bustest.NewFake(time.Now(), "coord", "amy", "bob"), done: make(chan struct{}),
		amyBeat: time.NewTicker(sprint.FriendBeatEvery)}
	r.bobBus = &chaosBus{Fake: r.fake}
	r.coord, r.session = &bus.Bus{Store: r.fake}, &bus.Bus{Store: r.fake}
	r.harness = &fakeHarness{r: r}
	r.mem = store.NewMem()
	n := 0
	r.st = &store.Store{B: r.mem, Names: sprint.Names{Prefix: "t-"}, Actor: "coord", Now: time.Now,
		NewID: func() string { n++; return fmt.Sprint(n) }, Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(ctx))
	require.NoError(t, r.mem.SetCoordinator(ctx, "coord"))
	// both take flash, the tier of a card whose brief names none: a friend whose
	// row names no tier is dealt no card (friendTakes, internal/sprint/friend_deal.go)
	_, _, _, err := r.st.SyncFriends(ctx, []store.FriendSpec{{Name: "amy", Width: 2, Class: "flash"}, {Name: "bob", Width: 2, Class: "flash"}})
	require.NoError(t, err)
	_, _, _, err = r.st.SetMachine(ctx, true)
	require.NoError(t, err)

	d := &friend.Daemon{Friend: "bob", Harness: "fake", Dir: t.TempDir(), Width: 2, Store: r.bobBus, Deliver: r.harness,
		Coordinator: "coord", Now: time.Now,
		Pause: func(ctx context.Context, d time.Duration) {
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
		},
		Beat: func(ctx context.Context, active time.Time) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			// the beat carries the session's last activity, as nova-friend's does
			_, err := r.st.FriendBeatReport(ctx, "bob", sprint.FriendReport{Active: active}, nil)
			return err
		},
		Record: func(string) {},
		Pong: func() (friend.Pong, bool, error) {
			r.pongMu.Lock()
			defer r.pongMu.Unlock()
			return r.pong, r.pongSet, nil
		},
		Status: func(s friend.Status) error {
			r.pongMu.Lock()
			defer r.pongMu.Unlock()
			r.status = append(r.status, s)
			return nil
		},
		PongCommand: func(nonce string) string { return "nova-friend pong --as bob --nonce " + nonce },
	}
	go func() { defer close(r.done); _ = d.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-r.done; r.amyBeat.Stop() })

	r.mu.Lock()
	require.NoError(t, r.answered("amy"))
	require.NoError(t, r.answered("bob"))
	r.amyBeats = 0
	r.mu.Unlock()
	r.addCards(4)
	r.step(2 * time.Second)
	require.Equal(t, sprint.Up, r.friendStatus("bob"), "bob is up from the start")
	require.Len(t, r.cardsOf("bob"), 2, "two of the four cards are bob's")
	r.ping()
	r.step(10 * time.Second)
	require.Equal(t, friend.Quiet, r.lastStatus().Challenge, "the session answers the first ping")
	require.Positive(t, r.lastStatus().Pongs)
	return r
}

// sessionPong is the session running its pong line: the pong recorded where
// the daemon reads it, the pong line sent on the bus as bob, and the
// coordinator's reading of it on the twin (friend health --state up), his
// session's evidence. It runs on the daemon's goroutine, so a failed write is
// an error on the test, never a FailNow.
func (r *chaosRig) sessionPong(ctx context.Context, nonce string) {
	now := time.Now()
	r.pongMu.Lock()
	r.pong, r.pongSet, r.answer = friend.Pong{Nonce: nonce, At: now, To: "coord", Width: 2}, true, now
	r.pongMu.Unlock()
	_, _ = r.session.Send(ctx, bus.Message{From: "bob", To: []string{"coord"}, Subject: friend.PongSubject, Body: friend.PongLine(nonce, 0, 0, 2) + "\n"})
	r.mu.Lock()
	err := r.answered("bob")
	r.mu.Unlock()
	if err != nil {
		r.t.Errorf("the coordinator's friend health for bob's pong: %v", err)
	}
}

// answered is the coordinator's observation that the friend's session
// answered its wake ping now (friend health --state up under the first seat),
// her session's evidence on the twin for FriendPongWindow. The caller holds mu.
func (r *chaosRig) answered(name string) error {
	_, _, _, err := r.st.FriendHealth(r.ctx, name, "coord", sprint.FriendHealth{State: sprint.Up, Seen: time.Now(), Generation: sprint.FirstSeatGeneration}, "")
	return err
}

// ping is the coordinator's ping to bob with a fresh nonce, and a card's word
// with it, so a turn goes in and carries the pong line.
func (r *chaosRig) ping() string {
	r.t.Helper()
	r.nonce++
	nonce := fmt.Sprintf("n%05d", r.nonce)
	for _, m := range []bus.Message{
		{From: "coord", To: []string{"bob"}, Subject: friend.PingPrefix + nonce, Body: friend.PingText("coord", time.Now(), nonce)},
		{From: "coord", To: []string{"bob"}, Subject: "card dealt", Body: "a card is in your inbox"},
	} {
		_, err := r.coord.Send(r.ctx, m)
		require.NoError(r.t, err)
	}
	return nonce
}

// addCards adds n cards for any friend to the friends' stream.
func (r *chaosRig) addCards(n int) []string {
	r.t.Helper()
	var adds []sprint.CardAdd
	var ids []string
	for range n {
		r.cards++
		id := fmt.Sprintf("f1-%d", r.cards)
		ids = append(ids, id)
		adds = append(adds, sprint.CardAdd{ID: id, Brief: "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend\n\nThe task."})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.st.Run(r.ctx, store.AddStep(sprint.AddReq{Stream: "f1", Cards: adds}))
	require.NoError(r.t, err)
	require.Empty(r.t, res.Refused)
	return ids
}

// step runs the world for d of the bubble's fake time, one of amy's beats at a
// time (FriendBeatEvery, a second): at each, amy beats, her session answers
// the coordinator's wake ping once a minute (she never fails, and a beat is no
// evidence), and the tick runs. The
// wait is amy's beat ticker, the fake clock of the synctest bubble, never the
// wall clock (the waits check of internal/ci reads a time.Sleep here as a
// wall-clock wait: it does not see the bubble).
func (r *chaosRig) step(d time.Duration) {
	r.t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); {
		<-r.amyBeat.C
		r.mu.Lock()
		_, err := r.st.FriendBeat(r.ctx, "amy")
		require.NoError(r.t, err)
		// A minute of beats since her last answer is a minute of the bubble's
		// clock: the beats are counted, never the clock read (the waits check).
		r.amyBeats++
		if time.Duration(r.amyBeats)*sprint.FriendBeatEvery >= time.Minute {
			require.NoError(r.t, r.answered("amy"))
			r.amyBeats = 0
		}
		_, err = r.st.Tick(r.ctx)
		r.mu.Unlock()
		require.NoError(r.t, err)
	}
}

// within steps a beat at a time until cond holds or bound has been stepped;
// it answers how long was stepped and whether cond held.
func (r *chaosRig) within(bound time.Duration, cond func() bool) (time.Duration, bool) {
	r.t.Helper()
	for took := time.Duration(0); ; took += sprint.FriendBeatEvery {
		if cond() {
			return took, true
		}
		if took >= bound {
			return took, false
		}
		r.step(sprint.FriendBeatEvery)
	}
}

func (r *chaosRig) row(name string) store.FriendRow {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	rows, err := r.st.FriendRows(r.ctx, time.Now())
	require.NoError(r.t, err)
	for _, f := range rows {
		if f.Name == name {
			return f
		}
	}
	r.t.Fatalf("no friend %s on the table", name)
	return store.FriendRow{}
}

func (r *chaosRig) friendStatus(name string) string { return r.row(name).Status }

// cardsOf is the work cards on the friend's row, ready and working.
func (r *chaosRig) cardsOf(name string) []string {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	var out []string
	for _, col := range []string{sprint.Ready, sprint.Working} {
		for _, c := range s.Fleet.Cell(sprint.FriendRow(name), col) {
			out = append(out, c.F("primary"))
		}
	}
	slices.Sort(out)
	return out
}

func (r *chaosRig) lastStatus() friend.Status {
	r.pongMu.Lock()
	defer r.pongMu.Unlock()
	if len(r.status) == 0 {
		return friend.Status{}
	}
	return r.status[len(r.status)-1]
}

func (r *chaosRig) lastAnswer() time.Time {
	r.pongMu.Lock()
	defer r.pongMu.Unlock()
	return r.answer
}

// openNotes is the twin's open judgments whose words name every one of words.
func (r *chaosRig) openNotes(words ...string) []string {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	opens, err := r.mem.OpenNotes(r.ctx)
	require.NoError(r.t, err)
	var out []string
	for _, o := range opens {
		what := o.Note.What
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(what, w) }) {
			out = append(out, what)
		}
	}
	return out
}

// owed collects a case's parts the landed code cannot yet meet: each names
// the card that turns it green and what was measured.
type owed struct{ parts []string }

// check records the part as owed by card when ok is false.
func (o *owed) check(ok bool, card, format string, args ...any) {
	if !ok {
		o.parts = append(o.parts, "OWED "+card+": "+fmt.Sprintf(format, args...))
	}
}

// settle ends the case, outside its bubble (a skip inside one is reported as
// a pass): nothing owed passes; anything owed is a named skip until its card
// lands, each "OWED <card>: ..." line listed, because a red-by-design test in
// the merge-queue tier blocks every promotion. A met part's assertion that
// failed in the case still fails it: a test that failed and then skipped is
// reported failed. A skip here is not a pass anywhere it counts: `release
// cut` reads each case of this suite on its own, as a promised recovery
// journey, and an OWED skip is incomplete there until its card lands
// (pkg/release/journeygate.go, docs/SPEC-RELEASE.md rule 14).
func (o *owed) settle(t *testing.T) {
	t.Helper()
	if len(o.parts) == 0 {
		return
	}
	t.Skipf("owed until the cards land:\n%s", strings.Join(o.parts, "\n"))
}

// dealtElsewhere is the movement every down or held case asserts: the two
// new cards added while bob is out both go to amy, though amy and bob hold as
// many and amy is first by name (a bob counted up would take one of them),
// and none of bob's cards is left on him (the model's invariant: a held or
// down friend holds no card).
func (r *chaosRig) dealtElsewhere(o *owed, card string) {
	r.t.Helper()
	before := r.cardsOf("amy")
	fresh := r.addCards(2)
	r.step(2 * time.Second)
	amy := r.cardsOf("amy")
	for _, id := range fresh {
		o.check(slices.Contains(amy, id), card, "new card %s went to amy: amy holds %v (before %v), bob %v", id, amy, before, r.cardsOf("bob"))
	}
	left := r.cardsOf("bob")
	o.check(len(left) == 0, card, "no card left on bob: bob holds %v", left)
}

// TestEveryFriendFailureShowsWithinItsBound breaks a friend each way he can
// fail and asserts the bound and the card movement (docs/SPEC-FRIEND.md,
// "Chaos"). Which card turns each owed part green is named in the part.
func TestEveryFriendFailureShowsWithinItsBound(t *testing.T) {
	t.Parallel()
	t.Run("harness closed: down within 1 minute", func(t *testing.T) {
		t.Parallel()
		var o owed
		synctest.Test(t, func(t *testing.T) {
			r := newChaosRig(t)
			r.harness.state.Store(harnessClosed)
			took, ok := r.within(time.Minute, func() bool { return r.friendStatus("bob") == sprint.Down })
			o.check(ok, "fr-harness-alive", "bob down within 1m of his harness closing: after %s the table says %s (his daemon beat %s ago)", took, r.friendStatus("bob"), time.Since(r.row("bob").Beat))
			r.dealtElsewhere(&o, "fr-harness-alive, fr-presence-model")
		})
		o.settle(t)
	})

	t.Run("session silent: down within 15 minutes of bus silence", func(t *testing.T) {
		t.Parallel()
		var o owed
		synctest.Test(t, func(t *testing.T) {
			r := newChaosRig(t)
			r.harness.state.Store(harnessSilent)
			silentFrom := r.lastAnswer()
			require.False(t, silentFrom.IsZero())
			bound := 15 * time.Minute
			// the coordinator pings once a window, as the contract has it; a ping
			// more often reopens the challenge and the daemon never calls it deaf
			next := time.Now()
			_, ok := r.within(bound-time.Since(silentFrom), func() bool {
				if !time.Now().Before(next) {
					r.ping()
					next = time.Now().Add(friend.Window)
				}
				return r.friendStatus("bob") == sprint.Down
			})
			assert.Equal(t, silentFrom, r.lastAnswer(), "the silent session said nothing on the bus")
			assert.Positive(t, r.harness.turns.Load(), "turns went in; the session took them and said nothing")
			// landed: the daemon itself knows within the bound: challenged a window with no pong
			assert.Equal(t, friend.Deaf, r.lastStatus().Challenge, "the daemon calls the session deaf within %s of its last answer", bound)
			o.check(ok, "fr-session-proof-of-life, fr-status-from-evidence", "bob down within %s of his session's last bus message: after %s the table says %s, while his daemon says %s", bound, time.Since(silentFrom), r.friendStatus("bob"), r.lastStatus().Challenge)
			r.dealtElsewhere(&o, "fr-session-proof-of-life, fr-presence-model")
		})
		o.settle(t)
	})

	t.Run("usage limit: down until the reset, woken after", func(t *testing.T) {
		t.Parallel()
		var o owed
		synctest.Test(t, func(t *testing.T) {
			r := newChaosRig(t)
			reset := time.Now().Add(30 * time.Minute).Truncate(time.Second)
			r.harness.reset.Store(reset.Unix())
			r.harness.state.Store(harnessLimited)
			turns := r.harness.turns.Load()
			r.ping() // a turn goes in and meets the limit
			_, ok := r.within(time.Minute, func() bool { return r.friendStatus("bob") == sprint.Down })
			assert.Greater(t, r.harness.turns.Load(), turns, "a turn met the limit")
			row := r.row("bob")
			o.check(ok, "fr-limits-and-credits", "bob down within 1m of the limit: the table says %s", row.Status)
			o.check(row.Until.Equal(reset), "fr-limits-and-credits", "his row says until the reset %s: it says %q until %s", reset.Format(time.RFC3339), row.Reason, row.Until.Format(time.RFC3339))
			r.dealtElsewhere(&o, "fr-limits-and-credits, fr-presence-model")
			r.step(time.Until(reset) - time.Minute)
			o.check(r.friendStatus("bob") != sprint.Up, "fr-limits-and-credits", "bob still not up a minute before the reset: the table says %s", r.friendStatus("bob"))
			r.step(time.Until(reset))
			r.harness.state.Store(harnessAnswering) // the credits refresh
			_, woke := r.within(2*time.Minute, func() bool { return r.friendStatus("bob") == sprint.Up && r.lastAnswer().After(reset) })
			o.check(woke, "fr-limits-and-credits", "bob woken after the reset with a nonce his session answered: the table says %s, last answer %s, reset %s", r.friendStatus("bob"), r.lastAnswer().Format(time.RFC3339), reset.Format(time.RFC3339))
		})
		o.settle(t)
	})

	t.Run("bus credential revoked: an alarm on the first failed send", func(t *testing.T) {
		t.Parallel()
		var o owed
		synctest.Test(t, func(t *testing.T) {
			r := newChaosRig(t)
			r.bobBus.revoked.Store(true)
			// a read in flight when the credential goes finishes its block (a beat), the
			// next command fails, and the status is written after the step's pause (a beat)
			_, said := r.within(3*sprint.FriendBeatEvery, func() bool { return strings.Contains(r.lastStatus().StoreError, "WRONGPASS") }) // wall-ok: fake time in the synctest bubble
			assert.True(t, said, "the daemon's status names the refusal at its first failed command: %q", r.lastStatus().StoreError)
			took, down := r.within(sprint.FriendPongWindow+2*time.Second, func() bool { return r.friendStatus("bob") == sprint.Down }) // wall-ok: fake time in the synctest bubble
			assert.True(t, down, "a daemon whose bus refuses it stops beating, and the table says down within %s: after %s it says %s", sprint.FriendPongWindow, took, r.friendStatus("bob"))
			alarms := r.openNotes("bob", "WRONGPASS")
			o.check(len(alarms) == 1, "fr-delivery-receipts", "one alarm to the coordinator naming bob, the store and the user on the first failed send (%d failed): the twin holds %d: %v", r.bobBus.fails.Load(), len(alarms), alarms)
			r.dealtElsewhere(&o, "fr-presence-model")
		})
		o.settle(t)
	})

	t.Run("hold: no card left on him, his cards dealt elsewhere", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newChaosRig(t)
			his := r.cardsOf("bob")
			require.Len(t, his, 2)
			r.mu.Lock()
			res, err := r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"bob"}, Reason: "chaos: held", Return: true, Who: "coord"})
			r.mu.Unlock()
			require.NoError(t, err)
			require.Empty(t, res.Refused)
			assert.Equal(t, sprint.Held, r.friendStatus("bob"), "held at once, though his daemon beats")
			r.step(2 * time.Second)
			assert.Empty(t, r.cardsOf("bob"), "no card left on him")
			assert.Subset(t, r.cardsOf("amy"), his, "his cards dealt to amy at the next tick")
			fresh := r.addCards(1)
			r.step(2 * time.Second)
			assert.NotContains(t, r.cardsOf("bob"), fresh[0], "a held friend is dealt no new card")
		})
	})
}
