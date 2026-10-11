package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The standard asks whether go and sbcl can be EXECUTED inside the sandbox wall,
// not merely whether they are on PATH. A card does not run in the bench user's
// shell: it runs behind the wall, whose execute roots are the system table of
// pkg/sandbox/wrap_linux.go plus $HOME/sdk. An interpreter at
// $HOME/.local/bin is found by `command -v` and is `Permission denied` inside
// the wall, so a bench passes a presence check and every card on it dies.
//
// The layout is the whole method: the tool is installed at a path under a fake
// HOME and first on PATH, and every other check is left to fail and is not read.

// A tool that resolves under NO root the wall executes is one DRIFT line that
// names the remedy: "your sbcl is in the wrong place" is a sentence somebody
// then has to work out the right place for.
func TestAToolTheWallCannotExecuteIsOneLineNamingTheRemedy(t *testing.T) {
	t.Parallel()
	b := emptyBench(t)
	path := b.toolOnPath("sbcl", ".local/bin/sbcl")
	lines := b.wantWallLines("sbcl", path, 1)
	for _, want := range []string{path, "$HOME/sdk", "sdk/sbcl-", "EXECUTE", "under NO read root the sandbox wall grants"} {
		assert.Contains(t, lines[0], want, "the wall-toolchain DRIFT line")
	}
	// go is held to the same rule: the check is a loop over both.
	bg := emptyBench(t)
	pathGo := bg.toolOnPath("go", ".local/bin/go")
	bg.wantWallLines("go", pathGo, 1)
}

// The positive direction catches a check written as "always drift": $HOME/sdk
// carries execute, so a tool under it is the conforming layout.
func TestAToolUnderAGrantedRootDrawsNoWallLine(t *testing.T) {
	t.Parallel()
	b := emptyBench(t)
	b.wantWallLines("sbcl", b.toolOnPath("sbcl", "sdk/sbcl-2.5.8/bin/sbcl"), 0)
	bg := emptyBench(t)
	bg.wantWallLines("go", bg.toolOnPath("go", "sdk/go1.26.6/bin/go"), 0)
}

// A tool reached through a link is judged by where it really lives.
func TestALinkIntoSdkIsGrantedAndALinkOutOfItIsNot(t *testing.T) {
	t.Parallel()
	b := emptyBench(t)
	b.wantWallLines("sbcl", b.linkedTool("sbcl", "sdk/sbcl-2.5.8/bin/sbcl"), 0)
	c := emptyBench(t)
	c.wantWallLines("sbcl", c.linkedTool("sbcl", ".local/bin/sbcl"), 1)
}

// The wall also grants the directory /etc/resolv.conf RESOLVES to, per machine;
// a tool under it is wall-executable and the check accepts it. The control is
// the same layout with no resolver pointing there, which must drift, so the
// acceptance is the resolver grant and not an accident of where the fixture lives.
func TestTheResolverDirectoryTheWallGrantsIsGrantedHere(t *testing.T) {
	t.Parallel()
	b := emptyBench(t)
	path := b.toolOnPath("sbcl", "mnt/wsl/sbcl-2.5.8/bin/sbcl")
	wsl := filepath.Join(b.home, "mnt", "wsl")
	require.NoError(t, os.WriteFile(filepath.Join(wsl, "resolv.conf"), []byte("nameserver 10.255.255.254\n"), 0o644))
	link := filepath.Join(b.home, "etc-resolv.conf")
	require.NoError(t, os.Symlink(filepath.Join(wsl, "resolv.conf"), link))
	// The tool is at mnt/wsl/sbcl-2.5.8/bin, a subdirectory of the resolver
	// directory, which is what "under" means.
	b.setEnv("NOVA_RESOLV_CONF=" + link)
	b.wantWallLines("sbcl", path, 0)

	ctl := emptyBench(t)
	pathCtl := ctl.toolOnPath("sbcl", "mnt/wsl/sbcl-2.5.8/bin/sbcl")
	ctl.setEnv("NOVA_RESOLV_CONF=" + filepath.Join(ctl.home, "no-resolv.conf"))
	ctl.wantWallLines("sbcl", pathCtl, 1)
}

func TestWallExecRootsAreTheWallsTableTheResolverAndSdk(t *testing.T) {
	t.Parallel()
	got := wallExecRoots("/home/u", "/mnt/wsl")
	want := append(append([]string{}, wallReadRoots...), "/mnt/wsl", "/home/u/sdk")
	assert.Equal(t, want, got)
	got = wallExecRoots("/home/u", "")
	assert.Len(t, got, len(wallReadRoots)+1)
	assert.Equal(t, "/home/u/sdk", got[len(got)-1])
	// go/pkg/mod is granted read WITHOUT execute, so it is never an execute root.
	for _, r := range got {
		assert.NotContains(t, r, "pkg/mod", "an execute root under the module cache")
	}
}
