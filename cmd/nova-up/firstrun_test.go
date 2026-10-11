package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/up"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// The onboarding standard (docs/ONBOARDING.md), pinned for this binary: a bare
// command refuses and names its door, and docs/TESTS.md's first run is compared
// against what the tool prints, line for line, by the one comparator.

// documentedHome is the home directory the TESTS.md block was recorded under:
// a linux login whose PATH holds every program the setup runs, typing from home.
const documentedHome = "/home/you"

// firstRunMachine is that machine for a dry run: every program is on PATH under
// /usr/bin and answers its version, and nothing else is run, since a dry run of
// a fresh root runs nothing but those probes (internal/up: a plan reads, an
// apply writes). Any other command is an error the transcript would show.
type firstRunMachine struct{}

func (firstRunMachine) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func (firstRunMachine) Run(_ context.Context, c up.Cmd) (string, error) {
	if len(c.Args) == 1 && (c.Args[0] == "version" || c.Args[0] == "--version") {
		return filepath.Base(c.Name) + " version 1.2.0\n", nil
	}
	return "", errors.New("the first run's machine runs only version probes, not: " + c.String())
}

// sitting runs nova-up's tool over that machine, its home the test's own
// directory, and resolves the documented ./nova-try under it, so nothing is
// read or written in the checkout or the process's working directory.
func sitting(t *testing.T) (func(args []string) onboarding.Result, string) {
	t.Helper()
	home := t.TempDir()
	machine := func() (up.Machine, error) {
		return up.Machine{GOOS: "linux", Home: home, UID: 1000, Exec: firstRunMachine{},
			Now:  func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) },
			Rand: strings.NewReader(strings.Repeat("0123456789abcdef", 64))}, nil
	}
	stand := strings.NewReplacer("./nova-try", filepath.Join(home, "nova-try"))
	return func(args []string) onboarding.Result {
		local := make([]string, len(args))
		for i, a := range args {
			local[i] = stand.Replace(a)
		}
		var out, errb bytes.Buffer
		code := up.Tool("", machine).Run(local, strings.NewReader(""), &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	}, home
}

// (a) A bare command refuses in one line, on stderr at exit 2, and names its door.
func TestABareCommandNamesItsDoor(t *testing.T) {
	t.Parallel()
	run, home := sitting(t)
	res := run(nil)
	assert.Equal(t, 2, res.Code)
	assert.Empty(t, res.Stdout)
	assert.Equal(t, 1, strings.Count(res.Stderr, "\n"), res.Stderr)
	assert.Contains(t, res.Stderr, "run: nova-up help")
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	assert.Empty(t, entries, "a refusal writes nothing")
}

// (b) The docs/TESTS.md first run is EXECUTED, every command in order, and its
// whole output compared with the block by the one comparator; the one value not
// compared as written is the home directory the run plans under. The dry run
// writes nothing, and the block says so.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-up")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-up", lines)
	require.NoError(t, err)
	run, home := sitting(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "tmpdir", Doc: documentedHome, Run: home}}))
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	assert.Empty(t, entries, "the first run is a dry run and writes nothing")
}

// The banner's example block is a first run a stranger types in order, and on
// the first run's machine each line runs at exit 0 and writes nothing.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	run, home := sitting(t)
	banner := run([]string{"help"})
	require.Equal(t, 0, banner.Code, banner.Stderr)
	examples, err := onboarding.ExampleLines(banner.Stdout, "nova-up")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(examples), onboarding.MinExampleCommands, "the example block: %q", examples)
	for _, ex := range examples {
		fields, err := onboarding.SplitShell(ex)
		require.NoError(t, err)
		res := run(fields[1:])
		assert.Equal(t, 0, res.Code, "the example %q exits %d: %s", ex, res.Code, res.Stderr)
	}
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	assert.Empty(t, entries, "the examples write nothing")
}
