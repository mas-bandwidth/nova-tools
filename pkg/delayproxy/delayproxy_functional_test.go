//go:build functional

// The proxy's tests through the loopback rig of rig_functional_test.go: the
// proxy's own listener, an echo target and real clients. A socket test is a
// functional test; the tests that own no socket are in delayproxy_test.go.

package delayproxy

import (
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every write is held once, whatever it carries: three commands sent one after
// the other pay the delay three times, a hundred sent in one write pay it once,
// and the replies pay nothing.
func TestEachWriteIsHeldOnceAndAPipelinePaysOnce(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	p := r.serve()
	c := r.client()

	separate := []string{"one", "two", "three"} // each waits for its reply
	for i, send := range separate {
		got := roundTrip(t, c, send)
		require.Equal(t, send, got, "command %d came back as %q", i, got)
	}
	asked := r.fake.asked()
	require.True(t, onlyDelays(asked, len(separate)), "%d commands, each waiting for its reply, asked the clock for %v; want the delay once each", len(separate), asked)
	got := roundTrip(t, c, pipeline)
	require.Equal(t, pipeline, got, "the pipeline did not come back whole")
	asked = r.fake.asked()
	require.True(t, onlyDelays(asked, len(separate)+1), "%d commands in one write brought the clock's waits to %v; want one more delay, not %d", pipelined, asked, pipelined)
	p.Stop() // waits for forward's record before the count is read (delayproxy.go:301-319)
	require.Equal(t, len(separate)+1, p.Writes(), "the proxy counts %d writes; want %d", p.Writes(), len(separate)+1)
}

// Nothing reaches the target before its delay is over. The proxy is the only
// way to the target and it is held in its wait, so what the target has received
// at that moment is what was sent early.
func TestNothingIsForwardedBeforeItsDelayIsOver(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(1)
	p := r.serve()
	c := r.client()

	_, err := io.WriteString(c, "hello")
	require.NoError(t, err, err)
	r.await() // the write is held
	n := r.echo.received.Load()
	require.Zero(t, n, "the target has received %d bytes while the write is still held", n)
	require.Zero(t, p.Writes(), "the proxy counts %d writes while the first is still held", p.Writes())
	r.release()
	got := make([]byte, len("hello"))
	_, err = io.ReadFull(c, got)
	require.NoError(t, err, "after the delay the reply is %q, %v; want hello", got, err)
	require.Equal(t, "hello", string(got), "after the delay the reply is %q, %v; want hello", got, err)
	p.Stop() // waits for forward's record before the count is read (delayproxy.go:301-319)
	require.Equal(t, 1, p.Writes(), "the proxy counts %d writes; want 1", p.Writes())
}

// A write larger than one read comes in as several, each of them held the delay,
// and the bytes come out whole and in order.
func TestALargeWriteComesOutWholeAndInOrder(t *testing.T) {
	t.Parallel()

	send := make([]byte, largeWrite)
	for i := range send {
		send[i] = byte(i % patternPeriod)
	}
	r := newRig(t)
	p := r.serve()
	c := r.client()

	got := roundTrip(t, c, string(send))
	require.Equal(t, string(send), got, "the large write did not come back whole and in order")
	asked := r.fake.asked()
	least := (largeWrite + ChunkBytes - 1) / ChunkBytes
	require.GreaterOrEqual(t, len(asked), least, "%d bytes came in as %d reads; want at least %d", largeWrite, len(asked), least)
	for i, d := range asked {
		require.Equal(t, delay, d, "read %d waited %v; want the delay, %v", i, d, delay)
	}
	p.Stop() // waits for forward's record before the count is read (delayproxy.go:301-319)
	require.Equal(t, len(asked), p.Writes(), "the proxy counts %d writes for %d reads", p.Writes(), len(asked))
}

// A client that has sent all it will and closes its sending side still gets the
// replies to what was held when it did.
func TestAClientThatStopsSendingStillGetsTheRepliesToWhatWasHeld(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(1)
	r.serve()
	c := r.client().(*net.TCPConn)

	_, err := io.WriteString(c, "abc")
	require.NoError(t, err, err)
	err = c.CloseWrite()
	require.NoError(t, err, err)
	r.await()
	r.release()
	got, err := io.ReadAll(c)
	require.NoError(t, err, "read after the client stopped sending: %q, %v; want abc", got, err)
	require.Equal(t, "abc", string(got), "read after the client stopped sending: %q, %v; want abc", got, err)
}

// Stop returns with nothing of the proxy running, though clients are mid-hold,
// and hangs up on them. What was held is never sent.
func TestStopEndsEveryGoroutineWhileWritesAreHeld(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(heldClients)
	p := r.serve()
	conns := make([]net.Conn, heldClients)
	for i := range conns {
		conns[i] = r.client()
		_, err := io.WriteString(conns[i], "held")
		require.NoError(t, err, err)
		r.await()
	}
	got, want := int(p.live.Load()), acceptLoop+GoroutinesPerConn*heldClients
	require.Equal(t, want, got, "live = %d with %d connections held; want %d: one to accept and %d each", got, heldClients, want, GoroutinesPerConn)
	p.Stop()
	got = int(p.live.Load())
	require.Zero(t, got, "live = %d after Stop returned; want 0", got)
	p.Stop() // a second call only waits
	for i, c := range conns {
		got, err := io.ReadAll(c)
		require.Zero(t, len(got), "client %d read %q, %v after Stop; want it hung up on with nothing", i, got, err)
	}
	n := r.echo.received.Load()
	require.Zero(t, n, "the target received %d bytes of writes that were held when Stop ran", n)
	require.Zero(t, p.Writes(), "the proxy counts %d writes; want 0", p.Writes())
}

// The bound is on open connections: with one allowed, a second client waits in
// the backlog, unserved and undialled, until the first is over, and is then
// served.
func TestAClientPastTheBoundWaitsForASlot(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	const slots = 1
	r.opts.MaxConns = slots
	p := r.serve()
	first := r.client()
	got := roundTrip(t, first, "first")
	require.Equal(t, "first", got, "first came back as %q", got)
	second := r.client()
	_, err := io.WriteString(second, "second")
	require.NoError(t, err, err)
	n := r.echo.accepted.Load()
	require.Equal(t, int64(slots), n, "the target has taken %d connections with %d slot and the first still open; want %d", n, slots, slots)
	live, want := int(p.live.Load()), acceptLoop+GoroutinesPerConn*slots
	require.Equal(t, want, live, "live = %d; want %d: the accept loop and the one connection", live, want)
	err = first.Close()
	require.NoError(t, err, err)
	gotBytes := make([]byte, len("second"))
	_, err = io.ReadFull(second, gotBytes)
	require.NoError(t, err, "the second client, once a slot was free, read %q, %v; want second", gotBytes, err)
	require.Equal(t, "second", string(gotBytes), "the second client, once a slot was free, read %q, %v; want second", gotBytes, err)
	n = r.echo.accepted.Load()
	require.Equal(t, int64(slots+1), n, "the target has taken %d connections; want %d", n, slots+1)
}

// The real clock holds a write for at least the delay, by the proxy's own
// measure. It is the only test of this file that waits on real time.
func TestTheRealClockHoldsAWriteForAtLeastTheDelay(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.delay, r.opts = realDelay, Options{} // the real clock is the proxy's own when no clock is given
	p := r.serve()
	require.Zero(t, p.Writes(), "a new proxy counts %d writes, shortest %v; want none", p.Writes(), p.Shortest())
	require.Zero(t, p.Shortest(), "a new proxy counts %d writes, shortest %v; want none", p.Writes(), p.Shortest())
	c := r.client()
	got := roundTrip(t, c, "real clock")
	require.Equal(t, "real clock", got, "came back as %q", got)
	// The echoed bytes can reach the client before the forwarding half runs
	// record, which counts a write only once the target has taken all of it.
	// Stop waits for every connection goroutine (delayproxy.go:301-319, p.wg.Wait
	// at :318), so forward's record has run before the counts are read.
	p.Stop()
	require.Equal(t, 1, p.Writes(), "after one write through the real clock: %d writes, shortest %v; want 1, at least %v", p.Writes(), p.Shortest(), realDelay)
	require.GreaterOrEqual(t, p.Shortest(), realDelay, "after one write through the real clock: %d writes, shortest %v; want 1, at least %v", p.Writes(), p.Shortest(), realDelay)
}

// One client's hold does not delay another's: each connection waits for itself,
// on no lock the proxy shares. Were the delay shared, a second client would wait
// behind the first's endless hold and fail its read at the ceiling, by name.
func TestOneClientsHoldDoesNotDelayAnother(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.hold(2)
	r.fake.firstOnly = true // the first client's hold does not end
	r.serve()

	first := r.client()
	_, err := io.WriteString(first, "a")
	require.NoError(t, err, err)
	r.await() // the first client's write is held, and its wait does not end

	second := r.client()
	_, err = io.WriteString(second, "b")
	require.NoError(t, err, err)
	got := make([]byte, 1)
	_, err = io.ReadFull(second, got)
	require.NoError(t, err, "a client that came while another client's write was held was not answered: %v; the clients share the delay", err)
	require.Equal(t, "b", string(got), "a client that came while another client's write was held read %q; want b", got)
	n := r.echo.received.Load()
	require.Equal(t, int64(1), n, "the target has received %d bytes; want the second client's one, for the first is still held", n)
	r.release()
	_, err = io.ReadFull(first, got)
	require.NoError(t, err, "the first client, once its hold was over, read %q, %v; want a", got, err)
	require.Equal(t, "a", string(got), "the first client, once its hold was over, read %q, %v; want a", got, err)
}
