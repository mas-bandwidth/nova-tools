package friend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

func subjects(ps []Push) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Subject)
	}
	return out
}

// The connection: no ping for a window says silent exactly once; a ping
// says back once; a ping within the window says nothing.
func TestCoordinatorSilentIsPushedOncePerOutageAndBackOnce(t *testing.T) {
	t.Parallel()
	m := Start(t0)
	assert.Empty(t, m.Tick(t0.Add(Window-time.Second)), "within the window nothing is said")
	assert.Equal(t, []string{"coordinator silent"}, subjects(m.Tick(t0.Add(Window))))
	assert.Equal(t, Silent, m.Connection)
	assert.Empty(t, m.Tick(t0.Add(2*Window)), "silent is said once per outage")
	assert.Empty(t, m.Tick(t0.Add(3*Window)))
	got := m.Ping(t0.Add(3*Window+time.Second), "ada", t0, "n1")
	assert.Equal(t, []string{"coordinator back"}, subjects(got))
	assert.Contains(t, got[0].Text, "ada has the seat")
	assert.Equal(t, Connected, m.Connection)
	assert.Equal(t, "ada", m.Seat)
	assert.Empty(t, subjects(m.Ping(t0.Add(3*Window+2*time.Second), "ada", t0, "n2")), "connected: a ping says nothing of the coordinator")
	assert.Empty(t, m.Tick(t0.Add(4*Window)), "the window is measured from the last ping")
	assert.Equal(t, []string{"coordinator silent"}, subjects(m.Tick(t0.Add(4*Window+2*time.Second))), "a second outage is said again")
}

func TestSleepingMachinePausesChallengeAndSuppressesSyntheticNotices(t *testing.T) {
	t.Parallel()
	m := Start(t0)
	m.Ping(t0.Add(time.Second), "ada", t0, "n1")
	m.Sleep(t0.Add(2 * time.Second))

	got := m.TickWhen(t0.Add(time.Second+Window), true)
	assert.Empty(t, got, "sleep suppresses daemon-generated coordinator-silent pushes")
	assert.Equal(t, Silent, m.Connection, "the transport clock still advances while asleep")
	assert.Equal(t, Challenged, m.Challenge, "sleep itself must not make the session deaf")

	wakeAt := t0.Add(time.Second + Window + time.Second)
	back := m.ReceivePing(wakeAt, "coordinator", "coordinator", "ada", t0, "n2")
	assert.Equal(t, []string{"coordinator back"}, subjects(back), "a real coordinator ping after durable wake remains an event")
	assert.False(t, m.Asleep)
	assert.Equal(t, t0.Add(time.Second+Window+time.Second), m.Asked, "the new nonce starts a fresh challenge after wake")

	localSleep := wakeAt.Add(time.Second)
	m.Sleep(localSleep)
	awakeAt := localSleep.Add(5 * time.Second)
	assert.Empty(t, m.TickWhen(awakeAt, false))
	assert.Equal(t, Challenged, m.Challenge)
	got = m.TickWhen(m.Asked.Add(Window), false)
	assert.Equal(t, []string{"coordinator silent"}, subjects(got), "transport silence is independent of the paused session challenge")
	assert.Equal(t, Deaf, m.Challenge, "awake time after wake still counts toward the deadline")
}

func TestSleepingMachineRejectsNonCoordinatorPing(t *testing.T) {
	t.Parallel()
	m := Start(t0)
	m.Sleep(t0.Add(time.Second))
	got := m.ReceivePing(t0.Add(2*time.Second), "unrelated", "coordinator", "ada", t0, "n1")
	assert.Empty(t, got)
	assert.Empty(t, m.Ping(t0.Add(2*time.Second), "ada", t0, "n1"), "raw pings cannot bypass sleep gating")
	assert.True(t, m.Asleep)
	assert.Equal(t, t0, m.LastPing, "a noncoordinator cannot change transport state while asleep")
	assert.Empty(t, m.Nonce, "the message is not applied to the session challenge")
}

func TestAsleepMachineIsNotUpDespitePriorPong(t *testing.T) {
	t.Parallel()
	m := Start(t0)
	m.Ping(t0.Add(time.Second), "coordinator", t0, "n1")
	require.True(t, m.Pong(t0.Add(2*time.Second), "n1"))
	assert.True(t, m.Up())
	m.Sleep(t0.Add(3 * time.Second))
	assert.False(t, m.Up(), "sleep status must not be reported as an active session")
	m.Wake(t0.Add(4 * time.Second))
	assert.True(t, m.Up(), "the earlier proof remains after local wake")
}

