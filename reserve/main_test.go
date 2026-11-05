package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
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

func newClock() *clock { return &clock{t: epoch} }

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
	dir      string
	ran      []string
	code     int
	out, err bytes.Buffer
	w        world
}

func newHarness(t *testing.T) *harness {
	h := &harness{f: newFake(), c: newClock(), dir: t.TempDir()}
	h.w = world{
		env:     func(k string) string { return map[string]string{"NOVA_RESERVE_DIR": h.dir}[k] },
		lock:    h.f.lock,
		now:     h.c.now,
		sleep:   h.c.sleep,
		resolve: func(name string) (string, error) { return "/bin/" + name, nil },
		exec: func(_ string, argv []string) int {
			h.ran = argv
			return h.code
		},
		hold:   func(time.Duration) string { return "SIGTERM" },
		parent: func() string { return "" },
		pid:    42,
		cpus:   8,
		stdout: &h.out,
		stderr: &h.err,
	}
	return h
}

// sibling is a second caller on the same machine: same locks, dir and clock.
func (h *harness) sibling(pid int) *harness {
	s := &harness{f: h.f, c: h.c, dir: h.dir}
	s.w = h.w
	s.w.pid, s.w.stdout, s.w.stderr = pid, &s.out, &s.err
	s.w.exec = func(_ string, argv []string) int { s.ran = argv; return s.code }
	return s
}

func (h *harness) opts(slots int, exclusive bool, wait time.Duration) options {
	return options{verb: "run", slots: slots, exclusive: exclusive, wait: wait, dir: h.dir}
}

func (h *harness) setCap(t *testing.T, n int) {
	t.Helper()
	require.NoError(t, writeCapacity(h.dir, n, 7, epoch))
}

func TestSharedTakesFirstFreeSlot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 3)
	a, err := acquire(h.w, h.opts(0, false, 0))
	require.NoError(t, err)
	b, err := acquire(h.w, h.opts(3, false, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, a.slot)
	assert.Equal(t, 2, b.slot)
	assert.Equal(t, 2, h.f.sh[filepath.Join(h.dir, "all")])
	assert.Equal(t, 0, h.f.sh[filepath.Join(h.dir, "gate")], "a shared taker drops the gate once it holds")
	a.release()
	c, err := acquire(h.w, h.opts(0, false, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, c.slot, "a freed slot is taken again")
	assert.Empty(t, h.c.slept)
}

func TestFullSlotsTimeOutNamingTheHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := h.sibling(102933)
	require.Equal(t, 0, run([]string{"--slots", "1", "--why", "bench x", "--", "true"}, s.w))
	s.w.exec = func(_ string, _ []string) int {
		assert.Equal(t, 1, run([]string{"--wait", "1s", "--", "make"}, h.w))
		return 0
	}
	require.Equal(t, 0, run([]string{"--why", "bench x", "--", "sleep", "9"}, s.w))
	assert.Equal(t, "nova-reserve: no slot within 1s: slot1 held by pid 102933 since 2026-11-05T22:03:32Z (why: bench x); run: nova-reserve status\n", h.err.String())
	assert.Nil(t, h.ran)
	assert.True(t, h.f.idle())
}

// TestWaitIsABound: a 100 ms wait with a 1 s poll sleeps once, 100 ms, and
// refuses at 100 ms.
func TestWaitIsABound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	_, err := acquire(h.w, h.opts(0, false, 0))
	require.NoError(t, err)
	_, err = acquire(h.w, h.opts(0, false, 100*time.Millisecond))
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, 1, e.code)
	assert.Equal(t, []time.Duration{100 * time.Millisecond}, h.c.slept)
	assert.Equal(t, epoch.Add(100*time.Millisecond), h.c.now())
	assert.Contains(t, e.msg, "no slot within 100ms: slot1 held")
}

func TestWaiterRunsWhenAHolderEnds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	a, err := acquire(h.w, h.opts(1, false, 0))
	require.NoError(t, err)
	h.c.onSleep = func(n int) {
		if n == 3 {
			a.release()
		}
	}
	b, err := acquire(h.w, h.opts(1, false, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, b.slot)
	assert.Equal(t, []time.Duration{poll, poll, poll}, h.c.slept)
}

