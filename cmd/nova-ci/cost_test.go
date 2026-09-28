package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cicost"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/redis/go-redis/v9"
)

// cost_test.go is the red-test contract of `nova-ci cost`: the fixture
// listing internal/cicost/testdata/jobs.json (one failed job at 42 s, one
// rerun job at 33 s) on stdin yields the one COST line, the store write goes
// through the seam to a fake, and every refusal names what the input wants.
// Nothing here opens a socket or reads the clock.

const costTestSHA = "0123456789abcdef0123456789abcdef01234567"

// costListingPath is the fixture as docs/CLI.md's transcript names it, from
// the checkout root.
const costListingPath = "internal/cicost/testdata/jobs.json"

func costListing(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(costListingPath)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// costReceiptArgs are the receipt's flags, as the ci-ok step spells them.
func costReceiptArgs(extra ...string) []string {
	args := []string{"cost", "--repo", "mas-bandwidth/nova-tools", "--sha", costTestSHA, "--run-id", "777",
		"--workflow", "ci", "--conclusion", "failure", "--pr", "4328", "--at", "2026-09-26T20:06:00Z"}
	return append(args, extra...)
}

// fakeCostStore is the seam's fake: it records the address opened and the
// entry written, and answers with a fixed id.
type fakeCostStore struct {
	addr    string
	stream  string
	values  []string
	closed  bool
	openErr error
	addErr  error
}

func (f *fakeCostStore) XAdd(_ context.Context, a *redis.XAddArgs) *redis.StringCmd {
	f.stream = a.Stream
	f.values, _ = a.Values.([]string)
	if f.addErr != nil {
		return redis.NewStringResult("", f.addErr)
	}
	return redis.NewStringResult("1700000000000-0", nil)
}

func (f *fakeCostStore) open(_ context.Context, addr string) (cicost.Writer, func() error, error) {
	f.addr = addr
	if f.openErr != nil {
		return nil, nil, f.openErr
	}
	return f, func() error { f.closed = true; return nil }, nil
}

func runCost(t *testing.T, f *fakeCostStore, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	var open costOpener
	if f != nil {
		open = f.open
	}
	code := cmdCost(args[1:], strings.NewReader(stdin), &out, &errb, open)
	return code, out.String(), errb.String()
}

// costWantLine is the fixture's line, as docs/CLI.md pastes it.
const costWantLine = "COST repo=mas-bandwidth/nova-tools sha=" + costTestSHA + " run=777 workflow=ci conclusion=failure pr=4328" +
	" jobs=5 total=150 spin=75 unknown=0" +
	` job=lint:15:success:1:ok job=test\x20(linux):42:failure:1:failed job=functional:33:success:2:rerun job=docs:0:skipped:1:ok job=test-hosted:60:success:1:ok ev=-`

// Without --redis the line is printed and not written: ev=-.
func TestCostPrintsTheOneLine(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCost(t, nil, costListing(t), costReceiptArgs()...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	if stdout != costWantLine+"\n" {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, costWantLine)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// With --redis the entry lands on ci:cost through the seam, as the receipt's
// identity plus the totals, the store is closed, and the line ends in the id.
func TestCostWritesTheStreamThroughTheSeam(t *testing.T) {
	t.Parallel()

	f := &fakeCostStore{}
	code, stdout, stderr := runCost(t, f, costListing(t), costReceiptArgs("--redis", testverbhelp.RefusedAddr)...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	if f.addr != testverbhelp.RefusedAddr || f.stream != cicost.Stream || !f.closed {
		t.Errorf("opened %q, wrote %q, closed=%v; want %s, %s, true", f.addr, f.stream, f.closed, testverbhelp.RefusedAddr, cicost.Stream)
	}
	got := map[string]string{}
	for i := 0; i+1 < len(f.values); i += 2 {
		got[f.values[i]] = f.values[i+1]
	}
	for k, want := range map[string]string{"repo": "mas-bandwidth/nova-tools", "sha": costTestSHA, "run_id": "777",
		"pr": "4328", "spin": "75", "total": "150", "jobs": "5", "at": "2026-09-26T20:06:00Z"} {
		if got[k] != want {
			t.Errorf("field %s = %q, want %q", k, got[k], want)
		}
	}
	if !strings.HasSuffix(stdout, " ev=1700000000000-0\n") || strings.Count(stdout, "\n") != 1 {
		t.Errorf("stdout = %q, want one line ending in the entry id", stdout)
	}
}

// Every refusal is one stderr line that names what the input wants, exit 2
// before any dial, and nothing on stdout.
func TestCostRefusalsNameWhatTheInputWants(t *testing.T) {
	t.Parallel()

	listing := costListing(t)
	cases := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"no flags", listing, []string{"cost"}, "--repo wants owner/name"},
		{"short sha", listing, []string{"cost", "--repo", "o/n", "--sha", "abc"}, "--sha wants the 40-hex head"},
		{"no conclusion", listing, []string{"cost", "--repo", "o/n", "--sha", costTestSHA, "--run-id", "1", "--workflow", "ci"}, "--conclusion wants"},
		{"unknown flag", listing, costReceiptArgs("--event", "push"), "flag provided but not defined: -event"},
		{"empty stdin", "", costReceiptArgs(), "stdin is empty; it wants the run's job listing"},
		{"not json", "<html>", costReceiptArgs(), "not the forge's job listing"},
		{"no jobs", `{"total_count":0,"jobs":[]}`, costReceiptArgs(), "holds no jobs"},
		{"partial page", `{"total_count":2,"jobs":[{"name":"one","run_attempt":1,"conclusion":"success","started_at":"2026-09-28T00:00:00Z","completed_at":"2026-09-28T00:01:00Z"}]}`, costReceiptArgs(), "listing is partial"},
		{"positional", listing, costReceiptArgs("jobs.json"), "unexpected argument"},
	}
	for _, tc := range cases {
		code, stdout, stderr := runCost(t, nil, tc.stdin, tc.args...)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2; stderr: %s", tc.name, code, stderr)
		}
		if stdout != "" {
			t.Errorf("%s: stdout = %q, want empty", tc.name, stdout)
		}
		if !strings.Contains(stderr, tc.want) || strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(stderr, "nova-ci cost: ") || !strings.HasSuffix(stderr, "; run: nova-ci help\n") {
			t.Errorf("%s: stderr = %q, want one `nova-ci cost: ` line holding %q and naming the door", tc.name, stderr, tc.want)
		}
	}
}

// A listing whose total_count exceeds the jobs read is refused before any
// dial: a complete listing is required before writing a run-total COST entry.
func TestCostTruncatedPageRefused(t *testing.T) {
	t.Parallel()

	input := `{"total_count":2,"jobs":[{"name":"one","run_attempt":1,"conclusion":"success","started_at":"2026-09-28T00:00:00Z","completed_at":"2026-09-28T00:01:00Z"}]}`
	code, stdout, stderr := runCost(t, nil, input, costReceiptArgs()...)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"listing is partial", "1 jobs read", "2 expected", "page through the forge's listing, or pass every page"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q lacks %q", stderr, want)
		}
	}
}

