package adoption

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVerifyDaemonAdoptionViaProbe(t *testing.T) {
	ev := DaemonEvidence{
		Bench:      "batman",
		Daemon:     "nova-swarm",
		DiskSHA:    "86abcf23dd6cd95668ae1a865e11e29556996b03",
		RunningSHA: "86abcf23dd6cd95668ae1a865e11e29556996b03",
		Kind:       EvidenceProbe,
		ReportedAt: time.Now(),
	}

	if err := VerifyDaemonAdoption(ev, "86abcf23dd6cd95668ae1a865e11e29556996b03"); err != nil {
		t.Fatalf("expected adoption verified via probe, got error: %v", err)
	}

	if !ev.IsAdopted("86abcf23dd6cd95668ae1a865e11e29556996b03") {
		t.Fatal("expected IsAdopted to report true")
	}
}

func TestVerifyDaemonAdoptionViaHeartbeat(t *testing.T) {
	ev := DaemonEvidence{
		Bench:      "superman",
		Daemon:     "nova-pulse",
		DiskSHA:    "94b5ca53112233445566778899aabbccddeeff00",
		RunningSHA: "94b5ca53112233445566778899aabbccddeeff00",
		Kind:       EvidenceHeartbeat,
		ReportedAt: time.Now(),
	}

	if err := VerifyDaemonAdoption(ev, "94b5ca53112233445566778899aabbccddeeff00"); err != nil {
		t.Fatalf("expected adoption verified via heartbeat, got error: %v", err)
	}
}

func TestVerifyDaemonAdoptionRefusesMismatchInstalledNotAdopted(t *testing.T) {
	// Disk has new binary installed, but daemon is still executing old SHA!
	ev := DaemonEvidence{
		Bench:      "batman",
		Daemon:     "nova-swarm",
		DiskSHA:    "86abcf23dd6cd95668ae1a865e11e29556996b03",
		RunningSHA: "1fbb5e20112233445566778899aabbccddeeff00", // stale running process!
		Kind:       EvidenceProbe,
		ReportedAt: time.Now(),
	}

	err := VerifyDaemonAdoption(ev, "86abcf23dd6cd95668ae1a865e11e29556996b03")
	if err == nil {
		t.Fatal("expected adoption verification to fail when RUNNING_SHA != DISK_SHA")
	}

	if !errors.Is(err, ErrInstalledNotAdopted) {
		t.Fatalf("expected ErrInstalledNotAdopted, got: %v", err)
	}

	if !strings.Contains(err.Error(), InvariantInstalledNotAdopted) {
		t.Fatalf("expected error to quote %q, got: %v", InvariantInstalledNotAdopted, err)
	}

	if ev.IsAdopted("86abcf23dd6cd95668ae1a865e11e29556996b03") {
		t.Fatal("IsAdopted must be false when daemon is running older binary")
	}
}

func TestVerifyDaemonAdoptionDaemonNotRunning(t *testing.T) {
	ev := DaemonEvidence{
		Bench:      "batman",
		Daemon:     "nova-swarm",
		DiskSHA:    "86abcf23dd6cd95668ae1a865e11e29556996b03",
		RunningSHA: "", // Daemon not running or probe failed to return SHA
		Kind:       EvidenceProbe,
		ReportedAt: time.Now(),
	}

	err := VerifyDaemonAdoption(ev, "86abcf23dd6cd95668ae1a865e11e29556996b03")
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	if !errors.Is(err, ErrDaemonNotRunning) {
		t.Fatalf("expected ErrDaemonNotRunning, got: %v", err)
	}
}

