package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// benchStandardScript is the LINUX provisioning standard's one admin entry, and the file
// that carries that standard's own copy of the toolchain roots. The DARWIN standard is
// pulse.FleetStandardChecks's darwin table -- a Mac bench is checked by `nova-pulse fleet
// standard`, whose checks are data per operating system, and never by this script, which
// refuses to run anywhere but on a Linux bench.
const benchStandardScript = "tools/bench-standard.sh"

// toolchainRootsMarker brackets that copy. A marker rather than a grep for the variable
// name, so the list a reader edits and the list this test reads are the same lines.
const (
	toolchainRootsBegin = "# NOVA_TOOLCHAIN_ROOTS BEGIN"
	toolchainRootsEnd   = "# NOVA_TOOLCHAIN_ROOTS END"
)

// TestBenchStandardAndTheWallNameTheSameToolchainRoots is the class rule behind the edge
// the schema dogfood loop hit on 2026-09-18: TWO CONTRACTS THAT NAME THE SAME PATHS IN TWO
// PLACES WILL DISAGREE, and the disagreement is discovered by a card that dies. The bench
// provisioning standard put Go and sbcl under `~/sdk`; the sandbox wall's implicit worker
// description named no toolchain root at all and pinned GOTOOLCHAIN=local; so every Go card
// on hulk was denied EXECUTION of the bench's own go, fell back to the distribution's
// `/usr/bin/go` 1.22.2 under the `/usr` root, and got
// `go: go.mod requires go >= 1.26 (running go 1.22.2; GOTOOLCHAIN=local)`.
//
// THE SAME HURT HAS A DARWIN FACE, measured on the M2 Air the same day: a Mac's toolchains
// are INSTALLED and on PATH, and three of them still died inside the bare wall, because each
// resolves its runtime from the directory of the launcher that ran it and that launcher is a
// symlink out of any granted tree -- `go: cannot find GOROOT directory: 'go' binary is
// trimmed`, `dotnet: Failed to resolve full path of the current executable []`, `java:
// Unable to locate a Java Runtime`. So the one list is PER GOOS, and this test is PER GOOS
// with it: an OS whose two lists disagree is red here rather than red on a bench.
//
// The fix was ONE list (internal/swarm/toolchain.go) that the wall reads to build its argv.
// This test is what keeps it one, in BOTH DIRECTIONS for every operating system the list
// speaks for -- a root the wall grants that the standard does not name is a wall granting a
// path that will not be there, and a root the standard names that the wall does not grant is
// the original bug returning.
func TestBenchStandardAndTheWallNameTheSameToolchainRoots(t *testing.T) {
	root := repoRoot(t)
	oses := swarm.ToolchainRootOSes()
	if len(oses) == 0 {
		t.Fatal("the wall names no toolchain root on any OS at all: that IS the bug this test exists for")
	}
	for _, goos := range oses {
		fromWall := swarm.ToolchainRootNames(goos)
		if len(fromWall) == 0 {
			t.Errorf("the wall names no toolchain root for %s at all: that IS the bug this test exists for", goos)
			continue
		}
		// THE STANDARD'S SIDE, per OS: the same agreement, read from the place that operating
		// system's benches are actually provisioned and checked from.
		fromStandard := standardRoots(t, root, goos)
		if strings.Join(fromStandard, " ") != strings.Join(fromWall, " ") {
			t.Errorf("the %s provisioning standard and the wall name different toolchain roots:\n  standard (%s): %v\n  internal/swarm/toolchain.go: %v\nThey are ONE list. Edit both sides together.",
				goos, standardSource(goos), fromStandard, fromWall)
		}
		// ONE LIST, TWO KINDS, and the kind is the security decision. A `--read` root carries
		// EXECUTE on both bodies -- landlock's read subset is EXECUTE|READ_FILE|READ_DIR and
		// the darwin profile grants process-exec* globally -- so what a root is granted AS is
		// as load-bearing as whether it is granted at all (Johnny's security read of #1364).
		kind := map[string]bool{} // name -> carries execute
		for _, r := range swarm.ToolchainRootList(goos) {
			if _, twice := kind[r.Name]; twice {
				t.Errorf("%s: the toolchain list names %s twice; one root, one kind", goos, r.Name)
			}
			kind[r.Name] = r.Exec
			// NEVER A BIN DIRECTORY, on any OS: a directory of launchers is writable by the
			// bench user or by brew, and exec on it hands a card whatever lands there. Every
			// grant is on a toolchain TREE.
			if base := strings.TrimSuffix(r.Name, "/"); strings.HasSuffix(base, "/bin") || base == "bin" {
				t.Errorf("%s: the wall grants the bin directory %s; the grant is on the toolchain tree, never a directory of launchers", goos, r.Name)
			}
		}
		// go/pkg/mod IS granted on every OS and is granted READ WITHOUT EXECUTE. Every
		// `go mod download` on the bench lands there and the bench user can write to it, so
		// execute on that tree would let a card run whatever a dependency shipped.
		if exec, granted := kind["go/pkg/mod"]; !granted {
			t.Errorf("%s: the wall does not grant the module cache ~/go/pkg/mod at all; it is the read-without-execute kind", goos)
		} else if exec {
			t.Errorf("%s: the wall grants the module cache ~/go/pkg/mod EXECUTE: it is the read-without-execute kind (Johnny's security read of #1364)", goos)
		}
		// The sdk tree is the card's own `go` where the standard unpacks one, and it is the
		// ONE home directory that carries execute.
		if exec, granted := kind["sdk"]; !granted || !exec {
			t.Errorf("%s: the wall does not grant ~/sdk execute (granted=%v exec=%v); it is the card's own toolchain", goos, granted, exec)
		}
		// AND THE ROOT THAT STAYS OUT ENTIRELY, named here so a later widening is a red run
		// and not a judgement call: ~/go/bin is GOPATH/bin, every `go install` lands there
		// and the bench user can write to it, so granting it under EITHER kind hands a card
		// the bench user's own tools. Nothing is lost -- ~/go/bin/go is a symlink into the
		// sdk tree and the kernel checks the resolved target.
		if _, granted := kind["go/bin"]; granted {
			t.Errorf("%s: the wall grants the toolchain root ~/go/bin: it is granted under neither kind (Johnny's security read of #1364)", goos)
		}
		// And the roots are documented where a reader of the wall looks for them, under the
		// spelling the doc uses: `~/name` for a home root, the path itself for a system one.
		for _, doc := range []string{"docs/SPEC-SWARM.md", "docs/CLI.md"} {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(doc)))
			if err != nil {
				t.Fatalf("reading %s: %v", doc, err)
			}
			for _, r := range swarm.ToolchainRootList(goos) {
				spelled := r.Name
				if r.Home() {
					spelled = "~/" + r.Name
				}
				if !strings.Contains(string(body), spelled) {
					t.Errorf("%s does not name the %s toolchain root %s the wall grants", doc, goos, spelled)
				}
			}
		}
	}
	// The LINUX standard must also CHECK its roots, not merely declare them: a bench missing
	// one has to drift before a card discovers it. (A darwin root is reported and never
	// drifted on -- a Mac with no .NET is a Mac with no .NET -- which is the darwin table's
	// MatchNonempty, asserted in standardRoots.)
	if !strings.Contains(string(rawBenchStandard(t, root)), "drift \"toolchain root ") {
		t.Errorf("%s declares the toolchain roots but never drifts on a missing one", benchStandardScript)
	}
}

