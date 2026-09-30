package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The standard asks whether go and sbcl can be EXECUTED inside the sandbox wall,
// not merely whether they are on PATH. A card does not run in the bench user's
// shell: it runs behind the wall, whose execute roots are the system table of
// internal/sandbox/wrap_linux.go plus $HOME/sdk. An interpreter at
// $HOME/.local/bin is found by `command -v` and is `Permission denied` inside
// the wall, so a bench passes a presence check and every card on it dies.
//
// The layout is the whole method: the tool is installed at a path under a fake
// HOME and first on PATH, and every other check is left to fail and is not read.

// benchWithTool is an empty bench with `tool` at `at` (relative to the home) and
// its directory first on PATH.
func benchWithTool(t *testing.T, tool, at string) (*bench, string) {
	t.Helper()
	b := emptyBench(t)
	full := b.write(at, "#!/bin/sh\necho 'stub "+tool+" 0.0'\n", true)
	b.setEnv("PATH=" + filepath.Dir(full))
	return b, full
}

func wallLines(output, tool string) []string { return driftWith(output, tool+" on PATH is ") }

// A tool that resolves under NO root the wall executes is one DRIFT line that
// names the remedy: "your sbcl is in the wrong place" is a sentence somebody
// then has to work out the right place for.
func TestAToolTheWallCannotExecuteIsOneLineNamingTheRemedy(t *testing.T) {
	t.Parallel()
	b, path := benchWithTool(t, "sbcl", ".local/bin/sbcl")
	_, output := b.standard()
	lines := wallLines(output, "sbcl")
	if len(lines) != 1 {
		t.Fatalf("an sbcl at %s drew %d wall lines, want 1:\n%s", path, len(lines), output)
	}
	for _, want := range []string{path, "$HOME/sdk", "sdk/sbcl-", "EXECUTE", "under NO read root the sandbox wall grants"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the wall-toolchain DRIFT line does not carry %q:\n%s", want, lines[0])
		}
	}
	// go is held to the same rule: the check is a loop over both.
	bg, pathGo := benchWithTool(t, "go", ".local/bin/go")
	_, outGo := bg.standard()
	if got := wallLines(outGo, "go"); len(got) != 1 {
		t.Errorf("a go at %s drew %d wall lines, want 1:\n%s", pathGo, len(got), outGo)
	}
}

// The positive direction catches a check written as "always drift": $HOME/sdk
// carries execute, so a tool under it is the conforming layout.
func TestAToolUnderAGrantedRootDrawsNoWallLine(t *testing.T) {
	t.Parallel()
	b, path := benchWithTool(t, "sbcl", "sdk/sbcl-2.5.8/bin/sbcl")
	if _, output := b.standard(); len(wallLines(output, "sbcl")) != 0 {
		t.Errorf("an sbcl at %s is under the granted $HOME/sdk and still drifted:\n%s", path, output)
	}
	bg, pathGo := benchWithTool(t, "go", "sdk/go1.26.6/bin/go")
	if _, output := bg.standard(); len(wallLines(output, "go")) != 0 {
		t.Errorf("a go at %s is under the granted $HOME/sdk and still drifted:\n%s", pathGo, output)
	}
}

// A tool reached through a link is judged by where it really lives.
func TestALinkIntoSdkIsGrantedAndALinkOutOfItIsNot(t *testing.T) {
	t.Parallel()
	b := emptyBench(t)
	real := b.write("sdk/sbcl-2.5.8/bin/sbcl", "#!/bin/sh\n", true)
	if err := os.Symlink(real, filepath.Join(b.bin, "sbcl")); err != nil {
		t.Fatal(err)
	}
	if _, output := b.standard(); len(wallLines(output, "sbcl")) != 0 {
		t.Errorf("a link into sdk drifted:\n%s", output)
	}
	c := emptyBench(t)
	out := c.write(".local/bin/sbcl", "#!/bin/sh\n", true)
	if err := os.Symlink(out, filepath.Join(c.bin, "sbcl")); err != nil {
		t.Fatal(err)
	}
	if _, output := c.standard(); len(wallLines(output, "sbcl")) != 1 {
		t.Errorf("a link out of sdk did not drift:\n%s", output)
	}
}

// The wall also grants the directory /etc/resolv.conf RESOLVES to, per machine;
// a tool under it is wall-executable and the check accepts it. The control is
// the same layout with no resolver pointing there, which must drift, so the
// acceptance is the resolver grant and not an accident of where the fixture lives.
func TestTheResolverDirectoryTheWallGrantsIsGrantedHere(t *testing.T) {
	t.Parallel()
	b, path := benchWithTool(t, "sbcl", "mnt/wsl/sbcl-2.5.8/bin/sbcl")
	wsl := filepath.Join(b.home, "mnt", "wsl")
	if err := os.WriteFile(filepath.Join(wsl, "resolv.conf"), []byte("nameserver 10.255.255.254\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(b.home, "etc-resolv.conf")
	if err := os.Symlink(filepath.Join(wsl, "resolv.conf"), link); err != nil {
		t.Fatal(err)
	}
	// The tool is at mnt/wsl/sbcl-2.5.8/bin, a subdirectory of the resolver
	// directory, which is what "under" means.
	b.setEnv("NOVA_RESOLV_CONF=" + link)
	if _, output := b.standard(); len(wallLines(output, "sbcl")) != 0 {
		t.Errorf("an sbcl at %s is under the resolver directory the wall grants and still drifted:\n%s", path, output)
	}

	ctl, pathCtl := benchWithTool(t, "sbcl", "mnt/wsl/sbcl-2.5.8/bin/sbcl")
	ctl.setEnv("NOVA_RESOLV_CONF=" + filepath.Join(ctl.home, "no-resolv.conf"))
	if _, output := ctl.standard(); len(wallLines(output, "sbcl")) != 1 {
		t.Errorf("control: an sbcl at %s with no resolver pointing there did not drift:\n%s", pathCtl, output)
	}
}

func TestWallExecRootsAreTheWallsTableTheResolverAndSdk(t *testing.T) {
	t.Parallel()
	got := wallExecRoots("/home/u", "/mnt/wsl")
	want := append(append([]string{}, wallReadRoots...), "/mnt/wsl", "/home/u/sdk")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("wallExecRoots = %v, want %v", got, want)
	}
	got = wallExecRoots("/home/u", "")
	if len(got) != len(wallReadRoots)+1 || got[len(got)-1] != "/home/u/sdk" {
		t.Errorf("with no resolver directory, wallExecRoots = %v", got)
	}
	// go/pkg/mod is granted read WITHOUT execute, so it is never an execute root.
	for _, r := range got {
		if strings.Contains(r, "pkg/mod") {
			t.Errorf("an execute root under the module cache: %s", r)
		}
	}
}
