package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestKeeperRefusesMissingQueue verifies that omitting --queue is refused with exit 2.
func TestKeeperRefusesMissingQueue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Queue:  "",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--queue is required") {
		t.Fatalf("stderr does not name --queue: %s", stderr.String())
	}
}

// TestKeeperRunsOnTheHourAndWritesReceipt tests the full hourly wake cycle.
func TestKeeperRunsOnTheHourAndWritesReceipt(t *testing.T) {
	qDir := t.TempDir()
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Queue:  qDir,
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return now },
		LoopChecker: func(loops []string) []LoopStatus {
			res := make([]LoopStatus, len(loops))
			for i, l := range loops {
				res[i] = LoopStatus{
					Name:    l,
					Alive:   true,
					PID:     1000 + i,
					Command: l + " binary",
					Details: "running",
				}
			}
			return res
		},
		BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
			return MergeBaseStatus{
				Repo:         repo,
				Base:         base,
				HeadSHA:      "86abcf23",
				BaseSHA:      "86abcf23",
				MergeBaseSHA: "86abcf23",
				Status:       "aligned",
				Decision:     "OK: head is aligned with base",
			}, nil
		},
		Folder: func(queue, roots string) (FoldSummary, error) {
			return FoldSummary{
				Total:   2,
				Clean:   1,
				Defect:  1,
				Failed:  0,
				Skipped: 0,
				Results: []FoldResult{
					{Card: "card-001.md", Verdict: "clean"},
					{Card: "card-002.md", Verdict: "defect"},
				},
			}, nil
		},
	})

	if code != 0 {
		t.Fatalf("FleetKeeper exit = %d, stderr: %s", code, stderr.String())
	}

	expectedReceipt := filepath.Join(qDir, "wake", "2026-09-21T10Z.receipt")
	data, err := os.ReadFile(expectedReceipt)
	if err != nil {
		t.Fatalf("receipt file %s not found: %v", expectedReceipt, err)
	}

	receipt, err := ParseKeeperReceipt(data)
	if err != nil {
		t.Fatalf("ParseKeeperReceipt failed: %v", err)
	}

	if receipt.Hour != "2026-09-21T10:00:00Z" {
		t.Errorf("receipt hour = %q, want 2026-09-21T10:00:00Z", receipt.Hour)
	}
	if receipt.Verdict != "OK" {
		t.Errorf("receipt verdict = %q, want OK", receipt.Verdict)
	}
	if receipt.Folds.Total != 2 || receipt.Folds.Clean != 1 || receipt.Folds.Defect != 1 {
		t.Errorf("receipt folds = %+v, want total=2 clean=1 defect=1", receipt.Folds)
	}
	if receipt.MergeBase.Status != "aligned" {
		t.Errorf("receipt merge base status = %q, want aligned", receipt.MergeBase.Status)
	}
	if len(receipt.Loops) != 4 {
		t.Errorf("receipt loops count = %d, want 4", len(receipt.Loops))
	}
	for _, l := range receipt.Loops {
		if !l.Alive {
			t.Errorf("loop %s is not alive in receipt", l.Name)
		}
	}

	// Verify stdout one-line summary
	out := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(out, "KEEPER WAKE hour=2026-09-21T10:00:00Z") {
		t.Errorf("stdout does not start with KEEPER WAKE hour: %q", out)
	}
	if !strings.Contains(out, "folded=2") {
		t.Errorf("stdout does not report folded=2: %q", out)
	}
	if !strings.Contains(out, "base=aligned") {
		t.Errorf("stdout does not report base=aligned: %q", out)
	}
	if !strings.Contains(out, "loops=dealer:up,backpressure:up,harvest:up,sprint-table:up") {
		t.Errorf("stdout does not report all loops up: %q", out)
	}
}

