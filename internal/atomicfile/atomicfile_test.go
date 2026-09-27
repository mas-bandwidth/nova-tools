package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWriteSuccessAndFileModes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	testCases := []struct {
		name     string
		filename string
		initial  []byte
		updated  []byte
		mode     os.FileMode
		useAlias bool
	}{
		{
			name:     "new_file_0644",
			filename: "file1.txt",
			initial:  []byte("hello world\n"),
			mode:     0o644,
		},
		{
			name:     "overwrite_0600",
			filename: "file2.txt",
			initial:  []byte("initial content\n"),
			updated:  []byte("updated private content\n"),
			mode:     0o600,
		},
		{
			name:     "overwrite_0755",
			filename: "file3.sh",
			initial:  []byte("#!/bin/sh\necho old\n"),
			updated:  []byte("#!/bin/sh\necho new\n"),
			mode:     0o755,
		},
		{
			name:     "read_only_0444",
			filename: "file4.ro",
			initial:  []byte("read only data\n"),
			mode:     0o444,
		},
		{
			name:     "empty_content_via_writefile_alias",
			filename: "file5.empty",
			initial:  []byte{},
			mode:     0o644,
			useAlias: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := filepath.Join(dir, tc.filename)

			writeFn := Write
			if tc.useAlias {
				writeFn = WriteFile
			}

			if err := writeFn(target, tc.initial, tc.mode); err != nil {
				t.Fatalf("Write(%q) failed: %v", target, err)
			}

			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("ReadFile(%q) failed: %v", target, err)
			}
			if !bytes.Equal(got, tc.initial) {
				t.Fatalf("ReadFile(%q) = %q, want %q", target, got, tc.initial)
			}

			info, err := os.Stat(target)
			if err != nil {
				t.Fatalf("Stat(%q) failed: %v", target, err)
			}
			if perm := info.Mode().Perm(); perm != tc.mode {
				t.Fatalf("file perm = %04o, want %04o", perm, tc.mode)
			}

			if tc.updated != nil {
				if err := writeFn(target, tc.updated, tc.mode); err != nil {
					t.Fatalf("second Write(%q) failed: %v", target, err)
				}
				gotUpdated, err := os.ReadFile(target)
				if err != nil {
					t.Fatalf("ReadFile(%q) after update failed: %v", target, err)
				}
				if !bytes.Equal(gotUpdated, tc.updated) {
					t.Fatalf("ReadFile(%q) = %q, want %q", target, gotUpdated, tc.updated)
				}
				infoUpdated, err := os.Stat(target)
				if err != nil {
					t.Fatalf("Stat(%q) after update failed: %v", target, err)
				}
				if perm := infoUpdated.Mode().Perm(); perm != tc.mode {
					t.Fatalf("file perm after update = %04o, want %04o", perm, tc.mode)
				}
			}
		})
	}
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	t.Run("empty_path", func(t *testing.T) {
		t.Parallel()
		err := Write("", []byte("data"), 0o644)
		if err == nil {
			t.Fatal("Write with empty path succeeded; want error")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Fatalf("error %q does not mention empty", err)
		}
	})

	t.Run("target_is_directory", func(t *testing.T) {
		t.Parallel()
		sub := filepath.Join(dir, "subdir")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		err := Write(sub, []byte("data"), 0o644)
		if err == nil {
			t.Fatal("Write over existing directory succeeded; want refusal")
		}
		if !strings.Contains(err.Error(), sub) {
			t.Fatalf("error %q does not name path %q", err, sub)
		}
		st, err := os.Stat(sub)
		if err != nil || !st.IsDir() {
			t.Fatalf("directory %q was corrupted: %v", sub, err)
		}
	})

	t.Run("target_is_symlink", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "target.txt")
		if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.txt")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}

		err := Write(link, []byte("replacement\n"), 0o644)
		if err == nil {
			t.Fatal("Write over symlink succeeded; want refusal")
		}
		if !strings.Contains(err.Error(), link) {
			t.Fatalf("error %q does not name path %q", err, link)
		}

		lst, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("Lstat(%q) failed: %v", link, err)
		}
		if lst.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("link %q is no longer a symlink", link)
		}
		targetBytes, err := os.ReadFile(target)
		if err != nil || string(targetBytes) != "original\n" {
			t.Fatalf("target was modified: %s (%v)", string(targetBytes), err)
		}
	})

	t.Run("target_is_broken_symlink", func(t *testing.T) {
		t.Parallel()
		broken := filepath.Join(dir, "dangling.txt")
		if err := os.Symlink(filepath.Join(dir, "nonexistent.txt"), broken); err != nil {
			t.Fatal(err)
		}

		err := Write(broken, []byte("data\n"), 0o644)
		if err == nil {
			t.Fatal("Write over broken symlink succeeded; want refusal")
		}
		if !strings.Contains(err.Error(), broken) {
			t.Fatalf("error %q does not name path %q", err, broken)
		}
		lst, err := os.Lstat(broken)
		if err != nil || lst.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("broken link was modified or removed: %v", err)
		}
	})

	t.Run("target_in_readonly_directory", func(t *testing.T) {
		t.Parallel()
		roDir := filepath.Join(dir, "ro-dir")
		if err := os.Mkdir(roDir, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(roDir, "file.txt")
		if err := os.Chmod(roDir, 0o555); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(roDir, 0o755) }()

		err := Write(target, []byte("data"), 0o644)
		if err == nil {
			t.Fatal("Write in read-only directory succeeded; want refusal")
		}
		if !strings.Contains(err.Error(), target) {
			t.Fatalf("error %q does not name path %q", err, target)
		}
	})

	t.Run("parent_does_not_exist", func(t *testing.T) {
		t.Parallel()
		missingParent := filepath.Join(dir, "no-such-parent", "file.txt")
		err := Write(missingParent, []byte("data"), 0o644)
		if err == nil {
			t.Fatal("Write with non-existent parent succeeded; want refusal")
		}
		if !strings.Contains(err.Error(), missingParent) {
			t.Fatalf("error %q does not name path %q", err, missingParent)
		}
		if _, err := os.Stat(filepath.Dir(missingParent)); !os.IsNotExist(err) {
			t.Fatalf("parent directory was created silently: %v", err)
		}
	})

	t.Run("parent_is_not_a_directory", func(t *testing.T) {
		t.Parallel()
		regParent := filepath.Join(dir, "file_as_parent")
		if err := os.WriteFile(regParent, []byte("regular file\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(regParent, "child.txt")
		err := Write(target, []byte("data"), 0o644)
		if err == nil {
			t.Fatal("Write with non-dir parent succeeded; want refusal")
		}
		if !strings.Contains(err.Error(), target) {
			t.Fatalf("error %q does not name path %q", err, target)
		}
	})

	t.Run("unexpected_lstat_error", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "lstat_err.txt")
		injectedErr := errors.New("injected lstat failure")
		h := defaultHooks()
		h.lstat = func(name string) (os.FileInfo, error) {
			return nil, injectedErr
		}
		err := writeWithHooks(target, []byte("data"), 0o644, h)
		if err == nil {
			t.Fatal("writeWithHooks succeeded; want error")
		}
		if !strings.Contains(err.Error(), target) {
			t.Fatalf("error %q does not name path %q", err, target)
		}
		if !errors.Is(err, injectedErr) {
			t.Fatalf("error %v does not wrap injected error %v", err, injectedErr)
		}
	})
}

