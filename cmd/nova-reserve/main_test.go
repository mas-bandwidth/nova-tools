package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLocks models kernel flock(2) in memory: exclusive locks conflict with all
// locks; shared locks conflict only with exclusive locks.
type fakeLocks struct {
	mu sync.Mutex
	ex map[string]bool
	sh map[string]int
}

func newFake() *fakeLocks { return &fakeLocks{ex: map[string]bool{}, sh: map[string]int{}} }

func (f *fakeLocks) lock(path string, exclusive bool) (func(), bool, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			_, ok, oerr := osLock(path, exclusive)
			return nil, ok, oerr
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if !exclusive {
			return nil, false, os.ErrNotExist
		}
		f0, oerr := openIn(path, os.O_RDWR|os.O_CREATE)
		if oerr != nil {
			return nil, false, oerr
		}
		_ = f0.Close()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ex[path] || (exclusive && f.sh[path] > 0) {
		return nil, false, nil
	}
	if exclusive {
		f.ex[path] = true
		var once sync.Once
		return func() {
			once.Do(func() {
				f.mu.Lock()
				delete(f.ex, path)
				f.mu.Unlock()
			})
		}, true, nil
	}
	f.sh[path]++
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			f.sh[path]--
			if f.sh[path] <= 0 {
				delete(f.sh, path)
			}
			f.mu.Unlock()
		})
	}, true, nil
}

func (f *fakeLocks) idle() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ex) == 0 && len(f.sh) == 0
}

// clock starts at Unix timestamp 1000 and advances deterministically on sleep.
var epoch = time.Unix(1000, 0).UTC()

type clock struct {
	mu      sync.Mutex
	t       time.Time
	slept   []time.Duration
	onSleep func(n int)
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) sleep(d time.Duration) {
	c.mu.Lock()
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
	n := len(c.slept)
	fn := c.onSleep
	c.mu.Unlock()
	if fn != nil {
		fn(n)
	}
}

type fakeWatch struct {
	c *clock
}

func (w fakeWatch) drain()                        {}
func (w fakeWatch) wait(_ []int, d time.Duration) { w.c.sleep(d) }
func (w fakeWatch) close()                        {}

type harness struct {
	f        *fakeLocks
	c        *clock
	envMap   map[string]string
	dir      string
	ran      []string
	ranEnv   []string
	ranLower bool
	code     int
	out, err bytes.Buffer
	w        world
}

