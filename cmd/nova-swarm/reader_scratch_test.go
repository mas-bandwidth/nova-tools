package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// argvEnv reads one name out of the native-argv.log native writes into the slot.
func argvEnv(t *testing.T, slotDir, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(slotDir, "native-argv.log"))
	require.NoError(t, err, "the run recorded no native-argv.log")
	var got []string
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "env: "+name+"="); ok {
			got = append(got, v)
		}
	}
	return got
}

// A read's scratch is inside its job, so a reader that writes its scratch directory is
// inside the wall and publishes a verdict instead of being refused and spending a reader
// (the a-reader-writes-scratch-inside-its-job fault, 2026-10-11). This is the wiring: the
// read frame makes nativeTmpDir <job>/tmp, the child's TMPDIR, TMP and TEMP all name it,
// and it is made before the child starts.
func TestAReadsScratchIsInsideItsJob(t *testing.T) {
	t.Parallel()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	cfg, job, _, _ := stagedRead(t, cardcontract.Frame{})
	write(t, filepath.Join(cfg.root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	cfg.binary = builtHarness
	cfg.sandbox = builtFakeSandbox
	cfg.deadline = 30 * time.Second

	want := filepath.Join(job, "tmp")
	assert.True(t, sameDir(nativeTmpDir(cfg, job), want), "a read's scratch is %q, want %q", nativeTmpDir(cfg, job), want)

	var errOut bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "the read runs, got %d:\n%s", code, errOut.String())
	assert.True(t, sameDir(res.tmp, want), "the read's TMPDIR is %q, want %q", res.tmp, want)
	st, err := os.Stat(want)
	require.NoError(t, err, "the read's scratch %q was not made before the child started: %v", want, err)
	require.True(t, st.IsDir(), "the read's scratch %q is not a directory", want)

	// The child's environment names all three at the one scratch directory inside its job.
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		got := argvEnv(t, cfg.slotDir, name)
		require.Len(t, got, 1, "the child has %d %s entries, want 1: %v", len(got), name, got)
		assert.True(t, sameDir(got[0], want), "%s is %q, want the job's scratch %q", name, got[0], want)
	}
}

// A work card keeps the slot's own tmp/<label>: only a reader's scratch moved inside its
// job, and a work card's must stay outside the job directory admission git-inits (issue
// #460). A frame-less unit test is the same shape as a work card.
func TestAWorkCardsScratchIsTheSlotsTmp(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	read := nativeTmpDir(nativeRunConfig{slotDir: slot, label: "w.r1", frame: &cardcontract.Frame{Kind: "read"}}, filepath.Join(slot, "jobs", "w.r1"))
	assert.Equal(t, filepath.Join(slot, "jobs", "w.r1", "tmp"), read)
	work := nativeTmpDir(nativeRunConfig{slotDir: slot, label: "c.w1", frame: &cardcontract.Frame{Kind: "work"}}, filepath.Join(slot, "jobs", "c.w1"))
	assert.Equal(t, filepath.Join(slot, "tmp", "c.w1"), work)
	bare := nativeTmpDir(nativeRunConfig{slotDir: slot, label: "a-label"}, filepath.Join(slot, "jobs", "a-label"))
	assert.Equal(t, filepath.Join(slot, "tmp", "a-label"), bare)
}

// TestTheChildEnvNamesOneScratch: TMPDIR, TMP and TEMP all name the one directory the run
// hands the child, so a harness or library that reads any of the three by name writes
// inside the wall.
func TestTheChildEnvNamesOneScratch(t *testing.T) {
	t.Parallel()
	env := nativeChildEnvFrom([]string{"TMP=/caller", "TEMP=/caller", "TMPDIR=/caller"}, "data", "job", "scratch", "", "", "", "", nil)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		got, ok := lookup(env, name)
		require.True(t, ok, "the child was handed no %s", name)
		assert.Equal(t, "scratch", got, "%s = %q, want the one scratch directory", name, got)
	}
}

