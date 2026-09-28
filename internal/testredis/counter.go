package testredis

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// CommandCounter listens on 127.0.0.1, on a port the kernel chose, and counts
// every command a Redis client sends it, handshake included (#3277). It is
// not a redis-server and starts no process. It answers just enough RESP2 for
// a go-redis client to finish a command: HELLO is refused so the client falls
// back to RESP2, PING is PONG and anything else is OK. A test calls a
// package's Open or Dial against the address and asserts the count is still
// zero: the caller's first batch, not the open, is the first command.
func CommandCounter(t testing.TB) (addr string, count func() int64) {
	t.Helper()
	return real.counter(t, net.Listen)
}

func (l launch) counter(t testing.TB, listen func(network, address string) (net.Listener, error)) (string, func() int64) {
	t.Helper()
	l.refuse()
	ln, err := listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testredis: the command counter has no loopback port: %v", err)
	}
	var n atomic.Int64
	var conns []net.Conn
	var serving sync.WaitGroup
	accepting := make(chan struct{})
	// The cleanup hangs up on every client, so a test that fails before it
	// closes its own client still ends. The accept loop has ended by then, so
	// the list of clients is whole.
	t.Cleanup(func() {
		_ = ln.Close()
		<-accepting
		for _, c := range conns {
			_ = c.Close()
		}
		serving.Wait()
	})
	go func() {
		defer close(accepting)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
			serving.Add(1)
			go func() {
				defer serving.Done()
				defer c.Close()
				serveCounted(c, &n)
			}()
		}
	}()
	return ln.Addr().String(), n.Load
}

// serveCounted reads RESP arrays of bulk strings until the peer hangs up.
func serveCounted(c net.Conn, n *atomic.Int64) {
	r := bufio.NewReader(c)
	for {
		args, err := readArray(r)
		if err != nil {
			return
		}
		n.Add(1)
		reply := "+OK\r\n"
		switch strings.ToUpper(args[0]) {
		case "HELLO":
			reply = "-ERR unknown command 'HELLO'\r\n"
		case "PING":
			reply = "+PONG\r\n"
		}
		if _, err := io.WriteString(c, reply); err != nil {
			return
		}
	}
}

// The counter reads what a test's client sends; a length no client sends is
// refused before it is allocated.
const (
	maxWords = 1 << 20
	maxWord  = 64 << 20
)

// readArray reads one command: a RESP array of bulk strings.
func readArray(r *bufio.Reader) ([]string, error) {
	head, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(head, "*") {
		return nil, io.ErrUnexpectedEOF
	}
	count, err := strconv.Atoi(strings.TrimSpace(head[1:]))
	if err != nil || count < 1 || count > maxWords {
		return nil, io.ErrUnexpectedEOF
	}
	args := make([]string, count)
	for i := range args {
		size, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(size, "$") {
			return nil, io.ErrUnexpectedEOF
		}
		l, err := strconv.Atoi(strings.TrimSpace(size[1:]))
		if err != nil || l < 0 || l > maxWord {
			return nil, io.ErrUnexpectedEOF
		}
		buf := make([]byte, l+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:l])
	}
	return args, nil
}
