package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func capture(args []string, now time.Time) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, now)
	return code, stdout.String(), stderr.String()
}

func TestVersion(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"version", "--version"} {
		code, out, errOut := capture([]string{arg}, time.Now())
		if code != 0 {
			t.Errorf("%s: expected exit 0, got %d (stderr: %s)", arg, code, errOut)
		}
		if !strings.Contains(out, "nova-sprint") {
			t.Errorf("%s: expected nova-sprint in output, got %q", arg, out)
		}
	}
}

func TestVersion_UnexpectedArg(t *testing.T) {
	t.Parallel()
	code, _, errOut := capture([]string{"version", "extra"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "takes no flags and no arguments") {
		t.Errorf("expected takes no flags error, got %q", errOut)
	}
}

func TestHelp(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"help", "-h", "--help"} {
		code, out, errOut := capture([]string{arg}, time.Now())
		if code != 0 {
			t.Errorf("%s: expected exit 0, got %d (stderr: %s)", arg, code, errOut)
		}
		if !strings.Contains(out, "nova-sprint:") || !strings.Contains(out, "usage:") {
			t.Errorf("%s: expected usage text, got %q", arg, out)
		}
	}
}

func TestNoCommand(t *testing.T) {
	t.Parallel()
	code, _, errOut := capture(nil, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "no command given") {
		t.Errorf("expected no command given error, got %q", errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	code, _, errOut := capture([]string{"frobnicate"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("expected unknown command error, got %q", errOut)
	}
}

func TestInit_MissingDir(t *testing.T) {
	t.Parallel()
	code, _, errOut := capture([]string{"init"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "--dir is required") {
		t.Errorf("expected missing --dir error, got %q", errOut)
	}
}

func TestInit_UnexpectedArg(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	code, _, errOut := capture([]string{"init", "--dir", dir, "extra"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("expected unexpected argument error, got %q", errOut)
	}
}

func TestInit_Success(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	code, out, errOut := capture([]string{"init", "--dir", dir}, now)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, errOut)
	}

	if !strings.Contains(out, "SPRINT OK init dir=") || !strings.Contains(out, "journal=journal.nsjr") {
		t.Errorf("unexpected stdout: %q", out)
	}

	jPath := filepath.Join(dir, JournalFileName)
	if _, err := os.Stat(jPath); err != nil {
		t.Fatalf("journal file not created: %v", err)
	}

	mPath := filepath.Join(dir, MetaFileName)
	if _, err := os.Stat(mPath); err != nil {
		t.Fatalf("meta file not created: %v", err)
	}
}

func TestInit_AlreadyInitialized(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Now()
	code, _, _ := capture([]string{"init", "--dir", dir}, now)
	if code != 0 {
		t.Fatalf("first init failed: %d", code)
	}

	// Second init on same directory must refuse with exit 1
	code2, _, errOut2 := capture([]string{"init", "--dir", dir}, now)
	if code2 != 1 {
		t.Fatalf("expected exit 1 on second init, got %d", code2)
	}
	if !strings.Contains(errOut2, "SPRINT FAIL init") || !strings.Contains(errOut2, "already initialized") {
		t.Errorf("expected already initialized refusal, got %q", errOut2)
	}
}

func TestStatus_MissingDir(t *testing.T) {
	t.Parallel()
	code, _, errOut := capture([]string{"status"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "--dir is required") {
		t.Errorf("expected missing --dir error, got %q", errOut)
	}
}

func TestStatus_NegativeMax(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	code, _, errOut := capture([]string{"status", "--dir", dir, "--max", "-5"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "--max must be >= 0") {
		t.Errorf("expected negative max error, got %q", errOut)
	}
}

func TestStatus_NotInitialized(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint-nonexistent")
	code, _, errOut := capture([]string{"status", "--dir", dir}, time.Now())
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "SPRINT FAIL status") || !strings.Contains(errOut, "not initialized") {
		t.Errorf("expected not initialized error, got %q", errOut)
	}
}

func TestStatus_UnexpectedArg(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	code, _, errOut := capture([]string{"status", "--dir", dir, "extra"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("expected unexpected argument error, got %q", errOut)
	}
}

func TestStatus_EmptyJournal(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Now()
	capture([]string{"init", "--dir", dir}, now)

	code, out, errOut := capture([]string{"status", "--dir", dir}, now)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, errOut)
	}
	if !strings.Contains(out, "SPRINT OK status") || !strings.Contains(out, "frames=0") || !strings.Contains(out, "bytes=0") {
		t.Errorf("unexpected status output: %q", out)
	}
}

func TestStep_MissingArgs(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	code, _, errOut := capture([]string{"step", "--dir", dir}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2 on missing action, got %d", code)
	}
	if !strings.Contains(errOut, "--dir and --action are required") {
		t.Errorf("expected missing arg error, got %q", errOut)
	}

	code2, _, errOut2 := capture([]string{"step", "--action", `{"type":"test"}`}, time.Now())
	if code2 != 2 {
		t.Errorf("expected exit 2 on missing dir, got %d", code2)
	}
	if !strings.Contains(errOut2, "--dir and --action are required") {
		t.Errorf("expected missing arg error, got %q", errOut2)
	}
}

func TestStep_UnexpectedArg(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	code, _, errOut := capture([]string{"step", "--dir", dir, "--action", `{"type":"test"}`, "extra"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("expected unexpected argument error, got %q", errOut)
	}
}

func TestStep_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	capture([]string{"init", "--dir", dir}, time.Now())

	code, _, errOut := capture([]string{"step", "--dir", dir, "--action", "{not-valid-json"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2 on malformed JSON, got %d", code)
	}
	if !strings.Contains(errOut, "must be valid JSON") {
		t.Errorf("expected valid JSON error, got %q", errOut)
	}
}

func TestStep_NotInitialized(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint-nonexistent")
	code, _, errOut := capture([]string{"step", "--dir", dir, "--action", `{"type":"push"}`}, time.Now())
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "SPRINT FAIL step") || !strings.Contains(errOut, "not initialized") {
		t.Errorf("expected not initialized refusal, got %q", errOut)
	}
}

func TestStep_Success(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Now()
	capture([]string{"init", "--dir", dir}, now)

	code, out, errOut := capture([]string{"step", "--dir", dir, "--action", `{"type":"push","card":"c1","stream":"core"}`}, now)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, errOut)
	}
	if !strings.Contains(out, "SPRINT OK step") || !strings.Contains(out, "seq=1") || !strings.Contains(out, "offset=0") {
		t.Errorf("unexpected step output: %q", out)
	}
}

func TestLifecycle_FullSequence(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)

	// 1. init
	cInit, oInit, eInit := capture([]string{"init", "--dir", dir}, now)
	if cInit != 0 {
		t.Fatalf("init failed: %d (stderr: %s)", cInit, eInit)
	}
	if !strings.Contains(oInit, "SPRINT OK init") {
		t.Errorf("init output mismatch: %s", oInit)
	}

	// 2. step action 1
	c1, o1, e1 := capture([]string{"step", "--dir", dir, "--action", `{"type":"push","card":"c1","stream":"core"}`}, now)
	if c1 != 0 {
		t.Fatalf("step 1 failed: %d (stderr: %s)", c1, e1)
	}
	if !strings.Contains(o1, "seq=1") {
		t.Errorf("step 1 output mismatch: %s", o1)
	}

	// 3. step action 2
	c2, o2, e2 := capture([]string{"step", "--dir", dir, "--action", `{"type":"deal","card":"c1","consumer":"bench-1"}`}, now)
	if c2 != 0 {
		t.Fatalf("step 2 failed: %d (stderr: %s)", c2, e2)
	}
	if !strings.Contains(o2, "seq=2") {
		t.Errorf("step 2 output mismatch: %s", o2)
	}

	// 4. step action 3
	c3, o3, e3 := capture([]string{"step", "--dir", dir, "--action", `{"type":"land","card":"c1"}`}, now)
	if c3 != 0 {
		t.Fatalf("step 3 failed: %d (stderr: %s)", c3, e3)
	}
	if !strings.Contains(o3, "seq=3") {
		t.Errorf("step 3 output mismatch: %s", o3)
	}

	// 5. status
	cStat, oStat, eStat := capture([]string{"status", "--dir", dir}, now)
	if cStat != 0 {
		t.Fatalf("status failed: %d (stderr: %s)", cStat, eStat)
	}
	if !strings.Contains(oStat, "frames=3") || !strings.Contains(oStat, "last_seq=3") {
		t.Errorf("status summary mismatch: %s", oStat)
	}
	if !strings.Contains(oStat, "seq=1") || !strings.Contains(oStat, "seq=2") || !strings.Contains(oStat, "seq=3") {
		t.Errorf("status listing mismatch: %s", oStat)
	}

	// 6. replay
	cRep, oRep, eRep := capture([]string{"replay", "--dir", dir}, now)
	if cRep != 0 {
		t.Fatalf("replay failed: %d (stderr: %s)", cRep, eRep)
	}
	if !strings.Contains(oRep, "SPRINT OK replay") || !strings.Contains(oRep, "frames=3") || !strings.Contains(oRep, "verified=true") {
		t.Errorf("replay output mismatch: %s", oRep)
	}

	// 7. check
	cChk, oChk, eChk := capture([]string{"check", "--dir", dir}, now)
	if cChk != 0 {
		t.Fatalf("check failed: %d (stderr: %s)", cChk, eChk)
	}
	if !strings.Contains(oChk, "SPRINT OK check") || !strings.Contains(oChk, "frames=3") || !strings.Contains(oChk, "intact=true") {
		t.Errorf("check output mismatch: %s", oChk)
	}
}

