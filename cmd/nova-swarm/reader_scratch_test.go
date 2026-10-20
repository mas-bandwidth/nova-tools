//go:build unix

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
		assert.True(t, sameTmp(got[0], want, ""), "%s is %q, want the job's scratch %q", name, got[0], want)
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

// TestALongTmpLeavesRoomForASocket: a linux child's TMPDIR leaves room for a unix socket
// below it (nativeShortTmp). vng makes its virtiofs socket at $TMPDIR/virtme<8 random>, and
// a slot path of about 190 bytes put it past the 108 bytes a socket path holds, so the
// guest never started. The run hands the child a short link to its tmp, every one of
// TMPDIR, TMP and TEMP names it, and the run removes it when it ends.
func TestALongTmpLeavesRoomForASocket(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the short tmp name is linux's, where vng runs")
	}
	require.NoError(t, buildShared(), "building the binaries these tests run")
	root, _ := aSlot(t)
	slot := filepath.Join(root, strings.Repeat("rocketnet-linux-hard-killed-peer-never-stalls-receive.", 3)+"slot")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	base := t.TempDir()
	label := "long-tmp"
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{binary: builtHarness, sandbox: builtFakeSandbox, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second, shortTmpBase: base}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	want := filepath.Join(slot, "tmp", label)
	socket := "/virtme" + strings.Repeat("x", 8) // vng's socket name below TMPDIR
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		got := argvEnv(t, slot, name)
		require.Len(t, got, 1, "the child has %d %s entries, want 1: %v", len(got), name, got)
		assert.Less(t, len(got[0]+socket), unixSocketPathMax, "%s is %q (%d bytes): a socket below it is past the 108 bytes a socket path holds", name, got[0], len(got[0]))
		assert.True(t, sameTmp(got[0], want, base), "%s is %q, want the short link to the slot's tmp %q", name, got[0], want)
	}
	_, err := os.Lstat(argvEnv(t, slot, "TMPDIR")[0])
	assert.ErrorIs(t, err, os.ErrNotExist, "the short link outlived the run")
}

// TestTheShortTmpLinkIsOnlyEverItsOwn pins the link nativeShortTmp names: a tmp path with
// room for a socket is named as it is; a long one gets a link that resolves to it; a stale
// link to the same directory is made again; anything else at the name is refused and left
// as it is; and the link is removed only while it still names the run's directory.
func TestTheShortTmpLinkIsOnlyEverItsOwn(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	short := filepath.Join(base, "tmp")
	got, isShort := nativeShortTmp(base, short)
	assert.False(t, isShort, "a tmp path with room for a socket gets no link")
	assert.Equal(t, short, got)

	tmp := filepath.Join(base, strings.Repeat("a-long-slot-directory-name.", 8), "tmp", "label")
	require.NoError(t, os.MkdirAll(tmp, 0o755))
	name, isShort := nativeShortTmp(base, tmp)
	require.True(t, isShort, "a %d-byte tmp path gets a short link", len(tmp))
	assert.Equal(t, base, filepath.Dir(name), "the link is made in its base")
	other, _ := nativeShortTmp(base, tmp+"2")
	assert.NotEqual(t, name, other, "two tmp directories get two links")
	def, _ := nativeShortTmp("", tmp)
	assert.Equal(t, "/tmp", filepath.Dir(def), "an empty base is /tmp")
	assert.Less(t, len(def)+tmpSocketRoom, unixSocketPathMax, "the default link leaves room for a socket")

	require.NoError(t, linkShortTmp(name, tmp), "the link is made")
	assert.True(t, sameDir(name, tmp), "the link resolves to the tmp directory")
	require.NoError(t, linkShortTmp(name, tmp), "a stale link to the same directory is made again")

	unlinkShortTmp(name, tmp+"2")
	_, err := os.Lstat(name)
	require.NoError(t, err, "a link naming another directory is not this run's to remove")
	unlinkShortTmp(name, tmp)
	_, err = os.Lstat(name)
	assert.ErrorIs(t, err, os.ErrNotExist, "the run's own link is removed")

	write(t, name, "someone else's file")
	assert.Error(t, linkShortTmp(name, tmp), "a name taken by anything else is refused")
	unlinkShortTmp(name, tmp)
	raw, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, "someone else's file", string(raw), "what was at the name is left as it is")
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

	// The child is handed its tmp by a short link (nativeShortTmp), as a long slot's is: a
	// write through the link lands in the tmp, and a write beside the link, outside both the
	// job and its tmp, is refused like /tmp.
	base := t.TempDir()
	link := filepath.Join(base, "nv-link")
	require.NoError(t, linkShortTmp(link, tmp))
	script := `echo "tmpdir=$TMPDIR"; echo hi > "$TMPDIR/scratch"; echo tmp=$?
if ( : > /tmp/nova-swarm-outside ) 2>/dev/null; then echo deny=unexpected; else echo deny=denied; fi
if ( : > "$1/beside" ) 2>/dev/null; then echo beside=unexpected; else echo beside=denied; fi`
	cfg := nativeRunConfig{slotDir: slot, benchHome: t.TempDir(), benchOS: runtime.GOOS}
	argv := nativeSandboxArgv([]string{"/bin/sh", "-c", script, "sh", base}, cfg, data, jobDir, tmp)

	cmd := exec.Command(builtSandbox, argv...)
	cmd.Env = nativeChildEnvFrom([]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, data, jobDir, link, "", "", "", "", nil)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	got := out.String()
	t.Logf("walled read child:\n%s%s", got, errb.String())
	assert.Contains(t, got, "tmpdir="+link+"\n", "the wall hands the child its tmp by the short link it was given")
	assert.Contains(t, got, "tmp=0", "writing the read's scratch inside its job is allowed by the wall")
	assert.FileExists(t, filepath.Join(tmp, "scratch"), "the read's scratch landed in its job's tmp")
	assert.Contains(t, got, "deny=denied", "a write to /tmp outside the job is refused by the wall")
	assert.Contains(t, got, "beside=denied", "a write beside the short link, outside the job and its tmp, is refused by the wall")
	assert.NoFileExists(t, filepath.Join(base, "beside"))
}
