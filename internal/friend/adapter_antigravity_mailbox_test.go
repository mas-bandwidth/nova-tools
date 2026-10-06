package friend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockedFS is a MapFS the delivery (in the daemon's turn goroutine) writes and Follow (in the
// loop) reads: every look under one lock.
type lockedFS struct {
	mu sync.Mutex
	m  fstest.MapFS
}

func (l *lockedFS) Open(name string) (fs.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.Open(name)
}

func (l *lockedFS) ReadFile(name string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.ReadFile(name)
}

func (l *lockedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.ReadDir(name)
}

func (l *lockedFS) Stat(name string) (fs.FileInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.Stat(name)
}

func (l *lockedFS) put(name string, f *fstest.MapFile) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.m[name] = f
}

// agHarness is Antigravity on the friend's machine for these tests: the language server
// running or not, the workspace's root conversations newest first, and send-message landing
// each message (<conversation>-1, -2, ...) in the named conversation's mailbox (or nowhere
// yet, while late is set), where nothing reads it unless the test marks it.
type agHarness struct {
	mu     sync.Mutex
	fs     *lockedFS
	closed bool
	late   bool
	rows   string
	n      map[string]int
	sentTo []string
	texts  map[string]string // message id to the text sent
}

func agBox(c string) string { return antigravityMailbox(c) }

func newAgHarness(at time.Time, conversations ...string) *agHarness {
	h := &agHarness{fs: &lockedFS{m: fstest.MapFS{}}, n: map[string]int{}, texts: map[string]string{}}
	var rows []string
	for _, c := range conversations {
		h.fs.m[agBox(c)+"/read.json"] = &fstest.MapFile{Data: []byte(`{}`), ModTime: at.Add(-time.Hour)}
		rows = append(rows, `{"conversation_id":"`+c+`","workspace_uris":"[\"file:///w/emma\"]"}`)
	}
	h.rows = "[" + strings.Join(rows, ",") + "]"
	return h
}

func (h *agHarness) run(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch name {
	case "ps":
		if h.closed {
			return "  root 1 /sbin/launchd\n", 0, nil
		}
		return agPS, 0, nil
	case "lsof":
		return "p87357\nn127.0.0.1:52570\n", 0, nil
	case "sqlite3":
		return h.rows, 0, nil
	}
	switch args[3] {
	case "get-conversation-metadata":
		return `{"response": {"conversationMetadata": {}}}`, 0, nil
	case "send-message":
		c := args[5]
		h.n[c]++
		id := fmt.Sprintf("%s-%d", c, h.n[c])
		h.sentTo = append(h.sentTo, c)
		h.texts[id] = args[6]
		if !h.late {
			h.land(c, id)
		}
		return `{"response": {"sendMessage": {}}}`, 0, nil
	}
	return "", 1, nil
}

// land puts message id in c's mailbox.
func (h *agHarness) land(c, id string) {
	h.fs.put(agBox(c)+"/"+id+".json", &fstest.MapFile{Data: []byte(`{"renderDetails":{"messageTitle":"nova-friend"}}`)})
}

// reads marks the ids read in c's read.json.
func (h *agHarness) reads(c string, ids ...string) {
	var marks []string
	for _, id := range ids {
		marks = append(marks, `"`+id+`":true`)
	}
	h.fs.put(agBox(c)+"/read.json", &fstest.MapFile{Data: []byte("{" + strings.Join(marks, ",") + "}")})
}

// got is every text sent into conversation c, in order.
func (h *agHarness) got(c string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for i := 1; i <= h.n[c]; i++ {
		out = append(out, h.texts[fmt.Sprintf("%s-%d", c, i)])
	}
	return out
}

func (h *agHarness) adapter(session, state string, out *strings.Builder, now func() time.Time) *Antigravity {
	a := &Antigravity{User: "emma", Dir: "/w/emma", Session: session, Run: h.run, Home: "/home", FS: h.fs, Now: now, State: state,
		Wait: func(context.Context) bool { return false }}
	if out != nil {
		a.Out = out
	}
	return a
}

func agDeliver(t *testing.T, a *Antigravity, texts ...string) {
	t.Helper()
	for _, text := range texts {
		exit, err := a.Deliver(context.Background(), text)
		require.NoError(t, err)
		require.Zero(t, exit)
	}
}

