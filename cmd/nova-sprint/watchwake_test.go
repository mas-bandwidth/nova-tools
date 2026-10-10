package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The wake verb's model is tla/CoordinatorWake.tla: these tests run runWake on a
// fake world, its clock and its state file, and open no socket and never sleep.

// fakeWake is the world a wake looks at: the sprint's numbers as one look reads
// them, and the coordinator's bus stream.
type fakeWake struct {
	world wakeLook
	bus   []wakeMsg
	seq   int
}

func (f *fakeWake) look(_ context.Context, after string) (wakeLook, error) {
	l := f.world
	l.Coordinator = "coord"
	for _, m := range f.bus {
		if m.ID > after {
			l.Msgs = append(l.Msgs, m)
		}
	}
	return l, nil
}

// send puts a message on the stream, its id after every one before it.
func (f *fakeWake) send(at time.Time, from, subject, kind string) {
	f.seq++
	f.bus = append(f.bus, wakeMsg{ID: fmt.Sprintf("%d-%d", at.UnixMilli(), f.seq), From: from, Subject: subject, Kind: kind})
}

// wakeRig is a fake world, a clock that moves only when the verb sleeps, and one state file.
type wakeRig struct {
	t    *testing.T
	now  time.Time
	f    *fakeWake
	path string
	cfg  wakeCfg
}

func newWakeRig(t *testing.T) *wakeRig {
	t.Helper()
	return &wakeRig{t: t, now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), f: &fakeWake{}, path: filepath.Join(t.TempDir(), "wake.json"), cfg: defaultWakeCfg()}
}

