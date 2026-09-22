package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A LAUNCH THAT DIES FAST ON A PROVIDER 5XX IS RETRIED (issue #900). These tests hold the
// inherited grace path: the retry keeps the task, the usage rows carry attempt=1,2,3, and a
// slow failure is not retried. The tail text is not evidence the provider never accepted
// the request.

// poolUsageRow reads one pool usage row by job id.
func poolUsageRow(t *testing.T, pool, id string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(pool, "usage", id+".tsv"))
	if err != nil {
		t.Fatalf("no usage row for %s: %v", id, err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("the usage row for %s holds no row:\n%s", id, raw)
	}
	head := strings.Split(lines[0], "\t")
	values := strings.Split(lines[1], "\t")
	row := map[string]string{}
	for i, name := range head {
		if i < len(values) {
			row[name] = values[i]
		}
	}
	return row
}

// sidecarIn reads the sidecar a task landed with under one of the pool's directories.
func sidecarIn(t *testing.T, pool, dir, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(pool, dir, id+".json"))
	if err != nil {
		t.Fatalf("no sidecar for %s under %s: %v", id, dir, err)
	}
	var sc map[string]any
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}
	return sc
}

// TestReactingToAProvider5xx: three fast provider 5xx failures retry twice and then file the
// task `end=provider`, with the provider's ref on the report line. Each launch's usage row
// carries its attempt number, for the one task.
func TestProvider5xxRetriesTwiceThenEndsProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	b := newBench(t)
	b.extraEnv = []string{"NOVA_SWARM_PROVIDER_BACKOFF=0s"}
	id := b.add("a card that dies at request start\nFAKE-LAUNCHES\nFAKE-5XX\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	if n := strings.Count(stdout, "RUN PROVIDER id="); n != 3 {
		t.Fatalf("a fast 5xx wants three launches (the first and its two retries), got %d:\n%s", n, stdout)
	}
	mustContain(t, "the final failure", stdout, "provider=err_fake_5xx")
	mustContain(t, "the final failure", stdout, "dest=failed")
	mustContain(t, "the final failure", stdout, "attempts=3")

	// The lineage: the first attempt is the task the person added; each retry carries
	// from= the attempt before and its own attempt number. The final one is filed.
	ids := []string{id}
	for {
		last := ids[len(ids)-1]
		sc := sidecarIn(t, b.pool, "failed", last)
		next, _ := sc["replaced_by"].(string)
		_ = next
		// The retry is a NEW id carrying from=last; find it by scanning failed/ and done/.
		nextID := findRetry(t, b.pool, last)
		if nextID == "" {
			break
		}
		ids = append(ids, nextID)
		if len(ids) > 4 {
			t.Fatal("the retry chain did not end")
		}
	}
	if len(ids) != 3 {
		t.Fatalf("a task wants three attempts before it is filed, got %d: %v", len(ids), ids)
	}
	for i, attemptID := range ids {
		row := poolUsageRow(t, b.pool, attemptID)
		if want := []string{"1", "2", "3"}[i]; row["attempt"] != want {
			t.Errorf("attempt %d of the task carries attempt=%s, want %s", i+1, row["attempt"], want)
		}
	}
	final := sidecarIn(t, b.pool, "failed", ids[2])
	if final["end"] != "provider" {
		t.Errorf("the filed task ends %v, want provider", final["end"])
	}
	if final["provider_ref"] != "err_fake_5xx" {
		t.Errorf("the filed task carries provider_ref=%v, want err_fake_5xx", final["provider_ref"])
	}
}

// findRetry returns the id of the task that names from=id, under failed/ or done/.
func findRetry(t *testing.T, pool, id string) string {
	t.Helper()
	for _, dir := range []string{"failed", "done"} {
		entries, err := os.ReadDir(filepath.Join(pool, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(pool, dir, e.Name()))
			if err != nil {
				continue
			}
			var sc struct {
				From string `json:"from"`
			}
			if json.Unmarshal(raw, &sc) == nil && sc.From == id {
				return strings.TrimSuffix(e.Name(), ".json")
			}
		}
	}
	return ""
}

