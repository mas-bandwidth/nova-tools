package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handoff_flags_test.go is the part of the handoff's contract that holds on
// every platform: the flags are checked before a volume is made, the usage
// names the door, and the bounded copy writes no byte past its budget.

// 8. The flags are checked before a volume is made: --artifact without --out,
// a bad --out-max-bytes, a `..` in an artifact, and --out on windows.
func TestRunValidatesTheHandoffFlagsBeforeAnythingIsMade(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		goos string
		args []string
		want string
	}{
		{"artifact without out", "darwin", []string{"--artifact", "RESULT.md"}, "no_out"},
		{"cap without out", "darwin", []string{"--out-max-bytes", "64m"}, "no_out"},
		{"bad cap", "darwin", []string{"--out", "/tmp/x", "--out-max-bytes", "lots"}, "bad_out_max"},
		{"dotdot artifact", "darwin", []string{"--out", "/tmp/x", "--artifact", "../x"}, "bad_artifact"},
		{"out on windows", "windows", []string{"--out", `C:\h`, "--scratch", `C:\nova`}, "no_out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"--name", "j1"}, c.args...)
			if c.goos != "windows" {
				args = append(args, "--size", "64m")
			}
			args = append(args, "--", "/bin/sh", "-c", "true")
			f := parseRun(args)
			_, bad := validateRun(&f, c.goos)
			var reasons []string
			for _, r := range bad {
				reasons = append(reasons, r.Reason)
			}
			require.Contains(t, reasons, c.want, "reasons = %v, want one %s", reasons, c.want)
		})
	}
}

// 9. The banner answers the question. `run --help` names the door, the default
// artifacts and the bundle that carries a commit out.
func TestRunUsageNamesTheHandoffAndTheBundle(t *testing.T) {
	t.Parallel()

	for _, want := range []string{"--out <dir>", "--artifact <p>", "--out-max-bytes", "repo.bundle", "git bundle create"} {
		assert.Contains(t, runUsage, want, "run --help does not name %q", want)
	}
}

// 10. The bounded copy writes at most its budget and never the byte that
// proves the source held more: nothing past --out-max-bytes reaches --out.
func TestCopyBoundedWritesNoBytePastTheBudget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		src    string
		budget int64
		want   string
		over   bool
	}{
		{strings.Repeat("x", 4096), 1024, strings.Repeat("x", 1024), true},
		{"exact", 5, "exact", false},
		{"short", 64, "short", false},
		{"a", 0, "", true},
		{"", 0, "", false},
	}
	for _, c := range cases {
		var dst bytes.Buffer
		n, over, err := copyBounded(&dst, strings.NewReader(c.src), c.budget)
		require.NoError(t, err, "src=%d budget=%d: %v", len(c.src), c.budget, err)
		msg := []any{"src=%d budget=%d: wrote %d bytes (n=%d) over=%v, want %d bytes over=%v",
			len(c.src), c.budget, dst.Len(), n, over, len(c.want), c.over}
		assert.Equal(t, c.want, dst.String(), msg...)
		assert.Equal(t, int64(len(c.want)), n, msg...)
		assert.Equal(t, c.over, over, msg...)
	}
}
