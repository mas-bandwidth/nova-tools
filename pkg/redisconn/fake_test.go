package redisconn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// fakeStore is a store for the unit tier: it speaks the wire protocol over
// net.Pipe, so a test of Open opens no socket and starts no process. Every
// dial makes a new connection served by its own goroutine, and every command
// every connection received is kept in order.
type fakeStore struct {
	// reply answers one command with the bytes to write back. hang is a store
	// that never answers, hangUp one that drops the connection.
	reply func(conn int, cmd []string) string

	mu    sync.Mutex
	seen  []string // "<conn>: <command and arguments>"
	dials int
	spies []*spyConn
	stop  chan struct{}
	wg    sync.WaitGroup
}

const (
	hang   = "\x00hang"
	hangUp = "\x00hang up"
)

// helloAccepted is the reply of a store that accepted HELLO 3.
const helloAccepted = "%7\r\n$6\r\nserver\r\n$5\r\nredis\r\n$7\r\nversion\r\n$5\r\n8.0.0\r\n$5\r\nproto\r\n:3\r\n$2\r\nid\r\n:7\r\n$4\r\nmode\r\n$10\r\nstandalone\r\n$4\r\nrole\r\n$6\r\nmaster\r\n$7\r\nmodules\r\n*0\r\n"

// accepting is the replies of a store that accepts every login and answers
// the few commands these tests send.
func accepting(_ int, cmd []string) string {
	switch strings.ToUpper(cmd[0]) {
	case "HELLO":
		return helloAccepted
	case "PING":
		return "+PONG\r\n"
	case "GET", "HGET":
		if strings.HasPrefix(cmd[1], "absent") {
			return "_\r\n"
		}
		return "$5\r\nvalue\r\n"
	case "LPUSH":
		return "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"
	case "CLIENT":
		return ":7\r\n"
	case "MULTI":
		return "+OK\r\n"
	case "EXEC":
		return "*1\r\n+OK\r\n"
	}
	return "+OK\r\n"
}

// refusing is accepting, with HELLO and AUTH refused by the line given.
func refusing(line string) func(int, []string) string {
	return func(conn int, cmd []string) string {
		switch strings.ToUpper(cmd[0]) {
		case "HELLO", "AUTH":
			return line
		}
		return accepting(conn, cmd)
	}
}

// newFakeStore starts a store that answers with reply and stops with the
// test.
func newFakeStore(t testing.TB, reply func(conn int, cmd []string) string) *fakeStore {
	t.Helper()
	s := &fakeStore{reply: reply, stop: make(chan struct{})}
	t.Cleanup(func() {
		close(s.stop)
		s.mu.Lock()
		for _, spy := range s.spies {
			_ = spy.Conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return s
}

// dial is the store's dialer: open's dialFunc.
func (s *fakeStore) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	client, server := net.Pipe()
	s.mu.Lock()
	s.dials++
	conn := s.dials
	spy := &spyConn{Conn: client}
	if deadline, ok := ctx.Deadline(); !ok {
		spy.fault("dial %d has no deadline", conn)
	} else if left := time.Until(deadline); left > DialTimeout {
		spy.fault("dial %d has %v, over the bound of %v", conn, left, DialTimeout)
	}
	s.spies = append(s.spies, spy)
	s.mu.Unlock()
	s.wg.Add(1)
	go s.serve(conn, server)
	return spy, nil
}

func (s *fakeStore) serve(conn int, c net.Conn) {
	defer s.wg.Done()
	defer func() { _ = c.Close() }() // ignored: the store is shutting down; a close error changes nothing
	rd := bufio.NewReader(c)
	for {
		cmd, err := readCommand(rd)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.seen = append(s.seen, fmt.Sprintf("%d: %s", conn, strings.Join(cmd, " ")))
		s.mu.Unlock()
		switch out := s.reply(conn, cmd); out {
		case hang:
			<-s.stop
			return
		case hangUp:
			return
		default:
			if _, err := io.WriteString(c, out); err != nil {
				return
			}
		}
	}
}

// commands is every command the store has received so far, in order, each
// as "<connection>: <words>", and forgets them.
func (s *fakeStore) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.seen
	s.seen = nil
	return out
}

// dialed is the number of dials made so far.
func (s *fakeStore) dialed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

// open is the number of connections the client has not closed.
func (s *fakeStore) open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, spy := range s.spies {
		spy.mu.Lock()
		if !spy.closed {
			n++
		}
		spy.mu.Unlock()
	}
	return n
}

// faults is every read, write and dial that was made without a deadline or
// with one beyond the bounds of the package.
func (s *fakeStore) faults() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, spy := range s.spies {
		spy.mu.Lock()
		out = append(out, spy.faults...)
		spy.mu.Unlock()
	}
	return out
}

