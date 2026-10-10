package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE NATIVE OPENCODE PATH (issue #296, slice 2). A frozen run configuration is executed
// directly against the real fake harness, with no pool, no worker description and no
// supervisor: the binary, the model, the card text, the slot and the deadline are the whole
// truth. These tests run the same fake harness the dispatcher tests run, so the child that
// starts is the one whose argv, sleep and exit the machinery already trusts.

func nativeHarness(t *testing.T) string {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	return builtHarness
}

// TestNativeArgvSkipsAToolchainRootThatIsNotThere: rule 5 of the wall REFUSES a --read
// naming a path that does not exist, so a bench without the standard's layout -- a darwin
// bench has no ~/sdk -- loses the root rather than refusing the run.
// TestNativeArgvReadsTheDarwinToolchainRoots is the darwin face of the same edge, measured
// on the M2 Air 2026-09-18: a Mac's toolchains are INSTALLED and on PATH, and three of them
// still died inside the bare wall because each resolves its runtime from the directory of
// the launcher that ran it, and that launcher is a symlink out of any granted tree --
// `go: cannot find GOROOT directory: 'go' binary is trimmed`, `dotnet: Failed to resolve
// full path of the current executable []`, `java: Unable to locate a Java Runtime`. The
// remedy measured by hand was `--read /opt/homebrew/Cellar/go/1.27.1`, and the wall now
// names that tree itself, with the version read off the launcher.
//
// It runs ON a Mac, because what it asserts is that THIS bench's own installed toolchain
// reaches the argv; the per-OS list itself is held by the class test in internal/ci on every
// platform.
func TestNativeArgvReadsTheDarwinToolchainRoots(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skip("the darwin toolchain roots are this bench's own installs; asserted on a Mac")
	}
	var system []swarm.ToolchainRoot
	for _, r := range swarm.ToolchainRoots("darwin", os.Getenv("HOME")) {
		if !r.Home() {
			system = append(system, r)
		}
	}
	if len(system) == 0 {
		t.Skip("this Mac has none of the darwin system toolchains installed")
	}
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	cfg := nativeRunConfig{slotDir: slot, benchHome: t.TempDir(), benchOS: "darwin"}
	argv := nativeSandboxArgv([]string{bin}, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	for _, r := range system {
		// Every darwin system root is a RUNTIME the card runs, so every one of them is the
		// exec-carrying kind -- and each reaches the argv RESOLVED, because the grant is
		// checked against the resolved target and `/opt/homebrew/opt/openjdk` is itself a
		// symlink into the Cellar.
		assert.True(t, r.Exec, "the darwin system root %s is granted without execute; it is a runtime the card runs", r.Name)
		assert.True(t, filepath.IsAbs(r.Path), "the darwin root %s resolved to %s, which is not a toolchain tree", r.Name, r.Path)
		assert.False(t, strings.HasSuffix(r.Path, string(filepath.Separator)+"bin"), "the darwin root %s resolved to %s, which is not a toolchain tree", r.Name, r.Path)
		assert.True(t, hasFlagPair(argv, "--read", r.Path), "the wall argv does not carry the darwin toolchain root %s (%s) as --read:\n%s", r.Name, r.Path, strings.Join(argv, " "))
		assert.False(t, hasFlagPair(argv, "--write", r.Path), "the darwin toolchain root %s is a WRITE; it is read-only:\n%s", r.Path, strings.Join(argv, " "))
	}
	// AND NEVER A DIRECTORY OF LAUNCHERS. `/opt/homebrew/bin` holds a symlink for every
	// formula on the machine and brew writes it; the grant is on the Cellar tree the runtime
	// lives in, and naming the bin directory as a toolchain root is the widening the
	// security read of #1364 refused on ~/go/bin.
	for _, r := range swarm.ToolchainRootList("darwin") {
		assert.False(t, strings.HasSuffix(r.Name, "/bin"), "the darwin list names the launcher directory %s as a toolchain root", r.Name)
	}
}

func hasFlagPair(argv []string, flag, val string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) && argv[i+1] == val {
			return true
		}
	}
	return false
}

