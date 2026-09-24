package land_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Helper to set up a git repository with a bare mirror and working directory.
func setupGitMirror(t *testing.T) (bareDir, workDir string, runGit func(...string) string) {
	t.Helper()
	tmp := t.TempDir()
	bareDir = filepath.Join(tmp, "mirror.git")
	if err := exec.Command("git", "init", "--bare", bareDir).Run(); err != nil {
		t.Fatalf("git init bare: %v", err)
	}

	workDir = filepath.Join(tmp, "work")
	if err := exec.Command("git", "clone", bareDir, workDir).Run(); err != nil {
		t.Fatalf("git clone: %v", err)
	}

	runGit = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = workDir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, workDir, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	return bareDir, workDir, runGit
}

// TestDefect1_WorkerTestsTrainHead verifies that the worker checks out the train head
// so the gate tests the train, turning RED when a member commit introduces a test failure (spec 5.3, 5).
func TestDefect1_WorkerTestsTrainHead(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	bareDir, workDir, runGit := setupGitMirror(t)

	// Base commit with a passing Go test
	_ = os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module testpkg\n\ngo 1.24\n"), 0644)
	_ = os.MkdirAll(filepath.Join(workDir, "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(workDir, "pkg", "pkg.go"), []byte("package pkg\n\nfunc Val() int { return 1 }\n"), 0644)
	_ = os.WriteFile(filepath.Join(workDir, "pkg", "pkg_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestVal(t *testing.T) {\n\tif Val() != 1 { t.Fatal(\"bad\") }\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base commit")
	runGit("push", "origin", "HEAD:main")
	fromTip := runGit("rev-parse", "HEAD")

	// Member commit breaks the test
	runGit("checkout", "-b", "m1", fromTip)
	_ = os.WriteFile(filepath.Join(workDir, "pkg", "pkg_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestVal(t *testing.T) {\n\tt.Fatal(\"broken on train head\")\n}\n"), 0644)
	runGit("commit", "-am", "break test in member")
	runGit("push", "origin", "HEAD:m1")
	m1Head := runGit("rev-parse", "HEAD")

	unit := "gh/mas-bandwidth/nova-tools/301"
	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "m1", Head: m1Head, BaseSHA: fromTip,
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0)

	batchID := "batch-d1"
	_, _, err = land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, unit+"@"+m1Head, "pkg/pkg_test.go", "go", fromTip, "in-d1")
	if err != nil {
		t.Fatalf("batch plan: %v", err)
	}

	st, err := store.Open(f.ctx, f.client.Options().Addr)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()
	_ = capacity.SetBudget(f.ctx, st, "bench-d1", 4000, 8192, "operator", "")
	_ = f.client.HSet(f.ctx, "bench:bench-d1:desired", "machine", "bench-d1").Err()
	_ = f.client.HSet(f.ctx, "bench:bench-d1:land", "cores_go", "2000", "mem_go", "4096").Err()

	w := land.NewWorker(land.WorkerConfig{
		Client:    f.client,
		Store:     st,
		Bench:     "bench-d1",
		Repos:     []string{f.repo},
		Slots:     1,
		MirrorDir: bareDir,
	})

	ran, err := w.RunOnce(f.ctx, "slot-1")
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !ran {
		t.Fatalf("RunOnce did not process gate")
	}

	rkey := land.ReceiptKey(f.repo, batchID, 1)
	verdict, err := f.client.HGet(f.ctx, rkey, "verdict").Result()
	if err != nil {
		t.Fatalf("HGet verdict: %v", err)
	}
	if verdict != "RED" {
		t.Fatalf("expected verdict RED on broken train head, got %s (the worker must check out the train head)", verdict)
	}
}

// TestDefect2_WorkerChangedFilesAndSelectionCache verifies that changedFiles is populated
// from member changes and selection graph is cached in Redis (spec 5.4, B4, B6).
func TestDefect2_WorkerChangedFilesAndSelectionCache(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	bareDir, workDir, runGit := setupGitMirror(t)

	_ = os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module testpkg\n\ngo 1.24\n"), 0644)
	_ = os.MkdirAll(filepath.Join(workDir, "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(workDir, "pkg", "pkg.go"), []byte("package pkg\n\nfunc Val() int { return 1 }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base commit")
	runGit("push", "origin", "HEAD:main")
	fromTip := runGit("rev-parse", "HEAD")

	runGit("checkout", "-b", "m2", fromTip)
	_ = os.WriteFile(filepath.Join(workDir, "pkg", "pkg.go"), []byte("package pkg\n\nfunc Val() int { return 2 }\n"), 0644)
	runGit("commit", "-am", "change in pkg")
	runGit("push", "origin", "HEAD:m2")
	m2Head := runGit("rev-parse", "HEAD")

	unit := "gh/mas-bandwidth/nova-tools/302"
	_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "m2", Head: m2Head, BaseSHA: fromTip,
	})
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0)

	batchID := "batch-d2"
	_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, unit+"@"+m2Head, "pkg/pkg.go", "go", fromTip, "in-d2")
	if err != nil {
		t.Fatalf("batch plan: %v", err)
	}

	st, err := store.Open(f.ctx, f.client.Options().Addr)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()
	_ = capacity.SetBudget(f.ctx, st, "bench-d2", 4000, 8192, "operator", "")
	_ = f.client.HSet(f.ctx, "bench:bench-d2:desired", "machine", "bench-d2").Err()
	_ = f.client.HSet(f.ctx, "bench:bench-d2:land", "cores_go", "2000", "mem_go", "4096").Err()

	w := land.NewWorker(land.WorkerConfig{
		Client:    f.client,
		Store:     st,
		Bench:     "bench-d2",
		Repos:     []string{f.repo},
		Slots:     1,
		MirrorDir: bareDir,
	})

	ran, err := w.RunOnce(f.ctx, "slot-1")
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !ran {
		t.Fatalf("RunOnce did not process gate")
	}

	rkey := land.ReceiptKey(f.repo, batchID, 1)
	selection, err := f.client.HGet(f.ctx, rkey, "selection").Result()
	if err != nil {
		t.Fatalf("HGet selection: %v", err)
	}
	if selection == "" || selection == "checks=selected packages=0" {
		t.Fatalf("expected non-empty selection with selected packages > 0, got %q (changedFiles must be filled)", selection)
	}

	// Verify bounded selection cache was populated
	selKeys, err := f.client.Keys(f.ctx, "land:"+f.repo+":sel:*").Result()
	if err != nil {
		t.Fatalf("Keys sel: %v", err)
	}
	if len(selKeys) == 0 {
		t.Fatalf("expected selection graph to be cached under land:%s:sel:*, found 0 keys", f.repo)
	}
}

