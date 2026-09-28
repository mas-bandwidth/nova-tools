package tokens

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFoldLockSymlinkPreservesTarget(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		name := "existing-target"
		if missing {
			name = "missing-target"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			out := filepath.Join(root, "out")
			target := filepath.Join(root, "unrelated")
			if err := os.Mkdir(out, 0700); err != nil {
				t.Fatal(err)
			}
			const body = "unrelated data must survive\n"
			if !missing {
				if err := os.WriteFile(target, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(out, LockName)
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			release, err := TakeFoldLock(out, 0)
			if release != nil {
				release()
			}
			t.Logf("TakeFoldLock err=%v", err)
			if err == nil {
				t.Error("expected symlink refusal before touching target")
			}
			raw, readErr := os.ReadFile(target)
			if missing {
				if !os.IsNotExist(readErr) {
					t.Errorf("created symlink target: %q (%v)", raw, readErr)
				}
			} else if readErr != nil || string(raw) != body {
				t.Errorf("target changed: %q (%v)", raw, readErr)
			}
			info, lstatErr := os.Lstat(link)
			if lstatErr != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Errorf("link changed: %v (%v)", info, lstatErr)
			}
		})
	}
}

func TestFoldLockRefusesDirectory(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	path := filepath.Join(out, LockName)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	release, err := TakeFoldLock(out, 0)
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("directory lock was accepted")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("directory changed: %v (%v)", info, statErr)
	}
}
