package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	if f.ex[path] || (exclusive && f.sh[path] > 0) {
		return nil, false, nil
	}
	if exclusive {
		f.ex[path] = true
		return func() { f.ex[path] = false }, true, nil
	}
	f.sh[path]++
	return func() { f.sh[path]-- }, true, nil
}

func (f *fakeLocks) idle() bool {
	for _, v := range f.ex {
		if v {
			return false
		}
	}
	for _, v := range f.sh {
		if v > 0 {
			return false
		}
	}
	return true
}

// clock is fake time: each sleep advances it by its duration and runs onSleep.
type clock struct {
	t       time.Time
	slept   []time.Duration
	onSleep func(n int)
}

var epoch = time.Date(2026, 11, 5, 22, 3, 32, 0, time.UTC)

func (c *clock) now() time.Time { return c.t }
func (c *clock) sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
	if c.onSleep != nil {
		c.onSleep(len(c.slept))
	}
}

// harness is a world of fakes over one real temporary reservation directory.
type harness struct {
	f        *fakeLocks
	c        *clock
	dead     map[int]bool
	dir      string
	ran      []string
	code     int
	out, err bytes.Buffer
	w        world
}

func newHarness(t *testing.T) *harness {
	h := &harness{f: newFake(), c: &clock{t: epoch}, dead: map[int]bool{}, dir: t.TempDir()}
	h.w = world{
		env:     func(k string) string { return map[string]string{"NOVA_RESERVE_DIR": h.dir, "PATH": "/usr/bin:/bin"}[k] },
		lock:    h.f.lock,
		now:     h.c.now,
		sleep:   h.c.sleep,
		resolve: func(name string) (string, error) { return "/bin/" + name, nil },
		exec: func(_ string, argv []string) int {
			h.ran = argv
			return h.code
		},
		running: func(pid int) bool { return !h.dead[pid] },
		pid:     42,
		cpus:    8,
		stdout:  &h.out,
		stderr:  &h.err,
	}
	return h
}

// sibling is a second caller on the same machine: same locks, dir and clock.
func (h *harness) sibling(pid int) *harness {
	s := &harness{f: h.f, c: h.c, dead: h.dead, dir: h.dir}
	s.w = h.w
	s.w.pid, s.w.stdout, s.w.stderr = pid, &s.out, &s.err
	s.w.exec = func(_ string, argv []string) int { s.ran = argv; return s.code }
	return s
}

func (h *harness) opts(slots int, exclusive bool, wait time.Duration) options {
	return options{verb: "run", slots: slots, exclusive: exclusive, wait: wait, dir: h.dir}
}

// take acquires as do does, holder file included.
func (h *harness) take(t *testing.T, o options) *taker {
	t.Helper()
	tk, err := acquire(h.w, o)
	require.NoError(t, err)
	require.NoError(t, tk.mark(tk.name()))
	return tk
}

func (h *harness) setCap(t *testing.T, n int) {
	t.Helper()
	require.NoError(t, writeCapacity(h.dir, n, 7, epoch))
}

func TestSharedTakesFirstFreeSlot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 3)
	a := h.take(t, h.opts(0, false, 0))
	b := h.take(t, h.opts(3, false, 0))
	assert.Equal(t, 1, a.slot)
	assert.Equal(t, 2, b.slot)
	assert.Equal(t, 2, h.f.sh[filepath.Join(h.dir, "all")])
	assert.Equal(t, 0, h.f.sh[filepath.Join(h.dir, "gate")], "a shared taker drops the gate once it holds")
	a.release()
	assert.NoFileExists(t, filepath.Join(h.dir, "slot1.holder"))
	c := h.take(t, h.opts(0, false, 0))
	assert.Equal(t, 1, c.slot, "a freed slot is taken again")
	assert.Empty(t, h.c.slept)
}

func TestFullSlotsTimeOutNamingTheHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := h.sibling(102933)
	s.w.exec = func(_ string, _ []string) int {
		assert.Equal(t, 1, run([]string{"--wait", "1s", "--", "make"}, h.w))
		return 0
	}
	require.Equal(t, 0, run([]string{"--slots", "1", "--why", "bench x", "--", "sleep", "9"}, s.w))
	assert.Equal(t, `nova-reserve: no slot within 1s: slot1 held pid=102933 since=2026-11-05T22:03:32Z why=bench\sx cmd=sleep\s9; run: nova-reserve status`+"\n", h.err.String())
	assert.Nil(t, h.ran)
	assert.True(t, h.f.idle())
}

// TestWaitIsABound: a 100 ms wait with a 1 s poll sleeps once, 100 ms, and
// refuses at 100 ms.
func TestWaitIsABound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	h.take(t, h.opts(0, false, 0))
	_, err := acquire(h.w, h.opts(0, false, 100*time.Millisecond))
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, 1, e.code)
	assert.Equal(t, []time.Duration{100 * time.Millisecond}, h.c.slept)
	assert.Equal(t, epoch.Add(100*time.Millisecond), h.c.now())
	assert.Contains(t, e.msg, "no slot within 100ms: slot1 held pid=42")
}

func TestWaiterRunsWhenAHolderEnds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	a := h.take(t, h.opts(1, false, 0))
	h.c.onSleep = func(n int) {
		if n == 3 {
			a.release()
		}
	}
	b := h.take(t, h.opts(1, false, 0))
	assert.Equal(t, 1, b.slot)
	assert.Equal(t, []time.Duration{poll, poll, poll}, h.c.slept)
}

func TestExclusiveWaitsForSharedAndBarsNewShared(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 4)
	a := h.take(t, h.opts(0, false, 0))
	var during error
	h.c.onSleep = func(n int) {
		if n == 1 {
			// the exclusive taker holds the gate now: a shared taker with free slots waits
			other := h.sibling(9)
			other.c = &clock{t: epoch}
			other.w.now, other.w.sleep = other.c.now, other.c.sleep
			_, during = acquire(other.w, h.opts(0, false, time.Second))
		}
		if n == 2 {
			a.release()
		}
	}
	x := h.take(t, h.opts(0, true, 0))
	var e *exitError
	require.ErrorAs(t, during, &e)
	assert.Contains(t, e.msg, "slot1 held pid=42")
	assert.Contains(t, e.msg, "gate held pid=42")
	assert.True(t, h.f.ex[filepath.Join(h.dir, "all")])
	assert.Equal(t, "mode=exclusive slot=all pid=42", x.describe())
	h.c.onSleep = nil
	_, err := acquire(h.w, h.opts(0, false, time.Second))
	assert.ErrorAs(t, err, &e, "no shared taker while an exclusive one runs")
	x.release()
	assert.NoFileExists(t, filepath.Join(h.dir, "gate.holder"))
	assert.NoFileExists(t, filepath.Join(h.dir, "all.holder"))
	_, err = acquire(h.w, h.opts(0, false, 0))
	assert.NoError(t, err)
}

func TestExclusiveTimeoutGivesBackTheGate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	h.take(t, h.opts(0, false, 0))
	_, err := acquire(h.w, h.opts(0, true, 3*time.Second))
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, 1, e.code)
	assert.True(t, strings.HasPrefix(e.msg, "no exclusive hold within 3s: slot1 held pid=42"), e.msg)
	assert.NotContains(t, e.msg, "gate held", "the gate is given back before the holders are read")
	assert.False(t, h.f.ex[filepath.Join(h.dir, "gate")])
	_, err = acquire(h.w, h.opts(0, false, 0))
	assert.NoError(t, err, "shared takers pass again")
}

// TestFakeIsAWitness is the reversed witness for the tests above: a lock
// table that grants everything lets N+1 holders in.
func TestFakeIsAWitness(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	h.w.lock = func(string, bool) (func(), bool, error) { return func() {}, true, nil }
	for range 3 {
		_, err := acquire(h.w, h.opts(2, false, time.Second))
		require.NoError(t, err)
	}
}

