package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// benchStandardScript is the provisioning standard's one admin entry, and the file that
// carries the standard's own copy of the toolchain roots.
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
// The fix was ONE list (internal/swarm/toolchain.go) that the wall reads to build its argv.
// This test is what keeps it one: the standard's script carries the same names between two
// markers, and a change to either side without the other is red here rather than red on a
// bench. It is checked in BOTH directions -- a root the wall grants that the standard does
// not provision is a wall granting a path that will not be there, and a root the standard
// provisions that the wall does not grant is the original bug returning.
func TestBenchStandardAndTheWallNameTheSameToolchainRoots(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(benchStandardScript)))
	if err != nil {
		t.Fatalf("reading the provisioning standard: %v", err)
	}
	fromScript := toolchainRootsInScript(t, string(raw))
	fromWall := swarm.ToolchainRootNames()
	if len(fromWall) == 0 {
		t.Fatal("the wall names no toolchain root at all: that IS the bug this test exists for")
	}
	if strings.Join(fromScript, " ") != strings.Join(fromWall, " ") {
		t.Errorf("the provisioning standard and the wall name different toolchain roots:\n  %s: %v\n  internal/swarm/toolchain.go: %v\nThey are ONE list. Edit internal/swarm/toolchain.go and the marked block in the script together.",
			benchStandardScript, fromScript, fromWall)
	}
	// The standard must also CHECK them, not merely declare them: a bench missing a root
	// has to drift before a card discovers it.
	if !strings.Contains(string(raw), "drift \"toolchain root ") {
		t.Errorf("%s declares the toolchain roots but never drifts on a missing one", benchStandardScript)
	}
	// And the roots are documented where a reader of the wall looks for them.
	for _, doc := range []string{"docs/SPEC-SWARM.md", "docs/CLI.md"} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(doc)))
		if err != nil {
			t.Fatalf("reading %s: %v", doc, err)
		}
		for _, name := range fromWall {
			if !strings.Contains(string(body), "~/"+name) {
				t.Errorf("%s does not name the toolchain root ~/%s the wall grants", doc, name)
			}
		}
	}
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
