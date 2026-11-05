package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLocks is the kernel's lock table in memory: per path, an exclusive
// holder or a count of shared holders.
type fakeLocks struct {
	ex map[string]bool
	sh map[string]int
}

func newFake() *fakeLocks { return &fakeLocks{ex: map[string]bool{}, sh: map[string]int{}} }

func (f *fakeLocks) lock(path string, exclusive bool) (func(), bool, error) {
	name := filepath.Base(path)
	if f.ex[name] || (exclusive && f.sh[name] > 0) {
		return nil, false, nil
	}
	if exclusive {
		f.ex[name] = true
		return func() { f.ex[name] = false }, true, nil
	}
	f.sh[name]++
	return func() { f.sh[name]-- }, true, nil
}

// clock is fake time: each sleep advances it a second and runs onSleep.
type clock struct {
	t       time.Time
	sleeps  int
	onSleep func(n int)
}

func (c *clock) now() time.Time { return c.t }
func (c *clock) sleep() {
	c.sleeps++
	c.t = c.t.Add(time.Second)
	if c.onSleep != nil {
		c.onSleep(c.sleeps)
	}
}

func opts(slots int, exclusive bool, wait time.Duration) options {
	return options{slots: slots, exclusive: exclusive, wait: wait, dir: "/d", card: "-"}
}

func TestSharedTakesFirstFreeSlot(t *testing.T) {
	t.Parallel()
	f, c := newFake(), &clock{}
	a, err := acquire(f.lock, opts(3, false, 0), c.now, c.sleep)
	require.NoError(t, err)
	b, err := acquire(f.lock, opts(3, false, 0), c.now, c.sleep)
	require.NoError(t, err)
	assert.Equal(t, 1, a.slot)
	assert.Equal(t, 2, b.slot)
	assert.Equal(t, 2, f.sh["all"])
	assert.Equal(t, 0, f.sh["gate"], "a shared taker drops the gate once it holds")
	a.release()
	c2, err := acquire(f.lock, opts(3, false, 0), c.now, c.sleep)
	require.NoError(t, err)
	assert.Equal(t, 1, c2.slot, "a freed slot is taken again")
	assert.Equal(t, 0, c.sleeps)
}

func TestFullSlotsTimeOutAndHoldNothing(t *testing.T) {
	t.Parallel()
	f, c := newFake(), &clock{}
	for range 2 {
		_, err := acquire(f.lock, opts(2, false, 0), c.now, c.sleep)
		require.NoError(t, err)
	}
	_, err := acquire(f.lock, opts(2, false, 5*time.Second), c.now, c.sleep)
	assert.ErrorIs(t, err, errTimeout)
	assert.Equal(t, 5, c.sleeps)
	assert.Equal(t, 2, f.sh["all"])
	assert.Equal(t, 0, f.sh["gate"])
}

func TestWaiterRunsWhenAHolderEnds(t *testing.T) {
	t.Parallel()
	f, c := newFake(), &clock{}
	a, err := acquire(f.lock, opts(1, false, 0), c.now, c.sleep)
	require.NoError(t, err)
	c.onSleep = func(n int) {
		if n == 3 {
			a.release()
		}
	}
	b, err := acquire(f.lock, opts(1, false, 0), c.now, c.sleep)
	require.NoError(t, err)
	assert.Equal(t, 1, b.slot)
	assert.Equal(t, 3, c.sleeps)
}

func TestExclusiveWaitsForSharedAndBarsNewShared(t *testing.T) {
	t.Parallel()
	f, c := newFake(), &clock{}
	a, err := acquire(f.lock, opts(4, false, 0), c.now, c.sleep)
	require.NoError(t, err)
	var during error
	c.onSleep = func(n int) {
		if n == 1 {
			// the exclusive taker holds the gate now: a shared taker with free slots waits
			other := &clock{}
			_, during = acquire(f.lock, opts(4, false, time.Second), other.now, other.sleep)
		}
		if n == 2 {
			a.release()
		}
	}
	x, err := acquire(f.lock, opts(4, true, 0), c.now, c.sleep)
	require.NoError(t, err)
	assert.ErrorIs(t, during, errTimeout)
	assert.True(t, f.ex["all"])
	assert.True(t, f.ex["gate"])
	assert.Equal(t, "mode=exclusive", strings.Fields(x.describe())[0])
	c.onSleep = nil
	_, err = acquire(f.lock, opts(4, false, time.Second), c.now, c.sleep)
	assert.ErrorIs(t, err, errTimeout, "no shared taker while an exclusive one runs")
	x.release()
	_, err = acquire(f.lock, opts(4, false, 0), c.now, c.sleep)
	assert.NoError(t, err)
}