// --- capacity ---------------------------------------------------------------

func TestFirstTakerSetsCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.Equal(t, 0, run([]string{"--slots", "2", "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: CAPACITY SET slots=2\nnova-reserve: HELD mode=shared slot=1/2 pid=42\n", h.err.String())
	b, err := os.ReadFile(filepath.Join(h.dir, "capacity"))
	require.NoError(t, err)
	assert.Equal(t, "2\npid 42 at 2026-11-05T22:03:32Z\n", string(b))
	assert.NoFileExists(t, filepath.Join(h.dir, "capacity.42.tmp"))
	h.err.Reset()
	require.Equal(t, 0, run([]string{"--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: HELD mode=shared slot=1/2 pid=42\n", h.err.String(), "the second taker stores nothing")
}

func TestNoSlotsStoresTheDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.w.cpus = 12
	require.Equal(t, 0, run([]string{"--", "make"}, h.w))
	assert.Contains(t, h.err.String(), "CAPACITY SET slots=3\n")
	c, have, err := readCapacity(h.dir)
	require.NoError(t, err)
	assert.True(t, have)
	assert.Equal(t, 3, c.n)
}

// TestFirstTakerRetriesTheClaimNotTheGate: a first taker whose claim is busy
// polls the claim again, never the gate shared, which would hold another first
// taker's claim out (tla/Capacity.tla found that livelock).
func TestFirstTakerRetriesTheClaimNotTheGate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	gate := filepath.Join(h.dir, "gate")
	release, ok, _ := h.f.lock(gate, false) // another taker, mid-read
	require.True(t, ok)
	shared := 0
	h.w.lock = func(path string, exclusive bool) (func(), bool, error) {
		if path == gate && !exclusive {
			shared++
		}
		return h.f.lock(path, exclusive)
	}
	h.c.onSleep = func(n int) {
		if n == 2 {
			release()
		}
	}
	tk, err := acquire(h.w, h.opts(2, false, 0))
	require.NoError(t, err)
	assert.Equal(t, 2, shared, "the gate shared once to read, once to take after storing")
	assert.Equal(t, 2, tk.capSet)
	assert.Equal(t, []time.Duration{poll, poll}, h.c.slept)
}

// TestClaimerSeesTheStoredCapacity: a first taker whose claim waits on all,
// while another first taker stored the capacity and holds slot1, goes back to
// the gate and takes slot2 (tla/Capacity.tla SClaimSeen; without it, it was
// refused while slots were free).
func TestClaimerSeesTheStoredCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	gate := filepath.Join(h.dir, "gate")
	release, ok, _ := h.f.lock(gate, false) // the other first taker, mid-read
	require.True(t, ok)
	h.c.onSleep = func(n int) {
		if n == 1 {
			release()
			s := h.sibling(77)
			s.take(t, s.opts(8, false, 0)) // stores 8, holds slot1 and all shared
		}
	}
	tk, err := acquire(h.w, h.opts(8, false, 3*time.Second))
	require.NoError(t, err)
	assert.Equal(t, 2, tk.slot)
	assert.Zero(t, tk.capSet)
	assert.Equal(t, []time.Duration{poll}, h.c.slept)
}

// TestOtherCapacityIsRefused is the per-caller bug: a --slots 1 holder and a
// --slots 4 caller both ran. Now the second is refused and never runs.
func TestOtherCapacityIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	s := h.sibling(123)
	s.w.exec = func(_ string, _ []string) int {
		assert.Equal(t, 2, run([]string{"--slots", "4", "--dir", h.dir, "--", "make"}, h.w))
		return 0
	}
	require.Equal(t, 0, run([]string{"--slots", "1", "--", "sleep", "9"}, s.w))
	assert.Equal(t, "nova-reserve: this machine's capacity is 1 (set by pid 7 at 2026-11-05T22:03:32Z); you asked 4; run: nova-reserve set --slots 4 --dir "+shellQuote(h.dir)+"\n", h.err.String())
	assert.Nil(t, h.ran)
	assert.True(t, h.f.idle())
}

