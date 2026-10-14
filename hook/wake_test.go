package hook_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wakeAt(t *testing.T, r *rig, names []string, now time.Time) []hook.Wake {
	t.Helper()
	got, err := r.s.Wake(context.Background(), names, now)
	require.NoError(t, err)
	require.Len(t, got, len(names))
	return got
}

func wakeNamed(t *testing.T, got []hook.Wake, name string) hook.Wake {
	t.Helper()
	for _, w := range got {
		if w.Name == name {
			return w
		}
	}
	t.Fatalf("no wake row for %s", name)
	return hook.Wake{}
}

func commitMiss(t *testing.T, r *rig, w hook.Wake) {
	t.Helper()
	once, marks, ok := w.Miss()
	require.True(t, ok)
	require.NotEmpty(t, once)
	ctx := context.Background()
	for _, m := range marks {
		require.NoError(t, r.Client.HSet(ctx, hook.DrillKey(w.Name), m.Field, m.Value).Err())
	}
}

// TestAMissEmitsOneJudgmentAndASecondTheSameDayEmitsNone pins the drill's no
// (tla/WakeDrill.tla, MissEmitsOne and NoSecondSameDay): a hooked friend with
// no answer, whose epoch is older than a day, is a miss once; a later pass
// the same UTC day, a rehook, and a clock that is the same UTC day in another
// zone emit nothing more. The minute challenge, the miss count and the hook
// state are untouched, so a friend answering those challenges is not unhooked.
func TestAMissEmitsOneJudgmentAndASecondTheSameDayEmitsNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Coordinator)
	r.Hook(t, "bob", hook.Friend)
	r.Hook(t, "m1", hook.Member)
	names := []string{"ada", "bob", "m1"}
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("bob"), "armed", base.Add(-48*time.Hour).UnixMilli()).Err())
	require.NoError(t, r.Client.HDel(ctx, hook.RecordKey("bob"), "answered_at").Err())
	require.Equal(t, hook.BeatChallenged, r.beat(t, "srv", nonces())["bob"].Did, "the minute challenge still runs")
	chal, err := r.Client.HGetAll(ctx, hook.ChallengeKey("bob")).Result()
	require.NoError(t, err)
	rec, err := r.Client.HGetAll(ctx, hook.RecordKey("bob")).Result()
	require.NoError(t, err)

	got := wakeAt(t, r, names, base)
	bob := wakeNamed(t, got, "bob")
	assert.Equal(t, hook.WakeMissed, bob.Did)
	assert.Equal(t, hook.WakeLeft, wakeNamed(t, got, "ada").Did, "a coordinator is not a friend")
	assert.Equal(t, hook.WakeLeft, wakeNamed(t, got, "m1").Did, "a member has no session to drill")
	once, _, ok := bob.Miss()
	require.True(t, ok)
	n, err := r.Client.Exists(ctx, hook.DrillKey("bob")).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "a miss is not recorded until the judgment's send marks it")
	chalNow, err := r.Client.HGetAll(ctx, hook.ChallengeKey("bob")).Result()
	require.NoError(t, err)
	recNow, err := r.Client.HGetAll(ctx, hook.RecordKey("bob")).Result()
	require.NoError(t, err)
	assert.Equal(t, chal, chalNow, "the minute challenge is unchanged")
	assert.Equal(t, rec["misses"], recNow["misses"])
	assert.Equal(t, rec["state"], recNow["state"])
	assert.Equal(t, rec["beat_at"], recNow["beat_at"])
	assert.NotEqual(t, hook.StateUnhooked, r.state(t, "bob").State)

	again := wakeAt(t, r, names, base.Add(time.Hour))
	bob2 := wakeNamed(t, again, "bob")
	assert.Equal(t, hook.WakeMissed, bob2.Did, "an unsent miss is still a miss")
	once2, _, ok := bob2.Miss()
	require.True(t, ok)
	assert.Equal(t, once, once2, "the same day is the same once-key")

	commitMiss(t, r, bob)
	later := wakeNamed(t, wakeAt(t, r, names, base.Add(11*time.Hour)), "bob")
	assert.Equal(t, hook.WakeAlready, later.Did)
	assert.Equal(t, 1, later.Judged)
	_, _, ok = later.Miss()
	assert.False(t, ok, "an already-drilled day emits nothing")
	east := time.Date(2026, 10, 12, 8, 0, 0, 0, time.FixedZone("east", 14*3600)) // UTC 2026-10-11 18:00
	assert.Equal(t, hook.WakeAlready, wakeNamed(t, wakeAt(t, r, names, east), "bob").Did, "the day is UTC")

	r.Unhook(t, "bob")
	r.Hook(t, "bob", hook.Friend)
	assert.Equal(t, hook.WakeAlready, wakeNamed(t, wakeAt(t, r, names, base.Add(11*time.Hour)), "bob").Did, "a rehook the same day is not a second judgment")
	assert.NotEqual(t, hook.StateUnhooked, r.state(t, "bob").State)

	nextDay := base.Add(25 * time.Hour)
	require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("bob"), "armed", nextDay.Add(-48*time.Hour).UnixMilli()).Err())
	require.NoError(t, r.Client.HDel(ctx, hook.RecordKey("bob"), "answered_at").Err())
	miss := wakeNamed(t, wakeAt(t, r, names, nextDay), "bob")
	assert.Equal(t, hook.WakeMissed, miss.Did, "the next day is a new drill")
	nextOnce, _, ok := miss.Miss()
	require.True(t, ok)
	assert.NotEqual(t, once, nextOnce)
	commitMiss(t, r, miss)
	assert.Equal(t, hook.WakeAlready, wakeNamed(t, wakeAt(t, r, names, nextDay.Add(time.Hour)), "bob").Did)
	assert.NotEqual(t, hook.StateUnhooked, r.state(t, "bob").State)
}

