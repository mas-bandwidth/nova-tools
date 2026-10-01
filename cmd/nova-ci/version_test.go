package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shape, asserted field by field. `nova-ci <version> <goos>/<goarch> <go version>` is
// the one line every shipped binary prints, so a release assertion can read the identity
// out of field two without knowing which tool wrote it -- and asserting only that the
// output "contains" the version would pass over a line broken in two.
func TestVersionLineShape(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cmdVersion(nil, &stdout, &stderr)
	require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, stderr.String())
	assert.Zero(t, stderr.Len(), "wrote to stderr: %q", stderr.String())
	line := stdout.String()
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("want exactly one terminated line, got %q", line)
	}
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	require.Len(t, fields, 4, "want 4 fields, got %d: %q", len(fields), line)
	assert.Equal(t, "nova-ci", fields[0], "field 1 is the binary's name: got %q", fields[0])
	assert.NotEmpty(t, fields[1], "field 2 is the version and is never empty: %q", line)
	if want := runtime.GOOS + "/" + runtime.GOARCH; fields[2] != want {
		t.Errorf("field 3: got %q, want %q", fields[2], want)
	}
	if fields[3] != runtime.Version() {
		t.Errorf("field 4: got %q, want %q", fields[3], runtime.Version())
	}
}

// The stamp is the ONE field that comes from outside the toolchain: a release's ${TAG} is
// a shell variable, and a build stamped with a newline must not make this line say two
// things.
func TestVersionLineHoldsWhateverTheStampContains(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_CI_VERSION_STAMP_CHILD") == "1" {
		saved := version
		t.Cleanup(func() { version = saved })
		version = "v1.2.3\nnova-ci v9.9.9 linux/amd64 go1.0 extra"

		var stdout, stderr bytes.Buffer
		code := cmdVersion(nil, &stdout, &stderr)
		require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, stderr.String())
		line := stdout.String()
		require.Equal(t, 1, strings.Count(line, "\n"), "a stamped newline broke the line in two: %q", line)
		fields := strings.Fields(strings.TrimSuffix(line, "\n"))
		require.Len(t, fields, 4, "want 4 fields whatever the stamp holds, got %d: %q", len(fields), line)
		assert.Contains(t, line, `v1.2.3\x0a`, "the stamp is escaped rather than dropped or printed raw: %q", line)
		return
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVersionLineHoldsWhateverTheStampContains$", "-test.count=1")
	child.Env = append(os.Environ(), "NOVA_CI_VERSION_STAMP_CHILD=1")
	child.WaitDelay = 2 * time.Second
	out, err := child.CombinedOutput()
	require.NoError(t, err, "the isolated release-stamp test failed:\n%s", out)
}

// Both spellings reach the verb through the dispatch, from the day the verb lands.
func TestVersionVerbIsReachableFromTheDispatch(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{"version", "--version"} {
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{verb}, strings.NewReader(""), &stdout, &stderr)
			require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, stderr.String())
			assert.True(t, strings.HasPrefix(stdout.String(), "nova-ci "), "not the version line: %q", stdout.String())
			assert.Equal(t, 1, strings.Count(stdout.String(), "\n"), "not one line: %q", stdout.String())
		})
	}
}

func TestVersionRefusesFlagsAndArguments(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"--budget", "60"}, {"extra"}} {
		var stdout, stderr bytes.Buffer
		code := cmdVersion(args, &stdout, &stderr)
		assert.Equal(t, 2, code, "%v: exit %d, want 2", args, code)
		assert.Zero(t, stdout.Len(), "%v: a refusal printed a version line anyway: %q", args, stdout.String())
		assert.Contains(t, stderr.String(), "takes no flags and no arguments", "%v: refusal does not say why: %q", args, stderr.String())
	}
}

// The banner names it, because a verb a reader cannot find is a verb that answers nobody.
func TestVersionIsInTheBanner(t *testing.T) {
	t.Parallel()

	assert.Contains(t, usage, "nova-ci version", "the usage block does not list the version verb")
}