// TestAProvider5xxPastTheGraceIsNotRetried: a failure that takes longer than the launch
// grace is a real run that failed, not a launch, and gets exactly one launch.
func TestProvider5xxPastTheGraceIsNotRetried(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a worker pool")
	}
	b := newBench(t)
	b.rewriteWorker(func(d map[string]any) { d["launch_grace"] = "1s" })
	b.extraEnv = []string{"NOVA_SWARM_PROVIDER_BACKOFF=0s"}
	id := b.add("a card that fails slowly\nFAKE-LAUNCHES\nFAKE-5XX-SLOW 2\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	if n := strings.Count(stdout, "RUN PROVIDER id="); n != 0 {
		t.Errorf("a slow failure is not a launch failure and earns no RUN PROVIDER line, got %d:\n%s", n, stdout)
	}
	if n := strings.Count(stdout, "RUN DONE id="); n != 1 {
		t.Errorf("a slow failure is not retried: one RUN DONE wanted, got %d:\n%s", n, stdout)
	}
	launches := b.jobFile(id, "launches")
	if n := strings.Count(strings.TrimRight(launches, "\n"), "\n") + 1; n != 1 {
		t.Errorf("a slow failure launched %d times, want 1", n)
	}
}

// TestNativeRetriesAProvider5xxLaunch: the native path retries a launch that dies inside the
// grace on a provider server error, keeps the same job, harvests the second attempt's result
// and writes one usage row per attempt for the one job.
func TestNativeRetriesAProvider5xxLaunch(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "retry-5xx"

	t.Setenv("NOVA_SWARM_PROVIDER_BACKOFF", "0s")

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a card that dies at request start\nFAKE-LAUNCHES\nFAKE-5XX-FIRST\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("native run exits 0, got %d:\n%s", code, errOut.String())
	}
	if res.rc != 0 {
		t.Fatalf("the second attempt succeeds and the run records rc=0, got %d:\n%s", res.rc, errOut.String())
	}
	// The harvested result is the second attempt's: the first died before publishing and
	// the second published, so RESULT.md exists in the job directory.
	jobDir := filepath.Join(slot, "jobs", label)
	if _, err := os.Stat(filepath.Join(jobDir, "RESULT.md")); err != nil {
		t.Errorf("the second attempt's result was not harvested: %v", err)
	}
	// One usage row per launch, attempt=1 and attempt=2, for the one job.
	raw, err := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(rows) != 3 {
		t.Fatalf("a retried card wants a header and two rows, got %d:\n%s", len(rows), raw)
	}
	head := strings.Split(rows[0], "\t")
	attemptAt := -1
	usdAt := -1
	for i, name := range head {
		switch name {
		case "attempt":
			attemptAt = i
		case "usd":
			usdAt = i
		}
	}
	if attemptAt < 0 || usdAt < 0 {
		t.Fatalf("the header does not carry attempt and usd:\n%s", rows[0])
	}
	if got := strings.Split(rows[1], "\t")[attemptAt]; got != "1" {
		t.Errorf("the first launch row carries attempt=%s, want 1", got)
	}
	if got := strings.Split(rows[2], "\t")[attemptAt]; got != "2" {
		t.Errorf("the second launch row carries attempt=%s, want 2", got)
	}
	if got := strings.Split(rows[1], "\t")[usdAt]; got != "-" {
		t.Errorf("an unreported usd stays a dash, got %q", got)
	}
}

