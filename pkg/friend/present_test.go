package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// presentRig is the daemon rig with the present on, and one card on her row (live.w1, working).
func presentRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.d.noPresent = false
	r.d.Held = func(context.Context) (Row, error) {
		return Row{Cards: []HeldCard{{Card: "live.w1", Job: "live.w1~15", Col: "working", Brief: "STATUS: nova-sprint card live.w1, epoch 15\n"}}, Reads: true, From: FromCards}, nil
	}
	return r
}

func (r *rig) ping(t *testing.T, nonce string) bus.Message {
	t.Helper()
	m, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Subject: PingPrefix + nonce, Body: PingText("ada", t0, nonce) + "\n"})
	require.NoError(t, err)
	return m
}

func (r *rig) recordText() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.records, "\n")
}

func (r *rig) streamEmpty(t *testing.T) {
	t.Helper()
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "nothing older stays pending")
	assert.Empty(t, fresh, "nothing older comes back")
}

// The finding of 2026-10-06: a session that starts is handed its stream's backlog (old deals,
// expired nonces, old coordinator notes) and reads it as current. On a start the daemon delivers
// one PRESENT turn and nothing older: her live queue, the seat, the newest coordinator note and
// one skipped line; every older message is acked superseded, on the record and on the bus log;
// the expired nonce is dropped, never answered.
func TestAStartedSessionGetsThePresentAndNeverTheBacklog(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	r.send(t, "ada", "card old.w1 dealt: inbox/old.w1~14/BRIEF.md", "card old.w1 dealt to you\n")
	r.ping(t, "n-old")
	r.send(t, "ada", "work old.w1", "old note: work old.w1 first\n")
	r.store.Advance(Window + time.Minute) // all of the above is past the challenge window now
	r.ping(t, "n-fresh")
	newest := r.send(t, "ada", "the live list", "newest note: QUEUE.json is the live list\n")
	r.run(t, 6)

	require.Len(t, r.delivered, 1, "one PRESENT turn, and nothing older after it")
	p := r.delivered[0]
	assert.True(t, strings.HasPrefix(p, PresentTextRule+" at "), p)
	assert.Contains(t, p, "You are bob; the seat is ada.")
	assert.Contains(t, p, "Your live queue, 1 card(s) on your row:\n- live.w1 working inbox/live.w1~15/BRIEF.md\n")
	assert.Contains(t, p, "Skipped: 1 deals, 1 pings, 1 notes, all superseded by the present at ")
	assert.Contains(t, p, "The newest note from the coordinator:\n\n"+Text(newest), "the newest coordinator note rides in the present, plain from the seat")
	for _, old := range []string{"old note", "card old.w1 dealt", "n-old", "n-fresh"} {
		assert.NotContains(t, p, old, "nothing older is delivered")
	}
	r.streamEmpty(t)

	ada := strings.Join(r.adaGot(t), "\n")
	assert.Contains(t, ada, DaemonPongSubject+": daemon-pong n-fresh", "a nonce inside the window is answered by the daemon")
	assert.NotContains(t, ada, "daemon-pong n-old", "an expired nonce is dropped, never answered")
	assert.Contains(t, ada, "friend bob: present at ", "the seat is told, on the bus log")
	assert.Contains(t, ada, "skipped 1 deals, 1 pings, 1 notes")
	assert.Contains(t, ada, "Every older message on her stream was marked superseded unread, superseded by the present at ")
	log, err := r.store.Range(context.Background(), bus.LogKey, "-", "+", 100)
	require.NoError(t, err)
	assert.Contains(t, log[len(log)-1].Message().Body, "superseded by the present at ", "the bus log shows the acks")

	rec := r.recordText()
	assert.Contains(t, rec, `acked=3 reason="superseded by the present at `)
	assert.Contains(t, rec, "queue=1 note="+newest.ID)
	assert.Equal(t, 1, r.last().Delivered, "the note the present carried is acked when its turn ends at exit 0")
	assert.FileExists(t, r.d.Dir+"/inbox/live.w1~15/BRIEF.md", "her live queue is her row, its brief written")
}