func TestStatus_BoundedMax(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Now()
	capture([]string{"init", "--dir", dir}, now)

	for i := 1; i <= 25; i++ {
		payload := fmt.Sprintf(`{"type":"event","step":%d}`, i)
		capture([]string{"step", "--dir", dir, "--action", payload}, now)
	}

	// Capped status with --max 5
	code, out, errOut := capture([]string{"status", "--dir", dir, "--max", "5"}, now)
	if code != 0 {
		t.Fatalf("status failed: %d (stderr: %s)", code, errOut)
	}
	if !strings.Contains(out, "frames=25") {
		t.Errorf("expected total frames=25, got %s", out)
	}
	if !strings.Contains(out, "SPRINT MORE kind=frames shown=5 total=25") {
		t.Errorf("expected SPRINT MORE line, got %s", out)
	}

	// Uncapped status with --max 0
	code0, out0, errOut0 := capture([]string{"status", "--dir", dir, "--max", "0"}, now)
	if code0 != 0 {
		t.Fatalf("status --max 0 failed: %d (stderr: %s)", code0, errOut0)
	}
	if strings.Contains(out0, "SPRINT MORE") {
		t.Errorf("expected no SPRINT MORE with --max 0, got %s", out0)
	}
	if !strings.Contains(out0, "seq=25") {
		t.Errorf("expected all 25 frames listed, got %s", out0)
	}
}

