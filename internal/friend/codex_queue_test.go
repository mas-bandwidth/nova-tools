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
	deleted   []string
	queuedN   int   // codex queue runs
	deleteErr error // the app refuses a withdrawal
}

func (f *codexApp) Call(_ context.Context, method string, params, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var reply any
	switch method {
	case "thread/queue/list":
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
		n := len(f.queue)
		f.queue = slices.DeleteFunc(f.queue, func(q CodexQueued) bool { return q.ID == id })
		if len(f.queue) < n {
			f.deleted = append(f.deleted, id)
		}
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
	if args[0] != "queue" {
		return "", 1, errors.New("only the open chat's queue here")
	}
	f.queuedN++
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
	return &Codex{Dir: "/w/stella", Session: "thread-1", Home: "/codex", Program: "codex", Run: f.run, Out: record,
		Held: func(string) bool { return true },
		App:  func(context.Context) (CodexAppServer, error) { return f, nil },
		Now:  func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }}
}

func pongCmd(nonce string) string {
	return "/Users/x/.local/bin/nova-friend pong --as stella --nonce " + nonce + " --state-dir /s --redis 127.0.0.1:6381"
}

// A delivery while her Codex chat is mid-turn cannot be steered: the turn runs in the Codex
// app's own server, which takes no request from outside, and the app holds what is queued
// until the turn ends (the owner's screenshot of 2026-10-06 2:58 PM ET: three queued pong
// requests for nonce d6i4h2 and a fourth in the composer). So her queue holds one request
// for a pong at a time: an older request a newer one supersedes is withdrawn, the same
// request still unread is not queued twice, and a queued message that carries anything else
// is never withdrawn; a withdrawal the app refuses is said, superseded.
func TestACodexDeliveryDuringATurnIsNotQueuedTwice(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	var record strings.Builder
	c := f.codex(&record)
	note := "Run this now, first, exactly as written: " + pongCmd("d6i4h2") + "\nThen read on.\n\nnova-friend: 1 message(s) for you, oldest first, in one turn; take each in order.\n\n=== message 1 of 1 ===\nRECV OK id=m1 from=rowan\n\ncard notes\n"
	f.add(t0.Add(-20*time.Minute), WakeTurnText(pongCmd("d6i4h2")))
	f.add(t0.Add(-15*time.Minute), note)
	f.add(t0.Add(-10*time.Minute), "Run this now, first, exactly as written: "+pongCmd("d6i4h2")+"\nThen read on.\n\nnova-friend: you hold 2 cards (a, b) and your session has written nothing for 10m0s; continue the oldest, a.\n")

	check := SessionCheckText("k2p9xz", pongCmd("k2p9xz"), "rowan")
	exit, err := c.Deliver(context.Background(), check)
	require.NoError(t, err)
	assert.Zero(t, exit)
	assert.Equal(t, []string{note, check}, f.texts(), "the older pong requests withdrawn, the card note kept, the new check queued")
	n, ok := c.Queued()
	assert.True(t, ok)
	assert.Equal(t, 2, n)
	assert.Contains(t, record.String(), "codex: withdrew the queued pong request d6i4h2 ("+uuid7(t0.Add(-20*time.Minute), 1)+") from thread thread-1: superseded by nonce k2p9xz\n")

	f.now = t0.Add(10 * time.Minute)
	exit, err = c.Deliver(context.Background(), check)
	require.NoError(t, err)
	assert.Zero(t, exit)
	assert.Equal(t, 1, f.queuedN, "the same check, unread, is not queued twice")
	assert.Contains(t, record.String(), "codex: the pong request k2p9xz is still unread in the queue of thread thread-1 since "+t0.UTC().Format(time.RFC3339)+"; not queued twice\n")

	f.deleteErr = errors.New("not allowed")
	newer := WakeTurnText(pongCmd("q7r1aa"))
	_, err = c.Deliver(context.Background(), newer)
	require.NoError(t, err)
	assert.Contains(t, record.String(), "codex: the queued pong request k2p9xz ("+uuid7(t0, 4)+") not withdrawn from thread thread-1: not allowed; marked superseded\n")
	assert.Equal(t, 2, f.queuedN)
}

// One session check is in flight at a time in her Codex queue: asked again every ten
// minutes while it stands unread, it is not queued again; once the session has taken it, the
// next ask is queued; one left unread for CodexCheckRequeue is withdrawn and queued afresh.
func TestOneSessionCheckInFlightForCodex(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	var record strings.Builder
	c := f.codex(&record)
	check := SessionCheckText("n1", pongCmd("n1"), "rowan")
	for i := range 6 {
		f.now = t0.Add(time.Duration(i) * SessionQuiet)
		_, err := c.Deliver(context.Background(), check)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, f.queuedN, "asked six times over fifty minutes, queued once")
	assert.Len(t, f.texts(), 1)

	f.mu.Lock()
	f.queue = nil // her turn ended: she took it
	f.mu.Unlock()
	f.now = t0.Add(time.Hour)
	_, err := c.Deliver(context.Background(), check)
	require.NoError(t, err)
	assert.Equal(t, 2, f.queuedN, "taken, then asked again: queued again")

	f.now = t0.Add(2*time.Hour + time.Minute)
	_, err = c.Deliver(context.Background(), check)
	require.NoError(t, err)
	assert.Equal(t, 3, f.queuedN)
	assert.Len(t, f.texts(), 1, "the hour-old request withdrawn, one in flight")
	assert.Contains(t, record.String(), "superseded by the same request queued again\n")
}

func TestPongRequestReadsWhatADeliveryAsksFor(t *testing.T) {
	t.Parallel()
	for text, want := range map[string][2]any{
		SessionCheckText("a1", pongCmd("a1"), "rowan"): {"a1", true},
		WakeTurnText(pongCmd("b2")):                    {"b2", true},
		"Run this now, first, exactly as written: " + pongCmd("c3") + "\nThen read on.\n\nnova-friend: you hold 2 cards":                                   {"c3", true},
		"Run this now, first, exactly as written: " + pongCmd("d4") + "\nThen read on.\n\nnova-friend: 2 sprint card(s) dealt to you are in your inbox:\n": {"d4", false},
		"Run this now, first, exactly as written: " + pongCmd("e5") + "\nThen read on.\n\nRECV OK id=1 from=rowan":                                         {"e5", false},
		"RECV OK id=1 from=rowan\n\nhello\n": {"", false},
	} {
		nonce, only := PongRequest(text)
		assert.Equal(t, want, [2]any{nonce, only}, text)
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
	defer client.Close()
	defer server.Close()
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
