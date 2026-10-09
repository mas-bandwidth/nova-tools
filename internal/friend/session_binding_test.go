package friend

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The adapter reports the conversation that takes the nonce, independently of a pong.
type boundSessionApp struct {
	closedApp
	session string
}

func (a *boundSessionApp) DeliverySession() string               { return a.session }
func (a *boundSessionApp) SwitchDeliverySession(id string) error { a.session = id; return nil }

func TestDeliveryGoesOnlyToAProvenSessionAndAFriendCanSwitchIt(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	app := &boundSessionApp{session: "conversation-a"}
	r.sc.Deliver = r.sc.Gate(app)
	reply := func(nonce, session string) {
		r.send(t, r.direct, "bob", PongSubject, PongLine(nonce, 0, 0, 4)+"\nsession_id="+session+"\n")
	}
	r.step(t, time.Second)
	reply("n1", "conversation-other")
	r.step(t, time.Second)
	proved, _ := r.sc.Proof()
	assert.False(t, proved, "a nonce read by another conversation proves no delivery target")
	up, why := r.sc.Present()
	assert.False(t, up)
	assert.Contains(t, why, "mismatch")
	assert.Contains(t, why, "conversation-a")
	assert.Contains(t, why, "conversation-other")
	reply("n1", "conversation-a")
	r.step(t, time.Second)
	proved, _ = r.sc.Proof()
	assert.True(t, proved)
	_, err := r.direct.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: "request", Subject: "session conversation-forged", Body: "switch"})
	require.NoError(t, err)
	r.step(t, time.Second)
	assert.Equal(t, "conversation-a", app.session, "only the friend herself can request a switch")
	_, err = r.direct.Send(context.Background(), bus.Message{From: "bob", To: []string{"bob"}, Kind: "request", Subject: "session conversation-b", Body: "switch"})
	require.NoError(t, err)
	r.step(t, time.Second)
	proved, _ = r.sc.Proof()
	assert.False(t, proved, "a requested target is not proven by the old target's receipt")
	assert.Equal(t, "conversation-b", app.session, "the next proof round trip targets the candidate")
	reply("n1", "conversation-b")
	r.step(t, time.Second)
	proved, _ = r.sc.Proof()
	assert.False(t, proved, "a switch uses a fresh nonce")
	reply("n2", "conversation-b")
	r.step(t, time.Second)
	proved, _ = r.sc.Proof()
	assert.True(t, proved)
	exit, err := r.sc.Deliver.Deliver(context.Background(), "ordinary message")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
}

func TestSessionSwitchPersistsBeforeItBecomesProven(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	app := &boundSessionApp{session: "conversation-a"}
	r.sc.Deliver = r.sc.Gate(app)
	var saved []string
	r.sc.PersistSession = func(id string) error { saved = append(saved, id); return nil }
	r.step(t, time.Second)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0)+"\nsession_id=conversation-a\n")
	r.step(t, time.Second)
	assert.Equal(t, []string{"conversation-a"}, saved)
	info := r.sc.SessionInfo()
	assert.Equal(t, "conversation-a", info.ID)
	assert.Equal(t, "proven", info.Proof)
}

func TestSessionUnprovenIsDownEvenWithARecentPong(t *testing.T) {
	t.Parallel()
	for _, proof := range []string{"", "unproven", "mismatch"} {
		t.Run(proof, func(t *testing.T) {
			t.Parallel()
			v, why := factsVerdict(DaemonFacts{Friend: "f", SessionID: "conversation-a", SessionObserved: "conversation-b", SessionProof: proof, Presence: PresenceUp, PongAge: "1s"}, HarnessFacts{Broken: "-", Harness: "codex", Delivered: 1}, BusFacts{RealSince: 1}, WorkFacts{}, time.Hour)
			assert.Equal(t, VerdictDown, v)
			assert.Contains(t, why, "session unproven")
		})
	}
}

func TestSessionSwitchWaitsForTheActiveTurn(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	app := &boundSessionApp{session: "conversation-a"}
	r.sc.Deliver = r.sc.Gate(app)
	r.step(t, time.Second)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0)+"\nsession_id=conversation-a\n")
	r.step(t, time.Second)
	r.sc.turn.RLock()
	assert.True(t, r.sc.SessionRequest(context.Background(), bus.Message{From: "bob", To: []string{"bob"}, Kind: "request", Subject: "session conversation-b"}))
	proved, _ := r.sc.Proof()
	assert.False(t, proved)
	assert.Equal(t, "conversation-a", app.session)
	r.sc.turn.RUnlock()
	r.step(t, time.Second)
	assert.Equal(t, "conversation-b", app.session)
	proved, _ = r.sc.Proof()
	assert.False(t, proved)
}

