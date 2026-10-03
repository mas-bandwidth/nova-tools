package main

import (
	"bytes"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SHAPE, asserted field by field. `nova-bus <version> <goos>/<goarch> <go version>` is
// what a person is asked to paste when two lines on one bus disagree, so a run of it has
// to be one line and four tokens -- and asserting only that the output "contains" the
// version would pass over a line broken in two, which is the failure this verb's own
// escaping exists to prevent.
func TestVersionLineShape(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	{
		code := busTool().Run([]string{"version"}, strings.NewReader(""), &out, &errOut)
		require.Equalf(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	assert.Falsef(t, errOut.Len() != 0, "wrote to stderr: %q", errOut.String())
	line := out.String()
	require.Falsef(t, !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1, "want exactly one terminated line, got %q", line)
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	require.Equalf(t, 4, len(fields), "want 4 fields, got %d: %q", len(fields), line)
	assert.Equalf(t, "nova-bus", fields[0], "field 1 is the binary's name: got %q", fields[0])
	assert.NotEmptyf(t, fields[1], "field 2 is the version and is never empty: %q", line)
	{
		want := runtime.GOOS + "/" + runtime.GOARCH
		assert.Equalf(t, want, fields[2], "field 3: got %q, want %q", fields[2], want)
	}
	assert.Equalf(t, runtime.Version(), fields[3], "field 4: got %q, want %q", fields[3], runtime.Version())
}

// The stamp is the ONE field that comes from outside the toolchain, and a release
// workflow's ${TAG} is a shell variable: a build that stamped a newline or a space into it
// must not be able to make this line say two things, or make a build date land in the
// slot a reader takes for an architecture.
func TestVersionLineHoldsWhateverTheStampContains(t *testing.T) {
	t.Parallel()
	const ver = "v1.2.3\nnova-bus v9.9.9 linux/amd64 go1.0 extra"

	tool := busTool()
	tool.Stamp = ver
	var out, errOut bytes.Buffer
	{
		code := tool.Run([]string{"version"}, strings.NewReader(""), &out, &errOut)
		require.Equalf(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	line := out.String()
	require.Falsef(t, strings.Count(line, "\n") != 1, "a stamped newline broke the line in two: %q", line)
	{
		fields := strings.Fields(strings.TrimSuffix(line, "\n"))
		require.Equalf(t, 4, len(fields), "want 4 fields whatever the stamp holds, got %d: %q", len(fields), line)
	}
	assert.Containsf(t, line, `v1.2.3\x0a`, "the stamp is escaped rather than dropped or printed raw: %q", line)
}

func TestVersionRefusesFlagsAndArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--short"}, {"extra"}, {"--bus", "."}} {
		var out, errOut bytes.Buffer
		{
			code := busTool().Run(append([]string{"version"}, args...), strings.NewReader(""), &out, &errOut)
			assert.Equalf(t, 2, code, "%v: exit %d, want 2", args, code)
		}
		assert.Falsef(t, out.Len() != 0, "%v: a refusal printed a version line anyway: %q", args, out.String())
		assert.NotEmptyf(t, errOut.String(), "%v: refusal does not say why: %q", args, errOut.String())
	}
}

// The order in version.go's header, one case per rank, because an order asserted only by
// the build the test happens to run under is asserted by one case out of four.
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
		{
			got := resolveVersion(c.stamped, c.info, c.ok)
			assert.Equalf(t, c.want, got, "%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The wiring, which lives in main.go's dispatch and is one line there. This test is the
// thing that will catch it if that line is ever removed.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"version", "--version"} {
		r := invoke(t, "", verb)
		if r.code != 0 {
			assert.Failf(t, "assertion failed", "%s: exit %d, want 0\nstderr: %s", verb, r.code, r.stderr)
			continue
		}
		assert.Falsef(t, !strings.HasPrefix(r.stdout, "nova-bus ") || strings.Count(r.stdout, "\n") != 1, "%s: not the version line: %q", verb, r.stdout)
	}
}
