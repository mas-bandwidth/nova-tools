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
	up := DaemonFacts{Friend: "bob", Presence: PresenceUp, Agent: "loaded", Status: "ok", Hook: HookProven, Reason: "-"}
	unhooked := DaemonFacts{Friend: "bob", Presence: PresenceDown, Agent: "none", Status: "none", Hook: "unhooked", Reason: "missed 2 challenges"}
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
		shown   *ShownEntry
		verdict string
		why     string
	}{
		{"broken session", up, HarnessFacts{Friend: "bob", Broken: "2026-10-05T10:00:00Z", Reason: "rate limit exceeded"}, nil, VerdictBroken, "session broken: rate limit exceeded"},
		{"every lane turn in the window failed", up, hf(3, 3), nil, VerdictBroken, "every lane turn in the window failed (3 of 3)"},
		{"one failure among successes is not broken", up, hf(3, 1), nil, VerdictOK, "live: hook proven"},
		{"a proven record is live with no daemon at all", DaemonFacts{Friend: "bob", Presence: PresenceUp, Agent: "none", Status: "none", Hook: HookProven, Reason: "-"}, hf(0, 0), nil, VerdictOK, "live: hook proven"},
		{"an unhooked record is down with its reason", unhooked, hf(0, 0), nil, VerdictDown, "hook unhooked: missed 2 challenges"},
		{"a record not read is down", DaemonFacts{Friend: "bob", Presence: PresenceDown, Hook: "-", Reason: "connection refused"}, hf(0, 0), nil, VerdictDown, "hook -: connection refused"},
		{"shown up, broken: why leads with untrue", up, HarnessFacts{Friend: "bob", Broken: "2026-10-05T10:00:00Z", Reason: "quota"}, claim, VerdictBroken, "untrue: shown up/2, session broken: quota"},
		{"shown up, down: why leads with untrue", unhooked, hf(0, 0), claim, VerdictDown, "untrue: shown up/2, hook unhooked: missed 2 challenges"},
		{"shown up and live: ok", up, hf(1, 0), &ShownEntry{State: "up"}, VerdictOK, "live: hook proven"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			vf := DecideVerdict(c.df, c.hf, bob, wk, c.shown, window)
			assert.Equal(t, c.verdict, vf.Verdict)
			assert.Equal(t, c.why, vf.Why)
		})
	}
}

// The daemon's status file says only that its loop runs; presence is the hook
// record's, read through the ReadHook seam, whatever the file's age.
func TestCheckFriendReadsPresenceFromTheHookRecord(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 23, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		name     string
		age      time.Duration
		hook     HookRecord
		status   string
		presence string
		verdict  string
	}{
		{"fresh daemon, proven record", time.Second, HookRecord{State: HookProven, Since: now.Add(-time.Hour), LastAnswer: now.Add(-time.Second)}, "ok", PresenceUp, VerdictOK},
		{"stale daemon, proven record", time.Minute, HookRecord{State: HookProven, Since: now.Add(-time.Hour), LastAnswer: now.Add(-time.Second)}, "stale", PresenceUp, VerdictOK},
		{"fresh daemon, unhooked record", time.Second, HookRecord{State: "unhooked", Since: now.Add(-time.Minute), Misses: 2, Down: "missed 2 challenges"}, "ok", PresenceDown, VerdictDown},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			st := Status{Friend: "bob", At: now.Add(-row.age)}
			fc := CheckFriend(context.Background(), "bob", CheckSeams{
				Now:        func() time.Time { return now },
				Launchctl:  func(context.Context, ...string) (string, error) { return "123 0 com.nova.friend-bob", nil },
				ReadStatus: func(string) (Status, bool, error) { return st, true, nil },
				ReadHook:   func(string) (HookRecord, error) { return row.hook, nil },
				HarnessDir: func(string) (string, string, error) { return "opencode", "", nil },
			}, time.Hour, nil)
			assert.Equal(t, row.status, fc.Daemon.Status)
			assert.Equal(t, row.presence, fc.Daemon.Presence)
			assert.Equal(t, row.verdict, fc.Verdict.Verdict)
			assert.Equal(t, row.hook.Misses, fc.Daemon.Misses)
			if !row.hook.LastAnswer.IsZero() {
				assert.Equal(t, "1s", fc.Daemon.Answer, "the model's last answer to a challenge")
			}
		})
	}
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
	assert.False(t, IsRealMessage("pong"))
	assert.False(t, IsRealMessage(" Pong "))
	assert.True(t, IsRealMessage("work"))
	assert.True(t, IsRealMessage("turn"))
	assert.True(t, IsRealMessage("card 123"))
}

func TestLineFormatting(t *testing.T) {
	t.Parallel()
	df := DaemonFacts{
		Friend: "bob", Agent: "loaded", PID: "123", Status: "ok",
		Hook: HookProven, Since: "1h0m0s", Answer: "5s", Misses: 0, Presence: "up", Reason: "-",
	}
	assert.Equal(t, "CHECK DAEMON friend=bob agent=loaded pid=123 status=ok hook=proven since=1h0m0s answer=5s misses=0 presence=up reason=-", df.Line())

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
	assert.Equal(t, "CHECK OK friends=1 ok=1 broken=0 down=0 untrue=0", summary.Line())
}
