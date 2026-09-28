package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
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
		{[]string{}, "the verb is receipt"},
		{[]string{"status"}, "the verb is receipt"},
		{receiptArgs("extra"), "takes flags only"},
		{receiptArgs("--job", "lint=success"), "flag provided but not defined: -job"},
		{receiptArgs("--event", "push"), "flag provided but not defined: -event"},
		{receiptArgs("--from-runner=false"), "--from-runner is the one source"},
		{receiptArgs("--sha", "abc"), "--sha wants the 40-hex head"},
		{receiptArgs("--conclusion", "skipped"), "--conclusion wants success, failure or cancelled"},
	} {
		var out, errOut bytes.Buffer
		code := cmdGitHub(c.args, &out, &errOut, noEnv)
		msg := errOut.String()
		if code != 2 || out.Len() != 0 || !strings.Contains(msg, c.want) || !strings.HasPrefix(msg, "nova-ci github") ||
			strings.Count(msg, "\n") != 1 {
			t.Errorf("%v: code %d out %q err %q, want 2 and %q", c.args, code, out.String(), msg, c.want)
		}
	}
}

func noEnv(string) string { return "" }

func TestReceiptNeedsAStoreAddress(t *testing.T) {
	t.Parallel()
	args := receiptArgs()
	args[3] = ""
	var out, errOut bytes.Buffer
	if code := cmdGitHub(args, &out, &errOut, noEnv); code != 2 || !strings.Contains(errOut.String(), "needs --redis <addr> or NOVA_REDIS_ADDR") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}

func TestReceiptHelpIsTheUsageLine(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := run([]string{"github", "receipt", "--help"}, nil, &out, &errOut); code != 0 ||
		!strings.HasPrefix(out.String(), "nova-ci github receipt --from-runner --redis <addr>") || errOut.Len() != 0 {
		t.Fatalf("code %d out %q err %q", code, out.String(), errOut.String())
	}
}

// TestTheCommandReferenceReceiptRefusalsAreWhatTheToolPrints executes the
// fenced transcript under docs/CLI.md's `### github receipt` line for line
// through onboarding.CompareTranscript. Every documented step is refused
// before any dial, so no store is involved and nothing is normalised.
func TestTheCommandReferenceReceiptRefusalsAreWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.Transcript(string(raw), "nova-ci", "github receipt")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-ci", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("the `### github receipt` block runs %d commands, want 2 refusals", len(steps))
	}
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		if len(s.Args) < 2 || s.Args[0] != "github" || s.Args[1] != "receipt" {
			t.Fatalf("the documented command %q is not nova-ci github receipt", s.Line)
		}
		var out, errb bytes.Buffer
		code := cmdGitHub(s.Args[1:], &out, &errb, noEnv)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2 (refused before any dial)", s.Line, code)
		}
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}
