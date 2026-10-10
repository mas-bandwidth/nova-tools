package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolchainVersionDirReadsTheVersionOffTheLauncher is the darwin half of the hurt of
// 2026-09-18 in one assertion. On the M2 Air `/opt/homebrew/bin/go` is a symlink into
// `/opt/homebrew/Cellar/go/1.27.1/libexec/bin/go`, and inside the bare wall the card got
//
//	go: cannot find GOROOT directory: 'go' binary is trimmed
//
// because the tree the launcher resolves its runtime out of was not granted. The remedy
// measured by hand was `--read /opt/homebrew/Cellar/go/1.27.1`, and the VERSION in it is the
// machine's: naming the prefix would grant every version ever brewed, and pinning 1.27.1
// would be stale on the next `brew upgrade`. So the wall reads the version off the launcher
// the bench's own PATH finds, exactly as `readlink -f "$(command -v go)"` does.
//
// The test builds that shape under a temporary prefix, so it asserts the resolver on every
// platform and never the machine it happens to run on.
func TestToolchainVersionDirReadsTheVersionOffTheLauncher(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the versioned-prefix roots are a darwin shape and the launcher is a symlink")
	}
	prefix := filepath.Join(t.TempDir(), "Cellar", "go")
	real := filepath.Join(prefix, "1.27.1", "libexec", "bin")
	require.NoError(t, os.MkdirAll(real, 0o755))
	require.NoError(t, testbin.WriteExecutable(filepath.Join(real, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	// The launcher on PATH is a symlink into the tree, the way brew links one. The lookup
	// is the resolver's own parameter (the serial-tests ledger's way off: "a field on the
	// value under test"), so the test's PATH is an argument and the process's PATH stands.
	binDir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(real, "go"), filepath.Join(binDir, "go")))

	got, ok := toolchainVersionDir(prefix, "go", lookInDir(binDir))
	require.True(t, ok, "the launcher at %s resolved to no versioned directory under %s", filepath.Join(binDir, "go"), prefix)
	// The prefix as the resolver reports it: on a Mac a temp dir is under /var, a symlink
	// to /private/var, and both sides are resolved before they are compared.
	realPrefix, err := filepath.EvalSymlinks(prefix)
	require.NoError(t, err)
	want := filepath.Join(realPrefix, "1.27.1")
	assert.Equal(t, want, got, "the versioned root is %s, want %s (the version is read off the launcher, never guessed)", got, want)
	// A GO FROM SOMEWHERE ELSE NAMES NOTHING. This entry is brew's copy and only brew's: a
	// Go unpacked into ~/sdk or the distribution's /usr/bin/go is under another prefix and
	// must not drag a Cellar path that is not there onto the argv, which rule 5 refuses.
	other := t.TempDir()
	require.NoError(t, testbin.WriteExecutable(filepath.Join(other, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	got, ok = toolchainVersionDir(prefix, "go", lookInDir(other))
	assert.False(t, ok, "a go outside the prefix named the root %s; the versioned entry is brew's copy and only brew's", got)
	// AND A TOOL THAT IS NOT INSTALLED AT ALL names nothing, rather than a prefix.
	got, ok = toolchainVersionDir(prefix, "dotnet", lookInDir(t.TempDir()))
	assert.False(t, ok, "a tool that is not on PATH named the root %s", got)
}

// lookInDir is the test's own PATH: a lookup that answers with dir's copy of the tool, or
// with exec.ErrNotFound the way exec.LookPath answers for a PATH that holds no such name.
func lookInDir(dir string) func(string) (string, error) {
	return func(tool string) (string, error) {
		path := filepath.Join(dir, tool)
		if _, err := os.Stat(path); err != nil {
			return "", exec.ErrNotFound
		}
		return path, nil
	}
}

// TestToolchainRootsAreOneListPerOS holds the shape the wall depends on, on every platform:
// each OS's list resolves under a home of the test's own, a system root needs no home, and
// an OS the list does not speak for names nothing rather than another OS's layout.
func TestToolchainRootsAreOneListPerOS(t *testing.T) {
	t.Parallel()

	for _, goos := range ToolchainRootOSes() {
		names := ToolchainRootNames(goos)
		assert.NotEmpty(t, names, "%s: the one list names no toolchain root at all", goos)
		var home int
		for _, r := range ToolchainRootList(goos) {
			if r.Home() {
				home++
				assert.False(t, strings.HasPrefix(r.Name, "/"), "%s: %s is a home root and an absolute path at once", goos, r.Name)
				continue
			}
			assert.True(t, strings.HasPrefix(r.Name, "/"), "%s: the system root %s is not absolute", goos, r.Name)
		}
		assert.NotZero(t, home, "%s: no root is under HOME; the module cache always is", goos)
	}
	got := ToolchainRootNames("plan9")
	assert.Empty(t, got, "an OS the list does not speak for names %v; it names nothing", got)
	resolved := ToolchainRoots("plan9", t.TempDir())
	assert.Empty(t, resolved, "an OS the list does not speak for resolved %v; it names nothing", resolved)
	// AN EMPTY HOME NAMES NO HOME-RELATIVE ROOT: a relative root is a refusal and a root at
	// the filesystem's top is not a toolchain.
	for _, r := range ToolchainRoots(ThisOS(), "") {
		assert.False(t, r.Home(), "an empty home still named the home root %s at %s", r.Name, r.Path)
	}
}

// TestToolchainRootsResolveSymlinks: the grant is checked against the RESOLVED target on
// both wall bodies -- that is why ~/go/bin/go, a symlink into the sdk tree, still runs while
// a real binary in that directory is Permission denied -- so a root that is itself a symlink
// (`/opt/homebrew/opt/openjdk` points into the Cellar) has to reach the argv resolved, or
// the wall names a tree the card's runtime is not under.
func TestToolchainRootsResolveSymlinks(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlinked toolchain roots are a unix shape")
	}
	home := t.TempDir()
	real := filepath.Join(t.TempDir(), "real-sdk")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "go1.26.5", "bin"), 0o755))
	require.NoError(t, os.Symlink(real, filepath.Join(home, "sdk")))
	var found bool
	for _, r := range ToolchainRoots("linux", home) {
		if r.Name != "sdk" {
			continue
		}
		found = true
		want, err := filepath.EvalSymlinks(real)
		require.NoError(t, err)
		assert.Equal(t, want, r.Path, "the symlinked root ~/sdk reached the argv as %s, want the resolved %s", r.Path, want)
	}
	assert.True(t, found, "a symlinked ~/sdk was dropped; it is a directory and it is the card's toolchain")
}

