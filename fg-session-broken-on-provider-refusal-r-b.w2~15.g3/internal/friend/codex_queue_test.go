package friend

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uuid7 is a UUIDv7 made at t, as Codex makes its queued submission ids.
func uuid7(t time.Time, seq int) string {
	h := fmt.Sprintf("%012x", t.UnixMilli())
	return fmt.Sprintf("%s-%s-7%03x-8000-000000000000", h[:8], h[8:12], seq)
}

// codexApp is the Codex app-server's queue for one thread, and codex queue adding to it: the
// open chat is mid-turn, so nothing leaves the queue unless the test takes it.
type codexApp struct {
	mu        sync.Mutex
	now       time.Time
	queue     []CodexQueued
	seq       int
	ops       []string // queue, delete <id>, in order
	queuedN   int      // codex queue runs that took the text
	queueFail bool     // codex queue (and resume) fail
	listErr   error    // the app-server does not answer a read
	deleteErr error    // the app refuses a withdrawal
	gone      []string // ids the session takes between a read and a withdrawal
}

func (f *codexApp) Call(_ context.Context, method string, params, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var reply any
	switch method {
	case "thread/queue/list":
		if f.listErr != nil {
			return f.listErr
		}
		var data []map[string]any
		for _, q := range f.queue {
			data = append(data, map[string]any{"id": q.ID, "input": []map[string]string{{"type": "text", "text": q.Text}}})
		}
		reply = map[string]any{"data": data, "nextCursor": nil}
	case "thread/queue/delete":
		if f.deleteErr != nil {
			return f.deleteErr
		}
		id := params.(map[string]string)["queuedSubmissionId"]
		f.ops = append(f.ops, "delete "+id)
		f.queue = slices.DeleteFunc(f.queue, func(q CodexQueued) bool { return slices.Contains(f.gone, q.ID) })
		n := len(f.queue)
		f.queue = slices.DeleteFunc(f.queue, func(q CodexQueued) bool { return q.ID == id })
		reply = map[string]bool{"deleted": len(f.queue) < n}
	default:
		return fmt.Errorf("unexpected %s", method)
	}
	raw, _ := json.Marshal(reply)
	return json.Unmarshal(raw, result)
}

func (f *codexApp) Close() error { return nil }

// add puts text on the queue as queued at.
func (f *codexApp) add(at time.Time, text string) string {
	f.seq++
	id := uuid7(at, f.seq)
	f.queue = append(f.queue, CodexQueued{ID: id, Text: text})
	return id
}

func (f *codexApp) run(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queueFail || args[0] != "queue" {
		return "", 1, nil
	}
	f.queuedN++
	f.ops = append(f.ops, "queue")
	f.add(f.now, args[4])
	return "", 0, nil
}

func (f *codexApp) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, q := range f.queue {
		out = append(out, q.Text)
	}
	return out
}

func (f *codexApp) codex(record *strings.Builder) *Codex {
	c := &Codex{Dir: "/w/stella", Session: "thread-1", Home: "/codex", Program: "codex", Run: f.run,
		Held: func(string) bool { return true },
		App:  func(context.Context) (CodexAppServer, error) { return f, nil },
		Now:  func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }}
	if record != nil {
		c.Out = record
	}
	return c
}

func pongCmd(nonce string) string {
	return "/Users/x/.local/bin/nova-friend pong --as stella --nonce " + nonce + " --state-dir /s --redis 127.0.0.1:6381"
}

func idleWake(nonce string) string {
	return "Run this now, first, exactly as written: " + pongCmd(nonce) + "\nThen read on.\n\nnova-friend: you hold 2 cards (a, b) and your session has written nothing for 10m0s; continue the oldest, a.\n"
}

func codexDeliver(t *testing.T, c *Codex, text string) {
	t.Helper()
	exit, err := c.Deliver(context.Background(), text)
	require.NoError(t, err)
	require.Zero(t, exit)
}

