package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverStore embeds bus.Store and overrides only Range, so BusLogEntries reads a
// scripted log with no Redis and no socket.
type coverStore struct {
	bus.Store
	entries []bus.Entry
	err     error
}

func (s *coverStore) Range(context.Context, string, string, string, int) ([]bus.Entry, error) {
	return s.entries, s.err
}

// coverSeams is the seams every CheckFriend cover case starts from: a fixed clock,
// an opencode harness, and nothing found on disk or on the bus.
func coverSeams(now time.Time) CheckSeams {
	return CheckSeams{
		Now:          func() time.Time { return now },
		HarnessDir:   func(string) (string, string, error) { return "opencode", "", nil },
		ReadStatus:   func(string) (Status, bool, error) { return Status{}, false, nil },
		ReadPresence: func(string) (PresenceStatus, bool, error) { return PresenceStatus{}, false, nil },
		ReadPong:     func(string) (Pong, bool, error) { return Pong{}, false, nil },
	}
}

// coverHomeWithPlist makes a home directory holding friend's LaunchAgents plist.
func coverHomeWithPlist(t *testing.T, friend string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "com.nova.friend-"+friend+".plist"), nil, 0o644))
	return home
}

func TestFriendCheckCoverDefaultReadWork(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	t.Run("empty dir is zeros and a dash", func(t *testing.T) {
		t.Parallel()
		inbox, outbox, name, at, err := DefaultReadWork("")
		require.NoError(t, err)
		assert.Equal(t, 0, inbox)
		assert.Equal(t, 0, outbox)
		assert.Equal(t, "-", name)
		assert.True(t, at.IsZero())
	})
	t.Run("no work directories is zeros and no error", func(t *testing.T) {
		t.Parallel()
		inbox, outbox, name, at, err := DefaultReadWork(t.TempDir())
		require.NoError(t, err)
		assert.Equal(t, 0, inbox)
		assert.Equal(t, 0, outbox)
		assert.Equal(t, "-", name)
		assert.True(t, at.IsZero())
	})
	t.Run("inbox skips the dotfile and the queue, outbox names the newest", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inboxDir, outboxDir := filepath.Join(dir, "inbox"), filepath.Join(dir, "outbox")
		require.NoError(t, os.MkdirAll(inboxDir, 0o755))
		require.NoError(t, os.MkdirAll(outboxDir, 0o755))
		for _, name := range []string{".hidden", "QUEUE.json", "a1", "a2"} {
			require.NoError(t, os.WriteFile(filepath.Join(inboxDir, name), nil, 0o644))
		}
		require.NoError(t, os.WriteFile(filepath.Join(outboxDir, ".hidden"), nil, 0o644))
		older, newer := filepath.Join(outboxDir, "old.md"), filepath.Join(outboxDir, "new.md")
		require.NoError(t, os.WriteFile(older, nil, 0o644))
		require.NoError(t, os.WriteFile(newer, nil, 0o644))
		require.NoError(t, os.Chtimes(older, fixed.Add(-time.Hour), fixed.Add(-time.Hour)))
		require.NoError(t, os.Chtimes(newer, fixed, fixed))
		inbox, outbox, name, at, err := DefaultReadWork(dir)
		require.NoError(t, err)
		assert.Equal(t, 2, inbox)
		assert.Equal(t, 2, outbox)
		assert.Equal(t, "new.md", name)
		assert.Equal(t, fixed, at.UTC())
	})
}

func TestFriendCheckCoverComputeSummary(t *testing.T) {
	t.Parallel()
	var checks []FriendCheck
	for _, v := range []string{VerdictOK, VerdictBroken, VerdictDeaf, VerdictSilent, VerdictDown, VerdictUntrue} {
		checks = append(checks, FriendCheck{Verdict: VerdictFacts{Verdict: v}})
	}
	checks = append(checks, FriendCheck{Verdict: VerdictFacts{Verdict: "unknown"}})
	assert.Equal(t, CheckSummary{Friends: 7, OK: 1, Broken: 1, Deaf: 1, Silent: 1, Down: 1, Untrue: 1}, ComputeSummary(checks))
}