// The presence rig only ranges the bus log, so a question claimed by the daemon
// loop never fails it. This runs the loop: while the push is unproven the
// question is injected and acked, and an ordinary message stays pending.
func TestTheDaemonLoopInjectsTheLiveSessionQuestionWhileUnproven(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sc := &SessionCheck{
		Friend: "bob", Store: r.store, Now: func() time.Time { return t0 },
		Nonce:          func() string { return "n1" },
		Text:           func(nonce string) string { return "check " + nonce },
		Go:             func(f func()) { f() },
		RequireSession: true,
		JoinFrom:       func() string { return "ada" },
	}
	sc.Deliver = sc.Gate(r)
	r.d.Deliver = sc.Deliver
	r.d.Proof = func() (bool, string) { return false, "n1" }
	r.d.SessionRequest = sc.SessionRequest
	r.d.SessionInfo = sc.SessionInfo

	r.send(t, "ada", "hello", "ordinary work")
	_, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: "request", Subject: "join", Body: "join"})
	require.NoError(t, err)
	r.run(t, 4)

	require.Len(t, r.delivered, 1, "the question goes in while the push is unproven; ordinary text does not")
	assert.Contains(t, r.delivered[0], "session <live-session-id>")
	assert.NotContains(t, r.delivered[0], "ordinary work")
	assert.Equal(t, PushUnproven, r.last().Push)
	assert.Equal(t, "unproven", sc.SessionInfo().Proof)

	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, fresh)
	var subjects []string
	for _, e := range pending {
		subjects = append(subjects, e.Message().Subject)
	}
	assert.NotContains(t, subjects, LiveSessionSubject)
	assert.Contains(t, subjects, "hello")
}

func TestOnlyTheCoordinatorJoinAsksForALiveSession(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	app := &boundSessionApp{session: "conversation-a"}
	r.sc.Deliver = r.sc.Gate(app)
	r.sc.JoinFrom = func() string { return "ada" }
	r.step(t, time.Second)
	_, err := r.direct.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: "request", Subject: "join", Body: "join"})
	require.NoError(t, err)
	r.step(t, time.Second)
	entries, err := r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 0)
	require.NoError(t, err)
	asks := 0
	for _, e := range entries {
		if e.Message().Subject == "live session id wanted" {
			asks++
		}
	}
	assert.Equal(t, 1, asks)
	r.step(t, time.Second)
	entries, err = r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 0)
	require.NoError(t, err)
	count := 0
	for _, e := range entries {
		if e.Message().Subject == "live session id wanted" {
			count++
		}
	}
	assert.Equal(t, 1, count, "an unproven target is asked once")
}

func TestSessionPersistenceFailureLeavesDeliveryBlocked(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	app := &boundSessionApp{session: "conversation-a"}
	r.sc.Deliver = r.sc.Gate(app)
	r.sc.PersistSession = func(string) error { return context.DeadlineExceeded }
	r.step(t, time.Second)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0)+"\nsession_id=conversation-a\n")
	r.step(t, time.Second)
	proved, _ := r.sc.Proof()
	assert.False(t, proved)
	_, err := r.sc.Deliver.Deliver(context.Background(), "ordinary delivery")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unproven")
}

// A mailbox receipt is the adapter's own observation, independent of the claimed pong id.
type readSessionApp struct {
	boundSessionApp
	reader string
}

func (a *readSessionApp) ReadNonceSession(string) string { return a.reader }

func TestTheAdapterReceiptCannotBeReplacedByAPongClaim(t *testing.T) {
	t.Parallel()
	for _, observed := range []string{"", "conversation-other"} {
		t.Run(observed, func(t *testing.T) {
			t.Parallel()
			r := newPresenceRig(t)
			app := &readSessionApp{boundSessionApp: boundSessionApp{session: "conversation-a"}, reader: observed}
			r.sc.Deliver = r.sc.Gate(app)
			r.step(t, time.Second)
			r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0)+"\nsession_id=conversation-a\n")
			r.step(t, time.Second)
			proved, _ := r.sc.Proof()
			assert.False(t, proved, "a pong claim cannot replace the adapter's reader observation")
			app.reader = "conversation-a"
			if observed != "" {
				r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0)+"\nsession_id=conversation-a\n")
			}
			r.step(t, time.Second)
			proved, _ = r.sc.Proof()
			assert.True(t, proved, "an early pong waits for the mailbox read; a mismatched one needs a new answer")
		})
	}
}
