// Package delayproxy is a TCP proxy that holds every write of its clients back
// by a fixed time before it forwards it: a store that stands far away, made on
// the loopback. A test reaches it through testredis.Far, a person through
// tools/fardelay; both run this one proxy.
//
// WHAT IS DELAYED. The direction from the client to the target, and only it.
// A reply goes straight back, so one command and its reply cost the delay once:
// a delay of 128ms is a store 128ms away by round trip.
//
// A WRITE IS WHAT ONE READ RETURNS. The proxy cannot see a client's writes, it
// sees what a read of the client's connection returns. A client's write of up
// to ChunkBytes (16 KiB) is one read on the loopback, so it is held once: a
// pipeline the client writes in one go pays the delay once however many
// commands it holds, up to the window below, and three commands sent one after
// the other, each waiting for its reply, pay it three times. A write larger
// than ChunkBytes is several reads.
//
// A LINE, NOT A QUEUE. Every read is stamped with the time it arrived and is
// forwarded when arrival + delay is reached, never a delay after the read
// before it. A large write that comes in as several reads leaves as a run and
// pays the delay once, the way a long wire delays every bit by the same time
// and not each bit by the time of the one ahead of it. Order is kept.
//
// A WINDOW, LIKE A LINK. A connection queues at most 256 reads between the
// client and the target, WindowBytes (4 MiB) of full reads, and has two more in
// hand: the one the forwarding half waits to send and the one the reading half
// cannot yet queue. A pipeline of up to the window pays the delay once. A client
// that writes more is held back by TCP until the reads ahead of it have gone,
// and what it writes then is stamped when it is read: each further window pays
// the delay once more, so a pipeline of 100,000 commands of 45 bytes (4.3 MiB)
// pays it twice, and one of 11.5 MiB three times, as it would across a long link
// with a window of that size.
//
// BOUNDED. The proxy runs one goroutine to accept, and GoroutinesPerConn
// (three) for each open connection: the replies back, the client's reads, the
// delay. At most Options.MaxConns connections are open; a client past the
// bound waits in the listener's backlog until one ends, and is never served
// out of turn. If the listener itself fails, the proxy closes it, so a client
// is refused and not left waiting in a backlog nothing reads.
//
// IT ENDS WHEN ASKED. Stop closes the listener and every connection and
// returns when every goroutine the proxy started has returned: nothing it
// started runs after Stop. A connection ends when either side ends it; a
// client that hangs up its sending side still gets the replies to what it
// sent, including what was still held.
//
// Nothing here names a host, a store or a tool. Who may be listened on or
// forwarded to is the caller's rule: see Loopback.
package delayproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMaxConns is the open connections a proxy serves at once when
	// Options.MaxConns is not set: three goroutines each.
	DefaultMaxConns = 1024

	// MaxDelay is the longest delay Serve accepts. A store is never 30 seconds
	// away; a larger number is a unit typed wrong.
	MaxDelay = 30 * time.Second

	// ChunkBytes is the most one read of a client returns, and so the largest
	// write that is held once.
	ChunkBytes = 16 << 10

	// GoroutinesPerConn is what an open connection runs: one to copy the
	// replies back, one to read the client and one to hold and forward.
	GoroutinesPerConn = 3

	// inFlight is how many reads one connection queues at most between the
	// client and the target, beside the two it has in hand.
	inFlight = 256

	// WindowBytes is what the queue of a connection holds when its reads are
	// full, inFlight reads of ChunkBytes: a pipeline of up to this pays the delay
	// once, and each further WindowBytes pays it once more.
	WindowBytes = inFlight * ChunkBytes

	// dialBound is how long one connection to the target may take.
	dialBound = 10 * time.Second

	// noHold is Shortest's stored value while nothing has been forwarded.
	noHold = -1

	// portMin and portMax are the ports of a TCP address: a listener may ask
	// for 0, a port the kernel picks, and a target may not.
	portMin = 1
	portMax = 65535
)

// Clock is what the proxy asks the time of. The zero Clock is the real one; a
// test puts its own in, so that "each write is delayed once" is a count of
// waits and never a measurement of a machine's load.
type Clock struct {
	// Now is the time. Nil is time.Now.
	Now func() time.Time
	// Wait returns when d has passed and reports true, or returns at once and
	// reports false when stop is closed first. Nil waits on a timer.
	Wait func(stop <-chan struct{}, d time.Duration) bool
}