// A first delivery after the stale bound with none is the present, not the message: the gap
// supersedes what waited, and the newest coordinator note rides in the present.
func TestADeliveryAfterTheStaleBoundIsThePresent(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	var first, after bus.Message
	r.at[3] = func() { first = r.send(t, "ada", "first", "first note\n") }
	r.at[6] = func() {
		r.mu.Lock()
		r.now = r.now.Add(StaleAfter)
		r.mu.Unlock()
		r.send(t, "ada", "card late.w1 dealt: inbox/late.w1~15/BRIEF.md", "dealt\n")
		after = r.send(t, "ada", "after the gap", "after the gap\n")
	}
	r.run(t, 12)
	require.Len(t, r.delivered, 3, "%q", r.delivered)
	assert.Contains(t, r.delivered[0], PresentTextRule, "the start")
	assert.Contains(t, r.delivered[0], "Skipped: 0 deals, 0 pings, 0 notes")
	assert.Equal(t, Text(first), r.delivered[1], "inside the bound a message is delivered as it comes")
	assert.Contains(t, r.delivered[2], PresentTextRule, "after the bound: the present")
	assert.Contains(t, r.delivered[2], "Skipped: 1 deals, 0 pings, 0 notes")
	assert.Contains(t, r.delivered[2], Text(after))
	assert.NotContains(t, r.delivered[2], "card late.w1 dealt")
	r.streamEmpty(t)
}

// Her own "present" on the bus brings the present at once; the request is acked with the
// backlog and counted as nothing.
func TestAFriendsOwnPresentRequestBringsThePresent(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	r.at[3] = func() {
		r.send(t, "ada", "old", "an older note\n")
		_, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"bob"}, Subject: PresentSubject, Body: "present\n"})
		require.NoError(t, err)
	}
	r.run(t, 8)
	require.Len(t, r.delivered, 2, "%q", r.delivered)
	assert.Contains(t, r.delivered[1], PresentTextRule)
	assert.Contains(t, r.delivered[1], "Skipped: 0 deals, 0 pings, 0 notes", "the request is no note, and the newest note rides")
	assert.Contains(t, r.delivered[1], "an older note")
	r.streamEmpty(t)
}

// A new session id is a new session: the present again.
func TestANewSessionIDBringsThePresent(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	session := "s1"
	r.d.Session = func() string { r.mu.Lock(); defer r.mu.Unlock(); return session }
	r.at[3] = func() {
		r.mu.Lock()
		session = "s2"
		r.mu.Unlock()
		r.send(t, "ada", "card x.w1 dealt: inbox/x.w1~15/BRIEF.md", "dealt\n")
	}
	r.run(t, 8)
	require.Len(t, r.delivered, 2, "%q", r.delivered)
	assert.Contains(t, r.delivered[1], PresentTextRule)
	assert.Contains(t, r.delivered[1], "Skipped: 1 deals, 0 pings, 0 notes")
	assert.Contains(t, r.recordText(), "session: s2, was s1: the present is owed")
}

// A ping read after its window by the store's clock is dropped and never answered, outside a
// present too.
func TestAPingPastTheChallengeWindowIsDroppedNotAnswered(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	r.at[3] = func() { r.ping(t, "n-late"); r.store.Advance(Window) }
	r.run(t, 6)
	assert.NotContains(t, strings.Join(r.adaGot(t), "\n"), "daemon-pong n-late")
	assert.Contains(t, r.recordText(), "ping n-late dropped: sent ")
	assert.Equal(t, Quiet, r.last().Challenge, "the machine never saw it")
	r.streamEmpty(t)
}

