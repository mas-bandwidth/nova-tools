package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLI coverage for `nova-post issue`: flag validation, section parsing and
// the ISSUE READY line (hold on #2867: runIssue had no CLI tests).

func writeSection(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "section.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIssueVerbReady(t *testing.T) {
	t.Parallel()

	sec := writeSection(t, "RESULT test-card sha=abcd\nKIND: fix\nSCHEMA: v2\n")
	var out, errb bytes.Buffer
	code := run([]string{"issue", "--section", sec, "--owner", "mas-bandwidth", "--repo", "nova-tools"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "ISSUE READY owner=mas-bandwidth repo=nova-tools title=") {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestIssueVerbRefusals(t *testing.T) {
	t.Parallel()

	good := writeSection(t, "RESULT test-card sha=abcd\nKIND: fix\nSCHEMA: v2\n")
	noKind := writeSection(t, "RESULT test-card sha=abcd\nSCHEMA: v2\n")
	missing := filepath.Join(t.TempDir(), "absent.md")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no section", []string{"issue", "--owner", "o", "--repo", "r"}, "missing-flag"},
		{"no owner", []string{"issue", "--section", good, "--repo", "r"}, "missing-flag"},
		{"no repo", []string{"issue", "--section", good, "--owner", "o"}, "missing-flag"},
		{"bad flag", []string{"issue", "--nope"}, "bad-flags"},
		{"stray arg", []string{"issue", "--section", good, "--owner", "o", "--repo", "r", "extra"}, "bad-flags"},
		{"unreadable", []string{"issue", "--section", missing, "--owner", "o", "--repo", "r"}, "bad-section"},
		{"missing field", []string{"issue", "--section", noKind, "--owner", "o", "--repo", "r"}, "missing-field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(c.args, &out, &errb); code != 2 {
				t.Fatalf("exit %d, want 2 (stderr %q)", code, errb.String())
			}
			if out.Len() != 0 {
				t.Fatalf("stdout not empty: %q", out.String())
			}
			if !strings.Contains(errb.String(), c.want) {
				t.Fatalf("stderr %q, want %q", errb.String(), c.want)
			}
		})
	}
}
