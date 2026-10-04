package friend

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"
)

// The desktop IPC protocol is measured in SPEC-FRIEND.md, Codex. A short
// deadline and bounded frames keep an absent or incompatible app a deferral.
const codexIPCBudget = 5 * time.Second
const codexIPCFrameLimit = 1 << 20

type codexIPCReply struct {
	Type              string          `json:"type"`
	RequestID         string          `json:"requestId"`
	ResultType        string          `json:"resultType"`
	Method            string          `json:"method"`
	HandledByClientID string          `json:"handledByClientId"`
	Error             string          `json:"error"`
	Result            json.RawMessage `json:"result"`
}

type codexIPC struct {
	conn   io.ReadWriter
	client string
	owner  string
}

// codexOpenChat implements the measured owner-discovery and admission protocol
// in SPEC-FRIEND.md, Codex. Success means admitted input, not a finished answer.
func codexOpenChat(ctx context.Context, home, session, text string, dial func(context.Context, string, string) (net.Conn, error)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, codexIPCBudget)
	defer cancel()
	conn, err := dial(ctx, "unix", filepath.Join(home, "ipc", "ipc.sock"))
	if err != nil {
		return "", err
	}
	defer conn.Close() // ignored: the admission receipt, not closing the stream, determines delivery.
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() }) // ignored: closing only unblocks cancelled I/O.
	defer stop()
	ipc := codexIPC{conn: conn, client: "initializing-client"}
	init, err := ipc.call("initialize", 0, map[string]any{"clientType": "nova-friend"})
	if err != nil {
		return "", err
	}
	var initialized struct {
		ClientID string `json:"clientId"`
	}
	if err := json.Unmarshal(init.Result, &initialized); err != nil || initialized.ClientID == "" {
		return "", fmt.Errorf("desktop IPC initialization has no client ID")
	}
	ipc.client = initialized.ClientID
	owner, err := ipc.call("thread-owner-discovery", 1, map[string]any{"hostId": "local", "conversationId": session})
	if err != nil {
		return "", err
	}
	if owner.HandledByClientID == "" {
		return "", fmt.Errorf("desktop IPC discovery has no owner")
	}
	ipc.owner = owner.HandledByClientID
	admitted, err := ipc.call("thread-follower-start-turn", 2, map[string]any{
		"conversationId": session,
		"turnStart": map[string]any{"request": map[string]any{
			"threadId": session,
			"input":    []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}},
		}, "context": map[string]any{}},
	})
	if err != nil {
		return "", err
	}
	var admission struct {
		Result struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		} `json:"result"`
	}
	if err := json.Unmarshal(admitted.Result, &admission); err != nil || admission.Result.Turn.ID == "" {
		return "", fmt.Errorf("desktop IPC reply has no admitted turn ID")
	}
	return admission.Result.Turn.ID, nil
}

// call sends one framed request and accepts only its matching success receipt
// from the discovered owner (SPEC-FRIEND.md, Codex). Broadcasts are not receipts.
func (c *codexIPC) call(method string, version int, params any) (codexIPCReply, error) {
	var reply codexIPCReply
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return reply, err
	}
	id := fmt.Sprintf("%x", nonce)
	request := map[string]any{"type": "request", "requestId": id, "sourceClientId": c.client, "method": method, "version": version, "params": params, "timeoutMs": codexIPCBudget.Milliseconds()}
	if c.owner != "" {
		request["targetClientId"] = c.owner
	}
	body, err := json.Marshal(request)
	if err != nil {
		return reply, err
	}
	if len(body) > codexIPCFrameLimit {
		return reply, fmt.Errorf("desktop IPC request exceeds %d bytes", codexIPCFrameLimit)
	}
	frame := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	if n, err := c.conn.Write(frame); err != nil {
		return reply, err
	} else if n != len(frame) {
		return reply, io.ErrShortWrite
	}
	for {
		var header [4]byte
		if _, err := io.ReadFull(c.conn, header[:]); err != nil {
			return reply, err
		}
		size := binary.LittleEndian.Uint32(header[:])
		if size == 0 || size > codexIPCFrameLimit {
			return reply, fmt.Errorf("desktop IPC frame size %d is outside 1..%d", size, codexIPCFrameLimit)
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(c.conn, body); err != nil {
			return reply, err
		}
		reply = codexIPCReply{}
		if err := json.Unmarshal(body, &reply); err != nil {
			return reply, err
		}
		if reply.Type != "response" || reply.RequestID != id {
			continue
		}
		if reply.ResultType != "success" {
			return reply, fmt.Errorf("desktop IPC %s: %s", method, reply.Error)
		}
		if reply.Method != method || (c.owner != "" && reply.HandledByClientID != c.owner) {
			return reply, fmt.Errorf("desktop IPC receipt does not match the requested owner and method")
		}
		return reply, nil
	}
}