func TestVerifyDaemonAdoptionTargetMismatch(t *testing.T) {
	ev := DaemonEvidence{
		Bench:      "batman",
		Daemon:     "nova-swarm",
		DiskSHA:    "oldsha00112233445566778899aabbccddeeff",
		RunningSHA: "oldsha00112233445566778899aabbccddeeff",
		Kind:       EvidenceProbe,
		ReportedAt: time.Now(),
	}

	err := VerifyDaemonAdoption(ev, "newtarget00112233445566778899aabbccddeeff")
	if err == nil {
		t.Fatal("expected error when disk SHA does not match target")
	}
	if !errors.Is(err, ErrTargetMismatch) {
		t.Fatalf("expected ErrTargetMismatch, got: %v", err)
	}
}

func TestVerifyDaemonAdoptionUnrecognizedEvidence(t *testing.T) {
	ev := DaemonEvidence{
		Bench:      "batman",
		Daemon:     "nova-swarm",
		DiskSHA:    "86abcf23dd6cd95668ae1a865e11e29556996b03",
		RunningSHA: "86abcf23dd6cd95668ae1a865e11e29556996b03",
		Kind:       "unverified-manual-text",
		ReportedAt: time.Now(),
	}

	err := VerifyDaemonAdoption(ev, "86abcf23dd6cd95668ae1a865e11e29556996b03")
	if err == nil {
		t.Fatal("expected error on unrecognized evidence channel")
	}
	if !errors.Is(err, ErrUnrecognizedEvidence) {
		t.Fatalf("expected ErrUnrecognizedEvidence, got: %v", err)
	}
}

func TestHeartbeatIsStale(t *testing.T) {
	now := time.Now()
	evFresh := DaemonEvidence{
		Kind:       EvidenceHeartbeat,
		ReportedAt: now.Add(-1 * time.Minute),
	}
	if evFresh.IsStale(now, 5*time.Minute) {
		t.Fatal("expected 1-minute-old heartbeat to not be stale under 5m bound")
	}

	evStale := DaemonEvidence{
		Kind:       EvidenceHeartbeat,
		ReportedAt: now.Add(-10 * time.Minute),
	}
	if !evStale.IsStale(now, 5*time.Minute) {
		t.Fatal("expected 10-minute-old heartbeat to be stale under 5m bound")
	}

	// Probe is never considered stale via heartbeat timer
	evProbe := DaemonEvidence{
		Kind:       EvidenceProbe,
		ReportedAt: now.Add(-10 * time.Minute),
	}
	if evProbe.IsStale(now, 5*time.Minute) {
		t.Fatal("probe should not report stale via heartbeat check")
	}
}