func TestStepFailureInjection(t *testing.T) {
	t.Parallel()

	steps := []string{"create", "chmod", "write", "sync", "close", "rename"}

	for _, step := range steps {
		step := step
		t.Run(step+"_failure_target_not_preexisting", func(t *testing.T) {
			t.Parallel()
			testStepFailure(t, step, false)
		})
		t.Run(step+"_failure_target_preexisting", func(t *testing.T) {
			t.Parallel()
			testStepFailure(t, step, true)
		})
	}
}

func testStepFailure(t *testing.T, failStep string, preexisting bool) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	const initialContent = "PRE-EXISTING CONTENT\n"
	const newContent = "NEW CONTENT THAT SHOULD FAIL\n"

	if preexisting {
		if err := os.WriteFile(target, []byte(initialContent), 0o644); err != nil {
			t.Fatalf("setup pre-existing file failed: %v", err)
		}
	}

	injectedErr := fmt.Errorf("injected failure at %s", failStep)
	h := defaultHooks()

	switch failStep {
	case "create":
		h.createTemp = func(dir, pattern string) (*os.File, error) {
			return nil, injectedErr
		}
	case "chmod":
		h.chmod = func(f *os.File, mode os.FileMode) error {
			return injectedErr
		}
	case "write":
		h.write = func(f *os.File, data []byte) (int, error) {
			return 0, injectedErr
		}
	case "sync":
		h.sync = func(f *os.File) error {
			return injectedErr
		}
	case "close":
		h.close = func(f *os.File) error {
			_ = f.Close()
			return injectedErr
		}
	case "rename":
		h.rename = func(oldpath, newpath string) error {
			return injectedErr
		}
	default:
		t.Fatalf("unknown step %q", failStep)
	}

	err := writeWithHooks(target, []byte(newContent), 0o644, h)
	if err == nil {
		t.Fatalf("writeWithHooks unexpectedly succeeded at %s step", failStep)
	}
	if !strings.Contains(err.Error(), target) {
		t.Fatalf("error %q does not name target path %q", err, target)
	}
	if !errors.Is(err, injectedErr) {
		t.Fatalf("error %v does not wrap injected error %v", err, injectedErr)
	}

	// Verify target file state
	if preexisting {
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("target was deleted on %s failure: %v", failStep, err)
		}
		if string(got) != initialContent {
			t.Fatalf("target was corrupted on %s failure: got %q, want %q", failStep, string(got), initialContent)
		}
	} else {
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("target was created despite %s failure: %v", failStep, err)
		}
	}

	// Verify NO temporary files remain in the target directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "target.txt" {
			t.Fatalf("leaked file %q in dir after %s failure", e.Name(), failStep)
		}
	}
}

