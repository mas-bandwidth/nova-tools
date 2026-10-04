package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stamp verb, pinned by every assertion the release-checks job made of the
// shell assertion it replaces, and by the usage and refusal branches of the
// shell itself. The binaries are fakes: a file with the executable bit whose
// `version` answer comes from the answers map; what needs the real linker is
// the controls verb, run by the release-checks job on this tree's real binaries.

type answer struct {
	out string
	rc  int
}

// stampWorld is a tree of tools, each with a binary at bin/<tool>.
type stampWorld struct {
	*harness
	answers map[string]answer
}

func newStampWorld(t *testing.T, stamp string, tools ...string) *stampWorld {
	t.Helper()
	w := &stampWorld{harness: newHarness(t), answers: map[string]answer{}}
	w.tools(tools...)
	for _, n := range tools {
		w.write("bin/"+n, "#!binary\n", 0o755)
		w.answers[n] = answer{out: fmt.Sprintf("%s %s linux/amd64 go1.27\n", n, stamp)}
	}
	w.runner.output = func(c command) (string, int) {
		if len(c.args) != 1 || c.args[0] != "version" {
			t.Errorf("the assertion ran %v, want `version`", c.args)
		}
		a, ok := w.answers[filepath.Base(c.name)]
		if !ok {
			t.Errorf("no answer for %s", c.name)
		}
		return a.out, a.rc
	}
	return w
}

func TestStampPassesWhenEveryToolReportsTheTagAndNamesItself(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v0.0.0-dry-run", "nova-bus", "nova-tokens")
	w.wantRC(w.do("stamp", "v0.0.0-dry-run", "bin/%s"), 0)
	w.mustContain("ok: nova-bus v0.0.0-dry-run linux/amd64 go1.27\n")
	w.mustContain("ok: nova-tokens v0.0.0-dry-run linux/amd64 go1.27\n")
	w.mustContain("asserted the v0.0.0-dry-run stamp on 2 of 2 shipped tools (0 exempt until #121)\n")
}

func TestStampIsRedForBinariesNothingStampedAndNamesTheTool(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v0.0.0-dry-run", "nova-bus", "nova-tokens")
	w.answers["nova-bus"] = answer{out: "nova-bus v0.0.0-20260930-abcdef-dirty linux/amd64\n"}
	w.wantRC(w.do("stamp", "v0.0.0-dry-run", "bin/%s"), 1)
	w.mustContain("FAIL: nova-bus does not report the tag it was built from\n")
	w.mustContain("  want the token: v0.0.0-dry-run\n")
	w.mustContain("the linker ignores -X main.version in silence")
}

func TestStampOneToolLosingItsStampFailsByNameWithTheOthersStillAsserted(t *testing.T) {
	t.Parallel()
	// The tool-by-tool rule: one binary answering a pseudo-version while the
	// others keep the tag is red by name, is never demoted to a NOTE, and the
	// tools before it are still asserted (the release-checks mutation controls,
	// over fakes; the linker half runs on real binaries in `controls`).
	w := newStampWorld(t, "v0.0.0-dry-run", "nova-bus", "nova-tokens")
	w.answers["nova-tokens"] = answer{out: "nova-tokens (devel) linux/amd64\n"}
	w.wantRC(w.do("stamp", "v0.0.0-dry-run", "bin/%s"), 1)
	w.mustContain("FAIL: nova-tokens does not report the tag it was built from")
	w.mustNotContain("NOTE: nova-tokens")
	w.mustContain("ok: nova-bus v0.0.0-dry-run")
}

func TestStampMatchesTheTagAsAWholeToken(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		printed string
		pass    bool
	}{
		{"nova-bus v0.11 linux/amd64", false}, // v0.1 is not v0.11
		{"nova-bus v0.1.0 linux/amd64", false},
		{"nova-bus xv0.1 linux/amd64", false},
		{"nova-bus v0.1 linux/amd64", true},
		{"nova-bus v0.1", true},
		{"nova-bus build=v0.1", true},          // = is a separator: the value half of key=value counts
		{"nova-bus backend=x v0.1 y", true},    // the tag beside an extra
		{"/home/v0.1/nova-bus (devel)", false}, // a tag inside a path is not the token
	} {
		w := newStampWorld(t, "v0.1", "nova-bus")
		w.answers["nova-bus"] = answer{out: c.printed + "\n"}
		rc := w.do("stamp", "v0.1", "bin/%s")
		if (rc == 0) != c.pass {
			t.Errorf("%q: exit %d, pass want %v\n%s", c.printed, rc, c.pass, w.all())
		}
	}
}

