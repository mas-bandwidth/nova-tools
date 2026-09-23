package swarm_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestRelocateJobResultsRefusesNestedOrOverlappingDest verifies that RelocateJobResults
// and CleanupJobStorageAtCompletion reject destination directories that overlap with jobDir
// (nested inside, identical to, or parent of jobDir) without moving or destroying files.
func TestRelocateJobResultsRefusesNestedOrOverlappingDest(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	jobDir := filepath.Join(slotDir, "jobs", "card-1")
	tmpDir := filepath.Join(slotDir, "tmp", "card-1")
	cloneDir := filepath.Join(jobDir, "repo")

	if err := os.MkdirAll(cloneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(jobDir, "RESULT.md")
	if err := os.WriteFile(testFile, []byte("PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(jobDir, "harness-output.log")
	if err := os.WriteFile(logFile, []byte("log content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. resultsDir nested inside jobDir
	nestedResults := filepath.Join(jobDir, "nested-results")
	if err := swarm.RelocateJobResults(jobDir, nestedResults); err == nil {
		t.Fatalf("RelocateJobResults succeeded with nested resultsDir %s", nestedResults)
	}

	// CleanupJobStorageAtCompletion must refuse and preserve jobDir
	res, err := swarm.CleanupJobStorageAtCompletion(swarm.JobStorageCleanupOpts{
		JobDir:     jobDir,
		TmpDir:     tmpDir,
		SlotDir:    slotDir,
		Root:       root,
		ResultsDir: nestedResults,
		Label:      "card-1",
	})
	if err == nil {
		t.Fatalf("CleanupJobStorageAtCompletion succeeded with nested resultsDir; want error")
	}
	if res.Cleaned {
		t.Fatalf("CleanupJobStorageAtCompletion cleaned storage despite nested resultsDir error")
	}
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("RESULT.md was lost after nested resultsDir error: %v", err)
	}
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("jobDir was deleted after nested resultsDir error: %v", err)
	}

	// 2. resultsDir identical to jobDir
	if err := swarm.RelocateJobResults(jobDir, jobDir); err == nil {
		t.Fatalf("RelocateJobResults succeeded when resultsDir == jobDir")
	}

	// 3. jobDir nested inside resultsDir
	parentResults := filepath.Dir(jobDir)
	if err := swarm.RelocateJobResults(jobDir, parentResults); err == nil {
		t.Fatalf("RelocateJobResults succeeded when jobDir nested inside resultsDir")
	}
}

// TestCleanupAtCompletionPreservesSourceWhenMoveFails verifies that if moving a durable
// candidate fails during cleanup, the operation aborts, error is returned, and the
// source files and job directory are preserved without deletion.
func TestCleanupAtCompletionPreservesSourceWhenMoveFails(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	jobDir := filepath.Join(slotDir, "jobs", "card-1")
	tmpDir := filepath.Join(slotDir, "tmp", "card-1")
	resultsDir := filepath.Join(root, "results", "card-1")
	cloneDir := filepath.Join(jobDir, "repo")

	for _, d := range []string{cloneDir, tmpDir, resultsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resultContent := "PASS\nresult data\n"
	usageContent := "job\tattempt\ncard-1\t1\n"
	logContent := "harness output log\n"
	reportContent := "# REPORT\nall green\n"
	codeContent := "package main\n"

	files := map[string]string{
		"RESULT.md":          resultContent,
		"usage.tsv":          usageContent,
		"harness-output.log": logContent,
		"REPORT.md":          reportContent,
		"repo/main.go":       codeContent,
	}
	for relPath, content := range files {
		p := filepath.Join(jobDir, relPath)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate move failure by making resultsDir unwriteable
	if err := os.Chmod(resultsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(resultsDir, 0o755)
	})

	res, err := swarm.CleanupJobStorageAtCompletion(swarm.JobStorageCleanupOpts{
		JobDir:     jobDir,
		TmpDir:     tmpDir,
		SlotDir:    slotDir,
		Root:       root,
		ResultsDir: resultsDir,
		Label:      "card-1",
	})
	if err == nil {
		t.Fatalf("expected CleanupJobStorageAtCompletion to fail when resultsDir is unwriteable")
	}
	if res.Cleaned {
		t.Fatalf("CleanupJobStorageAtCompletion reported Cleaned=true on move failure")
	}

	// Verify jobDir and tmpDir were NOT removed
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatalf("jobDir was deleted despite failed move: %v", err)
	}
	if _, err := os.Stat(tmpDir); err != nil {
		t.Fatalf("tmpDir was deleted despite failed move: %v", err)
	}

	// Verify all source files in jobDir are completely preserved
	for relPath, wantContent := range files {
		p := filepath.Join(jobDir, relPath)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("source file %s was destroyed or unreadable in jobDir: %v", relPath, err)
		}
		if string(got) != wantContent {
			t.Fatalf("source file %s content changed: got %q, want %q", relPath, string(got), wantContent)
		}
	}
}

// TestCleanJobStorageRefusesOutsideSlotPathBeforeAnyRename proves CleanJobStorage validates
// containment under slotDir BEFORE it mutates anything. Stella's HOLD 6 at 57a9211a: the old
// code renamed jobDir to a ".deleting-*" sibling first and only asked safepath.RemoveUnder
// whether that staged path was under slotDir afterward, so an out-of-slot directory could be
// renamed (mutated) in its real parent before the refusal was ever raised. jobDir here is a
// normal directory, outside slotDir, that AssertNotProtectedStoragePath does not catch (it is
// not a cache or a mirror) -- only the new pre-rename containment check can refuse it.
func TestCleanJobStorageRefusesOutsideSlotPathBeforeAnyRename(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	outsideDir := filepath.Join(root, "002", "jobs", "card-1")
	if err := os.MkdirAll(slotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outsideDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := swarm.CleanJobStorage(outsideDir, filepath.Join(slotDir, "tmp", "x"), slotDir, root, "")
	if err == nil {
		t.Fatalf("CleanJobStorage succeeded on a jobDir outside slotDir; expected refusal")
	}
	var refused *safepath.Refused
	if !errors.As(err, &refused) {
		t.Fatalf("expected a *safepath.Refused containment refusal, got %v", err)
	}

	// The directory must still exist under its ORIGINAL name: a validate-before-mutate
	// refusal never renames anything, so there must be no ".deleting-*" sibling either.
	if _, err := os.Stat(outsideDir); err != nil {
		t.Fatalf("outsideDir %s was renamed or removed despite refusal: %v", outsideDir, err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel in outsideDir was destroyed by refused clean: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(outsideDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".deleting-") {
			t.Fatalf("found staged %q next to outsideDir; a rename happened before the containment refusal", e.Name())
		}
	}
}

// TestCleanJobStorageRefusesSymlinkedParentBeforeAnyRename proves the same validate-before-
// mutate ordering when it is not jobDir itself that is a symlink (the existing Lstat check
// already covers that) but an ANCESTOR of jobDir: slotDir/jobs/linked is a symlink to a
// directory outside slotDir, so jobDir = slotDir/jobs/linked/card-2 is syntactically under
// slotDir but resolves, once the symlinked ancestor is followed, to somewhere else entirely.
// Stella's HOLD 6 named exactly this: "a path reached through a symlinked parent" must be
// refused before any rename, not discovered afterward.
func TestCleanJobStorageRefusesSymlinkedParentBeforeAnyRename(t *testing.T) {
	root := t.TempDir()
	slotDir := filepath.Join(root, "001")
	elsewhere := filepath.Join(root, "elsewhere")
	realOutsideDir := filepath.Join(elsewhere, "card-2")
	if err := os.MkdirAll(filepath.Join(slotDir, "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(realOutsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(realOutsideDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkedAncestor := filepath.Join(slotDir, "jobs", "linked")
	if err := os.Symlink(elsewhere, linkedAncestor); err != nil {
		t.Fatal(err)
	}

	// jobDir is syntactically under slotDir/jobs/linked/card-2, and card-2 itself is an
	// ordinary directory (not a symlink) -- only its ancestor "linked" is.
	jobDir := filepath.Join(linkedAncestor, "card-2")

	err := swarm.CleanJobStorage(jobDir, filepath.Join(slotDir, "tmp", "x"), slotDir, root, "")
	if err == nil {
		t.Fatalf("CleanJobStorage succeeded on a jobDir reached through a symlinked ancestor; expected refusal")
	}
	var refused *safepath.Refused
	if !errors.As(err, &refused) {
		t.Fatalf("expected a *safepath.Refused containment refusal, got %v", err)
	}

	if _, err := os.Stat(realOutsideDir); err != nil {
		t.Fatalf("realOutsideDir %s was renamed or removed despite refusal: %v", realOutsideDir, err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel behind symlinked ancestor was destroyed by refused clean: %v", err)
	}
	entries, err := os.ReadDir(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".deleting-") {
			t.Fatalf("found staged %q under the symlink target; a rename happened before the containment refusal", e.Name())
		}
	}
}
