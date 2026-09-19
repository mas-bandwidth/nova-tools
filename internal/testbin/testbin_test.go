package testbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMain lets a placed copy of this test binary answer as a trivial program:
// with TESTBIN_CHILD=1 it prints one word and exits, so Place can be proven to
// produce something that runs without a second build.
func TestMain(m *testing.M) {
	if os.Getenv("TESTBIN_CHILD") == "1" {
		os.Stdout.WriteString("placed\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestPlaceRunsThePlacedProgram: the thing Place produces is an executable, on
// the link path and on the copy path alike. This test binary is both the source
// and, re-entered through TestMain, the program.
func TestPlaceRunsThePlacedProgram(t *testing.T) {
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "placed")
	if runtime.GOOS == "windows" {
		dst += ".exe"
	}
	if err := Place(src, dst); err != nil {
		t.Fatalf("Place: %v", err)
	}
	cmd := exec.Command(dst)
	cmd.Env = append(os.Environ(), "TESTBIN_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the placed program: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "placed" {
		t.Errorf("placed program printed %q, want %q", out, "placed")
	}
}

// TestPlaceHardLinksInTheSameDirectory: on a platform with links, a source and
// a destination in the same directory are the same file after Place -- the link
// is what avoids the macOS scan, and a copy here would be a regression.
func TestPlaceHardLinksInTheSameDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows always copies")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Place(src, dst); err != nil {
		t.Fatal(err)
	}
	si, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(si, di) {
		t.Errorf("Place copied instead of linking: %s and %s are different files", src, dst)
	}
}

// TestPlaceFallsBackToCopyWhenLinkFails: a destination on another filesystem
// cannot be linked, so Place must copy the bytes -- executable -- rather than
// fail. The link function is injected so the fallback is exercised on any
// filesystem.
func TestPlaceFallsBackToCopyWhenLinkFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	raw := []byte("built\n")
	if err := os.WriteFile(src, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := link
	link = func(oldname, newname string) error { return os.ErrInvalid }
	defer func() { link = orig }()
	if err := Place(src, dst); err != nil {
		t.Fatalf("Place with a failing link: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Errorf("copied content = %q, want %q", got, raw)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the fallback copy mode %v has no execute bit", info.Mode().Perm())
	}
	if runtime.GOOS != "windows" {
		si, err := os.Stat(src)
		if err != nil {
			t.Fatal(err)
		}
		di, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(si, di) {
			t.Errorf("the fallback must copy, not link")
		}
	}
}

// TestPlaceReplacesAnExistingFile: a caller may ask twice, so a dst already
// there is removed rather than linked through.
func TestPlaceReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Place(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("dst = %q, want %q", got, "new")
	}
}
