package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonEnvelope represents the standard envelope shape parsed by JSON tests.
type testJSONEnvelope struct {
	Result struct {
		Verb   string `json:"verb"`
		Status string `json:"status"`
		Exit   int    `json:"exit"`
	} `json:"result"`
	Facts map[string]any   `json:"facts"`
	Items []map[string]any `json:"items"`
	Notes []string         `json:"notes"`
}

func parseJSONEnvelope(t *testing.T, raw string) testJSONEnvelope {
	t.Helper()
	var env testJSONEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &env); err != nil {
		t.Fatalf("failed to parse JSON envelope:\n%s\nerror: %v", raw, err)
	}
	return env
}

func TestTemplateJSON(t *testing.T) {
	t.Parallel()

	// Text mode: verbatim content without envelope
	rc, stdout, stderr := runSwarm(t, "template", "--name", "read-pr")
	if rc != 0 {
		t.Fatalf("template text failed: %s %s", stdout, stderr)
	}
	if strings.HasPrefix(stdout, "{") {
		t.Fatalf("text mode should not output json: %s", stdout)
	}

	// JSON mode: standard envelope
	rc, stdout, stderr = runSwarm(t, "template", "--name", "read-pr", "--json")
	if rc != 0 {
		t.Fatalf("template --json failed: %s %s", stdout, stderr)
	}
	env := parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "template" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if env.Facts["name"] != "read-pr" {
		t.Fatalf("facts.name = %v, want 'read-pr'", env.Facts["name"])
	}
	if len(env.Notes) == 0 || !strings.Contains(env.Notes[0], "read-pr") {
		t.Fatalf("unexpected notes: %+v", env.Notes)
	}
}

func TestDoctorJSON(t *testing.T) {
	t.Parallel()

	rc, stdout, stderr := runSwarm(t, "doctor", "--json")
	if rc != 0 {
		t.Fatalf("doctor --json failed: %s %s", stdout, stderr)
	}
	env := parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "doctor" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if env.Facts == nil {
		t.Fatalf("facts should not be nil")
	}
}