// TestDefect3_WorkerReceiptGIDAndCoreS verifies that the worker writes gid receipts
// and computes core_s from execution instead of hard-coding "10" (spec 3.7, 5.5, B6).
func TestDefect3_WorkerReceiptGIDAndCoreS(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	policyID := "pol-test-d3"
	requiredSetID := "req-test-d3"
	runnerID := "runner-test-d3"
	if err := land.CallPolicySet(f.ctx, f.client, f.repo, f.base, policyID, requiredSetID, runnerID); err != nil {
		t.Fatalf("policy set: %v", err)
	}

	bareDir, workDir, runGit := setupGitMirror(t)

	_ = os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module testpkg\n\ngo 1.24\n"), 0644)
	_ = os.MkdirAll(filepath.Join(workDir, "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(workDir, "pkg", "pkg.go"), []byte("package pkg\n\nfunc Val() int { return 1 }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base commit")
	runGit("push", "origin", "HEAD:main")
	fromTip := runGit("rev-parse", "HEAD")

	runGit("checkout", "-b", "m3", fromTip)
	_ = os.WriteFile(filepath.Join(workDir, "pkg", "pkg.go"), []byte("package pkg\n\nfunc Val() int { return 3 }\n"), 0644)
	runGit("commit", "-am", "change")
	runGit("push", "origin", "HEAD:m3")
	m3Head := runGit("rev-parse", "HEAD")

	unit := "gh/mas-bandwidth/nova-tools/303"
	_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "m3", Head: m3Head, BaseSHA: fromTip,
	})
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0)

	batchID := "batch-d3"
	_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, unit+"@"+m3Head, "pkg/pkg.go", "go", fromTip, "in-d3")
	if err != nil {
		t.Fatalf("batch plan: %v", err)
	}

	st, err := store.Open(f.ctx, f.client.Options().Addr)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()
	_ = capacity.SetBudget(f.ctx, st, "bench-d3", 4000, 8192, "operator", "")
	_ = f.client.HSet(f.ctx, "bench:bench-d3:desired", "machine", "bench-d3").Err()
	_ = f.client.HSet(f.ctx, "bench:bench-d3:land", "cores_go", "2000", "mem_go", "4096").Err()

	w := land.NewWorker(land.WorkerConfig{
		Client:    f.client,
		Store:     st,
		Bench:     "bench-d3",
		Repos:     []string{f.repo},
		Slots:     1,
		MirrorDir: bareDir,
	})

	ran, err := w.RunOnce(f.ctx, "slot-1")
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !ran {
		t.Fatalf("RunOnce did not process gate")
	}

	// 1. Verify core_s is computed, not hard-coded "10"
	rkey := land.ReceiptKey(f.repo, batchID, 1)
	coreS, err := f.client.HGet(f.ctx, rkey, "core_s").Result()
	if err != nil {
		t.Fatalf("HGet core_s: %v", err)
	}
	if coreS == "10" {
		t.Fatalf("core_s is hard-coded literal \"10\", must be measured from execution")
	}

	// 2. Verify gid receipt was written
	gid := land.GID("single", f.base, fromTip, requiredSetID, policyID, runnerID)
	ciKey := land.CIKey(f.repo, m3Head, gid)
	exists, err := f.client.Exists(f.ctx, ciKey).Result()
	if err != nil {
		t.Fatalf("Exists ciKey: %v", err)
	}
	if exists != 1 {
		t.Fatalf("gid receipt %s does not exist; worker must write gid receipts", ciKey)
	}

	ciVerdict, err := f.client.HGet(f.ctx, ciKey, "verdict").Result()
	if err != nil {
		t.Fatalf("HGet ci verdict: %v", err)
	}
	if ciVerdict != "GREEN" {
		t.Fatalf("expected ci verdict GREEN, got %s", ciVerdict)
	}

	gids, err := f.client.SMembers(f.ctx, land.CIGIDsKey(f.repo, m3Head)).Result()
	if err != nil {
		t.Fatalf("SMembers gids: %v", err)
	}
	found := false
	for _, g := range gids {
		if g == gid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected gid %s in %s:gids, got %v", gid, m3Head, gids)
	}
}

