package main

import (
	"bytes"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// The SHAPE, asserted field by field. `nova-board <version> <goos>/<goarch> <go version>` is
// what a person is asked to paste when two lines on one bus disagree, so a run of it has
// to be one line and four tokens -- and asserting only that the output "contains" the
// version would pass over a line broken in two, which is the failure this verb's own
// escaping exists to prevent.
func TestVersionLineShape(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cmdVersion(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("wrote to stderr: %q", errOut.String())
	}
	line := out.String()
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("want exactly one terminated line, got %q", line)
	}
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	if len(fields) != 4 {
		t.Fatalf("want 4 fields, got %d: %q", len(fields), line)
	}
	if fields[0] != "nova-board" {
		t.Errorf("field 1 is the binary's name: got %q", fields[0])
	}
	if fields[1] == "" {
		t.Errorf("field 2 is the version and is never empty: %q", line)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; fields[2] != want {
		t.Errorf("field 3: got %q, want %q", fields[2], want)
	}
	if fields[3] != runtime.Version() {
		t.Errorf("field 4: got %q, want %q", fields[3], runtime.Version())
	}
}

// The stamp is the ONE field that comes from outside the toolchain, and a release
// workflow's ${TAG} is a shell variable: a build that stamped a newline or a space into it
// must not be able to make this line say two things, or make a build date land in the
// slot a reader takes for an architecture.
func TestVersionLineHoldsWhateverTheStampContains(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3\nnova-board v9.9.9 linux/amd64 go1.0 extra"

	var out, errOut bytes.Buffer
	if code := cmdVersion(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	line := out.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("a stamped newline broke the line in two: %q", line)
	}
	if fields := strings.Fields(strings.TrimSuffix(line, "\n")); len(fields) != 4 {
		t.Fatalf("want 4 fields whatever the stamp holds, got %d: %q", len(fields), line)
	}
	if !strings.Contains(line, `v1.2.3\x0a`) {
		t.Errorf("the stamp is escaped rather than dropped or printed raw: %q", line)
	}
}

func TestVersionRefusesFlagsAndArguments(t *testing.T) {
	for _, args := range [][]string{{"--short"}, {"extra"}, {"--bus", "."}} {
		var out, errOut bytes.Buffer
		if code := cmdVersion(args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v: a refusal printed a version line anyway: %q", args, out.String())
		}
		if !strings.Contains(errOut.String(), "takes no flags and no arguments") {
			t.Errorf("%v: refusal does not say why: %q", args, errOut.String())
		}
	}
}

// The order in version.go's header, one case per rank, because an order asserted only by
// the build the test happens to run under is asserted by one case out of four.
func TestVersionResolvesInOrder(t *testing.T) {
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
		if got := resolveVersion(c.stamped, c.info, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The wiring, in main.go's dispatch. nova-board routes the verb from the day the file
// lands, so this is an assertion rather than the skip nova-bus's version test carried
// while its own dispatch line was owed.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	for _, verb := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		code := run([]string{verb}, &out, &errOut, at(t, "2026-09-11T10:00:00Z"), &seq{})
		if code != 0 {
			t.Errorf("%s: exit %d, want 0\nstderr: %s", verb, code, errOut.String())
			continue
		}
		if !strings.HasPrefix(out.String(), "nova-board ") || strings.Count(out.String(), "\n") != 1 {
			t.Errorf("%s: not the version line: %q", verb, out.String())
		}
	}
}

// The banner names it, because a verb a reader cannot find is a verb that answers nobody.
func TestVersionIsInTheBanner(t *testing.T) {
	if !strings.Contains(usage, "nova-board version") {
		t.Error("the usage block does not list the version verb")
	}
}
