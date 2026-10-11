package hostload

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTheBeatReportsOpenFileDescriptorsAndTheTopHolders: every measurement reads the
// machine's open file descriptors beside its load. Under the warn bound it is one count
// and no holder is listed (the holders' walk is the expensive read); over it the top
// holders are listed, most first, and kept for HoldersEvery before they are read again;
// over the alarm bound the reading says alarm; a holders' read that fails says why and
// the count stands. A load another meter gave is the beat's, and the descriptors are
// still measured. No wall clock is read: the times are the caller's.
func TestTheBeatReportsOpenFileDescriptorsAndTheTopHolders(t *testing.T) {
	t.Parallel()
	open, calls := 1000, 0
	var holdersErr error
	src := Source{NCPU: 4, Load1: func() (float64, bool) { return 1, true },
		OpenFiles: func() (int, int, error) { return open, 491520, nil },
		Holders: func() ([]Holder, error) {
			calls++
			if holdersErr != nil {
				return nil, holdersErr
			}
			return []Holder{
				{PID: 7, Command: "small", User: "build", Open: 3},
				{PID: 200, Command: "redis-server", User: "build", Open: 9000},
				{PID: 300, Command: "node", User: "build", Open: 40000},
			}, nil
		},
		FilesWarn: 50000, FilesAlarm: 150000}

	// under the warn bound: the count, the bounds, no holders read
	pct, how, st, ok := Measure(src, State{}, t0)
	require.True(t, ok)
	require.Equal(t, HowLoad1, how, "the load is measured as before")
	require.True(t, near(pct, 25), "load = %v", pct)
	require.NotNil(t, st.Files, "the descriptors are measured with the load")
	require.Equal(t, Files{At: t0, Open: 1000, Max: 491520, Warn: 50000, Alarm: 150000}, *st.Files)
	require.Equal(t, LevelOK, st.Files.Level())
	require.Zero(t, calls, "under the warn bound the holders are never read")

	// over the warn bound: warn, and the top holders, most first
	open = 60000
	_, _, st, _ = Measure(src, st, t0.Add(5*time.Second))
	require.Equal(t, LevelWarn, st.Files.Level())
	require.Equal(t, 1, calls)
	require.Equal(t, []Holder{
		{PID: 300, Command: "node", User: "build", Open: 40000},
		{PID: 200, Command: "redis-server", User: "build", Open: 9000},
		{PID: 7, Command: "small", User: "build", Open: 3},
	}, st.Files.Top)
	require.Equal(t, t0.Add(5*time.Second), st.Files.TopAt)
	require.Equal(t, "node pid=300 user=build fds=40000, redis-server pid=200 user=build fds=9000, small pid=7 user=build fds=3", st.Files.TopText())

	// within HoldersEvery the holders stand; the count is new
	open = 70000
	_, _, st, _ = Measure(src, st, t0.Add(5*time.Second+HoldersEvery-time.Second))
	require.Equal(t, 1, calls, "the holders are read at most once every %s", HoldersEvery)
	require.Equal(t, 70000, st.Files.Open)
	require.Len(t, st.Files.Top, 3, "the last holders stand between reads")

	// over the alarm bound, HoldersEvery on: alarm, the holders read again
	open = 160000
	_, _, st, _ = Measure(src, st, t0.Add(5*time.Second+HoldersEvery))
	require.Equal(t, LevelAlarm, st.Files.Level())
	require.Equal(t, 2, calls)

	// a holders' read that fails: the count stands, the reason is said, no holders
	holdersErr = errors.New("lsof timed out")
	_, _, st, _ = Measure(src, st, t0.Add(5*time.Second+2*HoldersEvery))
	require.Equal(t, LevelAlarm, st.Files.Level())
	require.Empty(t, st.Files.Top)
	require.Equal(t, "lsof timed out", st.Files.TopErr)
	require.Equal(t, "not listed: lsof timed out", st.Files.TopText())

	// back under the warn bound: no holders kept
	open, holdersErr = 100, nil
	_, _, st, _ = Measure(src, st, t0.Add(5*time.Second+3*HoldersEvery))
	require.Equal(t, LevelOK, st.Files.Level())
	require.Empty(t, st.Files.Top)
	require.Empty(t, st.Files.TopErr)

	// the reading rides in the measuring state the beat record keeps
	b, err := json.Marshal(st)
	require.NoError(t, err)
	var back State
	require.NoError(t, json.Unmarshal(b, &back))
	require.Equal(t, st.Files, back.Files)

	// a load given by another meter (fleet beat --load): the beat's, with the
	// descriptors measured beside it
	given := src
	given.Given = func() (float64, bool) { return 12.5, true }
	open = 60000
	pct, how, st, ok = Measure(given, State{}, t0)
	require.True(t, ok)
	require.Equal(t, HowGiven, how)
	require.Equal(t, 12.5, pct)
	require.Equal(t, LevelWarn, st.Files.Level())

	// default bounds: the workshop tool's, 50000 and 150000
	def := src
	def.FilesWarn, def.FilesAlarm = 0, 0
	_, _, st, _ = Measure(def, State{}, t0)
	require.Equal(t, FilesWarnDefault, st.Files.Warn)
	require.Equal(t, FilesAlarmDefault, st.Files.Alarm)

	// a machine that cannot count its descriptors has no reading, and its load stands
	none := src
	none.OpenFiles = func() (int, int, error) { return 0, 0, errors.New("no sysctl") }
	_, _, st, ok = Measure(none, State{Files: &Files{At: t0, Open: 160000}}, t0.Add(time.Second))
	require.True(t, ok)
	require.Nil(t, st.Files, "a reading that failed leaves none, never the last one")
	none.OpenFiles = nil
	_, _, st, _ = Measure(none, State{}, t0)
	require.Nil(t, st.Files)
}

// TestTopHoldersKeepsTheMost: the top holders are the FilesTop holding most, ties by pid.
func TestTopHoldersKeepsTheMost(t *testing.T) {
	t.Parallel()
	var hs []Holder
	for pid := 1; pid <= FilesTop+3; pid++ {
		hs = append(hs, Holder{PID: pid, Command: "c", Open: pid % 5})
	}
	top := TopHolders(hs, FilesTop)
	require.Len(t, top, FilesTop)
	require.Equal(t, Holder{PID: 4, Command: "c", Open: 4}, top[0], "most first, the lower pid of a tie")
	require.Equal(t, Holder{PID: 9, Command: "c", Open: 4}, top[1])
	require.Empty(t, TopHolders(nil, FilesTop))
}
