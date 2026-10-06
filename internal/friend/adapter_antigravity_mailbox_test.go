package friend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
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
// each message (m-1, m-2, ...) in the named conversation's mailbox, where nothing reads it
// unless the test marks it.
type agHarness struct {
	mu      sync.Mutex
	fs      *lockedFS
	closed  bool
	rows    string
	sends   int
	sentTo  []string
	appTime time.Time
}

func agBox(c string) string { return antigravityMailbox(c) }

func newAgHarness(at time.Time, conversations ...string) *agHarness {
	h := &agHarness{fs: &lockedFS{m: fstest.MapFS{}}, appTime: at}
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
		h.sends++
		id := fmt.Sprintf("m-%d", h.sends)
		h.sentTo = append(h.sentTo, args[5])
		h.fs.put(agBox(args[5])+"/"+id+".json", &fstest.MapFile{Data: []byte(`{"renderDetails":{"messageTitle":"nova-friend"}}`)})
		return `{"response": {"sendMessage": {}}}`, 0, nil
	}
	return "", 1, nil
}

// wrote marks conversation c's transcript written at at: it is taking turns.
func (h *agHarness) wrote(c string, at time.Time) {
	h.fs.put(path.Join(AntigravityData, "brain", c, ".system_generated", "logs", "transcript.jsonl"), &fstest.MapFile{Data: []byte("{}\n"), ModTime: at})
}

// reads marks the ids read in c's read.json, written at at.
func (h *agHarness) reads(c string, at time.Time, ids ...string) {
	var marks []string
	for _, id := range ids {
		marks = append(marks, `"`+id+`":true`)
	}
	h.fs.put(agBox(c)+"/read.json", &fstest.MapFile{Data: []byte("{" + strings.Join(marks, ",") + "}"), ModTime: at})
}

func (h *agHarness) adapter(session string, out *strings.Builder, now func() time.Time) *Antigravity {
	a := &Antigravity{User: "emma", Dir: "/w/emma", Session: session, Run: h.run, Home: "/home", FS: h.fs, Now: now,
		Wait: func(context.Context) bool { return false }}
	if out != nil {
		a.Out = out
	}
	return a
}

// The daemon delivers into Antigravity's mailbox at once, every time: a turn the message
// starts runs on and a second message queues in the mailbox, so nothing waits for the
// session to read (the finding of 2026-10-05 and 06: each delivery waited two minutes for
// the read, the session check and every other message held behind it, and three waits gave
// up a message already in her mailbox). The only answer that is not a delivery is the
// harness's own refusal (no app, no conversation, no token): a SessionRefused naming why,
// said once, every message pending, and never a deferral; the check reads route=mailbox and
// deferred=0.
func TestAntigravityDeliversIntoTheMailboxWithoutDeferring(t *testing.T) {
	t.Parallel()
	t.Run("the adapter answers once the message is in the mailbox", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "root-new")
		var out strings.Builder
		a := h.adapter("", &out, func() time.Time { return t0 })
		for _, text := range []string{"first", "second, while the first is unread"} {
			exit, err := a.Deliver(context.Background(), text)
			require.NoError(t, err)
			assert.Zero(t, exit)
		}
		assert.Equal(t, "antigravity: message m-1 in the mailbox of conversation root-new\nantigravity: message m-2 in the mailbox of conversation root-new\n", out.String())

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
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t)
			h := newAgHarness(t0, "root-new")
			a := h.adapter("", nil, func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now })
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

