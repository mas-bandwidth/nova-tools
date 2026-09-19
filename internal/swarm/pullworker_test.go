package swarm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
)

// plantWorkerTestCard puts one <name>.card in store/queue/.
func plantWorkerTestCard(t *testing.T, store, name, body string) string {
	t.Helper()
	queue := filepath.Join(store, queueDirName)
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	path := filepath.Join(queue, name+".card")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write card: %v", err)
	}
	return path
}

func writeWorkerTestStore(t *testing.T, shares string) string {
	t.Helper()
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte(shares), 0o644); err != nil {
		t.Fatalf("write shares.tsv: %v", err)
	}
	return store
}

// TestWorkerRunsCardAndHarvestsOutcome: A worker takes a card under a lease, runs it,
// heartbeats it, writes outcome to harvest/ before releasing the lease, and cleans up.
func TestWorkerRunsCardAndHarvestsOutcome(t *testing.T) {
	store := writeWorkerTestStore(t, "capacity\t2\nreserve\t0\nhulk\t2\n")
	plantWorkerTestCard(t, store, "CARD-001", "RESULT: CARD-001 pass\nDo something")

	harvestDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	ran := false

	opts := PullWorkerOptions{
		Bench:   "hulk",
		Store:   store,
		Harvest: harvestDir,
		For:     10 * time.Minute,
		Once:    true,
		Stdout:  &stdout,
		Stderr:  &stderr,
		RunCard: func(ctx context.Context, label, cardPath, workDir string) (int, error) {
			ran = true
			if label != "CARD-001" {
				return 1, fmt.Errorf("unexpected label %q", label)
			}
			// Write RESULT.md in the workdir
			resultFile := filepath.Join(workDir, "RESULT.md")
			if err := os.WriteFile(resultFile, []byte("RESULT: CARD-001 done\nAll tests pass"), 0o644); err != nil {
				return 1, err
			}
			return 0, nil
		},
	}

	code := RunPullWorker(context.Background(), opts)
	if code != 0 {
		t.Fatalf("RunPullWorker exit = %d, want 0\nStderr:\n%s", code, stderr.String())
	}
	if !ran {
		t.Fatalf("RunCard was not executed")
	}

	// Verify harvested RESULT.md
	harvested := filepath.Join(harvestDir, "CARD-001", "RESULT.md")
	data, err := os.ReadFile(harvested)
	if err != nil {
		t.Fatalf("missing harvested result at %s: %v", harvested, err)
	}
	if !strings.Contains(string(data), "RESULT: CARD-001 done") {
		t.Fatalf("harvested content mismatch: %s", string(data))
	}

	// Verify stdout output has PULL DONE
	out := stdout.String()
	if !strings.Contains(out, "PULL DONE") || !strings.Contains(out, "card=CARD-001.card") {
		t.Fatalf("stdout missing PULL DONE line:\n%s", out)
	}

	// Verify card is no longer in queue/ or taken/
	if _, err := os.Stat(filepath.Join(store, queueDirName, "CARD-001.card")); !os.IsNotExist(err) {
		t.Fatalf("card still in queue/")
	}
	taken, _ := os.ReadDir(filepath.Join(store, takenDirName))
	if len(taken) > 0 {
		t.Fatalf("card still in taken/: %v", taken)
	}
	// Verify card is in done/
	if _, err := os.Stat(filepath.Join(store, "done", "CARD-001.card")); err != nil {
		t.Fatalf("card not moved to done/: %v", err)
	}
}