func TestExclusiveTimeoutGivesBackTheGate(t *testing.T) {
	t.Parallel()
	f, c := newFake(), &clock{}
	_, err := acquire(f.lock, opts(2, false, 0), c.now, c.sleep)
	require.NoError(t, err)
	_, err = acquire(f.lock, opts(2, true, 3*time.Second), c.now, c.sleep)
	assert.ErrorIs(t, err, errTimeout)
	assert.False(t, f.ex["gate"])
	_, err = acquire(f.lock, opts(2, false, 0), c.now, c.sleep)
	assert.NoError(t, err, "shared takers pass again")
}

// TestFakeIsAWitness is the reversed witness for the tests above: a lock
// table that grants everything lets N+1 holders in, and the timeout test
// would see no timeout.
func TestFakeIsAWitness(t *testing.T) {
	t.Parallel()
	open := func(string, bool) (func(), bool, error) { return func() {}, true, nil }
	c := &clock{}
	for range 3 {
		_, err := acquire(open, opts(2, false, time.Second), c.now, c.sleep)
		require.NoError(t, err)
	}
}

func testWorld(f *fakeLocks, dir string, stdin string, code int, ran *[]string) (world, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	c := &clock{}
	return world{
		env:   func(k string) string { return map[string]string{"NOVA_SLOT_DIR": dir}[k] },
		lock:  f.lock,
		now:   c.now,
		sleep: c.sleep,
		exec: func(argv []string) int {
			*ran = argv
			return code
		},
		stdin:  strings.NewReader(stdin),
		stdout: &out,
		stderr: &errb,
	}, &out, &errb
}

func TestRunPassesTheStatusAndFrees(t *testing.T) {
	t.Parallel()
	f, dir := newFake(), t.TempDir()
	var ran []string
	w, _, errb := testWorld(f, dir, "", 7, &ran)
	code := run([]string{"--slots", "2", "--card", "c1", "--", "make", "-j4"}, w)
	assert.Equal(t, 7, code)
	assert.Equal(t, []string{"make", "-j4"}, ran)
	assert.Contains(t, errb.String(), "HELD mode=shared slot=1/2 card=c1")
	assert.Equal(t, 0, f.sh["all"])
	assert.False(t, f.ex["slot1"])
	assert.NoFileExists(t, filepath.Join(dir, "slot1.holder"))
}

func TestHoldPrintsHeldAndEndsOnStdinClose(t *testing.T) {
	t.Parallel()
	f := newFake()
	var ran []string
	w, out, _ := testWorld(f, t.TempDir(), "anything", 0, &ran)
	// winlock's form, as the sprint member's --lane-lock and the WSL wrapper call it
	code := run([]string{"hold", "--card", "c2", "--slots", "8", "--dir", `C:\rowan-jobs`, "--exclusive"}, w)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasPrefix(out.String(), "HELD mode=exclusive slot=all/8 card=c2"))
	assert.Nil(t, ran)
	assert.False(t, f.ex["all"])
	assert.False(t, f.ex["gate"])
}

func TestStatusNamesHolders(t *testing.T) {
	t.Parallel()
	f, dir := newFake(), t.TempDir()
	w, out, _ := testWorld(f, dir, "", 0, new([]string))
	w.exec = func([]string) int {
		// status, run while the command holds its slot
		w2, out2, _ := testWorld(f, dir, "", 0, new([]string))
		require.Equal(t, 0, run([]string{"status"}, w2))
		out.WriteString(out2.String())
		return 0
	}
	// status lists the lock files on disk; the fake table holds the locks
	for _, n := range []string{"slot1", "slot2"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), nil, 0o644))
	}
	require.Equal(t, 0, run([]string{"--slots", "2", "--card", "c3", "--", "sleep", "9"}, w))
	assert.Contains(t, out.String(), "slot1 held pid=")
	assert.Contains(t, out.String(), "card=c3")
	assert.Contains(t, out.String(), "cmd=sleep 9")
	assert.Contains(t, out.String(), "slot2 free\n")
	assert.Contains(t, out.String(), "all free\n")
}

func TestParseRefuses(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{},
		{"--slots", "0", "--", "x"},
		{"--wait", "-1s", "--", "x"},
		{"--bogus", "--", "x"},
		{"status", "extra"},
		{"hold", "--slots"},
	} {
		var ran []string
		w, _, _ := testWorld(newFake(), t.TempDir(), "", 0, &ran)
		assert.Equal(t, 2, run(args, w), "%q", args)
		assert.Nil(t, ran)
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	o, err := parse("run", []string{"--", "x"}, func(string) string { return "" }, &bytes.Buffer{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, o.slots, 1)
	assert.Equal(t, time.Duration(0), o.wait)
	assert.NotEmpty(t, o.dir)
	var out bytes.Buffer
	w, _, _ := testWorld(newFake(), t.TempDir(), "", 0, new([]string))
	w.stdout = &out
	assert.Equal(t, 0, run([]string{"help"}, w))
	assert.Contains(t, out.String(), "nova-slot status")
}
