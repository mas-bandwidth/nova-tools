package friend

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The Codex app-server (docs/SPEC-FRIEND.md, Codex): `codex queue` puts a message on the
// thread's queue through the local app-server daemon, which the Codex app shows under its
// composer and starts when the turn under way ends. The daemon reads and withdraws from that
// queue through the same server (thread/queue/list, thread/queue/delete), over its control
// socket: a WebSocket on <CODEX_HOME>/app-server-control/app-server-control.sock speaking
// JSON-RPC. Steering a turn (turn/steer) is not reachable there: the open chat's turn runs
// in the Codex app's own server, and the daemon's answers "thread not found" for it
// (measured 2026-10-06, codex 0.153.4: thread/loaded/list empty, turn/steer on the friend's
// thread "thread not found", thread/queue/list her three queued checks).

// CodexAppServer is one connection to the app-server: one JSON-RPC call at a time.
type CodexAppServer interface {
	Call(ctx context.Context, method string, params, result any) error
	Close() error
}

// CodexControlSocket is the app-server's control socket under home (CODEX_HOME).
func CodexControlSocket(home string) string {
	return filepath.Join(home, "app-server-control", "app-server-control.sock")
}

// CodexAppServerBudget bounds one connection's dial, handshake and calls.
const CodexAppServerBudget = 5 * time.Second

// DialCodexAppServer connects to the app-server under home and initializes the session.
func DialCodexAppServer(ctx context.Context, home string) (CodexAppServer, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", CodexControlSocket(home))
	if err != nil {
		return nil, fmt.Errorf("the codex app-server: %w", err)
	}
	s, err := NewCodexAppServer(ctx, conn)
	if err != nil {
		_ = conn.Close() // ignored: the handshake's error is the one said
		return nil, err
	}
	return s, nil
}

// codexRPC is JSON-RPC over a client WebSocket (RFC 6455: text frames, masked).
type codexRPC struct {
	mu   sync.Mutex
	conn net.Conn
	r    *bufio.Reader
	next int
}