// TestDefect4_GateTakeOneArgumentForm verifies that ns_gate_take uses a single argument form
// without a hard-coded list of bench names, correctly handling arbitrary bench names.
func TestDefect4_GateTakeOneArgumentForm(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	// Verify that land.lua does NOT contain hardcoded bench names
	luaBytes, err := os.ReadFile("../fn/lua/land.lua")
	if err != nil {
		t.Fatalf("read land.lua: %v", err)
	}
	luaContent := string(luaBytes)
	for _, bench := range []string{"studio", "hetzner", "macbook", "superman", "spacegame", "hulk", "vision"} {
		pattern := fmt.Sprintf("args[1] ~= '%s'", bench)
		if strings.Contains(luaContent, pattern) {
			t.Fatalf("land.lua still contains hard-coded bench name check %q; must keep one argument form", pattern)
		}
	}

	// Test with a novel bench name
	const customBench = "custom-bench-falcon-99"
	st, err := store.Open(f.ctx, f.client.Options().Addr)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	if err := capacity.SetBudget(f.ctx, st, customBench, 4000, 8192, "operator", ""); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	_ = f.client.HSet(f.ctx, "bench:"+customBench+":desired", "machine", customBench).Err()
	_ = f.client.HSet(f.ctx, "bench:"+customBench+":land", "cores_go", "2000", "mem_go", "4096").Err()

	unit := "gh/mas-bandwidth/nova-tools/304"
	head := "4444111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"
	_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-304", Head: head, BaseSHA: fromTip,
	})
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0)
	_, _, err = land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-d4", f.lease, unit+"@"+head, "", "go", fromTip, "in-d4")
	if err != nil {
		t.Fatalf("batch plan: %v", err)
	}

	// CallGateTake with custom bench: must parse correctly and claim the entry
	res, err := land.CallGateTake(f.ctx, f.client, f.repo, customBench, "slot-1", "go", 2000, 4096)
	if err != nil {
		t.Fatalf("CallGateTake: %v", err)
	}
	if res.Status != "OK" {
		t.Fatalf("expected OK on populated gates stream, got status %q", res.Status)
	}

	// Verify the debit was taken for consumer land:<customBench>:slot-1
	expectedConsumer := "land:" + customBench + ":slot-1"
	dkey := "machine:" + customBench + ":debit:" + expectedConsumer
	exists, err := f.client.Exists(f.ctx, dkey).Result()
	if err != nil {
		t.Fatalf("Exists dkey: %v", err)
	}
	if exists != 1 {
		t.Fatalf("expected debit key %s to exist in Redis", dkey)
	}
}

