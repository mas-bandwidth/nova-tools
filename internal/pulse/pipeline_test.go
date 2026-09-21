package pulse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock(t *testing.T) func() time.Time {
	fixed, err := time.Parse(time.RFC3339, "2026-09-21T14:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return func() time.Time { return fixed }
}

func TestClassifyPathAndRouting(t *testing.T) {
	// 1. Security paths -> Johnny
	secCases := []string{
		"cmd/nova-sandbox/main.go",
		"internal/secrets/vault.go",
		"infra/image/Dockerfile",
		"internal/crypto/keys.go",
		"docs/SPEC-SECRETS.md",
		"internal/sandbox/policy.go",
		"internal/auth/token.go",
	}
	for _, p := range secCases {
		friend, class := ClassifyPath(p)
		if friend != FriendJohnny || class != ClassSecurity {
			t.Errorf("ClassifyPath(%q) = (%s, %s); want (johnny, security)", p, friend, class)
		}
	}

	// 2. Contract paths -> Stella
	conCases := []string{
		"docs/SPEC-DECIDE.md",
		"COVENANT.md",
		"AGENTS.md",
		"GEMINI.md",
		"docs/SPEC-WORK.md",
		"docs/SPEC-PULSE.md",
		"internal/contract/rules.go",
	}
	for _, p := range conCases {
		friend, class := ClassifyPath(p)
		if friend != FriendStella || class != ClassContract {
			t.Errorf("ClassifyPath(%q) = (%s, %s); want (stella, contract)", p, friend, class)
		}
	}

	// 3. Spec/Test / General code paths -> Emma
	testCases := []string{
		"internal/pulse/cut_test.go",
		"internal/pulse/pipeline.go",
		"internal/ci/verify_test.go",
		"cmd/nova-pulse/main.go",
	}
	for _, p := range testCases {
		friend, class := ClassifyPath(p)
		if friend != FriendEmma || class != ClassSpecTest {
			t.Errorf("ClassifyPath(%q) = (%s, %s); want (emma, spec-test)", p, friend, class)
		}
	}

	// 4. Precedence: Security > Contract > SpecTest
	multiSec := []string{"internal/pulse/pipeline.go", "docs/SPEC-WORK.md", "internal/secrets/keys.go"}
	f, c := RouteFiles(multiSec)
	if f != FriendJohnny || c != ClassSecurity {
		t.Errorf("RouteFiles(multiSec) = (%s, %s); want (johnny, security)", f, c)
	}

	multiCon := []string{"internal/pulse/pipeline.go", "docs/SPEC-DECIDE.md"}
	f, c = RouteFiles(multiCon)
	if f != FriendStella || c != ClassContract {
		t.Errorf("RouteFiles(multiCon) = (%s, %s); want (stella, contract)", f, c)
	}

	specOnly := []string{"internal/pulse/pipeline.go", "internal/pulse/pipeline_test.go"}
	f, c = RouteFiles(specOnly)
	if f != FriendEmma || c != ClassSpecTest {
		t.Errorf("RouteFiles(specOnly) = (%s, %s); want (emma, spec-test)", f, c)
	}
}

func TestPipelineOnPROpen(t *testing.T) {
	qDir := t.TempDir()
	clock := fixedClock(t)

	headSHA := "1111222233334444555566667777888899990000"
	res, err := PipelineOnPROpen(PipelinePROpenInput{
		QueueDir:     qDir,
		Repo:         "mas-bandwidth/nova-tools",
		PR:           2509,
		Head:         headSHA,
		Base:         "dev",
		Title:        "Pipeline the second phase per card",
		TouchedFiles: []string{"internal/sandbox/policy.go", "internal/pulse/pipeline.go"},
		Checks:       "pass",
		Holds:        0,
		ReadCap:      5,
		Now:          clock,
	})
	if err != nil {
		t.Fatalf("PipelineOnPROpen failed: %v", err)
	}

	// Security path touched -> Johnny
	if res.Friend != FriendJohnny || res.Class != ClassSecurity {
		t.Errorf("got friend=%s, class=%s; want johnny, security", res.Friend, res.Class)
	}

	// Verify GateFacts file
	gfPath := filepath.Join(qDir, "gate", "pr-2509-facts.json")
	gfData, err := os.ReadFile(gfPath)
	if err != nil {
		t.Fatalf("reading gate facts: %v", err)
	}
	var gf GateFacts
	if err := json.Unmarshal(gfData, &gf); err != nil {
		t.Fatalf("unmarshal gate facts: %v", err)
	}
	if gf.PR != 2509 || gf.Head != headSHA || gf.Class != ClassSecurity || gf.Assigned != FriendJohnny {
		t.Errorf("unexpected gate facts content: %+v", gf)
	}

	// Verify ReportCard file
	rcPath := filepath.Join(qDir, "reports", "pr-2509.md")
	rcData, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("reading report card: %v", err)
	}
	rcStr := string(rcData)
	if !strings.Contains(rcStr, "Report Card: PR #2509") || !strings.Contains(rcStr, "`red-first`: pass") {
		t.Errorf("unexpected report card content:\n%s", rcStr)
	}

	// Verify ReadRequest file in Johnny's queue
	rrPath := filepath.Join(qDir, "reads", "johnny", "pr-2509.json")
	rrData, err := os.ReadFile(rrPath)
	if err != nil {
		t.Fatalf("reading read request: %v", err)
	}
	var rr ReadRequest
	if err := json.Unmarshal(rrData, &rr); err != nil {
		t.Fatalf("unmarshal read request: %v", err)
	}
	if rr.Status != "pending" || rr.Assigned != FriendJohnny || rr.GateFacts.Head != headSHA {
		t.Errorf("unexpected read request: %+v", rr)
	}

	// Verify Log line
	wantLine := "PIPELINE OPEN repo=mas-bandwidth/nova-tools pr=2509 head=1111222233334444555566667777888899990000 class=security assigned=johnny"
	if res.Line != wantLine {
		t.Errorf("got line %q; want %q", res.Line, wantLine)
	}
}