// TestAnAnswerIsAPassAndEmitsNothing pins the drill's yes (tla/WakeDrill.tla,
// PassEmitsNone and AnsweringStaysHooked): a friend who answered the minute
// challenge, on an epoch older than a day, is a pass; going silent later the
// same day emits nothing and does not unhook; a young epoch with no answer is
// not due.
func TestAnAnswerIsAPassAndEmitsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "bob", hook.Friend)
	names := []string{"bob"}
	b := r.beat(t, "srv", nonces())["bob"]
	_, err := r.s.Answer(ctx, "bob", b.Nonce, "session")
	require.NoError(t, err)
	st := r.state(t, "bob")
	require.False(t, st.AnswerAt.IsZero())
	assert.Zero(t, st.Misses)
	require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("bob"), "armed", st.AnswerAt.Add(-48*time.Hour).UnixMilli()).Err())
	require.NoError(t, r.Client.Del(ctx, hook.DrillKey("bob")).Err())

	got := wakeNamed(t, wakeAt(t, r, names, st.AnswerAt), "bob")
	assert.Equal(t, hook.WakePassed, got.Did)
	assert.Zero(t, got.Judged)
	_, _, ok := got.Miss()
	assert.False(t, ok)
	n, err := r.Client.Exists(ctx, hook.DrillKey("bob")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "a pass records the day and emits nothing")
	assert.Zero(t, r.state(t, "bob").Misses)
	assert.NotEqual(t, hook.StateUnhooked, r.state(t, "bob").State)

	require.NoError(t, r.Client.HDel(ctx, hook.RecordKey("bob"), "answered_at").Err())
	same := wakeNamed(t, wakeAt(t, r, names, st.AnswerAt.Add(2*time.Hour)), "bob")
	assert.Equal(t, hook.WakeAlready, same.Did, "silence later the same day is not a second drill")
	assert.Zero(t, same.Judged)
	assert.NotEqual(t, hook.StateUnhooked, r.state(t, "bob").State)

	young := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("bob"), "armed", young.UnixMilli()).Err())
	require.NoError(t, r.Client.HDel(ctx, hook.RecordKey("bob"), "answered_at").Err())
	require.NoError(t, r.Client.Del(ctx, hook.DrillKey("bob")).Err())
	y := wakeNamed(t, wakeAt(t, r, names, young), "bob")
	assert.Equal(t, hook.WakePassed, y.Did, "an epoch younger than a day is not a miss")
	assert.Zero(t, y.Judged)
	assert.NotEqual(t, hook.StateUnhooked, r.state(t, "bob").State)
}
