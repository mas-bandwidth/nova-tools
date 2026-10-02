package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The help's example lines as a stranger meets them at a shell prompt: this command is
// built into a temp dir, and each line runs through `sh -c` from a directory of its own,
// the built binary first on PATH and stdin closed. It builds rather than calls the package
// because that is the question; the environment is sanitized with goenv.Clean because the
// parent's GOFLAGS can reshape a go command's output under the parser that reads it
// (internal/goenv, the `goenv` class test).
func TestHelpFoldExampleIsComparedToOutput(t *testing.T) {
	t.Parallel()

	bin := filepath.Join(t.TempDir(), "nova-tokens")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = goenv.Clean(os.Environ())
	out, err := build.CombinedOutput()
	require.NoError(t, err, "building nova-tokens: %s", out)
	sh := func(dir, line string) (int, string) {
		cmd := exec.Command("sh", "-c", line)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), string(out)
		}
		require.NoError(t, err, "running %q", line)
		return 0, string(out)
	}
	examples, err := onboarding.ExampleLines(usage, "nova-tokens")
	require.NoError(t, err)

	// The documented source-free fold example is compared with the real typed output from
	// the known transcript fixture, so a flag, accounting or output change updates the
	// contract. The built binary stamps the instant it ran and the build it is: both are
	// the run's.
	t.Run("the fold line prints what the help says", func(t *testing.T) {
		t.Parallel()
		line := "nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts"
		assert.Contains(t, examples, line)
		exit, output := sh(exampleBench(t), line)
		step := onboarding.Step{Line: "$ " + line, Want: []string{
			"TOKENS FOLD at=2026-09-11T23:55:02Z build=devel out=./out sources=1 days=2026-09-11 repos=./repos.tsv",
			"TOKENS SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=3 dup=1 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=2",
			"TOKENS DAY date=2026-09-11 rows=2 models=1 repos=2 turns=3 unknown=0.0% other=0.0% rough=0 dashes=3 nonutc=0 sources=claude:bench written=true",
			"TOKENS OK days=1 rows=2 sources=1 unreadable=0 unparsed=0 mixed=0 conflict=0 shrank=0 partial=0 quiet=0",
			"TOKENS NOTE nothing was wrong; nova-tokens check --out ./out is the gate",
		}}
		assert.Empty(t, onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{{Code: exit, Stdout: output}},
			[]onboarding.Field{{Name: "at"}, {Name: "build"}}))
	})

	// Every example line runs, as printed, after the setup line above the block in an
	// otherwise empty directory, and exits 0. An example line that names an input the
	// reader has not made exits 2, and an example exiting 2 is a broken example
	// (ONBOARDING point 1). The banner is read AS
	// SOURCE, the `usage` beside this test, because the lines under test are the ones a
	// reader pastes. No line here pushes, publishes, contacts a forge, acts on a machine or
	// needs a key, so none is skipped by name: a skip would be a hole. SCOPE, said out loud:
	// this covers nova-tokens's own block and nothing else.
	t.Run("every example line runs as printed", func(t *testing.T) {
		t.Parallel()
		setup := onboarding.SetupLine(usage)
		require.True(t, strings.HasPrefix(setup, "mkdir -p ./transcripts"), "the usage banner has no fixture setup line above the block, so a stranger pasting it names inputs they have not made")
		assert.NotContains(t, setup, "cmd/nova-tokens/testdata", "the setup still depends on a source checkout")
		root := t.TempDir()
		exit, out := sh(root, setup)
		require.Equal(t, 0, exit, "the fixture setup line:\n  %s\n%s", setup, out)
		for _, line := range examples {
			exit, out := sh(root, line)
			assert.Equal(t, 0, exit, "the example `%s` -- a line a stranger pastes must run as printed:\n%s", line, out)
		}
	})
}
