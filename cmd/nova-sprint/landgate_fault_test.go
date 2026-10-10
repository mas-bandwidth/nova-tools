package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The gate classifies its failure before it reports: each bench fault text is the
// right kind and is never a red tree, and a FAIL line naming a test is a red tree.
// A test that failed because the bench could not run is the bench's fault, even
// when a FAIL line names it (docs/SPEC-SPRINT.md section 7).
func TestGateClassifiesEachFaultTextAndAFAILIsRed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		out  string
		red  bool
		kind string
		what string
	}{
		{"git exit status 128", "git ls-files from the repository root: exit status 128", false, "git",
			"git ls-files from the repository root: exit status 128"},
		{"not a git repository", "fatal: not a git repository (or any of the parent directories): .git", false, "git",
			"fatal: not a git repository (or any of the parent directories): .git"},
		{"ENOSPC", "write /tmp/x: no space left on device\nENOSPC", false, "disk", "write /tmp/x: no space left on device"},
		{"disk quota exceeded", "write /tmp/x: disk quota exceeded", false, "disk", "write /tmp/x: disk quota exceeded"},
		{"no space left", "write /tmp/x: no space left", false, "disk", "write /tmp/x: no space left"},
		{"a missing go toolchain", "go: no such toolchain go1.26.6", false, "tmp", "go: no such toolchain go1.26.6"},
		{"ssh exit status 255", "ssh: connect to host bench-1 port 22: Connection refused\nexit status 255", false, "ssh",
			"ssh: connect to host bench-1 port 22: Connection refused"},
		{"a copy that did not finish", "copy did not finish: the bench stopped reading", false, "copy",
			"copy did not finish: the bench stopped reading"},
		{"a FAIL line names a test", "--- FAIL: TestA (1.02s)\n    a_test.go:9: boom\nFAIL\nFAIL\tm/p\t1.1s", true, "",
			"--- FAIL: TestA (1.02s)"},
		{"a package FAIL line", "FAIL\tm/p\t1.1s", true, "", "FAIL\tm/p\t1.1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			red, kind, what := classifyGateOutput(tc.out)
			assert.Equal(t, tc.red, red, "red: %q", tc.out)
			assert.Equal(t, tc.kind, kind, "kind: %q", tc.out)
			assert.Equal(t, tc.what, what, "what is the first line: %q", tc.out)
		})
	}
	// The bench fault is checked first: a git fault behind a FAIL line is still the
	// bench's, and the base is never marked red for it.
	red, kind, _ := classifyGateOutput("--- FAIL: TestA (1.02s)\n    a_test.go:9: git ls-files: exit status 128\nFAIL\tm/p\t1.1s")
	assert.False(t, red, "a bench fault behind a FAIL line is not a red tree")
	assert.Equal(t, "git", kind)
}

// A bench fault steps the gate to the next slot of the hash ring and never marks
// the base red: every slot faulting defers the landing one tick with one judgment,
// and the base re-check sees the same fault, not a red base.
func TestABenchFaultStepsTheRingAndIsNeverARedBase(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("init")

	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)

	hosts := []string{"m1", "m2", "m3"}
	var asked []string
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked = append(asked, host)
		return "fatal: not a git repository", 1, nil
	}
	b.mu.Unlock()

	l := &lander{a: r.a, st: st, gateKey: "s1"}
	runs := gateRuns(false, nil)
	why, ran := l.benchGate(context.Background(), hosts, r.clone, runs, false)

	assert.True(t, ran, "the gate ran on benches")
	assert.Equal(t, "LAND DEFERRED stream=s1 faults=3", why, "every slot faulted: one tick's defer")
	require.ElementsMatch(t, []string{"m1", "m2", "m3"}, asked, "every ring slot is asked")

	// the faulted benches are marked, and one judgment per pass comes off it
	marks := r.a.benchFaultMarks(time.Now())
	for _, h := range hosts {
		assert.Contains(t, marks, h, "the faulted bench %s is marked", h)
	}
	var faultJudgments []sprint.Group
	for _, g := range r.inboxGroups() {
		if g.Kind == sprint.Judgment && strings.HasPrefix(g.Type, "every bench faulted") {
			faultJudgments = append(faultJudgments, g)
		}
	}
	require.Len(t, faultJudgments, 1, "one judgment 'every bench faulted: git'")
	assert.Equal(t, "every bench faulted: git", faultJudgments[0].Type)

	// the base re-check sees the same fault and never counts a red base
	require.NoError(t, os.WriteFile(filepath.Join(r.clone, "go.mod"), []byte("module bench\n"), 0o600))
	l.gatesBase("main")
	baseWhy, stop := l.treeGateBase(context.Background(), r.clone, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	assert.False(t, stop, "a bench fault stops no stream")
	assert.True(t, benchFaultWhy(baseWhy), "the base re-check says the bench fault: %q", baseWhy)
	l.locks().gateMu.Lock()
	_, cached := l.baseGateCache["deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"]
	fail := l.baseGateFails["deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"]
	l.locks().gateMu.Unlock()
	assert.False(t, cached, "a bench fault is not cached as a base verdict")
	assert.Nil(t, fail, "a bench fault counts no base failure")
}