// TestPerCallerCountingIsTheBug is the reversed witness: a taker that scans
// its own --slots instead of the stored capacity lets two in at capacity 1.
func TestPerCallerCountingIsTheBug(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	a := h.take(t, h.opts(1, false, 0))
	b := &taker{w: h.w, o: h.opts(4, false, 0)}
	ok, err := b.scan(capacity{n: 4}) // the old code: N is the caller's own
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []int{1, 2}, []int{a.slot, b.slot}, "two holders at capacity 1")
}

func TestSetRefusedWhileHeld(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	s := h.sibling(102933)
	s.w.exec = func(_ string, _ []string) int {
		assert.Equal(t, 1, run([]string{"set", "--slots", "4", "--dir", h.dir}, h.w))
		return 0
	}
	require.Equal(t, 0, run([]string{"--why", "ci", "--", "make"}, s.w))
	assert.Equal(t, "nova-reserve set: capacity in use (slot1 held pid=102933 since=2026-11-05T22:03:32Z why=ci cmd=make); retry when free; run: nova-reserve status --dir "+shellQuote(h.dir)+"\n", h.err.String())
	c, _, _ := readCapacity(h.dir)
	assert.Equal(t, 2, c.n)
	assert.NoFileExists(t, filepath.Join(h.dir, "slot3"), "the refusal made no lock file")
}

func TestSetWritesWhenFree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	require.Equal(t, 0, run([]string{"set", "--slots", "4"}, h.w))
	assert.Equal(t, "CAPACITY SET slots=4\n", h.out.String())
	h.out.Reset()
	require.Equal(t, 0, run([]string{"set", "--slots", "4"}, h.w))
	assert.Equal(t, "CAPACITY SET slots=4 note=unchanged\n", h.out.String())
	c, _, _ := readCapacity(h.dir)
	assert.Equal(t, 4, c.n)
	assert.True(t, h.f.idle())
}

func TestCorruptCapacityIsRefusedWithARemedy(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "0\n", "lots\n"} {
		h := newHarness(t)
		path := filepath.Join(h.dir, "capacity")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		assert.Equal(t, 2, run([]string{"--dir", h.dir, "--", "make"}, h.w))
		assert.Equal(t, "nova-reserve: "+path+" holds \""+strings.TrimSpace(body)+"\", not a capacity of 1..1024; run: nova-reserve set --dir "+shellQuote(h.dir)+" --slots 2  # your choice\n", h.err.String())
		assert.Nil(t, h.ran)
		assert.True(t, h.f.idle())
		h.err.Reset()
		require.Equal(t, 0, run([]string{"set", "--slots", "2"}, h.w), "set repairs it")
		c, _, err := readCapacity(h.dir)
		require.NoError(t, err)
		assert.Equal(t, 2, c.n)
	}
}

// TestCapacityRewriteCannotLeaveItEmpty: a rewrite is one write at offset 0,
// then a cut; a kill between leaves the record and old bytes, read as the record.
func TestCapacityRewriteCannotLeaveItEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	path := filepath.Join(h.dir, "capacity")
	require.NoError(t, os.WriteFile(path, []byte("1024\npid 1 at 2026-01-01T00:00:00Z\nmore old bytes\n"), 0o644))
	require.NoError(t, writeCapacity(h.dir, 4, 9, epoch))
	b, _ := os.ReadFile(path)
	assert.Equal(t, "4\npid 9 at 2026-11-05T22:03:32Z\n", string(b))
	// the state a kill between the write and the cut leaves
	require.NoError(t, os.WriteFile(path, []byte("4\npid 9 at 2026-11-05T22:03:32Z\n:00:00Z\nmore old bytes\n"), 0o644))
	c, have, err := readCapacity(h.dir)
	require.NoError(t, err)
	assert.True(t, have)
	assert.Equal(t, capacity{n: 4, pid: "9", at: "2026-11-05T22:03:32Z"}, c)
}