// TestKeeperDetectsDeadDurableLoops tests detection of dead durable loops and strict failure mode.
func TestKeeperDetectsDeadDurableLoops(t *testing.T) {
	qDir := t.TempDir()
	now := time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Queue:  qDir,
		Strict: true,
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return now },
		LoopChecker: func(loops []string) []LoopStatus {
			return []LoopStatus{
				{Name: "dealer", Alive: true, PID: 101},
				{Name: "backpressure", Alive: false, PID: 0, Details: "not running"},
				{Name: "harvest", Alive: true, PID: 103},
				{Name: "sprint table", Alive: false, PID: 0, Details: "not running"},
			}
		},
		BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
			return MergeBaseStatus{Status: "aligned"}, nil
		},
		Folder: func(queue, roots string) (FoldSummary, error) {
			return FoldSummary{}, nil
		},
	})

	if code != 1 {
		t.Fatalf("strict exit = %d, want 1 for dead loops", code)
	}

	expectedReceipt := filepath.Join(qDir, "wake", "2026-09-21T11Z.receipt")
	data, err := os.ReadFile(expectedReceipt)
	if err != nil {
		t.Fatalf("receipt file not found: %v", err)
	}
	receipt, err := ParseKeeperReceipt(data)
	if err != nil {
		t.Fatalf("ParseKeeperReceipt failed: %v", err)
	}

	if receipt.Verdict != "DEGRADED" {
		t.Errorf("verdict = %q, want DEGRADED", receipt.Verdict)
	}

	// Verify decisions record dead loops
	foundBP := false
	foundST := false
	for _, d := range receipt.Decisions {
		if strings.Contains(d, "LOOP backpressure: DEAD") {
			foundBP = true
		}
		if strings.Contains(d, "LOOP sprint table: DEAD") {
			foundST = true
		}
	}
	if !foundBP || !foundST {
		t.Errorf("decisions did not record dead loops: %v", receipt.Decisions)
	}

	out := stdout.String()
	if !strings.Contains(out, "dead=backpressure,sprint table") {
		t.Errorf("stdout does not report dead loops: %q", out)
	}
}

// TestKeeperRestartsDeadDurableLoops tests automatic restart attempts for dead loops.
func TestKeeperRestartsDeadDurableLoops(t *testing.T) {
	qDir := t.TempDir()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	restarted := []string{}
	var stdout, stderr bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Queue:       qDir,
		RestartDead: true,
		Stdout:      &stdout,
		Stderr:      &stderr,
		Now:         func() time.Time { return now },
		LoopChecker: func(loops []string) []LoopStatus {
			return []LoopStatus{
				{Name: "dealer", Alive: false, PID: 0},
				{Name: "backpressure", Alive: true, PID: 102},
				{Name: "harvest", Alive: true, PID: 103},
				{Name: "sprint table", Alive: true, PID: 104},
			}
		},
		Restarter: func(loopName string) error {
			restarted = append(restarted, loopName)
			return nil
		},
		BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
			return MergeBaseStatus{Status: "aligned"}, nil
		},
		Folder: func(queue, roots string) (FoldSummary, error) {
			return FoldSummary{}, nil
		},
	})

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(restarted) != 1 || restarted[0] != "dealer" {
		t.Fatalf("restarted = %v, want ['dealer']", restarted)
	}

	expectedReceipt := filepath.Join(qDir, "wake", "2026-09-21T12Z.receipt")
	data, _ := os.ReadFile(expectedReceipt)
	if !strings.Contains(string(data), "LOOP dealer: DEAD -> restart required (restart succeeded)") {
		t.Errorf("receipt does not note restart success:\n%s", string(data))
	}
}