func newHarness(t *testing.T) *harness {
	h := &harness{
		f:      newFake(),
		c:      &clock{t: epoch},
		envMap: map[string]string{"PATH": "/usr/bin:/bin"},
		dir:    t.TempDir(),
	}
	h.w = world{
		env:     func(k string) string { return h.envMap[k] },
		environ: func() []string { return []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent"} },
		lock:    h.f.lock,
		watch:   func(string) watcher { return fakeWatch{c: h.c} },
		now:     h.c.now,
		sleep:   h.c.sleep,
		resolve: func(name string) (string, error) { return "/bin/" + name, nil },
		exec: func(_ string, argv, env []string, lower bool) int {
			h.ran, h.ranEnv, h.ranLower = argv, env, lower
			return h.code
		},
		waitEOF: func(in io.Reader) {
			if in != nil {
				_, _ = io.Copy(io.Discard, in)
			}
		},
		running: func(int) bool { return true },
		bench:   func(cores int, _ bool) (int, int, error) { return max(1, cores), 32, nil },
		pid:     42,
		cpus:    8,
		goos:    "linux",
		goarch:  "amd64",
		usable:  func() ([]int, error) { return nil, nil },
		groups:  func([]int) [][]int { return nil },
		pin:     func([]int) error { panic("pinned with no usable CPUs") },
		stdin:   strings.NewReader(""),
		stdout:  &h.out,
		stderr:  &h.err,
	}
	return h
}

func (h *harness) sibling(pid int) *harness {
	s := &harness{f: h.f, c: h.c, envMap: map[string]string{"PATH": "/usr/bin:/bin"}, dir: h.dir}
	s.w = h.w
	s.w.pid, s.w.stdout, s.w.stderr = pid, &s.out, &s.err
	s.w.env = func(k string) string { return s.envMap[k] }
	s.w.exec = func(_ string, argv, env []string, lower bool) int {
		s.ran, s.ranEnv, s.ranLower = argv, env, lower
		return s.code
	}
	return s
}

func (h *harness) setCap(t *testing.T, cpu, mem, cores int, tags ...string) {
	t.Helper()
	args := []string{"set", "--dir", h.dir, "--cpu", itoaTest(cpu), "--memory", itoaTest(mem), "--cores", itoaTest(cores)}
	if len(tags) > 0 {
		args = append(args, "--tags", strings.Join(tags, ","))
	}
	require.Equal(t, 0, run(args, h.w), h.err.String())
	h.out.Reset()
	h.err.Reset()
}

func itoaTest(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(jsonNumber(n), "\n", ""))
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (h *harness) take(t *testing.T, extra ...string) *activeHold {
	t.Helper()
	args := append([]string{"hold", "--dir", h.dir}, extra...)
	o, err := parse(args)
	require.NoError(t, err)
	require.NoError(t, ensureLayout(o.dir, o.dirFrom, "nova-reserve"))
	ah, err := acquire(h.w, o)
	require.NoError(t, err)
	return ah
}

func envLookup(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// --- measure & set -----------------------------------------------------------

func TestMeasureWritesCapacityAndHonorsForce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.w.bench = func(cores int, force bool) (int, int, error) {
		if !force {
			return 0, 0, errors.New("machine is busy (60% CPU in use); stop other work or pass --force")
		}
		return 12, 64, nil
	}
	assert.Equal(t, 1, run([]string{"measure", "--dir", h.dir}, h.w))
	assert.Contains(t, h.err.String(), "machine is busy")
	assert.Contains(t, h.err.String(), "run: nova-reserve measure --force --dir "+shellQuote(h.dir))
	h.err.Reset()

	assert.Equal(t, 0, run([]string{"measure", "--force", "--dir", h.dir}, h.w))
	assert.Equal(t, "MEASURE OK cpu=12 memory=64 cores=8\n", h.out.String())
	h.out.Reset()

	// Add custom tag and custom resource, then re-measure --json: tags & custom resources stay.
	assert.Equal(t, 0, run([]string{"set", "--dir", h.dir, "--tags", "gpu", "--resources", "ps5=1"}, h.w))
	h.out.Reset()
	assert.Equal(t, 0, run([]string{"measure", "--force", "--json", "--dir", h.dir}, h.w))
	var got struct {
		CPU         int      `json:"cpu"`
		Memory      int      `json:"memory"`
		UsableCores int      `json:"usable_cores"`
		Tags        []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(h.out.Bytes(), &got))
	assert.Equal(t, 12, got.CPU)
	assert.Equal(t, 64, got.Memory)
	assert.Equal(t, 8, got.UsableCores)
	assert.Equal(t, []string{"amd64", "gpu", "linux"}, got.Tags)

	c, have, err := readCapacity(h.dir)
	require.NoError(t, err)
	require.True(t, have)
	assert.Equal(t, 1, c.Resources["ps5"])
	assert.True(t, h.f.idle())
}

func TestCustomDirNeverAutoMeasures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	calledBench := false
	h.w.bench = func(int, bool) (int, int, error) { calledBench = true; return 8, 16, nil }
	assert.Equal(t, 2, run([]string{"--dir", h.dir, "--", "make"}, h.w))
	assert.False(t, calledBench, "custom --dir must never auto-measure")
	assert.Contains(t, h.err.String(), "has no capacity.json")
	assert.Nil(t, h.ran)
}

func TestSetUpdatesAtomicallyWhileHoldsAreActive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 4, 8, 4)
	a := h.take(t, "--needs", "cpu 4, memory 8")
	defer a.release(false)

	s := h.sibling(99)
	assert.Equal(t, 0, run([]string{"set", "--dir", h.dir, "--cpu", "8", "--tags", "gpu", "--resources", "ps5=1"}, s.w))
	assert.Equal(t, "CAPACITY SET cpu=8 memory=8 cores=4\n", s.out.String())
	s.out.Reset()
	assert.Equal(t, 0, run([]string{"set", "--dir", h.dir, "--cpu", "8", "--tags", "gpu", "--resources", "ps5=1"}, s.w))
	assert.Equal(t, "CAPACITY SET cpu=8 memory=8 cores=4 note=unchanged\n", s.out.String())
}

