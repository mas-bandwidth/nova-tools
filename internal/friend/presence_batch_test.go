package friend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A batch friend's presence is her engine's beat, never a session's answer
// (docs/SPEC-FRIEND.md, "Presence per mode"; sprint.Beat.EnginePresence). Found
// dogfooding v1.2 on 2026-10-07: a friend with no session, her runner beating her
// lanes every five seconds, read down "no session evidence" for twelve hours and
// was dealt nothing. The rule is the sprint server's (internal/sprint/presence.go)
// and is read here with a fake clock and a fake row: the beat carries the row's
// mode as it stood when she beat, so a mode flip takes effect on her next beat.

var batchT0 = time.Date(2030, 3, 4, 5, 6, 7, 0, time.UTC)

// engineBeat is a runner's beat for a friend whose row's mode is mode, at at: her
// lanes' report (friend beat --width --working --running).
func engineBeat(mode string, at time.Time) sprint.Beat {
	width, working := 8, 3
	return sprint.Beat{At: at, RowMode: mode, Friend: &sprint.FriendReport{Width: &width, Working: &working, Running: []string{"card-a", "card-b", "card-c"}}}
}

// daemonBeat is a session friend's daemon's beat: its own facts, no lanes.
func daemonBeat(mode string, at time.Time) sprint.Beat {
	return sprint.Beat{At: at, RowMode: mode, Friend: &sprint.FriendReport{Active: at, Build: "b1", Started: at.Add(-time.Hour), Present: at.Add(-time.Hour)}}
}

func TestABatchFriendIsUpOnHerEnginesFreshBeat(t *testing.T) {
	t.Parallel()
	now := batchT0
	for _, age := range []time.Duration{0, 5 * time.Second, time.Minute, sprint.FriendEngineSilent - time.Second} {
		f := sprint.FriendPresence{Beat: engineBeat(config.FriendModeBatch, now.Add(-age))}
		word, why := sprint.FriendEvidence(f, now)
		assert.Equal(t, sprint.Up, word, age.String())
		assert.Equal(t, "engine beat "+age.String()+" ago", why)
		assert.Empty(t, sprint.FriendDownWhy(f, now), "nothing refuses a take for her")
	}
	// a beat record from before the field names no mode: it reads as the row's default, batch
	word, why := sprint.FriendEvidence(sprint.FriendPresence{Beat: engineBeat("", now.Add(-3*time.Second))}, now)
	assert.Equal(t, sprint.Up, word)
	assert.Equal(t, "engine beat 3s ago", why)
	// the engine's report is what makes a beat an engine's: any one of the lanes' counts
	width, queue := 4, 0
	for name, rep := range map[string]*sprint.FriendReport{
		"width":   {Width: &width},
		"working": {Working: &queue},
		"queue":   {Queue: &queue},
		"running": {Running: []string{"card-a"}},
	} {
		b := sprint.Beat{At: now, RowMode: config.FriendModeBatch, Friend: rep}
		assert.True(t, b.FromEngine(), name)
		assert.True(t, b.EnginePresence(), name)
	}
}

func TestABatchFriendIsDownWhenHerEngineIsSilent(t *testing.T) {
	t.Parallel()
	now := batchT0
	for age, want := range map[time.Duration]string{
		sprint.FriendEngineSilent:     "engine silent for 20m0s",
		sprint.FriendEngineSilent + 1: "engine silent for 20m0s",
		12 * time.Hour:                "engine silent for 12h0m0s",
	} {
		f := sprint.FriendPresence{Beat: engineBeat(config.FriendModeBatch, now.Add(-age))}
		word, why := sprint.FriendEvidence(f, now)
		assert.Equal(t, sprint.Down, word, age.String())
		assert.Equal(t, want, why)
		assert.Equal(t, want, sprint.FriendDownWhy(f, now), "a take refused for her names her silent engine")
	}
	// a pong, a proof or a finish is no engine: her presence is her engine's beat alone
	f := sprint.FriendPresence{Beat: engineBeat(config.FriendModeBatch, now.Add(-time.Hour)), Generation: 2,
		Health: sprint.FriendHealth{State: sprint.Up, Seen: now, Generation: 2}, Finished: now}
	f.Beat.Proof = now
	word, why := sprint.FriendEvidence(f, now)
	assert.Equal(t, sprint.Down, word)
	assert.Equal(t, "engine silent for 1h0m0s", why)
	// a beat dated after now is no evidence
	word, why = sprint.FriendEvidence(sprint.FriendPresence{Beat: engineBeat(config.FriendModeBatch, now.Add(time.Second))}, now)
	assert.Equal(t, sprint.Down, word)
	assert.Equal(t, "engine beat in the future", why)
}