// readCommand reads one command as a client writes it: an array of bulk
// strings.
func readCommand(rd *bufio.Reader) ([]string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(line, "*"), "\r\n"))
	if err != nil {
		return nil, fmt.Errorf("not an array of %q: %w", line, err)
	}
	cmd := make([]string, n)
	for i := range cmd {
		head, err := rd.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(head, "$"), "\r\n"))
		if err != nil {
			return nil, fmt.Errorf("not a bulk string of %q: %w", head, err)
		}
		body := make([]byte, size+2)
		if _, err := io.ReadFull(rd, body); err != nil {
			return nil, err
		}
		cmd[i] = string(body[:size])
	}
	return cmd, nil
}

// spyConn is the client's end of a connection to the fake store. It holds
// every read and every write to the rule of the package: a deadline is in
// force, and it is no further away than the bound.
type spyConn struct {
	net.Conn
	mu            sync.Mutex
	read, written time.Time
	faults        []string
	closed        bool
}

func (c *spyConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *spyConn) fault(format string, args ...any) {
	c.faults = append(c.faults, fmt.Sprintf(format, args...))
}

// A pipe whose far end has closed refuses a deadline and refuses a write
// with io.ErrClosedPipe. A socket does neither: it takes the deadline, and a
// write to it fails as a broken pipe. The spy answers as the socket would,
// so a dropped connection is what a tool would meet: the end of the stream
// on the read.
func (c *spyConn) asSocket(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if errors.Is(err, io.ErrClosedPipe) && !c.closed {
		return nil
	}
	return err
}

func (c *spyConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.read, c.written = t, t
	c.mu.Unlock()
	return c.asSocket(c.Conn.SetDeadline(t))
}

func (c *spyConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.read = t
	c.mu.Unlock()
	return c.asSocket(c.Conn.SetReadDeadline(t))
}

func (c *spyConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.written = t
	c.mu.Unlock()
	return c.asSocket(c.Conn.SetWriteDeadline(t))
}

func (c *spyConn) held(what string, deadline time.Time, bound time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case deadline.IsZero():
		c.fault("%s with no deadline", what)
	case time.Until(deadline) > bound:
		c.fault("%s with %v, over the bound of %v", what, time.Until(deadline), bound)
	}
}

func (c *spyConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	deadline := c.read
	c.mu.Unlock()
	c.held("read", deadline, ReadTimeout)
	return c.Conn.Read(p)
}

func (c *spyConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	deadline := c.written
	c.mu.Unlock()
	c.held("write", deadline, WriteTimeout)
	n, err := c.Conn.Write(p)
	if c.asSocket(err) == nil && err != nil {
		err = &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.EPIPE)}
	}
	return n, err
}

// refusedDial is a dialer whose store is not there: what the network answers
// for a closed port.
func refusedDial(dials *int) dialFunc {
	return func(_ context.Context, network, addr string) (net.Conn, error) {
		*dials++
		return nil, &net.OpError{Op: "dial", Net: network, Addr: fakeAddr(addr),
			Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
}

// silentStore is a store that never answers a dial. Its dialer returns when
// the dial's own context ends, as the network's dialer does, which can be
// after the caller has stopped waiting; settled waits for the dials that are
// still under way. It is for a synctest bubble.
type silentStore struct {
	mu      sync.Mutex
	dials   int
	pending sync.WaitGroup
}

func (s *silentStore) dial(dctx context.Context, network, addr string) (net.Conn, error) {
	s.pending.Add(1)
	defer s.pending.Done()
	s.mu.Lock()
	s.dials++
	s.mu.Unlock()
	<-dctx.Done()
	return nil, &net.OpError{Op: "dial", Net: network, Addr: fakeAddr(addr), Err: dctx.Err()}
}

func (s *silentStore) dialed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

// settled returns when every dial under way has returned. go-redis dials on
// a goroutine of its own (v9.22.0, its pool's pool.go:1078, queuedNewConn),
// started before Open returns and not yet at dial's pending.Add(1) when Open
// returns; a Wait on pending alone could return before the dial began, and
// the dial would then run after the test had ended (a race the -race run
// found). synctest.Wait first: every other goroutine of the bubble is then
// durably blocked or done, so a dial that was coming has begun and is
// counted before pending is waited on.
func (s *silentStore) settled() {
	synctest.Wait()
	s.pending.Wait()
}

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

// environment is a getenv over a map.
func environment(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// nothing is the empty environment.
func nothing(string) string { return "" }

// errorsText is every text an error shows: itself under each verb, and each
// error it wraps.
func errorsText(err error) []string {
	var out []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		out = append(out, e.Error(), fmt.Sprintf("%v", e), fmt.Sprintf("%+v", e), fmt.Sprintf("%#v", e), fmt.Sprintf("%s", e), fmt.Sprintf("%q", e))
	}
	return out
}