// TestDefect5_WorkerMergeConflictVerdict verifies that a merge-tree conflict is receipted
// as CONFLICT, never ERROR (spec 5.3, 5).
func TestDefect5_WorkerMergeConflictVerdict(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	bareDir, workDir, runGit := setupGitMirror(t)

	// Base commit
	_ = os.WriteFile(filepath.Join(workDir, "conflict.txt"), []byte("base line\n"), 0644)
	runGit("add", "conflict.txt")
	runGit("commit", "-m", "base commit")
	runGit("push", "origin", "HEAD:main")
	fromTip := runGit("rev-parse", "HEAD")

	// Member 1 modifies conflict.txt
	runGit("checkout", "-b", "m1", fromTip)
	_ = os.WriteFile(filepath.Join(workDir, "conflict.txt"), []byte("member 1 conflicting change\n"), 0644)
	runGit("commit", "-am", "member 1 change")
	runGit("push", "origin", "HEAD:m1")
	m1Head := runGit("rev-parse", "HEAD")

	// Member 2 modifies conflict.txt differently
	runGit("checkout", "-b", "m2", fromTip)
	_ = os.WriteFile(filepath.Join(workDir, "conflict.txt"), []byte("member 2 completely different conflicting change\n"), 0644)
	runGit("commit", "-am", "member 2 change")
	runGit("push", "origin", "HEAD:m2")
	m2Head := runGit("rev-parse", "HEAD")

	unit1 := "gh/mas-bandwidth/nova-tools/305a"
	unit2 := "gh/mas-bandwidth/nova-tools/305b"
	_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit1, Repo: f.repo, Base: f.base,
		Branch: "m1", Head: m1Head, BaseSHA: fromTip,
	})
	_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit2, Repo: f.repo, Base: f.base,
		Branch: "m2", Head: m2Head, BaseSHA: fromTip,
	})
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit1, f.repo, f.base, 0)
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit2, f.repo, f.base, 0)

	batchID := "batch-d5"
	membersCSV := fmt.Sprintf("%s@%s,%s@%s", unit1, m1Head, unit2, m2Head)
	_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, membersCSV, "conflict.txt", "go", fromTip, "in-d5")
	if err != nil {
		t.Fatalf("batch plan: %v", err)
	}

	st, err := store.Open(f.ctx, f.client.Options().Addr)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()
	_ = capacity.SetBudget(f.ctx, st, "bench-d5", 4000, 8192, "operator", "")
	_ = f.client.HSet(f.ctx, "bench:bench-d5:desired", "machine", "bench-d5").Err()
	_ = f.client.HSet(f.ctx, "bench:bench-d5:land", "cores_go", "2000", "mem_go", "4096").Err()

	w := land.NewWorker(land.WorkerConfig{
		Client:    f.client,
		Store:     st,
		Bench:     "bench-d5",
		Repos:     []string{f.repo},
		Slots:     1,
		MirrorDir: bareDir,
	})

	ran, err := w.RunOnce(f.ctx, "slot-1")
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !ran {
		t.Fatalf("RunOnce did not process gate")
	}

	rkey := land.ReceiptKey(f.repo, batchID, 1)
	verdict, err := f.client.HGet(f.ctx, rkey, "verdict").Result()
	if err != nil {
		t.Fatalf("HGet verdict: %v", err)
	}
	if verdict != "CONFLICT" {
		t.Fatalf("expected receipt verdict CONFLICT on merge conflict, got %q", verdict)
	}

	bkey := land.BatchKey(f.repo, f.base, batchID)
	state, err := f.client.HGet(f.ctx, bkey, "state").Result()
	if err != nil {
		t.Fatalf("HGet state: %v", err)
	}
	if state != "conflict" {
		t.Fatalf("expected batch state conflict, got %q", state)
	}
}