func (c Clock) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c Clock) wait(stop <-chan struct{}, d time.Duration) bool {
	if c.Wait != nil {
		return c.Wait(stop, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stop:
		return false
	}
}

// Options are what Serve may be asked for beyond the target and the delay.
type Options struct {
	// MaxConns is the open connections served at once. Zero is
	// DefaultMaxConns; a negative number is refused.
	MaxConns int
	// Clock is the time the proxy uses. The zero Clock is the real one.
	Clock Clock
	// Dial connects one client to the target: network is "tcp" and address is
	// the target Serve was given, and ctx ends when the connection has not
	// been made in time or the proxy stops. Nil dials TCP. A test puts its own
	// in, so that no port is dialled.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Logf is told what the proxy cannot tell a client: a target that did
	// not answer, a listener that failed. It may be called from any of the
	// proxy's goroutines and never after Stop returns. Nil says nothing.
	Logf func(format string, args ...any)
}

// Proxy is one running proxy: a listener, its target and its delay.
type Proxy struct {
	ln     net.Listener
	target string
	delay  time.Duration
	clock  Clock
	dial   func(ctx context.Context, network, address string) (net.Conn, error)
	logf   func(format string, args ...any)

	slots  chan struct{} // one token per open connection; its capacity is the bound
	stop   chan struct{} // closed by Stop
	ctx    context.Context
	cancel context.CancelFunc
	end    sync.Once
	wg     sync.WaitGroup // every goroutine the proxy started
	live   atomic.Int64   // the goroutines running now

	mu      sync.Mutex
	stopped bool
	ends    map[uint64]func() // how to end each open connection
	next    uint64

	writes   atomic.Int64 // writes forwarded
	shortest atomic.Int64 // the least time any of them was held, in nanoseconds; noHold before the first
}

// Listen listens on addr, a host:port, and serves it: Serve over net.Listen.
// Port 0 lets the kernel pick; Addr says which.
func Listen(addr, target string, delay time.Duration, opts Options) (*Proxy, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}
	p, err := Serve(ln, target, delay, opts)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	return p, nil
}

// Serve accepts connections on ln and forwards each to target, a host:port
// dialled once per client connection, holding every write of the client back
// by delay. It owns ln from here on: Stop closes it. It refuses a target that
// is not a host and a port, a delay below zero or above MaxDelay, and a
// negative MaxConns, and starts nothing.
func Serve(ln net.Listener, target string, delay time.Duration, opts Options) (*Proxy, error) {
	if err := Target(target); err != nil {
		return nil, fmt.Errorf("target %q: %w", target, err)
	}
	if delay < 0 || delay > MaxDelay {
		return nil, fmt.Errorf("delay %v is outside 0 to %v", delay, MaxDelay)
	}
	if opts.MaxConns < 0 {
		return nil, fmt.Errorf("MaxConns %d is below zero", opts.MaxConns)
	}
	bound := opts.MaxConns
	if bound == 0 {
		bound = DefaultMaxConns
	}
	p := &Proxy{
		ln:     ln,
		target: target,
		delay:  delay,
		clock:  opts.Clock,
		dial:   opts.Dial,
		logf:   opts.Logf,
		slots:  make(chan struct{}, bound),
		stop:   make(chan struct{}),
		ends:   map[uint64]func(){},
	}
	if p.dial == nil {
		p.dial = (&net.Dialer{}).DialContext
	}
	if p.logf == nil {
		p.logf = func(string, ...any) {}
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.shortest.Store(noHold)
	p.spawn(nil, p.accept)
	return p, nil
}

// Target refuses an address that is not a host and a port to forward to: no
// host, or a port that is not 1 to 65535. Serve applies it to its target; a
// caller that wants the refusal before it listens applies it first.
func Target(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("no host")
	}
	if n, err := strconv.Atoi(port); err != nil || n < portMin || n > portMax {
		return fmt.Errorf("port %q is not %d to %d", port, portMin, portMax)
	}
	return nil
}

// Loopback refuses a host:port to listen on or forward to that is not on this
// machine's loopback: an IP literal in 127.0.0.0/8 or ::1, or the name
// localhost, with a port from 0 (the kernel picks) to 65535. It is the rule
// for a proxy that a test or a tool must not turn into a way out of the
// machine, or a way in to it; the proxy itself forwards wherever it is told.
func Loopback(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > portMax {
		return fmt.Errorf("port %q is not 0 to %d", port, portMax)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%q is not a loopback address (an IP in 127.0.0.0/8 or ::1, or localhost)", host)
}

// Addr is the address the proxy listens on, host:port.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// Writes is how many writes have been held and forwarded to the target. A
// write larger than 16 KiB counts once for each read it came in as.
func (p *Proxy) Writes() int { return int(p.writes.Load()) }

// Shortest is the least time any forwarded write was held, by the proxy's own
// clock, from the read that returned it to the moment it was sent on: never
// less than the delay while the proxy works. Zero before the first write.
func (p *Proxy) Shortest() time.Duration {
	if n := p.shortest.Load(); n != noHold {
		return time.Duration(n)
	}
	return 0
}

// Live is how many goroutines the proxy is running now: at most
// one to accept plus GoroutinesPerConn for each of MaxConns, and 0 once Stop
// has returned.
func (p *Proxy) Live() int { return int(p.live.Load()) }

// Stop closes the listener and every connection and returns when every
// goroutine the proxy started has returned. A second call only waits.
func (p *Proxy) Stop() {
	p.end.Do(func() {
		close(p.stop)
		p.cancel()
		_ = p.ln.Close()
		p.mu.Lock()
		p.stopped = true
		ends := make([]func(), 0, len(p.ends))
		for _, end := range p.ends {
			ends = append(ends, end)
		}
		p.mu.Unlock()
		for _, end := range ends {
			end()
		}
	})
	p.wg.Wait()
}

// spawn runs f on a goroutine the proxy counts. conn, when set, is the
// connection's own count, so a connection can wait for its goroutines. It is
// called only from goroutines the proxy already counts, or before Serve
// returns, so Stop's Wait never starts ahead of an Add.
func (p *Proxy) spawn(conn *sync.WaitGroup, f func()) {
	p.wg.Add(1)
	p.live.Add(1)
	if conn != nil {
		conn.Add(1)
	}
	go func() {
		defer p.wg.Done()
		defer p.live.Add(-1)
		if conn != nil {
			defer conn.Done()
		}
		f()
	}()
}

// accept takes a slot, then a client, so a client past the bound is never
// accepted and waits in the listener's backlog.
func (p *Proxy) accept() {
	for {
		select {
		case p.slots <- struct{}{}:
		case <-p.stop:
			return
		}
		client, err := p.ln.Accept()
		if err != nil {
			<-p.slots
			select {
			case <-p.stop:
			default:
				// Nothing will be accepted again. The listener is closed, so a
				// client is refused at once and not left in a backlog nobody
				// reads; the connections already open go on until they end.
				p.logf("delayproxy: no longer accepting on %s: %v; closing the listener", p.Addr(), err)
				_ = p.ln.Close()
			}
			return
		}
		p.spawn(nil, func() { p.serve(client) })
	}
}

// track records how to end a connection, and refuses it once Stop has run.
func (p *Proxy) track(end func()) (uint64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return 0, false
	}
	p.next++
	p.ends[p.next] = end
	return p.next, true
}