// A delivery while her Codex chat is mid-turn cannot be steered: the turn runs in the Codex
// app's own server, which takes no request from outside, and the app holds what is queued
// until the turn ends (the owner's screenshot of 2026-10-06 2:58 PM ET: three queued pong
// requests for nonce d6i4h2 and a fourth in the composer). So her queue holds one request
// for a pong of each kind: the new one is queued first, then an older request of its kind is
// withdrawn; the same request still unread is not queued twice; a queued message that
// carries anything else is never withdrawn; a withdrawal the app refuses, or one the session
// took first, is said.
func TestACodexDeliveryDuringATurnIsNotQueuedTwice(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	var record strings.Builder
	c := f.codex(&record)
	note := "Run this now, first, exactly as written: " + pongCmd("d6i4h2") + "\nThen read on.\n\nnova-friend: 1 message(s) for you, oldest first, in one turn; take each in order.\n\n=== message 1 of 1 ===\nRECV OK id=m1 from=rowan\n\ncard notes\n"
	wake := WakeTurnText(pongCmd("d6i4h2"))
	f.add(t0.Add(-20*time.Minute), wake)
	f.add(t0.Add(-15*time.Minute), note)
	f.add(t0.Add(-10*time.Minute), idleWake("d6i4h2"))
	oldCheck := SessionCheckText("p1", pongCmd("p1"), "rowan")
	old := f.add(t0.Add(-5*time.Minute), oldCheck)

	check := SessionCheckText("k2p9xz", pongCmd("k2p9xz"), "rowan")
	codexDeliver(t, c, check)
	assert.Equal(t, []string{wake, note, idleWake("d6i4h2"), check}, f.texts(), "the older session check withdrawn; the wakes, the other kind, and the card note kept")
	assert.Equal(t, []string{"queue", "delete " + old}, f.ops, "the new one in first, then the old one out")
	n, ok := c.Queued()
	assert.True(t, ok)
	assert.Equal(t, 4, n)
	assert.Contains(t, record.String(), "codex: withdrew the queued pong request p1 ("+old+") from thread thread-1: superseded by the session check k2p9xz\n")

	f.now = t0.Add(10 * time.Minute)
	codexDeliver(t, c, check)
	assert.Equal(t, 1, f.queuedN, "the same check, unread, is not queued twice")
	assert.Contains(t, record.String(), "codex: the session check k2p9xz is still unread in the queue of thread thread-1 since "+t0.UTC().Format(time.RFC3339)+"; not queued twice\n")

	codexDeliver(t, c, WakeTurnText(pongCmd("q7r1aa")))
	assert.Equal(t, []string{note, check, WakeTurnText(pongCmd("q7r1aa"))}, f.texts(), "the older wakes withdrawn; the session check stands")

	f.deleteErr = errors.New("not allowed")
	codexDeliver(t, c, idleWake("z9"))
	assert.Contains(t, record.String(), "codex: the queued pong request q7r1aa ("+uuid7(f.now, 6)+") not withdrawn from thread thread-1: not allowed; marked superseded\n")

	f.deleteErr = nil
	f.gone = []string{uuid7(f.now, 6), uuid7(f.now, 7)} // she takes them while the next goes in
	codexDeliver(t, c, WakeTurnText(pongCmd("w8")))
	assert.Contains(t, record.String(), "codex: the queued pong request z9 ("+uuid7(f.now, 7)+") was taken before it could be withdrawn: superseded by the wake w8\n")
}

// A session check and a wake are two series of nonces (the presence's and the coordinator's
// challenge): a request of one kind never withdraws one of the other.
func TestAWakeNeverWithdrawsASessionCheck(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	c := f.codex(nil)
	check := SessionCheckText("p1", pongCmd("p1"), "rowan")
	codexDeliver(t, c, check)
	codexDeliver(t, c, WakeTurnText(pongCmd("c1")))
	codexDeliver(t, c, idleWake("c2"))
	assert.Equal(t, []string{check, idleWake("c2")}, f.texts(), "the wake c2 withdrew the wake c1, never the check")
	check2 := SessionCheckText("p2", pongCmd("p2"), "rowan")
	codexDeliver(t, c, check2)
	assert.Equal(t, []string{idleWake("c2"), check2}, f.texts(), "the check p2 withdrew the check p1, never the wake")
}

// The new request goes in before the old one goes out: a codex queue that fails leaves the
// old request standing, and nothing is withdrawn.
func TestAFailedCodexQueueLeavesTheOldRequestStanding(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	c := f.codex(nil)
	check := SessionCheckText("p1", pongCmd("p1"), "rowan")
	codexDeliver(t, c, check)
	f.queueFail = true
	_, err := c.Deliver(context.Background(), SessionCheckText("p2", pongCmd("p2"), "rowan"))
	var d Deferred
	require.ErrorAs(t, err, &d)
	assert.Equal(t, []string{check}, f.texts())
	assert.Equal(t, []string{"queue"}, f.ops, "no withdrawal")
	n, ok := c.Queued()
	assert.True(t, ok)
	assert.Equal(t, 1, n)
}

