package testredis

import (
	"bufio"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestReadArrayReadsOneCommand(t *testing.T) {
	t.Parallel()

	r := bufio.NewReader(strings.NewReader("*2\r\n$3\r\nGET\r\n$5\r\na\r\nb!\r\n*1\r\n$4\r\nPING\r\n"))
	for _, want := range [][]string{{"GET", "a\r\nb!"}, {"PING"}} {
		if got, err := readArray(r); err != nil || !slices.Equal(got, want) {
			require.Failf(t, "", "readArray = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := readArray(r); !errors.Is(err, io.EOF) {
		require.ErrorIs(t, err, io.EOF, "readArray at the end = %v; want EOF", err)
	}
	for name, send := range malformed {
		if got, err := readArray(bufio.NewReader(strings.NewReader(send))); err == nil {
			assert.Error(t, err, "%s: readArray = %q; want an error", name, got)
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
		require.NoError(t, err, err)
	}
	if err := client.Close(); err != nil {
		require.NoError(t, err, err)
	}
	<-done
	if got := n.Load(); got != 1 {
		require.EqualValues(t, 1, got, "the command of a client that hung up was counted %d times; want once", got)
	}
}

func TestCommandCounterFailsTheTestThatHasNoPort(t *testing.T) {
	t.Parallel()

	r := provoke(t, func(tb testing.TB) {
		real.counter(tb, func(string, string) (net.Listener, error) { return nil, errors.New("no descriptors left") })
	})
	if !strings.Contains(r.fatal, "no descriptors left") {
		require.Contains(t, r.fatal, "no descriptors left", "the counter with no port failed with %q; want the cause", r.fatal)
	}
}
