package friend

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideVerdictPure(t *testing.T) {
	t.Parallel()
	const window = 24 * time.Hour
	up := DaemonFacts{Friend: "bob", Presence: "up", Agent: "loaded", Status: "ok", PongAge: "-"}
	hf := func(delivered, failed int) HarnessFacts {
		return HarnessFacts{Friend: "bob", Broken: "-", Reason: "-", Delivered: delivered, Failed: failed}
	}
	bob := BusFacts{Friend: "bob"}
	wk := WorkFacts{Friend: "bob"}
	claim := &ShownEntry{State: "up", Working: 2}

	cases := []struct {
		name    string
		df      DaemonFacts
		hf      HarnessFacts
		bf      BusFacts
		shown   *ShownEntry
		verdict string
		why     string
	}{
		{"broken session", up, HarnessFacts{Friend: "bob", Broken: "2026-10-05T10:00:00Z", Reason: "rate limit exceeded"}, bob, nil, VerdictBroken, "session broken: rate limit exceeded"},
		{"every delivery in the window failed", up, hf(3, 3), bob, nil, VerdictBroken, "every delivery in the window failed (3 of 3)"},
		{"one failure among successes is not broken", up, hf(3, 1), BusFacts{RealSince: 1}, nil, VerdictOK, "live"},
		{"deaf: deliveries succeed, nothing came back", up, hf(2, 0), bob, nil, VerdictDeaf, "deliveries succeed but no session pong or real message came back in the window"},
		{"deaf: a failure beside a success is still deaf", up, hf(2, 1), bob, nil, VerdictDeaf, "deliveries succeed but no session pong or real message came back in the window"},
		{"a pong outside the window does not answer", withPong(up, "25h0m0s"), hf(1, 0), bob, nil, VerdictDeaf, "deliveries succeed but no session pong or real message came back in the window"},
		{"a pong in the window answers", withPong(up, "4s"), hf(1, 0), bob, nil, VerdictOK, "live"},
		{"silent: no delivery was due", up, hf(0, 0), bob, nil, VerdictSilent, "no delivery was due and nothing came back in the window"},
		{"down by presence", DaemonFacts{Friend: "bob", Presence: "down", Agent: "loaded", Status: "ok", PongAge: "-"}, hf(1, 0), BusFacts{RealSince: 2}, nil, VerdictDown, "down by presence"},
		{"stale status cannot be live despite fresh presence", DaemonFacts{Friend: "bob", Presence: "up", Agent: "loaded", Status: "stale", PongAge: "4s"}, hf(1, 0), BusFacts{RealSince: 1}, nil, VerdictDown, "stale daemon status"},
		{"shown up, broken: why leads with untrue", up, HarnessFacts{Friend: "bob", Broken: "2026-10-05T10:00:00Z", Reason: "quota"}, bob, claim, VerdictBroken, "untrue: shown up/2, session broken: quota"},
		{"shown up, deaf: why leads with untrue", up, hf(2, 0), bob, claim, VerdictDeaf, "untrue: shown up/2, deliveries succeed but no session pong or real message came back in the window"},
		{"shown up, silent: why leads with untrue", up, hf(0, 0), bob, claim, VerdictSilent, "untrue: shown up/2, no delivery was due and nothing came back in the window"},
		{"shown up, down: why leads with untrue", DaemonFacts{Friend: "bob", Presence: "down", Agent: "none", PongAge: "-"}, hf(0, 0), bob, claim, VerdictDown, "untrue: shown up/2, down by presence"},
		{"shown working, asleep: untrue", DaemonFacts{Friend: "bob", Presence: "asleep", Agent: "loaded", Status: "ok", PongAge: "4s"}, hf(1, 0), bob, &ShownEntry{State: "asleep", Working: 1}, VerdictUntrue, "shown asleep/1, facts say asleep"},
		{"shown up, agent not loaded: untrue", DaemonFacts{Friend: "bob", Presence: "up", Agent: "not-loaded", Status: "ok", PongAge: "4s"}, hf(1, 0), bob, claim, VerdictUntrue, "shown up/2, facts say agent not-loaded"},
		{"shown up and live: ok", withPong(up, "3s"), hf(1, 0), BusFacts{RealSince: 1}, &ShownEntry{State: "up"}, VerdictOK, "live"},
		{"shown asleep, nothing working: no claim", up, hf(0, 0), bob, &ShownEntry{State: "asleep"}, VerdictSilent, "no delivery was due and nothing came back in the window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			vf := DecideVerdict(c.df, c.hf, c.bf, wk, c.shown, window)
			assert.Equal(t, c.verdict, vf.Verdict)
			assert.Equal(t, c.why, vf.Why)
		})
	}
}

