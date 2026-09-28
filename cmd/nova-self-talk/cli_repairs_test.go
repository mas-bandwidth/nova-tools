package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestLateFlagsRefuseBeforeReadingFiles(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.md")
	var out, errOut bytes.Buffer
	code := run([]string{missing, "--max", "1"}, &out, &errOut)
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "flags come before files") || strings.Contains(errOut.String(), "no such file") {
		t.Fatalf("late flag: exit %d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestFlagErrorsUseOnlyRelevantHints(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--bogus"}, {"--max", "many"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 || strings.Contains(errOut.String(), "BASENAME") {
			t.Errorf("%v: exit %d err=%s", args, code, errOut.String())
		}
	}
}

func TestStandingFindingsNameOriginalLines(t *testing.T) {
	t.Parallel()
	f := write(t, t.TempDir(), "journal.md", "A neutral sentence.\n\n**I cannot\ncheck my own work.**\n\nI cannot check my own work.\n\nI cannot ever get this right.\n")
	var out, errOut bytes.Buffer
	if code := run([]string{f}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d err=%s", code, errOut.String())
	}
	for _, line := range []string{":3: STANDING:", ":6: STANDING:", ":8: STANDING:"} {
		if !strings.Contains(errOut.String(), f+line) {
			t.Errorf("missing %s: %s", line, errOut.String())
		}
	}
}

func TestAllSkippedReportsNoScan(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "skip.md")
	var out, errOut bytes.Buffer
	code := run([]string{"--skip", "skip.md", missing}, &out, &errOut)
	if code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "SELFTALK SKIP files=0 skipped=1 reason=all-skipped") || strings.Contains(out.String(), "SELFTALK OK") || !strings.Contains(out.String(), "SELFTALK NOTE") {
		t.Fatalf("all skipped: exit %d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestSeparatorProtectsLiteralFilesNotFlagValues(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--skip", "--max", "--", "--max"},
		{"--skip", "--skip", "--", "--skip"},
		{"--max", "1", "--skip", "a.md", "--skip", "--max", "a.md", "--", "--max"},
	} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "reason=all-skipped") || errOut.Len() != 0 {
			t.Errorf("literal files %v: exit %d out=%s err=%s", args, code, out.String(), errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	code := run([]string{"--skip", "--", filepath.Join(t.TempDir(), "missing.md"), "--max", "1"}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "flags come before files") || strings.Contains(errOut.String(), "no such file") {
		t.Fatalf("flag value was mistaken for terminator: exit %d err=%s", code, errOut.String())
	}
}
