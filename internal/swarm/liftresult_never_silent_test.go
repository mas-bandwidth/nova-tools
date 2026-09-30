package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLiftResultSaysWhenTheCopyFails holds the never-silent rule at liftResult
// (issue #594): a RESULT.md the card published inside its clone is copied up to
// the job root, and when the copy fails the card is about to be scored
// no-result, so one BATCH NOTE line says why, where the result is, and what to
// do by hand. The job root is made read-only so the write fails.
func TestLiftResultSaysWhenTheCopyFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory stops a write only for a non-root user on a POSIX system")
	}

	job := t.TempDir()
	from := filepath.Join(job, "repo", "RESULT.md")
	if err := os.MkdirAll(filepath.Dir(from), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("RESULT: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(job, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(job, 0o755) })

	var notes bytes.Buffer
	liftResult(job, "card-1", &notes)

	got := notes.String()
	for _, want := range []string{
		"BATCH NOTE card-1 RESULT.md not copied up from " + from,
		"copy it to " + filepath.Join(job, "RESULT.md") + " by hand and re-gather",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the note lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "copied up from") && !strings.Contains(got, "not copied up") {
		t.Errorf("a failed copy said it copied:\n%s", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("the note is not one line:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(job, "RESULT.md")); err == nil {
		t.Errorf("a result appeared at the job root though the write was refused")
	}
}

// TestLiftResultSaysWhenTheCopyWorks is the other branch of the same function:
// the copy happened, the note says so, and the bytes are the card's.
func TestLiftResultSaysWhenTheCopyWorks(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	from := filepath.Join(job, "repo", "RESULT.md")
	if err := os.MkdirAll(filepath.Dir(from), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("RESULT: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var notes bytes.Buffer
	liftResult(job, "card-1", &notes)

	if want := "BATCH NOTE card-1 RESULT.md copied up from " + from + "\n"; notes.String() != want {
		t.Errorf("note = %q, want %q", notes.String(), want)
	}
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil || string(raw) != "RESULT: done\n" {
		t.Errorf("job root result = %q, %v", raw, err)
	}
}