func TestCheckFriendDaemonFreshnessMatchesStatusBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 23, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		name     string
		age      time.Duration
		status   string
		presence string
		verdict  string
	}{
		{"before boundary", DaemonStale - time.Nanosecond, "ok", PresenceUp, VerdictOK},
		{"at boundary", DaemonStale, "stale", PresenceDown, VerdictDown},
		{"after boundary", DaemonStale + time.Nanosecond, "stale", PresenceDown, VerdictDown},
		{"old status with recent session presence", time.Minute, "stale", PresenceDown, VerdictDown},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			st := Status{Friend: "bob", At: now.Add(-row.age)}
			pr := PresenceStatus{Friend: "bob", Presence: PresenceUp, LastHeard: now.Add(-time.Second)}
			fc := CheckFriend(context.Background(), "bob", CheckSeams{
				Now:          func() time.Time { return now },
				Launchctl:    func(context.Context, ...string) (string, error) { return "123 0 com.nova.friend-bob", nil },
				ReadStatus:   func(string) (Status, bool, error) { return st, true, nil },
				ReadPresence: func(string) (PresenceStatus, bool, error) { return pr, true, nil },
				ReadPong:     func(string) (Pong, bool, error) { return Pong{At: now.Add(-time.Second)}, true, nil },
				HarnessDir:   func(string) (string, string, error) { return "opencode", "", nil },
			}, time.Hour, &ShownEntry{State: PresenceUp, Working: 2})
			assert.Equal(t, row.status, fc.Daemon.Status)
			assert.Equal(t, row.presence, fc.Daemon.Presence)
			assert.Equal(t, "1s", fc.Daemon.SeenAge, "recent session evidence is still shown")
			assert.Equal(t, row.verdict, fc.Verdict.Verdict)
			if row.status == "stale" {
				assert.Contains(t, fc.Verdict.Why, "untrue: shown up/2, stale daemon status")
			}
		})
	}
}

func withPong(df DaemonFacts, age string) DaemonFacts {
	df.PongAge = age
	return df
}

func TestParseLogCountsOnlyTheWindow(t *testing.T) {
	t.Parallel()
	lines := []string{
		"2026-10-05T01:00:00Z subject=ping exit=1",
		"RUN 2026-10-05T02:00:00Z deferred=1",
		"2026-10-05T03:00:00Z subject=task exit=1",
		"2026-10-05T04:00:00Z subject=card exit=0",
		"subject=unstamped exit=1",
	}
	all := ParseLog(lines, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, LogFacts{Last: "2026-10-05T04:00:00Z", LastExit: "0", FailedOfLast20: 2, Deferred: 1, Delivered: 3, Failed: 2}, all)

	late := ParseLog(lines, time.Date(2026, 10, 5, 2, 30, 0, 0, time.UTC))
	assert.Equal(t, LogFacts{Last: "2026-10-05T04:00:00Z", LastExit: "0", FailedOfLast20: 1, Deferred: 0, Delivered: 2, Failed: 1}, late)

	none := ParseLog(lines, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, LogFacts{Last: "-", LastExit: "-"}, none)
}

