package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// The resolution order, one case per rank, stamped and unstamped. An order asserted only
// by the build the test happens to run under is asserted by one case out of four: a test
// cannot install itself from a module proxy or rebuild itself from a dirty tree, which is
// why Resolve takes the build information rather than reading it.
func TestResolveOrder(t *testing.T) {
	t.Parallel()
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v1.4.0"}}
	built := func(settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: settings}
	}
	revision := debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"}
	stamp := debug.BuildSetting{Key: "vcs.time", Value: "2026-09-09T11:22:33Z"}
	dirty := debug.BuildSetting{Key: "vcs.modified", Value: "true"}
	clean := debug.BuildSetting{Key: "vcs.modified", Value: "false"}

	cases := []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"the ldflags stamp wins over everything", "v2.0.0", installed, true, "v2.0.0"},
		{"and over a vcs build", "v2.0.0", built(revision, stamp, clean), true, "v2.0.0"},
		{"a stamp of only spaces is no stamp", "   ", installed, true, "v1.4.0"},
		{"an installed module version", "", installed, true, "v1.4.0"},
		{"a vcs build is time then short revision", "", built(revision, stamp, clean), true, "20260909112233-0123456789ab"},
		{"an edited tree says so", "", built(revision, stamp, dirty), true, "20260909112233-0123456789ab-dirty"},
		{"a revision with no time is still an answer", "", built(revision), true, "0123456789ab"},
		{"an unparseable time falls back to the revision", "", built(revision, debug.BuildSetting{Key: "vcs.time", Value: "yesterday"}), true, "0123456789ab"},
		{"no revision at all is the floor", "", built(clean), true, "devel"},
		{"no build information at all is the floor", "", nil, false, "devel"},
		{"(devel) alone is the floor, not a version", "", built(), true, "devel"},
	}
	for _, c := range cases {
		if got := Resolve(c.stamped, c.info, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Nothing here invents a dotted number. The floor is the word every binary in the set
// spells the same way, and a reader who sees it knows the build's origin is unrecorded
// rather than guessing that 0.0.0 means something.
func TestTheFloorIsAWordAndNotANumber(t *testing.T) {
	t.Parallel()
	if strings.ContainsAny(Unknown, "0123456789.") {
		t.Errorf("the floor reads as a version number: %q", Unknown)
	}
}

// The one line, field by field, stamped and unstamped. Asserting only that the output
// "contains" the version would pass over a line broken in two, which is the failure the
// escaping exists to prevent.
func TestLineShape(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, stamped, wantVersion string }{
		{"stamped", "v9.9.9", "v9.9.9"},
		{"unstamped", "", ""},
	} {
		line := Line("nova-example", c.stamped)
		if strings.ContainsAny(line, "\n\r") {
			t.Fatalf("%s: not one line: %q", c.name, line)
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			t.Fatalf("%s: want 4 fields, got %d: %q", c.name, len(fields), line)
		}
		if fields[0] != "nova-example" {
			t.Errorf("%s: field 1 is the binary's name: got %q", c.name, fields[0])
		}
		if c.wantVersion != "" && fields[1] != c.wantVersion {
			t.Errorf("%s: field 2: got %q, want %q", c.name, fields[1], c.wantVersion)
		}
		if fields[1] == "" {
			t.Errorf("%s: field 2 is the identity and is never empty: %q", c.name, line)
		}
		if want := runtime.GOOS + "/" + runtime.GOARCH; fields[2] != want {
			t.Errorf("%s: field 3: got %q, want %q", c.name, fields[2], want)
		}
		if fields[3] != runtime.Version() {
			t.Errorf("%s: field 4: got %q, want %q", c.name, fields[3], runtime.Version())
		}
	}
}

// The stamp is the ONE field that comes from outside the toolchain, and a release
// workflow's ${TAG} is a shell variable: a build that stamped a newline or a space into
// it must not be able to make this line say two things, or make a build date land in the
// slot a reader takes for an architecture.
func TestLineHoldsWhateverTheStampContains(t *testing.T) {
	t.Parallel()
	line := Line("nova-example", "v1.2.3\nnova-example v9.9.9 linux/amd64 go1.0 extra")
	if strings.ContainsAny(line, "\n\r") {
		t.Fatalf("a stamped newline broke the line in two: %q", line)
	}
	if fields := strings.Fields(line); len(fields) != 4 {
		t.Fatalf("want 4 fields whatever the stamp holds, got %d: %q", len(fields), line)
	}
	if !strings.Contains(line, `v1.2.3\x0a`) {
		t.Errorf("the stamp is escaped rather than dropped or printed raw: %q", line)
	}
}
