package swarm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