// --- status -------------------------------------------------------------------

// statusDuring runs status from another caller while h's command runs.
func statusDuring(t *testing.T, h *harness, args ...string) string {
	t.Helper()
	var got string
	h.w.exec = func(_ string, _ []string) int {
		s := h.sibling(9)
		touched := false
		s.w.lock = func(string, bool) (func(), bool, error) { touched = true; return nil, false, nil }
		require.Equal(t, 0, run([]string{"status"}, s.w))
		assert.False(t, touched, "status takes no lock, so it is never seen as a holder")
		got = s.out.String()
		return 0
	}
	require.Equal(t, 0, run(args, h.w))
	return got
}

func TestStatusNamesHoldersAndCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	// a slot file from an older, larger capacity
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "slot3"), nil, 0o644))
	assert.Equal(t, `capacity=2 set_by_pid=7 set_at=2026-11-05T22:03:32Z
slot1 held pid=42 since=2026-11-05T22:03:32Z why=nightly cmd=sleep\s9
slot2 free
slot3 free # beyond capacity, from an older one; no new holder takes it
gate free # held by an --exclusive run from when it starts waiting until it ends
all free # the exclusive lock: held by an --exclusive run while it runs
free=1/2
`, statusDuring(t, h, "--why", "nightly", "--", "sleep", "9"))
}

func TestStatusDuringAnExclusiveRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 3)
	got := statusDuring(t, h, "--exclusive", "--", "bench")
	assert.Contains(t, got, "slot1 free\nslot2 free\nslot3 free\n")
	assert.Contains(t, got, "gate held pid=42 since=2026-11-05T22:03:32Z cmd=bench # held by")
	assert.Contains(t, got, "all held pid=42 since=2026-11-05T22:03:32Z cmd=bench # the exclusive lock")
	assert.Contains(t, got, "free=0/3\n")
	assert.NotContains(t, got, "  ")
}

// TestStatusIgnoresADeadHoldersFile: a SIGKILLed holder leaves its file; its
// pid is gone, so the slot reads as free.
func TestStatusIgnoresADeadHoldersFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "slot1.holder"), []byte("pid=555 since=2026-11-05T21:00:00Z\n"), 0o644))
	h.dead[555] = true
	require.Equal(t, 0, run([]string{"status"}, h.w))
	assert.Contains(t, h.out.String(), "slot1 free\n")
	assert.Contains(t, h.out.String(), "free=1/1\n")
}

func TestStatusWithACorruptCapacityStillShowsTheSlots(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	got := statusDuring(t, h, "--", "sleep", "9")
	require.Contains(t, got, "slot1 held pid=42")
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "capacity"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "slot1.holder"), []byte("pid=42 since=x\n"), 0o644))
	h.out.Reset()
	require.Equal(t, 0, run([]string{"status"}, h.w))
	assert.Equal(t, "capacity=? # corrupt: "+filepath.Join(h.dir, "capacity")+` holds "", not a capacity of 1..1024; nova-reserve set --slots 2  # your choice
slot1 held pid=42 since=x
gate free # held by an --exclusive run from when it starts waiting until it ends
all free # the exclusive lock: held by an --exclusive run while it runs
free=?
`, h.out.String())
}

func TestStatusWithNoCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.Equal(t, 0, run([]string{"status"}, h.w))
	assert.Contains(t, h.out.String(), "capacity=unset # the first run stores its --slots (or CPU count / 4); or: nova-reserve set --slots N\n")
	assert.Contains(t, h.out.String(), "free=?\n")
}