// TestBenchGoBinFindsTheSdkGoTheUnitPathLacks is the mechanical sprint's hurt of 2026-10-02:
// the loop unit's PATH names no Go, so a card's bare `go` was "command not found" although
// the wall grants the sdk tree. BenchGoBin searches env.sh's own entries before the
// caller's PATH and names the directory the found `go` really lives in.
func TestBenchGoBinFindsTheSdkGoTheUnitPathLacks(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the bench layouts are links into the sdk tree")
	}
	exe := []byte("#!/bin/sh\nexit 0\n")
	// bench lays out a fake home: the sdk Go, and each of links as name -> target, both
	// home-relative.
	bench := func(t *testing.T, links map[string]string) string {
		home := t.TempDir()
		sdkBin := filepath.Join(home, "sdk", "go1.26.6", "bin")
		require.NoError(t, os.MkdirAll(sdkBin, 0o755))
		require.NoError(t, testbin.WriteExecutable(filepath.Join(sdkBin, "go"), exe, 0o755))
		for name, target := range links {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(home, name)), 0o755))
			require.NoError(t, os.Symlink(filepath.Join(home, target), filepath.Join(home, name)))
		}
		return home
	}
	other := t.TempDir()
	require.NoError(t, testbin.WriteExecutable(filepath.Join(other, "go"), exe, 0o755))
	cases := map[string]struct {
		links map[string]string
		path  string
		want  string // home-relative; "other" is the go on the caller's PATH; "" is none
	}{
		"a Mac bench: sdk/bin links into the sdk tree": {
			links: map[string]string{"sdk/bin/go": "sdk/go1.26.6/bin/go"}, path: "/usr/bin:/bin", want: "sdk/go1.26.6/bin"},
		"a linux bench: go/bin links into the sdk tree": {
			links: map[string]string{"go/bin/go": "sdk/go1.26.6/bin/go"}, path: "/usr/bin:/bin", want: "sdk/go1.26.6/bin"},
		"the sdk is searched before the caller's PATH": {
			links: map[string]string{"sdk/bin/go": "sdk/go1.26.6/bin/go"}, path: other, want: "sdk/go1.26.6/bin"},
		"no Go in the home: the caller's PATH": {path: other, want: "other"},
		"no Go anywhere":                       {path: "/nonexistent", want: ""},
	}
	// A relative entry names a directory by the working directory, which inside a card is
	// the card's own: it is never read, though it reaches a Go here.
	wd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(wd, other)
	require.NoError(t, err)
	cases["a relative PATH entry is never read"] = struct {
		links map[string]string
		path  string
		want  string
	}{path: rel, want: ""}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := bench(t, c.links)
			want := ""
			switch c.want {
			case "":
			case "other":
				want, _ = filepath.EvalSymlinks(other)
			default:
				want, _ = filepath.EvalSymlinks(filepath.Join(home, filepath.FromSlash(c.want)))
			}
			assert.Equal(t, want, BenchGoBin(runtime.GOOS, home, c.path))
		})
	}
}

