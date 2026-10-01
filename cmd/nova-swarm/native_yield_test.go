package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/yield"
)

// CI OVER A CARD'S CHILD (nova-tools#4293; Glenn 2026-10-01: "We really need to have CI
// winning over children, or we will have failed CIs non-stop across the fleet"). A
// member's launch is `nova-swarm native` started by nativeRunner.Start; native steps
// itself to yield.Nice before it starts the wall, so the harness and everything the
// harness runs inherit it, while the member's own loop stays where it is.

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
// (nativeRunner.Start: the built nova-swarm native, the real nova-sandbox where this OS
// has one, the fake harness) and reads the nice of the harness and of a process the
// harness starts. Both are yield.Nice, or this test's own nice when that is already
// lower (a run under `nice -n 19`): an unprivileged process can only step down. Removing the
// yieldNative call from cmdNative turns this red (both read 0 on a test at 0).
func TestACardsLaunchRunsBehindCI(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	if !yield.Supported {
		t.Skip("no setpriority on this OS; native refuses here (TestANativeRunThatCannotYieldIsRefused)")
	}
	tool, path := builtBinaries(t)
	own := psNice(t, os.Getpid())
	want := max(own, yield.Nice)

	dir := t.TempDir()
	slots := filepath.Join(dir, "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	write(t, filepath.Join(dir, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	r := &nativeRunner{
		self: tool, sprintBin: "nova-sprint", harness: builtHarness, model: "fake/fake-model", root: dir, slots: slots,
		resultsRoot: filepath.Join(dir, "results"), deadline: 30 * time.Second, tokens: "unmetered", stderr: &bytes.Buffer{},
		env: []string{"PATH=" + path},
	}
	// the real wall where this kernel has one (sandbox-exec, Landlock), so the step is
	// read through it; none: the launch runs unwalled and the step is still read
	if out, err := exec.Command(builtSandbox, "check").Output(); err != nil || !strings.HasPrefix(string(out), "CHECK OK") || strings.Contains(string(out), "backend=none") {
		t.Logf("no wall on this machine (%q, %v): the launch runs with --no-wall", out, err)
		r.noWall = true
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

// TestANativeRunThatCannotYieldIsRefused: a launch whose step behind CI fails is refused
// with the reason on its NATIVE REFUSED line, and that reason is the report the member's
// finish carries (nativeChild.Result), never "ended without a result". A step that works
// writes nothing and lets the run go on.
func TestANativeRunThatCannotYieldIsRefused(t *testing.T) {
	t.Parallel()
	var ok bytes.Buffer
	require.True(t, yieldNative(func() error { return nil }, &ok), "a step that works refuses nothing")
	assert.Empty(t, ok.String(), "a step that works writes nothing")

	var refused bytes.Buffer
	cause := errors.New("nice 15: operation not permitted")
	require.False(t, yieldNative(func() error { return cause }, &refused), "a step that fails is a refusal")
	assert.Contains(t, refused.String(), "NATIVE REFUSED: yield to CI: nice 15: operation not permitted")

	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	require.NoError(t, os.WriteFile(logPath, refused.Bytes(), 0o644))
	c := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: make(chan struct{})}
	res := c.Result()
	assert.False(t, res.Ran, "a refused launch ran nothing")
	assert.Contains(t, res.Report, "native refused: yield to CI: nice 15: operation not permitted", "the refusal's reason is the finish's report")
	fin, why := member.Judge(res, member.Push{None: "the child committed nothing"})
	assert.Equal(t, member.FinishFailed, fin, "a refused launch is a failed finish (%s)", why)
}

// TestAMemberOnAnOSWithNoSetpriorityNotesItOnce: the member's start prints one NOTE on an
// OS with no setpriority, naming it, and nothing where a launch can step behind CI.
func TestAMemberOnAnOSWithNoSetpriorityNotesItOnce(t *testing.T) {
	t.Parallel()
	assert.Empty(t, yieldNote(true, "linux"), "a supported OS has no note")
	note := yieldNote(false, "plan9")
	assert.True(t, strings.HasPrefix(note, "NOTE member: no setpriority on plan9:"), "the note names the OS: %q", note)
	assert.Contains(t, note, "refuses every card")
}
