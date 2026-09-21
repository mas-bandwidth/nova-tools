package pulse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Essential 7 from nova-tools #2379 & #2407:
// - Move durable outputs (RESULT.md, notes.txt, usage.tsv, harness.log, git ref/bundle)
//   into results store ~/nova-bench/results/<label>/
// - Delete the finished job directory (including clone and sandbox tmp)
// - Ensure shared caches (tmp/cache/...) and mirrors (~/nova-bench/mirror) are never deleted

func TestHarvestResultsWorkingRelocatesDurableOutputs(t *testing.T) {
	working := t.TempDir()
	resultsDir := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")

	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "aaaa000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "aaaa000000000000000000000000000000000000\trefs/heads/rowan/e7"},
		{Arg: 3, Equals: "rev-parse", Stdout: "aaaa000000000000000000000000000000000000"},
		originRule("o/r"),
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: "[]"},
		{Arg: 2, Equals: "create", Stdout: "https://example.invalid/o/r/pull/42"},
	}})

	label := "card-essential-7"
	guidSlot := "guid-123-" + label
	slotDir := filepath.Join(working, "tmp", guidSlot)
	jobDir := filepath.Join(slotDir, "jobs", label)

	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. RESULT.md
	resBody := "RESULT " + label + " sha=aaa\nDONE\nBRANCH rowan/e7\nREPO o/r\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// 2. notes.txt
	notesBody := "essential notes from worker\n"
	if err := os.WriteFile(filepath.Join(jobDir, "notes.txt"), []byte(notesBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// 3. usage.tsv
	usageBody := "started\tended\trc\tusd\ttokens\n100\t200\t0\t0.005\t1200\n"
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte(usageBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// 4. harness.log
	harnessBody := "harness output log line 1\nharness output log line 2\n"
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte(harnessBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// 5. git bundle / repo clone
	clone := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "branch.bundle"), []byte("FAKE-BUNDLE-DATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 6. sandbox tmp inside slot
	sandboxTmp := filepath.Join(slotDir, "tmp")
	if err := os.MkdirAll(sandboxTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandboxTmp, "sandbox-trash.txt"), []byte("trash"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := wkRun(t, HarvestInput{
		Working:    working,
		Base:       "0123456789ab",
		Max:        20,
		ResultsDir: resultsDir,
	})
	if code != 0 {
		t.Fatalf("HarvestWorking exit = %d, want 0; out=%s", code, out)
	}

	// Verify results store holds all durable outputs
	targetStore := filepath.Join(resultsDir, label)
	if _, err := os.Stat(targetStore); err != nil {
		t.Fatalf("results store %s was not created: %v", targetStore, err)
	}

	// Check RESULT.md
	gotRes, err := os.ReadFile(filepath.Join(targetStore, "RESULT.md"))
	if err != nil {
		t.Fatalf("missing RESULT.md in results store: %v", err)
	}
	if string(gotRes) != resBody {
		t.Errorf("RESULT.md = %q, want %q", string(gotRes), resBody)
	}

	// Check notes.txt
	gotNotes, err := os.ReadFile(filepath.Join(targetStore, "notes.txt"))
	if err != nil {
		t.Fatalf("missing notes.txt in results store: %v", err)
	}
	if string(gotNotes) != notesBody {
		t.Errorf("notes.txt = %q, want %q", string(gotNotes), notesBody)
	}

	// Check usage.tsv
	gotUsage, err := os.ReadFile(filepath.Join(targetStore, "usage.tsv"))
	if err != nil {
		t.Fatalf("missing usage.tsv in results store: %v", err)
	}
	if string(gotUsage) != usageBody {
		t.Errorf("usage.tsv = %q, want %q", string(gotUsage), usageBody)
	}

	// Check harness.log
	gotLog, err := os.ReadFile(filepath.Join(targetStore, "harness.log"))
	if err != nil {
		t.Fatalf("missing harness.log in results store: %v", err)
	}
	if string(gotLog) != harnessBody {
		t.Errorf("harness.log = %q, want %q", string(gotLog), harnessBody)
	}

	// Check branch.bundle
	if _, err := os.Stat(filepath.Join(targetStore, "branch.bundle")); err != nil {
		t.Fatalf("missing branch.bundle in results store: %v", err)
	}

	// Check .harvested
	if _, err := os.Stat(filepath.Join(targetStore, ".harvested")); err != nil {
		t.Fatalf("missing .harvested marker in results store: %v", err)
	}

	// Check finished job directory (including clone and sandbox tmp) is DELETED
	if _, err := os.Stat(slotDir); !os.IsNotExist(err) {
		t.Fatalf("slot directory %s should be completely deleted: %v", slotDir, err)
	}
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("job directory %s should be deleted: %v", jobDir, err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("clone directory %s should be deleted: %v", clone, err)
	}
	if _, err := os.Stat(sandboxTmp); !os.IsNotExist(err) {
		t.Fatalf("sandbox tmp %s should be deleted: %v", sandboxTmp, err)
	}
}

func TestHarvestResultsWorkingNeverDeletesSharedCaches(t *testing.T) {
	working := t.TempDir()
	resultsDir := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")

	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "bbbb000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "bbbb000000000000000000000000000000000000\trefs/heads/rowan/cache-safe"},
		{Arg: 3, Equals: "rev-parse", Stdout: "bbbb000000000000000000000000000000000000"},
		originRule("o/r"),
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: "[]"},
		{Arg: 2, Equals: "create", Stdout: "https://example.invalid/o/r/pull/43"},
	}})

	// Create shared cache directories under tmp/cache/{go-mod,go-build,npm}
	cacheGoMod := filepath.Join(working, "tmp", "cache", "go-mod")
	cacheGoBuild := filepath.Join(working, "tmp", "cache", "go-build")
	cacheNPM := filepath.Join(working, "tmp", "cache", "npm")
	for _, c := range []string{cacheGoMod, cacheGoBuild, cacheNPM} {
		if err := os.MkdirAll(c, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(c, "cached_file.pkg"), []byte("precious-cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	label := "card-cache-safe"
	jobDir := wkJob(t, working, "guid-999-"+label, label, wkResult(label, "rowan/cache-safe", "o/r"))

	out, _, code := wkRun(t, HarvestInput{
		Working:    working,
		Base:       "0123456789ab",
		Max:        20,
		ResultsDir: resultsDir,
	})
	if code != 0 {
		t.Fatalf("HarvestWorking exit = %d, want 0; out=%s", code, out)
	}

	// Verify shared caches STILL EXIST and are unharmed
	for _, c := range []string{cacheGoMod, cacheGoBuild, cacheNPM} {
		f := filepath.Join(c, "cached_file.pkg")
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("CRITICAL DEFECT: shared cache file %s was destroyed! err=%v", f, err)
		}
		if string(data) != "precious-cache" {
			t.Fatalf("CRITICAL DEFECT: shared cache file %s was corrupted: %q", f, string(data))
		}
	}

	// But the job dir was removed
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Fatalf("finished job directory %s was not cleaned up: %v", jobDir, err)
	}
}

func TestHarvestResultsBenchScriptRelocatesDurableOutputsAndPreservesCaches(t *testing.T) {
	benchHome := t.TempDir()
	resultsRoot := filepath.Join(benchHome, "nova-bench", "results")

	label := "card-bench-e7"
	slotDir := filepath.Join(benchHome, "swarm-root", "001")
	jobDir := filepath.Join(slotDir, "jobs", label)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. RESULT.md
	resBody := "RESULT " + label + " sha=bbb\nDONE\nBRANCH rowan/be7\nREPO o/r\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// 2. notes.txt
	if err := os.WriteFile(filepath.Join(jobDir, "notes.txt"), []byte("notes from bench"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 3. usage.tsv
	if err := os.WriteFile(filepath.Join(jobDir, "usage.tsv"), []byte("1\t2\t0\t0.001\t100"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 4. harness.log
	if err := os.WriteFile(filepath.Join(jobDir, "harness.log"), []byte("bench harness log"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 5. sandbox tmp
	sandboxTmp := filepath.Join(slotDir, "tmp")
	if err := os.MkdirAll(sandboxTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandboxTmp, "trash.dat"), []byte("trash"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 6. shared cache
	cacheDir := filepath.Join(benchHome, "tmp", "cache", "go-mod")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "mod.pkg"), []byte("cached-mod"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 7. mirror
	mirrorDir := filepath.Join(benchHome, "nova-bench", "mirror", "nova-tools.git")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirrorDir, "HEAD"), []byte("ref: refs/heads/main"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Generate and execute the script on bench
	script := benchRelocateScript(jobDir, label, "rowan/be7", resultsRoot)
	cmd := exec.Command("/bin/sh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bench relocate script failed: %v\n%s", err, string(out))
	}

	targetResults := filepath.Join(resultsRoot, label)

	// Verify all durable outputs moved
	for _, file := range []string{"RESULT.md", "notes.txt", "usage.tsv", "harness.log", ".harvested"} {
		p := filepath.Join(targetResults, file)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s in bench results store: %v", file, err)
		}
	}

	// Verify slotDir is deleted
	if _, err := os.Stat(slotDir); !os.IsNotExist(err) {
		t.Fatalf("slot directory %s should be deleted: %v", slotDir, err)
	}

	// Verify shared cache and mirror are NEVER deleted
	cacheFile := filepath.Join(cacheDir, "mod.pkg")
	if data, err := os.ReadFile(cacheFile); err != nil || string(data) != "cached-mod" {
		t.Fatalf("CRITICAL: shared cache was deleted by bench script! err=%v", err)
	}
	mirrorFile := filepath.Join(mirrorDir, "HEAD")
	if data, err := os.ReadFile(mirrorFile); err != nil || string(data) != "ref: refs/heads/main" {
		t.Fatalf("CRITICAL: mirror was deleted by bench script! err=%v", err)
	}
}

// TestHarvestResultsMutationProtectionWithTeeth verifies that safety guards cannot be bypassed
func TestHarvestResultsMutationProtectionWithTeeth(t *testing.T) {
	working := "/home/user/rowan-working"
	roots := "/home/user/swarm-root,/data/root2"

	// 1. Cache protection must refuse
	for _, cachePath := range []string{
		"/tmp/cache",
		"/tmp/cache/go-mod",
		"/home/user/tmp/cache",
		"/home/user/tmp/cache/npm",
		"/var/cache",
		"cache",
	} {
		protected, reason := isProtectedFromDeletion(cachePath, working, roots)
		if !protected {
			t.Fatalf("MUTATION DETECTED: cache path %s was NOT protected!", cachePath)
		}
		if !strings.Contains(reason, "cache") {
			t.Errorf("reason for %s was %q, want mention of cache", cachePath, reason)
		}
	}

	// 2. Mirror protection must refuse
	for _, mirrorPath := range []string{
		"/home/user/nova-bench/mirror",
		"/home/user/nova-bench/mirror/repo.git",
		"/var/mirror",
		"/home/mirror",
		"/some/repo.git",
	} {
		protected, reason := isProtectedFromDeletion(mirrorPath, working, roots)
		if !protected {
			t.Fatalf("MUTATION DETECTED: mirror path %s was NOT protected!", mirrorPath)
		}
		if !strings.Contains(reason, "mirror") {
			t.Errorf("reason for %s was %q, want mention of mirror", mirrorPath, reason)
		}
	}

	// 3. Roots and system dirs must refuse
	for _, rootPath := range []string{
		"/",
		"/tmp",
		"/home",
		working,
		filepath.Join(working, "tmp"),
		"/home/user/swarm-root",
		"/data/root2",
	} {
		protected, _ := isProtectedFromDeletion(rootPath, working, roots)
		if !protected {
			t.Fatalf("MUTATION DETECTED: root path %s was NOT protected!", rootPath)
		}
	}

	// 4. Finished job dir logic
	// A slot with single job resolves to slot
	tmpDir := t.TempDir()
	slot1 := filepath.Join(tmpDir, "slot1")
	job1 := filepath.Join(slot1, "jobs", "card-1")
	if err := os.MkdirAll(job1, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := finishedJobDir(job1); got != slot1 {
		t.Fatalf("finishedJobDir for single job slot = %s, want %s", got, slot1)
	}

	// A slot with multiple jobs resolves to the job directory, protecting sibling
	job2 := filepath.Join(slot1, "jobs", "card-2")
	if err := os.MkdirAll(job2, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := finishedJobDir(job1); got != job1 {
		t.Fatalf("finishedJobDir for multi-job slot = %s, want %s (must protect sibling)", got, job1)
	}
}

// TestHarvestResultsWorkingArchivesPriorAttemptOnCollision verifies that if RESULT.md already exists
// in ~/nova-bench/results/<label>/, the previous attempt is archived under attempts/<timestamp>/
// and the new attempt overwrites top-level durable outputs without losing historical evidence.
func TestHarvestResultsWorkingArchivesPriorAttemptOnCollision(t *testing.T) {
	working := t.TempDir()
	resultsDir := t.TempDir()

	label := "card-collision-test"
	slotDir := filepath.Join(working, "tmp", "guid-col-"+label)
	jobDir := filepath.Join(slotDir, "jobs", label)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	targetStore := filepath.Join(resultsDir, label)
	if err := os.MkdirAll(targetStore, 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed existing attempt in results store
	oldResult := "RESULT " + label + " sha=old111\nDONE\nBRANCH old/branch\n"
	oldNotes := "notes from run 1\n"
	if err := os.WriteFile(filepath.Join(targetStore, "RESULT.md"), []byte(oldResult), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetStore, "notes.txt"), []byte(oldNotes), 0o644); err != nil {
		t.Fatal(err)
	}

	// Prepare new attempt in job directory
	newResult := "RESULT " + label + " sha=new222\nDONE\nBRANCH new/branch\n"
	newNotes := "notes from run 2\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(newResult), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "notes.txt"), []byte(newNotes), 0o644); err != nil {
		t.Fatal(err)
	}

	fixedTime := time.Date(2026, 9, 21, 14, 30, 0, 0, time.UTC)
	in := HarvestInput{
		Working:    working,
		ResultsDir: resultsDir,
		Now:        func() time.Time { return fixedTime },
	}
	job := harvestJob{
		dir:   jobDir,
		label: label,
	}

	if err := relocateHarvestedWorking(job, in); err != nil {
		t.Fatalf("relocateHarvestedWorking failed: %v", err)
	}

	// 1. Verify previous attempt is archived under attempts/20260921T143000Z/
	archiveDir := filepath.Join(targetStore, "attempts", "20260921T143000Z")
	archivedRes, err := os.ReadFile(filepath.Join(archiveDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("missing archived RESULT.md in %s: %v", archiveDir, err)
	}
	if string(archivedRes) != oldResult {
		t.Errorf("archived RESULT.md = %q, want %q", string(archivedRes), oldResult)
	}
	archivedNotes, err := os.ReadFile(filepath.Join(archiveDir, "notes.txt"))
	if err != nil {
		t.Fatalf("missing archived notes.txt in %s: %v", archiveDir, err)
	}
	if string(archivedNotes) != oldNotes {
		t.Errorf("archived notes.txt = %q, want %q", string(archivedNotes), oldNotes)
	}

	// 2. Verify top-level RESULT.md has the new content
	currRes, err := os.ReadFile(filepath.Join(targetStore, "RESULT.md"))
	if err != nil {
		t.Fatalf("missing top-level RESULT.md in %s: %v", targetStore, err)
	}
	if string(currRes) != newResult {
		t.Errorf("top-level RESULT.md = %q, want %q", string(currRes), newResult)
	}

	// 3. Verify manifest.tsv exists and references new attempt
	manifest, err := os.ReadFile(filepath.Join(targetStore, "manifest.tsv"))
	if err != nil {
		t.Fatalf("missing manifest.tsv: %v", err)
	}
	if !strings.Contains(string(manifest), label) {
		t.Errorf("manifest.tsv %q missing label %q", string(manifest), label)
	}

	// 4. Verify job directory was removed
	if _, err := os.Stat(slotDir); !os.IsNotExist(err) {
		t.Fatalf("slot directory %s should be deleted after successful relocation: %v", slotDir, err)
	}
}

// TestHarvestResultsWorkingRefusesDeletionOnZeroByteResult verifies that an empty (0-byte) RESULT.md
// triggers atomic readback check failure and refuses to delete the job directory.
func TestHarvestResultsWorkingRefusesDeletionOnZeroByteResult(t *testing.T) {
	working := t.TempDir()
	resultsDir := t.TempDir()

	label := "card-empty-result"
	slotDir := filepath.Join(working, "tmp", "guid-empty-"+label)
	jobDir := filepath.Join(slotDir, "jobs", label)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write 0-byte RESULT.md in job directory
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	in := HarvestInput{
		Working:    working,
		ResultsDir: resultsDir,
		Now:        func() time.Time { return time.Unix(1000, 0).UTC() },
	}
	job := harvestJob{
		dir:   jobDir,
		label: label,
	}

	err := relocateHarvestedWorking(job, in)
	if err == nil {
		t.Fatal("expected error for 0-byte RESULT.md, got nil")
	}
	if !strings.Contains(err.Error(), "empty") && !strings.Contains(err.Error(), "0 bytes") {
		t.Errorf("expected error mentioning empty/0 bytes, got: %v", err)
	}

	// Finished job directory MUST NOT be deleted
	if _, err := os.Stat(jobDir); os.IsNotExist(err) {
		t.Fatalf("CRITICAL DEFECT: job directory %s was deleted despite 0-byte RESULT.md!", jobDir)
	}
	if _, err := os.Stat(slotDir); os.IsNotExist(err) {
		t.Fatalf("CRITICAL DEFECT: slot directory %s was deleted despite 0-byte RESULT.md!", slotDir)
	}
}

// TestHarvestResultsBenchScriptRefusesDeletionOnEmptyResult verifies that the bench shell script
// checks [ -s "$R/RESULT.md" ] and refuses to delete the job directory when RESULT.md is empty.
func TestHarvestResultsBenchScriptRefusesDeletionOnEmptyResult(t *testing.T) {
	benchHome := t.TempDir()
	resultsRoot := filepath.Join(benchHome, "nova-bench", "results")

	label := "card-bench-empty"
	slotDir := filepath.Join(benchHome, "swarm-root", "002")
	jobDir := filepath.Join(slotDir, "jobs", label)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write 0-byte RESULT.md in bench job directory
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	script := benchRelocateScript(jobDir, label, "rowan/empty", resultsRoot)
	cmd := exec.Command("/bin/sh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bench script error: %v\n%s", err, string(out))
	}

	// Slot directory MUST NOT be deleted
	if _, err := os.Stat(slotDir); os.IsNotExist(err) {
		t.Fatalf("CRITICAL DEFECT: bench slot %s was deleted despite empty RESULT.md!", slotDir)
	}
	if _, err := os.Stat(jobDir); os.IsNotExist(err) {
		t.Fatalf("CRITICAL DEFECT: bench job dir %s was deleted despite empty RESULT.md!", jobDir)
	}
}