func TestProcessTypedRead_PromotesToLandable(t *testing.T) {
	qDir := t.TempDir()
	clock := fixedClock(t)
	headSHA := "aabbccddeeff00112233445566778899aabbccdd"

	// Create initial read request for PR 100 (security -> Johnny)
	_, err := PipelineOnPROpen(PipelinePROpenInput{
		QueueDir:     qDir,
		Repo:         "mas-bandwidth/nova-tools",
		PR:           100,
		Head:         headSHA,
		Base:         "dev",
		Title:        "Security fix",
		TouchedFiles: []string{"internal/secrets/vault.go"},
		Checks:       "pass",
		Holds:        0,
		ReadCap:      5,
		Now:          clock,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Johnny submits valid typed DISPOSITION at exact head with SCORE 9/10
	disp := "DISPOSITION who=johnny head=aabbccddeeff00112233445566778899aabbccdd verdict=APPROVE score=9/10"
	landable, err := ProcessTypedRead(qDir, disp, clock)
	if err != nil {
		t.Fatalf("ProcessTypedRead failed: %v", err)
	}
	if landable == nil {
		t.Fatal("expected landable PR, got nil")
	}
	if landable.PR != 100 || landable.Disposition.Score != "9/10" || landable.Disposition.Who != "johnny" {
		t.Errorf("unexpected landable: %+v", landable)
	}

	// Verify landable record on disk
	lPath := filepath.Join(qDir, "landable", "pr-100.json")
	if _, err := os.Stat(lPath); err != nil {
		t.Errorf("landable record pr-100.json missing: %v", err)
	}

	// Verify continuous consumption
	list, err := ListLandablePRs(qDir)
	if err != nil || len(list) != 1 || list[0].PR != 100 {
		t.Errorf("ListLandablePRs failed: list=%+v, err=%v", list, err)
	}

	popped, err := PopLandablePR(qDir)
	if err != nil || popped == nil || popped.PR != 100 {
		t.Errorf("PopLandablePR failed: popped=%+v, err=%v", popped, err)
	}

	// After pop, landable queue should be empty
	listAfter, err := ListLandablePRs(qDir)
	if err != nil || len(listAfter) != 0 {
		t.Errorf("expected empty landable list after pop, got: %+v", listAfter)
	}
}

func TestProcessTypedRead_HOLD(t *testing.T) {
	qDir := t.TempDir()
	clock := fixedClock(t)
	headSHA := "1234123412341234123412341234123412341234"

	_, err := PipelineOnPROpen(PipelinePROpenInput{
		QueueDir:     qDir,
		Repo:         "mas-bandwidth/nova-tools",
		PR:           101,
		Head:         headSHA,
		Base:         "dev",
		Title:        "Spec update",
		TouchedFiles: []string{"internal/pulse/pipeline.go"},
		Checks:       "pass",
		Holds:        0,
		ReadCap:      5,
		Now:          clock,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Emma submits HOLD
	disp := "DISPOSITION who=emma head=1234123412341234123412341234123412341234 verdict=HOLD score=5/10"
	landable, err := ProcessTypedRead(qDir, disp, clock)
	if err != nil {
		t.Fatalf("ProcessTypedRead failed: %v", err)
	}
	if landable != nil {
		t.Errorf("expected nil landable on HOLD, got: %+v", landable)
	}

	// Check read request status
	_, req, err := findReadRequest(qDir, 101, "")
	if err != nil || req.Status != "held" {
		t.Errorf("expected held read request, got status=%s err=%v", req.Status, err)
	}

	// Ensure no landable record
	if _, err := os.Stat(filepath.Join(qDir, "landable", "pr-101.json")); !os.IsNotExist(err) {
		t.Errorf("landable record should not exist for held PR")
	}
}

func TestProcessTypedRead_GuardsAndStellaMandate(t *testing.T) {
	clock := fixedClock(t)
	headSHA := "9999888877776666555544443333222211110000"

	// Guard 1: DONE shortcut rejected
	t.Run("RejectDONEToApprovalShortcut", func(t *testing.T) {
		qDir := t.TempDir()
		_, err := PipelineOnPROpen(PipelinePROpenInput{
			QueueDir:     qDir,
			Repo:         "mas-bandwidth/nova-tools",
			PR:           200,
			Head:         headSHA,
			Base:         "dev",
			Title:        "Done test",
			TouchedFiles: []string{"internal/pulse/pipeline.go"},
			Checks:       "pass",
			Now:          clock,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Trying DONE claim instead of typed DISPOSITION verdict=APPROVE
		_, err = ProcessTypedRead(qDir, "DONE: All checks pass, approve PR", clock)
		if err == nil || !strings.Contains(err.Error(), "DONE is not an approval") {
			t.Errorf("expected DONE rejection error, got: %v", err)
		}

		_, err = ProcessTypedRead(qDir, "DISPOSITION who=emma head="+headSHA+" verdict=DONE score=9/10", clock)
		if err == nil || !strings.Contains(err.Error(), "DONE is not an approval") {
			t.Errorf("expected verdict=DONE rejection error, got: %v", err)
		}
	})

	// Guard 2: Missing friend contract clearance rejected (Stella's mandate)
	t.Run("RejectMissingFriendContractClearance", func(t *testing.T) {
		qDir := t.TempDir()
		_, err := PipelineOnPROpen(PipelinePROpenInput{
			QueueDir:     qDir,
			Repo:         "mas-bandwidth/nova-tools",
			PR:           201,
			Head:         headSHA,
			Base:         "dev",
			Title:        "Contract update",
			TouchedFiles: []string{"docs/SPEC-DECIDE.md"}, // Contract class -> requires Stella
			Checks:       "pass",
			Now:          clock,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Emma attempts to approve contract PR without Stella
		disp := "DISPOSITION who=emma head=" + headSHA + " verdict=APPROVE score=9/10"
		_, err = ProcessTypedRead(qDir, disp, clock)
		if err == nil || !strings.Contains(err.Error(), "missing friend contract disposition") {
			t.Errorf("expected missing contract disposition rejection, got: %v", err)
		}

		// Stella approves -> succeeds!
		stellaDisp := "DISPOSITION who=stella head=" + headSHA + " verdict=APPROVE score=9/10"
		l, err := ProcessTypedRead(qDir, stellaDisp, clock)
		if err != nil || l == nil {
			t.Errorf("expected Stella approval to succeed, got: %v", err)
		}
	})

	// Guard 3: Exact head mismatch rejected (carried head is unpinned)
	t.Run("RejectHeadMismatch", func(t *testing.T) {
		qDir := t.TempDir()
		_, err := PipelineOnPROpen(PipelinePROpenInput{
			QueueDir:     qDir,
			Repo:         "mas-bandwidth/nova-tools",
			PR:           202,
			Head:         headSHA,
			Base:         "dev",
			Title:        "Head mismatch",
			TouchedFiles: []string{"internal/pulse/pipeline.go"},
			Checks:       "pass",
			Now:          clock,
		})
		if err != nil {
			t.Fatal(err)
		}

		staleHead := "ffffffffffffffffffffffffffffffffffffffff"
		disp := "DISPOSITION who=emma head=" + staleHead + " verdict=APPROVE score=9/10"
		_, err = ProcessTypedRead(qDir, disp, clock)
		if err == nil {
			t.Error("expected error for head mismatch, got nil")
		}
	})

	// Guard 4: Exact-head required passes check (CI failure rejects approval)
	t.Run("RejectFailingRequiredPasses", func(t *testing.T) {
		qDir := t.TempDir()
		_, err := PipelineOnPROpen(PipelinePROpenInput{
			QueueDir:     qDir,
			Repo:         "mas-bandwidth/nova-tools",
			PR:           203,
			Head:         headSHA,
			Base:         "dev",
			Title:        "Failing checks",
			TouchedFiles: []string{"internal/pulse/pipeline.go"},
			Checks:       "red-first:fail", // checks failing at head
			Now:          clock,
		})
		if err != nil {
			t.Fatal(err)
		}

		disp := "DISPOSITION who=emma head=" + headSHA + " verdict=APPROVE score=9/10"
		_, err = ProcessTypedRead(qDir, disp, clock)
		if err == nil || !strings.Contains(err.Error(), "exact-head required passes failed") {
			t.Errorf("expected required passes failure rejection, got: %v", err)
		}
	})

	// Guard 5: Active unresolved findings / holds rejection
	t.Run("RejectActiveFindings", func(t *testing.T) {
		qDir := t.TempDir()
		_, err := PipelineOnPROpen(PipelinePROpenInput{
			QueueDir:     qDir,
			Repo:         "mas-bandwidth/nova-tools",
			PR:           204,
			Head:         headSHA,
			Base:         "dev",
			Title:        "Active findings",
			TouchedFiles: []string{"internal/pulse/pipeline.go"},
			Checks:       "pass",
			Holds:        2, // 2 active unresolved holds
			Now:          clock,
		})
		if err != nil {
			t.Fatal(err)
		}

		disp := "DISPOSITION who=emma head=" + headSHA + " verdict=APPROVE score=9/10"
		_, err = ProcessTypedRead(qDir, disp, clock)
		if err == nil || !strings.Contains(err.Error(), "active unresolved findings/holds") {
			t.Errorf("expected active findings rejection, got: %v", err)
		}
	})

	// Guard 6: Score below landing bar (SCORE 8 required)
	t.Run("RejectScoreBelowLandingBar", func(t *testing.T) {
		qDir := t.TempDir()
		_, err := PipelineOnPROpen(PipelinePROpenInput{
			QueueDir:     qDir,
			Repo:         "mas-bandwidth/nova-tools",
			PR:           205,
			Head:         headSHA,
			Base:         "dev",
			Title:        "Subpar score",
			TouchedFiles: []string{"internal/pulse/pipeline.go"},
			Checks:       "pass",
			Now:          clock,
		})
		if err != nil {
			t.Fatal(err)
		}

		disp := "DISPOSITION who=emma head=" + headSHA + " verdict=APPROVE score=7/10"
		_, err = ProcessTypedRead(qDir, disp, clock)
		if err == nil || !strings.Contains(err.Error(), "below landing bar") {
			t.Errorf("expected sub-8 score rejection, got: %v", err)
		}
	})
}

func TestBackpressure_DealerSlowsWhenOverCap(t *testing.T) {
	qDir := t.TempDir()
	clock := fixedClock(t)

	capLimit := 2
	// Open 3 PRs (pending reads = 3 > cap of 2)
	for i := 1; i <= 3; i++ {
		sha := strings.Repeat(string(rune('0'+i)), 40)
		res, err := PipelineOnPROpen(PipelinePROpenInput{
			QueueDir:     qDir,
			Repo:         "mas-bandwidth/nova-tools",
			PR:           300 + i,
			Head:         sha,
			Base:         "dev",
			Title:        "PR",
			TouchedFiles: []string{"internal/pulse/pipeline.go"},
			Checks:       "pass",
			ReadCap:      capLimit,
			Now:          clock,
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 3 && !res.Backpressure {
			t.Errorf("expected backpressure active on 3rd PR (cap=2)")
		}
	}

	// Verify BACKPRESSURE file exists in queue/control/
	bpPath := filepath.Join(qDir, "control", "BACKPRESSURE")
	bpContent, err := os.ReadFile(bpPath)
	if err != nil {
		t.Fatalf("expected BACKPRESSURE file to exist: %v", err)
	}
	if !strings.Contains(string(bpContent), "BACKPRESSURE pending=3 cap=2") {
		t.Errorf("unexpected BACKPRESSURE content: %s", string(bpContent))
	}

	// Test Dealer slowing in fillTick:
	// Create mock fill input pointing to qDir
	readyDir := filepath.Join(qDir, "ready")
	launchedDir := filepath.Join(qDir, "launched")
	_ = os.MkdirAll(readyDir, 0o755)
	_ = os.MkdirAll(launchedDir, 0o755)

	// Put 5 ready cards
	for i := 1; i <= 5; i++ {
		cFile := filepath.Join(readyDir, fmt.Sprintf("card-%03d.md", i))
		_ = os.WriteFile(cFile, []byte("RESULT: CARD\n"), 0o644)
	}

	var stderrBuf bytes.Buffer
	fillIn := FillInput{
		Ready:    readyDir,
		Launched: launchedDir,
		Queue:    qDir,
		Benches:  []string{"hulk"},
		Capacity: laneCap{"hulk": 10}, // wants 10 cards normally
		Launcher: &laneLauncher{},
		Stderr:   &stderrBuf,
	}

	// Run fillTick
	lines, _ := fillTick(fillIn, 1)

	// Under backpressure, dealer must slow: want clamped to 1 card!
	if !strings.Contains(stderrBuf.String(), "FILL BACKPRESSURE") {
		t.Errorf("expected FILL BACKPRESSURE on stderr, got: %s", stderrBuf.String())
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "hulk:launched=1") {
		t.Errorf("expected dealer slowed to 1 card launched on hulk, got: %v", lines)
	}

	// Now approve 2 PRs to bring pending reads down to 1 (<= capLimit of 2)
	for i := 1; i <= 2; i++ {
		sha := strings.Repeat(string(rune('0'+i)), 40)
		dispLine := fmt.Sprintf("DISPOSITION who=emma head=%s verdict=APPROVE score=9/10 pr=%d", sha, 300+i)
		_, err := ProcessTypedRead(qDir, dispLine, clock)
		if err != nil {
			t.Fatalf("approval of PR %d failed: %v", 300+i, err)
		}
	}

	// Check backpressure update
	active, pending, err := UpdateBackpressure(qDir, capLimit)
	if err != nil {
		t.Fatal(err)
	}
	if active || pending > capLimit {
		t.Errorf("expected backpressure cleared (pending=%d, cap=%d)", pending, capLimit)
	}
	if _, err := os.Stat(bpPath); !os.IsNotExist(err) {
		t.Errorf("BACKPRESSURE file should be removed when under cap")
	}
}