// --- multi-resource fit, never-fit (R12), and FIFO waiter line (R18) --------

func TestNeverFitRefusesImmediatelyWithoutWaiting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 4, 8, 4, "gpu")
	for _, spec := range []string{
		"cpu 8",
		"memory 16",
		"metal",
		"ps5 1",
	} {
		h.err.Reset()
		assert.Equal(t, 1, run([]string{"--dir", h.dir, "--needs", spec, "--wait", "10m", "--", "make"}, h.w), spec)
		assert.Empty(t, h.c.slept, "never-fit must not sleep or queue: %s", spec)
		assert.Nil(t, h.ran)
	}
	assert.True(t, h.f.idle())
}

func TestStrictArrivalOrderWaiterLineAndNoWait(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 4, 8, 4)

	// Hold 1 takes cpu 3 (leaving cpu 1 free).
	h1 := h.take(t, "--needs", "cpu 3, memory 2")

	// Waiter W1 needs cpu 2 -> does not fit in cpu 1, so enters line/ at seq 2.
	w1 := h.sibling(101)
	w1.c.onSleep = func(n int) {
		if n == 1 {
			// While W1 waits at the head of line/, a smaller --nowait caller asking cpu 1
			// MUST NOT jump ahead of W1, even though 1 CPU is free!
			jumper := h.sibling(102)
			code := run([]string{"--dir", h.dir, "--needs", "cpu 1, memory 1", "--nowait", "--", "echo"}, jumper.w)
			assert.Equal(t, 1, code, "smaller caller must not bypass head of waiter line")
			assert.Contains(t, jumper.err.String(), "NO ROOM")
		}
		if n == 2 {
			h1.release(false)
		}
	}
	w1Hold := w1.take(t, "--needs", "cpu 2, memory 2")
	w1.c.onSleep = nil
	w1Hold.release(false)
	assert.True(t, h.f.idle())
}

func TestWaitDeadlineClampsSleepAndCleansLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2, 4, 2)
	held := h.take(t, "--needs", "cpu 2, memory 2", "--why", "heavy build")
	defer held.release(false)

	s := h.sibling(88)
	assert.Equal(t, 1, run([]string{"--dir", h.dir, "--wait", "100ms", "--", "make"}, s.w))
	assert.Equal(t, []time.Duration{100 * time.Millisecond}, h.c.slept)
	assert.Equal(t, epoch.Add(100*time.Millisecond), h.c.now())
	assert.Contains(t, s.err.String(), `no hold within 100ms: hold 1 held holder=person label=work pid=42 why=heavy\sbuild`)
}

// --- CI immediate acquisition, priority displacement, and between-command pause ---