func TestStatusRefusesAMissingDir(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	missing := filepath.Join(h.dir, "no-such-dir")
	assert.Equal(t, 2, run([]string{"status", "--dir", missing}, h.w))
	assert.Equal(t, "nova-reserve status: --dir "+missing+" is not a reservation directory (no reservation was taken there); run: nova-reserve status -h\n", h.err.String())
	assert.Empty(t, h.out.String())
	assert.NoDirExists(t, missing)
}

// --- the command line ---------------------------------------------------------

func TestRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		line string
	}{
		{[]string{}, "nova-reserve: no command: nova-reserve [flags] -- <cmd> [args...]; run: nova-reserve -h"},
		{[]string{"--slots", "abc", "--", "x"}, `nova-reserve: --slots wants 1..1024, not "abc"; run: nova-reserve -h`},
		{[]string{"--slots", "0", "--", "x"}, `nova-reserve: --slots wants 1..1024, not "0"; run: nova-reserve -h`},
		{[]string{"--wait", "-1s", "--", "x"}, `nova-reserve: --wait wants a duration above 0, like 90s or 15m, not "-1s"; run: nova-reserve -h`},
		{[]string{"--wait", "0", "--", "x"}, `nova-reserve: --wait wants a duration above 0, like 90s or 15m, not "0"; run: nova-reserve -h`},
		{[]string{"--wait=0s", "--", "x"}, `nova-reserve: --wait wants a duration above 0, like 90s or 15m, not "0s"; run: nova-reserve -h`},
		{[]string{"--bogus", "--", "x"}, "nova-reserve: unknown flag --bogus; run: nova-reserve -h"},
		{[]string{"status", "--slots", "2"}, "nova-reserve status: status takes no --slots; it takes --dir; run: nova-reserve status -h"},
		{[]string{"status", "--wait", "1s"}, "nova-reserve status: status takes no --wait; it takes --dir; run: nova-reserve status -h"},
		{[]string{"--exclusive", "--slots", "2", "--", "x"}, "nova-reserve: --exclusive takes every slot; --slots does not apply; run: nova-reserve -h"},
		{[]string{"--exclusive=yes", "--", "x"}, `nova-reserve: --exclusive takes no value, not "yes"; run: nova-reserve -h`},
		{[]string{"--slots", "1", "--slots", "2", "--", "x"}, "nova-reserve: --slots given twice; run: nova-reserve -h"},
		{[]string{"set"}, "nova-reserve set: set wants --slots N (1..1024); run: nova-reserve set -h"},
		{[]string{"set", "--slots", "2", "--exclusive"}, "nova-reserve set: set takes no --exclusive; it takes --slots, --dir; run: nova-reserve set -h"},
		{[]string{"status", "extra"}, `nova-reserve status: unexpected "extra"; a command goes after --; run: nova-reserve status -h`},
		{[]string{"--why", "", "--", "x"}, `nova-reserve: --why wants text, not ""; run: nova-reserve -h`},
		{[]string{"help", "nope"}, `nova-reserve help: help takes one of run, status, set, not "nope"; run: nova-reserve -h`},
		{[]string{"no-such-verb"}, "nova-reserve: unknown verb \"no-such-verb\"; verbs: run, status, set, help; a command goes after --: nova-reserve -- no-such-verb; run: nova-reserve help"},
		{[]string{"--card", "x", "--", "true"}, "nova-reserve: unknown flag --card; run: nova-reserve -h"},
	} {
		h := newHarness(t)
		h.dir = filepath.Join(h.dir, "never")
		assert.Equal(t, 2, run(c.args, h.w), "%q", c.args)
		assert.Equal(t, c.line+"\n", h.err.String(), "%q", c.args)
		assert.Empty(t, h.out.String(), "%q", c.args)
		assert.Nil(t, h.ran, "%q", c.args)
		assert.NoDirExists(t, h.dir, "%q: a refusal touches nothing", c.args)
		assert.True(t, h.f.idle())
	}
}

func TestVerbHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"help"}, {"-h"}, {"--help"}, {"help", "run"}, {"run", "-h"}, {"--slots", "2", "--help", "--", "x"},
		{"status", "-h"}, {"status", "--help"}, {"help", "status"},
		{"set", "-h"}, {"set", "--help"}, {"help", "set"},
		{"status", "--slots", "x", "-h"},
	} {
		h := newHarness(t)
		h.dir = filepath.Join(h.dir, "never")
		assert.Equal(t, 0, run(args, h.w), "%q", args)
		assert.Contains(t, h.out.String(), "nova-reserve", "%q", args)
		for _, v := range []string{"status", "set"} {
			if strings.Contains(strings.Join(args, " "), v) {
				assert.True(t, strings.HasPrefix(h.out.String(), "nova-reserve "+v), "%q: %s", args, h.out.String())
			}
		}
		assert.Empty(t, h.err.String(), "%q", args)
		assert.NoDirExists(t, h.dir, "%q: help has no side effects", args)
	}
}

func TestCommandIsResolvedBeforeTaking(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		err  error
		code int
		line string
	}{
		{"mkae", &exec.Error{Name: "mkae", Err: exec.ErrNotFound}, 127, "nova-reserve: mkae: command not found on PATH=/usr/bin:/bin\n"},
		{"./mkae", &exec.Error{Name: "./mkae", Err: fs.ErrNotExist}, 127, "nova-reserve: ./mkae: no such file\n"},
		{"mkae", &exec.Error{Name: "mkae", Err: fs.ErrPermission}, 126, "nova-reserve: mkae: not executable\n"},
	} {
		h := newHarness(t)
		h.dir = filepath.Join(h.dir, "never")
		h.w.resolve = func(string) (string, error) { return "", c.err }
		assert.Equal(t, c.code, run([]string{"--", c.name, "-j4"}, h.w))
		assert.Equal(t, c.line, h.err.String())
		assert.Nil(t, h.ran)
		assert.NoDirExists(t, h.dir, "nothing taken, nothing made")
		assert.True(t, h.f.idle())
	}
}

// TestCapacityFieldsNeedNoEscaping: who set the capacity is two plain fields;
// a file with no such line says so.
func TestCapacityFieldsNeedNoEscaping(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "capacity"), []byte("3\n"), 0o644))
	require.Equal(t, 0, run([]string{"status"}, h.w))
	assert.True(t, strings.HasPrefix(h.out.String(), "capacity=3 set_by_pid=? set_at=?\n"), h.out.String())
	assert.Equal(t, 2, run([]string{"--slots", "4", "--", "x"}, h.w))
	assert.Contains(t, h.err.String(), "capacity is 3 (set by an unknown taker); you asked 4")
}

func TestMissingDirParentIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	gone := filepath.Join(h.dir, "no", "x")
	assert.Equal(t, 2, run([]string{"--dir", gone, "--why", "a b", "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: --dir "+gone+": parent "+filepath.Dir(gone)+" does not exist; run: mkdir -p "+
		shellQuote(filepath.Dir(gone))+" && nova-reserve --dir "+shellQuote(gone)+" --why 'a b' -- make\n", h.err.String())
	assert.Nil(t, h.ran)
	assert.NoDirExists(t, filepath.Dir(gone))
}

// TestMissingParentNamesTheEnvironment: a directory from $NOVA_RESERVE_DIR is
// named as that, not as a --dir the caller never gave.
func TestMissingParentNamesTheEnvironment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.dir = filepath.Join(h.dir, "no", "x")
	assert.Equal(t, 2, run([]string{"set", "--slots", "2"}, h.w))
	assert.Equal(t, "nova-reserve set: NOVA_RESERVE_DIR "+h.dir+": parent "+filepath.Dir(h.dir)+" does not exist; run: mkdir -p "+
		shellQuote(filepath.Dir(h.dir))+" && nova-reserve set --slots 2\n", h.err.String())
}