func TestPlanPresentSupersedesAllButTheNewestNoteAndTheFreshPings(t *testing.T) {
	t.Parallel()
	at := func(m bus.Message, s int) bus.Entry {
		m.At = t0.Add(time.Duration(s) * time.Second)
		return bus.Entry{Entry: m.ID, Fields: m.Fields()}
	}
	backlog := []bus.Entry{
		at(bus.Message{ID: "1", From: "ada", To: []string{"bob"}, Subject: "card a.w1 dealt: x", Body: "x"}, 0),
		at(bus.Message{ID: "2", From: "ada", To: []string{"bob"}, Subject: "PING n1", Body: PingText("ada", t0, "n1")}, 0),
		at(bus.Message{ID: "3", From: "bob", To: []string{"bob"}, Subject: SessionCheckPrefix + "c1", Body: "SESSION CHECK c1"}, 590),
		at(bus.Message{ID: "4", From: "ada", To: []string{"bob"}, Subject: "note", Body: "older"}, 10),
		at(bus.Message{ID: "5", From: "cy", To: []string{"bob"}, Subject: "hi", Body: "from kin"}, 20),
		at(bus.Message{ID: "6", From: "ada", To: []string{"bob"}, Subject: "note", Body: "newest"}, 30),
		at(bus.Message{ID: "7", From: "bob", To: []string{"bob"}, Subject: "present", Body: "present"}, 40),
		at(bus.Message{ID: "8", From: "ada", To: []string{"bob"}, Subject: "PING n2", Body: PingText("ada", t0, "n2")}, 500),
		at(bus.Message{ID: "9", From: "ada", To: []string{"bob"}, Subject: "card b.w1 dealt: y", Body: "y"}, 50),
	}
	p := PlanPresent("bob", "ada", backlog, t0.Add(600*time.Second), Window)
	require.NotNil(t, p.Note)
	assert.Equal(t, "6", p.Note.Entry, "the newest from the seat that is no deal")
	require.Len(t, p.Fresh, 1)
	assert.Equal(t, "8", p.Fresh[0].Entry)
	assert.Equal(t, []string{"1", "2", "3", "4", "5", "7", "9"}, p.Superseded)
	assert.Equal(t, Skipped{Deals: 2, Pings: 2, Notes: 2}, p.Skipped, "a session check is never fresh; the request counts as nothing")
	assert.Equal(t, 1, p.Requests)
	assert.Nil(t, PlanPresent("bob", "", backlog, t0, Window).Note, "the seat unknown: no note is the coordinator's")
}

func TestAReportOnACardNoLongerHersNamesWhoHoldsItNow(t *testing.T) {
	t.Parallel()
	running := func() map[string]string { return map[string]string{"taken.w1": "cy"} }
	assert.Equal(t, "refused: card taken.w1 is not on her row, no longer hers; cy holds it now", NotHers("taken.w1", "taken.w1~15", "bob", running))
	assert.Contains(t, NotHers("gone.w1", "gone.w1~15", "bob", running), "no row the daemon reads says who holds it now")
	assert.Contains(t, NotHers("gone.w1", "gone.w1~15", "bob", nil), "no row the daemon reads says who holds it now")
}

// A superseded message whose ack was lost is handed in again by the claim: superseded again,
// never delivered (tla/Delivery.tla, the "reclaim" witness). One sent in the same second after
// the present is no such message, and is delivered.
func TestASupersededMessageHandedInAgainIsSupersededAgain(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	st := &lostAck{Fake: r.store, lose: 1}
	r.d.Store = st
	old := r.send(t, "ada", "card old.w1 dealt: x", "dealt\n")
	var after bus.Message
	r.at[3] = func() { after = r.send(t, "ada", "after", "sent after the present\n") }
	r.at[6] = func() { r.store.Advance(bus.ClaimAfter) }
	r.run(t, 12)
	require.Len(t, r.delivered, 2, "%q", r.delivered)
	assert.Contains(t, r.delivered[0], "Skipped: 1 deals")
	assert.Equal(t, Text(after), r.delivered[1])
	rec := r.recordText()
	assert.Contains(t, rec, "ack=failed after 0")
	assert.Contains(t, rec, "superseded id="+old.ID+` subject="card old.w1 dealt: x": superseded by the present at `)
	r.streamEmpty(t)
}

