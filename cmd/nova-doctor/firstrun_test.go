package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// The onboarding standard (docs/ONBOARDING.md), pinned for this binary: a bare
// command refuses and names its door, and docs/TESTS.md's first run is compared
// against what the tool prints, line for line, by the one comparator.

// documentedHome is the home directory the TESTS.md block was recorded under.
const documentedHome = "/home/you"

// homeEnv is the real Env with HOME at the sitting's own directory and no
// NOVA_FRIEND_HOME, so the harness check reads a home with no friend rows and
// nothing of the machine running the test.
type homeEnv struct {
	doctor.OSEnv
	home string
}

func (e homeEnv) Getenv(k string) string {
	switch k {
	case "HOME":
		return e.home
	case "NOVA_FRIEND_HOME":
		return ""
	}
	return e.OSEnv.Getenv(k)
}

// sitting runs this binary's entry point over the default registry, the one the
// binary runs, in an empty home of the test's own.
func sitting(t *testing.T) (func(args []string) onboarding.Result, string) {
	t.Helper()
	home := t.TempDir()
	return func(args []string) onboarding.Result {
		var out, errb bytes.Buffer
		code := doctor.Main(doctor.Default, homeEnv{home: home}, "", args, strings.NewReader(""), &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	}, home
}

// (a) A bare command refuses in one line, on stderr at exit 2, names its door,
// and runs no check.
func TestABareCommandNamesItsDoor(t *testing.T) {
	t.Parallel()
	run, _ := sitting(t)
	res := run(nil)
	assert.Equal(t, 2, res.Code)
	assert.Empty(t, res.Stdout, "a bare nova-doctor ran its checks")
	assert.Equal(t, 1, strings.Count(res.Stderr, "\n"), res.Stderr)
	assert.Contains(t, res.Stderr, "REFUSED")
	assert.Contains(t, res.Stderr, "run: nova-doctor help")
}

// (b) The docs/TESTS.md first run is EXECUTED, every command in order, and its
// whole output compared with the block by the one comparator; the one value not
// compared as written is the home directory the run reads.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-doctor")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-doctor", lines)
	require.NoError(t, err)
	run, home := sitting(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "tmpdir", Doc: documentedHome, Run: home}}))
}

// bareEnv is a machine with nothing on it but an empty home: no variable set, no
// file, no program and no network, so every check answers from what is missing
// and none starts a process or opens a socket.
type bareEnv struct{ home string }

func (e bareEnv) Getenv(k string) string {
	if k == "HOME" {
		return e.home
	}
	return ""
}
func (bareEnv) Now() time.Time                             { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) }
func (bareEnv) ReadFile(string) ([]byte, error)            { return nil, fs.ErrNotExist }
func (bareEnv) ReadDir(string) ([]fs.DirEntry, error)      { return nil, fs.ErrNotExist }
func (bareEnv) Dial(context.Context, string, string) error { return errors.New("no network here") }
func (bareEnv) Exec(context.Context, string, ...string) (string, error) {
	return "", errors.New("no program here")
}

// The banner's example block is a first run of at least three lines, and each
// line is a run the tool takes, never a refusal: on a bare machine every check
// says what is missing, with its fix line, and nothing is refused.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	run := func(args []string) onboarding.Result {
		var out, errb bytes.Buffer
		code := doctor.Main(doctor.Default, bareEnv{home: home}, "", args, strings.NewReader(""), &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	}
	banner := run([]string{"help"})
	require.Equal(t, 0, banner.Code, banner.Stderr)
	examples, err := onboarding.ExampleLines(banner.Stdout, "nova-doctor")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(examples), onboarding.MinExampleCommands, "the example block: %q", examples)
	for _, ex := range examples {
		fields, err := onboarding.SplitShell(ex)
		require.NoError(t, err)
		res := run(fields[1:])
		assert.NotContains(t, res.Stderr, "REFUSED", "the example %q was refused", ex)
		assert.NotEmpty(t, res.Stdout, "the example %q printed no result", ex)
	}
}
