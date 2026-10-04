package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveDashboard (dashboard.go:193, the finding's 0.0%) starts one listener per address,
// says each one it listens on with the address it actually bound, and a listener that
// cannot start closes the others. The addresses are loopback's ephemeral ports: the only
// sockets are the ones the test and the function itself open, and each is closed again
// before its test returns.
func TestDashboardCoverServesEachListenerAndRefusesWhenOneCannotStart(t *testing.T) {
	t.Parallel()
	t.Run("each listener starts and says the address it bound", func(t *testing.T) {
		var out bytes.Buffer
		servers, err := (&app{}).serveDashboard([]listener{
			{flag: "--listen", addr: "127.0.0.1:0", h: http.NewServeMux(), says: "DASHBOARD listening on http://%s/\n"},
			{flag: "--pull", addr: "127.0.0.1:0", h: http.NewServeMux(), says: "DASHBOARD pull routes on http://%s/\n"},
		}, &out)
		require.NoError(t, err)
		require.Len(t, servers, 2)
		t.Cleanup(func() {
			for _, s := range servers {
				// ignored: the test's servers at its end; a page asking now asks again
				_ = s.Close()
			}
		})
		assert.Equal(t, 2, strings.Count(out.String(), "\n"), out.String())
		assert.Contains(t, out.String(), "DASHBOARD listening on http://127.0.0.1:", out.String())
		assert.Contains(t, out.String(), "DASHBOARD pull routes on http://127.0.0.1:", out.String())
	})
	t.Run("a listener that cannot start closes the others and refuses", func(t *testing.T) {
		// the first address is one this test opens and releases, so it is free and
		// known; the second is held open, so serveDashboard's second Listen fails
		free, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		first := free.Addr().String()
		// ignored: the address is released so the listener under test can take it
		_ = free.Close()
		held, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() {
			// ignored: the test's own listener at its end
			_ = held.Close()
		})
		var out bytes.Buffer
		servers, err := (&app{}).serveDashboard([]listener{
			{flag: "--listen", addr: first, h: http.NewServeMux(), says: "DASHBOARD listening on http://%s/\n"},
			{flag: "--pull", addr: held.Addr().String(), h: http.NewServeMux(), says: "DASHBOARD pull routes on http://%s/\n"},
		}, &out)
		require.Error(t, err)
		assert.Nil(t, servers)
		assert.Empty(t, out.String(), "a listener that cannot start is refused before anything is said")
		assert.Contains(t, err.Error(), "--pull "+held.Addr().String(), err.Error())
		assert.Contains(t, err.Error(), "address already in use", err.Error())
		// the listener already open was closed again: its address is free to take
		again, err := net.Listen("tcp", first)
		require.NoError(t, err, "the listener opened before the failed one is closed again")
		// ignored: the test's own listener at its end
		_ = again.Close()
	})
}

// coverCounter is an underlying writer that tallies its writes: how many were inside it
// at once (lockedWriter's one writer at a time) and how many bytes went through.
type coverCounter struct {
	inFlight atomic.Int64
	most     atomic.Int64
	bytes    atomic.Int64
}

func (c *coverCounter) Write(p []byte) (int, error) {
	n := c.inFlight.Add(1)
	for {
		most := c.most.Load()
		if n <= most || c.most.CompareAndSwap(most, n) {
			break
		}
	}
	c.bytes.Add(int64(len(p)))
	c.inFlight.Add(-1)
	return len(p), nil
}

// coverBroken is an underlying writer that fails: it writes nothing and its error is
// the caller's.
type coverBroken struct{ err error }

func (b coverBroken) Write([]byte) (int, error) { return 0, b.err }

// lockedWriter.Write (dashboard.go:227, the finding's other 0.0%) writes each line whole,
// one writer at a time, and the underlying writer's error is the caller's, never
// swallowed.
func TestDashboardCoverLockedWriterWritesEachLineWholeAndReturnsTheWritersError(t *testing.T) {
	t.Parallel()
	t.Run("each write goes through whole, one writer at a time", func(t *testing.T) {
		var c coverCounter
		l := &lockedWriter{w: &c}
		var expected atomic.Int64
		var wg sync.WaitGroup
		for g := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 50 {
					line := fmt.Sprintf("DASHBOARD NOTE g%d line %d\n", g, i)
					expected.Add(int64(len(line)))
					_, err := l.Write([]byte(line))
					assert.NoError(t, err, "write")
				}
			}()
		}
		wg.Wait()
		assert.Equal(t, expected.Load(), c.bytes.Load(), "every byte through, whole")
		assert.Equal(t, int64(0), c.inFlight.Load(), "all writes finished")
		assert.Equal(t, int64(1), c.most.Load(), "one writer at a time, never two inside at once")
	})
	t.Run("the underlying writer's error is the caller's", func(t *testing.T) {
		boom := errors.New("the writer is broken")
		n, err := (&lockedWriter{w: coverBroken{err: boom}}).Write([]byte("DASHBOARD STOPPED\n"))
		require.ErrorIs(t, err, boom)
		assert.Zero(t, n)
	})
}