// aSlot returns a slot dir and the root it is under, both fresh.
func aSlot(t *testing.T) (root, slot string) {
	t.Helper()
	root = t.TempDir()
	slot = filepath.Join(root, "slot-1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	return root, slot
}

// A native run removes its own slot temp (<slot>/tmp/<label>) through safepath.RemoveUnder
// when the slot lease ends: the directory the card was handed as TMPDIR is gone, and a path
// outside the slot's tmp is refused (docs/SPEC-SWARM.md, native).
func TestNativeRemovesItsSlotTemp(t *testing.T) {
	t.Parallel()
	_, slot := aSlot(t)
	tmp := filepath.Join(slot, "tmp", "card")
	require.NoError(t, os.MkdirAll(tmp, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x"), []byte("x"), 0o644))
	require.NoError(t, removeNativeSlotTemp(slot, tmp), "the slot temp is removed when the slot lease ends")
	assert.NoDirExists(t, tmp, "the slot temp is removed when the slot lease ends")
	// a path outside the slot's tmp is refused, and nothing is removed
	outside := filepath.Join(slot, "data")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.Error(t, removeNativeSlotTemp(slot, outside), "a path outside the slot's tmp is refused")
	assert.DirExists(t, outside, "a path outside the slot's tmp is never removed")
}

func TestNativeGOTMPDIRIsTheRunsOwnTemp(t *testing.T) {
	t.Parallel()
	env := nativeChildEnvFrom([]string{"GOTMPDIR=/foreign", "TMPDIR=/foreign"}, "data", "job", "run-tmp", "", "", "", "", nil)
	assert.Contains(t, env, "TMPDIR=run-tmp")
	assert.Contains(t, env, "GOTMPDIR=run-tmp")
	assert.NotContains(t, env, "GOTMPDIR=/foreign")
}

func TestNativeRefusalRemovesTempBeforeReleasingTheSlot(t *testing.T) {
	t.Parallel()
	_, slot := aSlot(t)
	job := filepath.Join(slot, "jobs", "card")
	require.NoError(t, os.MkdirAll(job, 0o755))
	releaseJob, err := swarm.StartJobLease(job, "card")
	require.NoError(t, err)
	releaseSlot, err := swarm.StartSlotLease(slot, "card")
	require.NoError(t, err)
	tmp := filepath.Join(slot, "tmp", "card")
	require.NoError(t, os.MkdirAll(tmp, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "old"), []byte("old"), 0o644))

	// Once the slot is released, a successor may immediately create the same temp path.
	// The predecessor must not remove that successor's files afterward.
	releaseAndStartSuccessor := func() {
		releaseSlot()
		require.NoError(t, os.MkdirAll(tmp, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "successor"), []byte("new"), 0o644))
	}
	require.NoError(t, releaseNativeTemp(slot, tmp, releaseAndStartSuccessor, releaseJob))
	assert.NoFileExists(t, filepath.Join(tmp, "old"))
	assert.FileExists(t, filepath.Join(tmp, "successor"))
}

// A run refused after its slot temp was made removes that temp: the shell shim directory is
// made after <slot>/tmp/<label>, and a run whose shim cannot be written refuses with the slot
// temp gone, so a refusal does not leave a temp directory behind (docs/SPEC-SWARM.md, native).
func TestANativeRefusalAfterItsSlotTempIsMadeRemovesIt(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	require.NoError(t, os.WriteFile(filepath.Join(slot, "shim"), []byte("not a directory\n"), 0o644), "the shim path could not be made a file")
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
	}, &errOut)
	require.Equal(t, 2, code, "a run whose shim cannot be written exits 2, got %d:\n%s", code, errOut.String())
	require.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errOut.String())
	assert.NoDirExists(t, filepath.Join(slot, "tmp", "card"), "the refused run left its slot temp behind:\n%s", errOut.String())
}

// TestNativeRunRefusesMissingBinary: a binary that does not exist, and one that exists but
// is not executable, are both the refusal that runs before any child can start.
func TestNativeRunRefusesMissingBinary(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	for _, tc := range []struct {
		name   string
		binary string
		word   string
	}{
		{"missing", filepath.Join(t.TempDir(), "no-such-binary"), "missing"},
		{"not_executable", func() string {
			p := filepath.Join(t.TempDir(), "data")
			require.NoError(t, os.WriteFile(p, []byte("not a program\n"), 0o644))
			return p
		}(), "not executable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: tc.binary, model: "fake/fake-model", label: "lbl",
				card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
			}, &errOut)
			require.Equal(t, 2, code, "a bad binary exits 2, got %d:\n%s", code, errOut.String())
			require.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errOut.String())
			require.Contains(t, errOut.String(), tc.word, "the refusal names its reason (%s):\n%s", tc.word, errOut.String())
			got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1
			require.Equal(t, 1, got, "exactly one REFUSED line, got %d:\n%s", got, errOut.String())
		})
	}
}