// A lost response is one launch, and the usage row stays unknown. The fake
// harness prints the timeout and exits. It does not prove the installed
// OpenCode consumer honors headerTimeout; it proves this loop does not turn
// that tail into done or failed, and does not start a second launch.
func TestNativeLostResponseStaysUnknownAndLaunchesOnce(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "lost-response"
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("FAKE-LAUNCHES\nFAKE-LOST-RESPONSE\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("native run exits 0, got %d:\n%s", code, errOut.String())
	}
	if !res.lost || res.end != "unknown" {
		t.Fatalf("lost=%v end=%s, want a retained unknown", res.lost, res.end)
	}
	jobDir := filepath.Join(slot, "jobs", label)
	launches, err := os.ReadFile(filepath.Join(jobDir, "launches"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(launches), "launch"); n != 1 {
		t.Fatalf("a lost response launched %d times, want 1:\n%s", n, launches)
	}
	mark, err := os.ReadFile(filepath.Join(jobDir, "provider-acceptance"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mark) != "unknown\n" {
		t.Fatalf("provider-acceptance = %q, want unknown", mark)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "RESULT.md")); err == nil {
		t.Fatal("a lost response must not publish a result")
	}
}

func TestPersistUnknownFallsBackWhenTheMarkerCannotBeWritten(t *testing.T) {
	job := t.TempDir()
	if err := os.Mkdir(filepath.Join(job, "provider-acceptance"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(job, "harness-output.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := persistUnknown(job); err != nil {
		t.Fatal(err)
	}
	if !swarm.AcceptanceUnknown(job) {
		t.Fatal("the fallback log was not held")
	}
}

func TestUnrecordedUnknownIsStillAHarvestHold(t *testing.T) {
	windowsIsNotABench(t)
	prev := persistUnknownFn
	persistUnknownFn = func(string) error { return errors.New("disk full") }
	t.Cleanup(func() { persistUnknownFn = prev })

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "unrecorded"
	cardPath := filepath.Join(root, label+".md")
	if err := os.WriteFile(cardPath, []byte("FAKE-LOST-RESPONSE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if code == 0 {
		t.Fatalf("a failed record exited 0:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "why=unknown-acceptance") {
		t.Fatalf("the refusal returned before the unknown verdict:\n%s\n%s", stdout.String(), stderr.String())
	}

	harvestRoot := t.TempDir()
	job := filepath.Join(harvestRoot, "1", "jobs", label)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "harness.log"), stdout.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(harvestRoot, label+".md")
	if err := os.WriteFile(card, []byte("RESULT "+label+" sha=u\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harvestRoot, "cards.tsv"), []byte(label+"\t1\tflash\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var hout, herr bytes.Buffer
	hcode := pulse.Harvest(pulse.HarvestInput{
		ID: "p1", Root: harvestRoot, Templates: harvestRoot, MaxBodyBytes: 4096, Max: 20,
		Stdout: &hout, Stderr: &herr, Now: time.Now,
	})
	if hcode == 0 && !strings.Contains(herr.String(), "HARVEST HOLD") {
		t.Fatalf("harvest did not hold the unrecorded run:\ncode=%d\n%s\n%s", hcode, hout.String(), herr.String())
	}
	if raw, err := os.ReadFile(filepath.Join(harvestRoot, "retry.tsv")); err == nil && strings.Contains(string(raw), label) {
		t.Fatalf("harvest retried the unrecorded unknown:\n%s", raw)
	}
	if !strings.Contains(herr.String(), "unknown-acceptance") {
		t.Fatalf("harvest did not name the unknown:\n%s\n%s", hout.String(), herr.String())
	}
}

func TestPersistUnknownFailsWhenNothingCanBeWritten(t *testing.T) {
	job := t.TempDir()
	if err := os.Chmod(job, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(job, 0o755) })
	if err := persistUnknown(job); err == nil {
		t.Fatal("an unwritable job recorded the unknown")
	}
}

func TestNativeLostResponseLineSaysUnknownAcceptance(t *testing.T) {
	out := nativeVerdict(t, "lost", "FAKE-LOST-RESPONSE\n")
	if strings.Contains(out, "NATIVE OK") {
		t.Fatalf("a lost response said OK:\n%s", out)
	}
	if !strings.Contains(out, "why=unknown-acceptance") {
		t.Fatalf("the verdict did not keep unknown-acceptance:\n%s", out)
	}
	if strings.Contains(out, "why=no-result") || strings.Contains(out, "why=rc") {
		t.Fatalf("a lost response was filed as an ordinary incomplete:\n%s", out)
	}
}
