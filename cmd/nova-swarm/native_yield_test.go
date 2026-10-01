package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	if own >= yield.Nice {
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
	read, class := map[string]int{}, map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		if strings.HasSuffix(k, "_behind") {
			class[strings.TrimSuffix(k, "_behind")] = v
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		require.NoError(t, err, "RESULT.md line %q carries no nice value", line)
		read[k] = n
	}
	assert.Equal(t, want, read["harness"], "the card's harness runs at nice %d, want %d (yield.Nice %d, this test at %d)", read["harness"], want, yield.Nice, own)
	assert.Equal(t, want, read["grandchild"], "a process the harness started runs at nice %d, want %d", read["grandchild"], want)
	// past nice (the member passes --behind-ci): the background state on darwin, the idle
	// scope beside the runners on Linux, or, where neither can be had, one NOTE and nice alone
	if note := nativeBehindNote.FindSubmatch(log); note != nil {
		t.Logf("this machine has no idle class for the launch: %s", note[1])
		assert.NotEqual(t, "darwin", runtime.GOOS, "darwin always has the background state")
		return
	}
	for _, who := range []string{"harness", "grandchild"} {
		if runtime.GOOS == "darwin" {
			assert.Equal(t, "darwin_bg=1", class[who], "the card's %s is not in the background state", who)
		} else {
			assert.Contains(t, class[who], "/nova-card-nice1-", "the card's %s is not in its scope", who)
			// the wall (Landlock) gives the card no read of /sys: there the idle mark is
			// native's own reading of cpu.idle, which it made before the wall and which
			// would have been a NATIVE NOTE (above) had it not read 1
			if strings.HasSuffix(class[who], ";idle=unreadable") {
				t.Logf("the card's %s cannot read its cpu.idle inside the wall; native read it as 1 (no NOTE): %s", who, class[who])
				continue
			}
			assert.True(t, strings.HasSuffix(class[who], ";idle=1"), "the card's %s scope is not idle: %s", who, class[who])
		}
	}
}

// TestANativeRunThatCannotYieldIsTheMachinesRefusal: a launch whose step behind CI fails
// is refused with the reason on its NATIVE REFUSED line. The member reads that one
// refusal as a staging refusal (no child ran, the machine's fault): the finish is
// `staging refused: yield to CI: ...` and the sprint deals the card elsewhere, never a
// failed finish charged to the card. Every other NATIVE REFUSED line, and a yield line
// beside a NATIVE rc line, stays the card's, its reason in the report. A step that works
// writes nothing and lets the run go on.
func TestANativeRunThatCannotYieldIsTheMachinesRefusal(t *testing.T) {
	t.Parallel()
	var ok bytes.Buffer
	require.True(t, yieldNative(func() error { return nil }, &ok), "a step that works refuses nothing")
	assert.Empty(t, ok.String(), "a step that works writes nothing")

	var refused bytes.Buffer
	require.False(t, yieldNative(func() error { return errors.New("nice 15: operation not permitted") }, &refused), "a step that fails is a refusal")
	require.Contains(t, refused.String(), "NATIVE REFUSED: yield to CI: nice 15: operation not permitted")

	other := "NATIVE REFUSED: the harness binary /x is missing; run: nova-swarm native -h\n"
	rc := "NATIVE OK label=c1 job=/j tmp=/t rc=0 wall=1.00s sandbox=none card_sha256=a binary_sha256=b config=- harness=ok budget=unmetered\n"
	cases := []struct {
		name, log, staging, report string
	}{
		{"the yield refusal is a staging refusal", refused.String(), "yield to CI: nice 15: operation not permitted", "no child ran"},
		{"another refusal is the card's, its reason reported", other, "", "native refused: the harness binary /x is missing"},
		{"a yield line beside a NATIVE rc line is not one", refused.String() + rc, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "c1.native.log")
			require.NoError(t, os.WriteFile(logPath, []byte(c.log), 0o644))
			ch := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: make(chan struct{})}
			res := ch.Result()
			fin, why := member.Judge(res, member.Push{None: "the child committed nothing"})
			assert.Equal(t, member.FinishFailed, fin, "no result is never an ok finish (%s)", why)
			if c.staging != "" {
				assert.Equal(t, member.EndStaging, res.End, "the machine's refusal ends as staging refused")
				assert.True(t, strings.HasPrefix(res.Staging, c.staging), "staging text %q, want it to begin %q", res.Staging, c.staging)
				assert.True(t, strings.HasPrefix(why, member.EndStaging+": "+c.staging), "the finish's reason %q", why)
			} else {
				assert.NotEqual(t, member.EndStaging, res.End, "only the yield refusal, with no rc line, is the machine's")
			}
			assert.Contains(t, res.Report, c.report)
		})
	}
}

