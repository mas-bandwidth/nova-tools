package keepalive

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var testTime = time.Date(2026, 10, 4, 14, 0, 0, 0, time.UTC)
var testSeat = Seat{Holder: "ada", Epoch: 1, Generation: 1}

func pair(t *testing.T) (*Machine, *Machine) {
	t.Helper()
	a, err := New("ada", "bob", "coordinator", "a1", testSeat)
	require.NoError(t, err)
	b, err := New("bob", "ada", "friend", "b1", testSeat)
	require.NoError(t, err)
	return a, b
}

func next(t *testing.T, m *Machine, now time.Time, asleep bool) Frame {
	t.Helper()
	f, due, err := m.Next(now, asleep)
	require.NoError(t, err)
	require.True(t, due)
	return f
}

func prove(t *testing.T, a, b *Machine) Frame {
	t.Helper()
	af, bf := next(t, a, testTime, false), next(t, b, testTime, false)
	ok, err := a.Observe(testTime, bf)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = b.Observe(testTime, af)
	require.NoError(t, err)
	require.False(t, ok)
	af, bf = next(t, a, testTime.Add(time.Second), false), next(t, b, testTime.Add(time.Second), true)
	ok, err = a.Observe(testTime.Add(time.Second), bf)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = b.Observe(testTime.Add(time.Second), af)
	require.NoError(t, err)
	require.True(t, ok)
	return bf
}

func TestKeepaliveBootstrapNeedsFreshProofAndExpiresAtTenSeconds(t *testing.T) {
	t.Parallel()
	a, b := pair(t)
	require.False(t, a.Status(testTime).Up)
	prove(t, a, b)
	s := a.Status(testTime.Add(time.Second))
	require.True(t, s.Up)
	require.True(t, s.Asleep)
	require.Equal(t, "b1", s.PeerInstance)
	require.Equal(t, uint64(1), s.LastAckSeq)
	require.True(t, a.Status(testTime.Add(11*time.Second-time.Nanosecond)).Up)
	require.False(t, a.Status(testTime.Add(11*time.Second)).Up)
	require.False(t, a.Status(testTime.Add(11*time.Second)).Asleep)
}

func TestKeepaliveDuplicateAckAndNewChallengesCannotRenewProof(t *testing.T) {
	t.Parallel()
	a, b := pair(t)
	ack := prove(t, a, b)
	for i := 2; i <= 12; i++ {
		now := testTime.Add(time.Duration(i) * time.Second)
		next(t, a, now, false)
		ok, err := a.Observe(now, ack)
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, testTime.Add(time.Second), a.Status(now).LastPong)
	}
	require.False(t, a.Status(testTime.Add(12*time.Second)).Up)
}

func TestKeepaliveAckAgeBoundaryWrongRoleSeatAndInvocationReject(t *testing.T) {
	t.Parallel()
	for _, age := range []time.Duration{10*time.Second - time.Nanosecond, 10 * time.Second, -time.Nanosecond} {
		t.Run(age.String(), func(t *testing.T) {
			t.Parallel()
			a, _ := pair(t)
			own := next(t, a, testTime, false)
			f := Frame{Version: Version, From: "bob", To: "ada", Role: "friend", Seat: testSeat, Instance: "b1", Seq: 1, AckInstance: own.Instance, AckSeq: own.Seq}
			ok, err := a.Observe(testTime.Add(age), f)
			require.NoError(t, err)
			require.Equal(t, age >= 0 && age < Window, ok)
		})
	}
	for _, change := range []func(*Frame){
		func(f *Frame) { f.Seat.Generation++ },
		func(f *Frame) { f.AckInstance = "previous-process" },
		func(f *Frame) { f.From = "eve" },
		func(f *Frame) { f.Role = "coordinator" },
	} {
		a, _ := pair(t)
		own := next(t, a, testTime, false)
		f := Frame{Version: Version, From: "bob", To: "ada", Role: "friend", Seat: testSeat, Instance: "b1", Seq: 1, AckInstance: own.Instance, AckSeq: own.Seq}
		change(&f)
		ok, _ := a.Observe(testTime.Add(time.Second), f)
		require.False(t, ok)
		require.False(t, a.Status(testTime.Add(time.Second)).Up)
	}
}