// The challenge: a ping challenges; only the current nonce answers it; a
// window unanswered is deaf; a pong ends deaf; up only after a pong.
func TestOnlyTheCurrentNonceAnswersAndAWindowUnansweredIsDeaf(t *testing.T) {
	t.Parallel()
	m := Start(t0)
	assert.False(t, m.Up(), "never ponged: not up")
	assert.False(t, m.Pong(t0, "n0"), "a pong with nothing asked is stale")
	m.Ping(t0.Add(time.Second), "ada", t0, "n1")
	assert.Equal(t, Challenged, m.Challenge)
	assert.False(t, m.Up(), "challenged: not up")
	assert.False(t, m.Pong(t0.Add(2*time.Second), "nope"), "a stale nonce changes nothing")
	assert.Equal(t, Challenged, m.Challenge)
	require.True(t, m.Pong(t0.Add(2*time.Second), "n1"))
	assert.Equal(t, Quiet, m.Challenge)
	assert.True(t, m.Up())
	assert.False(t, m.Pong(t0.Add(3*time.Second), "n1"), "the same nonce again is a replay")

	m.Ping(t0.Add(4*time.Second), "ada", t0, "n2")
	m.Ping(t0.Add(5*time.Second), "ada", t0, "n3")
	assert.False(t, m.Pong(t0.Add(6*time.Second), "n2"), "the older nonce is stale once a newer one was asked")
	m.Tick(t0.Add(5*time.Second + Window))
	assert.Equal(t, Deaf, m.Challenge)
	assert.False(t, m.Up())
	m.Ping(t0.Add(6*time.Second+Window), "ada", t0, "n4")
	assert.Equal(t, Deaf, m.Challenge, "deaf stays deaf under a new ping until a pong")
	require.True(t, m.Pong(t0.Add(7*time.Second+Window), "n4"))
	assert.Equal(t, Quiet, m.Challenge)
	assert.True(t, m.Up())
	assert.Equal(t, 2, m.Pongs)
}

func TestRepeatedCurrentNonceKeepsTheOriginalChallengeAndPong(t *testing.T) {
	t.Parallel()
	m := Start(t0)
	asked := t0.Add(time.Second)
	m.Ping(asked, "ada", t0, "n1")
	duplicate := asked.Add(Window - time.Second)
	m.Ping(duplicate, "ada", t0, "n1")
	assert.Equal(t, duplicate, m.LastPing, "a repeated ping still refreshes the connection")
	assert.Equal(t, asked, m.Asked, "it does not postpone the challenge deadline")
	m.Tick(asked.Add(Window))
	assert.Equal(t, Deaf, m.Challenge)
	require.True(t, m.Pong(asked.Add(Window), "n1"))
	m.Ping(asked.Add(Window+time.Second), "ada", t0, "n1")
	assert.Equal(t, Quiet, m.Challenge, "an answered nonce is not a new challenge")
	assert.Equal(t, asked, m.Asked)
	assert.False(t, m.Pong(asked.Add(Window+2*time.Second), "n1"))
	assert.Equal(t, 1, m.Pongs)
	next := asked.Add(Window + 3*time.Second)
	m.Ping(next, "ada", t0, "n2")
	assert.Equal(t, Challenged, m.Challenge)
	assert.Equal(t, next, m.Asked)
	require.True(t, m.Pong(next, "n2"))
	assert.Equal(t, 2, m.Pongs)
}

func TestPingAndPongLinesRoundTrip(t *testing.T) {
	t.Parallel()
	text := PingText("ada", t0, "abc123")
	nonce, seat, since, ok := ParsePing(text)
	require.True(t, ok)
	assert.Equal(t, []any{"abc123", "ada", t0}, []any{nonce, seat, since})
	assert.Contains(t, text, "nova-friend pong --as <you> --nonce abc123")
	nonce, seat, _, ok = ParsePing("PING zz9\nhello")
	assert.True(t, ok && nonce == "zz9" && seat == "")
	_, _, _, ok = ParsePing("hello PING x")
	assert.False(t, ok)

	n, q, w, wd, ok := ParsePong(PongLine("abc123", 5, 3, 8))
	require.True(t, ok)
	assert.Equal(t, []int{5, 3, 8}, []int{q, w, wd})
	assert.Equal(t, "abc123", n)
	n, _, _, _, ok = ParsePong("pong abc123")
	assert.True(t, ok && n == "abc123", "the numbers are optional")
	_, _, _, _, ok = ParsePong("daemon-pong abc123")
	assert.False(t, ok)
}