func TestStampRequiresTheVersionLineToNameTheTool(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v1", "nova-bus")
	w.answers["nova-bus"] = answer{out: "nova-tokens v1 linux/amd64\n"}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain("FAIL: nova-bus version does not name the tool it is reporting for")
}

func TestStampFailsAToolWhoseVersionVerbExits(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v1", "nova-bus")
	w.answers["nova-bus"] = answer{out: "usage: nova-bus <verb>\nline two\n", rc: 2}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain("FAIL: nova-bus is a shipped binary and must report the tag, but `nova-bus version` exited 2\n")
	w.mustContain("  it printed: usage: nova-bus <verb> line two\n") // one line, newlines folded
}

func TestStampFailsWhenThereIsNoRunnableBinary(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v1", "nova-bus", "nova-tokens")
	if err := os.Remove(filepath.Join(w.dir, "bin", "nova-tokens")); err != nil {
		t.Fatal(err)
	}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain("FAIL: nova-tokens is built by the release loop but there is no runnable binary at bin/nova-tokens")

	w = newStampWorld(t, "v1", "nova-bus")
	if err := os.Chmod(filepath.Join(w.dir, "bin", "nova-bus"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain("there is no runnable binary at bin/nova-bus")
}

func TestStampShowsALongLineCutAndMarkedButMatchesItWhole(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v1", "nova-bus")
	// The tag is past character 200: a cut match would fail a tool for a defect
	// in the check, and the message would name the tag the binary just printed.
	w.answers["nova-bus"] = answer{out: "nova-bus " + strings.Repeat("x", 300) + " v1\n"}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 0)
	w.mustContain("ok: nova-bus " + strings.Repeat("x", 191) + "...\n")
	w.mustNotContain(strings.Repeat("x", 192))

	w = newStampWorld(t, "v1", "nova-bus")
	w.answers["nova-bus"] = answer{out: "nova-bus " + strings.Repeat("y", 300) + "\n"}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain(strings.Repeat("y", 191) + "...\n")
}

func TestStampRefusesACallItCannotCheckBeforeRunningAnyBinary(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		args   []string
		phrase string
	}{
		{"no tag", []string{"", "bin/%s"}, "no expected tag given"},
		{"no %s", []string{"v1", "bin/nova-bus"}, "holds no %s"},
		{"a % beyond the %s (a tag carrying %s)", []string{"v1.0%s-rc1", "bin/%s_v1.0%s-rc1_linux_amd64"}, "beyond its single %s"},
		{"a % beyond the %s (a %d)", []string{"v1", "bin/%s_%d"}, "beyond its single %s"},
		{"=", []string{"v1.0=rc1", "bin/%s_v1.0=rc1_linux_amd64"}, "contains ="},
		{"whitespace", []string{"v0.13.0 rc1", "bin/%s_v0.13.0 rc1_linux_amd64"}, "carries whitespace"},
	} {
		w := newStampWorld(t, "v1", "nova-bus")
		w.wantRC(w.do(append([]string{"stamp"}, c.args...)...), 2)
		if !strings.Contains(w.errb.String(), c.phrase) {
			t.Errorf("%s: refused, but not for %q:\n%s", c.name, c.phrase, w.all())
		}
		if strings.Contains(w.all(), "ok: ") || strings.Contains(w.all(), "FAIL: ") {
			t.Errorf("%s: the assertion ran the tools before refusing; the refusal must precede them:\n%s", c.name, w.all())
		}
		if n := len(w.runner.called()); n != 0 {
			t.Errorf("%s: %d binaries run before the refusal", c.name, n)
		}
	}
}

func TestStampWrongArgumentCountIsAUsageError(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"stamp"}, {"stamp", "v1"}, {"stamp", "v1", "bin/%s", "cmd", "extra"}} {
		w := newStampWorld(t, "v1", "nova-bus")
		w.wantRC(w.do(args...), 2)
		w.mustContain("usage:")
		if n := len(w.runner.called()); n != 0 {
			t.Errorf("%v: %d binaries run", args, n)
		}
	}
}