func TestComputeStageProgression(t *testing.T) {
	const (
		shaPR  = "a1b2c3d4e5f600112233445566778899aabbccdd"
		shaDev = "a1b2c3d4e5f600112233445566778899aabbccdd"
	)

	rec := Record{
		Tool:      "nova-pulse",
		Verb:      "adoption",
		PR:        2068,
		Issues:    []int{2068},
		Owner:     "Emma",
		Readers:   []string{"Stella"},
		LiveHead:  shaPR,
		TargetSHA: shaDev,
	}

	// 1. Stage 0: PR not open
	rec.PROpen = false
	stage, _, nextAct, owed := rec.ComputeStage([]string{"batman"})
	if stage != StageNone || stage.Percent() != 0 {
		t.Fatalf("expected StageNone (0%%), got %v", stage)
	}
	if nextAct != "open PR" || owed != "Emma" {
		t.Fatalf("unexpected nextAct=%q owed=%q", nextAct, owed)
	}

	// 2. Stage 25: PR open, CI not yet green
	rec.PROpen = true
	rec.Green = false
	stage, _, nextAct, owed = rec.ComputeStage([]string{"batman"})
	if stage != StagePROpen || stage.Percent() != 25 {
		t.Fatalf("expected StagePROpen (25%%), got %v", stage)
	}
	if nextAct != "green CI and pass hygiene" || owed != "Emma" {
		t.Fatalf("unexpected nextAct=%q owed=%q", nextAct, owed)
	}

	// 3. Stage 50: Green, awaiting required review approval
	rec.Green = true
	rec.Approvals = map[string]string{}
	stage, _, nextAct, owed = rec.ComputeStage([]string{"batman"})
	if stage != StageGreen || stage.Percent() != 50 {
		t.Fatalf("expected StageGreen (50%%), got %v", stage)
	}
	if !strings.Contains(nextAct, "review and approve") || owed != "Stella" {
		t.Fatalf("unexpected nextAct=%q owed=%q", nextAct, owed)
	}

	// 4. Stage 75: Approved by Stella, not landed on dev
	rec.Approvals = map[string]string{"Stella": shaPR}
	rec.OnDev = false
	stage, _, nextAct, owed = rec.ComputeStage([]string{"batman"})
	if stage != StageApproved || stage.Percent() != 75 {
		t.Fatalf("expected StageApproved (75%%), got %v", stage)
	}
	if nextAct != "land on dev via merge queue" || owed != "coordinator" {
		t.Fatalf("unexpected nextAct=%q owed=%q", nextAct, owed)
	}

	// 5. Landed on dev, but binary not yet installed on disk
	rec.OnDev = true
	rec.Fleet = map[string]DaemonEvidence{
		"batman": {
			Bench:      "batman",
			Daemon:     "nova-pulse",
			DiskSHA:    "older-sha-on-disk",
			RunningSHA: "older-sha-on-disk",
			Kind:       EvidenceProbe,
		},
	}
	stage, _, nextAct, owed = rec.ComputeStage([]string{"batman"})
	if stage != StageApproved {
		t.Fatalf("expected StageApproved while disk install pending, got %v", stage)
	}
	if !strings.Contains(nextAct, "install build") || owed != "operator" {
		t.Fatalf("unexpected nextAct=%q owed=%q", nextAct, owed)
	}

	// 6. Stage 90: "INSTALLED IS NOT ADOPTED"
	// Disk SHA updated, but daemon is still running old binary!
	rec.Fleet["batman"] = DaemonEvidence{
		Bench:      "batman",
		Daemon:     "nova-pulse",
		DiskSHA:    shaDev,
		RunningSHA: "older-sha-still-running", // Not restarted!
		Kind:       EvidenceProbe,
		ReportedAt: time.Now(),
	}
	stage, status, nextAct, owed := rec.ComputeStage([]string{"batman"})
	if stage != StageInstalled || stage.Percent() != 90 {
		t.Fatalf("expected StageInstalled (90%%), got %v", stage)
	}
	if !strings.Contains(status, "unadopted on batman") {
		t.Fatalf("expected status to mention unadopted, got %q", status)
	}
	if !strings.Contains(nextAct, InvariantInstalledNotAdopted) {
		t.Fatalf("expected nextAct to declare %q, got: %q", InvariantInstalledNotAdopted, nextAct)
	}
	if owed != "operator" {
		t.Fatalf("expected owed by operator, got %q", owed)
	}

	// 7. Stage 90 with daemon running matched SHA, but operational HOLD from friend
	rec.Fleet["batman"] = DaemonEvidence{
		Bench:      "batman",
		Daemon:     "nova-pulse",
		DiskSHA:    shaDev,
		RunningSHA: shaDev,
		Kind:       EvidenceProbe,
		ReportedAt: time.Now(),
	}
	rec.Holds = map[string]string{"Johnny": "investigating edge case on timeout"}
	stage, status, nextAct, owed = rec.ComputeStage([]string{"batman"})
	if stage != StageInstalled {
		t.Fatalf("expected StageInstalled (held), got %v", stage)
	}
	if !strings.Contains(status, "operational hold") || !strings.Contains(nextAct, "resolve hold") || owed != "Emma" {
		t.Fatalf("unexpected hold handling: status=%q nextAct=%q owed=%q", status, nextAct, owed)
	}

	// 8. Stage 90 with hold cleared, but missing real execution receipt
	rec.Holds = nil
	rec.Receipts = nil
	stage, status, nextAct, owed = rec.ComputeStage([]string{"batman"})
	if stage != StageInstalled {
		t.Fatalf("expected StageInstalled (receipt pending), got %v", stage)
	}
	if !strings.Contains(nextAct, "post receipt") || owed != "Emma" {
		t.Fatalf("unexpected receipt handling: status=%q nextAct=%q owed=%q", status, nextAct, owed)
	}

	// 9. Stage 100: IN USE
	// Daemon verified matching disk, real run receipt present, no holds!
	rec.Receipts = []string{"pulse-adoption-batman-pass.tsv"}
	stage, status, nextAct, owed = rec.ComputeStage([]string{"batman"})
	if stage != StageInUse || stage.Percent() != 100 {
		t.Fatalf("expected StageInUse (100%%), got %v", stage)
	}
	if nextAct != "none (adopted)" || owed != "-" {
		t.Fatalf("unexpected nextAct=%q owed=%q", nextAct, owed)
	}
	if status != "adopted and in use" {
		t.Fatalf("unexpected status=%q", status)
	}
}

