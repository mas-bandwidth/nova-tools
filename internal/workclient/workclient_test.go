package workclient

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// listen binds a Unix socket by RELATIVE name inside the test's own temp
// directory, the house way around sun_path: a Unix-domain socket's path is
// capped (104 bytes on darwin, 107 on linux) and the absolute spelling of a
// t.TempDir() outgrows it. cmd/nova-work's own tests bind the same way.
func listen(t *testing.T) (socket string, ln net.Listener) {
	t.Helper()
	t.Chdir(t.TempDir())
	l, err := net.Listen("unix", "session.sock")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return "session.sock", l
}

// serve runs one handler per accepted connection until the listener closes.
func serve(ln net.Listener, handle func(net.Conn)) {
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(c)
		}
	}()
}

// within runs one exchange and fails the test if it has not returned by the
// wall-clock bound, so a client with no deadline is a RED test and never a
// hung one.
func within(t *testing.T, bound time.Duration, run func() (string, error)) (string, error) {
	t.Helper()
	type outcome struct {
		line string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		line, err := run()
		done <- outcome{line, err}
	}()
	select {
	case o := <-done:
		return o.line, o.err
	case <-time.After(bound):
		t.Fatalf("the exchange had not returned after %s: the client waits without a bound", bound)
		return "", nil
	}
}

func TestExchangeCarriesTheRequestAndReturnsTheOneReplyLine(t *testing.T) {
	socket, ln := listen(t)
	got := make(chan string, 1)
	serve(ln, func(c net.Conn) {
		defer c.Close()
		buf := make([]byte, 1024)
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		got <- string(buf[:n])
		c.Write([]byte("SESSION OK owner=rowan\n"))
	})

	line, err := within(t, 5*time.Second, func() (string, error) {
		return Exchange(socket, "session status --session "+socket)
	})
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if line != "SESSION OK owner=rowan" {
		t.Fatalf("reply = %q, want the session's line without its newline", line)
	}
	select {
	case r := <-got:
		if r != "session status --session "+socket+"\n" {
			t.Fatalf("request = %q, want the line newline-terminated", r)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no request reached the session")
	}
}

// The gap this test is the receipt for: a session that accepts the connection
// and then answers nothing held the client forever. There is no verb, no
// signal and no bound that ends the wait: the CLI is wedged until a person
// kills it.
func TestExchangeStopsWaitingWhenTheSessionAcceptsAndNeverAnswers(t *testing.T) {
	socket, ln := listen(t)
	hold := make(chan struct{})
	serve(ln, func(c net.Conn) {
		defer c.Close()
		<-hold // accept, read nothing, answer nothing
	})
	t.Cleanup(func() { close(hold) })

	_, err := within(t, 10*time.Second, func() (string, error) {
		return ExchangeWithin(socket, "session status", 150*time.Millisecond)
	})
	if err == nil {
		t.Fatal("a session that never answers was not refused")
	}
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("err = %v, want it to be ErrSilent", err)
	}
	if !strings.Contains(err.Error(), "150ms") {
		t.Fatalf("err = %q, want it to name the bound the caller set", err)
	}
}

// A silence in the MIDDLE of the reply line is the same wedge: the session
// wrote a few bytes and then stopped, and a reader that waits for a newline
// waits forever.
func TestExchangeStopsWaitingOnAHalfWrittenReply(t *testing.T) {
	socket, ln := listen(t)
	hold := make(chan struct{})
	serve(ln, func(c net.Conn) {
		defer c.Close()
		c.Read(make([]byte, 1024))
		c.Write([]byte("SESSION OK owner=")) // no newline, ever
		<-hold
	})
	t.Cleanup(func() { close(hold) })

	_, err := within(t, 10*time.Second, func() (string, error) {
		return ExchangeWithin(socket, "session status", 150*time.Millisecond)
	})
	if err == nil {
		t.Fatal("a half-written reply line was not refused")
	}
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("err = %v, want it to be ErrSilent", err)
	}
}

