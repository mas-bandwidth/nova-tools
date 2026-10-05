package friend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ladder is the ladder's world: a fake clock a second per sleep, proof that
// appears at a chosen instant, and every effect and line recorded.
type ladder struct {
	now     time.Time
	proofAt time.Time // zero: never
	skip    map[ReachStep]string
	fail    map[ReachStep]error
	did     []ReachStep
	lines   []string
}

func (l *ladder) effects() ReachEffects {
	return ReachEffects{
		Do: func(_ context.Context, s ReachStep) (string, string, error) {
			if err := l.fail[s]; err != nil {
				return "", "", err
			}
			if why := l.skip[s]; why != "" {
				return "", why, nil
			}
			l.did = append(l.did, s)
			return "id-" + string(s), "", nil
		},
		Proof: func(context.Context) (string, bool, error) {
			return "pong", !l.proofAt.IsZero() && !l.now.Before(l.proofAt), nil
		},
		Now:   func() time.Time { return l.now },
		Sleep: func(_ context.Context, d time.Duration) { l.now = l.now.Add(d) },
		Event: func(e ReachEvent) { l.lines = append(l.lines, e.Line()) },
	}
}

func TestReachLadderMachine(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		steps   []ReachStep
		proofIn time.Duration // from t0; 0 never
		skip    map[ReachStep]string
		ok      bool
		at      ReachStep
		did     []ReachStep
		lines   []string
	}{
		{name: "proof at the first step stops the ladder", steps: ReachSteps, proofIn: 2 * time.Second, ok: true, at: ReachBus, did: []ReachStep{ReachBus},
			lines: []string{"REACH STEP step=bus sent=id-bus nonce=n1", "REACH PROOF step=bus after=2s by=pong"}},
		{name: "proof in the second step's wait", steps: ReachSteps, proofIn: 5 * time.Second, ok: true, at: ReachPush, did: []ReachStep{ReachBus, ReachPush},
			lines: []string{"REACH STEP step=bus sent=id-bus nonce=n1", "REACH NONE step=bus waited=3s", "REACH STEP step=push sent=id-push nonce=n1", "REACH PROOF step=push after=2s by=pong"}},
		{name: "no proof fails after all three", steps: ReachSteps, did: ReachSteps,
			lines: []string{"REACH STEP step=bus sent=id-bus nonce=n1", "REACH NONE step=bus waited=3s", "REACH STEP step=push sent=id-push nonce=n1", "REACH NONE step=push waited=3s", "REACH STEP step=window sent=id-window nonce=n1", "REACH NONE step=window waited=3s"}},
		{name: "a skipped step waits nothing and climbs", steps: ReachSteps, proofIn: 4 * time.Second, skip: map[ReachStep]string{ReachPush: "the daemon is not up"}, ok: true, at: ReachWindow, did: []ReachStep{ReachBus, ReachWindow},
			lines: []string{"REACH STEP step=bus sent=id-bus nonce=n1", "REACH NONE step=bus waited=3s", `REACH SKIP step=push reason="the daemon is not up"`, "REACH STEP step=window sent=id-window nonce=n1", "REACH PROOF step=window after=1s by=pong"}},
		{name: "from a later step", steps: ReachSteps[2:], did: []ReachStep{ReachWindow},
			lines: []string{"REACH STEP step=window sent=id-window nonce=n1", "REACH NONE step=window waited=3s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := &ladder{now: t0, skip: c.skip}
			if c.proofIn > 0 {
				l.proofAt = t0.Add(c.proofIn)
			}
			res, err := Reach{Steps: c.steps, Nonce: "n1", Timeout: 3 * time.Second, Poll: time.Second}.Run(context.Background(), l.effects())
			require.NoError(t, err)
			assert.Equal(t, c.ok, res.OK)
			if c.ok {
				assert.Equal(t, c.at, res.Step)
			}
			assert.Equal(t, c.did, l.did, "no step's effect after the proof")
			assert.Equal(t, c.lines, l.lines)
		})
	}
}

func TestReachAnEffectThatCannotRunEndsTheLadder(t *testing.T) {
	t.Parallel()
	l := &ladder{now: time.Unix(0, 0), fail: map[ReachStep]error{ReachPush: errors.New("store down")}}
	_, err := Reach{Steps: ReachSteps, Nonce: "n1", Timeout: time.Second, Poll: time.Second}.Run(context.Background(), l.effects())
	require.ErrorContains(t, err, "step push: store down")
	assert.Equal(t, []ReachStep{ReachBus}, l.did)
}

func TestReachFromNamesTheSuffix(t *testing.T) {
	t.Parallel()
	got, ok := ReachFrom("push")
	require.True(t, ok)
	assert.Equal(t, []ReachStep{ReachPush, ReachWindow}, got)
	_, ok = ReachFrom("phone")
	assert.False(t, ok)
	assert.Equal(t, "bus,window", StepNames([]ReachStep{ReachBus, ReachWindow}))
	assert.Equal(t, "-", StepNames(nil))
}

func TestReachProofIsTheSessionsOwn(t *testing.T) {
	t.Parallel()
	entry := func(from, subject, body string) bus.Entry {
		return bus.Entry{Entry: "1-0", Fields: map[string]string{"from": from, "to": "ada", "subject": subject, "body": body, "at": "2026-10-05T12:00:00Z"}}
	}
	cases := []struct {
		name    string
		entries []bus.Entry
		by      string
	}{
		{"the pong for the nonce", []bus.Entry{entry("bob", PongSubject, PongLine("n1", 0, 0, 0))}, "pong"},
		{"a real message", []bus.Entry{entry("bob", "done with the card", "it landed")}, "message"},
		{"a pong for another nonce", []bus.Entry{entry("bob", PongSubject, PongLine("n0", 0, 0, 0))}, ""},
		{"the daemon's pong", []bus.Entry{entry("bob", DaemonPongSubject, "daemon-pong n1")}, ""},
		{"a session check", []bus.Entry{entry("bob", SessionCheckPrefix+"x1", "check")}, ""},
		{"someone else", []bus.Entry{entry("cy", "hello", "hi")}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			by, ok := ReachProof(c.entries, "bob", "n1")
			assert.Equal(t, c.by != "", ok)
			assert.Equal(t, c.by, by)
		})
	}
}

func TestReachDaemonIsUpByItsStatusFile(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	assert.Contains(t, ReachDaemon(Status{}, false, now), "no status file")
	assert.Contains(t, ReachDaemon(Status{At: now.Add(-time.Minute)}, true, now), "is 1m0s old")
	assert.Empty(t, ReachDaemon(Status{At: now.Add(-5 * time.Second)}, true, now))
}
