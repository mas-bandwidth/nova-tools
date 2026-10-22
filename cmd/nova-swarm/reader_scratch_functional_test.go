//go:build unix && functional

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
)

// The two reader-scratch tests that run a real program: the built fake harness through
// nativeRun, and a child inside the real wall. They are functional by the house rule
// (docs/TESTING.md, "The two tiers"); the wiring they share with the unit tier (where a
// read's scratch is, what the child's environment names, how a wall refusal ends) is pinned
// in reader_scratch_test.go with nothing started.

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
