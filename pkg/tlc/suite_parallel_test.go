package tlc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The case pool of RunSuite (carried from nova-sprint deaeede0). These tests use no
// real time: cases are ordered by channels the test holds, and the clock is a
// settable fake the test moves.

// settableClock is a clock safe for the pool's workers; the test moves it with set.
type settableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *settableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *settableClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

var parallelEpoch = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

const parallelPassLog = "Model checking completed. No error has been found.\n1 states generated, 1 distinct states found\n"

// parallelFixture is a plan of n tiny passing cases, MCTest0..MCTest<n-1>, and the
// options of a suite over all of them with a fixed clock.
func parallelFixture(t *testing.T, n int) (Options, []Case, *settableClock) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "tla")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	plan := "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"
	cases := make([]Case, n)
	for i := range cases {
		name := fmt.Sprintf("MCTest%d", i)
		plan += fmt.Sprintf("%s.cfg\t%s.tla\tpass\t-\tcheck\ttiny\trequired\t-\n", name, name)
		cases[i] = Case{Config: name + ".cfg", Module: name + ".tla", Expected: "pass", Property: "-", Deadlock: "check", Group: "tiny", Gate: "required", Debt: "-"}
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".cfg"), []byte("SPECIFICATION Spec\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".tla"), []byte("---- MODULE "+name+" ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\n====\n"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, CasesFile), []byte(plan), 0o644))
	clock := &settableClock{now: parallelEpoch}
	o := Options{
		Root: root, Cases: cases, Jar: Jar{Path: "unused", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Java: "unused", JavaVer: "21", Out: t.TempDir(), Budget: time.Minute, Workers: 2,
		Platform: "linux-amd64", CPUs: 8, Selection: Selection{Group: "tiny", Shards: 1}, Clock: clock.Now,
	}
	return o, cases, clock
}

func parallelCase(r Run) int {
	i := -1
	_, _ = fmt.Sscanf(r.Config, "MCTest%d.cfg", &i)
	return i
}

func TestRunSuiteRunsUpToParallelCasesAtOnce(t *testing.T) {
	t.Parallel()
	o, cases, _ := parallelFixture(t, 8)
	o.Parallel = 4
	var mu sync.Mutex
	active, peak := 0, 0
	started := make(chan struct{}, len(cases))
	release := make(chan struct{})
	o.Exec = func(ctx context.Context, _ Run, log string) int {
		mu.Lock()
		active++
		peak = max(peak, active)
		mu.Unlock()
		started <- struct{}{}
		<-release
		_ = os.WriteFile(log, []byte(parallelPassLog), 0o644)
		mu.Lock()
		active--
		mu.Unlock()
		return ExitPass
	}
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() { res, err := RunSuite(o); done <- out{res, err} }()
	// Four cases are running at once, each held at the gate: a pool narrower than
	// four would never deliver the fourth start.
	for i := 0; i < 4; i++ {
		<-started
	}
	close(release)
	got := <-done
	require.NoError(t, got.err)
	require.False(t, got.res.Failed, "suite = %+v", got.res)
	require.Len(t, got.res.Records, len(cases))
	assert.Equal(t, 4, peak, "the pool is exactly Parallel wide")
	assert.Len(t, started, len(cases)-4, "every case ran once")
}

func TestRunSuiteRecordsPlanOrderWhenCasesFinishOutOfOrder(t *testing.T) {
	t.Parallel()
	o, cases, _ := parallelFixture(t, 4)
	o.Parallel = 4
	started := make(chan int, len(cases))
	gates := make([]chan struct{}, len(cases))
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	o.Exec = func(ctx context.Context, r Run, log string) int {
		i := parallelCase(r)
		started <- i
		<-gates[i]
		_ = os.WriteFile(log, []byte(parallelPassLog), 0o644)
		return ExitPass
	}
	reported := make(chan string)
	var callbacks []string
	o.OnCase = func(r Record) {
		callbacks = append(callbacks, r.Config) // OnCase runs on RunSuite's own goroutine
		reported <- r.Config
	}
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() { res, err := RunSuite(o); done <- out{res, err} }()
	for range cases {
		<-started
	}
	// Finish the cases last to first, each only once the one before has been reported.
	for i := len(cases) - 1; i >= 0; i-- {
		close(gates[i])
		assert.Equal(t, cases[i].Config, <-reported, "OnCase is called in completion order")
	}
	got := <-done
	require.NoError(t, got.err)
	require.Len(t, got.res.Records, len(cases))
	for i, r := range got.res.Records {
		assert.Equal(t, cases[i].Config, r.Config, "record %d is in plan order", i)
	}
	assert.Equal(t, []string{"MCTest3.cfg", "MCTest2.cfg", "MCTest1.cfg", "MCTest0.cfg"}, callbacks)
}

func TestRunSuiteBudgetExhaustionRecordsUnstartedCases(t *testing.T) {
	t.Parallel()
	o, cases, clock := parallelFixture(t, 4)
	o.Parallel = 2
	o.Budget = time.Minute
	started := make(chan int, len(cases))
	release := make(chan struct{})
	o.Exec = func(ctx context.Context, r Run, log string) int {
		started <- parallelCase(r)
		<-release
		_ = os.WriteFile(log, []byte(parallelPassLog), 0o644)
		return ExitPass
	}
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() { res, err := RunSuite(o); done <- out{res, err} }()
	// The first two cases start inside the budget; the budget then ends while they run.
	ran := map[int]bool{<-started: true, <-started: true}
	clock.set(parallelEpoch.Add(2 * time.Minute))
	close(release)
	got := <-done
	require.NoError(t, got.err)
	require.True(t, got.res.Failed, "budget exhaustion fails the suite")
	require.Len(t, got.res.Records, len(cases), "every chosen case has a record")
	assert.Empty(t, started, "no case started after the budget")
	for i, c := range cases {
		b, err := os.ReadFile(filepath.Join(o.Out, c.Config+".log"))
		require.NoError(t, err)
		if ran[i] {
			assert.Equal(t, "PASS", got.res.Records[i].Result, "%s ran inside the budget", c.Config)
			continue
		}
		assert.Equal(t, "TLC suite budget exhausted before starting this case\n", string(b), "%s", c.Config)
		assert.Equal(t, "FAIL", got.res.Records[i].Result, "%s never started", c.Config)
	}
}

func TestRunSuiteParallelOnePreservesExecutionOrder(t *testing.T) {
	t.Parallel()
	o, cases, _ := parallelFixture(t, 4)
	o.Parallel = 1
	var calls []string
	o.Exec = func(ctx context.Context, r Run, log string) int {
		calls = append(calls, r.Config)
		_ = os.WriteFile(log, []byte(parallelPassLog), 0o644)
		return ExitPass
	}
	res, err := RunSuite(o)
	require.NoError(t, err)
	require.False(t, res.Failed, "suite = %+v", res)
	require.Len(t, calls, len(cases))
	for i, c := range cases {
		assert.Equal(t, c.Config, calls[i], "call %d", i)
	}
}

func TestCheckParallelIsBoundedByHalfTheCPUs(t *testing.T) {
	t.Parallel()
	require.NoError(t, CheckParallel(1, 1), "one CPU still allows one case")
	require.NoError(t, CheckParallel(4, 8))
	for _, tc := range []struct{ parallel, cpus int }{{0, 8}, {5, 8}, {2, 1}, {-1, 4}} {
		err := CheckParallel(tc.parallel, tc.cpus)
		require.Error(t, err, "parallel=%d cpus=%d", tc.parallel, tc.cpus)
		assert.Contains(t, err.Error(), "parallel must be between 1 and")
	}
}