func TestFriendCheckCoverBusLogEntries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	since := now.Add(-time.Hour)
	at := func(d time.Duration) string { return now.Add(d).UTC().Format(time.RFC3339) }
	entry := func(from, subject, stamp string) bus.Entry {
		return bus.Entry{Fields: map[string]string{"from": from, "subject": subject, "at": stamp}}
	}
	t.Run("a nil store is zeros and no error", func(t *testing.T) {
		t.Parallel()
		count, last, err := BusLogEntries(context.Background(), nil, "bob", since)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
		assert.True(t, last.IsZero())
	})
	t.Run("a Range error is returned", func(t *testing.T) {
		t.Parallel()
		_, _, err := BusLogEntries(context.Background(), &coverStore{err: errors.New("injected range failure")}, "bob", since)
		assert.ErrorContains(t, err, "injected range failure")
	})
	t.Run("only real messages from the friend are counted, last_real is the newest", func(t *testing.T) {
		t.Parallel()
		st := &coverStore{entries: []bus.Entry{
			entry("ada", "work", at(-30*time.Minute)),      // another friend
			entry("bob", "ping", at(-30*time.Minute)),      // a ping
			entry("bob", "keepalive", at(-30*time.Minute)), // a keepalive
			entry("bob", "turn", "not-a-stamp"),            // a bad "at"
			entry("bob", "turn", at(-2*time.Hour)),         // before since, still moves last_real
			entry("bob", "turn", at(-30*time.Minute)),      // after since
			entry("bob", "turn", at(-10*time.Minute)),      // after since, the newest
		}}
		count, last, err := BusLogEntries(context.Background(), st, "bob", since)
		require.NoError(t, err)
		assert.Equal(t, 2, count)
		assert.Equal(t, now.Add(-10*time.Minute), last)
	})
}

func TestFriendCheckCoverLines(t *testing.T) {
	t.Parallel()
	fc := FriendCheck{Friend: "bob"}
	lines := fc.Lines()
	require.Len(t, lines, 5)
	for i, prefix := range []string{"CHECK DAEMON ", "CHECK HARNESS ", "CHECK BUS ", "CHECK WORK ", "CHECK VERDICT "} {
		assert.True(t, strings.HasPrefix(lines[i], prefix), lines[i])
	}

	cr := CheckReport{Friends: []FriendCheck{fc, {Friend: "cy"}}, Summary: CheckSummary{Friends: 2, OK: 2}}
	out := cr.Lines()
	require.Len(t, out, 11)
	assert.Equal(t, lines, out[:5], "each friend's five lines come first")
	assert.Equal(t, fc.Lines(), out[5:10])
	assert.True(t, strings.HasPrefix(out[len(out)-1], "CHECK OK "), "the summary is the last line")
}

func TestFriendCheckCoverAgeString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0s", AgeString(-time.Second))
	assert.Equal(t, "5s", AgeString(5*time.Second))
	assert.Equal(t, "1m0s", AgeString(time.Minute))
}

func TestFriendCheckCoverLookupShown(t *testing.T) {
	t.Parallel()
	assert.Nil(t, LookupShown(nil, "bob"))
	shown := map[string]ShownEntry{"bob": {State: "up", Working: 1}, "*": {State: "asleep"}}
	exact := LookupShown(shown, "bob")
	require.NotNil(t, exact)
	assert.Equal(t, ShownEntry{State: "up", Working: 1}, *exact)
	wild := LookupShown(shown, "cy")
	require.NotNil(t, wild)
	assert.Equal(t, ShownEntry{State: "asleep"}, *wild)
}

func TestFriendCheckCoverCheckFriendAgent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	t.Run("no launchctl with the plist present is not-loaded", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.Home = coverHomeWithPlist(t, "bob")
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "not-loaded", fc.Daemon.Agent)
	})
	t.Run("launchctl without the label but with the plist is not-loaded", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.Home = coverHomeWithPlist(t, "bob")
		seams.Launchctl = func(context.Context, ...string) (string, error) { return "123 0 com.nova.friend-other", nil }
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "not-loaded", fc.Daemon.Agent)
	})
	t.Run("a list line with no pid keeps the dash", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.Launchctl = func(context.Context, ...string) (string, error) { return "- 0 com.nova.friend-bob", nil }
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "loaded", fc.Daemon.Agent)
		assert.Equal(t, "-", fc.Daemon.PID)
	})
}

func TestFriendCheckCoverCheckFriendProof(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	t.Run("an unproven push is pending with its age", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, Push: PushUnproven, PushSince: now.Add(-4 * time.Minute)}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "pending", fc.Daemon.Proof)
		assert.Equal(t, "4m0s", fc.Daemon.ProofAge)
	})
	t.Run("a taken proof is sent with its age", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, ProofSent: now.Add(-2 * time.Minute)}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "sent", fc.Daemon.Proof)
		assert.Equal(t, "2m0s", fc.Daemon.ProofAge)
	})
}

