package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/SPEC-JOBS.md section 6 names the pull verb and the flags one batch carries.
func TestPullVerbLineIsInTheHelp(t *testing.T) {
	_, stdout, _ := runSwarm(t, "help")
	for _, want := range []string{"nova-swarm pull", "--batch <n>", "--clone <dir>", "--harvest <dir>", "--queue <dir>"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help does not name %q:\n%s", want, stdout)
		}
	}
}

// A pull missing one required flag is a refusal: exit 2, one remedy line, nothing on stdout.
func TestPullWithoutAQueueRefusesOneLine(t *testing.T) {
	dir := t.TempDir()
	exit, stdout, stderr := runSwarm(t, "pull",
		"--clone", dir, "--harvest", dir, "--batch", "1", "--runner", "true")
	if exit != 2 {
		t.Fatalf("pull with no queue exit = %d, want 2 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("a refusal wrote to stdout: %q", stdout)
	}
	if lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("a refusal printed %d lines, want 1:\n%s", len(lines), stderr)
	}
	if !strings.Contains(stderr, "--queue is required") || !strings.Contains(stderr, "it wants") {
		t.Fatalf("the refusal names neither the flag nor what it wants:\n%s", stderr)
	}
}

// plantCard puts one <name>.card in a bench's queue/.
func plantCard(t *testing.T, bench, name string) {
	t.Helper()
	queue := filepath.Join(bench, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(queue, name+".card"), []byte("card\n"), 0o644); err != nil {
		t.Fatalf("write card: %v", err)
	}
}

// The help text carries the pull verb line and the rename that is the ownership
// record, exactly as section 2 prints them.
func TestHelpNamesThePullVerbAndItsRename(t *testing.T) {
	exit, stdout, _ := runSwarm(t, "help")
	if exit != 0 {
		t.Fatalf("help exit = %d, want 0", exit)
	}
	for _, want := range []string{"nova-swarm pull", "rename(<name>.card, taken/<worker>-<name>.card)"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help does not name %q:\n%s", want, stdout)
		}
	}
}

