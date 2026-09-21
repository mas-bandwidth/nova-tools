package swarm_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestCleanJobStorageRefusesSharedCaches: verify shared caches (tmp/cache/{go-mod,go-build,npm})
// and <root>/cache are NEVER in the removal set.
func TestCleanJobStorageRefusesSharedCaches(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	if err := os.MkdirAll(slotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create shared caches under tmp/cache and root/cache
	caches := []string{
		filepath.Join(root, "tmp", "cache", "go-mod"),
		filepath.Join(root, "tmp", "cache", "go-build"),
		filepath.Join(root, "tmp", "cache", "npm"),
		filepath.Join(root, "cache", "gomod"),
		filepath.Join(root, "cache", "gobuild"),
		filepath.Join(root, "cache", "npm"),
	}
	for _, c := range caches {
		if err := os.MkdirAll(c, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(c, "keep.txt")
		if err := os.WriteFile(sentinel, []byte("preserve cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range caches {
		// Attempting to clean a cache as jobDir must fail with ErrProtectedStoragePath
		err := swarm.CleanJobStorage(c, filepath.Join(slotDir, "tmp", "job"), slotDir, root, "")
		if err == nil {
			t.Fatalf("CleanJobStorage succeeded on protected cache %s; expected error", c)
		}
		if !errors.Is(err, swarm.ErrProtectedStoragePath) && !errors.Is(err, safepath.ErrUnsafe) {
			t.Fatalf("expected ErrProtectedStoragePath or ErrUnsafe for %s, got %v", c, err)
		}

		// Verify sentinel file still exists
		sentinel := filepath.Join(c, "keep.txt")
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("sentinel in cache %s was deleted: %v", c, err)
		}
	}
}

// TestCleanJobStorageRefusesMirrors: verify ~/nova-bench/mirror is NEVER in the removal set.
func TestCleanJobStorageRefusesMirrors(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "swarm")
	slotDir := filepath.Join(root, "001")
	mirror := filepath.Join(home, "nova-bench", "mirror")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(slotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sentinel := filepath.Join(mirror, "HEAD")
	if err := os.WriteFile(sentinel, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := swarm.CleanJobStorage(mirror, filepath.Join(slotDir, "tmp", "job"), slotDir, root, home)
	if err == nil {
		t.Fatalf("CleanJobStorage succeeded on mirror %s; expected error", mirror)
	}
	if !errors.Is(err, swarm.ErrProtectedStoragePath) && !errors.Is(err, safepath.ErrUnsafe) {
		t.Fatalf("expected ErrProtectedStoragePath or ErrUnsafe for mirror, got %v", err)
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel in mirror was deleted: %v", err)
	}
}

// TestRelocateJobResultsMovesOutputsAtomically: verify durable outputs move from jobDir to resultsDir.
func TestRelocateJobResultsMovesOutputsAtomically(t *testing.T) {
	root := t.TempDir()
	jobDir := filepath.Join(root, "jobs", "card-1")
	resultsDir := filepath.Join(root, "results", "card-1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"RESULT.md":          "PASS\nall done\n",
		"harness-output.log": "running step 1\n",
		"usage.tsv":          "job\tattempt\ncard-1\t1\n",
		"timeline.tsv":       "turn\t1\n",
		"wall.md":            "WALL report\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(jobDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := swarm.RelocateJobResults(jobDir, resultsDir); err != nil {
		t.Fatalf("RelocateJobResults failed: %v", err)
	}

	for name, wantContent := range files {
		dst := filepath.Join(resultsDir, name)
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("file %s not found in resultsDir %s: %v", name, resultsDir, err)
		}
		if string(got) != wantContent {
			t.Fatalf("file %s content mismatch: got %q, want %q", name, string(got), wantContent)
		}

		// Should be moved out of jobDir
		src := filepath.Join(jobDir, name)
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Fatalf("file %s still exists in jobDir %s", name, jobDir)
		}
	}
}

// TestCleanJobStorageRemovesJobAndTmpAtomically: verify jobDir and tmpDir are removed under slotDir.
func TestCleanJobStorageRemovesJobAndTmpAtomically(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	jobDir := filepath.Join(slotDir, "jobs", "card-1")
	tmpDir := filepath.Join(slotDir, "tmp", "card-1")
	dataDir := filepath.Join(slotDir, "data")
	cloneDir := filepath.Join(jobDir, "repo")
	scratchDir := filepath.Join(jobDir, "scratch")

	for _, d := range []string{cloneDir, scratchDir, tmpDir, dataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "code.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "temp.bin"), []byte("temp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "keep.data"), []byte("durable data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := swarm.CleanJobStorage(jobDir, tmpDir, slotDir, root, ""); err != nil {
		t.Fatalf("CleanJobStorage failed: %v", err)
	}

	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("jobDir %s still exists after clean", jobDir)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("tmpDir %s still exists after clean", tmpDir)
	}
	// Slot's dataDir must remain
	if _, err := os.Stat(filepath.Join(dataDir, "keep.data")); err != nil {
		t.Fatalf("dataDir was deleted: %v", err)
	}
}

// TestCleanupAtCompletionPreservesUnharvestedWork: a finished card whose results have NOT moved
// is NOT deleted.
func TestCleanupAtCompletionPreservesUnharvestedWork(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	jobDir := filepath.Join(slotDir, "jobs", "card-1")
	tmpDir := filepath.Join(slotDir, "tmp", "card-1")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := swarm.CleanupJobStorageAtCompletion(swarm.JobStorageCleanupOpts{
		JobDir:           jobDir,
		TmpDir:           tmpDir,
		SlotDir:          slotDir,
		Root:             root,
		Label:            "card-1",
		IsHarnessFailure: false,
		ResultsMoved:     false,
		ResultsDir:       "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Cleaned {
		t.Fatalf("CleanupJobStorageAtCompletion cleaned unharvested work")
	}

	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("jobDir was deleted for unharvested work: %v", err)
	}
}

// TestCleanupAtCompletionHarnessFailureRemovesStorage: a harness failure (idle-reaped, wall-refused)
// keeps RESULT.md and harness log in results store and removes job directory + tmp.
func TestCleanupAtCompletionHarnessFailureRemovesStorage(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	jobDir := filepath.Join(slotDir, "jobs", "card-1")
	tmpDir := filepath.Join(slotDir, "tmp", "card-1")
	resultsDir := filepath.Join(root, "results", "card-1")
	cloneDir := filepath.Join(jobDir, "repo")

	for _, d := range []string{cloneDir, tmpDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resultContent := "# BLOCKED\nidle for 300s\n"
	logContent := "2026-09-21 start\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resultContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "harness-output.log"), []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "discard.go"), []byte("package discard"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := swarm.CleanupJobStorageAtCompletion(swarm.JobStorageCleanupOpts{
		JobDir:           jobDir,
		TmpDir:           tmpDir,
		SlotDir:          slotDir,
		Root:             root,
		ResultsDir:       resultsDir,
		Label:            "card-1",
		IsHarnessFailure: true,
	})
	if err != nil {
		t.Fatalf("CleanupJobStorageAtCompletion failed: %v", err)
	}
	if !res.Cleaned {
		t.Fatalf("expected storage to be cleaned on harness failure")
	}

	// JobDir and TmpDir must be deleted
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("jobDir %s still exists", jobDir)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("tmpDir %s still exists", tmpDir)
	}

	// RESULT.md and harness log must be kept in resultsDir
	gotResult, err := os.ReadFile(filepath.Join(resultsDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("RESULT.md not preserved in %s: %v", resultsDir, err)
	}
	if string(gotResult) != resultContent {
		t.Fatalf("RESULT.md corrupted: got %q, want %q", string(gotResult), resultContent)
	}

	gotLog, err := os.ReadFile(filepath.Join(resultsDir, "harness-output.log"))
	if err != nil {
		t.Fatalf("harness-output.log not preserved in %s: %v", resultsDir, err)
	}
	if string(gotLog) != logContent {
		t.Fatalf("harness-output.log corrupted: got %q, want %q", string(gotLog), logContent)
	}
}

