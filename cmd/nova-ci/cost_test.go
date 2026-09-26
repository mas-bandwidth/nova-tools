package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/redis/go-redis/v9"
)

// cost_test.go is the red-test contract of `nova-ci cost`: the fixture
// listing internal/ci/testdata/cost/jobs.json (one failed job at 42 s, one
// rerun job at 33 s) on stdin yields the one COST line, the store write goes
// through the seam to a fake, and every refusal names what the input wants.
// Nothing here opens a socket or reads the clock.

const costTestSHA = "0123456789abcdef0123456789abcdef01234567"

func costListing(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "ci", "testdata", "cost", "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// costReceiptArgs are the receipt's flags, as the ci-ok step spells them.
func costReceiptArgs(extra ...string) []string {
	args := []string{"cost", "--repo", "mas-bandwidth/nova-tools", "--sha", costTestSHA, "--run-id", "777",
		"--event", "pull_request", "--workflow", "ci", "--conclusion", "failure", "--pr", "4328",
		"--head-branch", "rowan/x", "--base-branch", "dev", "--at", "2026-09-26T20:06:00Z"}
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

func (f *fakeCostStore) open(_ context.Context, addr string) (ci.CostWriter, func() error, error) {
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

// Without --redis the line is logged and not written: ev=-.
func TestCostPrintsTheOneLine(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCost(t, nil, costListing(t), costReceiptArgs()...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	want := "COST repo=mas-bandwidth/nova-tools sha=" + costTestSHA + " run=777 event=pull_request workflow=ci conclusion=failure pr=4328" +
		" jobs=5 total=150 spin=75 unknown=0" +
		` job=lint:15:success:1:ok job=test\x20(hulk):42:failure:1:failed job=e2e:33:success:2:rerun job=lisp:0:skipped:1:ok job=test-hosted:60:success:1:ok ev=-` + "\n"
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
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
	code, stdout, stderr := runCost(t, f, costListing(t), costReceiptArgs("--redis", "127.0.0.1:1")...)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	if f.addr != "127.0.0.1:1" || f.stream != ci.CostStream || !f.closed {
		t.Errorf("opened %q, wrote %q, closed=%v; want 127.0.0.1:1, %s, true", f.addr, f.stream, f.closed, ci.CostStream)
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

// Every refusal is one stderr line that names what the input wants, exit 2,
// and nothing on stdout; a store that will not take the entry is a refusal,
// never a line that pretends it was written.
func TestCostRefusalsNameWhatTheInputWants(t *testing.T) {
	t.Parallel()

	listing := costListing(t)
	cases := []struct {
		name  string
		f     *fakeCostStore
		stdin string
		args  []string
		want  string
	}{
		{"no flags", nil, listing, []string{"cost"}, "--repo wants owner/name"},
		{"short sha", nil, listing, []string{"cost", "--repo", "o/n", "--sha", "abc"}, "--sha wants the 40-hex head"},
		{"no conclusion", nil, listing, []string{"cost", "--repo", "o/n", "--sha", costTestSHA, "--run-id", "1", "--event", "push", "--workflow", "ci"}, "--conclusion wants"},
		{"empty stdin", nil, "", costReceiptArgs(), "stdin is empty"},
		{"not json", nil, "<html>", costReceiptArgs(), "not the forge's job listing"},
		{"no jobs", nil, `{"total_count":0,"jobs":[]}`, costReceiptArgs(), "holds no jobs"},
		{"positional", nil, listing, costReceiptArgs("jobs.json"), "unexpected argument"},
		{"open fails", &fakeCostStore{openErr: errors.New("NOAUTH")}, listing, costReceiptArgs("--redis", "127.0.0.1:1"), "open 127.0.0.1:1: NOAUTH"},
		{"xadd fails", &fakeCostStore{addErr: errors.New("NOPERM")}, listing, costReceiptArgs("--redis", "127.0.0.1:1"), "XADD ci:cost: NOPERM"},
	}
	for _, tc := range cases {
		code, stdout, stderr := runCost(t, tc.f, tc.stdin, tc.args...)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2; stderr: %s", tc.name, code, stderr)
		}
		if stdout != "" {
			t.Errorf("%s: stdout = %q, want empty", tc.name, stdout)
		}
		if !strings.Contains(stderr, tc.want) || strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(stderr, "nova-ci cost: ") {
			t.Errorf("%s: stderr = %q, want one `nova-ci cost: ` line holding %q", tc.name, stderr, tc.want)
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