func TestFriendCheckCoverCheckFriendPresence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	t.Run("no presence file, status ok and a pong inside the bound is up", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, LastPong: now.Add(-3 * time.Second)}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, PresenceUp, fc.Daemon.Presence)
		assert.Equal(t, "3s", fc.Daemon.SeenAge)
		assert.Equal(t, "3s", fc.Daemon.PongAge)
	})
	t.Run("no presence file, a stale pong and only a daemon pong is asleep", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, LastPong: now.Add(-(AnswerBound + time.Second)), LastDaemonPong: now.Add(-7 * time.Second)}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "asleep", fc.Daemon.Presence)
		assert.Equal(t, "7s", fc.Daemon.SeenAge)
	})
	t.Run("presence up with a pong file and no last-heard is seen by the pong", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) { return Status{At: now}, true, nil }
		seams.ReadPresence = func(string) (PresenceStatus, bool, error) {
			return PresenceStatus{Presence: PresenceUp}, true, nil
		}
		seams.ReadPong = func(string) (Pong, bool, error) { return Pong{At: now.Add(-2 * time.Second)}, true, nil }
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, PresenceUp, fc.Daemon.Presence)
		assert.Equal(t, "2s", fc.Daemon.SeenAge)
	})
	t.Run("presence up with no last-heard and no pong is seen by the status pong", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, LastPong: now.Add(-5 * time.Second)}, true, nil
		}
		seams.ReadPresence = func(string) (PresenceStatus, bool, error) {
			return PresenceStatus{Presence: PresenceUp}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, PresenceUp, fc.Daemon.Presence)
		assert.Equal(t, "5s", fc.Daemon.SeenAge)
	})
	t.Run("presence down with an ok status is asleep by the daemon pong", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, LastDaemonPong: now.Add(-9 * time.Second)}, true, nil
		}
		seams.ReadPresence = func(string) (PresenceStatus, bool, error) {
			return PresenceStatus{Presence: PresenceDown}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "asleep", fc.Daemon.Presence)
		assert.Equal(t, "9s", fc.Daemon.SeenAge)
	})
	t.Run("presence down with an ok status and no daemon pong is asleep by the status", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) { return Status{At: now.Add(-time.Second)}, true, nil }
		seams.ReadPresence = func(string) (PresenceStatus, bool, error) {
			return PresenceStatus{Presence: PresenceDown}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "asleep", fc.Daemon.Presence)
		assert.Equal(t, "1s", fc.Daemon.SeenAge)
	})
	t.Run("no presence file and no status is down", func(t *testing.T) {
		t.Parallel()
		fc := CheckFriend(context.Background(), "bob", coverSeams(now), time.Hour, nil)
		assert.Equal(t, PresenceDown, fc.Daemon.Presence)
	})
}

func TestFriendCheckCoverCheckFriendHarness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		name    string
		harness string
		route   string
	}{
		{"claude is passive", "claude", "passive"},
		{"antigravity is mailbox", "antigravity", "mailbox"},
		{"codex is queue", "codex", "queue"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			seams := coverSeams(now)
			seams.HarnessDir = func(string) (string, string, error) { return row.harness, "", nil }
			fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
			assert.Equal(t, row.harness, fc.Harness.Harness)
			assert.Equal(t, row.route, fc.Harness.Route)
		})
	}
	t.Run("an unknown harness takes the status's own", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.HarnessDir = func(string) (string, string, error) { return "", "", nil }
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, Harness: "opencode"}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "opencode", fc.Harness.Harness)
		assert.Equal(t, "push", fc.Harness.Route)
	})
	t.Run("a known codex queue is counted", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.HarnessDir = func(string) (string, string, error) { return "codex", "", nil }
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, Harness: "codex", Queued: 3, QueueKnown: true}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, "3", fc.Harness.Queued)
	})
	t.Run("a broken session with no broken-at is stamped from the status", func(t *testing.T) {
		t.Parallel()
		seams := coverSeams(now)
		seams.ReadStatus = func(string) (Status, bool, error) {
			return Status{At: now, Session: SessionBroken, SessionReason: "provider refused three turns"}, true, nil
		}
		fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
		assert.Equal(t, now.UTC().Format(time.RFC3339), fc.Harness.Broken)
		assert.Equal(t, "provider refused three turns", fc.Harness.Reason)
	})
}

func TestFriendCheckCoverCheckFriendBusAndWork(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	seams := coverSeams(now)
	seams.BusLog = func(context.Context, string, time.Time) (int, time.Time, error) {
		return 4, now.Add(-2 * time.Minute), nil
	}
	seams.ReadWork = func(string, string) (int, int, string, time.Time, error) {
		return 2, 3, "out.md", now.Add(-time.Minute), nil
	}
	fc := CheckFriend(context.Background(), "bob", seams, time.Hour, nil)
	assert.Equal(t, 4, fc.Bus.RealSince)
	assert.Equal(t, now.Add(-2*time.Minute).UTC().Format(time.RFC3339), fc.Bus.LastReal)
	assert.Equal(t, 2, fc.Work.Inbox)
	assert.Equal(t, 3, fc.Work.Outbox)
	assert.Equal(t, "out.md", fc.Work.NewestOutbox)
	assert.Equal(t, now.Add(-time.Minute).UTC().Format(time.RFC3339), fc.Work.NewestAt)
}