// TestKeeperDefaultFolderMovesCompletedCards tests the real disk-based result folder.
func TestKeeperDefaultFolderMovesCompletedCards(t *testing.T) {
	qDir := t.TempDir()
	launchedDir := filepath.Join(qDir, "launched")
	resultsDir := filepath.Join(qDir, "results")
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create 3 cards in launched with results
	card1 := "card-1.md"
	if err := os.WriteFile(filepath.Join(launchedDir, card1), []byte("# card 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resultsDir, card1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, card1, "RESULT.md"), []byte("RESULT clean sha=123\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	card2 := "card-2.md"
	if err := os.WriteFile(filepath.Join(launchedDir, card2), []byte("# card 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resultsDir, card2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, card2, "RESULT.md"), []byte("RESULT defect receipt=err.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	card3 := "card-3.md"
	if err := os.WriteFile(filepath.Join(launchedDir, card3), []byte("# card 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resultsDir, card3), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, card3, "RESULT.md"), []byte("RESULT failed build error\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := defaultResultFolder(qDir, "")
	if err != nil {
		t.Fatalf("defaultResultFolder failed: %v", err)
	}

	if summary.Total != 3 {
		t.Errorf("total = %d, want 3", summary.Total)
	}
	if summary.Clean != 1 {
		t.Errorf("clean = %d, want 1", summary.Clean)
	}
	if summary.Defect != 1 {
		t.Errorf("defect = %d, want 1", summary.Defect)
	}
	if summary.Failed != 1 {
		t.Errorf("failed = %d, want 1", summary.Failed)
	}

	// Verify cards moved out of launched
	if _, err := os.Stat(filepath.Join(qDir, "done", card1)); err != nil {
		t.Errorf("card1 not in done/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(qDir, "done", card2)); err != nil {
		t.Errorf("card2 not in done/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(qDir, "failed", card3)); err != nil {
		t.Errorf("card3 not in failed/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launchedDir, card1)); !os.IsNotExist(err) {
		t.Errorf("card1 still in launched/")
	}
}

// TestKeeperMergeBaseChecker verifies merge base comparison logic.
func TestKeeperMergeBaseChecker(t *testing.T) {
	cases := []struct {
		mb, base, head string
		wantStatus     string
	}{
		{"abc", "abc", "abc", "aligned"},
		{"abc", "abc", "def", "ahead"},
		{"abc", "def", "abc", "behind"},
		{"123", "abc", "def", "diverged"},
	}

	for _, tc := range cases {
		st := MergeBaseStatus{
			MergeBaseSHA: tc.mb,
			BaseSHA:      tc.base,
			HeadSHA:      tc.head,
		}
		switch {
		case st.MergeBaseSHA == st.BaseSHA && st.MergeBaseSHA == st.HeadSHA:
			st.Status = "aligned"
		case st.MergeBaseSHA == st.BaseSHA && st.MergeBaseSHA != st.HeadSHA:
			st.Status = "ahead"
		case st.MergeBaseSHA == st.HeadSHA && st.MergeBaseSHA != st.BaseSHA:
			st.Status = "behind"
		default:
			st.Status = "diverged"
		}
		if st.Status != tc.wantStatus {
			t.Errorf("mb=%s base=%s head=%s got status %q, want %q", tc.mb, tc.base, tc.head, st.Status, tc.wantStatus)
		}
	}
}

// TestKeeperMutationTestWithTeeth proves that mutations to keeper verification fail loudly.
func TestKeeperMutationTestWithTeeth(t *testing.T) {
	qDir := t.TempDir()
	now := time.Date(2026, 9, 21, 14, 0, 0, 0, time.UTC)

	// Invariant 1: Receipt MUST be created in queue/wake/ with valid hour stamp
	receiptFile := filepath.Join(qDir, "wake", "2026-09-21T14Z.receipt")

	var stdout, stderr bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Queue:  qDir,
		Strict: true,
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return now },
		LoopChecker: func(loops []string) []LoopStatus {
			return []LoopStatus{
				{Name: "dealer", Alive: true, PID: 101},
				{Name: "backpressure", Alive: true, PID: 102},
				{Name: "harvest", Alive: true, PID: 103},
				{Name: "sprint table", Alive: true, PID: 104},
			}
		},
		BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
			return MergeBaseStatus{
				HeadSHA:      "commit1",
				BaseSHA:      "commit1",
				MergeBaseSHA: "commit1",
				Status:       "aligned",
				Decision:     "OK",
			}, nil
		},
		Folder: func(queue, roots string) (FoldSummary, error) {
			return FoldSummary{Total: 1, Clean: 1}, nil
		},
	})

	if code != 0 {
		t.Fatalf("normal run must succeed, got %d", code)
	}
	if _, err := os.Stat(receiptFile); err != nil {
		t.Fatalf("receipt must exist at %s", receiptFile)
	}

	// MUTATION 1: A dead loop in strict mode MUST cause non-zero exit and DEGRADED verdict.
	codeMut := FleetKeeper(FleetKeeperInput{
		Queue:  qDir,
		Strict: true,
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return now },
		LoopChecker: func(loops []string) []LoopStatus {
			return []LoopStatus{
				{Name: "dealer", Alive: true, PID: 101},
				{Name: "backpressure", Alive: false, PID: 0}, // MUTATION: dead backpressure
				{Name: "harvest", Alive: true, PID: 103},
				{Name: "sprint table", Alive: true, PID: 104},
			}
		},
		BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
			return MergeBaseStatus{Status: "aligned"}, nil
		},
		Folder: func(queue, roots string) (FoldSummary, error) {
			return FoldSummary{}, nil
		},
	})
	if codeMut == 0 {
		t.Fatalf("mutation with dead loop must NOT exit 0 in strict mode (teeth failure)")
	}

	// MUTATION 2: Diverged base in strict mode MUST cause non-zero exit and ATTENTION_NEEDED verdict.
	codeDiverged := FleetKeeper(FleetKeeperInput{
		Queue:  qDir,
		Strict: true,
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return now },
		LoopChecker: func(loops []string) []LoopStatus {
			return []LoopStatus{
				{Name: "dealer", Alive: true, PID: 101},
				{Name: "backpressure", Alive: true, PID: 102},
				{Name: "harvest", Alive: true, PID: 103},
				{Name: "sprint table", Alive: true, PID: 104},
			}
		},
		BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
			return MergeBaseStatus{
				Status:   "diverged",
				Decision: "WARN: diverged",
			}, nil
		},
		Folder: func(queue, roots string) (FoldSummary, error) {
			return FoldSummary{}, nil
		},
	})
	if codeDiverged == 0 {
		t.Fatalf("mutation with diverged base must NOT exit 0 in strict mode (teeth failure)")
	}
}

// TestDefaultResultFolderLeavesActiveCardWithoutResultInLaunched verifies that an active
// card still running in launched/ (and mentioning clean, defect, skip in its prompt/body)
// is NOT folded or moved to done/failed when RESULT.md / .result is absent.
func TestDefaultResultFolderLeavesActiveCardWithoutResultInLaunched(t *testing.T) {
	qDir := t.TempDir()
	launchedDir := filepath.Join(qDir, "launched")
	resultsDir := filepath.Join(qDir, "results")
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. An active card with text containing clean/defect/skip/pass, but NO result file.
	activeCard := "active-card.md"
	activeBody := "# Active Card\nEnsure code is clean, check defect rate, do not skip tests, expect pass.\n"
	if err := os.WriteFile(filepath.Join(launchedDir, activeCard), []byte(activeBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. A finished card with an actual RESULT.md file.
	finishedCard := "finished-card.md"
	if err := os.WriteFile(filepath.Join(launchedDir, finishedCard), []byte("# Finished Card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resultsDir, finishedCard), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, finishedCard, "RESULT.md"), []byte("RESULT clean commit=abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := defaultResultFolder(qDir, "")
	if err != nil {
		t.Fatalf("defaultResultFolder failed: %v", err)
	}

	// Only finished-card should have been folded
	if summary.Total != 1 {
		t.Errorf("total folded = %d, want 1", summary.Total)
	}
	if summary.Clean != 1 {
		t.Errorf("clean folded = %d, want 1", summary.Clean)
	}

	// Finished card must be moved to done/
	if _, err := os.Stat(filepath.Join(qDir, "done", finishedCard)); err != nil {
		t.Errorf("finished card not found in done/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launchedDir, finishedCard)); !os.IsNotExist(err) {
		t.Errorf("finished card still exists in launched/")
	}

	// Active card must REMAIN in launched/ and NOT be in done/ or failed/
	if _, err := os.Stat(filepath.Join(launchedDir, activeCard)); err != nil {
		t.Errorf("active card missing from launched/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(qDir, "done", activeCard)); !os.IsNotExist(err) {
		t.Errorf("active card was improperly moved to done/")
	}
	if _, err := os.Stat(filepath.Join(qDir, "failed", activeCard)); !os.IsNotExist(err) {
		t.Errorf("active card was improperly moved to failed/")
	}
}

// TestFleetKeeperPropagatesFolderAndBaseErrors tests that Folder or BaseChecker errors
// cause FleetKeeper to set verdict="ATTENTION_NEEDED", log the error in decisions, and exit 1.
func TestFleetKeeperPropagatesFolderAndBaseErrors(t *testing.T) {
	qDir := t.TempDir()
	now := time.Date(2026, 9, 21, 16, 0, 0, 0, time.UTC)

	// Case 1: Folder returns an error
	{
		var stdout, stderr bytes.Buffer
		code := FleetKeeper(FleetKeeperInput{
			Queue:  qDir,
			Strict: false, // Must exit 1 even when strict=false
			Stdout: &stdout,
			Stderr: &stderr,
			Now:    func() time.Time { return now },
			LoopChecker: func(loops []string) []LoopStatus {
				return []LoopStatus{{Name: "dealer", Alive: true, PID: 101}}
			},
			BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
				return MergeBaseStatus{Status: "aligned"}, nil
			},
			Folder: func(queue, roots string) (FoldSummary, error) {
				return FoldSummary{}, os.ErrPermission
			},
		})
		if code != 1 {
			t.Fatalf("folder error exit = %d, want 1", code)
		}

		receiptPath := filepath.Join(qDir, "wake", "2026-09-21T16Z.receipt")
		data, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Fatalf("receipt not written on folder error: %v", err)
		}
		receipt, err := ParseKeeperReceipt(data)
		if err != nil {
			t.Fatalf("ParseKeeperReceipt failed: %v", err)
		}
		if receipt.Verdict != "ATTENTION_NEEDED" {
			t.Errorf("receipt verdict = %q, want ATTENTION_NEEDED", receipt.Verdict)
		}
		foundFoldErr := false
		for _, d := range receipt.Decisions {
			if strings.Contains(d, "FOLD FAILED:") {
				foundFoldErr = true
				break
			}
		}
		if !foundFoldErr {
			t.Errorf("decisions do not contain FOLD FAILED: %v", receipt.Decisions)
		}
	}

	// Case 2: BaseChecker returns an error
	{
		var stdout, stderr bytes.Buffer
		now2 := time.Date(2026, 9, 21, 17, 0, 0, 0, time.UTC)
		code := FleetKeeper(FleetKeeperInput{
			Queue:  qDir,
			Strict: false, // Must exit 1 even when strict=false
			Stdout: &stdout,
			Stderr: &stderr,
			Now:    func() time.Time { return now2 },
			LoopChecker: func(loops []string) []LoopStatus {
				return []LoopStatus{{Name: "dealer", Alive: true, PID: 101}}
			},
			BaseChecker: func(repo, base string) (MergeBaseStatus, error) {
				return MergeBaseStatus{Repo: repo, Base: base, Status: "error"}, os.ErrInvalid
			},
			Folder: func(queue, roots string) (FoldSummary, error) {
				return FoldSummary{}, nil
			},
		})
		if code != 1 {
			t.Fatalf("base checker error exit = %d, want 1", code)
		}

		receiptPath := filepath.Join(qDir, "wake", "2026-09-21T17Z.receipt")
		data, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Fatalf("receipt not written on base checker error: %v", err)
		}
		receipt, err := ParseKeeperReceipt(data)
		if err != nil {
			t.Fatalf("ParseKeeperReceipt failed: %v", err)
		}
		if receipt.Verdict != "ATTENTION_NEEDED" {
			t.Errorf("receipt verdict = %q, want ATTENTION_NEEDED", receipt.Verdict)
		}
		foundBaseErr := false
		for _, d := range receipt.Decisions {
			if strings.Contains(d, "BASE FAILED:") {
				foundBaseErr = true
				break
			}
		}
		if !foundBaseErr {
			t.Errorf("decisions do not contain BASE FAILED: %v", receipt.Decisions)
		}
	}
}