// TestJobStorageMutationTeeth: mutation test with teeth verifying refusal of path escapes,
// symlinks, and protected cache/mirror mutations.
func TestJobStorageMutationTeeth(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "swarm")
	slotDir := filepath.Join(root, "001")
	mirrorDir := filepath.Join(home, "nova-bench", "mirror")
	cacheMod := filepath.Join(root, "tmp", "cache", "go-mod")
	for _, d := range []string{slotDir, mirrorDir, cacheMod} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Sentinel in mirror
	mirrorSentinel := filepath.Join(mirrorDir, "packed-refs")
	if err := os.WriteFile(mirrorSentinel, []byte("# refs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sentinel in cache
	cacheSentinel := filepath.Join(cacheMod, "modules.txt")
	if err := os.WriteFile(cacheSentinel, []byte("mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Symlink targeting mirror
	symlinkToMirror := filepath.Join(slotDir, "jobs", "symlink-mirror")
	if err := os.MkdirAll(filepath.Dir(symlinkToMirror), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mirrorDir, symlinkToMirror); err != nil {
		t.Fatal(err)
	}
	if err := swarm.CleanJobStorage(symlinkToMirror, filepath.Join(slotDir, "tmp", "x"), slotDir, root, home); err == nil {
		t.Fatalf("CleanJobStorage succeeded on symlink to mirror; teeth failed")
	}

	// 2. Symlink targeting cache
	symlinkToCache := filepath.Join(slotDir, "jobs", "symlink-cache")
	if err := os.Symlink(cacheMod, symlinkToCache); err != nil {
		t.Fatal(err)
	}
	if err := swarm.CleanJobStorage(symlinkToCache, filepath.Join(slotDir, "tmp", "x"), slotDir, root, home); err == nil {
		t.Fatalf("CleanJobStorage succeeded on symlink to cache; teeth failed")
	}

	// 3. Path traversal escaping slot
	escapePath := filepath.Join(slotDir, "jobs", "..", "..", "tmp", "cache", "go-mod")
	if err := swarm.CleanJobStorage(escapePath, filepath.Join(slotDir, "tmp", "x"), slotDir, root, home); err == nil {
		t.Fatalf("CleanJobStorage succeeded on path traversal escape; teeth failed")
	}

	// 4. Passing root itself
	if err := swarm.CleanJobStorage(root, filepath.Join(slotDir, "tmp", "x"), slotDir, root, home); err == nil {
		t.Fatalf("CleanJobStorage succeeded on root; teeth failed")
	}

	// 5. Passing slotDir itself
	if err := swarm.CleanJobStorage(slotDir, filepath.Join(slotDir, "tmp", "x"), slotDir, root, home); err == nil {
		t.Fatalf("CleanJobStorage succeeded on slotDir; teeth failed")
	}

	// 6. Subdirectory of mirror
	mirrorSub := filepath.Join(mirrorDir, "objects")
	_ = os.MkdirAll(mirrorSub, 0o755)
	if err := swarm.CleanJobStorage(mirrorSub, filepath.Join(slotDir, "tmp", "x"), slotDir, root, home); err == nil {
		t.Fatalf("CleanJobStorage succeeded on mirror subdirectory; teeth failed")
	}

	// 7. Verify sentinels are completely intact
	if _, err := os.Stat(mirrorSentinel); err != nil {
		t.Fatalf("mirror sentinel was destroyed by mutation attack: %v", err)
	}
	if _, err := os.Stat(cacheSentinel); err != nil {
		t.Fatalf("cache sentinel was destroyed by mutation attack: %v", err)
	}
}