// An ssh that cannot reach the first slot's bench answers bench.NoAnswer: the gate
// reports GATE FAULT kind=ssh and steps to the next slot, never ending the ring with
// an empty, non-run result.
func TestAnSSHBenchFaultStepsToTheNextSlotAndIsNotRed(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("init")

	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)

	hosts := []string{"m1", "m2"}
	first := benchRing("s1", hosts)[0]
	var asked []string
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked = append(asked, host)
		if host == first {
			return "ssh: connect to host " + host + " port 22: Connection refused", bench.NoAnswer, nil
		}
		return "", 0, nil
	}
	b.mu.Unlock()

	l := &lander{a: r.a, st: st, gateKey: "s1"}
	runs := gateRuns(false, nil)
	why, ran := l.benchGate(context.Background(), hosts, r.clone, runs, false)

	assert.True(t, ran, "the gate ran on the slot after the fault")
	assert.Empty(t, why, "an ssh bench fault is never a red tree")
	require.Len(t, asked, 2, "the faulted slot is left and the next is asked")
	assert.Equal(t, first, asked[0], "the slot the stream hashes to is asked first")
	assert.NotEqual(t, first, asked[1], "the next slot is the other bench")
}

// A bench that faulted on disk, tmp or git is skipped by the ring until its bound
// and shows its mark on its fleet row (where and the dashboard show it).
func TestAFaultedBenchIsSkippedForTheBoundAndShowsOnItsRow(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("init")
	for _, m := range []string{"m1", "m2"} {
		r.ok("fleet beat " + m + " --load 1 --cores 8")
		r.ok("fleet up " + m)
	}

	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)

	l := &lander{a: r.a, st: st, gateKey: "s1"}
	now := l.clock()
	// the record markFault writes, without the disk-guard the guard would not let a
	// unit test reach a host for
	r.a.recordBenchFault("m1", "disk", now)

	// skipped inside the bound, asked again after it
	assert.True(t, r.a.benchFaulted("m1", now), "the faulted bench is skipped now")
	assert.False(t, r.a.benchFaulted("m1", now.Add(benchFaultBound+time.Minute)), "the bound ends")
	assert.Contains(t, r.a.benchFaultMarks(now)["m1"], "bench-fault disk until ",
		"the row carries bench-fault <kind> until <t+15m>")

	// the ring the gate asks leaves the faulted bench out and keeps the other
	b := r.a.landState()
	b.mu.Lock()
	b.hostName = func() (string, error) { return "studio.local", nil }
	b.flight = &landFlight{}
	b.mu.Unlock()
	hosts, inLoop, _ := l.gateBenches(context.Background())
	require.True(t, inLoop, "the land loop sends the gate out")
	assert.NotContains(t, hosts, "m1", "the faulted bench is skipped by the ring")
	assert.Contains(t, hosts, "m2", "the bench that did not fault is asked")

	// and where shows it on the member's row
	var w whereView
	r.json("where", &w)
	require.Contains(t, w.Tables[sprint.Fleet], "m1")
	assert.Contains(t, w.Tables[sprint.Fleet]["m1"][benchFaultField], "bench-fault disk until ",
		"the fleet row carries the mark: %+v", w.Tables[sprint.Fleet]["m1"])
}
