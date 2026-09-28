package testredis

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The counter is not vacuous: a client that sends a PING is counted, and one
// that only opens is not.
func TestCommandCounterCountsAPing(t *testing.T) {
	t.Parallel()

	addr, count := CommandCounter(t)
	if host, _, err := net.SplitHostPort(addr); err != nil || host != "127.0.0.1" {
		t.Fatalf("the counter listens on %q; want 127.0.0.1 and a port", addr)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if n := count(); n != 0 {
		t.Fatalf("NewClient sent %d commands; want 0", n)
	}
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if n := count(); n < 1 {
		t.Fatalf("after a PING the counter reads %d; want at least 1", n)
	}
}

func TestCommandCounterAnswersEveryCommandAndCountsIt(t *testing.T) {
	t.Parallel()

	addr, count := CommandCounter(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	for i, step := range []struct{ send, want string }{
		{"*1\r\n$4\r\nPING\r\n", "+PONG\r\n"},
		{"*1\r\n$4\r\nping\r\n", "+PONG\r\n"},
		{"*2\r\n$5\r\nHELLO\r\n$1\r\n3\r\n", "-ERR unknown command 'HELLO'\r\n"},
		{"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$0\r\n\r\n", "+OK\r\n"},
	} {
		if _, err := io.WriteString(conn, step.send); err != nil {
			t.Fatal(err)
		}
		if got, err := r.ReadString('\n'); err != nil || got != step.want {
			t.Fatalf("%q was answered %q, %v; want %q", step.send, got, err, step.want)
		}
		if n := count(); n != int64(i+1) {
			t.Fatalf("after %d commands the counter reads %d", i+1, n)
		}
	}
}

// malformed is what is not one command: the counter hangs up and counts
// nothing.
var malformed = map[string]string{
	"an inline command":       "PING\r\n",
	"no words":                "*0\r\n",
	"a count that is no int":  "*x\r\n",
	"more words than any":     "*1048577\r\n",
	"a word with no length":   "*1\r\nPING\r\n",
	"a negative length":       "*1\r\n$-1\r\n",
	"a length that is no int": "*1\r\n$x\r\n",
	"a longer word than any":  "*1\r\n$67108865\r\n",
	"a word cut short":        "*1\r\n$4\r\nPI",
	"a command cut short":     "*2\r\n$4\r\nPING\r\n",
	"nothing":                 "",
}

func TestCommandCounterHangsUpOnWhatIsNotACommand(t *testing.T) {
	t.Parallel()

	addr, count := CommandCounter(t)
	for name, send := range malformed {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(conn, send); err != nil {
			t.Fatal(err)
		}
		// This side has said all it will: what is cut short stays short.
		if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		if got, err := io.ReadAll(conn); err != nil || len(got) != 0 {
			t.Errorf("%s: answered %q, %v; want the counter to hang up and say nothing", name, got, err)
		}
		_ = conn.Close()
	}
	if n := count(); n != 0 {
		t.Fatalf("what is not a command was counted: %d", n)
	}
}

func TestReadArrayReadsOneCommand(t *testing.T) {
	t.Parallel()

	r := bufio.NewReader(strings.NewReader("*2\r\n$3\r\nGET\r\n$5\r\na\r\nb!\r\n*1\r\n$4\r\nPING\r\n"))
	for _, want := range [][]string{{"GET", "a\r\nb!"}, {"PING"}} {
		if got, err := readArray(r); err != nil || !slices.Equal(got, want) {
			t.Fatalf("readArray = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := readArray(r); !errors.Is(err, io.EOF) {
		t.Fatalf("readArray at the end = %v; want EOF", err)
	}
	for name, send := range malformed {
		if got, err := readArray(bufio.NewReader(strings.NewReader(send))); err == nil {
			t.Errorf("%s: readArray = %q; want an error", name, got)
		}
	}
}

// A client that has hung up before the answer ends the serving of it.
func TestCommandCounterStopsServingAClientThatHungUp(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	var n atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveCounted(server, &n)
	}()
	if _, err := io.WriteString(client, "*1\r\n$4\r\nPING\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	if got := n.Load(); got != 1 {
		t.Fatalf("the command of a client that hung up was counted %d times; want once", got)
	}
}

// The cleanup hangs up on a client the test left open, closes its listener,
// and ends. The listener is asked, not the port: once it is closed the port
// is anyone's, and a parallel test may take it and answer there.
func TestCommandCounterHangsUpOnItsClientsAtCleanup(t *testing.T) {
	t.Parallel()

	var left net.Conn
	var ln net.Listener
	t.Run("a test that leaves its client open", func(t *testing.T) {
		addr, count := real.counter(t, func(network, address string) (net.Listener, error) {
			var err error
			ln, err = net.Listen(network, address)
			return ln, err
		})
		var err error
		if left, err = net.Dial("tcp", addr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(left, "*1\r\n$4\r\nPING\r\n"); err != nil {
			t.Fatal(err)
		}
		if got, err := bufio.NewReader(left).ReadString('\n'); err != nil || got != "+PONG\r\n" || count() != 1 {
			t.Fatalf("answered %q, %v, counted %d", got, err, count())
		}
	})
	defer left.Close()
	// The end of the stream: ReadAll reads it as no error and nothing more.
	if got, err := io.ReadAll(left); err != nil || len(got) != 0 {
		t.Fatalf("after the cleanup the client read %q, %v; want the end and nothing before it", got, err)
	}
	if conn, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("after the cleanup the counter's listener accepts: %v; want it closed", err)
	}
}

func TestCommandCounterFailsTheTestThatHasNoPort(t *testing.T) {
	t.Parallel()

	r := provoke(t, func(tb testing.TB) {
		real.counter(tb, func(string, string) (net.Listener, error) { return nil, errors.New("no descriptors left") })
	})
	if !strings.Contains(r.fatal, "no descriptors left") {
		t.Fatalf("the counter with no port failed with %q; want the cause", r.fatal)
	}
}