func TestReplay_MissingDir(t *testing.T) {
	t.Parallel()
	code, _, errOut := capture([]string{"replay"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2 on missing dir, got %d", code)
	}
	if !strings.Contains(errOut, "--dir is required") {
		t.Errorf("expected missing --dir error, got %q", errOut)
	}
}

func TestReplay_UnexpectedArg(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	code, _, errOut := capture([]string{"replay", "--dir", dir, "extra"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("expected unexpected argument error, got %q", errOut)
	}
}

func TestReplay_NotInitialized(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint-nonexistent")
	code, _, errOut := capture([]string{"replay", "--dir", dir}, time.Now())
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "SPRINT FAIL replay") || !strings.Contains(errOut, "not initialized") {
		t.Errorf("expected not initialized error, got %q", errOut)
	}
}

func TestCheck_MissingDir(t *testing.T) {
	t.Parallel()
	code, _, errOut := capture([]string{"check"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2 on missing dir, got %d", code)
	}
	if !strings.Contains(errOut, "--dir is required") {
		t.Errorf("expected missing --dir error, got %q", errOut)
	}
}

func TestCheck_UnexpectedArg(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	code, _, errOut := capture([]string{"check", "--dir", dir, "extra"}, time.Now())
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("expected unexpected argument error, got %q", errOut)
	}
}

func TestCheck_NotInitialized(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint-nonexistent")
	code, _, errOut := capture([]string{"check", "--dir", dir}, time.Now())
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "SPRINT FAIL check") || !strings.Contains(errOut, "not initialized") {
		t.Errorf("expected not initialized error, got %q", errOut)
	}
}

