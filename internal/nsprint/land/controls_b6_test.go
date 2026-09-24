package land_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// TestL2c verifies control L2c (Issue #3139 rev 7 §11):
// kill a slot immediately after ns_gate_take returns: requeued within 30 s, exactly one eventual receipt;
// 50 ticks of NOBUDGET: 0 pending entries and 0 debits, then budget restored: exactly one claim,
// and the debit sum is 0 after the receipt (fails on: an entry lost before claim; a leaked debit).
func TestL2c(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	const machine = "bench-studio"
	st, err := store.Open(f.ctx, f.client.Options().Addr)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	// 1. Setup machine budget: 4000 cpu, 8192 mem
	if err := capacity.SetBudget(f.ctx, st, machine, 4000, 8192, "operator", ""); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	_ = f.client.HSet(f.ctx, "bench:studio:desired", "machine", machine).Err()
	_ = f.client.HSet(f.ctx, "bench:studio:land", "cores_go", "2000", "mem_go", "4096").Err()

	unit := "gh/mas-bandwidth/nova-tools/202"
	head := "2222111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"

	_, err = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-202", Head: head, BaseSHA: fromTip,
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}

	_, entryID, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l2c", f.lease, unit+"@"+head, "", "go", fromTip, "in-l2c")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if entryID == "" {
		t.Fatalf("missing entryID")
	}

	// Exhaust budget with a holder debit
	_, err = capacity.Take(f.ctx, st, capacity.TakeRequest{
		Machine:  machine,
		Consumer: "holder-exhaust",
		CPUMilli: 4000,
		MemMB:    8192,
		TTLMs:    60000,
		Kind:     capacity.KindLand,
	})
	if err != nil {
		t.Fatalf("take exhaust: %v", err)
	}

	// 50 ticks of NOBUDGET: 0 pending entries and 0 debits
	for i := 0; i < 50; i++ {
		res, err := land.CallGateTake(f.ctx, f.client, f.repo, "studio", "slot-1", "go", 2000, 4096)
		if err != nil {
			t.Fatalf("tick %d take err: %v", i, err)
		}
		if res.Status != "NOBUDGET" {
			t.Fatalf("tick %d: got status %s, want NOBUDGET", i, res.Status)
		}
	}

	// Invariant: 0 pending entries in consumer group workers
	pend, err := f.client.XPending(f.ctx, "land:"+f.repo+":gates", "workers").Result()
	if err != nil && !strings.Contains(err.Error(), "NOGROUP") {
		t.Fatalf("xpending: %v", err)
	}
	if err == nil && pend.Count != 0 {
		t.Fatalf("expected 0 pending entries under NOBUDGET, got %d", pend.Count)
	}

	// Invariant: 0 debits for slot-1
	debits, err := capacity.ListDebits(f.ctx, st, machine)
	if err != nil {
		t.Fatalf("list debits: %v", err)
	}
	for _, d := range debits {
		if d.Consumer == "land:studio:slot-1" {
			t.Fatalf("leaked debit found for slot-1: %+v", d)
		}
	}

	// Restore budget by giving back the holder debit
	err = capacity.Give(f.ctx, st, capacity.GiveRequest{
		Machine:   machine,
		Consumer:  "holder-exhaust",
		Confirmed: true,
	})
	if err != nil {
		t.Fatalf("give exhaust: %v", err)
	}

	// Budget restored: exactly one claim
	takeRes1, err := land.CallGateTake(f.ctx, f.client, f.repo, "studio", "slot-1", "go", 2000, 4096)
	if err != nil || takeRes1.Status != "OK" {
		t.Fatalf("take after restore: got %+v, err: %v", takeRes1, err)
	}

	// Kill slot immediately after ns_gate_take returns:
	_ = f.client.Del(f.ctx, "worker:studio:slot-1").Err()

	// Requeued within 30 s
	requeued, err := land.SweepReclaim(f.ctx, f.client, f.repo, f.base)
	if err != nil {
		t.Fatalf("sweep reclaim: %v", err)
	}
	if len(requeued) != 1 || requeued[0].Attempt != 2 {
		t.Fatalf("expected batch-l2c requeued at attempt 2, got: %+v", requeued)
	}

	// Slot 2 takes the requeued attempt
	takeRes2, err := land.CallGateTake(f.ctx, f.client, f.repo, "studio", "slot-2", "go", 2000, 4096)
	if err != nil || takeRes2.Status != "OK" {
		t.Fatalf("slot-2 take: got %+v, err %v", takeRes2, err)
	}

	// Writes receipt
	resReceipt, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l2c", 2, takeRes2.Token, "GREEN", "studio", "worker-2", "train-head", "train-tree", "in-l2c", "", "", "", "", "10")
	if err != nil || resReceipt != "OK" {
		t.Fatalf("write receipt: got %s, err %v", resReceipt, err)
	}

	// Exactly one eventual receipt exists
	r1Exists, _ := f.client.Exists(f.ctx, "land:"+f.repo+":receipt:batch-l2c:1").Result()
	r2Exists, _ := f.client.Exists(f.ctx, "land:"+f.repo+":receipt:batch-l2c:2").Result()
	if r1Exists != 0 || r2Exists != 1 {
		t.Fatalf("expected exactly one eventual receipt (attempt 2), got r1=%d, r2=%d", r1Exists, r2Exists)
	}

	// Debit sum is 0 after the receipt
	activeDebits, err := capacity.ListDebits(f.ctx, st, machine)
	if err != nil {
		t.Fatalf("list debits: %v", err)
	}
	debitSum := 0
	for _, d := range activeDebits {
		debitSum += d.CPUMilli
	}
	if debitSum != 0 {
		t.Fatalf("expected debit sum = 0 after receipt, got %d (debits: %+v)", debitSum, activeDebits)
	}
}

