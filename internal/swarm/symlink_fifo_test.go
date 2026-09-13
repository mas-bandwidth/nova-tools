package swarm

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// THE DISPATCHER READS AND WRITES WORKER-WRITABLE PATHS, AND A WORKER OWNS ITS JOB
// DIRECTORY (security#30, findings 2, 3 and 4).
//
// sandbox.go makes <job> the worker's first --write and its --cwd, so every name under it
// is the worker's to choose: a symlink pointing out of the wall, or a FIFO nobody will ever
// write. The dispatcher runs OUTSIDE the wall. These tests plant both, in a temp dir, and
// ask that a read of a path that is not a regular file is no result and a write never
// lands on the far end of a link.
func plantLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
}

func plantFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("this platform will not make a FIFO: %v", err)
	}
}

func outsideFile(t *testing.T, dir string) string {
	t.Helper()
	v := filepath.Join(dir, "outside-the-wall")
	if err := os.WriteFile(v, []byte("a secret the wall was keeping\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return v
}

// Finding 2: a report that is a symlink is not this job's report.
func TestReadFileSteadyRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	v := outsideFile(t, dir)
	plantLink(t, v, ResultPath(job))
	raw, err := readFileSteady(ResultPath(job))
	if err == nil {
		t.Fatalf("the dispatcher read through a planted symlink and got %q", string(raw))
	}
	if missing(err) {
		t.Fatal("a planted symlink must not read as a record that is simply gone")
	}
}

func TestHarnessTailRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	v := outsideFile(t, dir)
	plantLink(t, v, filepath.Join(job, "harness.log"))
	if tail := HarnessTail(job); tail != "" {
		t.Fatalf("the log= tail carried a file from outside the wall: %q", tail)
	}
}

// Finding 3: the atomic write's temporary is the dispatcher's, never the worker's.
func TestWriteAtomicDoesNotWriteThroughAPlantedTemp(t *testing.T) {
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	v := outsideFile(t, dir)
	target := ExitPath(job)
	plantLink(t, v, target+".tmp")
	if err := writeAtomic(target, []byte("{\"rc\":0}\n"), 0o644); err != nil {
		t.Fatalf("a correct write was refused: %v", err)
	}
	raw, err := os.ReadFile(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "a secret the wall was keeping\n" {
		t.Fatalf("the supervisor truncated a file outside the job through the planted temporary: %q", string(raw))
	}
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatal("the rename moved the planted link over the record's own path")
	}
}

func TestAppendNoteRefusesASymlinkedNoteFile(t *testing.T) {
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	v := outsideFile(t, dir)
	plantLink(t, v, NotePath(job))
	if _, err := AppendNote(job, "a note", time.Now()); err == nil {
		t.Fatal("AppendNote appended through a symlinked note file and raised nothing")
	}
	raw, err := os.ReadFile(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "a secret the wall was keeping\n" {
		t.Fatalf("the note landed outside the job: %q", string(raw))
	}
}

// Finding 4: a FIFO is not a record, and it must never park the dispatcher.
func TestReadFileSteadyDoesNotBlockOnAFIFO(t *testing.T) {
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	plantFIFO(t, ResultPath(job))
	done := make(chan error, 1)
	go func() {
		_, err := readFileSteady(ResultPath(job))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO read as a published report")
		}
		if missing(err) {
			t.Fatal("a FIFO must not read as a record that is simply gone")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("STILL BLOCKED after 5s reading a FIFO at RESULT.md: the dispatcher is wedged")
	}
}

func TestReadJSONDoesNotBlockOnAFIFO(t *testing.T) {
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	plantFIFO(t, ExitPath(job))
	done := make(chan error, 1)
	go func() {
		var ex ExitRecord
		done <- ReadJSON(ExitPath(job), &ex)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO read as an exit record")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("STILL BLOCKED after 5s reading a FIFO at exit.json: the recovery pass is wedged")
	}
}