// TestWorkerIdleWhenQueueIsEmpty: With an empty queue and Once=true, it prints PULL IDLE and exits 0.
func TestWorkerIdleWhenQueueIsEmpty(t *testing.T) {
	store := writeWorkerTestStore(t, "capacity\t2\nreserve\t0\nhulk\t2\n")
	var stdout, stderr bytes.Buffer

	opts := PullWorkerOptions{
		Bench:  "hulk",
		Store:  store,
		Once:   true,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	code := RunPullWorker(context.Background(), opts)
	if code != 0 {
		t.Fatalf("RunPullWorker exit = %d, want 0\nStderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "PULL IDLE") || !strings.Contains(out, "cards=0") {
		t.Fatalf("stdout missing PULL IDLE:\n%s", out)
	}
}

// TestWorkerContainerContractArgs: verify that BuildContainerArgs matches the run contract.
func TestWorkerContainerContractArgs(t *testing.T) {
	args := BuildContainerArgs("podman", "nova-card:dev", "/home/bench/work", "opencode/deepseek-v4-flash", "CARD-42", "card text", "DEEPSEEK_API_KEY")
	joined := strings.Join(args, " ")

	requiredFlags := []string{
		"run --rm",
		"--userns=keep-id:uid=10001,gid=10001",
		"--read-only",
		"--tmpfs /tmp:rw,exec,nosuid,nodev,size=1g",
		"--mount type=tmpfs,destination=/home/card,tmpfs-size=2g,tmpfs-mode=0700,U=true,notmpcopyup",
		"--security-opt no-new-privileges",
		"--cap-drop=ALL",
		"--memory 8g",
		"--pids-limit 512",
		"-v /home/bench/work:/work",
		"-e DEEPSEEK_API_KEY",
		"nova-card:dev",
		"opencode run --model opencode/deepseek-v4-flash --title CARD-42 -- card text",
	}

	for _, req := range requiredFlags {
		if !strings.Contains(joined, req) {
			t.Errorf("BuildContainerArgs missing required contract %q\nGot:\n%s", req, joined)
		}
	}
}

// TestWrapSecretsExec: verify that WrapSecretsExec prefixes nova-secrets exec --as <seat> --.
func TestWrapSecretsExec(t *testing.T) {
	base := []string{"podman", "run", "--rm", "nova-card:dev"}
	wrapped := WrapSecretsExec("hulk", base)
	want := "nova-secrets exec --as hulk -- podman run --rm nova-card:dev"
	if strings.Join(wrapped, " ") != want {
		t.Errorf("WrapSecretsExec = %q, want %q", strings.Join(wrapped, " "), want)
	}

	empty := WrapSecretsExec("", base)
	if strings.Join(empty, " ") != strings.Join(base, " ") {
		t.Errorf("WrapSecretsExec empty seat modified args")
	}
}

func TestCardLaneAndExtractPR(t *testing.T) {
	// CardLane
	if got := CardLane("RESULT: CARD-1\nLANE: pulse\n"); got != "pulse" {
		t.Errorf("CardLane = %q, want pulse", got)
	}
	if got := CardLane("LANE:   docs-batch-2  \n"); got != "docs-batch-2" {
		t.Errorf("CardLane = %q, want docs-batch-2", got)
	}
	if got := CardLane("LANES: pulse\n"); got != "" {
		t.Errorf("CardLane = %q, want empty for LANES:", got)
	}
	if got := CardLane(""); got != "" {
		t.Errorf("CardLane empty = %q, want empty", got)
	}

	// ExtractPR
	if got := ExtractPR([]byte("RESULT: ok\nPR: 123\n")); got != 123 {
		t.Errorf("ExtractPR PR: = %d, want 123", got)
	}
	if got := ExtractPR([]byte("RESULT: ok\nPR #456\n")); got != 456 {
		t.Errorf("ExtractPR PR # = %d, want 456", got)
	}
	if got := ExtractPR([]byte("RESULT: ok\nhttps://github.com/mas-bandwidth/nova-tools/pull/789\n")); got != 789 {
		t.Errorf("ExtractPR URL = %d, want 789", got)
	}
	if got := ExtractPR([]byte("RESULT: ok\nno pr\n")); got != 0 {
		t.Errorf("ExtractPR no pr = %d, want 0", got)
	}
}

// TestPullWorkerRefusesCardWhenLaneHeld: Prove two cards with the same LANE: pulse
// cannot run concurrently; the second is refused admission and returned to queue/ until
// the first completes.
func TestPullWorkerRefusesCardWhenLaneHeld(t *testing.T) {
	store := writeWorkerTestStore(t, "capacity\t2\nreserve\t0\nhulk\t2\n")
	plantWorkerTestCard(t, store, "CARD-001", "RESULT: CARD-001\nLANE: pulse\n")

	adm := jobs.New(jobs.Vector{"slot:hulk": 2})
	defer adm.Close()

	// Pre-grant lane:pulse to an existing job
	_, err := adm.Grant(jobs.Request{
		ID:     "HOLD-001",
		Vector: jobs.Vector{"slot:hulk": 1, jobs.Lane("pulse"): 1},
	})
	if err != nil {
		t.Fatalf("setup pre-grant: %v", err)
	}

	var stdout, stderr bytes.Buffer
	harvestDir := t.TempDir()
	opts := PullWorkerOptions{
		Bench:     "hulk",
		Store:     store,
		Harvest:   harvestDir,
		Admission: adm,
		Once:      true,
		Stdout:    &stdout,
		Stderr:    &stderr,
		RunCard: func(ctx context.Context, label, cardPath, workDir string) (int, error) {
			return 0, os.WriteFile(filepath.Join(workDir, "RESULT.md"), []byte("RESULT: CARD-001 ok"), 0o644)
		},
	}

	// Worker should pull CARD-001, see lane:pulse is held, refuse admission, return CARD-001 to queue/, and exit 2.
	code := RunPullWorker(context.Background(), opts)
	if code != 2 {
		t.Fatalf("RunPullWorker exit = %d, want 2 on refusal\nStderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "admission refused") {
		t.Errorf("stderr missing 'admission refused':\n%s", stderr.String())
	}

	// Assert CARD-001 is back in queue/
	if _, err := os.Stat(filepath.Join(store, queueDirName, "CARD-001.card")); err != nil {
		t.Fatalf("card not in queue/ after refusal: %v", err)
	}
	// Assert taken/ is empty
	taken, _ := os.ReadDir(filepath.Join(store, takenDirName))
	if len(taken) != 0 {
		t.Fatalf("taken/ not empty after refusal: %v", taken)
	}

	// Now release the hold on lane:pulse
	if err := adm.Release("HOLD-001"); err != nil {
		t.Fatalf("release HOLD-001: %v", err)
	}

	// Run worker again; now CARD-001 should be admitted and run to completion
	stderr.Reset()
	stdout.Reset()
	code = RunPullWorker(context.Background(), opts)
	if code != 0 {
		t.Fatalf("RunPullWorker exit = %d, want 0 after lane freed\nStderr:\n%s", code, stderr.String())
	}

	// Assert CARD-001 is moved to done/
	if _, err := os.Stat(filepath.Join(store, "done", "CARD-001.card")); err != nil {
		t.Fatalf("card not in done/ after successful run: %v", err)
	}
}

// TestPullWorkerRecordsAttemptStartAndEndWithProof: Prove the worker records an initial
// attempt with uncertain, and on completion records ok with proof=sha256:... and pr.
func TestPullWorkerRecordsAttemptStartAndEndWithProof(t *testing.T) {
	store := writeWorkerTestStore(t, "capacity\t2\nreserve\t0\nhulk\t2\n")
	plantWorkerTestCard(t, store, "CARD-001", "RESULT: CARD-001\nLANE: pulse\n")

	type attemptCall struct {
		unit, by, outcome, proof string
		pr                       int
	}
	var calls []attemptCall

	harvestDir := t.TempDir()
	resultContent := []byte("RESULT: CARD-001 done\nPR: 1421\n")
	expectedSha := fmt.Sprintf("%x", sha256.Sum256(resultContent))

	opts := PullWorkerOptions{
		Bench:   "hulk",
		Store:   store,
		Harvest: harvestDir,
		Once:    true,
		RunCard: func(ctx context.Context, label, cardPath, workDir string) (int, error) {
			resultFile := filepath.Join(workDir, "RESULT.md")
			return 0, os.WriteFile(resultFile, resultContent, 0o644)
		},
		RecordAttempt: func(unit, by, outcome, proof string, pr int) error {
			calls = append(calls, attemptCall{unit: unit, by: by, outcome: outcome, proof: proof, pr: pr})
			return nil
		},
	}

	code := RunPullWorker(context.Background(), opts)
	if code != 0 {
		t.Fatalf("RunPullWorker exit = %d, want 0", code)
	}

	if len(calls) != 2 {
		t.Fatalf("RecordAttempt calls = %d, want 2 (start and end)", len(calls))
	}

	// Call 0: start with outcome=uncertain, no proof, no pr
	if calls[0].unit != "CARD-001" || calls[0].by != "hulk" || calls[0].outcome != "uncertain" || calls[0].proof != "" || calls[0].pr != 0 {
		t.Errorf("start attempt record mismatch: %+v", calls[0])
	}

	// Call 1: end with outcome=ok, proof=sha256, pr=1421
	if calls[1].unit != "CARD-001" || calls[1].by != "hulk" || calls[1].outcome != "ok" || calls[1].proof != expectedSha || calls[1].pr != 1421 {
		t.Errorf("end attempt record mismatch: got %+v, want proof=%s pr=1421", calls[1], expectedSha)
	}
}

// TestPullWorkerRefusesWhenBenchSlotsExhausted: Prove admission refuses when slot:<bench> capacity is saturated.
func TestPullWorkerRefusesWhenBenchSlotsExhausted(t *testing.T) {
	store := writeWorkerTestStore(t, "capacity\t1\nreserve\t0\nhulk\t1\n")
	plantWorkerTestCard(t, store, "CARD-001", "RESULT: CARD-001\n")

	adm := jobs.New(jobs.Vector{"slot:hulk": 1})
	defer adm.Close()

	// Pre-grant the only bench slot
	_, err := adm.Grant(jobs.Request{
		ID:     "HOLD-SLOT",
		Vector: jobs.Vector{"slot:hulk": 1},
	})
	if err != nil {
		t.Fatalf("pre-grant slot: %v", err)
	}

	var stderr bytes.Buffer
	opts := PullWorkerOptions{
		Bench:     "hulk",
		Store:     store,
		Admission: adm,
		Once:      true,
		Stderr:    &stderr,
	}

	code := RunPullWorker(context.Background(), opts)
	if code != 2 {
		t.Fatalf("RunPullWorker exit = %d, want 2 on slot exhaustion\nStderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "admission refused") {
		t.Errorf("stderr missing 'admission refused':\n%s", stderr.String())
	}

	// Card remains in queue/
	if _, err := os.Stat(filepath.Join(store, queueDirName, "CARD-001.card")); err != nil {
		t.Fatalf("card not in queue/ after refusal: %v", err)
	}
}
