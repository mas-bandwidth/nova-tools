//go:build unix

package filelock

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestVerifyInode_EdgeCases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "verify.lock")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Normal match
	match, err := verifyInode(f, path)
	if err != nil || !match {
		t.Fatalf("verifyInode normal = %v, %v, want true, nil", match, err)
	}

	// File removed from disk
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	matchRemoved, err := verifyInode(f, path)
	if err != nil || matchRemoved {
		t.Errorf("verifyInode removed = %v, %v, want false, nil", matchRemoved, err)
	}

	// Recreated different file at path
	if err := os.WriteFile(path, []byte("new"), 0666); err != nil {
		t.Fatal(err)
	}
	matchRecreated, err := verifyInode(f, path)
	if err != nil || matchRecreated {
		t.Errorf("verifyInode recreated = %v, %v, want false, nil", matchRecreated, err)
	}

	// Pass directory as fd
	dirF, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer dirF.Close()
	_, errDir := verifyInode(dirF, dir)
	if errDir == nil {
		t.Errorf("verifyInode on directory should error")
	}
}

func TestOpenFileSafe_FIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "test.fifo")
	if err := syscall.Mkfifo(fifoPath, 0666); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}

	// openFileSafe with O_RDONLY must not block on FIFO and must refuse it as not a regular file
	_, err := openFileSafe(fifoPath, os.O_RDONLY, 0)
	if err == nil {
		t.Fatalf("openFileSafe on FIFO succeeded, want error")
	}

	// ReadStamp on FIFO must also not block and return error
	_, err = ReadStamp(fifoPath)
	if err == nil {
		t.Fatalf("ReadStamp on FIFO succeeded, want error")
	}
}