// The daemon delivers into Antigravity's mailbox at once, every time: a turn the message
// starts runs on and a second message queues in the mailbox, so nothing waits for the
// session to read (the finding of 2026-10-05 and 06: each delivery waited two minutes for
// the read, the session check and every other message held behind it, and three waits gave
// up a message already in her mailbox). The only answer that is not a delivery is a
// refusal (no app, no conversation, no token, a session down): a SessionRefused naming why,
// said once, every message pending, and never a deferral; the check reads route=mailbox and
// deferred=0.
func TestAntigravityDeliversIntoTheMailboxWithoutDeferring(t *testing.T) {
	t.Parallel()
	t.Run("the adapter answers once the message is in the mailbox", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "root-new")
		var out strings.Builder
		a := h.adapter("", t.TempDir(), &out, func() time.Time { return t0 })
		agDeliver(t, a, "first", "second, while the first is unread")
		assert.Equal(t, "antigravity: message root-new-1 in the mailbox of conversation root-new\nantigravity: message root-new-2 in the mailbox of conversation root-new\n", out.String())

		h.closed = true
		exit, err := a.Deliver(context.Background(), "third")
		assert.Equal(t, 1, exit)
		var refused SessionRefused
		require.ErrorAs(t, err, &refused)
		assert.Equal(t, "no antigravity language server is running: is Antigravity open?", refused.Reason)
		assert.Equal(t, "root-new", refused.Session)
		assert.False(t, errors.As(err, new(Deferred)), "the harness's refusal is never a deferral")
	})

	t.Run("through the daemon", func(t *testing.T) {
		t.Parallel()
		state := t.TempDir()
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t)
			h := newAgHarness(t0, "root-new")
			a := h.adapter("", state, nil, func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now })
			r.d.Deliver, r.d.Mailbox, r.d.Harness, r.passive = a, a, "antigravity", true
			r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
			r.d.Coordinator = "ada"
			r.send(t, "ada", "one", "x")
			r.at[5] = func() { r.send(t, "ada", "two", "y") }
			r.at[10] = func() { r.send(t, "ada", "three", "z") }
			var live string
			r.at[20] = func() {
				live = r.last().SessionLive
				h.mu.Lock()
				h.closed = true // Antigravity is closed: the harness refuses
				h.mu.Unlock()
				r.send(t, "ada", "four", "w")
			}
			r.run(t, 20+3*int(RecheckEvery/BeatEvery))

			assert.Equal(t, []string{"root-new", "root-new", "root-new"}, h.sentTo, "each in at once, none of them read")
			assert.Equal(t, "root-new", live, "the status says the conversation delivered into")
			all := strings.Join(r.records, "\n")
			assert.Equal(t, 3, strings.Count(all, "acked=true"), all)
			assert.NotContains(t, all, "deferred")
			assert.Equal(t, 1, strings.Count(all, `session=broken reason="no antigravity language server is running: is Antigravity open?"`), "the refusal is said once, by name: %s", all)
			pending, _, err := r.bus.Peek(context.Background(), "bob")
			require.NoError(t, err)
			require.Len(t, pending, 1, "the message the harness refused stays pending")
			assert.Equal(t, "four", pending[0].Message().Subject)

			fc := CheckFriend(context.Background(), "bob", CheckSeams{
				Now:          func() time.Time { return r.last().At },
				ReadStatus:   func(string) (Status, bool, error) { return r.last(), true, nil },
				ReadPresence: func(string) (PresenceStatus, bool, error) { return PresenceStatus{}, false, nil },
				ReadPong:     func(string) (Pong, bool, error) { return Pong{}, false, nil },
				ReadLog:      func(string) ([]string, error) { return r.records, nil },
				ReadWork:     func(string, string) (int, int, string, time.Time, error) { return 0, 0, "", time.Time{}, nil },
				HarnessDir:   func(string) (string, string, error) { return "antigravity", "/w/emma", nil },
			}, time.Hour, nil)
			assert.Equal(t, "mailbox", fc.Harness.Route)
			assert.Zero(t, fc.Harness.Deferred)
			assert.Contains(t, fc.Harness.Line(), "route=mailbox ")
			assert.Contains(t, fc.Harness.Line(), " deferred=0 ")
		})
	})
}

