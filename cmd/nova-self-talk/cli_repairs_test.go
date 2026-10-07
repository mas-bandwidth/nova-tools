package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// STEP 3: flags stand before, between or after the files (docs/STANDARD.md
// section 1, one shape across the set; nova-memory's parse takes them anywhere).
// A late flag is a flag, so the file is still read and the run is the same run.
func TestFlagsAfterFilesAreStillFlags(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "journal.md", "I cannot check my own work.\n")
	t.Run("after the file", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := run([]string{f, "--json"}, &out, &errOut)
		require.Equal(t, 1, code, "late --json: exit %d out=%q err=%q", code, out.String(), errOut.String())
		require.Zero(t, errOut.Len(), "late --json is a flag, not a mistake: exit %d err=%q", code, errOut.String())
		require.Contains(t, out.String(), `"verb":"scan"`, "late --json must choose the JSON rendering: %s", out.String())
		require.NotContains(t, out.String(), "flags come before files", "a late flag is a flag: %s", out.String())
	})
	t.Run("between two files", func(t *testing.T) {
		g := write(t, t.TempDir(), "second.md", "A neutral sentence.\n")
		var out, errOut bytes.Buffer
		code := run([]string{f, "--max", "5", g}, &out, &errOut)
		require.Equal(t, 1, code, "a flag between files: exit %d out=%q err=%q", code, out.String(), errOut.String())
		require.Contains(t, out.String(), "files=2", "both files are read, the flag is not one of them: %s", out.String())
		require.NotContains(t, errOut.String(), "flags come before files", "a flag between files is a flag: %s", errOut.String())
	})
}

func TestFlagErrorsUseOnlyRelevantHints(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--bogus"}, {"--max", "many"}} {
		var out, errOut bytes.Buffer
		code := run(args, &out, &errOut)
		assert.Equal(t, 2, code, "%v: exit %d err=%s", args, code, errOut.String())
		assert.NotContains(t, errOut.String(), "BASENAME", "%v: exit %d err=%s", args, code, errOut.String())
	}
}

func TestStandingFindingsNameOriginalLines(t *testing.T) {
	t.Parallel()
	f := write(t, t.TempDir(), "journal.md", "A neutral sentence.\n\n**I cannot\ncheck my own work.**\n\nI cannot check my own work.\n\nI cannot ever get this right.\n")
	var out, errOut bytes.Buffer
	code := run([]string{f}, &out, &errOut)
	require.Equal(t, 1, code, "exit %d err=%s", code, errOut.String())
	for _, line := range []string{":3: STANDING match=", ":6: STANDING match=", ":8: STANDING match="} {
		assert.Contains(t, errOut.String(), f+line, "missing %s: %s", line, errOut.String())
	}
}

func TestAllSkippedReportsNoScan(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "skip.md")
	var out, errOut bytes.Buffer
	code := run([]string{"--skip", "skip.md", missing}, &out, &errOut)
	require.Equal(t, 0, code, "all skipped: exit %d out=%q err=%q", code, out.String(), errOut.String())
	require.Zero(t, errOut.Len(), "all skipped: exit %d out=%q err=%q", code, out.String(), errOut.String())
	require.Contains(t, out.String(), "SELFTALK SKIP files=0 skipped=1 reason=all-skipped", "all skipped: exit %d out=%q err=%q", code, out.String(), errOut.String())
	require.NotContains(t, out.String(), "SELFTALK OK", "all skipped: exit %d out=%q err=%q", code, out.String(), errOut.String())
	require.Contains(t, out.String(), "SELFTALK NOTE", "all skipped: exit %d out=%q err=%q", code, out.String(), errOut.String())
}

func TestSeparatorProtectsLiteralFilesNotFlagValues(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--skip", "--max", "--", "--max"},
		{"--skip", "--skip", "--", "--skip"},
		{"--max", "1", "--skip", "a.md", "--skip", "--max", "a.md", "--", "--max"},
	} {
		var out, errOut bytes.Buffer
		code := run(args, &out, &errOut)
		assert.Equal(t, 0, code, "literal files %v: exit %d out=%s err=%s", args, code, out.String(), errOut.String())
		assert.Contains(t, out.String(), "reason=all-skipped", "literal files %v: exit %d out=%s err=%s", args, code, out.String(), errOut.String())
		assert.Zero(t, errOut.Len(), "literal files %v: exit %d out=%s err=%s", args, code, out.String(), errOut.String())
	}
	var out, errOut bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.md")
	code := run([]string{"--skip", "--", missing, "--max", "1"}, &out, &errOut)
	require.Equal(t, 2, code, "flag value was mistaken for terminator: exit %d err=%s", code, errOut.String())
	require.Contains(t, errOut.String(), missing, "the unreadable file is named: exit %d err=%s", code, errOut.String())
	require.NotContains(t, errOut.String(), `"--max"`, "a value spelled -- is not the terminator; --max is still a flag, not a file: %s", errOut.String())
}
