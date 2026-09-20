package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A LAUNCH THAT DIES FAST ON A PROVIDER 5XX IS RETRIED (issue #900). The provider answered
// before the work began and the slot was spent on nothing; these tests hold the three facts
// the card names -- the retry keeps the task, the usage rows carry attempt=1,2,3, and a slow
// failure is not retried -- against the fake harness and no provider.

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

// TestNativeDoesNotRetryAProvider5xxLaunch: SPEC-AHEAD (#2078 split) — in-place
// native retry is disabled pending the shared total-attempt budget. A fast 5xx
// that would have retried (FAKE-5XX-FIRST) is one launch, no RESULT, end=provider.
func TestNativeDoesNotRetryAProvider5xxLaunch(t *testing.T) {
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
	if res.rc == 0 {
		t.Fatalf("in-place retry is disabled so the first 5xx is the run, want rc!=0, got %d:\n%s", res.rc, errOut.String())
	}
	if res.end != swarm.EndProvider {
		t.Fatalf("end=%s, want provider", res.end)
	}
	jobDir := filepath.Join(slot, "jobs", label)
	if _, err := os.Stat(filepath.Join(jobDir, "RESULT.md")); err == nil {
		t.Fatal("the first 5xx must not publish RESULT.md when retry is disabled")
	}
	launchesRaw, err := os.ReadFile(filepath.Join(jobDir, "launches"))
	if err != nil {
		t.Fatalf("launches record missing: %v", err)
	}
	if n := strings.Count(string(launchesRaw), "launch"); n != 1 {
		t.Fatalf("automatic in-place retry must be disabled, got %d launches, want 1", n)
	}
	raw, err := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(rows) != 2 {
		t.Fatalf("one launch wants a header and one row, got %d:\n%s", len(rows), raw)
	}
}
