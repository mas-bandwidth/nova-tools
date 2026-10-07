package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusGrammar pins the one status grammar docs/STANDARD.md section 2
// holds for every tool: after the verb token the first word is OK, REFUSED or
// FAILED, and the exit code tells the same truth (0 done, 1 the verb ran and
// said no, 2 it could not run). Each row drives one outcome of one verb
// through run and asserts that word and that exit together, so a renamed word
// that leaves its exit behind turns this test red. Status has no FAILED row
// because its exit table holds only 0 and 2 (help.go): a blown box still
// reports at exit 0, and an unreadable one is a refusal. Lift lockdown has
// only its refusal, by design. Path, version and help print no status line at
// all (path echoes the plumbing, version prints the build line, help prints
// usage), so this test covers the verbs that print one.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	// readOnlyDir is a directory that refuses writes, so a write verb fails
	// loudly instead of succeeding. It mirrors TestBlowingFailsLoudlyWhenItCannotWrite.
	readOnlyDir := func(t *testing.T) string {
		t.Helper()
		if runtime.GOOS == "windows" {
			t.Skip("windows: chmod 0555 does not make a directory refuse writes, so the failure this pins cannot be produced here")
		}
		if os.Geteuid() == 0 {
			t.Skip("running as root: a read-only directory does not refuse writes")
		}
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o555))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return dir
	}

	cases := []struct {
		name string
		// setup builds the fixture and returns the invocation to run.
		setup func(t *testing.T) []string
		// wantToken is the verb token that opens the first output line.
		wantToken string
		// wantWord is the first word after it: OK, REFUSED or FAILED.
		wantWord string
		// wantMarker is the refusal shape a REFUSED row carries on stderr.
		wantMarker string
		wantExit   int
	}{
		{
			name: "init ok",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"init", "--box", absentBoxIn(t)}
			},
			wantToken: "INIT", wantWord: "OK", wantExit: 0,
		},
		{
			name: "init refused without a box",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"init"}
			},
			wantToken: "nova-fuse", wantWord: "REFUSED",
			wantMarker: "run: nova-fuse help", wantExit: 2,
		},
		{
			name: "init failed over an existing box",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"init", "--box", boxIn(t)}
			},
			wantToken: "INIT", wantWord: "FAILED", wantExit: 1,
		},
		{
			name: "status ok",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"status", "--box", boxIn(t)}
			},
			wantToken: "STATUS", wantWord: "OK", wantExit: 0,
		},
		{
			name: "status refused without a box",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"status"}
			},
			wantToken: "nova-fuse", wantWord: "REFUSED",
			wantMarker: "run: nova-fuse help", wantExit: 2,
		},
		{
			name: "check ok",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"check", "--box", boxIn(t), "discord"}
			},
			wantToken: "FUSE", wantWord: "OK", wantExit: 0,
		},
		{
			name: "check refused without a box",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"check"}
			},
			wantToken: "nova-fuse", wantWord: "REFUSED",
			wantMarker: "run: nova-fuse help", wantExit: 2,
		},
		{
			name: "check failed over a quarantine",
			setup: func(t *testing.T) []string {
				t.Helper()
				box := boxIn(t)
				exit, _, stderr := runFuse(t, "quarantine", "--box", box, "discord", "many the same way")
				require.Equal(t, 0, exit, "setup quarantine: exit %d; stderr: %s", exit, stderr)
				return []string{"check", "--box", box, "discord"}
			},
			wantToken: "FUSE", wantWord: "FAILED", wantExit: 1,
		},
		{
			name: "lockdown ok",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"lockdown", "--box", absentBoxIn(t), "suspected compromise"}
			},
			wantToken: "LOCKDOWN", wantWord: "OK", wantExit: 0,
		},
		{
			name: "lockdown refused without a reason",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"lockdown"}
			},
			wantToken: "nova-fuse", wantWord: "REFUSED",
			wantMarker: "run: nova-fuse help", wantExit: 2,
		},
		{
			name: "lockdown failed when the box cannot be written",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"lockdown", "--box", filepath.Join(readOnlyDir(t), "fuses.json"), "suspected compromise"}
			},
			wantToken: "LOCKDOWN", wantWord: "FAILED", wantExit: 1,
		},
		{
			name: "quarantine ok",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"quarantine", "--box", boxIn(t), "discord", "many the same way"}
			},
			wantToken: "QUARANTINE", wantWord: "OK", wantExit: 0,
		},
		{
			name: "quarantine refused without a box",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"quarantine"}
			},
			wantToken: "nova-fuse", wantWord: "REFUSED",
			wantMarker: "run: nova-fuse help", wantExit: 2,
		},
		{
			name: "quarantine failed when the box cannot be written",
			setup: func(t *testing.T) []string {
				t.Helper()
				if runtime.GOOS == "windows" {
					t.Skip("windows: chmod 0555 does not make a directory refuse writes, so the failure this pins cannot be produced here")
				}
				if os.Geteuid() == 0 {
					t.Skip("running as root: a read-only directory does not refuse writes")
				}
				dir := t.TempDir()
				box := filepath.Join(dir, "fuses.json")
				require.NoError(t, fuse.CreateBox(box))
				require.NoError(t, os.Chmod(dir, 0o555))
				t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
				return []string{"quarantine", "--box", box, "discord", "many the same way"}
			},
			wantToken: "QUARANTINE", wantWord: "FAILED", wantExit: 1,
		},
		{
			name: "lift quarantine ok",
			setup: func(t *testing.T) []string {
				t.Helper()
				box := boxIn(t)
				exit, _, stderr := runFuse(t, "quarantine", "--box", box, "discord", "many the same way")
				require.Equal(t, 0, exit, "setup quarantine: exit %d; stderr: %s", exit, stderr)
				return []string{"lift", "quarantine", "--box", box, "discord"}
			},
			wantToken: "LIFT", wantWord: "OK", wantExit: 0,
		},
		{
			name: "lift quarantine refused without a box",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"lift", "quarantine"}
			},
			wantToken: "nova-fuse", wantWord: "REFUSED",
			wantMarker: "run: nova-fuse help", wantExit: 2,
		},
		{
			name: "lift quarantine failed with nothing to lift",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"lift", "quarantine", "--box", boxIn(t), "discord"}
			},
			wantToken: "LIFT", wantWord: "FAILED", wantExit: 1,
		},
		{
			name: "lift lockdown refused forever",
			setup: func(t *testing.T) []string {
				t.Helper()
				return []string{"lift", "lockdown"}
			},
			wantToken: "nova-fuse", wantWord: "REFUSED",
			wantMarker: "REFUSED, forever, by design", wantExit: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exit, stdout, stderr := runFuse(t, tc.setup(t)...)
			require.Equal(t, tc.wantExit, exit, "exit = %d, want %d\nstdout: %q\nstderr: %q", exit, tc.wantExit, stdout, stderr)
			if tc.wantWord == "REFUSED" {
				assert.Empty(t, stdout, "a refusal must print nothing on stdout, got %q", stdout)
				first := strings.Split(strings.TrimRight(stderr, "\n"), "\n")[0]
				assert.True(t, strings.HasPrefix(first, tc.wantToken), "the refusal must open with %q, got %q", tc.wantToken, first)
				assert.Contains(t, stderr, tc.wantMarker, "the refusal must carry its marker, got %q", stderr)
				return
			}
			stream := stdout
			if tc.wantExit != 0 {
				stream = stderr
			}
			first := strings.Split(strings.TrimRight(stream, "\n"), "\n")[0]
			fields := strings.Fields(first)
			require.GreaterOrEqual(t, len(fields), 2, "the first line must open with a verb token and a status word, got %q", first)
			assert.Equal(t, tc.wantToken, fields[0], "the verb token is %q, want %q in %q", fields[0], tc.wantToken, first)
			assert.Equal(t, tc.wantWord, fields[1], "the word after the verb token is %q, want %q in %q", fields[1], tc.wantWord, first)
		})
	}
}