func TestExclusiveWaitsForSharedAndBarsNewShared(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 4)
	a, err := acquire(h.w, h.opts(0, false, 0))
	require.NoError(t, err)
	var during error
	h.c.onSleep = func(n int) {
		if n == 1 {
			// the exclusive taker holds the gate now: a shared taker with free slots waits
			other := newHarness(t)
			other.w.lock, other.dir = h.f.lock, h.dir
			o := other.opts(0, false, time.Second)
			o.dir = h.dir
			_, during = acquire(other.w, o)
		}
		if n == 2 {
			a.release()
		}
	}
	x, err := acquire(h.w, h.opts(0, true, 0))
	require.NoError(t, err)
	var e *exitError
	require.ErrorAs(t, during, &e)
	assert.Contains(t, e.msg, "gate held by an --exclusive run")
	assert.True(t, h.f.ex[filepath.Join(h.dir, "all")])
	assert.Equal(t, "mode=exclusive slot=all pid=42", x.describe(42))
	h.c.onSleep = nil
	_, err = acquire(h.w, h.opts(0, false, time.Second))
	assert.ErrorAs(t, err, &e, "no shared taker while an exclusive one runs")
	x.release()
	_, err = acquire(h.w, h.opts(0, false, 0))
	assert.NoError(t, err)
}

func TestExclusiveTimeoutGivesBackTheGate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	_, err := acquire(h.w, h.opts(0, false, 0))
	require.NoError(t, err)
	_, err = acquire(h.w, h.opts(0, true, 3*time.Second))
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, 1, e.code)
	assert.True(t, strings.HasPrefix(e.msg, "no exclusive hold within 3s: slot1 held"), e.msg)
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
	h.err.Reset()
	require.Equal(t, 0, run([]string{"--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: HELD mode=shared slot=1/2 pid=42\n", h.err.String(), "the second taker stores nothing")
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
	a, err := acquire(h.w, h.opts(1, false, 0))
	require.NoError(t, err)
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
		assert.Equal(t, 1, run([]string{"set", "--slots", "4"}, h.w))
		return 0
	}
	require.Equal(t, 0, run([]string{"--why", "ci", "--", "make"}, s.w))
	assert.Equal(t, "nova-reserve set: capacity in use: slot1 held by pid 102933 since 2026-11-05T22:03:32Z (why: ci); retry when free\n", h.err.String())
	c, _, _ := readCapacity(h.dir)
	assert.Equal(t, 2, c.n)
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

func TestCorruptCapacityIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "capacity"), []byte("lots\n"), 0o644))
	assert.Equal(t, 2, run([]string{"--", "make"}, h.w))
	assert.Contains(t, h.err.String(), `holds "lots", not a capacity of 1..1024; run: nova-reserve set --slots N`)
	assert.Nil(t, h.ran)
	h.err.Reset()
	require.Equal(t, 0, run([]string{"set", "--slots", "2"}, h.w), "set repairs it")
}

// --- hold ---------------------------------------------------------------------

func TestHoldUntilFor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var asked time.Duration
	h.w.hold = func(d time.Duration) string {
		asked = d
		h.c.t = h.c.t.Add(d)
		return ""
	}
	require.Equal(t, 0, run([]string{"hold", "--for", "30m", "--slots", "8"}, h.w))
	assert.Equal(t, 30*time.Minute, asked)
	assert.Equal(t, "CAPACITY SET slots=8\nHELD mode=shared slot=1/8 pid=42\nRELEASED reason=for held_for=30m0s\n", h.out.String())
	assert.Empty(t, h.err.String())
	assert.True(t, h.f.idle())
}

func TestHoldUntilSignal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.w.hold = func(d time.Duration) string {
		assert.Zero(t, d, "no --for: no bound")
		h.c.t = h.c.t.Add(90 * time.Second)
		return "SIGINT"
	}
	require.Equal(t, 0, run([]string{"hold", "--exclusive"}, h.w))
	assert.Equal(t, "HELD mode=exclusive slot=all pid=42\nRELEASED reason=signal signal=SIGINT held_for=1m30s\n", h.out.String())
	assert.True(t, h.f.idle())
}

