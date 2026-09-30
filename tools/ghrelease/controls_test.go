package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The controls verb builds the tree and runs the stamp assertion over real
// binaries; here the toolchain is a fake that writes, for each tool, a file whose
// content is what the binary's `version` would print. The fake is honest about
// the linker: a build with the stamp flag reports the stamp, one without reports
// a pseudo-version, and a tool whose version variable is overlaid away reports a
// pseudo-version even when the flag is given. The broken worlds prove the
// controls can fail, which is what they exist to show.

const versionSource = `package main

import "runtime/debug"

// version is written by the linker.
var version string

func versionLine() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}
`

type linker struct {
	ignoresTheStampFlag bool // a world where the flag never reaches a binary
	overlayIsIgnored    bool // a world where a mutated source never reaches the build
}

func controlsWorld(t *testing.T, l linker) *harness {
	t.Helper()
	h := newHarness(t)
	for _, n := range []string{"nova-bus", "nova-tokens"} {
		h.write("cmd/"+n+"/main.go", "package main\n", 0o644)
		h.write("cmd/"+n+"/version.go", versionSource, 0o644)
	}
	h.runner.stream = func(c command, stdout, stderr io.Writer) int {
		var dir, stamp string
		overlay := false
		for i, a := range c.args {
			switch a {
			case "-o":
				dir = c.args[i+1]
			case "-ldflags":
				stamp = strings.TrimPrefix(c.args[i+1], "-s -w -X main.version=")
			case "-overlay":
				overlay = true
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 1
		}
		reported := stamp
		if l.ignoresTheStampFlag {
			reported = "v0.0.0-dry-run" // the stamp reaches every binary whatever the flags say
		}
		for _, n := range []string{"nova-bus", "nova-tokens"} {
			line := n + " (devel) linux/amd64"
			if reported != "" {
				line = n + " " + reported + " linux/amd64"
			}
			if overlay && n == "nova-tokens" && !l.overlayIsIgnored {
				line = n + " (devel) linux/amd64"
			}
			if err := os.WriteFile(filepath.Join(dir, n), []byte(line+"\n"), 0o755); err != nil {
				return 1
			}
		}
		return 0
	}
	h.runner.output = func(c command) (string, int) {
		b, err := os.ReadFile(c.name)
		if err != nil {
			return err.Error(), 127
		}
		return string(b), 0
	}
	return h
}

func TestControlsHoldOnAnHonestLinker(t *testing.T) {
	t.Parallel()
	h := controlsWorld(t, linker{})
	h.wantRC(h.do("controls"), 0)
	for _, want := range []string{
		"ok: unstamped binaries are refused by name",
		"ok: stamped binaries pass",
		"ok: release-named binaries pass through the release's template",
		"ok: a rename of the stamp is refused by name, with nova-bus still asserted",
		"ok: a remove of the stamp is refused by name, with nova-bus still asserted",
	} {
		h.mustContain(want)
	}
	// Four builds: unstamped, stamped, and the two mutations; each over the whole tree.
	builds := 0
	for _, c := range h.runner.called() {
		if c.name == "go" && c.args[0] == "build" {
			builds++
			if c.args[len(c.args)-1] != "./cmd/..." {
				t.Errorf("a control built %v, not the whole tree", c.args)
			}
		}
	}
	if builds != 4 {
		t.Errorf("%d builds, want 4", builds)
	}
	// The working tree is never edited: the mutation goes through an overlay.
	if b, _ := os.ReadFile(filepath.Join(h.dir, "cmd", "nova-tokens", "version.go")); string(b) != versionSource {
		t.Error("the controls edited the tool's source in the working tree")
	}
}

func TestControlsFailWhenTheAssertionPassesBinariesNothingStamped(t *testing.T) {
	t.Parallel()
	h := controlsWorld(t, linker{ignoresTheStampFlag: true})
	h.wantRC(h.do("controls"), 1)
	h.mustContain("the assertion PASSED a set of binaries nothing stamped; it would pass a broken release")
}

func TestControlsFailWhenAMutationNeverReachesTheBuild(t *testing.T) {
	t.Parallel()
	// The mutated tool still reports the tag, so the assertion passes: the
	// control says so instead of going green on a mutation that changed nothing.
	h := controlsWorld(t, linker{overlayIsIgnored: true})
	h.wantRC(h.do("controls"), 1)
	h.mustContain("the assertion PASSED a release whose nova-tokens answers a pseudo-version (rename)")
}

func TestControlsFailWhenTheFixtureDoesNotApply(t *testing.T) {
	t.Parallel()
	h := controlsWorld(t, linker{})
	h.write("cmd/nova-tokens/version.go", "package main\n\nvar other string\n", 0o644)
	h.wantRC(h.do("controls"), 1)
	h.mustContain("the rename fixture did not apply; this control would prove nothing")
}

func TestControlsFailWhenTheBuildFails(t *testing.T) {
	t.Parallel()
	h := controlsWorld(t, linker{})
	h.runner.stream = func(command, io.Writer, io.Writer) int { return 1 }
	h.wantRC(h.do("controls"), 1)
	h.mustContain("the unstamped build failed (go build exit 1); the controls cannot run")
}

func TestControlsTakeTheVictimWitnessAndStampAsFlags(t *testing.T) {
	t.Parallel()
	h := controlsWorld(t, linker{})
	h.wantRC(h.do("controls", "--victim", "nova-tokens", "--witness", "nova-bus", "--stamp", "v9.9.9-x"), 0)
	for _, bad := range [][]string{{"controls", "--nope"}, {"controls", "--victim"}} {
		h := controlsWorld(t, linker{})
		h.wantRC(h.do(bad...), 2)
		h.mustContain("usage:")
	}
	h = controlsWorld(t, linker{})
	h.wantRC(h.do("controls", "--stamp", "v1=2"), 2)
	h.mustContain("refusing: the release stamp")
}

func TestMutateVersionSourceRenamesAndRemovesTheSymbolAndSaysWhetherItLanded(t *testing.T) {
	t.Parallel()
	renamed, ok := mutateVersionSource(versionSource, "rename")
	if !ok || !strings.Contains(renamed, "var Version string") || !strings.Contains(renamed, "resolveVersion(Version, info, ok)") || containsLine(renamed, "var version string") {
		t.Errorf("rename: landed=%v\n%s", ok, renamed)
	}
	removed, ok := mutateVersionSource(versionSource, "remove")
	if !ok || containsLine(removed, "var version string") || !strings.Contains(removed, `resolveVersion("", info, ok)`) {
		t.Errorf("remove: landed=%v\n%s", ok, removed)
	}
	for _, how := range []string{"rename", "remove", "other"} {
		if _, ok := mutateVersionSource("package main\n", how); ok {
			t.Errorf("%s landed on a source with nothing to mutate", how)
		}
	}
}