// A delivered message is never lost, and delivery follows the conversation that reads (the
// findings of 2026-10-06: deliveries went to the conversation named by --session while a
// second had taken 161 earlier in the day, nothing said which was live, and a delivery the
// bus acked stayed unread in a conversation no one read). Every delivery is in the ledger in
// the state directory, unread ones never dropped. A conversation that has left three
// deliveries unread past the check period and read nothing since stopped reading; delivery
// moves to a conversation the daemon delivered to that read since, said once, and every
// delivery the old one left unread is sent again into it, once, headed by the re-sent line.
// A conversation no delivery went to is never moved to, and delivery never moves back within
// the hold.
func TestTheLiveConversationFollowsWhoReads(t *testing.T) {
	t.Parallel()
	t.Run("the reader takes delivery and what was unread", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "A", "B")
		state := t.TempDir()
		var out strings.Builder
		now := t0.Add(-time.Hour)
		clock := func() time.Time { return now }
		agDeliver(t, h.adapter("B", state, &out, clock), "for B") // B was the named session earlier in the day
		now = t0
		a := h.adapter("A", state, &out, clock) // the daemon restarted with --session A
		agDeliver(t, a, "m-1")
		h.reads("A", "A-1")
		a.Follow(context.Background(), t0.Add(10*time.Second))
		now = t0.Add(20 * time.Second)
		agDeliver(t, a, "m-2", "m-3", "m-4") // A closes: none of them is read
		h.reads("B", "B-1")                  // B reads

		a.Follow(context.Background(), now.Add(AntigravityReadBound-time.Second))
		assert.Equal(t, "A", a.Live(), "inside the check period nothing moves")
		a.Follow(context.Background(), now.Add(AntigravityReadBound))
		assert.Equal(t, "A", a.Live(), "looked at most every AntigravityFollowEvery")
		at := now.Add(AntigravityReadBound - time.Second + AntigravityFollowEvery)
		a.Follow(context.Background(), at)
		assert.Equal(t, "B", a.Live())
		got := h.got("B")
		require.Len(t, got, 4, "for B, then m-2..m-4 once each: %q", got)
		for i, text := range got[1:] {
			id := fmt.Sprintf("A-%d", i+2)
			assert.Equal(t, "re-sent: "+id+" was delivered to A at "+now.UTC().Format(time.RFC3339)+" and not read\nm-"+fmt.Sprint(i+2), text)
		}
		a.Follow(context.Background(), at.Add(AntigravityFollowEvery))
		a.Follow(context.Background(), at.Add(2*AntigravityFollowEvery))
		assert.Len(t, h.got("B"), 4, "sent again once, never twice")
		said := out.String()
		assert.Equal(t, 1, strings.Count(said, "antigravity: live conversation is now B (the named one stopped reading)\n"), said)
		assert.Contains(t, said, "antigravity: message A-1 read by conversation A\n")
		assert.Contains(t, said, "antigravity: message B-1 read by conversation B\n")
		assert.Contains(t, said, "antigravity: message A-2, unread in conversation A, sent again into conversation B\n")

		agDeliver(t, a, "next")
		assert.Equal(t, "B", h.sentTo[len(h.sentTo)-1], "the next delivery goes to the conversation that reads")
		restarted := h.adapter("A", state, nil, clock)
		assert.Equal(t, "B", restarted.Live(), "the move is in the state directory: a restart keeps it")
		assert.Equal(t, "C", h.adapter("C", state, nil, clock).Live(), "a move holds only for the session it was made from")

		fc := CheckFriend(context.Background(), "emma", CheckSeams{
			Now: func() time.Time { return t0 },
			ReadStatus: func(string) (Status, bool, error) {
				return Status{At: t0, Harness: "antigravity", SessionLive: a.Live()}, true, nil
			},
			ReadPresence: func(string) (PresenceStatus, bool, error) { return PresenceStatus{}, false, nil },
			ReadPong:     func(string) (Pong, bool, error) { return Pong{}, false, nil },
			HarnessDir:   func(string) (string, string, error) { return "antigravity", "/w/emma", nil },
		}, time.Hour, nil)
		assert.Contains(t, fc.Harness.Line(), " session_live=B queued=-")

		// B stops at once: delivery never bounces back within the hold
		now = at
		agDeliver(t, a, "b-1", "b-2", "b-3")
		h.reads("A", "A-1", "A-2")
		a.Follow(context.Background(), at.Add(AntigravityReadBound+AntigravityFollowEvery))
		assert.Equal(t, "B", a.Live(), "no move within AntigravitySwitchHold of the last")
	})
	t.Run("never to a conversation nothing was delivered to", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "A", "C")
		var out strings.Builder
		a := h.adapter("A", t.TempDir(), &out, func() time.Time { return t0 })
		agDeliver(t, a, "1", "2", "3")
		h.fs.put(agBox("C")+"/read.json", &fstest.MapFile{Data: []byte(`{"x":true}`), ModTime: t0.Add(time.Minute)}) // another conversation, someone else's
		a.Follow(context.Background(), t0.Add(time.Hour))
		assert.Equal(t, "A", a.Live())
		assert.NotContains(t, out.String(), "live conversation")
	})
	t.Run("an unread delivery is never dropped", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "A")
		state := t.TempDir()
		a := h.adapter("A", state, nil, func() time.Time { return t0 })
		for i := range AntigravityKeptRead + 10 {
			agDeliver(t, a, fmt.Sprint(i))
		}
		var l AntigravityLedger
		found, err := read(filepath.Join(state, AntigravityLedgerFile), &l)
		require.NoError(t, err)
		require.True(t, found)
		assert.Len(t, l.Deliveries, AntigravityKeptRead+10, "every unread delivery, with its text, in the state directory")
		assert.Equal(t, "0", l.Deliveries[0].Text)
	})
}