func TestMultiBenchFleetAdoptionRequiresAllBenches(t *testing.T) {
	const target = "86abcf23dd6cd95668ae1a865e11e29556996b03"

	rec := Record{
		Tool:      "nova-swarm",
		Verb:      "fleet",
		PR:        2046,
		Owner:     "Rowan",
		PROpen:    true,
		Green:     true,
		OnDev:     true,
		TargetSHA: target,
		Receipts:  []string{"fleet-probe-ok.receipt"},
		Fleet: map[string]DaemonEvidence{
			"batman": {
				Bench:      "batman",
				Daemon:     "nova-swarm",
				DiskSHA:    target,
				RunningSHA: target, // Restarted & adopted
				Kind:       EvidenceProbe,
			},
			"superman": {
				Bench:      "superman",
				Daemon:     "nova-swarm",
				DiskSHA:    target,
				RunningSHA: "old-running-sha", // Installed on disk, but old daemon still running!
				Kind:       EvidenceProbe,
			},
		},
	}

	benches := []string{"batman", "superman"}
	stage, _, nextAct, owed := rec.ComputeStage(benches)

	// Superman is not adopted, so overall stage must not be 100%
	if stage != StageInstalled || stage.Percent() != 90 {
		t.Fatalf("expected stage 90%% when one bench daemon is unadopted, got %v", stage)
	}
	if !strings.Contains(nextAct, "superman") {
		t.Fatalf("expected nextAct to call out superman, got %q", nextAct)
	}
	if owed != "operator" {
		t.Fatalf("expected operator to owe daemon restart, got %q", owed)
	}

	// Now update superman's daemon evidence
	rec.Fleet["superman"] = DaemonEvidence{
		Bench:      "superman",
		Daemon:     "nova-swarm",
		DiskSHA:    target,
		RunningSHA: target,
		Kind:       EvidenceHeartbeat,
	}

	stage, _, nextAct, owed = rec.ComputeStage(benches)
	if stage != StageInUse || stage.Percent() != 100 {
		t.Fatalf("expected StageInUse (100%%) once all benches adopt, got %v", stage)
	}
	if nextAct != "none (adopted)" {
		t.Fatalf("expected nextAct 'none (adopted)', got %q", nextAct)
	}
}