// A Redis close failure after a successful XADD prints the COST line with its
// event id on stdout, reports the close error on stderr, and exits 1 without
// claiming the entry was not written or recommending a rerun.
func TestCostCloseFailureAfterSuccessfulWrite(t *testing.T) {
	t.Parallel()

	f := &fakeCostStore{}
	var out, errb bytes.Buffer
	open := func(context.Context, string) (cicost.Writer, func() error, error) {
		return f, func() error { return errors.New("close failed") }, nil
	}
	code := cmdCost(costReceiptArgs("--redis", testverbhelp.RefusedAddr)[1:], strings.NewReader(costListing(t)), &out, &errb, open)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", code, errb.String())
	}
	if f.stream != cicost.Stream {
		t.Errorf("stream = %q, want %s", f.stream, cicost.Stream)
	}
	if !strings.Contains(out.String(), " ev=1700000000000-0\n") {
		t.Errorf("stdout %q lacks the event id", out.String())
	}
	if strings.Contains(errb.String(), "was not written") {
		t.Errorf("stderr %q claims entry was not written", errb.String())
	}
	if strings.Contains(errb.String(), "rerun") {
		t.Errorf("stderr %q recommends rerun", errb.String())
	}
	if !strings.Contains(errb.String(), "nova-ci cost: close: close failed\n") {
		t.Errorf("stderr = %q, want close error line", errb.String())
	}
}

