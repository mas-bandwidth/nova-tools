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

const receiptSHA = "0123456789abcdef0123456789abcdef01234567"

func receiptArgs(extra ...string) []string {
	return append([]string{"receipt", "--from-runner", "--redis", "127.0.0.1:1", "--repo", "mas-bandwidth/nova-tools",
		"--sha", receiptSHA, "--run-id", "42", "--workflow", "CI", "--conclusion", "success", "--pr", "7"}, extra...)
}

// TestReceiptRefusesBeforeTheStore: every usage error is exit 2 with one
// line naming the verb, and none of them dials (the address is a closed port).
func TestReceiptRefusesBeforeTheStore(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{}, "the one verb is receipt; run: nova-ci github receipt -h"},
		{[]string{"status"}, `unknown verb "status" after github; the one verb is receipt`},
		{receiptArgs("extra"), "receipt takes flags only"},
		{receiptArgs("--job", "lint=success"), "unknown flag --job; the flags of github receipt are --at, --conclusion, --dry-run"},
		{receiptArgs("--event", "push"), "unknown flag --event"},
		{receiptArgs("--from-runner=false"), "--from-runner is required"},
		{receiptArgs("--sha", "abc"), "--sha wants the 40-hex head"},
		{receiptArgs("--conclusion", "skipped"), "--conclusion wants success, failure or cancelled"},
	} {
		var out, errOut bytes.Buffer
		code := cmdGitHub(c.args, &out, &errOut, noEnv)
		msg := errOut.String()
		assert.Equal(t, 2, code, "%v", c.args)
		assert.Empty(t, out.String(), "%v", c.args)
		assert.Contains(t, msg, c.want, "%v", c.args)
		assert.Regexp(t, `^nova-ci github( receipt)? REFUSED: .*; run: nova-ci github receipt -h\n$`, msg, "%v", c.args)
	}
}

func noEnv(string) string { return "" }

// One run names every refused field and the missing store together, so the
// caller fixes the call once (STANDARD §2).
func TestReceiptNamesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	code := cmdGitHub([]string{"receipt", "--repo", "x", "--sha", "abc", "--run-id", "1", "--workflow", "CI", "--conclusion", "maybe"}, &out, &errOut, noEnv)
	assert.Equal(t, 2, code)
	for _, want := range []string{"--from-runner is required", "--repo wants", "--sha wants", "--conclusion wants", "needs --redis <host:port> or NOVA_REDIS_ADDR"} {
		assert.Contains(t, errOut.String(), want)
	}
	assert.Equal(t, 1, strings.Count(errOut.String(), "\n"), "one refusal line: %q", errOut.String())
}

// --dry-run checks the fields and prints the line the write would print, with
// ev=-, and needs no store: nothing is dialled (no --redis is given at all).
func TestReceiptDryRunChecksAndDialsNothing(t *testing.T) {
	t.Parallel()
	args := []string{"receipt", "--dry-run", "--from-runner", "--repo", "mas-bandwidth/nova-tools", "--sha", receiptSHA,
		"--run-id", "42", "--workflow", "CI", "--conclusion", "success", "--pr", "7"}
	var out, errOut bytes.Buffer
	code := cmdGitHub(args, &out, &errOut, noEnv)
	require.Equal(t, 0, code, "stderr %q", errOut.String())
	assert.Equal(t, "CI RECEIPT mas-bandwidth/nova-tools sha="+receiptSHA+" run=42 workflow=CI conclusion=success pr=7 ev=-\n"+
		"CI RECEIPT NOTE --dry-run: the fields are good; nothing was dialled or written; drop --dry-run to write it\n", out.String())
	assert.Empty(t, errOut.String())
}

func TestReceiptNeedsAStoreAddress(t *testing.T) {
	t.Parallel()
	args := receiptArgs()
	args[3] = ""
	var out, errOut bytes.Buffer
	code := cmdGitHub(args, &out, &errOut, noEnv)
	require.Equal(t, 2, code, "code %d err %q", code, errOut.String())
	require.Contains(t, errOut.String(), "needs --redis <host:port> or NOVA_REDIS_ADDR", "code %d err %q", code, errOut.String())
}

// receipt -h is the verb's help: its usage lines, every flag with what it
// wants, and its own exit codes.
func TestReceiptHelpDescribesEveryFlag(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	code := run([]string{"github", "receipt", "--help"}, nil, &out, &errOut)
	require.Equal(t, 0, code, "stderr %q", errOut.String())
	assert.Empty(t, errOut.String())
	assert.Contains(t, out.String(), "nova-ci github receipt --from-runner --redis <addr>")
	_, flags, found := strings.Cut(out.String(), "\nflags:\n")
	require.True(t, found, "receipt -h lists no flags:\n%s", out.String())
	for _, line := range strings.Split(flags, "\n") {
		if strings.HasPrefix(line, "  --") {
			assert.Regexp(t, `^  --[a-z-]+( <[a-z]+>)?  \S`, line, "flag line %q has no description", line)
		}
	}
	for _, flag := range []string{"--repo", "--sha", "--run-id", "--workflow", "--conclusion", "--redis", "--dry-run", "--from-runner"} {
		assert.Contains(t, out.String(), "\n  "+flag)
	}
	assert.Contains(t, out.String(), "github receipt: 0 written")
}

// TestTheCommandReferenceReceiptRefusalsAreWhatTheToolPrints executes the
// fenced transcript under docs/CLI.md's `### github receipt` line for line
// through onboarding.CompareTranscript. Every documented step is refused
// before any dial, so no store is involved and nothing is normalised.
func TestTheCommandReferenceReceiptRefusalsAreWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "CLI.md"))
	require.NoError(t, err)
	lines, err := onboarding.Transcript(string(raw), "nova-ci", "github receipt")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-ci", lines)
	require.NoError(t, err)
	require.Len(t, steps, 2, "the `### github receipt` block runs %d commands, want 2 refusals", len(steps))
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		require.GreaterOrEqual(t, len(s.Args), 2, "the documented command %q is not nova-ci github receipt", s.Line)
		require.Equal(t, "github", s.Args[0], "the documented command %q is not nova-ci github receipt", s.Line)
		require.Equal(t, "receipt", s.Args[1], "the documented command %q is not nova-ci github receipt", s.Line)
		var out, errb bytes.Buffer
		code := cmdGitHub(s.Args[1:], &out, &errb, noEnv)
		assert.Equal(t, 2, code, "%s: exit %d, want 2 (refused before any dial)", s.Line, code)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}
