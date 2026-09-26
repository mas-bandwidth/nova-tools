//go:build functional

package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// benchstandard_wall_toolchain_functional_test.go runs tools/bench-standard.sh, a
// whole bash program that probes the machine, under a fake PATH: exec of a whole
// program is the functional tier's (Glenn 2026-09-26, nova-tools#4328). The
// static half, the read roots the script and the wall share, stays in
// benchstandard_wall_toolchain_test.go.

// benchStandardWithTool runs the standard with `tool` installed at `at` (relative to the
// fake HOME) and first on PATH, and returns the script's combined output. Everything else
// about the bench is missing, so the script prints other DRIFT lines too and exits 1; only
// the wall-toolchain lines are read here. setup, when not nil, runs with the fake HOME after
// the tool is in place and returns extra environment for the run.
func benchStandardWithTool(t *testing.T, tool, at string, setup ...func(home string) []string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the bench standard is a bash script for a Linux bench")
	}
	home := t.TempDir()
	full := filepath.Join(home, at)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stub that answers --version, because check (3) reads `go version` before (3c) runs.
	if err := testbin.WriteExecutable(full, []byte("#!/bin/sh\necho 'stub "+tool+" 0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Dir(full)
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "tools", "bench-standard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
	)
	for _, f := range setup {
		cmd.Env = append(cmd.Env, f(home)...)
	}
	out, _ := cmd.CombinedOutput() // a bench missing everything exits 1; the lines are the answer
	return string(out), full
}

// driftLinesFor returns every DRIFT line that names the tool's wall problem.
func driftLinesFor(out, tool string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "DRIFT "+tool+" on PATH is ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestBenchStandardDriftsOnAToolTheWallCannotExecute is the negative direction: a tool that
// resolves under NO root the wall grants execute must be one DRIFT line, and that line must
// NAME THE REMEDY, because "your sbcl is in the wrong place" is a sentence somebody then has
// to go and work out the right place for at whatever hour it is.
func TestBenchStandardDriftsOnAToolTheWallCannotExecute(t *testing.T) {
	t.Parallel()

	// $HOME/.local/bin is exactly where the fleet's sbcl was on hulk, vision, mini and
	// captainamerica on 2026-09-19, and exactly where the wall denied it.
	out, path := benchStandardWithTool(t, "sbcl", ".local/bin/sbcl")
	lines := driftLinesFor(out, "sbcl")
	if len(lines) == 0 {
		t.Fatalf("an sbcl at %s is on PATH and `Permission denied` inside the wall, and the standard said nothing:\n%s", path, out)
	}
	if len(lines) != 1 {
		t.Errorf("the wall-toolchain finding is not one line for sbcl:\n%s", strings.Join(lines, "\n"))
	}
	line := lines[0]
	// The path as PATH gave it, the path it really resolves to, the granted home, and the
	// remedy. A DRIFT line that names the fault without naming the fix is half a finding.
	for _, want := range []string{path, "$HOME/sdk", "sdk/sbcl-", "EXECUTE"} {
		if !strings.Contains(line, want) {
			t.Errorf("the wall-toolchain DRIFT line does not carry %q:\n%s", want, line)
		}
	}

	// go is held to the same rule; the check is a loop over both and must not have been
	// written for sbcl alone.
	outGo, pathGo := benchStandardWithTool(t, "go", ".local/bin/go")
	if got := driftLinesFor(outGo, "go"); len(got) != 1 {
		t.Errorf("a go at %s drew %d wall-toolchain DRIFT lines, want 1:\n%s", pathGo, len(got), outGo)
	}
}

// TestBenchStandardAcceptsAToolUnderAGrantedRoot is the positive direction, and it is the
// half that catches a check written as `always drift`. $HOME/sdk is the toolchain root
// internal/swarm/toolchain.go grants with EXECUTE, so a tool under it is the CONFORMING
// layout and must draw no line at all.
func TestBenchStandardAcceptsAToolUnderAGrantedRoot(t *testing.T) {
	t.Parallel()

	out, path := benchStandardWithTool(t, "sbcl", "sdk/sbcl-2.5.8/bin/sbcl")
	if lines := driftLinesFor(out, "sbcl"); len(lines) != 0 {
		t.Errorf("an sbcl at %s is under the granted $HOME/sdk and still drifted:\n%s", path, strings.Join(lines, "\n"))
	}
	outGo, pathGo := benchStandardWithTool(t, "go", "sdk/go1.26.6/bin/go")
	if lines := driftLinesFor(outGo, "go"); len(lines) != 0 {
		t.Errorf("a go at %s is under the granted $HOME/sdk and still drifted:\n%s", pathGo, strings.Join(lines, "\n"))
	}
}

// TestBenchStandardGrantsTheResolverDirectoryTheWallGrants is the dynamic half of the same
// hold: linuxRoots() adds the directory /etc/resolv.conf RESOLVES to (#1737; /mnt/wsl on
// WSL2), so a tool under that directory is wall-executable and (3c) must accept it. The
// fixture stands a symlinked resolv.conf in the fake HOME through NOVA_RESOLV_CONF, the
// script's seam for the wall's resolvConfPath. The control is the same layout WITHOUT the
// resolver pointing there, which must drift -- so the acceptance is the resolver grant and
// not an accident of where the fixture lives.
func TestBenchStandardGrantsTheResolverDirectoryTheWallGrants(t *testing.T) {
	t.Parallel()

	resolver := func(home string) []string {
		wsl := filepath.Join(home, "mnt", "wsl")
		if err := os.WriteFile(filepath.Join(wsl, "resolv.conf"), []byte("nameserver 10.255.255.254\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(home, "etc-resolv.conf")
		if err := os.Symlink(filepath.Join(wsl, "resolv.conf"), link); err != nil {
			t.Fatal(err)
		}
		return []string{"NOVA_RESOLV_CONF=" + link}
	}
	out, path := benchStandardWithTool(t, "sbcl", "mnt/wsl/sbcl-2.5.8/bin/sbcl", resolver)
	if lines := driftLinesFor(out, "sbcl"); len(lines) != 0 {
		t.Errorf("an sbcl at %s is under the resolver directory the wall grants and still drifted:\n%s", path, strings.Join(lines, "\n"))
	}

	missing := func(home string) []string {
		return []string{"NOVA_RESOLV_CONF=" + filepath.Join(home, "no-resolv.conf")}
	}
	outCtl, pathCtl := benchStandardWithTool(t, "sbcl", "mnt/wsl/sbcl-2.5.8/bin/sbcl", missing)
	if got := driftLinesFor(outCtl, "sbcl"); len(got) != 1 {
		t.Errorf("control: an sbcl at %s with no resolver pointing there drew %d wall-toolchain DRIFT lines, want 1:\n%s", pathCtl, len(got), outCtl)
	}
}
