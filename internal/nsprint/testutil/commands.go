package testutil

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

// CommandCounter listens on 127.0.0.1 and counts every command a Redis client
// sends it, handshake included (#3277). It answers just enough RESP2 for a
// go-redis client to finish a command: HELLO is refused so the client falls
// back to RESP2, PING is PONG and anything else is OK. A test calls a
// package's Open or Dial against the address and asserts the count is still
// zero: the caller's first batch, not the open, is the first command.
func CommandCounter(t *testing.T) (addr string, count func() int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var n atomic.Int64
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	// The cleanup hangs up on every client, so a test that fails before it
	// closes its own client still ends.
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
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

func readArray(r *bufio.Reader) ([]string, error) {
	head, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(head, "*") {
		return nil, io.ErrUnexpectedEOF
	}
	count, err := strconv.Atoi(strings.TrimSpace(head[1:]))
	if err != nil || count < 1 {
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
		if err != nil || l < 0 {
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