func TestConcurrentWriters(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "concurrent_target.txt")

	const payloadSize = 32768
	payloadA := bytes.Repeat([]byte("A"), payloadSize)
	payloadB := bytes.Repeat([]byte("B"), payloadSize)

	for round := 0; round < 10; round++ {
		var wg sync.WaitGroup
		errs := make(chan error, 2)

		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := Write(target, payloadA, 0o644); err != nil {
				errs <- fmt.Errorf("writer A: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := Write(target, payloadB, 0o644); err != nil {
				errs <- fmt.Errorf("writer B: %w", err)
			}
		}()

		wg.Wait()
		close(errs)

		for err := range errs {
			t.Fatalf("concurrent writer error: %v", err)
		}

		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}

		isA := bytes.Equal(got, payloadA)
		isB := bytes.Equal(got, payloadB)
		if !isA && !isB {
			t.Fatalf("round %d: target holds corrupted mixed bytes (len=%d)", round, len(got))
		}
	}

	// Verify no temporary files leaked
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "concurrent_target.txt" {
			t.Fatalf("leaked file %q in dir", e.Name())
		}
	}
}

func TestReaderNeverSeesPartialFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "versioned.txt")

	makeVersionPayload := func(v int) []byte {
		// Large payload (4096 bytes) with recognizable header, body pattern, and trailer
		header := fmt.Sprintf("BEGIN %06d\n", v)
		trailer := fmt.Sprintf("END %06d\n", v)
		pattern := fmt.Sprintf("%08x", v)
		repeatCount := (4096 - len(header) - len(trailer)) / len(pattern)
		body := strings.Repeat(pattern, repeatCount)
		return []byte(header + body + "\n" + trailer)
	}

	// Write initial version 0
	if err := Write(target, makeVersionPayload(0), 0o644); err != nil {
		t.Fatalf("initial write failed: %v", err)
	}

	const totalWrites = 1000
	done := make(chan struct{})
	var readCount atomic.Int64
	var readErrors []string
	var errMu sync.Mutex

	recordErr := func(s string) {
		errMu.Lock()
		defer errMu.Unlock()
		if len(readErrors) < 10 {
			readErrors = append(readErrors, s)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)

	// Reader loop
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				data, err := os.ReadFile(target)
				if err != nil {
					recordErr(fmt.Sprintf("ReadFile error: %v", err))
					continue
				}
				readCount.Add(1)

				// Verify atomic content: must match makeVersionPayload(v) exactly
				var v int
				n, err := fmt.Sscanf(string(data), "BEGIN %d\n", &v)
				if err != nil || n != 1 {
					recordErr(fmt.Sprintf("corrupt header: %q", string(data[:min(len(data), 40)])))
					continue
				}
				expected := makeVersionPayload(v)
				if !bytes.Equal(data, expected) {
					recordErr(fmt.Sprintf("partial read for version %d: len %d, want %d", v, len(data), len(expected)))
				}
			}
		}
	}()

	// Writer writes 1,000 versions sequentially
	for v := 1; v <= totalWrites; v++ {
		if err := Write(target, makeVersionPayload(v), 0o644); err != nil {
			t.Fatalf("Write version %d failed: %v", v, err)
		}
	}

	close(done)
	wg.Wait()

	if len(readErrors) > 0 {
		t.Fatalf("reader saw %d partial/corrupt files; first error: %s", len(readErrors), readErrors[0])
	}

	t.Logf("Reader completed %d verified reads during %d atomic writes with 0 partial reads", readCount.Load(), totalWrites)

	// Final verification
	finalData, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile final failed: %v", err)
	}
	if !bytes.Equal(finalData, makeVersionPayload(totalWrites)) {
		t.Fatalf("final version not %d", totalWrites)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