// standardSource names the file a reader edits for one OS's side of the agreement.
func standardSource(goos string) string {
	if goos == "linux" {
		return benchStandardScript + " and internal/pulse/fleetstandard.go"
	}
	return "internal/pulse/fleetstandard.go"
}

// standardRoots is the provisioning standard's own list of toolchain roots for one OS, in
// the order it names them.
//
// `nova-pulse fleet standard` is the standard for EVERY bench -- its checks are data, one
// table per operating system -- so its table is read for both. A linux bench additionally
// has tools/bench-standard.sh, which runs ON the bench, and its marked block is held against
// the same names: three copies of one list is exactly the shape that let the standard and
// the wall disagree in the first place, so all three are compared rather than two.
func standardRoots(t *testing.T, repo, goos string) []string {
	t.Helper()
	var names []string
	for _, c := range pulse.FleetStandardChecks(goos, "", "", 25) {
		if c.Root == "" {
			continue
		}
		if c.Probe == "" || c.Match == "" {
			t.Errorf("%s: the standard's check %s names the toolchain root %s with no probe or no matcher", goos, c.Name, c.Root)
		}
		// A linux root is DEMANDED and a darwin root is REPORTED: a Mac's toolchains are
		// installed rather than unpacked into a home, so which trees exist is the machine's
		// shape and drifting on one would make every Mac bench permanently red.
		if goos == "linux" && c.Match != pulse.MatchEquals {
			t.Errorf("linux: the standard reports the toolchain root %s instead of demanding it (match=%s); a linux bench missing a root kills every Go card on it", c.Root, c.Match)
		}
		if goos == "darwin" && c.Match != pulse.MatchNonempty {
			t.Errorf("darwin: the standard DEMANDS the toolchain root %s (match=%s); a Mac's toolchains are installed, not provisioned into a home, so its roots are reported", c.Root, c.Match)
		}
		names = append(names, c.Root)
	}
	if goos != "linux" {
		return names
	}
	fromScript := toolchainRootsInScript(t, string(rawBenchStandard(t, repo)))
	if strings.Join(fromScript, " ") != strings.Join(names, " ") {
		t.Errorf("the two linux standards name different toolchain roots:\n  %s: %v\n  internal/pulse/fleetstandard.go: %v\nThey are ONE list.",
			benchStandardScript, fromScript, names)
	}
	return names
}

// rawBenchStandard is the linux standard's script.
func rawBenchStandard(t *testing.T, repo string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(benchStandardScript)))
	if err != nil {
		t.Fatalf("reading the provisioning standard: %v", err)
	}
	return raw
}

// toolchainRootsInScript reads the marked block and returns the names it assigns, in order.
// The block is required: its absence is the standard's copy having been renamed or removed,
// which is the drift this test is about.
func toolchainRootsInScript(t *testing.T, body string) []string {
	t.Helper()
	_, after, found := strings.Cut(body, toolchainRootsBegin)
	if !found {
		t.Fatalf("%s carries no %s marker", benchStandardScript, toolchainRootsBegin)
	}
	block, _, found := strings.Cut(after, toolchainRootsEnd)
	if !found {
		t.Fatalf("%s carries no %s marker", benchStandardScript, toolchainRootsEnd)
	}
	var names []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("the marked block in %s holds a line that is not an assignment: %q", benchStandardScript, line)
		}
		names = append(names, strings.Fields(strings.Trim(strings.TrimSpace(value), `"'`))...)
	}
	if len(names) == 0 {
		t.Fatalf("the marked block in %s names no root", benchStandardScript)
	}
	return names
}