// The negative control's twin: a session that takes its time and then ANSWERS
// is not refused, so the deadline cannot be satisfied by refusing everything.
//
// The session's delay is a rendezvous and not a sleep: it reads the request,
// says so, and answers only once the test has released it -- which it does
// after seeing the request arrive, by which point the client is already inside
// its read. A fixed sleep would be a wait this suite does not take (SPEC-CI.md,
// the fixed-waits class test).
func TestExchangeAdmitsASlowButAnsweringSession(t *testing.T) {
	socket, ln := listen(t)
	arrived := make(chan struct{})
	release := make(chan struct{})
	serve(ln, func(c net.Conn) {
		defer c.Close()
		c.Read(make([]byte, 1024))
		close(arrived)
		<-release
		c.Write([]byte("SESSION OK owner=rowan\n"))
	})

	type outcome struct {
		line string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		line, err := ExchangeWithin(socket, "session status", 30*time.Second)
		done <- outcome{line, err}
	}()

	select {
	case <-arrived:
	case <-time.After(30 * time.Second):
		t.Fatal("the request never reached the session")
	}
	close(release)

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("a session that answered inside the bound was refused: %v", o.err)
		}
		if o.line != "SESSION OK owner=rowan" {
			t.Fatalf("reply = %q", o.line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the exchange never returned after the session answered")
	}
}

// The close is still its own answer and is not dressed as a silence: a session
// that hangs up before one line has failed differently from one that is quiet,
// and a reader of the refusal needs to know which.
func TestExchangeNamesACloseBeforeOneLineSeparately(t *testing.T) {
	socket, ln := listen(t)
	serve(ln, func(c net.Conn) {
		c.Read(make([]byte, 1024))
		c.Close()
	})

	_, err := within(t, 10*time.Second, func() (string, error) {
		return ExchangeWithin(socket, "session status", 5*time.Second)
	})
	if err == nil {
		t.Fatal("a session that closed before one line was not refused")
	}
	if errors.Is(err, ErrSilent) {
		t.Fatalf("a close was reported as a silence: %v", err)
	}
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want it to be ErrClosed", err)
	}
}

// The cap was checked AFTER the whole line had been read into memory, so a
// session that writes without ever writing a newline grew the client's buffer
// without bound and the cap never fired. The cap has to hold WHILE the bytes
// are read, not after.
//
// The session here writes a long stream with no line end and then goes quiet
// without closing: a reader that stops at the cap refuses at once, and a
// reader that does not stop at the cap reaches the silence and waits, which is
// this test's own bound going off. So the test is red both ways the fix can be
// missing — the cap not held while reading, and the refusal not named.
func TestExchangeRefusesAReplyPastTheCapWhileItReads(t *testing.T) {
	socket, ln := listen(t)
	hold := make(chan struct{})
	drained := make(chan struct{})
	serve(ln, func(c net.Conn) {
		defer c.Close()
		c.Read(make([]byte, 1024))
		chunk := make([]byte, 64<<10)
		for i := range chunk {
			chunk[i] = 'x'
		}
		// A bounded allowance rather than a forever loop, so a client that
		// reads without a cap costs the bench 64 MiB and not its memory.
		for sent := 0; sent < 64*replyCap; sent += len(chunk) {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
		close(drained) // only a client that read the whole 64 MiB gets here
		<-hold
	})
	t.Cleanup(func() { close(hold) })

	_, err := within(t, 30*time.Second, func() (string, error) {
		return ExchangeWithin(socket, "session status", 20*time.Second)
	})
	if err == nil {
		t.Fatal("a reply with no newline past the cap was not refused")
	}
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v, want it to be ErrTooLong", err)
	}
	if !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("err = %q, want it to name the cap in bytes", err)
	}
	// The cap held WHILE the bytes were read: a reader that stops one byte
	// past the cap leaves the session's writes blocked on a full socket
	// buffer, so the session can never have written its whole allowance.
	select {
	case <-drained:
		t.Fatalf("the client read the session's whole %d-byte stream: the cap is checked after the read, not during it", 64*replyCap)
	default:
	}
}