// NewCodexAppServer speaks the WebSocket handshake and the app-server's initialize over conn.
func NewCodexAppServer(ctx context.Context, conn net.Conn) (CodexAppServer, error) {
	s := &codexRPC{conn: conn, r: bufio.NewReader(conn)}
	s.deadline(ctx)
	raw := make([]byte, 16)
	_, _ = rand.Read(raw) // ignored: crypto/rand never fails on the platforms built for
	key := base64.StdEncoding.EncodeToString(raw)
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", key); err != nil {
		return nil, fmt.Errorf("the codex app-server handshake: %w", err)
	}
	resp, err := http.ReadResponse(s.r, nil)
	if err != nil {
		return nil, fmt.Errorf("the codex app-server handshake: %w", err)
	}
	_ = resp.Body.Close() // ignored: a 101 has no body
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("the codex app-server handshake: %s", resp.Status)
	}
	init := map[string]any{"clientInfo": map[string]string{"name": "nova-friend", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}
	if err := s.Call(ctx, "initialize", init, nil); err != nil {
		return nil, err
	}
	if err := s.write(map[string]string{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		return nil, fmt.Errorf("the codex app-server: %w", err)
	}
	return s, nil
}

func (s *codexRPC) deadline(ctx context.Context) {
	if d, ok := ctx.Deadline(); ok {
		_ = s.conn.SetDeadline(d) // ignored: a conn without deadlines answers or fails on its own
	}
}

// Call sends one request and reads until its answer, past every notification.
func (s *codexRPC) Call(ctx context.Context, method string, params, result any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadline(ctx)
	s.next++
	id := s.next
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return fmt.Errorf("the codex app-server %s: %w", method, err)
	}
	for {
		msg, err := s.read()
		if err != nil {
			return fmt.Errorf("the codex app-server %s: %w", method, err)
		}
		var reply struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(msg, &reply) != nil || reply.ID == nil || *reply.ID != id {
			continue // a notification, or another's answer
		}
		if reply.Error != nil {
			return fmt.Errorf("the codex app-server %s: %s", method, reply.Error.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(reply.Result, result)
	}
}

func (s *codexRPC) Close() error { return s.conn.Close() }

// write sends v as one masked text frame.
func (s *codexRPC) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	head := []byte{0x81}
	switch n := len(data); {
	case n < 126:
		head = append(head, 0x80|byte(n))
	case n <= 0xffff:
		head = append(head, 0x80|126, byte(n>>8), byte(n))
	default:
		head = binary.BigEndian.AppendUint64(append(head, 0x80|127), uint64(n))
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask) // ignored: crypto/rand never fails on the platforms built for
	frame := append(head, mask...)
	for i, b := range data {
		frame = append(frame, b^mask[i%4])
	}
	_, err = s.conn.Write(frame)
	return err
}

// codexFrameCap bounds one message the server sends.
const codexFrameCap = 64 << 20

// read answers the next whole text message, answering pings and joining fragments.
func (s *codexRPC) read() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(s.r, h[:]); err != nil {
			return nil, err
		}
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(s.r, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(s.r, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		var mask [4]byte
		if h[1]&0x80 != 0 {
			if _, err := io.ReadFull(s.r, mask[:]); err != nil {
				return nil, err
			}
		}
		if n > codexFrameCap {
			return nil, fmt.Errorf("a frame of %d bytes, over %d", n, codexFrameCap)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(s.r, data); err != nil {
			return nil, err
		}
		if h[1]&0x80 != 0 {
			for i := range data {
				data[i] ^= mask[i%4]
			}
		}
		switch h[0] & 0x0f {
		case 0x8:
			return nil, errors.New("the server closed the connection")
		case 0x9:
			if err := s.control(0xA, data); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		}
		msg = append(msg, data...)
		if h[0]&0x80 != 0 {
			return msg, nil
		}
	}
}

// control sends one masked control frame (a pong) carrying data.
func (s *codexRPC) control(op byte, data []byte) error {
	if len(data) > 125 {
		data = data[:125]
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask) // ignored: crypto/rand never fails on the platforms built for
	frame := append([]byte{0x80 | op, 0x80 | byte(len(data))}, mask...)
	for i, b := range data {
		frame = append(frame, b^mask[i%4])
	}
	_, err := s.conn.Write(frame)
	return err
}

// CodexQueued is one message on a thread's queue: its queued submission id and its text.
type CodexQueued struct {
	ID, Text string
}

// CodexQueue is thread's queue, oldest first (thread/queue/list, every page).
func CodexQueue(ctx context.Context, s CodexAppServer, thread string) ([]CodexQueued, error) {
	var out []CodexQueued
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{"threadId": thread}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data []struct {
				ID    string `json:"id"`
				Input []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"input"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := s.Call(ctx, "thread/queue/list", params, &res); err != nil {
			return nil, err
		}
		for _, d := range res.Data {
			var texts []string
			for _, in := range d.Input {
				if in.Type == "text" {
					texts = append(texts, in.Text)
				}
			}
			out = append(out, CodexQueued{ID: d.ID, Text: strings.Join(texts, "\n")})
		}
		if res.NextCursor == nil || *res.NextCursor == "" {
			return out, nil
		}
		cursor = *res.NextCursor
	}
	return out, nil
}

// CodexDequeue withdraws one queued submission from thread's queue; deleted is false when it
// was no longer there (the session took it).
func CodexDequeue(ctx context.Context, s CodexAppServer, thread, id string) (deleted bool, err error) {
	var res struct {
		Deleted bool `json:"deleted"`
	}
	err = s.Call(ctx, "thread/queue/delete", map[string]string{"threadId": thread, "queuedSubmissionId": id}, &res)
	return res.Deleted, err
}

// UUIDv7Time is when a UUIDv7 (Codex's ids) was made: its first 48 bits, Unix milliseconds;
// ok is false for an id that carries no time.
func UUIDv7Time(id string) (time.Time, bool) {
	h := strings.ReplaceAll(id, "-", "")
	if len(h) != 32 || h[12] != '7' {
		return time.Time{}, false
	}
	b, err := hex.DecodeString("0000" + h[:12])
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(binary.BigEndian.Uint64(b))), true
}