func TestStampAcceptsTheShapesThisRepositoryTagsThroughTheReleaseTemplate(t *testing.T) {
	t.Parallel()
	// The positive control of the tag alphabet: a legal tag inside the template,
	// named the way a release names its artifacts, asserts every tool, so the
	// refusals above are the alphabet and not the fixture.
	for _, tag := range []string{"v0.13.0", "v0.13.0-rc1", "v0.0.0-dry-run"} {
		w := newHarness(t)
		w.tools("nova-bus", "nova-tokens")
		for _, n := range []string{"nova-bus", "nova-tokens"} {
			w.write(fmt.Sprintf("dist/%s_%s_linux_amd64", n, tag), "#!binary\n", 0o755)
		}
		w.runner.output = func(c command) (string, int) {
			n := strings.SplitN(filepath.Base(c.name), "_", 2)[0]
			return fmt.Sprintf("%s %s linux/amd64\n", n, tag), 0
		}
		w.wantRC(w.do("stamp", tag, "dist/%s_"+tag+"_linux_amd64"), 0)
		w.mustContain("asserted the " + tag + " stamp on 2 of 2 shipped tools")
	}
}

func TestStampTakesAnAlternateCmdDirectory(t *testing.T) {
	t.Parallel()
	w := newHarness(t)
	w.write("elsewhere/nova-bus/main.go", "package main\n", 0o644)
	w.write("bin/nova-bus", "#!binary\n", 0o755)
	w.runner.output = func(command) (string, int) { return "nova-bus v1\n", 0 }
	w.wantRC(w.do("stamp", "v1", "bin/%s", "elsewhere"), 0)
	w.mustContain("on 1 of 1 shipped tools")
}

func TestStampFailsWhenTheCmdDirectoryHoldsNoTool(t *testing.T) {
	t.Parallel()
	w := newHarness(t)
	w.write("cmd/AGENTS.md", "no tool here\n", 0o644)
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain("FAIL: cmd/ matched no tool directories; this check asserted nothing and would pass")
}

func TestTheShippedExemptionListIsEmpty(t *testing.T) {
	t.Parallel()
	if len(legacyNoVersionVerb) != 0 {
		t.Fatalf("legacyNoVersionVerb = %v; every shipped tool is required to report the tag", legacyNoVersionVerb)
	}
}

func TestStampAnExemptToolIsNamedAndNotedNeverSkippedInSilence(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v1", "nova-bus", "nova-old", "nova-mute")
	w.e.legacy = []string{"nova-old", "nova-mute"}
	w.answers["nova-old"] = answer{out: "nova-old sha256:abc\n"}
	w.answers["nova-mute"] = answer{out: "unknown verb version\n", rc: 2}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 0)
	w.mustContain("NOTE: nova-old prints an identity that is not the release stamp: nova-old sha256:abc\n")
	w.mustContain("NOTE: nova-mute has no version print today: `nova-mute version` exited 2: unknown verb version\n")
	w.mustContain("NOTE:   it is exempt by name until the common version verb lands: #121\n")
	w.mustContain("asserted the v1 stamp on 1 of 3 shipped tools (2 exempt until #121)\n")
}

func TestStampAStaleExemptionIsRefusedByNameBeforeAnythingRuns(t *testing.T) {
	t.Parallel()
	// The list is empty when shipped, so the fixture brings its own name.
	w := newStampWorld(t, "v1", "nova-bus")
	w.e.legacy = []string{"nova-departed"}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain("FAIL: nova-departed is exempted from the stamp assertion but cmd/nova-departed does not exist")
	if n := len(w.runner.called()); n != 0 {
		t.Fatalf("%d binaries run before a stale exemption was refused", n)
	}
}

func TestStampFailsWhenEveryShippedToolIsExempt(t *testing.T) {
	t.Parallel()
	w := newStampWorld(t, "v1", "nova-bus")
	w.e.legacy = []string{"nova-bus"}
	w.wantRC(w.do("stamp", "v1", "bin/%s"), 1)
	w.mustContain("FAIL: every one of the 1 shipped tools is on the exemption list")
}
