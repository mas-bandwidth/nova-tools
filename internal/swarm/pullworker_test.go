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
	queue := filepath.Join(store, QueueName)
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

	harvested := filepath.Join(harvestDir, "CARD-001", "RESULT.md")
	data, err := os.ReadFile(harvested)
	if err != nil {
		t.Fatalf("missing harvested result at %s: %v", harvested, err)
	}
	if !strings.Contains(string(data), "RESULT: CARD-001 done") {
		t.Fatalf("harvested content mismatch: %s", string(data))
	}

	out := stdout.String()
	if !strings.Contains(out, "PULL DONE") || !strings.Contains(out, "card=CARD-001.card") {
		t.Fatalf("stdout missing PULL DONE line:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(store, QueueName, "CARD-001.card")); !os.IsNotExist(err) {
		t.Fatalf("card still in queue/")
	}
	taken, _ := os.ReadDir(filepath.Join(store, TakenName))
	if len(taken) > 0 {
		t.Fatalf("card still in taken/: %v", taken)
	}
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

// writeFakeExec puts a shell script named <name> in dir/bin and returns the dir
// with that bin and nothing else on PATH, so a test can prove which binary a
// path ran and with what arguments, never the real one.
func writeFakeExec(t *testing.T, name, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A container that plants a symlink where RESULT.md is expected must not carry the
// host-side harvest out of the job: the read is refused, and nothing is harvested.
func TestHarvestRefusesASymlinkedResult(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET-CONTENT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(workDir, "RESULT.md")); err != nil {
		t.Fatal(err)
	}
	harvestPath := filepath.Join(t.TempDir(), "RESULT.md")

	harvestResult(workDir, harvestPath)

	if _, err := os.Stat(harvestPath); !os.IsNotExist(err) {
		t.Fatalf("harvestResult followed the symlink and wrote the secret: stat err=%v", err)
	}
}

// A queue/ is an entry point for untrusted names. A card whose label is not one
// safe path element must be refused before any join, not run into a workspace the
// raw name would build.
func TestWorkerRefusesACardWithAnUnsafeLabel(t *testing.T) {
	store := writeWorkerTestStore(t, "capacity\t2\nreserve\t0\nhulk\t2\n")
	plantWorkerTestCard(t, store, "CARD 001", "RESULT: pass\n")
	var stdout, stderr bytes.Buffer
	ran := false

	opts := PullWorkerOptions{
		Bench:  "hulk",
		Store:  store,
		Once:   true,
		Stdout: &stdout,
		Stderr: &stderr,
		RunCard: func(ctx context.Context, label, cardPath, workDir string) (int, error) {
			ran = true
			return 0, nil
		},
	}

	code := RunPullWorker(context.Background(), opts)
	if code != 2 {
		t.Fatalf("unsafe-label card exit = %d, want 2\nStderr:\n%s", code, stderr.String())
	}
	if ran {
		t.Fatalf("an unsafe-label card was run")
	}
	if _, err := os.Stat(filepath.Join(store, "done", "CARD 001.card")); err != nil {
		t.Fatalf("unsafe-label card not moved to done/: %v", err)
	}
	if !strings.Contains(stderr.String(), "not a safe name") {
		t.Fatalf("stderr does not say the label is unsafe:\n%s", stderr.String())
	}
}

// --seat wraps the container command through nova-secrets exec: the production
// executeContainer path, not just the helper, must hand nova-secrets the wrapped
// command line.
func TestExecuteContainerRunsThroughNovaSecrets(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	script := "#!/bin/sh\nprintf '%s' \"$*\" > " + `"` + log + `"` + "\n"
	bin := writeFakeExec(t, "nova-secrets", script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	workDir := t.TempDir()
	opts := PullWorkerOptions{
		Seat:      "hulk",
		Container: "podman",
		Image:     "nova-card:dev",
		Model:     "m",
		Stderr:    &bytes.Buffer{},
	}

	code, err := executeContainer(context.Background(), opts, "CARD-1", "card", workDir)
	if err != nil || code != 0 {
		t.Fatalf("executeContainer = %d, %v; want 0", code, err)
	}
	data, rerr := os.ReadFile(log)
	if rerr != nil {
		t.Fatalf("nova-secrets was never run: %v", rerr)
	}
	got := string(data)
	if !strings.Contains(got, "exec --as hulk -- podman") {
		t.Fatalf("nova-secrets was not handed the wrapped command; got %q", got)
	}
}

// --seat without nova-secrets must refuse, never fail open into a bare container
// run that drops the bench's secrets.
func TestExecuteContainerRefusesSeatWithoutNovaSecrets(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", empty)

	opts := PullWorkerOptions{
		Seat:      "hulk",
		Container: "podman",
		Stderr:    &bytes.Buffer{},
	}
	code, err := executeContainer(context.Background(), opts, "CARD-1", "card", t.TempDir())
	if err == nil {
		t.Fatalf("seat without nova-secrets returned no error (code=%d); want a refusal", code)
	}
	if code == 0 {
		t.Fatalf("seat without nova-secrets exit = 0, want a non-zero refusal")
	}
	if !strings.Contains(err.Error(), "nova-secrets") {
		t.Fatalf("refusal does not name nova-secrets: %v", err)
	}
}
