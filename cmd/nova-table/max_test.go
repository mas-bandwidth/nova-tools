package main

import (
	"strings"
	"testing"
)

func TestMaxFlagRefusesNegativeValues(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"list", []string{"list", "--max", "-1"}},
		{"show", []string{"show", "demo", "--max", "-1"}},
		{"cell members", []string{"cell", "members", "demo", "build", "ready", "--max", "-1"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, out, errout := runTable(tc.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; errout: %q", code, errout)
			}
			if out != "" {
				t.Fatalf("unexpected stdout: %q", out)
			}
			if !strings.Contains(errout, "--max must be zero or more; 0 means all") {
				t.Fatalf("missing error message in errout: %q", errout)
			}
		})
	}
}

func TestMaxFlagDiscoverableInHelp(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		args   []string
		syntax string
	}{
		{"list", []string{"help", "list"}, "nova-table list [--max <n>]"},
		{"show", []string{"help", "show"}, "nova-table show <table> [--at-epoch <n>] [--max <n>]"},
		{"cell members", []string{"help", "cell", "members"}, "nova-table cell members <table> <row> <col> [--max <n>]"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, out, errout := runTable(tc.args...)
			if code != 0 || errout != "" {
				t.Fatalf("help failed: code=%d errout=%q", code, errout)
			}
			if !strings.Contains(out, tc.syntax) {
				t.Errorf("output missing %q:\n%s", tc.syntax, out)
			}
			if !strings.Contains(out, "--max") {
				t.Errorf("output missing --max flag description:\n%s", out)
			}
		})
	}
}
