package main

import (
	"bytes"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// TestVersionIsBuildIdentity pins the issue: `nova-pulse version` printed "nova-pulse dev",
// a hand-written four-character floor that is wrong exactly at the commit after the
// release. It must print the same identity line as every other tool -- four tokens,
// field two the build identity read from the binary itself, never the four-character
// "dev" this file invented.
func TestVersionIsBuildIdentity(t *testing.T) {
	t.Parallel()
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
	if fields[0] != "nova-pulse" {
		t.Errorf("field 1 is the binary's name: got %q", fields[0])
	}
	if fields[1] == "" {
		t.Errorf("field 2 is the build identity and is never empty: %q", line)
	}
	if fields[1] == "dev" {
		t.Errorf("field 2 is the build identity, not the four-character dev: %q", line)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; fields[2] != want {
		t.Errorf("field 3: got %q, want %q", fields[2], want)
	}
	if fields[3] != runtime.Version() {
		t.Errorf("field 4: got %q, want %q", fields[3], runtime.Version())
	}
}

// The stamp is the ONE field of this line that comes from outside the toolchain, and a
// release workflow's ${TAG} is a shell variable.
func TestVersionLineHoldsWhateverTheStampContains(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3\nnova-pulse v9.9.9 linux/amd64 go1.0 extra"

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
	t.Parallel()
	for _, args := range [][]string{{"--short"}, {"extra"}, {"--dir", "."}} {
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
// the build the test happens to run under is asserted by one case out of four. This is the
// issue's own test: the module version is what field two holds, and the four-character
// "dev" this binary used to print is nowhere in any rank.
func TestVersionResolvesInOrder(t *testing.T) {
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
		if got := resolveVersion(c.stamped, c.info, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The wiring, which lives in main.go's dispatch and is one line there: this test is what
// will catch it if that line is ever removed.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		if code := run([]string{verb}, &out, &errOut, time.Now().UTC()); code != 0 {
			t.Errorf("%s: exit %d, want 0\nstderr: %s", verb, code, errOut.String())
			continue
		}
		if !strings.HasPrefix(out.String(), "nova-pulse ") || strings.Count(out.String(), "\n") != 1 {
			t.Errorf("%s: not the version line: %q", verb, out.String())
		}
	}
}