// While the session reads nothing it is down, and delivery waits: a delivery unread past the
// check period with nothing read since refuses the next one (a SessionRefused naming it, so
// the daemon keeps every message pending on the bus and says session=broken once), until a
// delivery is read (the finding of 2026-10-06: after the first proof, a closed window took
// every message sent while it was down).
func TestASessionThatReadsNothingKeepsMessagesPending(t *testing.T) {
	t.Parallel()
	h := newAgHarness(t0, "A")
	now := t0
	a := h.adapter("A", t.TempDir(), nil, func() time.Time { return now })
	agDeliver(t, a, "first")
	now = t0.Add(AntigravityReadBound - time.Second)
	agDeliver(t, a, "inside the check period")
	now = t0.Add(AntigravityReadBound)
	exit, err := a.Deliver(context.Background(), "the window is closed")
	assert.Equal(t, 1, exit)
	var refused SessionRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "the session is down: conversation A has read nothing delivered since "+t0.UTC().Format(time.RFC3339)+" (2 unread, the check period is 5m0s)", refused.Reason)
	assert.False(t, errors.As(err, new(Deferred)))
	assert.Len(t, h.got("A"), 2, "nothing went in")

	h.reads("A", "A-1", "A-2") // she comes back and reads
	agDeliver(t, a, "the window is open")
	assert.Equal(t, "the window is open", h.got("A")[2])
}

// A message agentapi took is delivered to that conversation even when its file lands after
// the land budget: answered 0, kept in the ledger, its id read off the mailbox when it lands,
// and its read said as any other; it is never sent a second time.
func TestAMessageThatLandsLateIsDeliveredOnce(t *testing.T) {
	t.Parallel()
	h := newAgHarness(t0, "A")
	h.late = true
	var out strings.Builder
	state := t.TempDir()
	a := h.adapter("A", state, &out, func() time.Time { return t0 })
	agDeliver(t, a, "slow")
	assert.Equal(t, []string{"A"}, h.sentTo, "sent once")
	assert.Equal(t, "antigravity: agentapi took a message for conversation A and it is not in the mailbox after 30s; kept as delivered, its id read when it lands\n", out.String())

	h.land("A", "A-1")
	h.reads("A", "A-1")
	a.Follow(context.Background(), t0.Add(time.Minute))
	assert.Contains(t, out.String(), "antigravity: message A-1 in the mailbox of conversation A (landed late)\nantigravity: message A-1 read by conversation A\n")
	var l AntigravityLedger
	_, err := read(filepath.Join(state, AntigravityLedgerFile), &l)
	require.NoError(t, err)
	require.Len(t, l.Deliveries, 1)
	assert.Equal(t, "A-1", l.Deliveries[0].ID)
	assert.False(t, l.Deliveries[0].ReadAt.IsZero())
	assert.Equal(t, []string{"A"}, h.sentTo, "never sent a second time")
}

// An antigravity friend's REPORT.md is finished by the daemon whatever her session is doing:
// the outbox pass runs each reconcile, beside a batch turn under way and never inside one
// (the finding of 2026-10-06: her reports were finished by the coordinator's stopgap). The
// general finisher (outbox.go, TestTheDaemonFinishesAReportItDidNotStage) reads every
// harness's outbox; this holds it for a session that is never free.
func TestAnAntigravityReportIsFinishedWhileTheSessionIsBusy(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Harness = "antigravity"
	r.hold, r.releaseAt = make(chan struct{}), 1<<30 // the turn runs on for the whole run
	row := &twinRow{}
	r.d.Held = row.held
	f := &finishes{}
	r.d.Finish = f.finish
	const head = "0123456789abcdef0123456789abcdef01234567"
	land := workCard("emma-land.w1", "working")
	inboxJob(t, r.d.Dir, land.Job, land.Brief)
	row.set(land)
	r.send(t, "ada", "a long task", "work on it")
	r.at[3] = func() { outboxReport(t, r.d.Dir, land.Job, "Verdict: LAND\nHead: "+head+"\n\nDone while busy.\n") }
	r.run(t, 6)
	// Run does not join a turn: the held one ends with the run's context, and its token on the
	// gate says its delivery was made and its result queued (without it, Run can return before
	// the turn's goroutine has delivered at all)
	<-r.gate

	r.mu.Lock()
	delivered := len(r.delivered)
	r.mu.Unlock()
	assert.Equal(t, 1, delivered, "one turn, still under way")
	got := f.got()
	require.Len(t, got, 1, "%v", r.records)
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "emma-land.w1@1", "--epoch", "15", "--head", head, "--branch", "sprint/emma-land.w1.g1.e15", "--report", "friend bob LAND: Done while busy."}, got[0])
}