func TestCIDisplacesLowestPriorityYoungestFirstAndNeverExceedsCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 6, 12, 6, "gpu")
	assert.Equal(t, 0, run([]string{"set", "--dir", h.dir, "--resources", "ps5=1"}, h.w))
	h.out.Reset()

	// Three active holds filling all 6 CPUs:
	// seq 1: high, cpu 2, mem 4, ps5 1
	// seq 2: normal (older), cpu 2, mem 4
	// seq 3: normal (younger), cpu 2, mem 4
	s1 := h.sibling(201).take(t, "--holder", "bench", "--consumer", "c-high@1", "--label", "critical", "--priority", "high", "--needs", "cpu 2, memory 4, ps5")
	s2 := h.sibling(202).take(t, "--holder", "bench", "--consumer", "c-old@1", "--label", "work", "--priority", "normal", "--needs", "cpu 2, memory 4")
	s3 := h.sibling(203).take(t, "--holder", "bench", "--consumer", "c-young@1", "--label", "work", "--priority", "normal", "--needs", "cpu 2, memory 4")

	// CI arrives needing cpu 2, memory 4 -> displaces ONLY s3 (normal, youngest: seq 3)!
	ci := h.sibling(300).take(t, "--holder", "ci", "--needs", "cpu 2, memory 4")

	st := h.sibling(400)
	require.Equal(t, 0, run([]string{"status", "--json", "--dir", h.dir}, st.w))
	var sj statusJSON
	require.NoError(t, json.Unmarshal(st.out.Bytes(), &sj))
	assert.True(t, sj.AnyPaused)
	assert.Equal(t, []string{"c-high@1", "c-old@1"}, sj.SprintConsumers)
	assert.Equal(t, []string{"c-young@1"}, sj.PausedConsumers)
	assert.Equal(t, map[string]int{"cpu": 4, "memory": 8, "ps5": 1}, sj.HeldSprint)
	assert.Equal(t, map[string]int{"cpu": 2, "memory": 4}, sj.HeldOther)
	assert.Equal(t, map[string]map[string]int{"work": {"cpu": 2, "memory": 4}}, sj.PausedByLabel)
	assert.Equal(t, map[string]map[string]int{
		"ci":       {"cpu": 2, "memory": 4},
		"critical": {"cpu": 2, "memory": 4, "ps5": 1},
		"work":     {"cpu": 2, "memory": 4},
	}, sj.HeldByLabel)

	// While c-young@1 is paused, even if s2 finishes (freeing cpu 2!), a new --nowait
	// friend caller is barred until paused work unpauses first — and s2's release
	// immediately unpauses s3!
	s2.release(true)
	st.out.Reset()
	require.Equal(t, 0, run([]string{"status", "--json", "--dir", h.dir}, st.w))
	require.NoError(t, json.Unmarshal(st.out.Bytes(), &sj))
	assert.False(t, sj.AnyPaused, "s2 releasing freed enough room to unpause s3 immediately")
	assert.Equal(t, []string{"c-high@1", "c-young@1"}, sj.SprintConsumers)

	ci.release(false)
	s1.release(false)
	s3.release(false)
	assert.True(t, h.f.idle())
}

func TestBetweenCommandPauseResumesAheadOfWaiters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 4, 8, 4)

	// Step 1 of consumer card-1.w1@1 holds cpu 4, memory 8.
	step1 := h.sibling(210).take(t, "--holder", "bench", "--consumer", "card-1.w1@1", "--priority", "high", "--needs", "cpu 4, memory 8")

	// CI arrives taking all cpu 4, memory 8 -> pauses step1.
	ci := h.sibling(310).take(t, "--holder", "ci", "--needs", "cpu 4, memory 8")

	// Step 1 finishes its in-flight command while CI is still running!
	step1.release(true)

	// Status still reports card-1.w1@1 as paused between commands!
	st := h.sibling(410)
	require.Equal(t, 0, run([]string{"status", "--json", "--dir", h.dir}, st.w))
	var sj statusJSON
	require.NoError(t, json.Unmarshal(st.out.Bytes(), &sj))
	assert.True(t, sj.AnyPaused)
	assert.Equal(t, []string{"card-1.w1@1"}, sj.PausedConsumers)

	// A new waiter tries --nowait for cpu 1 even if CI only held cpu 2 -> refused because AnyPaused!
	waiter := h.sibling(510)
	assert.Equal(t, 1, run([]string{"--dir", h.dir, "--nowait", "--needs", "cpu 1, memory 1", "--", "true"}, waiter.w))

	// Step 2 of card-1.w1@1 starts while CI runs; when CI releases on sleep 1, Step 2 unpauses and runs!
	step2 := h.sibling(211)
	h.c.onSleep = func(n int) {
		if n == 1 {
			ci.release(false)
		}
	}
	assert.Equal(t, 0, run([]string{"--dir", h.dir, "--holder", "bench", "--consumer", "card-1.w1@1", "--priority", "high", "--needs", "cpu 4, memory 8", "--", "cargo", "test"}, step2.w))
	h.c.onSleep = nil
	assert.Equal(t, []string{"cargo", "test"}, step2.ran)
	assert.True(t, h.f.idle())
}