func (p *Proxy) untrack(id uint64) {
	p.mu.Lock()
	delete(p.ends, id)
	p.mu.Unlock()
}

// held is one read of a client on its way to the target.
type held struct {
	data []byte
	at   time.Time // when the read returned
	due  time.Time // at + delay: when it is sent on
}

// serve is one client connection: the replies are copied back at once on this
// goroutine, and two more hold and forward the requests.
func (p *Proxy) serve(client net.Conn) {
	defer func() { <-p.slots }()
	defer client.Close()
	dialCtx, cancelDial := context.WithTimeout(p.ctx, dialBound)
	up, err := p.dial(dialCtx, "tcp", p.target)
	cancelDial()
	if err != nil {
		select {
		case <-p.stop:
		default:
			p.logf("delayproxy: dialling %s: %v; the client is hung up on", p.target, err)
		}
		return
	}
	defer up.Close()

	done := make(chan struct{}) // closed when this connection is over
	var once sync.Once
	end := func() {
		once.Do(func() {
			close(done)
			_ = client.Close()
			_ = up.Close()
		})
	}
	id, ok := p.track(end)
	if !ok {
		end()
		return
	}
	defer p.untrack(id)

	queue := make(chan held, inFlight)
	var conn sync.WaitGroup
	p.spawn(&conn, func() { p.read(client, queue, done, end) })
	p.spawn(&conn, func() { p.forward(up, queue, done, end) })
	// Replies are not delayed. The copy ends when the target hangs up or the
	// client cannot be written to, and then the connection is over.
	_, _ = io.Copy(client, up)
	end()
	conn.Wait()
}

// read stamps every read of the client and queues it. The client's end of
// sending is the end of the queue, and what is queued is still forwarded; any
// other error ends the connection.
func (p *Proxy) read(client net.Conn, queue chan<- held, done <-chan struct{}, end func()) {
	defer close(queue)
	buf := make([]byte, ChunkBytes)
	for {
		n, err := client.Read(buf)
		if n > 0 {
			at := p.clock.now()
			h := held{data: append([]byte(nil), buf[:n]...), at: at, due: at.Add(p.delay)}
			select {
			case queue <- h:
			case <-done:
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				end()
			}
			return
		}
	}
}

// forward sends each read on when its delay is over, in the order they came.
// Once the client has said it will send no more and everything is sent, it
// says the same to the target.
func (p *Proxy) forward(up net.Conn, queue <-chan held, done <-chan struct{}, end func()) {
	for {
		var h held
		select {
		case got, ok := <-queue:
			if !ok {
				if half, can := up.(interface{ CloseWrite() error }); can {
					_ = half.CloseWrite()
				}
				return
			}
			h = got
		case <-done:
			return
		}
		if wait := h.due.Sub(p.clock.now()); wait > 0 && !p.clock.wait(done, wait) {
			return
		}
		p.record(p.clock.now().Sub(h.at))
		if _, err := up.Write(h.data); err != nil {
			end()
			return
		}
	}
}

// record counts one write and keeps the shortest time any was held.
func (p *Proxy) record(d time.Duration) {
	p.writes.Add(1)
	for {
		cur := p.shortest.Load()
		if cur != noHold && cur <= int64(d) {
			return
		}
		if p.shortest.CompareAndSwap(cur, int64(d)) {
			return
		}
	}
}
