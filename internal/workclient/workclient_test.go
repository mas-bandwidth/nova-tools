package workclient

import (
	"bytes"
	"net"
	"testing"
)

// countingConn counts the bytes readReply pulls from the wire. A bounded reader
// stops at the cap plus the one delimiter byte; an unbounded one drains the
// whole stream, and the count is where that difference is observable.
type countingConn struct {
	net.Conn
	read int
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read += n
	return n, err
}

// pipeReply serves payload over a local net.Pipe and hands the reader the
// counting end. The writer runs on its own goroutine because a pipe write
// blocks until it is read; the returned client lets a test unblock a writer a
// bounded reader left mid-stream. The done channel closes when the writer
// returns, so coordination is by event and never by a wall-clock wait.
func pipeReply(t *testing.T, payload []byte) (client net.Conn, conn *countingConn, done <-chan struct{}) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	conn = &countingConn{Conn: server}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = client.Write(payload)
		_ = client.Close()
	}()
	return client, conn, finished
}

func TestReadReplyAcceptsOneLine(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		want    string
	}{
		{"empty newline", []byte("\n"), ""},
		{"ordinary newline", []byte("OK work query\n"), "OK work query"},
		{
			"exactly cap before newline",
			append(bytes.Repeat([]byte{'a'}, replyCap), '\n'),
			string(bytes.Repeat([]byte{'a'}, replyCap)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, conn, done := pipeReply(t, tc.payload)
			got, err := readReply(conn)
			if err != nil {
				t.Fatalf("readReply: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("readReply returned %d bytes, want %d", len(got), len(tc.want))
			}
			<-done
			client.Close()
		})
	}
}

func TestReadReplyRefusesWholeWithoutPartialReply(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"cap plus one before newline", append(bytes.Repeat([]byte{'b'}, replyCap+1), '\n')},
		{"overlong with no newline", bytes.Repeat([]byte{'c'}, replyCap+4096)},
		{"premature eof", []byte("OK work query")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, conn, done := pipeReply(t, tc.payload)
			got, err := readReply(conn)
			if err == nil {
				t.Fatalf("readReply accepted an overlong or unterminated reply")
			}
			if got != "" {
				t.Fatalf("readReply returned %d bytes with an error; want no partial reply", len(got))
			}
			client.Close()
			<-done
		})
	}
}

// TestReadReplyConsumesNoMoreThanTheCap is the regression: an overlong stream
// must be refused at the cap, not after the whole stream has been read and a
// post-read length check has run. A reader that consumes all of the payload
// fails here even though it also returns an error.
func TestReadReplyConsumesNoMoreThanTheCap(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"overlong with no newline", bytes.Repeat([]byte{'x'}, replyCap+4096)},
		{"cap plus one before newline", append(bytes.Repeat([]byte{'y'}, replyCap+1), '\n')},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, conn, done := pipeReply(t, tc.payload)
			got, err := readReply(conn)
			if err == nil {
				t.Fatalf("readReply accepted an overlong reply")
			}
			if got != "" {
				t.Fatalf("readReply returned %d bytes with an error; want no partial reply", len(got))
			}
			if conn.read > replyCap+1 {
				t.Fatalf("readReply consumed %d bytes; the cap plus the delimiter is %d", conn.read, replyCap+1)
			}
			client.Close()
			<-done
		})
	}
}