// run is one run of the verb: hook is called after each sleep with the number of sleeps
// of this run; a run that sleeps a day without a wake fails the test.
func (r *wakeRig) run(hook func(n int)) wakeLine {
	r.t.Helper()
	start, n := r.now, 0
	sleep := func(d time.Duration) {
		r.now = r.now.Add(d)
		n++
		if hook != nil {
			hook(n)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := r.cfg
	cfg.sleep = func(d time.Duration) {
		sleep(d)
		if r.now.Sub(start) > 24*time.Hour {
			cancel()
		}
	}
	w, err := runWake(ctx, cfg, r.f, r.path, func() time.Time { return r.now })
	require.NoError(r.t, err, "a run that never woke in a day")
	return w
}

// elapsed is how long the run took on the clock.
func (r *wakeRig) elapsed(since time.Time) time.Duration { return r.now.Sub(since) }

func TestWatchWakeFiresOncePerEventAndNeverLapses(t *testing.T) {
	t.Parallel()

	t.Run("bus: what is not a message to answer never wakes, one that is wakes once", func(t *testing.T) {
		t.Parallel()
		r := newWakeRig(t)
		r.f.send(r.now.Add(-time.Hour), "friend", "before the first run", "request") // history is not new
		w := r.run(func(n int) {
			switch n {
			case 1:
				r.f.send(r.now, "friend", "PING are you there", "")
				r.f.send(r.now, "friend", "card s1-4 dealt: go", "")
				r.f.send(r.now, "friend", "RESULT: s1-4 done", "")
				r.f.send(r.now, "friend", "chatter", "status")
				r.f.send(r.now, "friend", "got it", "ack")
				r.f.send(r.now, "coord", "my own note", "request")
			case 2:
				r.f.send(r.now, "friend", "need a decision on s1-9", "request")
			}
		})
		assert.Equal(t, "bus", w.Kind)
		assert.Contains(t, w.Evidence, "need a decision on s1-9")
		assert.NotContains(t, w.Evidence, "PING")
		assert.Contains(t, w.String(), "WAKE bus 2026-10-04T")

		// a blocker that arrives between two runs is woken by the second, at once
		r.f.send(r.now, "friend", "stuck on the base", "blocker")
		at := r.now
		w = r.run(nil)
		assert.Equal(t, "bus", w.Kind)
		assert.Contains(t, w.Evidence, "stuck on the base")
		assert.Zero(t, r.elapsed(at), "it was already there: no waiting")

		// and neither fires again: the next wake is the ten-minute check
		at = r.now
		w = r.run(nil)
		assert.Equal(t, "check", w.Kind)
		assert.GreaterOrEqual(t, r.elapsed(at), 10*time.Minute)

		// a subject filter stands in for a kind a message does not carry: a request-like subject wakes
		r.f.send(r.now, "friend", "please look at s2-3", "")
		w = r.run(nil)
		assert.Equal(t, "bus", w.Kind)
	})

	t.Run("judgment: a new one wakes, at most once in twenty minutes, and none is lost", func(t *testing.T) {
		t.Parallel()
		r := newWakeRig(t)
		r.cfg.check = 24 * time.Hour
		r.f.world.Judgments = []string{"old-1"}
		w := r.run(func(n int) {
			if n == 2 {
				r.f.world.Judgments = []string{"old-1", "j-2"}
			}
		})
		assert.Equal(t, "judgment", w.Kind)
		assert.Contains(t, w.Evidence, "j-2")
		assert.NotContains(t, w.Evidence, "old-1", "a judgment held from before the run is not new")
		first := r.now

		r.f.world.Judgments = []string{"old-1", "j-2", "j-3"}
		w = r.run(nil)
		assert.Equal(t, "judgment", w.Kind)
		assert.Contains(t, w.Evidence, "j-3")
		assert.NotContains(t, w.Evidence, "j-2", "j-2 was woken already")
		assert.GreaterOrEqual(t, r.elapsed(first), 20*time.Minute, "one wake in twenty minutes")
	})

	t.Run("stop: one the coordinator did not ask wakes once per stop; its own never does", func(t *testing.T) {
		t.Parallel()
		r := newWakeRig(t)
		r.f.world.Stopped, r.f.world.StopKey, r.f.world.StopLine = true, "since-1", "machine: STOPPED"
		w := r.run(nil)
		assert.Equal(t, "stop", w.Kind)
		assert.Contains(t, w.Evidence, "machine: STOPPED")
		assert.Equal(t, "check", r.run(nil).Kind, "the same stop does not wake twice")

		r.f.world.Stopped = false
		r.f.world.Stopped, r.f.world.StopKey = true, "since-2"
		assert.Equal(t, "stop", r.run(nil).Kind, "a later stop is a new event")

		r.f.world.StopKey, r.f.world.StopAsked = "since-3", true
		assert.Equal(t, "check", r.run(nil).Kind, "a stop the coordinator asked wakes nobody")
	})

	t.Run("friend: down twice in a row wakes once, one held on purpose never", func(t *testing.T) {
		t.Parallel()
		r := newWakeRig(t)
		r.f.world.Down = []string{"x"} // the look names only friends down by themselves; held ones are left out
		at := r.now
		w := r.run(nil)
		assert.Equal(t, "friend", w.Kind)
		assert.Contains(t, w.Evidence, "x")
		assert.Equal(t, r.cfg.every, r.elapsed(at), "the second look in a row")
		assert.Equal(t, "check", r.run(nil).Kind, "x stays down: the wake is not repeated")

		r.f.world.Down = []string{"x", "y"}
		w = r.run(nil)
		assert.Equal(t, "friend", w.Kind)
		assert.Contains(t, w.Evidence, "y")
		assert.NotContains(t, w.Evidence, "x")

		r.f.world.Down = nil
		r.run(nil) // up again, a check
		r.f.world.Down = []string{"x"}
		assert.Equal(t, "friend", r.run(nil).Kind, "down again is a new episode")
	})

	t.Run("merge: a backlog over the bound, or no land pass for a quarter hour, wakes at most every ten minutes", func(t *testing.T) {
		t.Parallel()
		r := newWakeRig(t)
		r.cfg.check = 24 * time.Hour
		r.f.world.Merging = 31
		w := r.run(nil)
		assert.Equal(t, "merge", w.Kind)
		assert.Contains(t, w.Evidence, "merging 31")
		first := r.now
		w = r.run(nil)
		assert.Equal(t, "merge", w.Kind)
		assert.GreaterOrEqual(t, r.elapsed(first), 10*time.Minute)

		r2 := newWakeRig(t)
		r2.cfg.check = 24 * time.Hour
		r2.f.world.Merging, r2.f.world.LastLand = 5, r2.now.Add(-16*time.Minute)
		w = r2.run(nil)
		assert.Equal(t, "merge", w.Kind)
		assert.Contains(t, w.Evidence, "last land pass")

		r3 := newWakeRig(t)
		r3.f.world.Merging, r3.f.world.LastLand, r3.f.world.StopAsked, r3.f.world.Stopped = 40, r3.now.Add(-time.Hour), true, true
		assert.Equal(t, "check", r3.run(nil).Kind, "a stop the coordinator asked silences the merge alarm")
	})

	t.Run("backlog: each reason names itself, one wake in half an hour", func(t *testing.T) {
		t.Parallel()
		for name, c := range map[string]struct {
			world func(*wakeLook)
			want  string
		}{
			"fleet":   {func(l *wakeLook) { l.Working, l.Width, l.Ready = 3, 10, 5 }, "fleet 3/10"},
			"review":  {func(l *wakeLook) { l.Review = 41 }, "review 41"},
			"starved": {func(l *wakeLook) { l.Ready, l.Waiting = 0, 3 }, "ready 0 with 3 waiting"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				r := newWakeRig(t)
				r.cfg.check = 24 * time.Hour
				c.world(&r.f.world)
				r.f.world.LastLand = r.now // a land pass just now: no merge wake
				w := r.run(nil)
				assert.Equal(t, "backlog", w.Kind)
				assert.Contains(t, w.Evidence, c.want)
				first := r.now
				w = r.run(nil)
				assert.Equal(t, "backlog", w.Kind)
				assert.GreaterOrEqual(t, r.elapsed(first), 30*time.Minute)
			})
		}
	})

	t.Run("lapse: events that arrive while no run waits are each woken by a later run", func(t *testing.T) {
		t.Parallel()
		r := newWakeRig(t)
		r.run(nil) // seeds the cursors: a check
		r.f.send(r.now, "friend", "question one", "request")
		r.f.world.Judgments = []string{"j-1"}
		r.f.world.Stopped, r.f.world.StopKey, r.f.world.StopLine = true, "since-9", "machine: STOPPED"
		var kinds []string
		for range 3 {
			at := r.now
			w := r.run(nil)
			kinds = append(kinds, w.Kind)
			assert.Zero(t, r.elapsed(at), "everything was already there: no waiting")
		}
		assert.Equal(t, []string{"bus", "judgment", "stop"}, kinds)
		assert.Equal(t, "check", r.run(nil).Kind, "and nothing is woken twice")
	})

	t.Run("state: the file keeps the cursors and is read back", func(t *testing.T) {
		t.Parallel()
		r := newWakeRig(t)
		r.f.world.Judgments = []string{"j-1"}
		r.run(nil)
		s, err := readWakeState(r.path)
		require.NoError(t, err)
		assert.True(t, s.Seeded)
		assert.Equal(t, []string{"j-1"}, s.Judgments)
		assert.NotEmpty(t, s.Bus)
		assert.False(t, s.Last["check"].IsZero())
		s2, err := readWakeState(filepath.Join(t.TempDir(), "none.json"))
		require.NoError(t, err)
		assert.False(t, s2.Seeded, "no file is a first run")
		assert.Error(t, writeWakeState(filepath.Join(t.TempDir(), "no", "dir", "x.json"), s))
	})
}

// TestWatchWakeWatchesTheWakeFile: the retired script recorded the wake file's
// line count at start and woke on a line appended to it. --wake-file holds both
// halves: the first run starts at the file's end, a line appended after wakes
// with up to three new lines shown, the state keeps the count, and the same
// lines never wake twice.
func TestWatchWakeWatchesTheWakeFile(t *testing.T) {
	t.Parallel()
	r := newWakeRig(t)
	r.f.world.WakeFile = []string{"before the run"} // the first run records the count: not new
	w := r.run(func(n int) {
		if n == 1 {
			r.f.world.WakeFile = []string{"before the run", "one", "two", "three", "four"}
		}
	})
	assert.Equal(t, "file", w.Kind)
	assert.Contains(t, w.Evidence, "one")
	assert.Contains(t, w.Evidence, "three")
	assert.NotContains(t, w.Evidence, "four", "up to three new lines")
	assert.NotContains(t, w.Evidence, "before the run", "the first run starts at the file's end")
	assert.Contains(t, w.String(), "WAKE file ")

	s, err := readWakeState(r.path)
	require.NoError(t, err)
	assert.Equal(t, 5, s.Wake, "the state records the lines consumed")

	r.f.world.WakeFile = append(r.f.world.WakeFile, "five")
	assert.Equal(t, "file", r.run(nil).Kind, "a later append wakes")
	assert.Equal(t, "check", r.run(nil).Kind, "the same lines do not wake twice")
}

// TestWatchWakeSeededOldState: a watch --wake state written before --wake-file
// was enabled is seeded (true) with no wake field, read as zero. Enabling the
// flag must set the baseline at the file's end like a first run, not treat the
// lines already there as new and wake at once.
func TestWatchWakeSeededOldState(t *testing.T) {
	t.Parallel()
	r := newWakeRig(t)
	require.NoError(t, os.WriteFile(r.path, []byte(`{"seeded":true,"bus":"1728540000000-0","judgments":[]}`), 0o600))

	r.f.world.WakeFile = []string{"existing 1", "existing 2", "existing 3"}
	assert.Equal(t, "check", r.run(nil).Kind, "a seeded state starts at the file's end")

	s, err := readWakeState(r.path)
	require.NoError(t, err)
	assert.True(t, s.WakeInit, "the wake baseline is set")
	assert.Equal(t, 3, s.Wake, "the baseline counts the lines already there")

	r.f.world.WakeFile = append(r.f.world.WakeFile, "appended 4")
	w := r.run(nil)
	assert.Equal(t, "file", w.Kind, "a line appended after the baseline wakes")
	assert.Contains(t, w.Evidence, "appended 4")
	assert.NotContains(t, w.Evidence, "existing")
}

func TestWatchRefusesWithoutWake(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	code, _, errs := ta.do("watch")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--wake")
	assert.True(t, strings.Contains(errs, "run: nova-sprint watch"), errs)
}

// storeWake reads the same numbers the view does, and a stop is asked only when the
// coordinator's stop verb recorded it.
func TestStoreWakeReadsTheSprintAndTellsAnAskedStop(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 3")
	st, err := ta.a.storeCtx(context.Background(), common{verb: "watch", redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	src := &storeWake{st: st, a: ta.a}

	l, err := src.look(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "coordinator", l.Coordinator)
	assert.Equal(t, 3, l.Ready+l.Waiting, "the three cards are counted as the view counts them")

	ta.ok("start")
	ta.ok("stop --reason maintenance --until 1h")
	l, err = src.look(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, l.Stopped)
	assert.True(t, l.StopAsked, "the stop verb recorded it: %q", l.StopLine)
	assert.NotEmpty(t, l.StopKey)
}
