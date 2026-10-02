package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SHAPE, asserted field by field. `nova-check <identity> <goos>/<goarch> <go version>` is
// what a person is asked to paste when two lines disagree about what they are running, so
// a run of it has to be one line and four tokens -- and asserting only that the output
// "contains" a version would pass over a line broken in two, which is the failure the
// escaping in internal/buildinfo exists to prevent.
func TestVersionLineShape(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	{
		code := cmdVersion(nil, &out, &errOut)
		require.EqualValues(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	assert.EqualValues(t, 0, errOut.Len(), "wrote to stderr: %q", errOut.String())
	line := out.String()
	require.True(t, strings.HasSuffix(line, "\n"), "want exactly one terminated line, got %q", line)
	require.Equal(t, 1, strings.Count(line, "\n"), "want exactly one terminated line, got %q", line)
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	require.EqualValues(t, 4, len(fields), "want 4 fields, got %d: %q", len(fields), line)
	assert.EqualValues(t, "nova-check", fields[0], "field 1 is the binary's name: got %q", fields[0])
	assert.NotEqualValues(t, "", fields[1], "field 2 is the build identity and is never empty: %q", line)
	assert.NotContains(t, fields[1], " ", "field 2 is one token: %q", line)
	assert.NotContains(t, fields[1], "\t", "field 2 is one token: %q", line)
	{
		want := runtime.GOOS + "/" + runtime.GOARCH
		assert.EqualValues(t, want, fields[2], "field 3: got %q, want %q", fields[2], want)
	}
	assert.EqualValues(t, runtime.Version(), fields[3], "field 4: got %q, want %q", fields[3], runtime.Version())
}

// The stamp is the ONE field of this line that comes from outside the toolchain, and a
// release workflow's ${TAG} is a shell variable.
func TestVersionLineHoldsWhateverTheStampContains(t *testing.T) {
	t.Parallel()
	const ver = "v1.2.3\nnova-check v9.9.9 linux/amd64 go1.0 extra"

	var out, errOut bytes.Buffer
	{
		code := cmdVersionWith(nil, &out, &errOut, ver)
		require.EqualValues(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	line := out.String()
	require.EqualValues(t, 1, strings.Count(line, "\n"), "a stamped newline broke the line in two: %q", line)
	{
		fields := strings.Fields(strings.TrimSuffix(line, "\n"))
		require.EqualValues(t, 4, len(fields), "want 4 fields whatever the stamp holds, got %d: %q", len(fields), line)
	}
	assert.Contains(t, line, `v1.2.3\x0a`, "the stamp is escaped rather than dropped or printed raw: %q", line)
}

// A stamped build says the tag and an unstamped one says what the toolchain recorded:
// either way field two is an identity, never a dotted number this file made up.
func TestVersionIdentityIsTheStampWhenThereIsOne(t *testing.T) {
	t.Parallel()
	const ver = "v9.9.9"
	var out, errOut bytes.Buffer
	{
		code := cmdVersionWith(nil, &out, &errOut, ver)
		require.EqualValues(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	{
		got := strings.Fields(out.String())[1]
		assert.EqualValues(t, "v9.9.9", got, "field 2 is the stamp: got %q", got)
	}
}

func TestVersionRefusesFlagsAndArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--short"}, {"extra"}, {"--dir", "."}} {
		var out, errOut bytes.Buffer
		{
			code := cmdVersion(args, &out, &errOut)
			assert.EqualValues(t, 2, code, "%v: exit %d, want 2", args, code)
		}
		assert.EqualValues(t, 0, out.Len(), "%v: a refusal printed a version line anyway: %q", args, out.String())
		assert.Contains(t, errOut.String(), "takes no flags and no arguments", "%v: refusal does not say why: %q", args, errOut.String())
	}
}

// The wiring, which lives in main.go's dispatch and is one line there: this test is what
// will catch it if that line is ever removed.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		{
			code := run([]string{verb}, &out, &errOut)
			if !assert.EqualValues(t, 0, code, "%s: exit %d, want 0\nstderr: %s", verb, code, errOut.String()) {
				continue
			}
		}
		assert.True(t, strings.HasPrefix(out.String(), "nova-check "), "%s: not the version line: %q", verb, out.String())
		assert.Equal(t, 1, strings.Count(out.String(), "\n"), "%s: not the version line: %q", verb, out.String())
	}
}
