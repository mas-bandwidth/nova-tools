// Package workclient is the S1 wire of the resident work session's thin client:
// one request line in, one reply line out over the Unix-domain socket
// --session names, newline-terminated both ways.
//
// It lives here rather than in cmd/nova-work so that the binary keeps a single
// classified print path: the raw .Write of a socket is the one thing the
// package's source-level audit cannot classify, and moving the dial and the
// read out of the binary lets every fmt call in package main remain escaped.
//
// Every exchange is bounded in TWO directions, because a thin client that a
// session can hold forever is not thin, it is hostage. The wall-clock bound is
// ExchangeWithin's, applied to the dial, the write and the read as one budget:
// a session that accepts the connection and then says nothing, or that writes
// half a line and stops, is refused as ErrSilent naming the bound, where
// before it held the CLI until a person killed it. The byte bound is
// replyCap's, and it holds WHILE the reply is read rather than after: a
// session that writes without ever writing a newline is refused as ErrTooLong
// at the cap, where before its bytes were buffered whole and the cap was
// checked against a line that had already been paid for.
package workclient

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// replyCap bounds the one reply line, refused whole rather than truncated, the
// family's own law in miniature. One MiB is the S1 wire's standing bound; the
// framed protocol the spec pins carries its own.
const replyCap = 1 << 20

// DefaultTimeout is the standing wall-clock bound on one exchange, the one
// Exchange applies so that the binary's call sites gain the bound without each
// naming a number of its own. It is deliberately generous: the session's own
// verbs are bounded and indexed, so anything past this is a wedge and not slow
// work — and the long operations the spec names return an operation id at once
// rather than holding the connection (docs/SPEC-WORK.md, "The engine and its
// client": "the CLI may exit while the work continues").
//
// THE NUMBER ITSELF IS NOT THE SPEC'S. docs/SPEC-WORK.md bounds the frame, the
// page and the operation wait, and names --deadline as the stamp "after it the
// caller stops waiting for this answer", but fixes no default for a client
// whose verb carries no deadline. Thirty seconds is this package's choice, held
// in one named constant so that the spec's answer replaces one line.
const DefaultTimeout = 30 * time.Second

// The three ways one exchange can fail to become a reply line. They are
// separate sentinels because a caller — and the person reading its refusal —
// acts differently on each: a silence is a session to go and look at, a close
// is a session that answered by hanging up, and an overrun is a session
// speaking a shape this wire does not carry.
var (
	// ErrSilent: the session did not complete the exchange inside the bound.
	// The work, if it was a mutation, may still have been accepted: a socket
	// disconnect is never a rollback (docs/SPEC-WORK.md, "The engine and its
	// client"), and neither is a client that stopped waiting.
	ErrSilent = errors.New("the session did not answer inside the bound")
	// ErrClosed: the peer hung up before one whole line.
	ErrClosed = errors.New("the session closed before one line")
	// ErrTooLong: the reply passed the cap with no newline, refused whole
	// rather than truncated.
	ErrTooLong = errors.New("the session's reply is past the cap")
)

// Exchange dials the session's socket, writes the request line and returns the
// one reply line without its newline, under the package's standing bound.
func Exchange(socket, request string) (string, error) {
	return ExchangeWithin(socket, request, DefaultTimeout)
}

// ExchangeWithin is Exchange under a caller's own wall-clock bound, spent
// across the dial, the write and the read as ONE budget: the bound is how long
// the caller will wait for this answer in total, not how long each syscall may
// take, because three separate bounds are three ways to wait 3n. A bound of
// zero or less sets no deadline at all, which is the one way back to waiting
// forever and has to be spelled.
func ExchangeWithin(socket, request string, within time.Duration) (string, error) {
	deadline := budget(within)
	conn, err := dial(socket, deadline)
	if err != nil {
		// A dial that times out is the same wedge as a silent reply — a
		// listener whose accept queue nobody is draining — so it is named the
		// same way; every other dial error keeps its own reason, because "no
		// such file" is the caller's own spelling of the socket and not the
		// session's silence.
		return "", classify(err, within)
	}
	defer conn.Close()
	if !deadline.IsZero() {
		// One deadline covers the write and the read: it is a wall-clock
		// instant, so it cannot be refreshed into an unbounded wait by a
		// session that dribbles a byte at a time.
		if err := conn.SetDeadline(deadline); err != nil {
			return "", err
		}
	}
	if _, err := conn.Write([]byte(request + "\n")); err != nil {
		return "", classify(err, within)
	}
	return readReply(conn, within)
}

// budget turns a caller's bound into the instant the exchange must be over by.
// A bound of zero or less is the caller saying "no bound", and answers the zero
// Time: the one way back to waiting forever, and it has to be spelled.
func budget(within time.Duration) time.Time {
	if within <= 0 {
		return time.Time{}
	}
	return time.Now().Add(within)
}

func dial(socket string, deadline time.Time) (net.Conn, error) {
	if deadline.IsZero() {
		return net.Dial("unix", socket)
	}
	d := net.Dialer{Deadline: deadline}
	return d.Dial("unix", socket)
}

// readReply reads at most replyCap+1 bytes and never one more: the limit is
// the reader's, so the refusal costs the cap and not the whole of whatever the
// session meant to send. replyCap+1 is the smallest window that can tell a
// reply exactly at the cap (the last byte admitted) from the first byte past
// it.
func readReply(conn net.Conn, within time.Duration) (string, error) {
	r := bufio.NewReader(io.LimitReader(conn, replyCap+1))
	line, err := r.ReadString('\n')
	switch {
	case err == nil:
		return strings.TrimSuffix(line, "\n"), nil
	case len(line) > replyCap:
		// The limit was reached with no newline in it: the reply is past the
		// cap whatever the session would have written next.
		return "", fmt.Errorf("%w: %d bytes with no line end", ErrTooLong, replyCap)
	default:
		return "", classify(err, within)
	}
}

// classify names which of the three failures this was. A timeout is asked
// first, because a deadline that fires while a read is in flight surfaces as a
// net.Error and never as io.EOF, and calling it a close would send a person
// looking for a session that is in fact still sitting there.
func classify(err error, within time.Duration) error {
	switch {
	case os.IsTimeout(err):
		return fmt.Errorf("%w of %s", ErrSilent, within)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return ErrClosed
	default:
		return err
	}
}