// pull lists a bench's queue/ and takes one card by rename, as one line (section 2).
func TestPullTakesOneCardByRename(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "alpha")

	exit, stdout, stderr := runSwarm(t, "pull", "--bench", bench, "--worker", "w1")
	if exit != 0 {
		t.Fatalf("pull exit = %d, want 0 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	line := strings.TrimSpace(stdout)
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("pull printed more than one line: %q", stdout)
	}
	for _, want := range []string{"PULL", "bench=" + bench, "worker=w1", "card=alpha", "source=queue"} {
		if !strings.Contains(line, want) {
			t.Fatalf("pull line %q does not name %q", line, want)
		}
	}
	if _, err := os.Stat(filepath.Join(bench, "queue", "alpha.card")); !os.IsNotExist(err) {
		t.Fatalf("the card is still in queue/: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(bench, "taken", "w1-alpha.card")); err != nil {
		t.Fatalf("the ownership record w1-alpha.card is missing: %v", err)
	}
}

// An empty queue with nowhere to steal is a normal idle line, not a refusal (section 2).
func TestPullOnAnEmptyQueuePrintsIdle(t *testing.T) {
	bench := t.TempDir()
	exit, stdout, stderr := runSwarm(t, "pull", "--bench", bench, "--worker", "w1")
	if exit != 0 {
		t.Fatalf("pull on an empty queue exit = %d, want 0 (stderr=%q)", exit, stderr)
	}
	line := strings.TrimSpace(stdout)
	for _, want := range []string{"card=-", "source=none"} {
		if !strings.Contains(line, want) {
			t.Fatalf("idle line %q does not name %q", line, want)
		}
	}
}

// A missing --bench or --worker is a refusal with the remedy, exit 2 (section 2).
func TestPullRefusesMissingFlags(t *testing.T) {
	exit, stdout, stderr := runSwarm(t, "pull")
	if exit != 2 {
		t.Fatalf("pull with no flags exit = %d, want 2 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	for _, want := range []string{"--bench is required", "--worker is required", "nova-swarm help"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("refusal %q does not name %q", stderr, want)
		}
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a refusal wrote to stdout: %q", stdout)
	}
}

// An idle worker steals from the fullest bench, never below the victim's capacity
// line (section 2).
func TestPullStealsFromTheFullestBench(t *testing.T) {
	home := t.TempDir()
	victim := t.TempDir()
	for i := 0; i < 5; i++ {
		plantCard(t, victim, "v"+string(rune('a'+i)))
	}

	exit, stdout, stderr := runSwarm(t, "pull", "--bench", home, "--worker", "w1",
		"--steal", victim, "--capacity", "2")
	if exit != 0 {
		t.Fatalf("stealing pull exit = %d, want 0 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	line := strings.TrimSpace(stdout)
	for _, want := range []string{"source=steal", "victim=" + victim} {
		if !strings.Contains(line, want) {
			t.Fatalf("steal line %q does not name %q", line, want)
		}
	}
	left, err := os.ReadDir(filepath.Join(victim, "queue"))
	if err != nil {
		t.Fatalf("victim queue: %v", err)
	}
	if len(left) != 2 {
		t.Fatalf("victim left with %d cards, want its capacity line 2", len(left))
	}
}

// a-launch-without-a-lease-is-refused-by-the-puller (docs/SPEC-JOBS.md section 3).
func TestALaunchWithoutALeaseIsRefusedByThePuller(t *testing.T) {
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte("capacity\t1\nreserve\t0\nalice\t0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store, "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "queue", "a.card"), []byte("card"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := runSwarm(t, "pull", "--store", store, "--owner", "alice", "--for", "1h")
	if exit != 2 {
		t.Fatalf("pull without a lease exit = %d, want 2 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "pull") || !strings.Contains(stderr, "lease") {
		t.Fatalf("refusal must name the puller and the lease, got %q", stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a refused pull wrote to stdout: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(store, "queue", "a.card")); err != nil {
		t.Fatalf("refused pull must leave the card in queue/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, "taken")); !os.IsNotExist(err) {
		t.Fatalf("refused pull must take nothing, taken/ stat err = %v", err)
	}
}

// TestPullWorkerRunsCardWithRunnerAndHarvestsOutcome: test the pull worker daemon CLI
// with --bench, --slots, --seat, --runner, --once.
func TestPullWorkerRunsCardWithRunnerAndHarvestsOutcome(t *testing.T) {
	bench := t.TempDir()
	if err := os.WriteFile(filepath.Join(bench, "shares.tsv"), []byte("capacity\t2\nreserve\t0\nhulk\t2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plantCard(t, bench, "CARD-777")
	harvest := t.TempDir()

	runnerScript := "echo 'RESULT: CARD-777 pass' > RESULT.md"
	exit, stdout, stderr := runSwarm(t, "pull",
		"--bench", bench,
		"--slots", "1",
		"--seat", "hulk",
		"--harvest", harvest,
		"--runner", runnerScript,
		"--once")

	if exit != 0 {
		t.Fatalf("pull worker exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}

	if !strings.Contains(stdout, "PULL DONE") || !strings.Contains(stdout, "card=CARD-777.card") {
		t.Fatalf("stdout missing PULL DONE line:\n%s", stdout)
	}

	// Verify harvested RESULT.md
	resFile := filepath.Join(harvest, "CARD-777", "RESULT.md")
	data, err := os.ReadFile(resFile)
	if err != nil {
		t.Fatalf("harvested RESULT.md not found at %s: %v", resFile, err)
	}
	if !strings.Contains(string(data), "RESULT: CARD-777 pass") {
		t.Fatalf("harvested content mismatch: %s", string(data))
	}
}

// TestPullWorkerIdleExitsZero: test pull worker with empty queue and --once exits 0 with PULL IDLE.
func TestPullWorkerIdleExitsZero(t *testing.T) {
	bench := t.TempDir()
	if err := os.WriteFile(filepath.Join(bench, "shares.tsv"), []byte("capacity\t2\nreserve\t0\nhulk\t2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := runSwarm(t, "pull",
		"--bench", bench,
		"--slots", "1",
		"--seat", "hulk",
		"--once")

	if exit != 0 {
		t.Fatalf("pull worker idle exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "PULL IDLE") {
		t.Fatalf("stdout missing PULL IDLE:\n%s", stdout)
	}
}

// TestPullCLIExitsNonZeroOnLockDirError verifies Stella's witness 1: if victim/.locks is a
// regular file, pull exits 2 with an error on stderr rather than silently exiting 0 with source=none.
func TestPullCLIExitsNonZeroOnLockDirError(t *testing.T) {
	bench := t.TempDir()
	plantCard(t, bench, "one")

	locksPath := filepath.Join(bench, ".locks")
	if err := os.WriteFile(locksPath, []byte("regular file"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := runSwarm(t, "pull", "--bench", bench, "--worker", "stella-review")
	if exit != 2 {
		t.Fatalf("pull exit = %d, want 2 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "nova-swarm pull:") {
		t.Fatalf("stderr = %q, want 'nova-swarm pull:' error prefix", stderr)
	}
}

// TestPullCLIStealExitsNonZeroOnLockDirError verifies Stella's witness 2: if victim/.locks is a
// regular file, stealing from that victim exits 2 promptly with an error rather than hanging or being killed.
func TestPullCLIStealExitsNonZeroOnLockDirError(t *testing.T) {
	local := t.TempDir()
	victim := t.TempDir()
	plantCard(t, victim, "one")

	locksPath := filepath.Join(victim, ".locks")
	if err := os.WriteFile(locksPath, []byte("regular file"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := runSwarm(t, "pull", "--bench", local, "--worker", "stella-review",
		"--steal", victim, "--capacity", "0")
	if exit != 2 {
		t.Fatalf("stealing pull exit = %d, want 2 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "nova-swarm pull:") {
		t.Fatalf("stderr = %q, want 'nova-swarm pull:' error prefix", stderr)
	}
}