func TestHoldHonoursDirAndLabelsWithTheParent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := filepath.Join(t.TempDir(), "jobs")
	h.w.parent = func() string { return "agent --tool bench" }
	var status bytes.Buffer
	h.w.hold = func(time.Duration) string {
		s := h.sibling(9)
		require.Equal(t, 0, run([]string{"status", "--dir", other}, s.w))
		status.WriteString(s.out.String())
		return "SIGTERM"
	}
	require.Equal(t, 0, run([]string{"hold", "--dir", other, "--slots", "2"}, h.w))
	assert.Contains(t, status.String(), "slot1 held pid=42 since=2026-11-05T22:03:32Z why=agent\\s--tool\\sbench\n")
	assert.Contains(t, status.String(), "free=1/2\n")
	assert.NoFileExists(t, filepath.Join(h.dir, "capacity"), "the default directory is untouched")
	assert.FileExists(t, filepath.Join(other, "capacity"))
}

// --- status -------------------------------------------------------------------

func TestStatusNamesHoldersAndCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2)
	// a slot file from an older, larger capacity
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "slot3"), nil, 0o644))
	h.w.exec = func(_ string, _ []string) int {
		s := h.sibling(9)
		require.Equal(t, 0, run([]string{"status"}, s.w))
		h.out.WriteString(s.out.String())
		return 0
	}
	require.Equal(t, 0, run([]string{"--why", "nightly", "--", "sleep", "9"}, h.w))
	assert.Equal(t, `capacity=2 set_by=pid\s7\sat\s2026-11-05T22:03:32Z
slot1 held pid=42 since=2026-11-05T22:03:32Z why=nightly cmd=sleep\s9
slot2 free
slot3 free # beyond capacity, from an older one; no new holder takes it
gate free # held by an --exclusive run while it waits, so no new holder passes
all free # the exclusive lock; held only by an --exclusive run
free=1/2
`, h.out.String())
}

func TestStatusWithNoCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.Equal(t, 0, run([]string{"status"}, h.w))
	assert.Contains(t, h.out.String(), "capacity=unset # the first run stores its --slots (or CPU count / 4); or: nova-reserve set --slots N\n")
	assert.Contains(t, h.out.String(), "free=unset\n")
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
		line string // the whole stderr, or "" to check only the code
	}{
		{[]string{}, "nova-reserve: no command: nova-reserve [flags] -- <cmd> [args...]; run: nova-reserve -h"},
		{[]string{"--slots", "abc", "--", "x"}, `nova-reserve: --slots wants 1..1024, not "abc"; run: nova-reserve -h`},
		{[]string{"--slots", "0", "--", "x"}, `nova-reserve: --slots wants 1..1024, not "0"; run: nova-reserve -h`},
		{[]string{"--wait", "-1s", "--", "x"}, `nova-reserve: --wait wants a duration like 90s or 15m, not "-1s"; run: nova-reserve -h`},
		{[]string{"--bogus", "--", "x"}, "nova-reserve: unknown flag --bogus; run: nova-reserve -h"},
		{[]string{"hold", "--no-such-flag"}, "nova-reserve hold: unknown flag --no-such-flag; run: nova-reserve hold -h"},
		{[]string{"status", "--slots", "2"}, "nova-reserve status: status takes no --slots; it takes --dir; run: nova-reserve status -h"},
		{[]string{"status", "--wait", "1s"}, "nova-reserve status: status takes no --wait; it takes --dir; run: nova-reserve status -h"},
		{[]string{"--for", "1m", "--", "x"}, "nova-reserve: run takes no --for; it takes --slots, --exclusive, --wait, --why, --dir; run: nova-reserve -h"},
		{[]string{"hold", "--slots"}, "nova-reserve hold: --slots wants 1..1024; run: nova-reserve hold -h"},
		{[]string{"hold", "--for", "0s"}, `nova-reserve hold: --for wants a duration like 30m, more than 0, not "0s"; run: nova-reserve hold -h`},
		{[]string{"--exclusive", "--slots", "2", "--", "x"}, "nova-reserve: --exclusive takes every slot; --slots does not apply; run: nova-reserve -h"},
		{[]string{"--exclusive=yes", "--", "x"}, `nova-reserve: --exclusive takes no value, not "yes"; run: nova-reserve -h`},
		{[]string{"--slots", "1", "--slots", "2", "--", "x"}, "nova-reserve: --slots given twice; run: nova-reserve -h"},
		{[]string{"set"}, "nova-reserve set: set wants --slots N (1..1024); run: nova-reserve set -h"},
		{[]string{"set", "--slots", "2", "--exclusive"}, "nova-reserve set: set takes no --exclusive; it takes --slots, --dir; run: nova-reserve set -h"},
		{[]string{"status", "extra"}, `nova-reserve status: unexpected "extra"; a command goes after --; run: nova-reserve status -h`},
		{[]string{"hold", "--", "x"}, `nova-reserve hold: hold takes no command, not "x"; run: nova-reserve hold -h`},
		{[]string{"--why", "", "--", "x"}, `nova-reserve: --why wants text, not ""; run: nova-reserve -h`},
		{[]string{"help", "nope"}, `nova-reserve help: help takes one of run, hold, status, set, not "nope"; run: nova-reserve -h`},
		{[]string{"no-such-verb"}, "nova-reserve: unknown verb \"no-such-verb\"; verbs: run, hold, status, set, help; a command goes after --: nova-reserve -- no-such-verb; run: nova-reserve help"},
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
		{"hold", "-h"}, {"hold", "--help"}, {"help", "hold"},
		{"status", "-h"}, {"status", "--help"}, {"help", "status"},
		{"set", "-h"}, {"set", "--help"}, {"help", "set"},
		{"status", "--slots", "x", "-h"},
	} {
		h := newHarness(t)
		h.dir = filepath.Join(h.dir, "never")
		assert.Equal(t, 0, run(args, h.w), "%q", args)
		assert.Contains(t, h.out.String(), "nova-reserve", "%q", args)
		for _, v := range []string{"hold", "status", "set"} {
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
		err  error
		code int
		line string
	}{
		{&exec.Error{Name: "mkae", Err: exec.ErrNotFound}, 127, "nova-reserve: mkae: command not found\n"},
		{&exec.Error{Name: "mkae", Err: fs.ErrPermission}, 126, "nova-reserve: mkae: not executable\n"},
	} {
		h := newHarness(t)
		h.dir = filepath.Join(h.dir, "never")
		h.w.resolve = func(string) (string, error) { return "", c.err }
		assert.Equal(t, c.code, run([]string{"--", "mkae", "-j4"}, h.w))
		assert.Equal(t, c.line, h.err.String())
		assert.Nil(t, h.ran)
		assert.NoDirExists(t, h.dir, "nothing taken, nothing made")
		assert.True(t, h.f.idle())
	}
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

func TestEscRoundTrips(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "plain", "two words", `back\slash`, "tab\tnl\nbell\x07", `\s literal`, "trailing\\", `\x4`} {
		e := esc(s)
		assert.NotContains(t, e, " ")
		assert.Equal(t, s, unesc(e), "%q via %q", s, e)
	}
}

func TestProcArgs(t *testing.T) {
	t.Parallel()
	b := append([]byte{3, 0, 0, 0}, []byte("/usr/bin/agent\x00\x00\x00agent\x00--tool\x00bench x\x00HOME=/h\x00")...)
	assert.Equal(t, "agent --tool bench x", procArgs(b))
	assert.Equal(t, "", procArgs([]byte{1}))
	assert.Equal(t, "make -j4", joinArgs([]byte("make\x00-j4\x00")))
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/tmp/x", shellQuote("/tmp/x"))
	assert.Equal(t, `'C:\jobs'`, shellQuote(`C:\jobs`))
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
	assert.Equal(t, "''", shellQuote(""))
}
