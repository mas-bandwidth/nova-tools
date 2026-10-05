package friend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideVerdictPure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	// 1. Broken when session broken
	t.Run("broken session", func(t *testing.T) {
		t.Parallel()
		df := DaemonFacts{Friend: "bob", Presence: "up"}
		hf := HarnessFacts{Friend: "bob", Broken: "2026-10-05T10:00:00Z", Reason: "rate limit exceeded"}
		bf := BusFacts{Friend: "bob"}
		wf := WorkFacts{Friend: "bob"}
		vf := DecideVerdict(df, hf, bf, wf, nil, now)
		assert.Equal(t, VerdictBroken, vf.Verdict)
		assert.Contains(t, vf.Why, "rate limit exceeded")
	})

	// 1b. Broken when every delivery failed
	t.Run("all deliveries failed", func(t *testing.T) {
		t.Parallel()
		df := DaemonFacts{Friend: "bob", Presence: "up"}
		hf := HarnessFacts{Friend: "bob", Broken: "-", LastExit: "1", FailedOfLast20: 3}
		bf := BusFacts{Friend: "bob"}
		wf := WorkFacts{Friend: "bob"}
		vf := DecideVerdict(df, hf, bf, wf, nil, now)
		assert.Equal(t, VerdictBroken, vf.Verdict)
		assert.Equal(t, "every delivery in window failed", vf.Why)
	})

	// 2. Deaf when deliveries succeed but no session pong or real message
	t.Run("deaf friend", func(t *testing.T) {
		t.Parallel()
		df := DaemonFacts{Friend: "bob", Presence: "up", PongAge: "-"}
		hf := HarnessFacts{Friend: "bob", Broken: "-", LastExit: "0", FailedOfLast20: 0}
		bf := BusFacts{Friend: "bob", RealSince: 0}
		wf := WorkFacts{Friend: "bob"}
		vf := DecideVerdict(df, hf, bf, wf, nil, now)
		assert.Equal(t, VerdictDeaf, vf.Verdict)
		assert.Equal(t, "deliveries succeed but no session pong or real message came back", vf.Why)
	})

	// 3. Silent when no delivery due and nothing came back
	t.Run("silent friend", func(t *testing.T) {
		t.Parallel()
		df := DaemonFacts{Friend: "bob", Presence: "up", PongAge: "-"}
		hf := HarnessFacts{Friend: "bob", Broken: "-", Last: "-", LastExit: "-", Deferred: 0}
		bf := BusFacts{Friend: "bob", RealSince: 0}
		wf := WorkFacts{Friend: "bob", Inbox: 0}
		vf := DecideVerdict(df, hf, bf, wf, nil, now)
		assert.Equal(t, VerdictSilent, vf.Verdict)
		assert.Equal(t, "no delivery was due and nothing came back", vf.Why)
	})

	// 4. Down by presence
	t.Run("down friend", func(t *testing.T) {
		t.Parallel()
		df := DaemonFacts{Friend: "bob", Presence: "down", Agent: "loaded", Status: "ok"}
		hf := HarnessFacts{Friend: "bob", Broken: "-", Last: "2026-10-05T11:00:00Z", LastExit: "0"}
		bf := BusFacts{Friend: "bob", RealSince: 2}
		wf := WorkFacts{Friend: "bob"}
		vf := DecideVerdict(df, hf, bf, wf, nil, now)
		assert.Equal(t, VerdictDown, vf.Verdict)
		assert.Equal(t, "down by presence", vf.Why)
	})

	// 5. Untrue when shown says up/working but facts say down
	t.Run("untrue shown up facts down", func(t *testing.T) {
		t.Parallel()
		df := DaemonFacts{Friend: "bob", Presence: "down", Agent: "none"}
		hf := HarnessFacts{Friend: "bob", Broken: "-"}
		bf := BusFacts{Friend: "bob"}
		wf := WorkFacts{Friend: "bob"}
		shown := &ShownEntry{State: "up", Working: 2}
		vf := DecideVerdict(df, hf, bf, wf, shown, now)
		assert.Equal(t, VerdictUntrue, vf.Verdict)
		assert.Contains(t, vf.Why, "shown up/working 2, facts say down by presence")
		assert.Equal(t, "up/2", vf.Shown)
	})

	// 6. Ok live friend
	t.Run("ok friend", func(t *testing.T) {
		t.Parallel()
		df := DaemonFacts{Friend: "bob", Presence: "up", Agent: "loaded", Status: "ok", PongAge: "3s"}
		hf := HarnessFacts{Friend: "bob", Broken: "-", Last: "2026-10-05T11:59:00Z", LastExit: "0", Route: "push"}
		bf := BusFacts{Friend: "bob", RealSince: 1, LastReal: "2026-10-05T11:59:30Z"}
		wf := WorkFacts{Friend: "bob", Inbox: 0, Outbox: 1}
		shown := &ShownEntry{State: "up", Working: 0}
		vf := DecideVerdict(df, hf, bf, wf, shown, now)
		assert.Equal(t, VerdictOK, vf.Verdict)
		assert.Equal(t, "live", vf.Why)
		assert.Equal(t, "up/0", vf.Shown)
	})
}

func TestParseLog(t *testing.T) {
	t.Parallel()
	lines := []string{
		"2026-10-05T01:00:00Z subject=ping exit=0",
		"RUN 2026-10-05T02:00:00Z deferred=1",
		"2026-10-05T03:00:00Z subject=task exit=1",
		"2026-10-05T04:00:00Z subject=card exit=0",
	}
	last, lastExit, failed, deferred := ParseLog(lines)
	assert.Equal(t, "2026-10-05T04:00:00Z", last)
	assert.Equal(t, "0", lastExit)
	assert.Equal(t, 1, failed)
	assert.Equal(t, 1, deferred)
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
	assert.Equal(t, "CHECK DAEMON friend=bob agent=loaded pid=123 status=ok connection=connected challenge=quiet pong_age=5s presence=up seen_age=2s", df.Line())

	hf := HarnessFacts{
		Friend: "bob", Harness: "opencode", Route: "push",
		Last: "2026-10-05T10:00:00Z", LastExit: "0", FailedOfLast20: 0,
		Deferred: 0, Broken: "-", Reason: "-",
	}
	assert.Equal(t, "CHECK HARNESS friend=bob harness=opencode route=push last=2026-10-05T10:00:00Z last_exit=0 failed_of_last20=0 deferred=0 broken=- reason=-", hf.Line())

	bf := BusFacts{Friend: "bob", RealSince: 4, LastReal: "2026-10-05T10:05:00Z"}
	assert.Equal(t, "CHECK BUS friend=bob real_since=4 last_real=2026-10-05T10:05:00Z", bf.Line())

	wf := WorkFacts{Friend: "bob", Inbox: 1, Outbox: 2, NewestOutbox: "out.md", NewestAt: "2026-10-05T10:06:00Z"}
	assert.Equal(t, "CHECK WORK friend=bob inbox=1 outbox=2 newest_outbox=out.md newest_at=2026-10-05T10:06:00Z", wf.Line())

	vf := VerdictFacts{Friend: "bob", Verdict: "ok", Shown: "up/0", Why: "live"}
	assert.Equal(t, "CHECK VERDICT friend=bob verdict=ok shown=up/0 why=live", vf.Line())

	summary := CheckSummary{Friends: 1, OK: 1}
	assert.Equal(t, "CHECK OK friends=1 ok=1 broken=0 deaf=0 silent=0 down=0 untrue=0", summary.Line())
}
