package workclient

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

// testFrame prefixes payload with the v1 wire's 4-byte big-endian length.
func testFrame(payload string) []byte {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	return append(hdr[:], payload...)
}

// readTestFrame reads one 4-byte length prefix and the payload it names, the
// framed twin of the newline the S1 fixtures read.
func readTestFrame(r io.Reader) (string, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > 1<<20 {
		return "", io.ErrUnexpectedEOF
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return "", err
	}
	return string(body), nil
}

// A listing verb's answer is many lines, and the S1 wire carries one: the
// socket's read-line/write-line loop and workclient.Exchange's one read cannot
// return a `query` (or `operation list`, or `savepoint list`, or any of the
// twelve built from the grammar's 25 ROW/NOTE/MORE shapes). The framed v1
// response carries `lines` as an ordered ARRAY, and ExchangeFramed is the Go
// reader beside Exchange that carries them whole -- with the response's own
// exit, rev and pushed, which the one-line wire has no field for.
func TestExchangeFramedCarriesAMultiRowListing(t *testing.T) {
	rows := []string{
		"QUERY ROW nova-tools/sec/alex-1 branch=closed disposition=done repo=mas-bandwidth/nova-tools kind=task state=done landed=9f2c1a7e released=v0.4.2 holder=unowned settled=2026-09-12T18:22:41Z evidence=2 verified=2 responsible=emma",
		"QUERY ROW nova-tools/sec/alex-3 branch=open disposition=working repo=mas-bandwidth/nova-tools kind=task state=doing landed=- released=- holder=freddy settled=- evidence=0 verified=0 responsible=freddy",
		"QUERY MORE rows=4 shown=2 pages=2 after=alex-3",
	}
	socket, ln := listen(t)
	hellos := make(chan string, 1)
	requests := make(chan string, 1)
	serve(ln, func(c net.Conn) {
		defer c.Close()
		offer, err := readTestFrame(c)
		if err != nil {
			return
		}
		hellos <- offer
		if _, err := c.Write(testFrame(`{"op": "hello-ok", "protocol": "1", "session": "sbcl-test"}`)); err != nil {
			return
		}
		asked, err := readTestFrame(c)
		if err != nil {
			return
		}
		requests <- asked
		body, err := json.Marshal(map[string]any{
			"request": "r-7",
			"ok":      true,
			"exit":    "0",
			"lines":   rows,
			"rev":     "410",
			"pushed":  "-",
		})
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		c.Write(testFrame(string(body)))
	})

	reply, err := ExchangeFramedWithin(socket,
		Frame{Op: "query", Request: "r-7", As: "rowan", Args: map[string]any{"ask": "under", "branch": "open"}},
		10*time.Second)
	if err != nil {
		t.Fatalf("ExchangeFramed: %v", err)
	}
	if len(reply.Lines) != len(rows) {
		t.Fatalf("carried %d lines, want %d: the one-line wire drops a listing", len(reply.Lines), len(rows))
	}
	for i := range rows {
		if reply.Lines[i] != rows[i] {
			t.Fatalf("line %d = %q, want %q", i, reply.Lines[i], rows[i])
		}
	}
	if reply.Exit != "0" || reply.Rev != "410" || reply.Pushed != "-" {
		t.Fatalf("exit=%q rev=%q pushed=%q, want 0/410/-", reply.Exit, reply.Rev, reply.Pushed)
	}

	select {
	case h := <-hellos:
		if h == "" {
			t.Fatalf("the client sent no hello frame")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the client never negotiated the protocol")
	}
	select {
	case req := <-requests:
		if req == "" {
			t.Fatalf("the client sent no request frame")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the client never sent its request frame")
	}
}