// A queue that cannot be read leaves its length unknown, never a stale number: an app-server
// that does not answer (said once while it stands), and no app-server socket at all; the
// delivery is queued as it comes either way.
func TestACodexQueueNotReadIsNotKnown(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	var record strings.Builder
	c := f.codex(&record)
	codexDeliver(t, c, "RECV OK id=1 from=rowan\n\nhello\n")
	_, ok := c.Queued()
	require.True(t, ok)

	f.listErr = errors.New("connection reset")
	codexDeliver(t, c, "RECV OK id=2 from=rowan\n\nagain\n")
	codexDeliver(t, c, "RECV OK id=3 from=rowan\n\nand again\n")
	_, ok = c.Queued()
	assert.False(t, ok, "a read that failed: not known")
	assert.Equal(t, 1, strings.Count(record.String(), "codex queue of thread thread-1 not read: connection reset; queued as it comes\n"), "said once while it stands")
	assert.Equal(t, 3, f.queuedN)

	f.listErr = nil
	codexDeliver(t, c, "RECV OK id=4 from=rowan\n\nback\n")
	_, ok = c.Queued()
	require.True(t, ok)
	c.App, c.Home = nil, t.TempDir() // no app-server socket here
	codexDeliver(t, c, "RECV OK id=5 from=rowan\n\ngone\n")
	_, ok = c.Queued()
	assert.False(t, ok, "no socket: not known")
}

// One session check is in flight at a time in her Codex queue: asked again every ten
// minutes while it stands unread, it is not queued again; once the session has taken it, the
// next ask is queued; one left unread past CodexCheckRequeue, a recheck under the hour's
// re-ask, is withdrawn and queued afresh by the re-ask (unread at 59 minutes it stands, at 61
// it is queued again), so the check is never held two hours.
func TestOneSessionCheckInFlightForCodex(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	c := f.codex(nil)
	check := SessionCheckText("n1", pongCmd("n1"), "rowan")
	for i := range 6 {
		f.now = t0.Add(time.Duration(i) * SessionQuiet)
		codexDeliver(t, c, check)
	}
	f.now = t0.Add(59 * time.Minute)
	codexDeliver(t, c, check)
	assert.Equal(t, 1, f.queuedN, "asked seven times inside 59 minutes, queued once")
	assert.Len(t, f.texts(), 1)

	f.now = t0.Add(61 * time.Minute)
	codexDeliver(t, c, check)
	assert.Equal(t, 2, f.queuedN, "unread at 61 minutes: the re-ask queues it afresh")
	assert.Equal(t, []string{check}, f.texts(), "the old one withdrawn, one in flight")

	f.mu.Lock()
	f.queue = nil // her turn ended: she took it
	f.mu.Unlock()
	f.now = t0.Add(70 * time.Minute)
	codexDeliver(t, c, check)
	assert.Equal(t, 3, f.queuedN, "taken, then asked again: queued again")
	assert.Equal(t, ReaskAfter-RecheckEvery, CodexCheckRequeue)
}

func TestPongRequestReadsWhatADeliveryAsksFor(t *testing.T) {
	t.Parallel()
	for text, want := range map[string][3]any{
		SessionCheckText("a1", pongCmd("a1"), "rowan"): {PongCheck, "a1", true},
		WakeTurnText(pongCmd("b2")):                    {PongWake, "b2", true},
		idleWake("c3"):                                 {PongWake, "c3", true},
		"Run this now, first, exactly as written: " + pongCmd("d4") + "\nThen read on.\n\nnova-friend: 2 sprint card(s) dealt to you are in your inbox:\n": {PongWake, "d4", false},
		"Run this now, first, exactly as written: " + pongCmd("e5") + "\nThen read on.\n\nRECV OK id=1 from=rowan":                                         {PongWake, "e5", false},
		"RECV OK id=1 from=rowan\n\nhello\n": {"", "", false},
	} {
		kind, nonce, only := PongRequest(text)
		assert.Equal(t, want, [3]any{kind, nonce, only}, text)
	}
	at, ok := UUIDv7Time("01a1128d-e981-7e93-9b36-0c7738cca0df")
	require.True(t, ok)
	assert.Equal(t, "2026-10-06T18:50:00Z", at.UTC().Truncate(time.Minute).Format(time.RFC3339))
	_, ok = UUIDv7Time("thread-1")
	assert.False(t, ok)
}

