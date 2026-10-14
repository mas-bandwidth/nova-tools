package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SHAPE, asserted field by field. `nova-self-talk <identity> <goos>/<goarch> <go version>` is
// what a person is asked to paste when two lines disagree about what they are running, so
// a run of it has to be one line and four tokens -- and asserting only that the output
// "contains" a version would pass over a line broken in two, which is the failure the
// escaping in pkg/buildinfo exists to prevent.
//
// Before this verb existed the same invocation was:
//
//	nova-self-talk: unknown subcommand "version"; run: nova-self-talk help
//
// on stderr at exit 2.
func TestVersionLineShape(t *testing.T) {
	t.Parallel()
	testkit.VersionLineShape(t, "nova-self-talk", func() (int, string, string) {
		var out, errOut bytes.Buffer
		code := cmdVersion(nil, &out, &errOut)
		return code, out.String(), errOut.String()
	})
}

// TestVersionStamp pins the stamp handling: a stamped build says the tag, an
// unstamped one says what the toolchain recorded, and an embedded newline is
// escaped so the line stays one line and four fields.
func TestVersionStamp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, stamp string
		wantSub     string
		fieldIdx    int
		wantField   string
	}{
		{"newline escaped", "v1.2.3\nnova-self-talk v9.9.9 linux/amd64 go1.0 extra", `v1.2.3\x0a`, -1, ""},
		{"identity is stamp", "v9.9.9", "", 1, "v9.9.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			code := cmdVersionWith(nil, &out, &errOut, tc.stamp)
			require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
			line := out.String()
			require.Equal(t, 1, strings.Count(line, "\n"))
			fields := strings.Fields(strings.TrimSuffix(line, "\n"))
			require.Len(t, fields, 4)
			if tc.wantSub != "" {
				assert.Contains(t, line, tc.wantSub)
			}
			if tc.fieldIdx >= 0 {
				assert.Equal(t, tc.wantField, fields[tc.fieldIdx])
			}
		})
	}
}

func TestVersionRefusesFlagsAndArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--short"}, {"extra"}, {"--max", "0"}} {
		var out, errOut bytes.Buffer
		code := cmdVersion(args, &out, &errOut)
		assert.Equal(t, 2, code, "%v: exit %d, want 2", args, code)
		assert.Zero(t, out.Len(), "%v: a refusal printed a version line anyway: %q", args, out.String())
		assert.Contains(t, errOut.String(), "takes no flags and no arguments", "%v: refusal does not say why: %q", args, errOut.String())
	}
}

// The wiring, which lives in main.go's dispatch and is one line there: this test is what
// will catch it if that line is ever removed.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	t.Parallel()
	// `version` is recognised as the WHOLE invocation, the way `help` is: this tool takes
	// files positionally, so it is a verb only when it stands alone.
	for _, verb := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		code := run([]string{verb}, &out, &errOut)
		if !assert.Equal(t, 0, code, "%s: exit %d, want 0\nstderr: %s", verb, code, errOut.String()) {
			continue
		}
		assert.True(t, strings.HasPrefix(out.String(), "nova-self-talk "), "%s: not the version line: %q", verb, out.String())
		assert.Equal(t, 1, strings.Count(out.String(), "\n"), "%s: not the version line: %q", verb, out.String())
	}
	// And a file actually named `version`, named beside another file, is still a FILE:
	// the verb took nothing away from the scan.
	dir := t.TempDir()
	for _, name := range []string{"version", "other.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("# a file\n"), 0o644))
	}
	var out, errOut bytes.Buffer
	code := run([]string{filepath.Join(dir, "version"), filepath.Join(dir, "other.md")}, &out, &errOut)
	require.Equal(t, 0, code, "two named files: exit %d, want 0\nstderr: %s", code, errOut.String())
	assert.NotContains(t, out.String(), runtime.Version(), "a file named version was answered with the version line: %q", out.String())
}