// TestL5 verifies control L5 (Issue #3139 rev 7 §11):
// SIGKILL mid-gate: re-gated within 30 s, late receipt STALE, one final receipt
// (fails on: the 90 s reclaim; 5:32 PM unit restarts).
func TestL5(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	unit := "gh/mas-bandwidth/nova-tools/105"
	head := "5555111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"

	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-105", Head: head, BaseSHA: fromTip,
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}

	token1, entryID1, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l5", f.lease, unit+"@"+head, "", "go", fromTip, "in-l5")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if entryID1 == "" {
		t.Fatalf("missing entryID")
	}

	// Slot 1 claims attempt 1
	claimRes, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l5", 1, token1, "studio", "slot-1")
	if err != nil || claimRes != "OK" {
		t.Fatalf("slot-1 claim: got %s, err %v", claimRes, err)
	}

	// SIGKILL mid-gate: simulated by worker heartbeat key removal (worker died, key expired)
	_ = f.client.Del(f.ctx, "worker:studio:slot-1").Err()

	// Reclaim sweep runs: must re-gate within 30 s
	requeued, err := land.SweepReclaim(f.ctx, f.client, f.repo, f.base)
	if err != nil {
		t.Fatalf("sweep reclaim: %v", err)
	}
	if len(requeued) != 1 || requeued[0].Attempt != 2 {
		t.Fatalf("expected batch-l5 requeued at attempt 2, got: %+v", requeued)
	}

	token2 := requeued[0].Token

	// Late receipt from the killed worker for attempt 1
	lateRes, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l5", 1, token1, "GREEN", "studio", "worker-1", "train-head", "train-tree", "in-l5", "", "", "", "", "10")
	if err != nil || lateRes != "STALE" {
		t.Fatalf("late receipt: got %s, err %v, want STALE", lateRes, err)
	}

	// Slot 2 claims attempt 2
	claimRes2, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l5", 2, token2, "superman", "slot-1")
	if err != nil || claimRes2 != "OK" {
		t.Fatalf("slot-2 claim: got %s, err %v", claimRes2, err)
	}

	// Slot 2 writes final receipt
	receiptRes2, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l5", 2, token2, "GREEN", "superman", "worker-2", "train-head", "train-tree", "in-l5", "", "", "", "", "10")
	if err != nil || receiptRes2 != "OK" {
		t.Fatalf("final receipt: got %s, err %v, want OK", receiptRes2, err)
	}

	// Verify exactly one final receipt
	r1Exists, _ := f.client.Exists(f.ctx, "land:"+f.repo+":receipt:batch-l5:1").Result()
	r2Exists, _ := f.client.Exists(f.ctx, "land:"+f.repo+":receipt:batch-l5:2").Result()
	if r1Exists != 0 || r2Exists != 1 {
		t.Fatalf("expected one final receipt for attempt 2, got r1=%d, r2=%d", r1Exists, r2Exists)
	}
}

