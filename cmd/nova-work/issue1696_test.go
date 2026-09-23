package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestIssue1696 pins nova-tools#1696: the socket's line wire cannot carry any
// listing verb -- 25 ROW/NOTE/MORE shapes in the grammar, one line on the
// wire. A listing verb's answer is MANY lines, and the versioned,
// length-prefixed JSON wire docs/SPEC-WORK.md pins carries them home as one
// ordered array, with the response's own exit beside them. The fake session
// here speaks that framed wire: it answers the line wire's probe with the one
// framed refusal the spec pins for a frame past the bound, then serves the
// hello and the request in frames. On the line wire alone not one of these
// answers can arrive.
func TestIssue1696(t *testing.T) {
	t.Run("a listing verb's rows and MORE cursor reach the caller", func(t *testing.T) {
		rows := []string{
			"QUERY ROW nova-tools/sec/alex-1 branch=closed disposition=done repo=mas-bandwidth/nova-tools kind=task state=done landed=9f2c1a7e released=v0.4.2 holder=unowned settled=2026-09-12T18:22:41Z evidence=2 verified=2 responsible=emma",
			"QUERY ROW nova-tools/sec/alex-3 branch=open disposition=working repo=mas-bandwidth/nova-tools kind=task state=doing landed=- released=- holder=freddy settled=- evidence=0 verified=0 responsible=freddy",
			"QUERY MORE rows=4 shown=2 pages=2 after=alex-3",
		}
		socket, requests := framedSession1696(t, rows, "0")
		var stdout, stderr bytes.Buffer
		code := run([]string{"query", "--session", socket, "--ask", "under", "--branch", "open"}, &stdout, &stderr, "")
		if code != 0 {
			t.Fatalf("query exit = %d, want 0 (stderr %q): the one-line wire cannot carry a listing verb's answer", code, stderr.String())
		}
		if want := strings.Join(rows, "\n") + "\n"; stdout.String() != want {
			t.Fatalf("query printed %q, want the session's whole ordered answer %q", stdout.String(), want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("query wrote stderr: %q", stderr.String())
		}
		select {
		case req := <-requests:
			if req["op"] != "query" {
				t.Fatalf("framed request op = %v, want query", req["op"])
			}
			args, _ := req["args"].(map[string]any)
			if args["ask"] != "under" || args["branch"] != "open" {
				t.Fatalf("framed request args = %v, want ask=under branch=open", args)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("no framed request reached the session: the client never left the one-line wire")
		}
	})

	t.Run("the response's own exit reaches the caller", func(t *testing.T) {
		lines := []string{"OPERATION FAIL session=/sessions/alpha.sock: the operation index is wedged"}
		socket, _ := framedSession1696(t, lines, "2")
		var stdout, stderr bytes.Buffer
		code := run([]string{"operation", "list", "--session", socket}, &stdout, &stderr, "")
		if code != 2 {
			t.Fatalf("operation list exit = %d, want the response's own exit 2, the field the one-line wire has no place for", code)
		}
		if stdout.Len() != 0 {
			t.Fatalf("operation list wrote stdout: %q", stdout.String())
		}
		if want := lines[0] + "\n"; stderr.String() != want {
			t.Fatalf("operation list stderr = %q, want the session's line %q", stderr.String(), want)
		}
	})
}

// framedSession1696 is the session end of the v1 wire: a Unix socket that
// tells the two wires apart by each connection's first byte -- a v1 frame's
// leading length byte is always 0x00 (a frame is bounded far under 16 MiB)
// and a request line's first byte is always a letter. A line probe is the
// spec's own refusal case, four ASCII bytes read as a frame length far past
// --max-frame-bytes, so it is answered with the one framed refusal the spec
// pins for a frame refused before a valid id can be decoded ("request": null)
// and the connection closes, never partially applied. A framed connection
// gets the hello, then the response the test ordered, echoing the request id.
func framedSession1696(t *testing.T, lines []string, exit string) (socket string, requests <-chan map[string]any) {
	t.Helper()
	t.Chdir(t.TempDir())
	ln, err := net.Listen("unix", "session.sock")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ch := make(chan map[string]any, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				first, err := r.Peek(1)
				if err != nil || len(first) == 0 {
					return
				}
				if first[0] != 0x00 {
					c.Write(frame1696(`{"request":null,"ok":false,"exit":"2","lines":["FRAME REFUSED: 4 ASCII bytes are a length far past --max-frame-bytes"],"rev":"0","pushed":"-"}`))
					return
				}
				hello := readFrame1696(t, r)
				if op, _ := hello["op"].(string); op != "hello" {
					t.Errorf("first frame op = %v, want the hello offer", hello["op"])
					return
				}
				if _, err := c.Write(frame1696(`{"op":"hello-ok","protocol":"1","session":"sbcl-test"}`)); err != nil {
					return
				}
				request := readFrame1696(t, r)
				ch <- request
				id, _ := request["request"].(string)
				body, err := json.Marshal(map[string]any{
					"request": id,
					"ok":      exit == "0",
					"exit":    exit,
					"lines":   lines,
					"rev":     "410",
					"pushed":  "-",
				})
				if err != nil {
					t.Errorf("marshal response: %v", err)
					return
				}
				c.Write(frame1696(string(body)))
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return "session.sock", ch
}

// frame1696 prefixes payload with the v1 wire's 4-byte big-endian length.
func frame1696(payload string) []byte {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	return append(hdr[:], payload...)
}

// readFrame1696 reads one v1 frame and decodes the one JSON object it carries.
func readFrame1696(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		t.Errorf("read frame header: %v", err)
		return nil
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > 1<<20 {
		t.Errorf("frame declares %d bytes, past the bound", n)
		return nil
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		t.Errorf("read frame body: %v", err)
		return nil
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Errorf("frame is not one JSON object: %v", err)
		return nil
	}
	return msg
}