// TestNativeRunAuthCopyIs0600: the named provider's entry is copied from the auth file into
// the data home, mode 0600, and no other provider's entry travels with it. Asked of
// copyAuth itself: the run that carries the copy removes it when the card ends, so after a
// run there is nothing left to stat (TestNativeAuthCopyIsGoneAfterTheRun).
func TestNativeRunAuthCopyIs0600(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	src := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(src, []byte(`{"fake":"the-fake-secret","other":"the-other-secret"}`), 0o600))
	dataHome := t.TempDir()
	reason := copyAuth(src, "fake", dataHome)
	require.Empty(t, reason, "an 0600 auth source copies, got the refusal: %s", reason)
	for _, copied := range []string{
		filepath.Join(dataHome, "auth.json"),
		filepath.Join(dataHome, "opencode", "auth.json"),
	} {
		st, err := os.Stat(copied)
		require.NoError(t, err, "the auth copy was not written")
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "the auth copy is mode %04o, want 0600", st.Mode().Perm())
		body, err := os.ReadFile(copied)
		require.NoError(t, err)
		assert.Equal(t, `{"fake":"the-fake-secret"}`, string(body), "the copy holds only the named provider entry, got %s", body)
	}
}

// TestWallNamedDecodesTheProducersEscapedCwd is the unit half of issue #572: the wall writes
// `cwd=` through oneline.Field (cmd/nova-sandbox/main.go), so the job directory reaches this
// side with its spaces escaped (`worker 2` -> `worker\x202`). wallNamed must decode that
// field back to the path the producer held before it names the directory the child ran in.
// The receipt is built by the producer's own encoder, not hard-coded unescaped.
func TestWallNamedDecodesTheProducersEscapedCwd(t *testing.T) {
	t.Parallel()

	dir := "/workspace/test dir 2/.scratch/tools/runs/1/jobs/terminology"
	line := "SANDBOX OK backend=sandbox-exec abi=- read=3 write=2 net=nopromise cwd=" +
		oneline.Field(dir) + " ancestors=17 cmd=opencode\n"
	backend, cwd, reason := wallNamed(line)
	require.Empty(t, reason, "wallNamed did not read the SANDBOX OK line (%s): %q", reason, line)
	assert.Equal(t, "sandbox-exec", backend, "wallNamed's backend is %q, want sandbox-exec", backend)
	assert.Equal(t, dir, cwd, "wallNamed's cwd is %q, want the decoded path %q", cwd, dir)
}

// THE WALL RULES OF THE NATIVE RUN (slice 11, lesson 11). A frozen run configuration gains
// two lists: repos (repositories a card may clone) and recipients (bus lanes a card may
// address, default none). The native run passes them to the sandbox layer as allow rules:
// a repo is network to github.com only, and it is a HOST rule -- a wall that cannot express
// it refuses rather than running unwalled -- while the recipients are never expressed, and
// a bus send is denied by the wall by construction (no nova-bus on PATH, no bus checkout in
// the write set).

// ISSUE #915 (windows leg): the legacy --auth native path refuses an auth source looser than
// 0600 and a copy that does not end 0600 (copyAuth). NTFS carries no unix permission bits --
// os.Stat reports 0666 for every readable file there (0444 when it is read-only) -- so on
// windows-latest a source the test wrote 0600 read 0666 and the check refused it: the legacy
// shape, TestNativeAuthWithAWorkerNamesItsLegacyCopy, died exit 2 on the line that says it
// runs. This is the same class as the execute bit (executable.go) and the key file mode
// (internal/swarm/key.go, which already answers nothing on windows). The rules now ask the
// platform, and because they are written where every platform compiles them, darwin and linux
// hold the windows answer to this contract.
func TestAuthModeRulesAskThePlatform(t *testing.T) {
	t.Parallel()

	// The source question: is any group or other bit set? The answer is "no" on windows,
	// however the file reads, because the bits do not exist there.
	for _, tc := range []struct {
		name string
		goos string
		mode os.FileMode
		want bool
	}{
		{"windows_0600", "windows", 0o600, false},
		{"windows_0644", "windows", 0o644, false},
		{"windows_0666", "windows", 0o666, false},
		{"linux_0600", "linux", 0o600, false},
		{"linux_0400", "linux", 0o400, false},
		{"linux_0644", "linux", 0o644, true},
		{"linux_0666", "linux", 0o666, true},
		{"darwin_0600", "darwin", 0o600, false},
		{"darwin_0604", "darwin", 0o604, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := authModeWiderThanOwner(tc.goos, tc.mode)
			require.Equal(t, tc.want, got, "authModeWiderThanOwner(goos=%s, mode=%04o) = %v, want %v; a file written 0600 reads 0666 on windows, so the bits are not a refusal there (#915)", tc.goos, tc.mode, got, tc.want)
		})
	}
	// The copy's own question: did the write end exactly 0600? Windows reports 0666 for
	// every readable file, so the copy cannot be shown owner-only and is not refused.
	for _, tc := range []struct {
		name string
		goos string
		mode os.FileMode
		want bool
	}{
		{"windows_0600", "windows", 0o600, false},
		{"windows_0666", "windows", 0o666, false},
		{"linux_0600", "linux", 0o600, false},
		{"linux_0644", "linux", 0o644, true},
		{"linux_0666", "linux", 0o666, true},
	} {
		t.Run("copy_"+tc.name, func(t *testing.T) {
			got := authModeNotOwnerOnly(tc.goos, tc.mode)
			require.Equal(t, tc.want, got, "authModeNotOwnerOnly(goos=%s, mode=%04o) = %v, want %v (#915)", tc.goos, tc.mode, got, tc.want)
		})
	}
}