// TestL31 verifies control L31 (Issue #3139 rev 7 §11):
// a GREEN receipt is reused only when input_id matches; a changed policy file or toolchain re-gates
// (fails on: a stale green landing).
func TestL31(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	unit := "gh/mas-bandwidth/nova-tools/131"
	head := "3131111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"

	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-131", Head: head, BaseSHA: fromTip,
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}

	// 1. Inputs for batch 1
	policy1 := "readers: 0\nrequired:\n  - build\n  - test\n"
	reqSet1 := "build,test"
	policyID1 := land.ComputePolicyID(policy1, reqSet1)
	runnerID1 := land.ComputeRunnerID("v1.0.0", "go1.24", "sbcl2.4")
	inputID1 := land.ComputeInputID(fromTip, []string{head}, "go", []string{"graph-1"}, policyID1, runnerID1)

	// 2. Plan batch 1 and gate GREEN
	tok1, entry1, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l31-1", f.lease, unit+"@"+head, "", "go", fromTip, inputID1)
	if err != nil || tok1 == "REUSE" {
		t.Fatalf("plan batch 1: got tok=%s, entry=%s, err=%v", tok1, entry1, err)
	}
	claimRes, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l31-1", 1, tok1, "studio", "slot-1")
	if err != nil || claimRes != "OK" {
		t.Fatalf("claim batch 1: got %s, err %v", claimRes, err)
	}
	rRes, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l31-1", 1, tok1, "GREEN", "studio", "worker-1", "train-head-31", "train-tree-31", inputID1, "", "", "", "", "10")
	if err != nil || rRes != "OK" {
		t.Fatalf("write receipt 1: got %s, err %v", rRes, err)
	}

	// Void batch 1 so member returns to landable state for re-plan
	if err := land.CallBatchVoid(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l31-1", "replan-test"); err != nil {
		t.Fatalf("void batch 1: %v", err)
	}

	// 3. Re-plan batch 2 with IDENTICAL input_id -> MUST REUSE receipt
	tok2, entry2, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l31-2", f.lease, unit+"@"+head, "", "go", fromTip, inputID1)
	if err != nil {
		t.Fatalf("plan batch 2 with identical input_id: %v", err)
	}
	if tok2 != "REUSE" {
		t.Fatalf("expected REUSE on identical input_id, got token %s", tok2)
	}
	expectedReceiptKey := fmt.Sprintf("land:%s:receipt:batch-l31-1:1", f.repo)
	if entry2 != expectedReceiptKey {
		t.Fatalf("expected reuse receipt %s, got %s", expectedReceiptKey, entry2)
	}
	b2State, _ := f.client.HGet(f.ctx, land.BatchKey(f.repo, f.base, "batch-l31-2"), "state").Result()
	if b2State != "green" {
		t.Fatalf("expected batch 2 state green on reuse, got %s", b2State)
	}

	// Void batch 2 so member returns to landable state for re-plan
	if err := land.CallBatchVoid(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l31-2", "replan-test"); err != nil {
		t.Fatalf("void batch 2: %v", err)
	}

	// 4. Changed policy file -> input_id changes -> MUST RE-GATE (not reuse)
	policy2 := "readers: 0\nrequired:\n  - build\n  - vet\n  - test\n"
	reqSet2 := "build,vet,test"
	policyID2 := land.ComputePolicyID(policy2, reqSet2)
	inputID2 := land.ComputeInputID(fromTip, []string{head}, "go", []string{"graph-1"}, policyID2, runnerID1)
	if inputID2 == inputID1 {
		t.Fatalf("input_id must change when policy changes")
	}

	tok3, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l31-3", f.lease, unit+"@"+head, "", "go", fromTip, inputID2)
	if err != nil {
		t.Fatalf("plan batch 3: %v", err)
	}
	if tok3 == "REUSE" {
		t.Fatalf("expected re-gate when policy file changed, but batch reused stale receipt")
	}

	// Void batch 3 so member returns to landable state for re-plan
	if err := land.CallBatchVoid(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l31-3", "replan-test"); err != nil {
		t.Fatalf("void batch 3: %v", err)
	}

	// 5. Changed toolchain -> runner_id changes -> input_id changes -> MUST RE-GATE (not reuse)
	runnerID2 := land.ComputeRunnerID("v1.1.0", "go1.25", "sbcl2.5")
	inputID3 := land.ComputeInputID(fromTip, []string{head}, "go", []string{"graph-1"}, policyID1, runnerID2)
	if inputID3 == inputID1 {
		t.Fatalf("input_id must change when toolchain changes")
	}

	tok4, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l31-4", f.lease, unit+"@"+head, "", "go", fromTip, inputID3)
	if err != nil {
		t.Fatalf("plan batch 4: %v", err)
	}
	if tok4 == "REUSE" {
		t.Fatalf("expected re-gate when toolchain changed, but batch reused stale receipt")
	}
}

