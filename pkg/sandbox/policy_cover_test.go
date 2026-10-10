// Unit coverage for the three answers pkg/sandbox/policy.go gives that the
// per-function coverage table of the unit tier held at 0.0%: Refusal.Error
// (policy.go:49), Policy.CmdName (policy.go:138) and ResolveCallerFile
// (policy.go:492) — the same shape as wrap_darwin_cover_test.go. Every test is
// in process: no sleeps, no real time, no network, no subprocess, no Redis or
// Postgres. ResolveCallerFile needs only a file, and it is written into the
// test's own t.TempDir(); Error and CmdName need nothing but their receiver.
// Each listed function gets its main path and one refusal where it has one:
// Error IS the rendering of a refusal, so every row of its table is a refusal;
// ResolveCallerFile's refusals are its table; CmdName is a one-line accessor
// with no refusal path, so its table pins the spellings of its answer.
package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPolicyCoverRefusalError pins the SANDBOX REFUSED line's grammar: the reason token,
// then ": ", then the text. A reader parses the reason= token out of this line, so the
// join is the contract, not the spacing.
func TestPolicyCoverRefusalError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  Refusal
		want string
	}{
		{"reason then colon space then text",
			Refusal{Reason: "bad_read", Text: "--read /nope does not exist; every path is named by the caller and none is created"},
			"bad_read: --read /nope does not exist; every path is named by the caller and none is created"},
		{"refuse formats the text, so every production refusal renders through it",
			refuse("bad_write", "--write %s is relative; --write wants an absolute path, which here would be %s", "job", "/cwd/job"),
			"bad_write: --write job is relative; --write wants an absolute path, which here would be /cwd/job"},
		{"the reason still leads when the text is empty",
			Refusal{Reason: "no_command"},
			"no_command: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.ref.Error())
		})
	}
}

// TestPolicyCoverCmdName pins that only the base name of the resolved command is ever
// printable: arguments carry task text, and the name arrives here already absolute, so
// whatever directory it was found in never reaches the line.
func TestPolicyCoverCmdName(t *testing.T) {
	t.Parallel()
	root := string(filepath.Separator)
	for _, tc := range []struct {
		name    string
		command string
		want    string
	}{
		{"the base of an absolute path", filepath.Join(root, "usr", "bin", "git"), "git"},
		{"a command under a directory of its own", filepath.Join(root, "opt", "my tools", "run"), "run"},
		{"a name with no directory is already the base", "git", "git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Policy{Command: tc.command}
			assert.Equal(t, tc.want, p.CmdName())
		})
	}
}

// TestPolicyCoverResolveCallerFile pins the four answers of the --secret path: a file that
// is there resolves to itself, and the empty, relative, missing and directory spellings
// are each refused naming the flag and what it wants. The refusal rows assert through
// Error(), so the real production refusal renders here too.
func TestPolicyCoverResolveCallerFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	secret := filepath.Join(dir, "gh-token")
	require.NoError(t, os.WriteFile(secret, []byte("x"), 0o600))

	for _, tc := range []struct {
		name       string
		raw        string
		wantReason string // empty: the path must resolve
		wantText   string
	}{
		{name: "a file that exists resolves", raw: secret},
		{name: "empty wants the file", raw: "", wantReason: "bad_read",
			wantText: "--secret wants a path to the file this probe proves it cannot read: --secret <path>"},
		{name: "relative is refused with its absolute form offered", raw: filepath.Join("secrets", "gh-token"),
			wantReason: "bad_read", wantText: "is relative; --secret wants an absolute path"},
		{name: "missing is refused, a probe against it proves nothing", raw: filepath.Join(dir, "not-there"),
			wantReason: "bad_read", wantText: "does not exist; a probe against a file that is not there proves nothing"},
		{name: "a directory is refused, the file itself is wanted", raw: dir,
			wantReason: "bad_read", wantText: "is a directory; --secret wants the credential file itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, r := ResolveCallerFile("--secret", tc.raw)
			if tc.wantReason == "" {
				require.Nil(t, r, "a real file was refused: %v", r)
				require.True(t, filepath.IsAbs(got), "the resolved path %q is not absolute", got)
				gotFI, err := os.Stat(got)
				require.NoError(t, err, "the resolved path %q does not exist", got)
				wantFI, err := os.Stat(secret)
				require.NoError(t, err, "the planted file vanished mid-test")
				assert.True(t, os.SameFile(gotFI, wantFI), "the resolved path %q is not the planted file %q", got, secret)
				return
			}
			require.NotNil(t, r, "ResolveCallerFile(%q) returned no refusal and %q", tc.raw, got)
			assert.Equal(t, tc.wantReason, r.Reason)
			assert.Contains(t, r.Error(), tc.wantText)
		})
	}
}