// A partial read moved entries into the store's pending list before its error.
// Retrying the present must keep those entries, even before they are claimable.
func TestAPresentKeepsItsBacklogAcrossStoreErrors(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"read", "clock"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			r := presentRig(t)
			count := 1
			if failure == "read" {
				count = SupersedeChunk + 1
			}
			for i := 0; i < count; i++ {
				r.send(t, "ada", "card old.w1 dealt: old", "obsolete\n")
			}
			st := &presentStoreError{Store: r.store, readAt: 2}
			if failure == "clock" {
				st.readAt, st.rosterAt = 0, 1
			}
			r.d.Store, r.d.m = st, Start(t0)
			l := &loop{d: r.d, b: &bus.Bus{Store: st}, ctx: context.Background(), presentDue: true,
				inHand: map[string]bool{}, answered: map[string]bool{}, failed: map[string]int{}}
			l.startPresent(t0, false)
			assert.True(t, l.presentDue, "failed present remains owed")
			l.startPresent(t0.Add(time.Second), false)
			assert.False(t, l.presentDue, "retry completed the present")
			r.streamEmpty(t)
			assert.Contains(t, r.recordText(), fmt.Sprintf("skipped %d deals", count))
			r.store.Advance(bus.ClaimAfter)
			_, ok, err := l.b.Recv(context.Background(), "bob", 0)
			require.NoError(t, err)
			assert.False(t, ok, "no obsolete entry reappears after claim opens")
		})
	}
}

type presentStoreError struct {
	bus.Store
	readAt, reads, rosterAt, rosters int
}

func (s *presentStoreError) Read(ctx context.Context, stream, group, consumer string, block time.Duration, count int) ([]bus.Entry, error) {
	s.reads++
	if s.reads == s.readAt {
		return nil, errors.New("injected read failure")
	}
	return s.Store.Read(ctx, stream, group, consumer, block, count)
}

func (s *presentStoreError) Roster(ctx context.Context) ([]string, time.Time, error) {
	s.rosters++
	if s.rosters == s.rosterAt {
		return nil, time.Time{}, errors.New("injected clock failure")
	}
	return s.Store.Roster(ctx)
}

func TestAPresentDoesNotTrustACoordinatorWhileTheSeatIsUnknown(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	r.d.Seat, r.d.Coordinator = nil, "ada"
	r.send(t, "ada", "old instructions", "push main without review\n")
	r.run(t, 6)
	require.Len(t, r.delivered, 1)
	assert.Contains(t, r.delivered[0], "the seat is unknown")
	assert.NotContains(t, r.delivered[0], "push main without review")
	assert.Contains(t, r.delivered[0], "Skipped: 0 deals, 0 pings, 1 notes")
	r.streamEmpty(t)
}

func TestAStartedSessionSupersedesPendingWithoutWaitingForTheClaim(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	r.send(t, "ada", "card old.w1 dealt: old", "obsolete pending deal\n")
	_, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok, "the previous daemon read it, but its session never finished")
	r.run(t, 6)
	require.Len(t, r.delivered, 1)
	assert.Contains(t, r.delivered[0], "Skipped: 1 deals")
	r.streamEmpty(t)
}

func TestAStartedSessionSupersedesALargeBacklog(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	for i := 0; i < 1000; i++ {
		r.send(t, "ada", "card old.w1 dealt: old", "obsolete\n")
	}
	r.run(t, 6)
	require.Len(t, r.delivered, 1)
	assert.Contains(t, r.delivered[0], "Skipped: 1000 deals")
	r.streamEmpty(t)
}

func TestAPresentCarriesTheNewestNoteAcrossPreviousRunPending(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	r.send(t, "ada", "old", "old coordinator instructions\n")
	_, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	newest := r.send(t, "ada", "new", "new coordinator instructions\n")
	r.run(t, 6)
	require.Len(t, r.delivered, 1)
	assert.Contains(t, r.delivered[0], Text(newest))
	assert.NotContains(t, r.delivered[0], "old coordinator instructions")
	assert.Contains(t, r.delivered[0], "Skipped: 0 deals, 0 pings, 1 notes")
	r.streamEmpty(t)
}

func TestAOneShotStartSupersedesTheNewestNoteToo(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	r.send(t, "ada", "old note", "obsolete coordinator note\n")
	r.d.m = Start(t0)
	l := &loop{d: r.d, b: r.bus, ctx: context.Background(), presentDue: true,
		inHand: map[string]bool{}, answered: map[string]bool{}, failed: map[string]int{}}
	l.startPresent(t0, false)
	r.streamEmpty(t)
	assert.Empty(t, r.delivered)
	assert.Contains(t, r.recordText(), "skipped 0 deals, 0 pings, 1 notes")
}

