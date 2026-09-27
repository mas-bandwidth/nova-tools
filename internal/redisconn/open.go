package redisconn

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

// The bounds. Nothing in this package waits without one of them, and a
// context's own deadline, when it comes sooner, bounds the same step.
const (
	// OpenTimeout bounds Open as a whole: the dial and the handshake
	// together.
	OpenTimeout = 5 * time.Second
	// DialTimeout bounds every dial a connection makes after Open: one
	// attempt, never a second.
	DialTimeout = 5 * time.Second
	// WriteTimeout bounds the write of every command and pipeline.
	WriteTimeout = 5 * time.Second
	// ReadTimeout bounds the read of every reply. A command that asks the
	// store to block (BLPOP, XREAD BLOCK) is given the time it asked for and
	// ten seconds more.
	ReadTimeout = 5 * time.Second
	// PoolTimeout bounds the wait for a free connection when every
	// connection of the client is in use.
	PoolTimeout = 5 * time.Second
)

// Conn is an open connection to the store: a go-redis client that was
// dialed, shook hands and logged in before Open returned it. It holds no
// field a formatting verb can print the password from.
type Conn struct {
	client *redis.Client
	login  login
	hide   func(string) string
}

// Open resolves the options (Resolve: what was given first, then getenv's
// environment), dials the store once and returns the connection only when
// the store has answered the handshake and accepted the login. It returns
// within OpenTimeout, or sooner when ctx ends sooner.
//
// What Open costs is the connect and one exchange, the handshake (HELLO 3,
// carrying the login when there is one). It sends no PING, no CLIENT
// SETINFO and nothing else, so a tool's first command is its first round
// trip. A store that refuses HELLO (one older than Redis 6, or one that
// wants a login and was given none) is asked a second time, by a PING sent
// for real, and its answer decides.
//
// Open makes one dial attempt and no retry. After Open the same holds for
// every command: it is sent at most once, a connection that broke is dialed
// again by the next command, once, and nothing waits longer than the bounds
// of this package. The password is read from getenv when Open runs and at
// no other time.
//
// An error is one of this package's (Classify): Unreachable, AuthRefused
// or Other, with one line that names what was tried and the next thing to
// do, and it never holds the password. On an error nothing is left open.
func Open(ctx context.Context, o Options, getenv func(string) string) (*Conn, error) {
	return open(ctx, o, getenv, netDial)
}

// dialFunc dials one connection; tests hand open a dialer of their own.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func netDial(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{Timeout: DialTimeout}).DialContext(ctx, network, addr)
}

func open(ctx context.Context, o Options, getenv func(string) string, dial dialFunc) (*Conn, error) {
	l, password, err := resolve(o, getenv)
	if err != nil {
		return nil, err
	}
	quietOnce.Do(func() { redis.SetLogger(quiet{}) })

	first := &firstDial{dial: dial}
	first.opening.Store(true)
	c := &Conn{login: l, hide: hider(password)}
	c.client = redis.NewClient(&redis.Options{
		Network: network(l.Addr),
		Addr:    l.Addr,
		Dialer:  first.dialer,
		// The login is handed over by a function, so no option of the client
		// holds the password as a field.
		CredentialsProviderContext: func(context.Context) (string, string, error) {
			return l.User, password, nil
		},
		Protocol: 3,

		DialTimeout:           DialTimeout,
		ReadTimeout:           ReadTimeout,
		WriteTimeout:          WriteTimeout,
		PoolTimeout:           PoolTimeout,
		ContextTimeoutEnabled: true,
		DialerRetries:         1,
		MaxRetries:            -1,

		// The connect is HELLO alone: no CLIENT SETINFO, no CLIENT
		// MAINT_NOTIFICATIONS, and no lookup of the host's name when the
		// client is made (go-redis does one to choose an endpoint type).
		DisableIdentity: true,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode:         maintnotifications.ModeDisabled,
			EndpointType: maintnotifications.EndpointTypeNone,
		},
	})

	ctx, cancel := context.WithTimeout(ctx, OpenTimeout)
	defer cancel()
	err = c.client.Do(ctx, probe).Err()
	first.done()
	if err != nil {
		// Closing the client closes every connection it holds, and the one
		// Open dialed is closed here as well: go-redis drops a connection
		// whose handshake failed without closing its socket.
		_ = c.client.Close()
		first.hangUp()
		return nil, explain(l, c.hide, err, true)
	}
	return c, nil
}

// quiet is go-redis's logger under this package: nothing. A failure comes
// back as an error, once; go-redis would also write it to standard error,
// ahead of the one line a tool prints. SetLogger writes a variable of the
// go-redis package, so it is set once for the process, by the first Open.
type quiet struct{}

// Printf writes nothing.
func (quiet) Printf(context.Context, string, ...interface{}) {}

var quietOnce sync.Once

// Client is the go-redis client, for the caller's commands and pipelines.
// It is the same client for the life of the connection.
func (c *Conn) Client() *redis.Client { return c.client }