// The daemon follows the conversation that reads (the finding of 2026-10-06: deliveries went
// to the conversation named by --session, a second conversation had taken 161 of them earlier
// in the day, and nothing said which one was live). Each read is said once. A conversation in
// a long turn writes its transcript and keeps delivery, however many messages wait in its
// mailbox. One that has left three deliveries unread past the bound and written nothing
// since stopped reading: delivery moves to the newest root conversation of the workspace that
// has written since, said once, and the status and the check say it. A session answer (a
// pong) after a delivery the live conversation has not read came from another conversation:
// it is still an answer, and delivery moves to that conversation at once.
func TestTheLiveConversationFollowsWhoReads(t *testing.T) {
	t.Parallel()
	deliver := func(t *testing.T, a *Antigravity, n int) {
		t.Helper()
		for range n {
			exit, err := a.Deliver(context.Background(), "x")
			require.NoError(t, err)
			require.Zero(t, exit)
		}
	}
	t.Run("a conversation that stopped reading", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "root-new", "named")
		var out strings.Builder
		now := t0
		a := h.adapter("named", &out, func() time.Time { return now })
		deliver(t, a, 4) // m-1 .. m-4 into the named conversation
		h.reads("named", t0, "m-1")
		h.wrote("named", t0.Add(-time.Minute))
		h.wrote("root-new", t0.Add(2*time.Minute)) // the friend works in another conversation now

		a.Follow(context.Background(), t0.Add(AntigravityReadBound-5*time.Second), time.Time{})
		assert.Equal(t, "named", a.Live(), "inside the bound nothing moves")
		a.Follow(context.Background(), t0.Add(AntigravityReadBound), time.Time{})
		assert.Equal(t, "named", a.Live(), "looked at most every AntigravityFollowEvery")
		a.Follow(context.Background(), t0.Add(AntigravityReadBound-5*time.Second+AntigravityFollowEvery), time.Time{})
		assert.Equal(t, "root-new", a.Live())
		assert.Equal(t, "antigravity: message m-1 read by conversation named\nantigravity: live conversation is now root-new (the named one stopped reading)\n",
			strings.TrimPrefix(out.String(), "antigravity: message m-1 in the mailbox of conversation named\nantigravity: message m-2 in the mailbox of conversation named\nantigravity: message m-3 in the mailbox of conversation named\nantigravity: message m-4 in the mailbox of conversation named\n"))

		deliver(t, a, 1)
		assert.Equal(t, "root-new", h.sentTo[len(h.sentTo)-1], "the next delivery goes to the conversation that reads")
		a.Follow(context.Background(), t0.Add(time.Hour), time.Time{})
		assert.Equal(t, 1, strings.Count(out.String(), "live conversation is now"), "said once")

		fc := CheckFriend(context.Background(), "emma", CheckSeams{
			Now: func() time.Time { return t0 },
			ReadStatus: func(string) (Status, bool, error) {
				return Status{At: t0, Harness: "antigravity", SessionLive: a.Live()}, true, nil
			},
			ReadPresence: func(string) (PresenceStatus, bool, error) { return PresenceStatus{}, false, nil },
			ReadPong:     func(string) (Pong, bool, error) { return Pong{}, false, nil },
			HarnessDir:   func(string) (string, string, error) { return "antigravity", "/w/emma", nil },
		}, time.Hour, nil)
		assert.True(t, strings.HasSuffix(fc.Harness.Line(), " session_live=root-new"), fc.Harness.Line())
	})
	t.Run("a conversation in a long turn keeps delivery", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "root-new", "named")
		var out strings.Builder
		a := h.adapter("named", &out, func() time.Time { return t0 })
		deliver(t, a, 5)
		h.wrote("named", t0.Add(3*time.Hour)) // its turn runs on: it reads the mailbox when it ends
		h.wrote("root-new", t0.Add(3*time.Hour))
		a.Follow(context.Background(), t0.Add(3*time.Hour), t0.Add(2*time.Hour))
		assert.Equal(t, "named", a.Live())
		assert.NotContains(t, out.String(), "live conversation")
	})
	t.Run("an answer from another conversation", func(t *testing.T) {
		t.Parallel()
		h := newAgHarness(t0, "root-new", "named", "root-old")
		var out strings.Builder
		a := h.adapter("named", &out, func() time.Time { return t0 })
		deliver(t, a, 1) // the session check, unread in the named conversation
		h.wrote("root-old", t0.Add(time.Minute))
		at := t0.Add(AntigravityReadBound)
		a.Follow(context.Background(), at, time.Time{})
		assert.Equal(t, "named", a.Live(), "one unread delivery, and no answer: nothing moves")
		a.Follow(context.Background(), at.Add(AntigravityFollowEvery), t0.Add(2*time.Minute))
		assert.Equal(t, "root-old", a.Live(), "the newest conversation of the workspace that wrote since")
		assert.Contains(t, out.String(), "antigravity: live conversation is now root-old (the named one stopped reading)\n")
	})
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

	r.mu.Lock()
	delivered := len(r.delivered)
	r.mu.Unlock()
	assert.Equal(t, 1, delivered, "one turn, still under way")
	got := f.got()
	require.Len(t, got, 1, "%v", r.records)
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "emma-land.w1@1", "--epoch", "15", "--head", head, "--branch", "sprint/emma-land.w1.g1.e15", "--report", "friend bob LAND: Done while busy."}, got[0])
}
