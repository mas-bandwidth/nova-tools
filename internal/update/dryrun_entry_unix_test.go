//go:build unix

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshot and moved read --dry-run once at entry, so every shape and every
// early refusal under it answers for itself: the skeleton fails only a call
// whose verb never read the flag, and that must never hide the real reason.
func TestDryRunKeepsEachAnswerItsOwn(t *testing.T) {
	t.Parallel()
	manifest := writeFile(t, "m.tsv", Header+"\nfoo\ttool\tv1.2.3\t-\tnone\tme\n")
	notGit := t.TempDir()
	for _, c := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"snapshot --file", []string{"snapshot", "--file", manifest, "--dry-run"}, 0, "SNAPSHOT OK checked=1 known=1 unknown=0"},
		{"snapshot --file unreadable", []string{"snapshot", "--file", "nope.tsv", "--dry-run"}, 2, "cannot open nope.tsv"},
		{"snapshot --bin unreadable", []string{"snapshot", "--bin", "nope", "--out", "x.tsv", "--dry-run"}, 2, "cannot read --bin nope"},
		{"moved off a checkout", []string{"moved", "--from", "a", "--to", "b", "--repo", notGit, "--out", "x.txt", "--dry-run"}, 2, "is not a git checkout"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, errs := runTool(t, "nova-version", c.args...)
			assert.Equal(t, c.code, code, out+errs)
			assert.Contains(t, out+errs, c.want)
			assert.NotContains(t, out+errs, "never read it")
		})
	}
}

// The skeleton owns the dry_run fact: a successful dry run carries it once, in
// the text and in the JSON, never once from the verb and again from the skeleton.
func TestDryRunFactIsSaidOnce(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nova-stub"), []byte("#!/bin/sh\nprintf 'nova-stub v1.0.0 linux/amd64 go1.0\\n'\n"), 0o755))
	out := filepath.Join(t.TempDir(), "s.tsv")
	manifest := writeFile(t, "m.tsv", Header+"\nfoo\ttool\tv1.2.3\t-\tnone\tme\n")
	for _, args := range [][]string{
		{"snapshot", "--bin", bin, "--out", out, "--dry-run"},
		{"snapshot", "--file", manifest, "--dry-run"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			code, text, errs := runTool(t, "nova-version", args...)
			require.Equal(t, 0, code, errs)
			assert.Equal(t, 1, strings.Count(text, "dry_run=true"), text)
			code, js, errs := runTool(t, "nova-version", append(args, "--json")...)
			require.Equal(t, 0, code, errs)
			assert.Equal(t, 1, strings.Count(js, `"dry_run"`), js)
			assert.NoFileExists(t, out)
		})
	}
}
