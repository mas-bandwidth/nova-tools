package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLateFlagsRefuseBeforeReadingFiles(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.md")
	var out, errOut bytes.Buffer
	code := run([]string{missing, "--max", "1"}, &out, &errOut)
	require.Equal(t, 2, code, "late flag: exit %d out=%q err=%q", code, out.String(), errOut.String())
	require.Zero(t, out.Len(), "late flag: exit %d out=%q err=%q", code, out.String(), errOut.String())
	require.Contains(t, errOut.String(), "flags come before files", "late flag: exit %d out=%q err=%q", code, out.String(), errOut.String())
	require.NotContains(t, errOut.String(), "no such file", "late flag: exit %d out=%q err=%q", code, out.String(), errOut.String())
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
	code := run([]string{"--skip", "--", filepath.Join(t.TempDir(), "missing.md"), "--max", "1"}, &out, &errOut)
	require.Equal(t, 2, code, "flag value was mistaken for terminator: exit %d err=%s", code, errOut.String())
	require.Contains(t, errOut.String(), "flags come before files", "flag value was mistaken for terminator: exit %d err=%s", code, errOut.String())
	require.NotContains(t, errOut.String(), "no such file", "flag value was mistaken for terminator: exit %d err=%s", code, errOut.String())
}
