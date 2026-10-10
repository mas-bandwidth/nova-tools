//go:build functional

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/yield"
)

// psNice is the nice of pid as ps reads it, the same reading the fake harness makes.
func psNice(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "ni=", "-p", strconv.Itoa(pid)).Output()
	require.NoError(t, err, "ps -o ni= -p %d", pid)
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err, "ps printed %q, not a nice value", out)
	return n
}

// TestACardsLaunchRunsBehindCI launches a trivial card through the member's own path
// (nativeRunner.Start: the built nova-worker native, the real nova-sandbox where this OS
// has one, the fake harness) and reads the nice of the harness and of a process the
// harness starts. Both are yield.Nice, or this test's own nice when that is already
// lower (a run under `nice -n 19`): an unprivileged process can only step down. Under the
// darwin wall, which forbids the child its own setpriority, native puts the child's whole
// group at childNice (19) from outside the wall (docs/SPEC-WORKER.md), so there both read 19.
// Unwalled, removing the yieldNative call from cmdNative turns this red (both read 0 on a
// test at 0).
func TestACardsLaunchRunsBehindCI(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	if !yield.Supported {
		t.Skip("no setpriority on this OS; native refuses here (TestANativeRunThatCannotYieldIsRefused)")
	}
	tool, path := builtBinaries(t)
	// the real wall where this kernel has one (sandbox-exec, Landlock), so the step is
	// read through it; none: the launch runs unwalled and the step is still read
	noWall := false
	if out, err := exec.Command(builtSandbox, "check").Output(); err != nil || !strings.HasPrefix(string(out), "CHECK OK") || strings.Contains(string(out), "backend=none") {
		t.Logf("no wall on this machine (%q, %v): the launch runs with --no-wall", out, err)
		noWall = true
	}
	own := psNice(t, os.Getpid())
	want := max(own, yield.Nice)
	if nativeNicesChild(runtime.GOOS, !noWall) {
		// the darwin wall: native lowers the child's group to childNice itself, so the
		// harness reads 19 whatever the step behind CI did (TestLowerChildPriorityPutsTheGroupAtNineteen)
		want = childNice
		t.Logf("the darwin wall: the child's group runs at %d; the step behind CI is read where the launch is unwalled (CI)", childNice)
	} else if own >= yield.Nice {
		// under `nice -n 19` the harness would read 19 with or without the step: this run
		// tests inheritance only. CI runs at nice 0, and there the step itself is read.
		t.Logf("this test runs at nice %d, at or above yield.Nice %d: the step behind CI is NOT tested by this run, only that the launch inherits; it is tested where the test runs below %d (CI, at 0)", own, yield.Nice, yield.Nice)
		assert.NotEqual(t, "true", os.Getenv("CI"), "CI runs this test at nice %d: the step behind CI goes untested there; CI legs run at 0", own)
	}

	dir := t.TempDir()
	slots := filepath.Join(dir, "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	write(t, filepath.Join(dir, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	r := &nativeRunner{
		self: tool, harness: builtHarness, model: "fake/fake-model", root: dir, slots: slots,
		resultsRoot: filepath.Join(dir, "results"), deadline: 30 * time.Second, tokens: "unmetered", stderr: &bytes.Buffer{},
		env: []string{"PATH=" + path}, noWall: noWall,
	}
	p := member.Packet{Card: "nice1", Kind: "work", Gen: 1, Attempt: 1, Epoch: 1, Brief: "FAKE-NICE\n"}
	ch, err := r.Start(p)
	require.NoError(t, err, "Start")
	c := ch.(*nativeChild)
	// the launch ends by itself: the harness writes one file and exits, and native's own
	// --deadline (30s) ends a stuck one, so no timer is needed here
	<-c.done
	log, _ := os.ReadFile(c.logPath)
	got := newestResult(c.results)
	require.NotEmpty(t, got, "the harness published no RESULT.md; native's log:\n%s", log)
	raw, err := os.ReadFile(got)
	require.NoError(t, err)
	read := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		n, err := strconv.Atoi(strings.TrimSpace(v))
		require.NoError(t, err, "RESULT.md line %q carries no nice value", line)
		read[k] = n
	}
	assert.Equal(t, want, read["harness"], "the card's harness runs at nice %d, want %d (yield.Nice %d, this test at %d)", read["harness"], want, yield.Nice, own)
	assert.Equal(t, want, read["grandchild"], "a process the harness started runs at nice %d, want %d", read["grandchild"], want)
}
