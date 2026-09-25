package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// Accounting is not optional (Glenn: token spend reporting is an obligation,
// and every decision is logged). A route that will call the provider must say
// where the spend and the decision are written BEFORE it calls: a call nobody
// can account for is refused rather than made.

// A --jev route with no --usage, no --log, or neither is refused, and the
// refusal names the flag that is missing.
func TestJevRequiresAccounting(t *testing.T) {
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	base := []string{"route", "--unit-id", "u", "--kind", "rebase", "--files", "2", "--packages", "1"}
	for name, tc := range map[string]struct {
		args []string
		want []string
	}{
		"neither":        {args: nil, want: []string{"--usage", "--log"}},
		"only usage":     {args: []string{"--usage", usage}, want: []string{"--log"}},
		"only log":       {args: []string{"--log", log}, want: []string{"--usage"}},
		"explicit --jev": {args: []string{"--jev"}, want: []string{"--usage", "--log"}},
	} {
		f := &fake{conf: 0.97}
		useFake(t, f)
		var stdout, stderr bytes.Buffer
		code := run(append(append([]string{}, base...), tc.args...), &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stdout=%q stderr=%q)", name, code, stdout.String(), stderr.String())
			continue
		}
		if f.calls != 0 {
			t.Errorf("%s: the provider was called %d times before the accounting was settled", name, f.calls)
		}
		line := strings.TrimSuffix(stderr.String(), "\n")
		if strings.Contains(line, "\n") {
			t.Errorf("%s: a refusal is one line: %q", name, stderr.String())
		}
		if !strings.Contains(line, "REFUSED reason=no-accounting") {
			t.Errorf("%s: want REFUSED reason=no-accounting, got %q", name, line)
		}
		for _, flag := range tc.want {
			if !strings.Contains(line, flag) {
				t.Errorf("%s: the refusal must name %s: %q", name, flag, line)
			}
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: a refusal belongs on stderr: %q", name, stdout.String())
		}
	}
}

// With both named, the call is made and both files are written.
func TestJevWithAccountingRuns(t *testing.T) {
	useFake(t, &fake{conf: 0.97, usage: decide.Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}})
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.tsv")
	log := filepath.Join(dir, "decide.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "u", "--kind", "rebase", "--files", "2", "--packages", "1",
		"--usage", usage, "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if got := tsvRow(t, usage); got["tokens_in"] != "937" {
		t.Errorf("the spend was not accounted for: %v", got)
	}
}

// --no-jev makes no call, so it spends nothing and may be run without either
// flag: the obligation is to account for what was spent, not to write a file
// for a decision that cost nothing.
func TestNoJevKeepsAccountingOptional(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--files", "2", "--packages", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rung=flash") {
		t.Errorf("the rules answered without a file in sight: %s", stdout.String())
	}
}