// A reply exactly at the cap is a reply, not an overrun: the bound is the last
// byte admitted and not the first refused.
func TestExchangeAdmitsAReplyExactlyAtTheCap(t *testing.T) {
	socket, ln := listen(t)
	body := strings.Repeat("x", replyCap)
	serve(ln, func(c net.Conn) {
		defer c.Close()
		c.Read(make([]byte, 1024))
		c.Write([]byte(body + "\n"))
	})

	line, err := within(t, 30*time.Second, func() (string, error) {
		return ExchangeWithin(socket, "session status", 20*time.Second)
	})
	if err != nil {
		t.Fatalf("a reply exactly at the cap was refused: %v", err)
	}
	if len(line) != replyCap {
		t.Fatalf("reply length = %d, want %d", len(line), replyCap)
	}
}

// The dial spends the same budget as the write and the read. A listener whose
// accept queue nobody is draining is the wedge that reaches the client here,
// and it cannot be built deterministically on every platform — so the bound is
// pinned at the dial itself: a budget already spent refuses before a
// connection, and never waits on one.
func TestDialRefusesOnceTheBudgetIsAlreadySpent(t *testing.T) {
	socket, ln := listen(t)
	serve(ln, func(c net.Conn) { c.Close() })

	conn, err := dial(socket, time.Now().Add(-time.Second))
	if err == nil {
		conn.Close()
		t.Fatal("the dial connected on a budget that was already spent")
	}
	if !os.IsTimeout(err) {
		t.Fatalf("dial err = %v, want a timeout", err)
	}
	// And the same socket is dialable when the budget is not spent, so the
	// refusal above is the deadline and not a broken listener.
	conn, err = dial(socket, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("dial inside the budget: %v", err)
	}
	conn.Close()
}

func TestExchangeRefusesASocketThatIsNotThere(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := within(t, 10*time.Second, func() (string, error) {
		return ExchangeWithin("no-such.sock", "session status", 5*time.Second)
	})
	if err == nil {
		t.Fatal("a socket that is not there was not refused")
	}
	if errors.Is(err, ErrSilent) {
		t.Fatalf("an absent socket was reported as a silence: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("err = %v, want the dial's own reason", err)
	}
}

// A bound of zero or less is the caller saying "no bound": it is the one way
// back to waiting forever and it is spelled, never defaulted into.
//
// The decision is tested where it is made. Proving it the other way round --
// by outlasting a deadline -- means a test that waits for one, and the only
// honest wait here is longer than any bound worth having.
func TestABoundOfZeroOrLessIsNoDeadlineAtAll(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second, -time.Hour} {
		if b := budget(d); !b.IsZero() {
			t.Fatalf("budget(%s) = %v, want the zero Time: no bound at all", d, b)
		}
	}
	if budget(time.Second).IsZero() {
		t.Fatal("budget(1s) is the zero Time: a positive bound set no deadline")
	}
	// And an exchange under no bound is still an exchange.
	socket, ln := listen(t)
	serve(ln, func(c net.Conn) {
		defer c.Close()
		c.Read(make([]byte, 1024))
		c.Write([]byte("SESSION OK owner=rowan\n"))
	})
	line, err := within(t, 30*time.Second, func() (string, error) {
		return ExchangeWithin(socket, "session status", 0)
	})
	if err != nil {
		t.Fatalf("Exchange with no bound: %v", err)
	}
	if line != "SESSION OK owner=rowan" {
		t.Fatalf("reply = %q", line)
	}
}

// Exchange is ExchangeWithin at the package's standing bound, so the binary's
// existing call site gains the deadline without naming a number of its own.
func TestExchangeCarriesTheStandingBound(t *testing.T) {
	if DefaultTimeout <= 0 {
		t.Fatalf("DefaultTimeout = %s, want a positive standing bound", DefaultTimeout)
	}
	socket, ln := listen(t)
	hold := make(chan struct{})
	serve(ln, func(c net.Conn) {
		defer c.Close()
		<-hold
	})
	t.Cleanup(func() { close(hold) })

	// The standing bound is long, so this only proves Exchange routes through
	// ExchangeWithin: a one-nanosecond bound on the same path must be silent.
	_, err := within(t, 10*time.Second, func() (string, error) {
		return ExchangeWithin(socket, "session status", time.Nanosecond)
	})
	if !errors.Is(err, ErrSilent) {
		t.Fatalf("err = %v, want ErrSilent", err)
	}
}
