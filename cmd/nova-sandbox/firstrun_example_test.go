package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHelpFirstRunLinesProveTheWall runs the help's `example:` block through
// the comparator, on macOS where the block's backend is sandbox-exec. /tmp/trial
// stands for a directory of this test's own; the mkdir is the mkdir; each walled
// line runs with the HOME it carries and nothing else in its environment; and
// the file the last line writes is read back, so the block is shown to put
// words inside the wall, not only to print OK.
func TestHelpFirstRunLinesProveTheWall(t *testing.T) {
	t.Parallel()

	// firstRunBlock is the help's `example:` block as a stranger pastes it: the
	// backend, a scratch directory with a HOME inside it, a probe that proves the
	// wall, and one command that writes inside it (ONBOARDING.md point 6).
	firstRunBlock := []string{
		"nova-sandbox check",
		"mkdir -p /tmp/trial/home",
		"HOME=/tmp/trial/home nova-sandbox probe --write /tmp/trial",
		"HOME=/tmp/trial/home nova-sandbox --write /tmp/trial -- /bin/sh -c 'echo inside > /tmp/trial/out'",
	}

	// firstRunWant is what the two walled lines print. A line opening `! ` is
	// standard error. The probe's paths (the pid-named control file, this test
	// binary) and the wrapped run's working directory and its depth are this run's
	// own, and are the only values not compared; the one delete root (deletes=,
	// docs/SPEC-SANDBOX.md) is the --write root, compared as the block's /tmp/trial.
	firstRunWant := map[string][]string{
		firstRunBlock[2]: {
			"PROBE STEP name=write_outside_control expect=allow got=allow path=-",
			"PROBE STEP name=write_outside expect=deny got=deny path=-",
			"PROBE STEP name=write_inside expect=allow got=allow path=-",
			"PROBE STEP name=read_root expect=allow got=allow path=-",
			"PROBE OK backend=sandbox-exec abi=- steps=4 passed=4 net=nopromise gpu=none",
		},
		firstRunBlock[3]: {
			"! SANDBOX OK backend=sandbox-exec abi=- read=0 read-noexec=0 write=1 net=nopromise cwd=- cwdb64=- ancestors=- cmd=sh gpu=none deletes=/tmp/trial",
		},
	}

	_, tail, _ := strings.Cut(usage, onboarding.ExampleHeading)
	var block []string
	for _, line := range strings.Split(tail, "\n") {
		if strings.TrimSpace(line) == "" {
			break
		}
		block = append(block, strings.TrimSpace(line))
	}
	require.Equal(t, strings.Join(firstRunBlock, "\n"), strings.Join(block, "\n"), "the help's example: block is not the sitting this test runs\nhelp:\n  %s\nwant:\n  %s", strings.Join(block, "\n  "), strings.Join(firstRunBlock, "\n  "))
	got := onboarding.ExampleCommands(usage, "nova-sandbox")
	require.Len(t, got, 3, "the block runs nova-sandbox %d times, want 3: %q", len(got), got)
	needDarwin(t)

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	trial := filepath.Join(base, "trial")
	elide := func(name, pattern, as string) onboarding.Norm {
		n, err := onboarding.Elide(name, pattern, as)
		require.NoError(t, err)
		return n
	}
	norms := []onboarding.Norm{
		elide("a probe step's path (a pid-named file, this test binary)", `path=\S+`, "path=-"),
		elide("the run's working directory", `cwd=\S+`, "cwd=-"),
		elide("the run's working directory, base64", `cwdb64=\S+`, "cwdb64=-"),
		elide("the working directory's depth", `ancestors=\d+`, "ancestors=-"),
		onboarding.Path("/tmp/trial", trial),
	}
	for _, line := range firstRunBlock[1:] {
		local := strings.ReplaceAll(line, "/tmp/trial", trial)
		if dir, ok := strings.CutPrefix(local, "mkdir -p "); ok {
			require.NoError(t, os.MkdirAll(dir, 0o755))
			continue
		}
		words, err := onboarding.SplitShell(local)
		require.NoError(t, err)
		home, ok := strings.CutPrefix(words[0], "HOME=")
		require.True(t, ok, "the example %q is not a HOME= and a nova-sandbox command", line)
		require.GreaterOrEqual(t, len(words), 2, "the example %q is not a HOME= and a nova-sandbox command", line)
		require.Equal(t, "nova-sandbox", words[1], "the example %q is not a HOME= and a nova-sandbox command", line)
		var out, errb bytes.Buffer
		code := run(words[2:], strings.NewReader(""), &out, &errb, []string{"HOME=" + home, "PATH=/usr/bin:/bin"})
		step := onboarding.Step{Line: "$ " + line, Want: firstRunWant[line]}
		for _, p := range onboarding.Compare(step, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, norms) {
			t.Error(p)
		}
		assert.Equal(t, 0, code, "the example %q exits %d, want 0\nstdout: %s\nstderr: %s", line, code, out.String(), errb.String())
	}
	wrote, err := os.ReadFile(filepath.Join(trial, "out"))
	assert.NoError(t, err, "the walled command wrote %q (%v) inside the wall, want %q", wrote, err, "inside\n")
	assert.Equal(t, "inside\n", string(wrote), "the walled command wrote %q (%v) inside the wall, want %q", wrote, err, "inside\n")
}