// Close closes the connection and every socket it holds. Closing twice, and
// closing a nil *Conn, is nil.
func (c *Conn) Close() error {
	if c == nil {
		return nil
	}
	if err := c.client.Close(); !errors.Is(err, redis.ErrClosed) {
		return err
	}
	return nil
}

// String names the store, the user and the variable the password came from,
// on one line: what every message of this connection opens with. It holds
// no secret.
func (c *Conn) String() string { return oneline.Escape(c.login.tried()) }

// Explain turns an error that a command on this connection returned into
// this package's error: its class (Classify), and one line that names the
// store and the login that were tried, what came back and the next thing to
// do, with the password taken out of any text that held it. nil stays nil,
// and an error that is already this package's comes back as it is.
func (c *Conn) Explain(err error) error {
	if err == nil || isFailure(err) {
		return err
	}
	return explain(c.login, c.hide, err, false)
}

// The probe: the command Open sends through go-redis to make it dial and
// shake hands now, the bytes go-redis writes for it, and the answer the
// first connection gives in the store's place.
const (
	probe       = "PING"
	probeWire   = "*1\r\n$4\r\nPING\r\n"
	probeAnswer = "+PONG\r\n"
)

// firstDial is the dialer of one client. While Open runs, the connection it
// dials is a firstConn.
type firstDial struct {
	dial    dialFunc
	opening atomic.Bool
	first   atomic.Pointer[firstConn]
}

func (d *firstDial) dialer(ctx context.Context, network, addr string) (net.Conn, error) {
	nc, err := d.dial(ctx, network, addr)
	if err != nil || !d.opening.Load() {
		return nc, err
	}
	first := &firstConn{Conn: nc}
	d.first.Store(first)
	if sys, ok := nc.(syscall.Conn); ok {
		return firstSysConn{firstConn: first, sys: sys}, nil
	}
	return first, nil
}

// done ends Open's part: from here on every connection this dialer made or
// makes is an ordinary one.
func (d *firstDial) done() {
	d.opening.Store(false)
	if first := d.first.Load(); first != nil {
		first.disarm()
	}
}

// hangUp closes the connection Open dialed, if it dialed one.
func (d *firstDial) hangUp() {
	if first := d.first.Load(); first != nil {
		_ = first.Close()
	}
}

// The states of a firstConn. Its TLA+ model is owed; firstconn_test.go holds
// the rules over every order of events up to six.
const (
	watching  int32 = iota // nothing has come back from the store yet
	armed                  // the store accepted the handshake
	answering              // the probe was taken; its answer waits to be read
	inert                  // an ordinary connection, for good
)

// firstConn is the connection Open dials, and the reason Open costs one
// exchange and not two.
//
// go-redis shakes hands inside the first command that uses a connection and
// offers no way to ask for the handshake alone, so Open sends a probe. The
// handshake's reply is what verifies the connection; the probe only sets the
// handshake going. firstConn watches the first byte the store sends: a
// RESP3 map ('%') is HELLO accepted, and then, and only then, the probe is
// answered here and never written to the store. Anything else the store
// says first, and any write that is not exactly the probe, makes the
// connection inert, so the probe travels and the store's own answer decides.
//
// Requests and replies alternate on a connection, so the answer given here
// stands exactly where the store's would have stood. Once Open returns
// (disarm) no write is taken again: a caller's own PING reaches the store.
type firstConn struct {
	net.Conn
	state  atomic.Int32
	answer atomic.Int32 // bytes of probeAnswer already read
}

// Read reads from the store, except for the answer to a probe taken by
// Write, which is read from here.
func (c *firstConn) Read(p []byte) (int, error) {
	switch c.state.Load() {
	case answering:
		at := int(c.answer.Load())
		n := copy(p, probeAnswer[at:])
		if c.answer.Add(int32(n)) == int32(len(probeAnswer)) {
			c.state.Store(inert)
		}
		return n, nil
	case watching:
		n, err := c.Conn.Read(p)
		if n > 0 {
			next := inert
			if p[0] == '%' {
				next = armed
			}
			c.state.CompareAndSwap(watching, next)
		}
		return n, err
	}
	return c.Conn.Read(p)
}

// Write writes to the store, except for the probe of an armed connection,
// which is taken here.
func (c *firstConn) Write(p []byte) (int, error) {
	if c.state.Load() == armed {
		if bytes.Equal(p, []byte(probeWire)) && c.state.CompareAndSwap(armed, answering) {
			return len(p), nil
		}
		c.state.CompareAndSwap(armed, inert)
	}
	return c.Conn.Write(p)
}

// disarm makes the connection inert unless an answer is still to be read.
func (c *firstConn) disarm() {
	c.state.CompareAndSwap(watching, inert)
	c.state.CompareAndSwap(armed, inert)
}

// firstSysConn is a firstConn over a socket: it hands out the socket's raw
// connection, which go-redis asks for to see whether an idle connection is
// still alive.
type firstSysConn struct {
	*firstConn
	sys syscall.Conn
}

// SyscallConn is the socket's own raw connection.
func (c firstSysConn) SyscallConn() (syscall.RawConn, error) { return c.sys.SyscallConn() }