func TestABatchFriendIsNeverAskedASessionAnswer(t *testing.T) {
	t.Parallel()
	now := batchT0
	// up with no pong, no proof and no finish, under any seat generation
	f := sprint.FriendPresence{Beat: engineBeat(config.FriendModeBatch, now.Add(-4*time.Second)), Generation: 7}
	word, why := sprint.FriendEvidence(f, now)
	assert.Equal(t, sprint.Up, word)
	assert.Equal(t, "engine beat 4s ago", why)
	assert.NotContains(t, why, "session")
	// down, the words name her engine, never a session answer she was not asked for
	f.Beat.At = now.Add(-sprint.FriendEngineSilent)
	word, why = sprint.FriendEvidence(f, now)
	assert.Equal(t, sprint.Down, word)
	assert.Equal(t, "engine silent for 20m0s", why)
	assert.NotContains(t, why, "session")
	assert.NotContains(t, why, "not evidence")
	// the bound is the daemon's silent-stop bound: an engine that prints nothing for that
	// long is stopped, so one that beats nothing for that long is gone
	assert.Equal(t, DefaultSilentStop, sprint.FriendEngineSilent)
}

func TestASessionFriendsRuleIsUnchanged(t *testing.T) {
	t.Parallel()
	now := batchT0
	// a session friend in batch mode: her daemon's beat carries no lanes and is no evidence
	bare := daemonBeat(config.FriendModeBatch, now.Add(-time.Second))
	assert.False(t, bare.FromEngine())
	assert.False(t, bare.EnginePresence())
	word, why := sprint.FriendEvidence(sprint.FriendPresence{Beat: bare, Generation: 2}, now)
	assert.Equal(t, sprint.Down, word)
	assert.Contains(t, why, "no session evidence")
	assert.Contains(t, why, "her beat 1s ago is not evidence")
	// her session's answers make her up as before: a wake ping answered, a proof on her
	// beat, a card finished
	pong := sprint.FriendHealth{State: sprint.Up, Seen: now, Generation: 2}
	word, why = sprint.FriendEvidence(sprint.FriendPresence{Beat: bare, Health: pong, Generation: 2}, now)
	assert.Equal(t, sprint.Up, word)
	assert.Equal(t, "session pong 0s ago", why)
	proved := bare
	proved.Proof = now.Add(-3 * time.Minute)
	word, why = sprint.FriendEvidence(sprint.FriendPresence{Beat: proved, Generation: 2}, now)
	assert.Equal(t, sprint.Up, word)
	assert.Equal(t, "session proof 3m0s ago", why)
	word, why = sprint.FriendEvidence(sprint.FriendPresence{Beat: bare, Finished: now.Add(-time.Minute), Generation: 2}, now)
	assert.Equal(t, sprint.Up, word)
	assert.Equal(t, "finish 1m0s ago", why)
	// a one-shot row's lanes prove her by the session check they answer: a lanes' report
	// on a one-shot beat is read by the session's rule
	lanes := engineBeat(config.FriendModeOneShot, now.Add(-time.Second))
	assert.True(t, lanes.FromEngine())
	assert.False(t, lanes.EnginePresence())
	word, why = sprint.FriendEvidence(sprint.FriendPresence{Beat: lanes, Generation: 2}, now)
	assert.Equal(t, sprint.Down, word)
	assert.Contains(t, why, "no session evidence")
	// held comes first, and a beat that says down is down first, whatever it carries
	assert.Equal(t, sprint.Held, sprint.FriendStatus(sprint.FriendPresence{Held: true, Beat: engineBeat(config.FriendModeBatch, now)}, now))
	down := engineBeat(config.FriendModeBatch, now)
	down.Friend.Until, down.Friend.Reason = now.Add(time.Hour), "out of credits"
	word, why = sprint.FriendEvidence(sprint.FriendPresence{Beat: down}, now)
	assert.Equal(t, sprint.Down, word)
	assert.Contains(t, why, "her beat says down until")
	assert.Contains(t, why, "out of credits")
}

