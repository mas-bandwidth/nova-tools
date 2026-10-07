package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalDue(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	since := t0
	g := Goal{Name: "friend-a"}
	require.True(t, g.Due(t0, since, nil), "a person never pushed to is due")
	g.Last = t0
	require.False(t, g.Due(t0.Add(RemindEvery-time.Second), since, nil), "five minutes of running time")
	require.True(t, g.Due(t0.Add(RemindEvery), since, nil), "five minutes of running time")
	// A run of the machine that began after the last push pushes at once.
	require.True(t, g.Due(t0.Add(time.Minute), t0.Add(time.Minute+time.Second), nil), "a start after the last push pushes at once")
	// Time STOPPED does not count.
	stopped := func(from, to time.Time) time.Duration {
		if to.After(t0.Add(time.Hour)) {
			return time.Hour - time.Minute
		}
		return 0
	}
	require.False(t, g.Due(t0.Add(time.Hour+2*time.Minute), since, stopped), "an hour stopped counted as running time")
	require.True(t, g.Due(t0.Add(time.Hour+5*time.Minute), since, stopped), "five minutes of running time after the stop")
	// A failed attempt counts as the attempt: it is retried each interval, not each tick.
	g.Tried = t0.Add(10 * time.Minute)
	require.False(t, g.Due(t0.Add(14*time.Minute), since, nil), "a failed attempt is retried after the interval")
	require.True(t, g.Due(t0.Add(15*time.Minute), since, nil), "a failed attempt is retried after the interval")
}

func TestGoalValidation(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"friend-a", "a", "reader_1.x"} {
		err := ValidGoalName(n)
		assert.NoError(t, err, "%q: %v", n, err)
	}
	for _, n := range []string{"", "-a", "A", "a b", "a/b", strings.Repeat("a", 65)} {
		assert.Error(t, ValidGoalName(n), "%q accepted", n)
	}
	require.NoError(t, ValidGoalText("ok", 2), "text validation")
	require.Error(t, ValidGoalText("abc", 2), "text validation")
	require.Error(t, ValidGoalText(" ", 9), "text validation")
	require.Error(t, ValidGoalText("a\x00", 9), "text validation")
	require.Error(t, ValidGoalText("\xff", 9), "text validation")
	k, p, err := ParseRoute("file:/x/y")
	require.NoError(t, err, "%v %v %v", k, p, err)
	require.Equal(t, RouteFile, k, "%v %v %v", k, p, err)
	require.Equal(t, "/x/y", p, "%v %v %v", k, p, err)
	for _, r := range []string{"", "file:", "x", "mail:a"} {
		_, _, err = ParseRoute(r)
		assert.Error(t, err, "route %q accepted", r)
	}
}
