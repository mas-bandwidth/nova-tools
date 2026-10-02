package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gate_flags_once_test.go: every gate flag takes one value. Package flag keeps the LAST of a
// repeated flag, so `--head HEAD --head HEAD~1` judged the diff HEAD~1..HEAD~1 and printed
// GATE APPROVE files=0 at exit 0 over a head the gate refuses.
func TestGateRefusesAFlagGivenTwice(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %v %s", args, err, out)
	}
	base := []string{"gate", "--store", dir, "--base", "HEAD", "--head", "HEAD"}
	for _, c := range []struct {
		flag  string
		extra []string
	}{
		{"head", []string{"--head", "HEAD"}},
		{"head", []string{"--head=HEAD"}},
		{"base", []string{"--base", "HEAD"}},
		{"store", []string{"--store", dir}},
		{"machines", []string{"--machines", "a.tsv", "--machines", "b.tsv"}},
	} {
		stdout, stderr, code := runNovaSecrets(bin, append(append([]string{}, base...), c.extra...)...)
		if !assert.Equal(t, 2, code, "--%s twice: exit=%d stdout=%q stderr=%q; want exit 2 and nothing on stdout", c.flag, code, stdout, stderr) ||
			!assert.Empty(t, stdout, "--%s twice: exit=%d stdout=%q stderr=%q; want exit 2 and nothing on stdout", c.flag, code, stdout, stderr) {
			continue
		}
		want := "SECRETS REFUSED: --" + c.flag + " is given more than once"
		assert.True(t, strings.HasPrefix(stderr, want), "--%s twice: stderr=%q; want one line beginning %q", c.flag, stderr, want)
		assert.Equal(t, 1, strings.Count(stderr, "\n"), "--%s twice: stderr=%q; want one line beginning %q", c.flag, stderr, want)
	}
	// The same flags once each still approve an empty diff.
	stdout, stderr, code := runNovaSecrets(bin, base...)
	assert.Equal(t, 0, code, "each flag once: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	assert.True(t, strings.HasPrefix(stdout, "GATE APPROVE files=0"), "each flag once: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
}

// The once-only wrapper does not break the verb-help seam: `gate -h` is help at exit 0, and
// the help reads the flags' own types, because the wrapper is taken off before verbflag's
// Recover prints them.
func TestGateHelpSurvivesTheOnceOnlyFlags(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	for _, h := range []string{"-h", "--help"} {
		stdout, stderr, code := runNovaSecrets(bin, "gate", h)
		assert.Equal(t, 0, code, "gate %s: exit=%d stdout=%q stderr=%q; want help at exit 0", h, code, stdout, stderr)
		assert.True(t, strings.HasPrefix(stdout, "usage: nova-secrets gate"), "gate %s: exit=%d stdout=%q stderr=%q; want help at exit 0", h, code, stdout, stderr)
		assert.Contains(t, stdout, "--head <ref>", "gate %s: the help does not read the flags' own types:\n%s", h, stdout)
		assert.NotContains(t, stdout, "<value>", "gate %s: the help does not read the flags' own types:\n%s", h, stdout)
	}
}
