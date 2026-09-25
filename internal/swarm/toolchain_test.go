package swarm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	if runtime.GOOS == "windows" {
		t.Skip("the versioned-prefix roots are a darwin shape and the launcher is a symlink")
	}
	prefix := filepath.Join(t.TempDir(), "Cellar", "go")
	real := filepath.Join(prefix, "1.27.1", "libexec", "bin")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The launcher on PATH is a symlink into the tree, the way brew links one.
	binDir := t.TempDir()
	if err := os.Symlink(filepath.Join(real, "go"), filepath.Join(binDir, "go")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	got, ok := toolchainVersionDir(prefix, "go")
	if !ok {
		t.Fatalf("the launcher at %s resolved to no versioned directory under %s", filepath.Join(binDir, "go"), prefix)
	}
	// The prefix as the resolver reports it: on a Mac a temp dir is under /var, a symlink
	// to /private/var, and both sides are resolved before they are compared.
	realPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(realPrefix, "1.27.1"); got != want {
		t.Errorf("the versioned root is %s, want %s (the version is read off the launcher, never guessed)", got, want)
	}
	// A GO FROM SOMEWHERE ELSE NAMES NOTHING. This entry is brew's copy and only brew's: a
	// Go unpacked into ~/sdk or the distribution's /usr/bin/go is under another prefix and
	// must not drag a Cellar path that is not there onto the argv, which rule 5 refuses.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other)
	if got, ok := toolchainVersionDir(prefix, "go"); ok {
		t.Errorf("a go outside the prefix named the root %s; the versioned entry is brew's copy and only brew's", got)
	}
	// AND A TOOL THAT IS NOT INSTALLED AT ALL names nothing, rather than a prefix.
	t.Setenv("PATH", t.TempDir())
	if got, ok := toolchainVersionDir(prefix, "dotnet"); ok {
		t.Errorf("a tool that is not on PATH named the root %s", got)
	}
}

// TestToolchainRootsAreOneListPerOS holds the shape the wall depends on, on every platform:
// each OS's list resolves under a home of the test's own, a system root needs no home, and
// an OS the list does not speak for names nothing rather than another OS's layout.
func TestToolchainRootsAreOneListPerOS(t *testing.T) {
	for _, goos := range ToolchainRootOSes() {
		names := ToolchainRootNames(goos)
		if len(names) == 0 {
			t.Errorf("%s: the one list names no toolchain root at all", goos)
		}
		var home int
		for _, r := range ToolchainRootList(goos) {
			if r.Home() {
				home++
				if strings.HasPrefix(r.Name, "/") {
					t.Errorf("%s: %s is a home root and an absolute path at once", goos, r.Name)
				}
				continue
			}
			if !strings.HasPrefix(r.Name, "/") {
				t.Errorf("%s: the system root %s is not absolute", goos, r.Name)
			}
		}
		if home == 0 {
			t.Errorf("%s: no root is under HOME; the module cache always is", goos)
		}
	}
	if got := ToolchainRootNames("plan9"); len(got) != 0 {
		t.Errorf("an OS the list does not speak for names %v; it names nothing", got)
	}
	if got := ToolchainRoots("plan9", t.TempDir()); len(got) != 0 {
		t.Errorf("an OS the list does not speak for resolved %v; it names nothing", got)
	}
	// AN EMPTY HOME NAMES NO HOME-RELATIVE ROOT: a relative root is a refusal and a root at
	// the filesystem's top is not a toolchain.
	for _, r := range ToolchainRoots(ThisOS(), "") {
		if r.Home() {
			t.Errorf("an empty home still named the home root %s at %s", r.Name, r.Path)
		}
	}
}

// TestToolchainRootsResolveSymlinks: the grant is checked against the RESOLVED target on
// both wall bodies -- that is why ~/go/bin/go, a symlink into the sdk tree, still runs while
// a real binary in that directory is Permission denied -- so a root that is itself a symlink
// (`/opt/homebrew/opt/openjdk` points into the Cellar) has to reach the argv resolved, or
// the wall names a tree the card's runtime is not under.
func TestToolchainRootsResolveSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinked toolchain roots are a unix shape")
	}
	home := t.TempDir()
	real := filepath.Join(t.TempDir(), "real-sdk")
	if err := os.MkdirAll(filepath.Join(real, "go1.26.5", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, "sdk")); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range ToolchainRoots("linux", home) {
		if r.Name != "sdk" {
			continue
		}
		found = true
		want, err := filepath.EvalSymlinks(real)
		if err != nil {
			t.Fatal(err)
		}
		if r.Path != want {
			t.Errorf("the symlinked root ~/sdk reached the argv as %s, want the resolved %s", r.Path, want)
		}
	}
	if !found {
		t.Error("a symlinked ~/sdk was dropped; it is a directory and it is the card's toolchain")
	}
}
