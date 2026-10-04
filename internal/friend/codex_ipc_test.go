package friend

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codexConn scripts frames in memory; no test dials a socket.
type codexConn struct {
	bytes.Buffer
	t        *testing.T
	requests []map[string]any
	change   func(string, map[string]any)
	prefix   []byte
	short    bool
	closed   bool
	deadline time.Time
	mu       sync.Mutex
}

func (c *codexConn) Write(p []byte) (int, error) {
	if c.short {
		return len(p) - 1, nil
	}
	require.GreaterOrEqual(c.t, len(p), 4)
	require.Equal(c.t, len(p)-4, int(binary.LittleEndian.Uint32(p)))
	var req map[string]any
	require.NoError(c.t, json.Unmarshal(p[4:], &req))
	c.requests = append(c.requests, req)
	method := req["method"].(string)
	reply := map[string]any{"type": "response", "requestId": req["requestId"], "resultType": "success", "method": method, "handledByClientId": "owner"}
	switch method {
	case "initialize":
		reply["result"] = map[string]any{"clientId": "client"}
	case "thread-owner-discovery":
		reply["result"] = map[string]any{"supportsUntrustedAppInput": true}
	case "thread-follower-start-turn":
		reply["result"] = map[string]any{"result": map[string]any{"turn": map[string]any{"id": "turn-1"}}}
	default:
		c.t.Fatalf("unexpected request %q", method)
	}
	if c.change != nil {
		c.change(method, reply)
	}
	if len(c.prefix) > 0 {
		_, err := c.Buffer.Write(c.prefix)
		require.NoError(c.t, err)
	}
	body, err := json.Marshal(reply)
	require.NoError(c.t, err)
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(body)))
	_, err = c.Buffer.Write(header[:])
	require.NoError(c.t, err)
	_, err = c.Buffer.Write(body)
	require.NoError(c.t, err)
	return len(p), nil
}
func (c *codexConn) Close() error                     { c.mu.Lock(); defer c.mu.Unlock(); c.closed = true; return nil }
func (c *codexConn) LocalAddr() net.Addr              { return nil }
func (c *codexConn) RemoteAddr() net.Addr             { return nil }
func (c *codexConn) SetDeadline(t time.Time) error    { c.deadline = t; return nil }
func (c *codexConn) SetReadDeadline(time.Time) error  { return nil }
func (c *codexConn) SetWriteDeadline(time.Time) error { return nil }
func (c *codexConn) dial(_ context.Context, network, address string) (net.Conn, error) {
	assert.Equal(c.t, "unix", network)
	assert.Equal(c.t, "/codex/ipc/ipc.sock", address)
	return c, nil
}

func TestCodexIPCAdmitsToTheDiscoveredOwner(t *testing.T) {
	t.Parallel()
	conn := &codexConn{t: t}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	turn, err := codexOpenChat(ctx, "/codex", "thread-1", "literal `text` [and] $shell", conn.dial)
	require.NoError(t, err)
	assert.Equal(t, "turn-1", turn)
	assert.True(t, conn.closed)
	assert.Equal(t, deadline, conn.deadline)
	require.Len(t, conn.requests, 3)
	assert.Equal(t, float64(0), conn.requests[0]["version"])
	assert.Equal(t, "client", conn.requests[1]["sourceClientId"])
	assert.Equal(t, map[string]any{"hostId": "local", "conversationId": "thread-1"}, conn.requests[1]["params"])
	send := conn.requests[2]
	assert.Equal(t, "owner", send["targetClientId"])
	assert.Equal(t, float64(2), send["version"])
	params := send["params"].(map[string]any)
	assert.Equal(t, "thread-1", params["conversationId"])
	start := params["turnStart"].(map[string]any)["request"].(map[string]any)
	assert.Equal(t, "thread-1", start["threadId"])
	assert.Equal(t, []any{map[string]any{"type": "text", "text": "literal `text` [and] $shell", "text_elements": []any{}}}, start["input"])
}

func TestCodexIPCRejectsUnconfirmedAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, want string
		change             func(map[string]any)
	}{
		{"no client", "initialize", "no client ID", func(r map[string]any) { r["result"] = map[string]any{} }},
		{"no owner", "thread-owner-discovery", "no owner", func(r map[string]any) { r["handledByClientId"] = "" }},
		{"unavailable", "thread-owner-discovery", "no-client-found", func(r map[string]any) { r["resultType"] = "error"; r["error"] = "no-client-found" }},
		{"wrong method", "thread-follower-start-turn", "does not match", func(r map[string]any) { r["method"] = "other" }},
		{"wrong owner", "thread-follower-start-turn", "does not match", func(r map[string]any) { r["handledByClientId"] = "other" }},
		{"no turn", "thread-follower-start-turn", "no admitted turn ID", func(r map[string]any) { r["result"] = map[string]any{} }},
		{"bad result", "thread-follower-start-turn", "no admitted turn ID", func(r map[string]any) { r["result"] = "not an object" }},
		{"wrong id", "thread-follower-start-turn", "EOF", func(r map[string]any) { r["requestId"] = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			conn := &codexConn{t: t, change: func(method string, r map[string]any) {
				if method == tc.method {
					tc.change(r)
				}
			}}
			turn, err := codexOpenChat(context.Background(), "/codex", "t", "hello", conn.dial)
			require.ErrorContains(t, err, tc.want)
			assert.Empty(t, turn)
			assert.True(t, conn.closed)
		})
	}
}

func TestCodexIPCFramesAreBoundedAndBroadcastsAreNotReceipts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		prefix []byte
		short  bool
		want   string
	}{
		{"empty frame", []byte{0, 0, 0, 0}, false, "outside"},
		{"oversized frame", []byte{1, 0, 16, 0}, false, "outside"},
		{"invalid json", []byte{1, 0, 0, 0, '!'}, false, "invalid character"},
		{"short write", nil, true, "short write"},
		{"broadcast", append([]byte{20, 0, 0, 0}, []byte(`{"type":"broadcast"}`)...), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			conn := &codexConn{t: t, prefix: tc.prefix, short: tc.short}
			_, err := codexOpenChat(context.Background(), "/codex", "t", "hello", conn.dial)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestCodexIPCDialFailureDoesNotAdmit(t *testing.T) {
	t.Parallel()
	turn, err := codexOpenChat(context.Background(), "/codex", "t", "hello", func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unavailable") })
	require.ErrorContains(t, err, "unavailable")
	assert.Empty(t, turn)
}

var _ net.Conn = (*codexConn)(nil)
var _ io.ReadWriter = (*codexConn)(nil)

// cancelledCodexConn blocks reads until cancellation closes it, without timers or sockets.
type cancelledCodexConn struct {
	*codexConn
	done chan struct{}
	once sync.Once
}

func (c *cancelledCodexConn) Read([]byte) (int, error) { <-c.done; return 0, io.EOF }
func (c *cancelledCodexConn) Close() error             { c.once.Do(func() { close(c.done) }); return nil }

func TestCodexIPCCancellationUnblocksTheRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &cancelledCodexConn{codexConn: &codexConn{t: t}, done: make(chan struct{})}
	turn, err := codexOpenChat(ctx, "/codex", "t", "hello", func(context.Context, string, string) (net.Conn, error) { cancel(); return conn, nil })
	require.ErrorIs(t, err, io.EOF)
	assert.Empty(t, turn)
	select {
	case <-conn.done:
	default:
		t.Fatal("connection was not closed on cancellation")
	}
}

func TestCodexIPCRejectsAnOversizedRequestBeforeSendingIt(t *testing.T) {
	t.Parallel()
	conn := &codexConn{t: t}
	_, err := codexOpenChat(context.Background(), "/codex", "t", strings.Repeat("x", codexIPCFrameLimit), conn.dial)
	require.ErrorContains(t, err, "request exceeds")
	require.Len(t, conn.requests, 2)
}
