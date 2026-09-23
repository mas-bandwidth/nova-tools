package ci

// The bench standard used to ask only whether `go` and `sbcl` were ON PATH. A card does not
// run in the bench user's shell: it runs behind the sandbox wall, whose linux read roots are
// the system table of internal/sandbox/wrap_linux.go plus the toolchain roots of
// internal/swarm/toolchain.go ($HOME/sdk with EXECUTE, $HOME/go/pkg/mod without). An
// interpreter at $HOME/.local/bin/sbcl satisfies `command -v` and is `Permission denied`
// inside the wall, so the bench passes the standard and every card on it dies -- which is
// exactly what happened: every lisp card was forced onto the one bench whose sbcl is
// /usr/bin/sbcl, E09-G1 taking 1036 s on vision against 248-393 s for the same class on
// space, and the r1785 worker on mini fetching an SBCL 2.4.0 of its own into $TMPDIR before
// it could run a test. The wall was right and the standard was silent.
//
// This is the #1706 shape, the one benchstandard_disk_test.go already uses: run the REAL
// tools/bench-standard.sh with a FAKE PATH layout and a HOME of its own. The layout is the
// whole method -- the test cannot move a real bench's sbcl, and a test that only passes
// where the developer's own sbcl happens to live is not a test.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

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
	// A stub that answers --version, because check (3) reads `go version` before (3b) runs.
	if err := os.WriteFile(full, []byte("#!/bin/sh\necho 'stub "+tool+" 0.0'\n"), 0o755); err != nil {
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
// resolves under NO read root the wall grants must be one DRIFT line, and that line must
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

// wallReadRootsBegin/End bracket the standard's copy of the wall's linux read-root table, as
// the NOVA_TOOLCHAIN_ROOTS markers bracket its copy of the toolchain roots.
const (
	wallReadRootsBegin = "# NOVA_WALL_READ_ROOTS BEGIN"
	wallReadRootsEnd   = "# NOVA_WALL_READ_ROOTS END"
)

// TestBenchStandardAndTheWallNameTheSameReadRoots is Stella's hold on #1870 made a class
// test: (3b) first shipped with a HAND-PICKED SUBSET of the wall's roots (no /etc, no
// /run/systemd/resolve, no /dev, no /proc), so a toolchain the wall executes under one of
// those was reported "under NO read root" and a conforming bench was rejected. The two
// lists are ONE list, read here from both places -- linuxReadRoots in
// internal/sandbox/wrap_linux.go (a linux-tagged unexported var, so read as source, which
// also keeps this test running on the darwin benches) and the marker block in the script --
// and must match in both directions and in order.
func TestBenchStandardAndTheWallNameTheSameReadRoots(t *testing.T) {
	root := repoRoot(t)
	wallSrc, err := os.ReadFile(filepath.Join(root, "internal", "sandbox", "wrap_linux.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^var linuxReadRoots = \[\]string\{([^}]*)\}`).FindSubmatch(wallSrc)
	if m == nil {
		t.Fatal("internal/sandbox/wrap_linux.go no longer declares `var linuxReadRoots = []string{...}` on one line; update this test's reader with it")
	}
	var fromWall []string
	for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllSubmatch(m[1], -1) {
		fromWall = append(fromWall, string(q[1]))
	}
	if len(fromWall) == 0 {
		t.Fatal("the wall's linux read-root table parsed empty")
	}

	script, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(benchStandardScript)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(script)
	b, e := strings.Index(body, wallReadRootsBegin), strings.Index(body, wallReadRootsEnd)
	if b < 0 || e < b {
		t.Fatalf("%s carries no %q ... %q block: check (3b)'s roots must be the wall's table, bracketed", benchStandardScript, wallReadRootsBegin, wallReadRootsEnd)
	}
	sm := regexp.MustCompile(`NOVA_WALL_READ_ROOTS="([^"]*)"`).FindStringSubmatch(body[b:e])
	if sm == nil {
		t.Fatalf("the %s block in %s sets no NOVA_WALL_READ_ROOTS=\"...\"", wallReadRootsBegin, benchStandardScript)
	}
	fromStandard := strings.Fields(sm[1])
	if strings.Join(fromStandard, " ") != strings.Join(fromWall, " ") {
		t.Errorf("check (3b) and the wall name different linux read roots:\n  %s: %v\n  internal/sandbox/wrap_linux.go linuxReadRoots: %v\nThey are ONE list: a root the wall grants and (3b) omits rejects a conforming bench. Edit both sides together.",
			benchStandardScript, fromStandard, fromWall)
	}
	// The block is only half the rule: the loop must actually read it (and the per-machine
	// resolver directory), or the block is decoration.
	if !strings.Contains(body, `for root in $NOVA_WALL_READ_ROOTS $wall_resolv_dir "$HOME_DIR/sdk"; do`) {
		t.Errorf("check (3b)'s root loop does not read $NOVA_WALL_READ_ROOTS, the resolver directory and $HOME/sdk")
	}
}

// TestBenchStandardGrantsTheResolverDirectoryTheWallGrants is the dynamic half of the same
// hold: linuxRoots() adds the directory /etc/resolv.conf RESOLVES to (#1737; /mnt/wsl on
// WSL2), so a tool under that directory is wall-executable and (3b) must accept it. The
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