// --- Re-entrancy, OS steering, hold verb, CLI refusals -----------------------

func TestReentrantNestedCallExecsDirectlyWithoutDeadlocking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 2, 4, 2)

	// Outer exclusive run sets NOVA_RESERVE_DIR in child env; simulated nested run
	// targeting the same dir bypasses locking instead of self-deadlocking!
	h.w.exec = func(_ string, argv, env []string, lower bool) int {
		h.ran, h.ranEnv, h.ranLower = argv, env, lower
		nested := h.sibling(77)
		nested.envMap["NOVA_RESERVE_DIR"] = envLookup(env, "NOVA_RESERVE_DIR")
		assert.Equal(t, 0, run([]string{"--dir", h.dir, "--exclusive", "--nowait", "--", "true"}, nested.w))
		assert.Equal(t, []string{"true"}, nested.ran)
		assert.Empty(t, nested.err.String(), "re-entrant run prints no second HELD receipt")
		return 0
	}
	assert.Equal(t, 0, run([]string{"--dir", h.dir, "--exclusive", "--", "make"}, h.w))
	assert.True(t, h.ranLower)
	assert.Equal(t, "2", envLookup(h.ranEnv, "NOVA_RESERVE_CPUS"))
	assert.Equal(t, "2", envLookup(h.ranEnv, "GOMAXPROCS"))
	assert.Equal(t, "2", envLookup(h.ranEnv, "CARGO_BUILD_JOBS"))
	assert.Equal(t, "2", envLookup(h.ranEnv, "RAYON_NUM_THREADS"))
	assert.Equal(t, "2", envLookup(h.ranEnv, "CMAKE_BUILD_PARALLEL_LEVEL"))
	assert.True(t, h.f.idle())
}

func TestCIHolderKeepsNormalOSPriority(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 16, 64, 16)
	assert.Equal(t, 0, run([]string{"--dir", h.dir, "--holder", "ci", "--needs", "cpu 4, memory 8", "--", "go", "test"}, h.w))
	assert.False(t, h.ranLower, "CI holds must not lower OS priority")
	assert.Equal(t, "4", envLookup(h.ranEnv, "NOVA_RESERVE_CPUS"))
}

func TestHoldVerbPrintsHELDAndReleasesOnEOF(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 16, 32, 8)
	var sawHeld bool
	h.w.waitEOF = func(io.Reader) {
		st := h.sibling(99)
		require.Equal(t, 0, run([]string{"status", "--dir", h.dir}, st.w))
		sawHeld = strings.Contains(st.out.String(), "hold 1 state=held")
	}
	assert.Equal(t, 0, run([]string{"hold", "--dir", h.dir, "--needs", "cpu 8, memory 8"}, h.w))
	assert.True(t, sawHeld)
	assert.Equal(t, "HELD cpu=8 memory=8 cores=4 pid=42\n", h.out.String())
	assert.True(t, h.f.idle())
}

func TestDeadHoldLockIsIgnoredByStatusAndReapedByNextCaller(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 4, 8, 4)

	// Simulate a SIGKILLed holder: .lock and .json left on disk with NO kernel flock held!
	lp, jp := seqFiles(h.dir, "holds", 1)
	require.NoError(t, os.WriteFile(lp, nil, 0o666))
	rec := holdRecord{Seq: 1, PID: 42, Holder: "bench", Consumer: "dead@1", Label: "work", Priority: "normal", Needs: map[string]int{"cpu": 4, "memory": 8}, Cores: 4}
	b, _ := json.Marshal(rec)
	require.NoError(t, writeRecord(jp, b, 42))

	// Even though PID 42 is alive (PID-reuse simulation!), LOCK_SH succeeds so status sees 0 holds!
	require.Equal(t, 0, run([]string{"status", "--json", "--dir", h.dir}, h.w))
	var sj statusJSON
	require.NoError(t, json.Unmarshal(h.out.Bytes(), &sj))
	assert.Empty(t, sj.HeldSprint)
	assert.Empty(t, sj.SprintConsumers)

	// Next mutator reaps the dead files and acquires all 4 CPUs cleanly!
	h.out.Reset()
	assert.Equal(t, 0, run([]string{"--dir", h.dir, "--needs", "cpu 4, memory 8", "--nowait", "--", "make"}, h.w))
	assert.Equal(t, []string{"make"}, h.ran)
}

func TestRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		line string
	}{
		{[]string{}, "nova-reserve: no command: nova-reserve [flags] -- <cmd> [args...]; run: nova-reserve -h"},
		{[]string{"--needs", "", "--", "x"}, `nova-reserve: --needs wants a comma-separated resource/tag list like "cpu 4, memory 8", not ""; run: nova-reserve -h`},
		{[]string{"--needs", "cpu 0", "--", "x"}, `nova-reserve: --needs wants a comma-separated resource/tag list like "cpu 4, memory 8", not "cpu 0"; run: nova-reserve -h`},
		{[]string{"--needs", "cpu 2, cpu 4", "--", "x"}, `nova-reserve: --needs wants a comma-separated resource/tag list like "cpu 4, memory 8", not "cpu 2, cpu 4"; run: nova-reserve -h`},
		{[]string{"--wait", "-1s", "--", "x"}, `nova-reserve: --wait wants a duration above 0, like 90s or 15m, not "-1s"; run: nova-reserve -h`},
		{[]string{"--wait", "0s", "--", "x"}, `nova-reserve: --wait wants a duration above 0, like 90s or 15m, not "0s"; run: nova-reserve -h`},
		{[]string{"--nowait", "--wait", "1s", "--", "x"}, "nova-reserve: --nowait and --wait cannot both be given; run: nova-reserve -h"},
		{[]string{"--exclusive", "--needs", "cpu 2", "--", "x"}, "nova-reserve: --exclusive takes every resource; --needs does not apply; run: nova-reserve -h"},
		{[]string{"--exclusive=yes", "--", "x"}, `nova-reserve: --exclusive takes no value, not "yes"; run: nova-reserve -h`},
		{[]string{"--holder", "nope", "--", "x"}, `nova-reserve: --holder wants one of bench, friend, ci, person, not "nope"; run: nova-reserve -h`},
		{[]string{"--priority", "urgent", "--", "x"}, `nova-reserve: --priority wants one of low, normal, high, read, critical, blocker, not "urgent"; run: nova-reserve -h`},
		{[]string{"--bogus", "--", "x"}, "nova-reserve: unknown flag --bogus; run: nova-reserve -h"},
		{[]string{"status", "--wait", "1s"}, "nova-reserve status: status takes no --wait; it takes --json, --dir; run: nova-reserve status -h"},
		{[]string{"set"}, "nova-reserve set: set wants at least one of --cpu, --memory, --cores, --tags, --resources; run: nova-reserve set -h"},
		{[]string{"set", "--cpu", "2", "--exclusive"}, "nova-reserve set: set takes no --exclusive; it takes --cpu, --memory, --cores, --tags, --resources, --dir; run: nova-reserve set -h"},
		{[]string{"hold", "--", "x"}, `nova-reserve hold: hold takes no command, not "x"; run: nova-reserve hold -h`},
		{[]string{"status", "extra"}, `nova-reserve status: unexpected "extra"; a command goes after --; run: nova-reserve status -h`},
		{[]string{"--why", "", "--", "x"}, `nova-reserve: --why wants text, not ""; run: nova-reserve -h`},
		{[]string{"help", "nope"}, `nova-reserve help: help takes one of run, hold, measure, set, status, not "nope"; run: nova-reserve -h`},
		{[]string{"no-such-verb"}, `nova-reserve: unknown verb "no-such-verb"; verbs: run, hold, measure, set, status, help; a command goes after --: nova-reserve -- no-such-verb; run: nova-reserve help`},
	} {
		h := newHarness(t)
		h.dir = filepath.Join(h.dir, "never")
		assert.Equal(t, 2, run(c.args, h.w), "%q", c.args)
		assert.Equal(t, c.line+"\n", h.err.String(), "%q", c.args)
		assert.Empty(t, h.out.String(), "%q", c.args)
		assert.Nil(t, h.ran, "%q", c.args)
		assert.NoDirExists(t, h.dir, "%q: refusal touches nothing", c.args)
	}
}

func TestVerbHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"help"}, {"-h"}, {"--help"},
		{"help", "run"}, {"run", "-h"}, {"--needs", "cpu 2", "--help", "--", "x"},
		{"help", "hold"}, {"hold", "-h"}, {"hold", "--help"},
		{"help", "measure"}, {"measure", "-h"}, {"measure", "--help"},
		{"help", "set"}, {"set", "-h"}, {"set", "--help"},
		{"help", "status"}, {"status", "-h"}, {"status", "--help"},
	} {
		h := newHarness(t)
		h.dir = filepath.Join(h.dir, "never")
		assert.Equal(t, 0, run(args, h.w), "%q", args)
		assert.Contains(t, h.out.String(), "nova-reserve", "%q", args)
		assert.Empty(t, h.err.String(), "%q", args)
		assert.NoDirExists(t, h.dir, "%q", args)
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
		assert.Equal(t, c.code, run([]string{"--dir", h.dir, "--", c.name, "-j4"}, h.w))
		assert.Equal(t, c.line, h.err.String())
		assert.Nil(t, h.ran)
		assert.NoDirExists(t, h.dir)
	}
}

func TestDirAndPermissionRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	missing := filepath.Join(h.dir, "no-such-dir")
	assert.Equal(t, 2, run([]string{"status", "--dir", missing}, h.w))
	assert.Equal(t, "nova-reserve status: --dir "+missing+" is not a reservation directory (no reservation was taken there); run: nova-reserve status -h\n", h.err.String())

	h.err.Reset()
	gone := filepath.Join(h.dir, "no", "x")
	assert.Equal(t, 2, run([]string{"--dir", gone, "--why", "a b", "--", "make"}, h.w))
	assert.Regexp(t, `^nova-reserve: --dir .*: parent .* does not exist; run: mkdir -p .* && nova-reserve --dir .* --why 'a b' -- make\n$`, h.err.String())

	h.err.Reset()
	file := filepath.Join(h.dir, "f")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	assert.Equal(t, 2, run([]string{"--dir", file, "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: --dir "+file+" is a file, not a directory; run: ls -ld "+shellQuote(file)+"\n", h.err.String())

	if os.Geteuid() != 0 && runtime.GOOS != "windows" {
		h.err.Reset()
		ro := filepath.Join(h.dir, "ro")
		require.NoError(t, os.Mkdir(ro, 0o500))
		t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
		assert.Equal(t, 2, run([]string{"set", "--cpu", "2", "--memory", "4", "--dir", filepath.Join(ro, "r")}, h.w))
		assert.Contains(t, h.err.String(), "not writable by this user")
	}
}

func TestCorruptCapacityIsRefusedAndRepairedByMeasureOrSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, ensureLayout(h.dir, "--dir", "nova-reserve"))
	require.NoError(t, os.WriteFile(capPath(h.dir), []byte("not-json\n"), 0o644))

	assert.Equal(t, 2, run([]string{"--dir", h.dir, "--", "make"}, h.w))
	assert.Contains(t, h.err.String(), "invalid JSON")
	h.err.Reset()

	assert.Equal(t, 0, run([]string{"status", "--dir", h.dir}, h.w))
	assert.Contains(t, h.out.String(), "capacity=? # corrupt:")
	h.out.Reset()

	assert.Equal(t, 0, run([]string{"set", "--dir", h.dir, "--cpu", "4", "--memory", "8"}, h.w))
	assert.Contains(t, h.out.String(), "CAPACITY SET cpu=4 memory=8")
}

func TestEscAndShellQuote(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `two\swords\ttab\nnl\x07\\`, esc("two words\ttab\nnl\x07\\"))
	assert.Equal(t, "plain", esc("plain"))
	assert.Equal(t, "/var/tmp/x", shellQuote("/var/tmp/x"))
	assert.Equal(t, `'C:\jobs'`, shellQuote(`C:\jobs`))
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
	assert.Equal(t, "''", shellQuote(""))
}
