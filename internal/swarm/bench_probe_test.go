package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBenchProbeCachePassingNotRerun verifies that a passing probe is cached and not
// re-run for the same build identity.
func TestBenchProbeCachePassingNotRerun(t *testing.T) {
	dir := t.TempDir()
	bench := "b2"
	binSHA := "abc123"

	// No cache entry yet.
	if ReadBenchProbeCache(dir, bench, binSHA) {
		t.Fatal("expected no cache entry")
	}

	// Write a passing entry.
	if err := WriteBenchProbeCache(dir, bench, binSHA, "ok"); err != nil {
		t.Fatal(err)
	}

	// Now it should be cached.
	if !ReadBenchProbeCache(dir, bench, binSHA) {
		t.Fatal("expected cache hit for passing probe")
	}

	// A different bench should not be cached.
	if ReadBenchProbeCache(dir, "other", binSHA) {
		t.Fatal("expected no cache hit for different bench")
	}

	// A different binary sha should not be cached.
	if ReadBenchProbeCache(dir, bench, "def456") {
		t.Fatal("expected no cache hit for different binary sha")
	}
}

// TestBenchProbeCacheFailingNotCached verifies that a failing probe is not cached as
// passing.
func TestBenchProbeCacheFailingNotCached(t *testing.T) {
	dir := t.TempDir()
	bench := "b2"
	binSHA := "abc123"

	// Write a failing entry.
	if err := WriteBenchProbeCache(dir, bench, binSHA, "fail"); err != nil {
		t.Fatal(err)
	}

	// It should not be read as passing.
	if ReadBenchProbeCache(dir, bench, binSHA) {
		t.Fatal("expected cache miss for failing probe")
	}
}

// TestBenchProbeResultLine verifies the BENCH PROBE output line format.
func TestBenchProbeResultLine(t *testing.T) {
	r := BenchProbeResult{Bench: "b2", GoVer: "go1.26.0", TestOK: true, FileOK: true}
	line := r.BenchProbeLine()
	if !strings.Contains(line, "BENCH PROBE bench=b2") {
		t.Errorf("missing bench name: %s", line)
	}
	if !strings.Contains(line, "go=go1.26.0") {
		t.Errorf("missing go version: %s", line)
	}
	if !strings.Contains(line, "test=ok") {
		t.Errorf("missing test=ok: %s", line)
	}
	if !strings.Contains(line, "file=ok") {
		t.Errorf("missing file=ok: %s", line)
	}

	r2 := BenchProbeResult{Bench: "b2", GoVer: "go1.26.0", TestOK: false, FileOK: true}
	line2 := r2.BenchProbeLine()
	if !strings.Contains(line2, "test=FAIL") {
		t.Errorf("expected test=FAIL: %s", line2)
	}
}

// TestBenchProbeResultProbeOK verifies the ProbeOK method.
func TestBenchProbeResultProbeOK(t *testing.T) {
	if !(BenchProbeResult{TestOK: true, FileOK: true}).ProbeOK() {
		t.Fatal("expected ok when both pass")
	}
	if (BenchProbeResult{TestOK: false, FileOK: true}).ProbeOK() {
		t.Fatal("expected not ok when test fails")
	}
	if (BenchProbeResult{TestOK: true, FileOK: false}).ProbeOK() {
		t.Fatal("expected not ok when file fails")
	}
}

// TestBinarySHA256 verifies the binary SHA computation.
func TestBinarySHA256(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "testbin")
	if err := os.WriteFile(bin, []byte("hello"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha, err := BinarySHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	if len(sha) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(sha))
	}
	// Same content should give same hash.
	sha2, err := BinarySHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	if sha != sha2 {
		t.Fatal("expected same hash for same content")
	}
}

// TestBenchProbeLocal verifies the local probe runs and produces output.
func TestBenchProbeLocal(t *testing.T) {
	dir := t.TempDir()
	scratchDir := filepath.Join(dir, "scratch")

	// Create a minimal repo structure for the probe.
	repoDir := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, "internal", "oneline"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create go.mod
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module test\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a test file
	if err := os.WriteFile(filepath.Join(repoDir, "internal", "oneline", "oneline_test.go"), []byte(`package oneline
import "testing"
func TestProbe(t *testing.T) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a .git marker
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Change to the repo directory.
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	os.Setenv("OLDPWD", dir)
	defer os.Unsetenv("OLDPWD")

	res, err := probeLocal("local", "", "https://example.com/repo.git", scratchDir, BenchProbeResult{Bench: "local"})
	if err != nil {
		t.Fatalf("probeLocal error: %v", err)
	}
	if res.GoVer == "" {
		t.Fatal("expected go version")
	}
	if !res.TestOK {
		t.Fatal("expected test to pass")
	}
	if !res.FileOK {
		t.Fatal("expected file to be written")
	}
	// Check probe.txt was written.
	if _, err := os.Stat(filepath.Join(scratchDir, "probe.txt")); err != nil {
		t.Fatalf("probe.txt not written: %v", err)
	}
}