func TestUnwritableDirParentIsRefused(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs a directory this user cannot write")
	}
	h := newHarness(t)
	ro := filepath.Join(h.dir, "ro")
	require.NoError(t, os.Mkdir(ro, 0o500))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	assert.Equal(t, 2, run([]string{"set", "--slots", "2", "--dir", filepath.Join(ro, "r")}, h.w))
	assert.Equal(t, "nova-reserve set: --dir "+filepath.Join(ro, "r")+": parent "+ro+" is not writable by this user; run: ls -ld "+shellQuote(ro)+"\n", h.err.String())
}

// TestUnwritableHolderFileIsRefused: a holder file this run can neither remove
// nor write (another user's in the sticky directory; here a directory in its
// place) would leave status naming no holder, or a stale one, while this run
// holds the slot; the run is refused instead, and gives the slot back.
func TestUnwritableHolderFileIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	p := filepath.Join(h.dir, "slot1.holder")
	require.NoError(t, os.MkdirAll(filepath.Join(p, "x"), 0o755))
	assert.Equal(t, 2, run([]string{"--", "make"}, h.w))
	assert.Nil(t, h.ran, "the command never ran")
	assert.True(t, h.f.idle(), "the slot was given back")
	assert.Regexp(t, "^nova-reserve: open "+regexp.QuoteMeta(p)+": .*; status could not show this hold; run: sudo rm -f "+
		regexp.QuoteMeta(shellQuote(p))+" && nova-reserve -- make\n$", h.err.String())
}

// TestUnwritableCapacityIsRefusedWithARemedy: set on a capacity file this user
// cannot write says how to clear it.
func TestUnwritableCapacityIsRefusedWithARemedy(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs a file this user cannot write")
	}
	h := newHarness(t)
	h.setCap(t, 2)
	p := filepath.Join(h.dir, "capacity")
	require.NoError(t, os.Chmod(p, 0o444))
	assert.Equal(t, 2, run([]string{"set", "--slots", "3"}, h.w))
	assert.Equal(t, "nova-reserve set: open "+p+": permission denied; run: sudo rm -f "+shellQuote(p)+
		" && nova-reserve set --slots 3\n", h.err.String())
}

// TestDirThroughAFileIsRefused: a --dir that is a file, or under one, is one
// line with a remedy, not a raw system error.
func TestDirThroughAFileIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	file := filepath.Join(h.dir, "f")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	assert.Equal(t, 2, run([]string{"--dir", file, "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: --dir "+file+" is a file, not a directory; run: ls -ld "+shellQuote(file)+"\n", h.err.String())
	h.err.Reset()
	under := filepath.Join(file, "r")
	assert.Equal(t, 2, run([]string{"--dir", under, "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: --dir "+under+": not a directory; run: ls -ld "+shellQuote(file)+"\n", h.err.String())
	assert.Nil(t, h.ran)
}

func TestRunPassesTheStatusAndFrees(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var path string
	h.w.exec = func(p string, argv []string) int { path, h.ran = p, argv; return 7 }
	assert.Equal(t, 7, run([]string{"--slots", "2", "--", "make", "-j4"}, h.w))
	assert.Equal(t, "/bin/make", path)
	assert.Equal(t, []string{"make", "-j4"}, h.ran)
	// stderr carries only the tool's own lines
	for _, l := range strings.Split(strings.TrimSpace(h.err.String()), "\n") {
		assert.True(t, strings.HasPrefix(l, "nova-reserve: "), l)
	}
	assert.True(t, h.f.idle())
	assert.NoFileExists(t, filepath.Join(h.dir, "slot1.holder"))
}

// --- helpers --------------------------------------------------------------------

func TestEsc(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `two\swords\ttab\nnl\x07\\`, esc("two words\ttab\nnl\x07\\"))
	assert.Equal(t, "plain", esc("plain"))
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/tmp/x", shellQuote("/tmp/x"))
	assert.Equal(t, `'C:\jobs'`, shellQuote(`C:\jobs`))
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
	assert.Equal(t, "''", shellQuote(""))
}