func TestReplay_CorruptedJournal(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Now()
	capture([]string{"init", "--dir", dir}, now)

	for i := 1; i <= 3; i++ {
		payload := fmt.Sprintf(`{"type":"action-%d"}`, i)
		capture([]string{"step", "--dir", dir, "--action", payload}, now)
	}

	jPath := filepath.Join(dir, JournalFileName)
	data, err := os.ReadFile(jPath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}

	// Corrupt a byte in the second frame (around offset 40)
	if len(data) > 50 {
		data[45] ^= 0xFF
	}
	if err := os.WriteFile(jPath, data, 0644); err != nil {
		t.Fatalf("write corrupted journal: %v", err)
	}

	// Replay must detect corruption and exit 1
	code, _, errOut := capture([]string{"replay", "--dir", dir}, now)
	if code != 1 {
		t.Errorf("expected exit 1 on corrupt journal replay, got %d", code)
	}
	if !strings.Contains(errOut, "SPRINT FAIL replay") || !strings.Contains(errOut, "verification failed") {
		t.Errorf("expected verification failed error, got %q", errOut)
	}

	// Check must also fail on corrupt journal
	codeChk, _, errOutChk := capture([]string{"check", "--dir", dir}, now)
	if codeChk != 1 {
		t.Errorf("expected exit 1 on check of corrupt journal, got %d", codeChk)
	}
	if !strings.Contains(errOutChk, "SPRINT FAIL check") || !strings.Contains(errOutChk, "corrupted") {
		t.Errorf("expected check corrupted error, got %q", errOutChk)
	}
}

func TestSingleLineOutputGuarantee(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sprint")
	now := time.Now()
	capture([]string{"init", "--dir", dir}, now)

	// Action with formatted multi-line JSON containing literal newlines and tabs
	uglyPayload := "{\n\t\"type\": \"weird\",\n\t\"note\": \"line1\\nline2\"\n}"
	code, out, errOut := capture([]string{"step", "--dir", dir, "--action", uglyPayload}, now)
	if code != 0 {
		t.Fatalf("step failed: %d (stderr: %s)", code, errOut)
	}

	// Step output must be strictly 1 line
	stepLines := strings.Split(strings.TrimSpace(out), "\n")
	if len(stepLines) != 1 {
		t.Errorf("expected step output to be exactly 1 line, got %d: %q", len(stepLines), out)
	}

	// Status output must have exactly 1 summary line + 1 frame line (total 2 lines)
	codeStat, outStat, errStat := capture([]string{"status", "--dir", dir}, now)
	if codeStat != 0 {
		t.Fatalf("status failed: %d (stderr: %s)", codeStat, errStat)
	}
	statLines := strings.Split(strings.TrimSpace(outStat), "\n")
	if len(statLines) != 2 {
		t.Errorf("expected status output to be exactly 2 lines (summary + 1 frame), got %d: %q", len(statLines), outStat)
	}
}