// codex-review's hold on #2806: the card owns the data home while it runs, so it can chmod
// dataHome and dataHome/opencode 0555 and an unlink there fails. The cleanup takes the write
// bit back and removes both copies; a copy it still cannot remove is returned, and the run
// fails on it rather than printing a NOTE.
func TestRemoveAuthCopySurvivesAReadOnlyDataHome(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not gate unlink on windows")
	}
	dataHome := t.TempDir()
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	reason := copyAuth(auth, "fake", dataHome)
	require.Empty(t, reason, "copyAuth refused: %s", reason)
	oc := filepath.Join(dataHome, "opencode")
	for _, d := range []string{oc, dataHome} {
		require.NoError(t, os.Chmod(d, 0o555))
	}
	t.Cleanup(func() { _ = os.Chmod(dataHome, 0o755); _ = os.Chmod(oc, 0o755) })
	var errOut bytes.Buffer
	left := removeAuthCopy(dataHome, &errOut)
	require.Empty(t, left, "a read-only data home kept the auth copy %v:\n%s", left, errOut.String())
	for _, p := range []string{filepath.Join(dataHome, "auth.json"), filepath.Join(oc, "auth.json")} {
		_, err := os.Lstat(p)
		assert.True(t, os.IsNotExist(err), "%s survived the cleanup of a read-only data home", p)
	}
}

// A copy the cleanup cannot remove at all is named, never swallowed: here the card replaced
// opencode/auth.json with a non-empty directory, which an unlink cannot take.
func TestRemoveAuthCopyNamesACopyItCannotRemove(t *testing.T) {
	t.Parallel()

	dataHome := t.TempDir()
	stuck := filepath.Join(dataHome, "opencode", "auth.json")
	require.NoError(t, os.MkdirAll(stuck, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stuck, "key"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dataHome, "auth.json"), []byte("{}"), 0o600))
	var errOut bytes.Buffer
	left := removeAuthCopy(dataHome, &errOut)
	require.Len(t, left, 1, "the cleanup should name exactly %s as left, got %v", stuck, left)
	require.Equal(t, stuck, left[0], "the cleanup should name exactly %s as left, got %v", stuck, left)
	_, err := os.Lstat(filepath.Join(dataHome, "auth.json"))
	assert.True(t, os.IsNotExist(err), "the removable copy was left beside the stuck one")
	mustContain(t, "the cleanup's NOTE", errOut.String(), "could not be removed")
}

// TestNativeSandboxArgvNamesEachDirectoryOnce: the shared cache root is ONE directory for the
// whole bench, and the wall argv names it once. It used to go in twice -- as the Go caches'
// write (card 8963) and again as the shared cache root (#1048), which are the same
// <root>/cache -- so every card's darwin profile carried two identical WRITE grants and two
// identical socket grants. Measured 2026-10-02 the profile's size is not the wall's cost on
// any Mac bench; this is the duplicate, removed, not a speed-up.
func TestNativeSandboxArgvNamesEachDirectoryOnce(t *testing.T) {
	t.Parallel()
	root, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "once")
	cfg := nativeRunConfig{slotDir: slot, root: root, benchHome: t.TempDir(), benchOS: "linux"}
	argv := nativeSandboxArgv([]string{"/bin/true"}, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "once"))
	seen := map[string]int{}
	for i, a := range argv {
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "--") && i+1 < len(argv) && filepath.IsAbs(argv[i+1]) {
			seen[a+" "+argv[i+1]]++
		}
	}
	for pair, n := range seen {
		assert.Equal(t, 1, n, "the wall argv names %q %d times:\n%s", pair, n, strings.Join(argv, " "))
	}
	assert.Equal(t, 1, seen["--write "+swarm.CacheRoot(root)], "the shared cache root is not a --write exactly once:\n%s", strings.Join(argv, " "))
}