// TestCostPaginationAndCloseFailureControls keeps pagination and close-failure
// controls active as a regression check.
func TestCostPaginationAndCloseFailureControls(t *testing.T) {
	t.Parallel()

	t.Run("partial", func(t *testing.T) {
		input := `{"total_count":2,"jobs":[{"name":"one","run_attempt":1,"conclusion":"success","started_at":"2026-09-28T00:00:00Z","completed_at":"2026-09-28T00:01:00Z"}]}`
		code, out, err := runCost(t, nil, input, costReceiptArgs()...)
		t.Logf("exit=%d stdout=%q stderr=%q", code, out, err)
		if code == 0 && !strings.Contains(out, "partial") {
			t.Error("incomplete listing reported as a complete total")
		}
		if code != 2 {
			t.Errorf("exit = %d, want 2", code)
		}
	})
	t.Run("close-after-write", func(t *testing.T) {
		f := &fakeCostStore{}
		var out, err bytes.Buffer
		open := func(context.Context, string) (cicost.Writer, func() error, error) {
			return f, func() error { return errors.New("close failed") }, nil
		}
		code := cmdCost(costReceiptArgs("--redis", "127.0.0.1:1")[1:], strings.NewReader(costListing(t)), &out, &err, open)
		t.Logf("exit=%d wrote_stream=%q field_values=%d stdout=%q stderr=%q", code, f.stream, len(f.values), out.String(), err.String())
		if f.stream == cicost.Stream && strings.Contains(err.String(), "was not written") {
			t.Error("successful XADD misreported as not written")
		}
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(out.String(), " ev=1700000000000-0\n") {
			t.Errorf("stdout %q lacks the event id", out.String())
		}
	})
}

// A store that will not take the entry is exit 1 with one line naming the
// cause, the state and the next action, never a line that pretends it was
// written; the same code the receipt gives the same failure.
func TestCostUnwrittenEntryIsExitOne(t *testing.T) {
	t.Parallel()

	listing := costListing(t)
	cases := []struct {
		name string
		f    *fakeCostStore
		want string
	}{
		{"open fails", &fakeCostStore{openErr: errors.New("NOAUTH")}, "open " + testverbhelp.RefusedAddr + ": NOAUTH"},
		{"xadd fails", &fakeCostStore{addErr: errors.New("NOPERM")}, "XADD ci:cost: NOPERM"},
	}
	for _, tc := range cases {
		code, stdout, stderr := runCost(t, tc.f, listing, costReceiptArgs("--redis", testverbhelp.RefusedAddr)...)
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1; stderr: %s", tc.name, code, stderr)
		}
		if stdout != "" {
			t.Errorf("%s: stdout = %q, want empty", tc.name, stdout)
		}
		if !strings.Contains(stderr, tc.want) || !strings.HasSuffix(stderr, "the COST entry was not written: fix the store or the bench seat and rerun ci-ok\n") || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%s: stderr = %q, want one line holding %q and the next action", tc.name, stderr, tc.want)
		}
	}
}

// The verb is dispatched from run() and listed in the usage banner.
func TestCostIsAVerb(t *testing.T) {
	t.Parallel()

	code, stdout, _ := runCI(t, costReceiptArgs(), costListing(t))
	if code != 0 || !strings.HasPrefix(stdout, "COST repo=") {
		t.Errorf("run() dispatched cost: exit %d, stdout %q", code, stdout)
	}
	_, help, _ := runCI(t, []string{"help"}, "")
	if !strings.Contains(help, "nova-ci cost ") {
		t.Error("the usage banner does not list the cost verb")
	}
}

// TestTheCommandReferenceCostIsWhatTheToolPrints executes the fenced
// transcript under docs/CLI.md's `### cost` line for line through
// onboarding.CompareTranscript: the documented command reads the fixture the
// `< path` redirect names, from the checkout root where a reader stands, and
// prints the documented line. Nothing is normalised: the fixture is on disk
// and the line carries no clock.
func TestTheCommandReferenceCostIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.Transcript(string(raw), "nova-ci", "cost")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-ci", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("the `### cost` block runs %d commands, want 1", len(steps))
	}
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		if len(s.Args) < 1 || s.Args[0] != "cost" || s.Stdin != costListingPath {
			t.Fatalf("the documented command %q is not `nova-ci cost ... < %s`", s.Line, costListingPath)
		}
		var out, errb bytes.Buffer
		code := cmdCost(s.Args[1:], strings.NewReader(costListing(t)), &out, &errb, nil)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}