func TestParseLogFailedOfLast20LooksAtTheNewestTwenty(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 25 {
		exit := 0
		if i < 5 {
			exit = 1
		}
		lines = append(lines, time.Date(2026, 10, 5, 0, i, 0, 0, time.UTC).Format(time.RFC3339)+" exit="+strconv.Itoa(exit))
	}
	lf := ParseLog(lines, time.Time{}.Add(time.Hour))
	assert.Equal(t, 0, lf.FailedOfLast20)
	assert.Equal(t, 5, lf.Failed)
	assert.Equal(t, 25, lf.Delivered)
}

func TestParseShown(t *testing.T) {
	t.Parallel()

	// Map format
	m, err := ParseShown([]byte(`{"bob": {"state": "up", "working": 3}}`))
	require.NoError(t, err)
	assert.Equal(t, 3, m["bob"].Working)
	assert.Equal(t, "up", m["bob"].State)

	// List format
	m, err = ParseShown([]byte(`[{"friend": "ada", "state": "down", "working": 0}]`))
	require.NoError(t, err)
	assert.Equal(t, "down", m["ada"].State)

	// Single format
	m, err = ParseShown([]byte(`{"state": "up", "working": 1}`))
	require.NoError(t, err)
	entry := LookupShown(m, "bob")
	require.NotNil(t, entry)
	assert.Equal(t, "up", entry.State)
	assert.Equal(t, 1, entry.Working)
}

func TestIsRealMessage(t *testing.T) {
	t.Parallel()
	assert.False(t, IsRealMessage("ping"))
	assert.False(t, IsRealMessage("pong"))
	assert.False(t, IsRealMessage("daemon-pong"))
	assert.False(t, IsRealMessage("keepalive"))
	assert.True(t, IsRealMessage("work"))
	assert.True(t, IsRealMessage("turn"))
	assert.True(t, IsRealMessage("card 123"))
}

func TestLineFormatting(t *testing.T) {
	t.Parallel()
	df := DaemonFacts{
		Friend: "bob", Agent: "loaded", PID: "123", Status: "ok",
		Connection: "connected", Challenge: "quiet", PongAge: "5s",
		Presence: "up", SeenAge: "2s",
	}
	assert.Equal(t, "CHECK DAEMON friend=bob agent=loaded pid=123 status=ok connection=connected challenge=quiet pong_age=5s presence=up seen_age=2s proof=none proof_age=-", df.Line())
	df.Proof, df.ProofAge = "pending", "4m0s"
	assert.Contains(t, df.Line(), " proof=pending proof_age=4m0s", "a daemon waiting for its push proof says so")

	hf := HarnessFacts{
		Friend: "bob", Harness: "opencode", Route: "push",
		Last: "2026-10-05T10:00:00Z", LastExit: "0", FailedOfLast20: 0,
		Deferred: 0, Broken: "-", Reason: "-",
	}
	assert.Equal(t, "CHECK HARNESS friend=bob harness=opencode route=push last=2026-10-05T10:00:00Z last_exit=0 failed_of_last20=0 deferred=0 broken=- reason=- session_live=- queued=-", hf.Line())

	bf := BusFacts{Friend: "bob", RealSince: 4, LastReal: "2026-10-05T10:05:00Z"}
	assert.Equal(t, "CHECK BUS friend=bob real_since=4 last_real=2026-10-05T10:05:00Z", bf.Line())

	wf := WorkFacts{Friend: "bob", Inbox: 1, Outbox: 2, NewestOutbox: "out.md", NewestAt: "2026-10-05T10:06:00Z"}
	assert.Equal(t, "CHECK WORK friend=bob inbox=1 outbox=2 newest_outbox=out.md newest_at=2026-10-05T10:06:00Z", wf.Line())

	vf := VerdictFacts{Friend: "bob", Verdict: "ok", Shown: "up/0", Why: "live"}
	assert.Equal(t, "CHECK VERDICT friend=bob verdict=ok shown=up/0 why=live", vf.Line())

	summary := CheckSummary{Friends: 1, OK: 1}
	assert.Equal(t, "CHECK OK friends=1 ok=1 broken=0 deaf=0 silent=0 down=0 untrue=0", summary.Line())
}