// TestAReadWallRefusalEndsAsTheReadersEnvironment pins the detection a read's wall refusal
// lands on: a read whose run log carries the wall's WALL line (swarm.WallRefusedLine's
// `WALL REFUSED ...` or swarm.WallLine's `WALL task=...`) ends as the reader's own
// ENVIRONMENT, never the work's verdict (member.EndEnvironment). This is the mapping at
// cmd/nova-swarm/member.go (`if c.read && nativeWall.Match(b)`); without it a wall that
// refused a write would still leave every read green and the fix would be a no-op. The same
// WALL line on a work card is never an environment end.
func TestAReadWallRefusalEndsAsTheReadersEnvironment(t *testing.T) {
	t.Parallel()
	logPath := filepath.Join(t.TempDir(), "native.log")
	// the two real lines the wall prints on a refused write (docs/SPEC-SANDBOX.md):
	// WallRefusedLine's `WALL REFUSED <what> <path> task=<id> step=<n>` and WallLine's
	// `WALL task=<id> path=<path> step=<n>`.
	write(t, logPath, "WALL REFUSED write /tmp/x task=w.r1 step=1\nWALL task=w.r1 path=/tmp/x step=1\n")
	readChild := &nativeChild{read: true, logPath: logPath}
	require.Equal(t, member.EndEnvironment, readChild.Result().End, "a read the wall refused ends in the reader's own environment, never a missing verdict")
	workChild := &nativeChild{read: false, logPath: logPath}
	require.NotEqual(t, member.EndEnvironment, workChild.Result().End, "the same WALL line on a work card is the work's wall end, never an environment")
}

// TestAReaderScratchIsInsideTheWallAndTmpIsRefused proves on the real wall the two halves the
// fix's tests must hold: a fake reader child's scratch inside its job ($TMPDIR, which the
// read frame puts at <job>/tmp) is writable inside the wall, and a write to /tmp outside the
// job is refused. This is the wall-native half in the shape of TestAChildDeletesItsJournal;
// the EndEnvironment mapping of a refusal is pinned by
// TestAReadWallRefusalEndsAsTheReadersEnvironment, and the re-ask-once by the member's
// read-environment-retry tests.
func TestAReaderScratchIsInsideTheWallAndTmpIsRefused(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the landlock wall; this wall test runs on linux")
	}
	require.NoError(t, buildShared(), "building the binaries these tests run")
	if out, err := exec.Command(builtSandbox, "check").Output(); err != nil || !strings.HasPrefix(string(out), "CHECK OK") || strings.Contains(string(out), "backend=none") {
		t.Skipf("no wall on this machine (%q, %v)", out, err)
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (docs/SPEC-SANDBOX.md rule 2)")
	}

	_, slot := aSlot(t)
	if got, err := filepath.EvalSymlinks(slot); err == nil {
		slot = got
	}
	jobDir := filepath.Join(slot, "jobs", "w.r1")
	data := filepath.Join(slot, "data")
	tmp := filepath.Join(slot, "tmp", "w.r1")
	for _, d := range []string{jobDir, data, tmp} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}

	script := `echo hi > "$TMPDIR/scratch"; echo tmp=$?
if ( : > /tmp/nova-swarm-outside ) 2>/dev/null; then echo deny=unexpected; else echo deny=denied; fi`
	cfg := nativeRunConfig{slotDir: slot, benchHome: t.TempDir(), benchOS: runtime.GOOS}
	argv := nativeSandboxArgv([]string{"/bin/sh", "-c", script, "sh", tmp}, cfg, data, jobDir, tmp)

	cmd := exec.Command(builtSandbox, argv...)
	cmd.Env = nativeChildEnvFrom([]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, data, jobDir, tmp, "", "", "", "", nil)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	got := out.String()
	t.Logf("walled read child:\n%s%s", got, errb.String())
	assert.Contains(t, got, "tmp=0", "writing the read's scratch inside its job is allowed by the wall")
	assert.FileExists(t, filepath.Join(tmp, "scratch"), "the read's scratch landed in its job's tmp")
	assert.Contains(t, got, "deny=denied", "a write to /tmp outside the job is refused by the wall")
}
