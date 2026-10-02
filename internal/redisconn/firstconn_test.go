package redisconn

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The connection Open dials takes one write and answers it itself. These
// tests hold it to its rules over every order of events up to a depth, and
// over long orders drawn from fixed seeds; tla/FirstConn.tla holds the same
// rules over the same events as invariants, with reversed witnesses. The
// rules are stated on what went in and what came out, not on the states the
// code keeps:
//
//	Taken only when accepted: a write is taken only when it is the probe,
//	    the first byte the client read from the store is '%', nothing but
//	    the handshake was written before, no write was taken before, and
//	    Open has not returned. When all of that holds, it is taken.
//	Everything else travels: the store receives every other write, whole
//	    and in order.
//	The answer stands in the store's place: the client reads the store's
//	    bytes in order, with the answer whole, once, where the taken write's
//	    reply would have stood, and never otherwise.

// farEnd is the store's side of the connection under test: what it sent
// waits to be read, and what it received is kept.
type farEnd struct {
	net.Conn
	sent     []byte
	received []byte
}

var errNothingSent = errors.New("the store has sent nothing")

func (f *farEnd) Read(p []byte) (int, error) {
	if len(f.sent) == 0 {
		return 0, errNothingSent
	}
	n := copy(p, f.sent)
	f.sent = f.sent[n:]
	return n, nil
}

func (f *farEnd) Write(p []byte) (int, error) {
	f.received = append(f.received, p...)
	return len(p), nil
}

// The events of a connection's life.
const (
	sendAccepted = iota // the store sends bytes that begin a map
	sendRefused         // the store sends bytes that begin an error
	readAll             // the client reads with room to spare
	readSome            // the client reads three bytes at most
	writeProbe          // the client writes the probe
	writeOther          // the client writes anything else
	openReturns         // Open returns
	events
)

var eventNames = [events]string{"store sends %", "store sends -", "read", "read 3", "write probe", "write other", "Open returns"}

const otherWire = "*2\r\n$5\r\nhello\r\n$1\r\n3\r\n"

// run plays one order of events on a new connection and returns the first
// rule it broke, or "".
func run(order []int) string {
	far := &farEnd{}
	conn := &firstConn{Conn: far}

	var (
		firstRead    byte   // the first byte the client read from the store
		wroteAfter   bool   // something other than the probe was written after that byte
		taken        bool   // a write was taken
		returned     bool   // Open has returned
		wantReceived []byte // what the store must have received
		wantRead     []byte // what the client must have read
		gotRead      []byte
		owed         = 0 // bytes of the answer the client has yet to read
		unread       []byte
	)
	for step, event := range order {
		at := fmt.Sprintf("step %d (%s)", step+1, eventNames[event])
		switch event {
		case sendAccepted, sendRefused:
			chunk := "%1\r\n+a\r\n+b\r\n"
			if event == sendRefused {
				chunk = "-NOAUTH\r\n"
			}
			far.sent = append(far.sent, chunk...)
			unread = append(unread, chunk...)

		case readAll, readSome:
			room := 64
			if event == readSome {
				room = 3
			}
			p := make([]byte, room)
			n, err := conn.Read(p)
			switch {
			case owed > 0:
				// The answer comes first and comes alone.
				want := probeAnswer[len(probeAnswer)-owed:]
				if len(want) > room {
					want = want[:room]
				}
				if err != nil || string(p[:n]) != want {
					return fmt.Sprintf("%s: read %q, %v; want %q of the answer", at, p[:n], err, want)
				}
				owed -= n
				wantRead = append(wantRead, want...)
			case len(unread) == 0:
				if n != 0 || err != errNothingSent {
					return fmt.Sprintf("%s: read %q, %v from a store that sent nothing; want the store's own error", at, p[:n], err)
				}
			default:
				want := unread
				if len(want) > room {
					want = want[:room]
				}
				if err != nil || !bytes.Equal(p[:n], want) {
					return fmt.Sprintf("%s: read %q, %v; want %q of the store's", at, p[:n], err, want)
				}
				if firstRead == 0 {
					firstRead = want[0]
				}
				unread = unread[n:]
				wantRead = append(wantRead, want...)
			}
			gotRead = append(gotRead, p[:n]...)

		case writeProbe, writeOther:
			wire := probeWire
			if event == writeOther {
				wire = otherWire
			}
			before := len(far.received)
			n, err := conn.Write([]byte(wire))
			if n != len(wire) || err != nil {
				return fmt.Sprintf("%s: wrote %d, %v", at, n, err)
			}
			travelled := len(far.received) > before
			due := event == writeProbe && firstRead == '%' && !wroteAfter && !taken && !returned
			switch {
			case due && travelled:
				return at + ": the probe travelled; it was due to be taken"
			case !due && !travelled:
				return at + ": a write was taken that was not due"
			}
			if due {
				taken = true
				owed = len(probeAnswer)
			} else {
				wantReceived = append(wantReceived, wire...)
				if firstRead != 0 {
					wroteAfter = true
				}
			}

		case openReturns:
			conn.disarm()
			returned = true
		}
		if !bytes.Equal(far.received, wantReceived) {
			return fmt.Sprintf("%s: the store received %q; want %q", at, far.received, wantReceived)
		}
	}
	if !bytes.Equal(gotRead, wantRead) {
		return fmt.Sprintf("the client read %q; want %q", gotRead, wantRead)
	}
	if n := strings.Count(string(gotRead), probeAnswer); taken && owed == 0 && n != 1 {
		return fmt.Sprintf("the client read the answer %d times in %q; want once", n, gotRead)
	}
	return ""
}