func TestTheDaemonRefusesAReportWhoseCardAnotherFriendHolds(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	row := &twinRow{}
	r.d.Held, r.d.Running = row.held, func() map[string]string { return map[string]string{"taken.w1": "cy"} }
	f := &finishes{}
	r.d.Finish = f.finish
	outboxReport(t, r.d.Dir, "taken.w1~15", "Verdict: LAND\nHead: 0123456789abcdef0123456789abcdef01234567\n")
	r.run(t, 3)
	assert.Empty(t, f.got(), "no finish is sent for a card outside her row")
	assert.Contains(t, r.recordText(), "refused: card taken.w1 is not on her row, no longer hers; cy holds it now")
}

func TestANewlyDiscoveredSessionAlsoGetsThePresent(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	session := ""
	r.d.Session = func() string { r.mu.Lock(); defer r.mu.Unlock(); return session }
	r.at[3] = func() {
		r.mu.Lock()
		session = "discovered"
		r.mu.Unlock()
		r.send(t, "ada", "card old.w1 dealt: old", "obsolete\n")
	}
	r.run(t, 8)
	require.Len(t, r.delivered, 2)
	assert.Contains(t, r.delivered[1], PresentTextRule)
	assert.Contains(t, r.delivered[1], "Skipped: 1 deals")
	r.streamEmpty(t)
}

func TestANonceWhoseAgeCannotBeReadIsNotAnswered(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	st := &presentStoreError{Store: r.store}
	r.d.Store = st
	r.at[3] = func() {
		r.ping(t, "clock-unread")
		r.store.Advance(Window)
		st.rosterAt = st.rosters + 2 // receive succeeds; the nonce's age lookup fails
	}
	r.run(t, 8)
	assert.NotContains(t, strings.Join(r.adaGot(t), "\n"), "daemon-pong clock-unread")
	assert.Contains(t, r.recordText(), "ping withheld: the store's clock could not be read")
	r.streamEmpty(t)
}

func TestAPresentDoesNotDeliverAnActedNoteWhoseAckWasLost(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	note := r.send(t, "ada", "already acted", "do not do this twice\n")
	_, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = r.bus.Stamp(context.Background(), "bob", bus.Acted, note.ID)
	require.NoError(t, err)
	r.run(t, 6)
	require.Len(t, r.delivered, 1)
	assert.NotContains(t, r.delivered[0], "do not do this twice")
	assert.Contains(t, r.delivered[0], "Skipped: 0 deals, 0 pings, 1 notes")
	r.streamEmpty(t)
}

func TestCurrentHoldersAreReadOnceForAllOldReportsInAPass(t *testing.T) {
	t.Parallel()
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			f := &finishes{}
			r.d.Finish = f.finish
			calls := 0
			r.d.Running = func() map[string]string { return map[string]string{"taken.w1": "old-owner"} }
			r.d.Holders = func(context.Context) (map[string]string, error) {
				calls++
				if failed {
					return nil, errors.New("injected holder-view failure")
				}
				return map[string]string{"taken.w1": "cy", "other.w1": "dana"}, nil
			}
			for _, job := range []string{"taken.w1~15", "other.w1~15"} {
				outboxReport(t, r.d.Dir, job, "Verdict: LAND\nHead: 0123456789abcdef0123456789abcdef01234567\n")
			}
			l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}
			l.outboxStep(t0)
			assert.Equal(t, 1, calls, "one view for two old reports")
			assert.Empty(t, f.got(), "no finish leaves her row")
			if failed {
				assert.Contains(t, r.recordText(), "the holder view could not be read: injected holder-view failure")
				assert.NotContains(t, r.recordText(), "old-owner holds it now", "a stale running list cannot replace the failed current view")
			} else {
				assert.Contains(t, r.recordText(), "cy holds it now")
				assert.Contains(t, r.recordText(), "dana holds it now")
			}
		})
	}
}