// modeRig is the twin store with an injected clock and one friend, zhi, whose row's
// mode the test flips with friend sync.
type modeRig struct {
	t   *testing.T
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

func newModeRig(t *testing.T) *modeRig {
	t.Helper()
	m := store.NewMem()
	r := &modeRig{t: t, ctx: context.Background(), now: batchT0}
	r.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { return "1" }, Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	return r
}

// row sets zhi's row to mode, as friend sync does.
func (r *modeRig) row(mode string) {
	r.t.Helper()
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "zhi", Width: 8, Mode: mode}})
	require.NoError(r.t, err)
}

// tick moves the clock by d.
func (r *modeRig) tick(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
}

// beat is her runner's beat: her lanes' report.
func (r *modeRig) beat() {
	r.t.Helper()
	width, working := 8, 2
	_, err := r.st.FriendBeatReport(r.ctx, "zhi", sprint.FriendReport{Width: &width, Working: &working, Running: []string{"card-a", "card-b"}}, nil)
	require.NoError(r.t, err)
}

// zhi is her row of the friends table now.
func (r *modeRig) zhi() store.FriendRow {
	r.t.Helper()
	rows, err := r.st.FriendRows(r.ctx, r.st.Now())
	require.NoError(r.t, err)
	require.Len(r.t, rows, 1)
	return rows[0]
}

func TestAModeFlipTakesEffectOnTheNextBeat(t *testing.T) {
	t.Parallel()
	now := batchT0
	// the rule reads the mode the beat was taken under
	word, _ := sprint.FriendEvidence(sprint.FriendPresence{Beat: engineBeat(config.FriendModeBatch, now)}, now)
	assert.Equal(t, sprint.Up, word)
	word, why := sprint.FriendEvidence(sprint.FriendPresence{Beat: engineBeat(config.FriendModeOneShot, now)}, now)
	assert.Equal(t, sprint.Down, word)
	assert.Contains(t, why, "no session evidence")
	word, _ = sprint.FriendEvidence(sprint.FriendPresence{Beat: engineBeat(config.FriendModeBatch, now)}, now)
	assert.Equal(t, sprint.Up, word)
	// through the store: the roster's mode is stamped on each beat record, and the friends
	// table follows it on her next beat
	r := newModeRig(t)
	r.row(config.FriendModeBatch)
	r.beat()
	row := r.zhi()
	assert.Equal(t, sprint.Up, row.Status)
	assert.Equal(t, "engine beat 0s ago", row.Evidence)
	assert.Equal(t, config.FriendModeBatch, row.Mode)
	b, err := r.st.FriendBeatOf(r.ctx, "zhi")
	require.NoError(t, err)
	assert.Equal(t, config.FriendModeBatch, b.RowMode, "the beat record carries the row's mode")
	// the row flips to one-shot: the beat on record was taken in batch mode, so her status
	// moves on her next beat, not before
	r.row(config.FriendModeOneShot)
	assert.Equal(t, sprint.Up, r.zhi().Status)
	r.tick(time.Second)
	r.beat()
	row = r.zhi()
	assert.Equal(t, sprint.Down, row.Status)
	assert.Contains(t, row.Evidence, "no session evidence")
	assert.Equal(t, config.FriendModeOneShot, row.Mode)
	// and back, with no reinstall and no restart
	r.row(config.FriendModeBatch)
	r.tick(time.Second)
	r.beat()
	assert.Equal(t, sprint.Up, r.zhi().Status)
	assert.Equal(t, "engine beat 0s ago", r.zhi().Evidence)
	// her engine stops: down at the bound, naming it
	r.tick(sprint.FriendEngineSilent - time.Second)
	assert.Equal(t, sprint.Up, r.zhi().Status)
	r.tick(time.Second)
	row = r.zhi()
	assert.Equal(t, sprint.Down, row.Status)
	assert.Equal(t, "engine silent for 20m0s", row.Evidence)
	// a roster entry from before the mode field is batch, the row's default
	_, _, _, err = r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "zhi", Width: 8}})
	require.NoError(t, err)
	r.tick(time.Second)
	r.beat()
	assert.Equal(t, sprint.Up, r.zhi().Status)
	b, err = r.st.FriendBeatOf(r.ctx, "zhi")
	require.NoError(t, err)
	assert.Equal(t, config.FriendModeBatch, b.RowMode)
}
