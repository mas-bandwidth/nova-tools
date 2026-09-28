package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The known limit, pinned as behaviour rather than left to be discovered:
// mtime:size cannot see a rewrite that preserves both.
func TestATouchedReportWithTheSameSizeAndMtimeDoesNotWake(t *testing.T) {
	t.Parallel()

	reports := t.TempDir()
	path := filepath.Join(reports, "job", "RESULT.md")
	write(t, path, "# a finding\n")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--reports", reports}
	wakeRun(t, args...) // the cold poll records it

	// A rewrite of the same length, with the mtime put back.
	write(t, path, "# a FINDING\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("this is the named limit and it is behaviour, not an accident:\n%s", r.all())
	}

	// The same limit, in the shape that catches a compared identity carrying
	// one field more than mtime:size. "ab\n" and "a\nb" are the same length and
	// a different number of lines, and the spec says THIS rewrite must not
	// wake: a content digest "would close it and costs a read of every watched
	// file on every poll; it is a v2 item behind a flag".
	write(t, path, "ab\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	wakeRun(t, args...) // whatever that rewrite was, it is recorded now
	write(t, path, "a\nb")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	r = wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Fatalf("a rewrite that preserved mtime and size woke the window on its LINE COUNT; the state value of a report file is mtime:size, and the limit above is pinned as behaviour:\n%s", r.all())
	}

	// And the other half: a real modification DOES wake, and its line still
	// carries lines= so the window can tell a stub from a finding.
	write(t, path, "# a finding, and a second line\n")
	r = wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE REPORT path=") || !strings.Contains(r.stdout, "modified") {
		t.Errorf("a modified report must wake:\n%s", r.all())
	}
	if !strings.Contains(r.stdout, "lines=1") {
		t.Errorf("the WAKE REPORT line must still carry the line count, which is display and not identity:\n%s", r.stdout)
	}
}

// A file that DISAPPEARS is not a change and its key is kept: a job directory
// being rebuilt is not news, and the window does not want to be woken by an rm.
func TestAVanishedReportIsNotAChange(t *testing.T) {
	t.Parallel()

	reports := t.TempDir()
	path := filepath.Join(reports, "job", "RESULT.md")
	write(t, path, "# a finding\n")
	state := filepath.Join(t.TempDir(), "wake.state")
	args := []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--reports", reports}
	wakeRun(t, args...)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE QUIET") {
		t.Errorf("an rm woke the window:\n%s", r.all())
	}
	if !strings.Contains(read(t, state), "report:") {
		t.Errorf("the key was dropped; a job directory being rebuilt must not be new again:\n%s", read(t, state))
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