// TestBenchPathIsTheWallsExecRootsMadeFindable is the fleet tooling probe of 2026-10-04: every
// up member's toolchain (go, dotnet, cargo, java, node, dart, elixir, bats) lives under ~/sdk,
// which the wall executes, and the child's PATH named none of it, so a card found only what
// the loop unit's PATH held -- on one bench a stale /usr/local/bin/go that go.mod refused.
// BenchPath is the exec'd home roots' bin directories and then GOROOT/bin, read off the same
// list the wall's argv is built from, ahead of anything the member's PATH holds.
func TestBenchPathIsTheWallsExecRootsMadeFindable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the bench layouts are links into the sdk tree")
	}
	exe := []byte("#!/bin/sh\nexit 0\n")
	home := t.TempDir()
	goroot := filepath.Join(home, "sdk", "go1.26.6", "bin")
	sdkBin := filepath.Join(home, "sdk", "bin")
	require.NoError(t, os.MkdirAll(goroot, 0o755))
	require.NoError(t, os.MkdirAll(sdkBin, 0o755))
	require.NoError(t, testbin.WriteExecutable(filepath.Join(goroot, "go"), exe, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(goroot, "go"), filepath.Join(sdkBin, "go")))
	for _, tool := range []string{"dotnet", "cargo"} {
		require.NoError(t, testbin.WriteExecutable(filepath.Join(sdkBin, tool), exe, 0o755))
	}
	stale := t.TempDir() // the member's PATH: a Go of its own, which comes after the sdk's
	require.NoError(t, testbin.WriteExecutable(filepath.Join(stale, "go"), exe, 0o755))
	realGoroot, err := filepath.EvalSymlinks(goroot)
	require.NoError(t, err)

	for _, goos := range ToolchainRootOSes() {
		t.Run(goos, func(t *testing.T) {
			assert.Equal(t, []string{sdkBin, realGoroot}, BenchPath(goos, home, stale))
			// derived from the roots, not restated: every exec'd home root's bin is on it
			for _, r := range ToolchainRootList(goos) {
				if r.Exec && r.Home() {
					assert.Contains(t, BenchToolBins(goos, home), filepath.Join(home, filepath.FromSlash(r.Name), "bin"), "root %s", r.Name)
				}
			}
		})
	}
	// a bench with no sdk: nothing but the Go the member's PATH holds
	bare := t.TempDir()
	staleReal, err := filepath.EvalSymlinks(stale)
	require.NoError(t, err)
	assert.Equal(t, []string{staleReal}, BenchPath(runtime.GOOS, bare, stale))
	assert.Nil(t, BenchPath(runtime.GOOS, bare, "/nonexistent"))
	assert.Nil(t, BenchPath(runtime.GOOS, "", "/nonexistent"), "no home names no home-relative root")
}
