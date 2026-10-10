package friend

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
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

// Up says whether the session has proved itself: it answered the current challenge, and
// has answered at least once (the daemon alone never makes a friend up; tla/Friend.tla:
// UpOnlyAfterPong).
func (m *Machine) Up() bool { return m.Challenge == Quiet && m.Pongs > 0 }

// Envelope is a pure function of the pending list, the clock and the limit:
// oldest first, each message's age at now, and the cap leaves the rest named.
// Calling it twice changes nothing, so it can be tested apart from the daemon
// (docs/SPEC-FRIEND.md, the loop).
func TestTheEnvelopeIsPureOverThePendingListAndTheClock(t *testing.T) {
	t.Parallel()
	msgs := []bus.Message{
		{ID: "m1", From: "ada", At: t0, Subject: "one", Body: "a\n"},
		{ID: "m2", From: "ada", At: t0.Add(2 * time.Minute), Subject: "two", Body: "b"},
	}
	now := t0.Add(5 * time.Minute)
	text, shown := Envelope("ada", msgs, now, "bob", 0, "", "")
	assert.Equal(t, 2, shown)
	assert.Contains(t, text, "[1/2] m1 from=ada at="+t0.Format(time.RFC3339)+" age=5m subject=one\na\n")
	assert.Contains(t, text, "[2/2] m2 from=ada at="+t0.Add(2*time.Minute).Format(time.RFC3339)+" age=3m subject=two\nb\n")
	assert.Less(t, strings.Index(text, "m1"), strings.Index(text, "m2"), "oldest first")

	again, shownAgain := Envelope("ada", msgs, now, "bob", 0, "", "")
	assert.Equal(t, text, again, "a function of its arguments")
	assert.Equal(t, shown, shownAgain)
	assert.Equal(t, "a\n", msgs[0].Body, "the pending list is not changed")

	text, shown = Envelope("ada", msgs, now, "bob", 0, "the coordinator is silent", "")
	assert.Equal(t, 2, shown)
	assert.True(t, strings.HasPrefix(text, "nova-friend: the coordinator is silent\n\n"), text)
}