func named(order []int) string {
	names := make([]string, len(order))
	for i, event := range order {
		names[i] = eventNames[event]
	}
	return strings.Join(names, ", ")
}

// TestFirstConnOverEveryOrderOfEvents: every order of the seven events, up
// to six of them.
func TestFirstConnOverEveryOrderOfEvents(t *testing.T) {
	t.Parallel()
	const depth = 6
	order := make([]int, 0, depth)
	orders, taken := 0, 0
	var walk func()
	walk = func() {
		orders++
		if fault := run(order); fault != "" {
			require.Empty(t, fault, "%s\n  in the order: %s", fault, named(order))
		}
		if len(order) == depth {
			return
		}
		for event := 0; event < events; event++ {
			order = append(order, event)
			walk()
			order = order[:len(order)-1]
		}
	}
	walk()

	// The walk is worth what it reaches: the order Open makes is in it, and
	// so is every refusal to take the probe.
	for _, c := range []struct {
		order []int
		want  bool
	}{
		{[]int{writeOther, sendAccepted, readAll, writeProbe, readAll}, true},
		{[]int{writeOther, sendAccepted, readSome, readAll, writeProbe, readSome}, true},
		{[]int{writeOther, sendRefused, readAll, writeProbe}, false},
		{[]int{writeOther, sendAccepted, readAll, writeOther, writeProbe}, false},
		{[]int{writeOther, sendAccepted, readAll, openReturns, writeProbe}, false},
		{[]int{writeOther, sendAccepted, writeProbe}, false},
		{[]int{writeProbe}, false},
		{[]int{openReturns, writeOther, sendAccepted, readAll, writeProbe}, false},
	} {
		if fault := run(c.order); fault != "" {
			require.Empty(t, fault, "%s\n  in the order: %s", fault, named(c.order))
		}
		far := &farEnd{}
		conn := &firstConn{Conn: far}
		for _, event := range c.order {
			switch event {
			case sendAccepted:
				far.sent = append(far.sent, "%0\r\n"...)
			case sendRefused:
				far.sent = append(far.sent, "-NO\r\n"...)
			case readAll:
				_, _ = conn.Read(make([]byte, 64))
			case readSome:
				_, _ = conn.Read(make([]byte, 3))
			case writeOther:
				_, _ = conn.Write([]byte(otherWire))
			case writeProbe:
				_, _ = conn.Write([]byte(probeWire))
			case openReturns:
				conn.disarm()
			}
		}
		if got := !bytes.Contains(far.received, []byte(probeWire)); got != c.want {
			assert.EqualValues(t, c.want, got, "the probe taken: %v; want %v, in the order: %s", got, c.want, named(c.order))
		}
		if c.want {
			taken++
		}
	}
	t.Logf("%d orders of events held the rules; %d of the named orders take the probe", orders, taken)
}

// TestFirstConnOverLongOrdersOfEvents: orders of forty events from fixed
// seeds, which reach what six events cannot: the answer read three bytes at
// a time, and a life that goes on after it.
func TestFirstConnOverLongOrdersOfEvents(t *testing.T) {
	t.Parallel()
	for seed := uint64(1); seed <= 4; seed++ {
		r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
		for i := 0; i < 5000; i++ {
			// Half of the orders begin as Open does, so that the probe is
			// taken in them and the rest of the order follows the answer.
			var order []int
			if r.IntN(2) == 0 {
				order = []int{writeOther, sendAccepted, readAll, writeProbe}
			}
			for len(order) < 40 {
				order = append(order, r.IntN(events))
			}
			if fault := run(order); fault != "" {
				require.Empty(t, fault, "seed %d case %d: %s\n  in the order: %s", seed, i, fault, named(order))
			}
		}
	}
}