func TestLedgerSummarize(t *testing.T) {
	const target = "86abcf23"
	ledger := NewLedger()

	// Row 1: Adopted
	ledger.AddRecord(Record{
		Tool:      "nova-pulse",
		Verb:      "adoption",
		PROpen:    true,
		Green:     true,
		OnDev:     true,
		TargetSHA: target,
		Receipts:  []string{"receipt-1"},
		Fleet: map[string]DaemonEvidence{
			"batman": {Bench: "batman", DiskSHA: target, RunningSHA: target, Kind: EvidenceProbe},
		},
	})

	// Row 2: Installed on disk, unadopted daemon
	ledger.AddRecord(Record{
		Tool:      "nova-swarm",
		Verb:      "slots",
		PROpen:    true,
		Green:     true,
		OnDev:     true,
		TargetSHA: target,
		Fleet: map[string]DaemonEvidence{
			"batman": {Bench: "batman", DiskSHA: target, RunningSHA: "stale-sha", Kind: EvidenceProbe},
		},
	})

	// Row 3: PR Open
	ledger.AddRecord(Record{
		Tool:   "nova-ci",
		Verb:   "wall",
		PROpen: true,
		Green:  false,
	})

	summary := ledger.Summarize([]string{"batman"})
	if summary.TotalRows != 3 {
		t.Fatalf("expected 3 total rows, got %d", summary.TotalRows)
	}
	if summary.InUseCount != 1 {
		t.Fatalf("expected 1 in-use row, got %d", summary.InUseCount)
	}
	if summary.InstalledCount != 1 {
		t.Fatalf("expected 1 installed row, got %d", summary.InstalledCount)
	}
	if summary.PROpenCount != 1 {
		t.Fatalf("expected 1 pr-open row, got %d", summary.PROpenCount)
	}
	if summary.UnadoptedDaemons != 1 {
		t.Fatalf("expected 1 unadopted daemon, got %d", summary.UnadoptedDaemons)
	}
}

func TestLedgerRenderTable(t *testing.T) {
	const target = "86abcf23dd6c"
	ledger := NewLedger()
	ledger.AddRecord(Record{
		Tool:      "nova-pulse",
		Verb:      "adoption",
		PR:        2068,
		Owner:     "Emma",
		PROpen:    true,
		Green:     true,
		OnDev:     true,
		TargetSHA: target,
		Fleet: map[string]DaemonEvidence{
			"batman": {Bench: "batman", DiskSHA: target, RunningSHA: "stale-sha", Kind: EvidenceProbe},
		},
	})

	var buf bytes.Buffer
	if err := ledger.RenderTable(&buf, []string{"batman"}); err != nil {
		t.Fatalf("RenderTable error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "nova-pulse adoption") {
		t.Errorf("output missing tool verb:\n%s", out)
	}
	if !strings.Contains(out, "90%") {
		t.Errorf("output missing 90%% stage:\n%s", out)
	}
	if !strings.Contains(out, "86abcf23") {
		t.Errorf("output missing disk sha:\n%s", out)
	}
}

func TestTSVRoundtrip(t *testing.T) {
	const target = "86abcf23dd6cd95668ae1a865e11e29556996b03"
	ledger := NewLedger()
	ledger.AddRecord(Record{
		Tool:      "nova-pulse",
		Verb:      "adoption",
		PR:        2068,
		Owner:     "Emma",
		PROpen:    true,
		Green:     true,
		OnDev:     true,
		TargetSHA: target,
		Fleet: map[string]DaemonEvidence{
			"batman": {
				Bench:      "batman",
				DiskSHA:    target,
				RunningSHA: target,
				Kind:       EvidenceHeartbeat,
			},
		},
		Receipts: []string{"receipt-pulse.tsv"},
	})

	var buf bytes.Buffer
	if err := ledger.RenderTSV(&buf, []string{"batman"}); err != nil {
		t.Fatalf("RenderTSV error: %v", err)
	}

	rows, err := LoadTSV(&buf)
	if err != nil {
		t.Fatalf("LoadTSV error: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Tool != "nova-pulse" || r.Verb != "adoption" || r.PR != 2068 || r.Stage != 100 {
		t.Fatalf("unexpected loaded row: %+v", r)
	}
	if r.DiskSHA != target || r.RunningSHA != target {
		t.Fatalf("unexpected SHAs in row: disk=%s running=%s", r.DiskSHA, r.RunningSHA)
	}
}
