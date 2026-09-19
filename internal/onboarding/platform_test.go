package onboarding

import (
	"strings"
	"testing"
)

// A transcript is real output pasted whole, so a section whose output carries a
// value only one platform produces cannot reproduce anywhere else -- the sandbox
// tool's `backend=sandbox-exec` and `abi=` on macOS against a Linux bench's
// `backend=landlock`, `hosts=`, `gpu=`, `used=` and `ancestors=` (#1509). The
// section says which platforms it is for in ONE typed line, so that elsewhere the
// test is a NAMED SKIP rather than a red a reader has to interpret, and so that a
// class test can ask whether every platform named is a leg CI actually runs: a
// skipped transcript is still executed somewhere.

const platformDoc = "## nova-sandbox\n" +
	"\n" +
	"Platform: darwin — recorded on macOS; a Linux bench prints `backend=landlock`.\n" +
	"\n" +
	"### First run\n" +
	"\n" +
	"```\n$ nova-sandbox probe\nPROBE OK\n```\n" +
	"\n" +
	"## nova-bus\n" +
	"\n" +
	"### First run\n" +
	"\n" +
	"```\n$ nova-bus read\nBUS OK n=0\n```\n"

func TestSectionPlatformsReadsTheGoosListAndItsNote(t *testing.T) {
	p, ok, err := SectionPlatforms(platformDoc, "nova-sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the `## nova-sandbox` section carries a Platform: line and none was read")
	}
	if len(p.GOOS) != 1 || p.GOOS[0] != "darwin" {
		t.Errorf("GOOS = %v, want [darwin]", p.GOOS)
	}
	if !strings.Contains(p.Note, "landlock") {
		t.Errorf("the note is dropped: %q; it is the sentence a reader on the wrong bench is shown", p.Note)
	}
}

// A section with no Platform: line reproduces everywhere, which is the ordinary
// case and is not an error.
func TestASectionWithNoPlatformLineIsNotAnError(t *testing.T) {
	p, ok, err := SectionPlatforms(platformDoc, "nova-bus")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("the `## nova-bus` section carries no Platform: line and one was read: %+v", p)
	}
}

// Two platforms, comma separated, is the spec's `<goos>[,<goos>]`.
func TestAPlatformLineMayNameTwoPlatforms(t *testing.T) {
	p, err := ParsePlatformLine(" linux,darwin — both benches print this")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.GOOS) != 2 || p.GOOS[0] != "linux" || p.GOOS[1] != "darwin" {
		t.Errorf("GOOS = %v, want [linux darwin]", p.GOOS)
	}
}

// The line is a GOOS LIST and not a sentence. A line that opens with prose names
// no platform a test can act on: the test cannot skip, the class test cannot ask
// whether CI runs it, and the reader is told nothing they could check.
func TestAPlatformLineThatNamesNoGoosIsRefused(t *testing.T) {
	for _, text := range []string{
		" recorded on macOS (darwin) — the fields below are that Mac's",
		" macOS",
		" — a note and no platform",
		"",
	} {
		if p, err := ParsePlatformLine(text); err == nil {
			t.Errorf("ParsePlatformLine(%q) = %+v, want an error: the line is `Platform: <goos>[,<goos>]`", text, p)
		}
	}
}

// The skip is NAMED: it says the tool, the platforms the section is for, and the
// platform this bench is, so a reader of a skipped run knows what was not run and
// where it does run.
func TestSectionSkipReasonNamesTheToolThePlatformsAndThisBench(t *testing.T) {
	p, _, err := SectionPlatforms(platformDoc, "nova-sandbox")
	if err != nil {
		t.Fatal(err)
	}
	reason := SectionSkipReason("nova-sandbox", p, "linux")
	for _, want := range []string{"nova-sandbox", "darwin", "linux"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the skip reason does not name %q: %q", want, reason)
		}
	}
	if SectionSkipReason("nova-sandbox", p, "darwin") != "" {
		t.Errorf("a section named for this platform must not skip: %q", SectionSkipReason("nova-sandbox", p, "darwin"))
	}
}
