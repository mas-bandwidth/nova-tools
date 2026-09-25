package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestDevRedAndReadVerbsRefuseUsage: both verbs are registered and refuse
// a missing subverb or flag with exit 2 and the remedy on the line.
func TestDevRedAndReadVerbsRefuseUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"dev-red"}, "want status, check, watch or unwatch"},
		{[]string{"dev-red", "status"}, "needs --repo <r> and --base <b>"},
		{[]string{"dev-red", "bogus"}, "unknown subverb bogus"},
		{[]string{"read"}, "want brief"},
		{[]string{"read", "carry", "--repo", "nova-tools", "--n", "7", "--redis", "127.0.0.1:1", "--line", "x"}, "want --repo <r> --n <n> [--sprint <S>]"},
		{[]string{"read", "digest", "--repo", "mas-bandwidth/nova-tools/x", "--n", "7", "--redis", "127.0.0.1:1"}, "needs --repo <owner/name|name>"},
	}
	for _, tc := range cases {
		var out, errOut bytes.Buffer
		if code := run(tc.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut.String(), tc.want)
		}
	}
}

// TestDevRedFromRuns: the forge seam's reading of check runs.
func TestDevRedFromRuns(t *testing.T) {
	t.Parallel()

	type run = struct{ Name, Status, Conclusion string }
	if st := devRedFromRuns(nil); st.Verdict != "" {
		t.Fatalf("no runs: %+v", st)
	}
	if st := devRedFromRuns([]run{{"lint", "completed", "success"}, {"test", "in_progress", ""}}); st.Verdict != "" {
		t.Fatalf("in progress: %+v", st)
	}
	if st := devRedFromRuns([]run{{"lint", "completed", "success"}, {"test", "completed", "success"}}); st.Verdict != civerdict.OK {
		t.Fatalf("all green: %+v", st)
	}
	if st := devRedFromRuns([]run{{"lint", "completed", "success"}, {"test-packages", "completed", "failure"}}); st != (reconcile.CIState{Verdict: "FAIL", Check: "test-packages"}) {
		t.Fatalf("one red: %+v", st)
	}
}