// TestLDeterministicTrain verifies that two independent workers produce the identical train sha
// (spec 5.3, B6: "two workers produce one train sha").
func TestLDeterministicTrain(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// 1. Create a bare git repository as the mirror
	bareDir := filepath.Join(tmp, "mirror.git")
	if err := exec.Command("git", "init", "--bare", bareDir).Run(); err != nil {
		t.Fatalf("git init bare: %v", err)
	}

	// 2. In a work tree, create initial commit (fromTip) and member commits
	workDir := filepath.Join(tmp, "work")
	if err := exec.Command("git", "clone", bareDir, workDir).Run(); err != nil {
		t.Fatalf("git clone: %v", err)
	}

	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Initial commit: base
	_ = os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Base\n"), 0644)
	runGit(workDir, "add", "README.md")
	runGit(workDir, "commit", "-m", "initial commit")
	runGit(workDir, "push", "origin", "HEAD:main")
	fromTip := runGit(workDir, "rev-parse", "HEAD")

	// Member 1 commit
	runGit(workDir, "checkout", "-b", "m1")
	_ = os.WriteFile(filepath.Join(workDir, "file1.txt"), []byte("member 1 change\n"), 0644)
	runGit(workDir, "add", "file1.txt")
	runGit(workDir, "commit", "-m", "member 1")
	runGit(workDir, "push", "origin", "HEAD:m1")
	m1Head := runGit(workDir, "rev-parse", "HEAD")

	// Member 2 commit (branched from base)
	runGit(workDir, "checkout", "-b", "m2", fromTip)
	_ = os.WriteFile(filepath.Join(workDir, "file2.txt"), []byte("member 2 change\n"), 0644)
	runGit(workDir, "add", "file2.txt")
	runGit(workDir, "commit", "-m", "member 2")
	runGit(workDir, "push", "origin", "HEAD:m2")
	m2Head := runGit(workDir, "rev-parse", "HEAD")

	members := []string{m1Head, m2Head}
	const batchID = "batch-test-train"
	const createdAt = "1790252098"

	// Worker 1 builds train in bareDir
	train1, err := land.BuildTrain(ctx, land.TrainParams{
		GitDir:    bareDir,
		FromTip:   fromTip,
		Members:   members,
		BatchID:   batchID,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("worker 1 build train: %v", err)
	}

	// Worker 2 in an independent clone/mirror builds train with identical inputs
	clone2 := filepath.Join(tmp, "mirror2.git")
	if err := exec.Command("git", "clone", "--bare", bareDir, clone2).Run(); err != nil {
		t.Fatalf("clone bare2: %v", err)
	}

	train2, err := land.BuildTrain(ctx, land.TrainParams{
		GitDir:    clone2,
		FromTip:   fromTip,
		Members:   members,
		BatchID:   batchID,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("worker 2 build train: %v", err)
	}

	// ASSERTION: Two independent workers produce the exact same train sha and tree
	if train1.TrainHead != train2.TrainHead {
		t.Fatalf("train head mismatch: worker1=%s, worker2=%s", train1.TrainHead, train2.TrainHead)
	}
	if train1.TrainTree != train2.TrainTree {
		t.Fatalf("train tree mismatch: worker1=%s, worker2=%s", train1.TrainTree, train2.TrainTree)
	}
	if len(train1.Commits) != 2 || len(train2.Commits) != 2 {
		t.Fatalf("expected 2 intermediate commits, got w1=%d, w2=%d", len(train1.Commits), len(train2.Commits))
	}
	for i := range train1.Commits {
		if train1.Commits[i] != train2.Commits[i] {
			t.Fatalf("commit %d mismatch: %s != %s", i, train1.Commits[i], train2.Commits[i])
		}
	}
}
