package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary. A newcomer's
// first stumble is the spec for these tests: the banner's examples are RUN rather
// than read, and every refusal a first run hits must say what the flag WANTS.

// exampleDogfood is the fixture the dogfood verb's banner example runs
// against: a command reference the size of a first run, and the receipts two
// friends left against it.
const exampleDogfood = "testdata/example-dogfood"

func runCheck(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb)
	return exit, out.String(), errb.String()
}

// localize points an example command at the fixture, so what is under test is
// the command's SHAPE and not the reader's directory layout. The banner shows a
// reader the paths they would type in their own repository (./docs/CLI.md,
// ./dogfood-receipts); here those become the fixtures that ship with the tool,
// so the example is run rather than read.
func localize(t *testing.T, args []string) []string {
	t.Helper()
	receipts := t.TempDir()
	entries, err := os.ReadDir(filepath.Join(exampleDogfood, "receipts"))
	require.NoError(t, err)
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(exampleDogfood, "receipts", e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(receipts, e.Name()), body, 0o644))
	}
	out := append([]string(nil), args...)
	for i, a := range out {
		switch a {
		case "./docs/CLI.md":
			out[i] = filepath.Join(exampleDogfood, "CLI.md")
		case "./dogfood-receipts":
			out[i] = receipts
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	// The banner is asked for, because a bare invocation no longer IS one: a refusal now
	// costs one line and names the door (`run: nova-dev help`). Reading it through that
	// door is also a test that the door opens.
	exit, stdout, stderr := runCheck(t, "help")
	require.EqualValues(t, 0, exit, "`nova-dev help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	lines, err := onboarding.ExampleLines(stdout, "nova-dev")
	require.NoError(t, err, "%s\n\n%s", err, stdout)
	return lines
}

// (a) The bare command prints usage ending in an `example:` block of lines that
// actually run. They are run here against the fixture: an example that has
// drifted out of the flag set teaches the wrong invocation to exactly the
// reader who cannot tell. The gate may answer 1 -- it ran and said no, over
// the open edge the fixture teaches with -- only 2 is a broken example.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	for _, ex := range examples(t) {
		fields := strings.Fields(ex)
		exit, stdout, stderr := runCheck(t, localize(t, fields[1:])...)
		if !assert.NotEqualValues(t, 2, exit, "the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr) {
			continue
		}
		if strings.HasPrefix(ex, "nova-dev dogfood gate") {
			assert.EqualValues(t, 1, exit, "the gate example %q exited %d; on the fixture it names the open edge and says no\nstderr: %s", ex, exit, stderr)
		} else {
			assert.EqualValues(t, 0, exit, "the usage example %q ran but said NO (exit %d); an example a stranger types should pass on the fixture\nstderr: %s", ex, exit, stderr)
		}
		assert.NotEqualValues(t, "", stdout, "the usage example %q printed nothing on stdout", ex)
	}
}

// Every verb the banner names is one a reader may type first, so every one
// of them appears in the usage block above the examples.
func TestTheLedgerIsTheFirstThingTheBannerOffers(t *testing.T) {
	t.Parallel()

	exs := examples(t)
	assert.True(t, strings.HasPrefix(exs[0], "nova-dev dogfood ledger "), "the first example is %q; a first run should be offered the ledger first", exs[0])
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"dogfood ledger without a verb list", []string{"dogfood", "ledger", "--receipts", "r"}, cliHint},
		{"dogfood ledger without receipts", []string{"dogfood", "ledger", "--cli", "c.md"}, receiptsHint},
		{"dogfood record without receipts", []string{"dogfood", "record", "--tool", "t", "--verb", "v", "--by", "b", "--notes", "n", "--ok"}, receiptsHint},
		{"convergence without a ledger", []string{"convergence", "--repo", "owner/name", "--receipts", "r", "--retired", "x.md", "--since", "24h"}, convLedgerHint},
		{"hygiene without an identity", []string{"hygiene", "--repo", ".", "--base", "main", "--head", "HEAD"}, "--identity is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := runCheck(t, tc.args...)
			require.EqualValues(t, 2, exit, "exit = %d, want 2 — guidance must not soften the refusal; stderr: %s", exit, stderr)
			assert.Contains(t, stderr, tc.want, "stderr = %q,\nwant it to contain the hint %q", stderr, tc.want)
			assert.EqualValues(t, "", stdout, "a refusal must print nothing on stdout, got %q", stdout)
		})
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if hygLabGoldenDir != "" {
		_ = os.RemoveAll(hygLabGoldenDir)
	}
	os.Exit(code)
}

// runDocumented calls this binary's own entry point with the documented
// arguments. nova-dev reads nothing on stdin.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb)
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-dev reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

// The tool's own definition meets the standard its banner carries by
// construction: a what line, an exit table, a how text of at most five lines,
// and every verb's effect one of inspection, local write or delivery.
func TestDevToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, devTool([]string{"dogfood"}).Problems(), "the tool definition falls short of STANDARD section 3")
}