func TestKeepaliveDelayedOlderAckCannotRollBackPeerIncarnation(t *testing.T) {
	t.Parallel()
	a, b := pair(t)
	old := prove(t, a, b)
	now := testTime.Add(2 * time.Second)
	own := next(t, a, now, false)
	newPeer := Frame{Version: Version, From: "bob", To: "ada", Role: "friend", Seat: testSeat, Instance: "b2", Seq: 1, AckInstance: own.Instance, AckSeq: own.Seq}
	ok, err := a.Observe(now, newPeer)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = a.Observe(now.Add(time.Second), old)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, "b2", a.Status(now.Add(time.Second)).PeerInstance)
	require.Equal(t, own.Seq, a.Status(now.Add(time.Second)).LastAckSeq)
}

func TestKeepaliveSeatChangeAndAuthorityLossResetProof(t *testing.T) {
	t.Parallel()
	a, b := pair(t)
	old := prove(t, a, b)
	seat := testSeat
	seat.Generation++
	require.NoError(t, a.SetSeat(seat))
	require.False(t, a.Status(testTime.Add(2*time.Second)).Up)
	ok, err := a.Observe(testTime.Add(2*time.Second), old)
	require.Error(t, err)
	require.False(t, ok)
	f := next(t, a, testTime.Add(2*time.Second), false)
	require.Equal(t, seat, f.Seat)
	a.ResetProof()
	require.Zero(t, a.Status(testTime.Add(2*time.Second)).Outstanding)
	_, due, err := a.Next(testTime.Add(2*time.Second), false)
	require.NoError(t, err)
	require.False(t, due, "authority reset must not allow a same-tick second emission")
	newFrame := next(t, a, testTime.Add(3*time.Second), false)
	require.Greater(t, newFrame.Seq, f.Seq)
}

func TestKeepaliveCadenceSkipsMissedTicksAndLedgerStaysBounded(t *testing.T) {
	t.Parallel()
	a, _ := pair(t)
	first := next(t, a, testTime, false)
	_, due, err := a.Next(testTime.Add(time.Second-time.Nanosecond), false)
	require.NoError(t, err)
	require.False(t, due)
	later := next(t, a, testTime.Add(time.Hour), false)
	require.Equal(t, first.Seq+1, later.Seq)
	_, due, err = a.Next(testTime.Add(time.Hour), false)
	require.NoError(t, err)
	require.False(t, due)
	for i := 1; i <= 1000; i++ {
		now := testTime.Add(time.Hour + time.Duration(i)*time.Second)
		next(t, a, now, false)
		require.LessOrEqual(t, a.Status(now).Outstanding, OutstandingLimit)
	}
	b, _ := pair(t)
	b.Every = 5 * time.Second
	next(t, b, testTime, false)
	_, due, err = b.Next(testTime.Add(time.Second), false)
	require.NoError(t, err)
	require.False(t, due)
	next(t, b, testTime.Add(5*time.Second), false)
	b.Every = time.Millisecond
	_, _, err = b.Next(testTime.Add(6*time.Second), false)
	require.Error(t, err)
}

func TestPeerTransitionInvalidatesLargerPreTransitionAcknowledgement(t *testing.T) {
	t.Parallel()
	a, b := pair(t)
	prove(t, a, b)
	third := next(t, a, testTime.Add(2*time.Second), false)
	fourth := next(t, a, testTime.Add(3*time.Second), false)
	newPeer := Frame{Version: Version, From: "bob", To: "ada", Role: "friend", Seat: testSeat, Instance: "b2", Seq: 1, AckInstance: third.Instance, AckSeq: third.Seq}
	ok, err := a.Observe(testTime.Add(3*time.Second), newPeer)
	require.NoError(t, err)
	require.True(t, ok)
	oldPeer := newPeer
	oldPeer.Instance = "b1"
	oldPeer.Seq = 10
	oldPeer.AckSeq = fourth.Seq
	ok, err = a.Observe(testTime.Add(4*time.Second), oldPeer)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, "b2", a.Status(testTime.Add(4*time.Second)).PeerInstance)
	postTransition := next(t, a, testTime.Add(4*time.Second), false)
	require.Equal(t, "b2", postTransition.AckInstance, "rejected old proof must not poison the piggyback target")
	require.Equal(t, uint64(1), postTransition.AckSeq)
	newPeer.Seq = 2
	newPeer.AckSeq = postTransition.Seq
	ok, err = a.Observe(testTime.Add(5*time.Second), newPeer)
	require.NoError(t, err)
	require.True(t, ok)
}
