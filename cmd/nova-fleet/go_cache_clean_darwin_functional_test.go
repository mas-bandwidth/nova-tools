//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// roles/health/files/nova-go-cache-clean, the Mac's trim of the Go build cache, run on a disposable cache.
func TestFunctionalGoCacheCleanDarwin(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("nova-go-cache-clean uses BSD stat flags: macOS only")
	}
	cache := filepath.Join(t.TempDir(), "go-build")
	old := time.Now().Add(-3 * time.Hour)
	for p, fresh := range map[string]bool{"ab/old-d": false, "ab/new-d": true, "README": false, "trim.txt": false} {
		f := filepath.Join(cache, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !fresh {
			if err := os.Chtimes(f, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	run := func(env ...string) string {
		cmd := exec.Command("bash", "roles/health/files/nova-go-cache-clean")
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("nova-go-cache-clean: %v: %s", err, out)
		}
		return string(out)
	}
	env := []string{"DISK_CLEAN_CACHE=" + cache, "DISK_WARN_PCT=100", "DISK_CLEAN_AGE_MIN=60"}
	if out := run(append(env, "DISK_CLEAN_DRY=1")...); !strings.Contains(out, "WOULD "+cache+" 1 files ") {
		t.Errorf("dry run does not name the one old cache file: %s", out)
	}
	if _, err := os.Stat(filepath.Join(cache, "ab/old-d")); err != nil {
		t.Errorf("dry run removed a file: %v", err)
	}
	out := run(env...)
	for p, want := range map[string]bool{"ab/old-d": false, "ab/new-d": true, "README": true, "trim.txt": true} {
		if _, err := os.Stat(filepath.Join(cache, p)); (err == nil) != want {
			t.Errorf("%s present=%v, want %v: %s", p, err == nil, want, out)
		}
	}
	if !strings.Contains(out, "CLEAN-DONE ") {
		t.Errorf("no CLEAN-DONE line: %s", out)
	}
	if out := run("DISK_CLEAN_CACHE="+filepath.Dir(cache), "DISK_CLEAN_AGE_MIN=0"); !strings.Contains(out, "CLEAN-REFUSED ") {
		t.Errorf("a cache not named go-build was not refused: %s", out)
	}
	if out := run("DISK_CLEAN_CACHE="+cache, "DISK_CLEAN_AGE_MIN=2880", "DISK_CLEAN_PRESSED_MIN=0", "DISK_WARN_PCT=-1"); !strings.Contains(out, " age=0m") {
		t.Errorf("over the warn percent the pressed age is not used: %s", out)
	}
}