func TestLintJSON(t *testing.T) {
	t.Parallel()

	// 1. lint --rules --json
	rc, stdout, stderr := runSwarm(t, "lint", "--rules", "--json")
	if rc != 0 {
		t.Fatalf("lint --rules --json failed: %s %s", stdout, stderr)
	}
	env := parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "lint" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if len(env.Items) == 0 {
		t.Fatalf("items should contain rules")
	}

	// 2. lint --card passing card
	tmp := t.TempDir()
	cardPath := filepath.Join(tmp, "card.md")
	// Generate template card
	_, cardContent, _ := runSwarm(t, "template", "--name", "card")
	if err := os.WriteFile(cardPath, []byte(cardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	rc, stdout, stderr = runSwarm(t, "lint", "--card", cardPath, "--child-rules", "--json")
	if rc != 0 {
		t.Fatalf("lint passing card failed: %s %s", stdout, stderr)
	}
	env = parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "lint" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}

	// 3. lint --card failing card
	badCardPath := filepath.Join(tmp, "badcard.md")
	if err := os.WriteFile(badCardPath, []byte("NOT A VALID CARD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rc, stdout, stderr = runSwarm(t, "lint", "--card", badCardPath, "--json")
	if rc != 1 {
		t.Fatalf("lint bad card rc = %d, want 1. stdout: %s, stderr: %s", rc, stdout, stderr)
	}
	env = parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "lint" || env.Result.Status != "failed" || env.Result.Exit != 1 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if len(env.Items) == 0 {
		t.Fatalf("items should contain violations/drifts")
	}
}

func TestSlotsJSON(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	store := filepath.Join(tmp, "slots")

	// 1. slots init --json
	rc, stdout, stderr := runSwarm(t, "slots", "init", "--store", store, "--owner", "worker1", "--capacity", "4", "--share", "2", "--json")
	if rc != 0 {
		t.Fatalf("slots init --json failed: %s %s", stdout, stderr)
	}
	env := parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "slots init" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if env.Facts["capacity"] != float64(4) || env.Facts["share"] != float64(2) {
		t.Fatalf("unexpected facts: %+v", env.Facts)
	}

	// 2. slots list --json (empty)
	rc, stdout, stderr = runSwarm(t, "slots", "list", "--store", store, "--json")
	if rc != 0 {
		t.Fatalf("slots list --json failed: %s %s", stdout, stderr)
	}
	env = parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "slots list" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if env.Facts["capacity"] != float64(4) || env.Facts["active"] != float64(0) {
		t.Fatalf("unexpected facts: %+v", env.Facts)
	}
	if len(env.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(env.Items))
	}

	// 3. slots take --json
	rc, stdout, stderr = runSwarm(t, "slots", "take", "--store", store, "--owner", "worker1", "--n", "1", "--for", "10m", "--label", "job-1", "--json")
	if rc != 0 {
		t.Fatalf("slots take --json failed: %s %s", stdout, stderr)
	}
	env = parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "slots take" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if env.Facts["granted"] != float64(1) {
		t.Fatalf("expected granted=1, got %v", env.Facts["granted"])
	}

	// 3b. slots take refusal --json (asking for 10 when capacity is 4)
	rc, stdout, stderr = runSwarm(t, "slots", "take", "--store", store, "--owner", "worker1", "--n", "10", "--for", "10m", "--label", "job-over", "--json")
	if rc != 2 {
		t.Fatalf("slots take overcapacity rc=%d, want 2. stdout: %s stderr: %s", rc, stdout, stderr)
	}
	env = parseJSONEnvelope(t, stderr)
	if env.Result.Verb != "slots take" || env.Result.Status != "refused" || env.Result.Exit != 2 {
		t.Fatalf("unexpected refusal envelope: %+v", env.Result)
	}

	// 4. slots list --json (1 item)
	rc, stdout, stderr = runSwarm(t, "slots", "list", "--store", store, "--json")
	if rc != 0 {
		t.Fatalf("slots list --json failed: %s %s", stdout, stderr)
	}
	env = parseJSONEnvelope(t, stdout)
	if env.Facts["active"] != float64(1) {
		t.Fatalf("expected active=1, got %v", env.Facts["active"])
	}
	if len(env.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(env.Items))
	}
	if env.Items[0]["owner"] != "worker1" || env.Items[0]["label"] != "job-1" {
		t.Fatalf("unexpected item: %+v", env.Items[0])
	}

	// 5. slots release --json
	rc, stdout, stderr = runSwarm(t, "slots", "release", "--store", store, "--owner", "worker1", "--label", "job-1", "--force", "--json")
	if rc != 0 {
		t.Fatalf("slots release --json failed: %s %s", stdout, stderr)
	}
	env = parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "slots release" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if env.Facts["released"] != float64(1) {
		t.Fatalf("expected released=1, got %v", env.Facts["released"])
	}
}

func TestWorkerCheckJSON(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	workerPath := filepath.Join(tmp, "worker.json")
	// Invalid worker JSON to test failed check
	if err := os.WriteFile(workerPath, []byte(`{"name": ""}`), 0o644); err != nil {
		t.Fatal(err)
	}

	rc, stdout, stderr := runSwarm(t, "worker", "check", workerPath, "--json")
	if rc != 2 {
		t.Fatalf("worker check bad rc = %d, want 2. stdout: %s, stderr: %s", rc, stdout, stderr)
	}
	env := parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "worker check" || env.Result.Status != "failed" || env.Result.Exit != 2 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if len(env.Items) == 0 {
		t.Fatalf("items should contain drifts")
	}
}

func TestBatchJSON(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	root := filepath.Join(tmp, "jobs")
	cardDir := filepath.Join(tmp, "cards")
	_ = os.MkdirAll(cardDir, 0o755)

	cardPath := filepath.Join(cardDir, "card1.md")
	if err := os.WriteFile(cardPath, []byte("CONTRACT LINE 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cardsTsv := filepath.Join(tmp, "cards.tsv")
	if err := os.WriteFile(cardsTsv, []byte("c1\t1\tmodel\t"+cardPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runnerPath := filepath.Join(tmp, "runner.sh")
	runnerScript := `#!/bin/sh
label="$1"
slot="$2"
root="$5"
jobdir="$root/$slot/jobs/$label"
mkdir -p "$jobdir"
echo "CONTRACT LINE 1" > "$jobdir/RESULT.md"
echo "DONE" >> "$jobdir/RESULT.md"
`
	if err := os.WriteFile(runnerPath, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Text mode check (backward compatibility)
	rc, stdout, stderr := runSwarm(t,
		"batch", "--id", "batch-txt", "--cards", cardsTsv,
		"--deadline", "10", "--root", root, "--tokens", "unmetered",
		"--runner", runnerPath, "--no-route", "--reason", "test",
	)
	if rc != 0 {
		t.Fatalf("batch text failed rc=%d: stdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if !strings.HasPrefix(stdout, "BATCH batch-txt n=1 done=1") {
		t.Fatalf("text output should start with BATCH line, got:\n%s", stdout)
	}

	// 2. JSON mode check
	rc, stdout, stderr = runSwarm(t,
		"batch", "--id", "batch-json", "--cards", cardsTsv,
		"--deadline", "10", "--root", root, "--tokens", "unmetered",
		"--runner", runnerPath, "--no-route", "--reason", "test",
		"--json",
	)
	if rc != 0 {
		t.Fatalf("batch --json failed rc=%d: stdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	env := parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "batch" || env.Result.Status != "ok" || env.Result.Exit != 0 {
		t.Fatalf("unexpected result: %+v", env.Result)
	}
	if env.Facts["id"] != "batch-json" || env.Facts["n"] != float64(1) || env.Facts["done"] != float64(1) {
		t.Fatalf("unexpected facts: %+v", env.Facts)
	}
	if len(env.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(env.Items))
	}
	if env.Items[0]["label"] != "c1" || env.Items[0]["state"] != "done" {
		t.Fatalf("unexpected item: %+v", env.Items[0])
	}

	// 3. JSON mode with abstaining card -> exit 1, status "failed"
	rootFail := filepath.Join(tmp, "jobs-fail")
	emptyRunnerPath := filepath.Join(tmp, "empty_runner.sh")
	if err := os.WriteFile(emptyRunnerPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rc, stdout, stderr = runSwarm(t,
		"batch", "--id", "batch-fail", "--cards", cardsTsv,
		"--deadline", "10", "--root", rootFail, "--tokens", "unmetered",
		"--runner", emptyRunnerPath, "--no-route", "--reason", "test",
		"--json",
	)
	if rc != 1 {
		t.Fatalf("batch fail rc=%d, want 1. stdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	env = parseJSONEnvelope(t, stdout)
	if env.Result.Verb != "batch" || env.Result.Status != "failed" || env.Result.Exit != 1 {
		t.Fatalf("unexpected fail envelope result: %+v", env.Result)
	}
	if env.Facts["abstain"] != float64(1) {
		t.Fatalf("expected facts.abstain=1, got %v", env.Facts["abstain"])
	}
}