// TestAMemberOnAnOSWithNoSetpriorityRefusesToStart: on an OS with no setpriority the
// member will not start (native would refuse every card it took); where a launch can
// step behind CI there is no refusal.
func TestAMemberOnAnOSWithNoSetpriorityRefusesToStart(t *testing.T) {
	t.Parallel()
	assert.Empty(t, yieldRefusal(true, "linux"), "a supported OS starts")
	why := yieldRefusal(false, "plan9")
	assert.True(t, strings.HasPrefix(why, "no setpriority on plan9:"), "the refusal names the OS: %q", why)
	assert.Contains(t, why, "refuse every card")
}

// nativeBehindNote is native's one line where --behind-ci could not be had.
var nativeBehindNote = regexp.MustCompile(`(?m)^NATIVE NOTE behind-ci unavailable: (.+)$`)

// TestBehindCIIsNeverARefusal: past nice, a step that works writes nothing; one that
// cannot be had (no user manager, a refusal) writes one NATIVE NOTE with the reason and
// the card runs on at nice 15; the launch log of such a run is never read as a
// refusal by the member. A member says it once at its start where it is so.
func TestBehindCIIsNeverARefusal(t *testing.T) {
	t.Parallel()
	var done bytes.Buffer
	behindNative(func(string) string { return "" }, "c1", &done)
	assert.Empty(t, done.String(), "a step that works says nothing")

	var note bytes.Buffer
	asked := ""
	behindNative(func(label string) string {
		asked = label
		return "not under a systemd user manager (cgroup /system.slice/x.service)"
	}, "c1", &note)
	assert.Equal(t, "c1", asked, "the scope is named for the launch")
	assert.Equal(t, 1, strings.Count(note.String(), "\n"), "one line: %q", note.String())
	assert.Contains(t, note.String(), "NATIVE NOTE behind-ci unavailable: not under a systemd user manager")
	assert.Contains(t, note.String(), "runs at nice 15 only")

	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	require.NoError(t, os.WriteFile(logPath, note.Bytes(), 0o644))
	res := (&nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: make(chan struct{})}).Result()
	assert.NotEqual(t, member.EndStaging, res.End, "a note is not the machine's refusal")
	assert.NotContains(t, res.Report, "native refused", "a note is not a refusal")

	assert.Empty(t, behindStartNote(""), "a member that can go past nice says nothing")
	start := behindStartNote("no user bus at /run/user/1000/bus")
	assert.True(t, strings.HasPrefix(start, "NOTE member: behind-ci unavailable here: no user bus"), "%q", start)
}

// TestAMemberAlwaysAsksForBehindCI: every launch a member starts carries --behind-ci,
// so a card's tree goes past nice by mechanism; the self here is a script that writes
// the argv it was started with.
func TestAMemberAlwaysAsksForBehindCI(t *testing.T) {
	t.Parallel()
	r, _, marker := markerRunner(t)
	argv := marker + ".argv"
	require.NoError(t, os.WriteFile(r.self, []byte("#!/bin/sh\necho \"$@\" > '"+argv+"'\n"), 0o755))
	ch, err := r.Start(member.Packet{Card: "c1", Kind: "work", Gen: 1, Attempt: 1, Epoch: 7, Branch: "work/c1"})
	require.NoError(t, err)
	<-ch.(*nativeChild).done
	got, err := os.ReadFile(argv)
	require.NoError(t, err)
	assert.Contains(t, string(got), " --behind-ci", "the launch's argv: %s", got)
}