// The app-server client speaks JSON-RPC over a WebSocket: the handshake checked, its frames
// masked, notifications and pings passed over until its answer (net.Pipe: no socket).
func TestTheCodexAppServerClientSpeaksJSONRPCOverAWebSocket(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	t.Cleanup(func() {
		assert.NoError(t, client.Close())
		assert.NoError(t, server.Close())
	})
	go func() {
		r := bufio.NewReader(server)
		req, err := http.ReadRequest(r)
		if err != nil {
			return
		}
		sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(server, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
		out := make(chan []byte, 64) // net.Pipe is synchronous: the server writes beside its reads
		defer close(out)
		go func() {
			for b := range out {
				_, _ = server.Write(b)
			}
		}()
		send := func(op byte, v []byte) {
			frame := []byte{0x80 | op}
			if len(v) < 126 {
				frame = append(frame, byte(len(v)))
			} else {
				frame = binary.BigEndian.AppendUint16(append(frame, 126), uint16(len(v)))
			}
			out <- append(frame, v...)
		}
		for {
			var h [2]byte
			if _, err := io.ReadFull(r, h[:]); err != nil {
				return
			}
			if h[1]&0x80 == 0 {
				return // a client frame must be masked
			}
			n := int(h[1] & 0x7f)
			if n == 126 {
				var b [2]byte
				_, _ = io.ReadFull(r, b[:])
				n = int(binary.BigEndian.Uint16(b[:]))
			}
			var mask [4]byte
			_, _ = io.ReadFull(r, mask[:])
			data := make([]byte, n)
			_, _ = io.ReadFull(r, data)
			for i := range data {
				data[i] ^= mask[i%4]
			}
			if h[0]&0x0f != 1 {
				continue
			}
			var msg struct {
				ID     *int           `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			_ = json.Unmarshal(data, &msg)
			if msg.ID == nil {
				continue
			}
			send(0x9, []byte("ping"))
			send(0x1, []byte(`{"method":"thread/queue/changed","params":{"threadId":"t"}}`))
			var result string
			switch msg.Method {
			case "initialize":
				result = `{"userAgent":"fake"}`
			case "thread/queue/list":
				result = `{"data":[{"id":"q1","input":[{"type":"text","text":"SESSION CHECK n1"}]}],"nextCursor":null}`
			case "thread/queue/delete":
				result = fmt.Sprintf(`{"deleted":%v}`, msg.Params["queuedSubmissionId"] == "q1")
			default:
				send(0x1, fmt.Appendf(nil, `{"id":%d,"error":{"code":-32600,"message":"thread not found: t"}}`, *msg.ID))
				continue
			}
			send(0x1, fmt.Appendf(nil, `{"id":%d,"result":%s}`, *msg.ID, result))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := NewCodexAppServer(ctx, client)
	require.NoError(t, err)
	items, err := CodexQueue(ctx, s, "t")
	require.NoError(t, err)
	assert.Equal(t, []CodexQueued{{ID: "q1", Text: "SESSION CHECK n1"}}, items)
	deleted, err := CodexDequeue(ctx, s, "t", "q1")
	require.NoError(t, err)
	assert.True(t, deleted)
	err = s.Call(ctx, "turn/steer", map[string]any{"threadId": "t"}, nil)
	assert.EqualError(t, err, "the codex app-server turn/steer: thread not found: t", "an error answer is the server's words")
}

func TestTheCheckLineSaysCodexQueues(t *testing.T) {
	t.Parallel()
	fc := CheckFriend(context.Background(), "stella", CheckSeams{
		Now: func() time.Time { return t0 },
		ReadStatus: func(string) (Status, bool, error) {
			return Status{At: t0, Harness: "codex", Queued: 2, QueueKnown: true}, true, nil
		},
		ReadPresence: func(string) (PresenceStatus, bool, error) { return PresenceStatus{}, false, nil },
		ReadPong:     func(string) (Pong, bool, error) { return Pong{}, false, nil },
		HarnessDir:   func(string) (string, string, error) { return "codex", "/w/stella", nil },
	}, time.Hour, nil)
	assert.Equal(t, "queue", fc.Harness.Route)
	assert.True(t, strings.HasSuffix(fc.Harness.Line(), " queued=2"), fc.Harness.Line())
	assert.Contains(t, fc.Harness.Line(), " route=queue ")
}
