package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedStore writes a run store with runs at varied states, the fixture the
// "Tests this spec demands" section names (15-18, nova-tools #2207): one run
// queued before the boundary, and after it a completed run, a run cancelled
// while running, a run that passed on its second attempt after a failed
// first, and a run still waiting in the queue.
func seedStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runs := map[string]string{
		"old.json":     `{"id":"old","state":"completed","queued_at":"2026-09-20T09:00:00Z","started_at":"2026-09-20T09:00:10Z","finished_at":"2026-09-20T09:01:00Z","attempts":[{"id":"old.a1","state":"completed"}]}`,
		"done.json":    `{"id":"done","state":"completed","queued_at":"2026-09-23T10:00:00Z","started_at":"2026-09-23T10:00:30Z","finished_at":"2026-09-23T10:02:30Z","attempts":[{"id":"done.a1","state":"completed"}]}`,
		"cancel.json":  `{"id":"cancel","state":"cancelled","queued_at":"2026-09-23T10:05:00Z","started_at":"2026-09-23T10:05:05Z","cancel_requested_at":"2026-09-23T10:06:00Z","cancelled_at":"2026-09-23T10:06:45Z","attempts":[{"id":"cancel.a1","state":"cancelled"}]}`,
		"retry.json":   `{"id":"retry","state":"completed","queued_at":"2026-09-23T10:10:00Z","started_at":"2026-09-23T10:10:20Z","finished_at":"2026-09-23T10:14:20Z","attempts":[{"id":"retry.a1","state":"failed","failure":"lint"},{"id":"retry.a2","state":"completed"}]}`,
		"waiting.json": `{"id":"waiting","state":"queued","queued_at":"2026-09-23T10:20:00Z"}`,
	}
	for name, body := range runs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// status runs the verb over the seeded store and returns one line per run,
// keyed by id, plus the summary line.
func status(t *testing.T, store string, extra ...string) (map[string]string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"status", "--store", store, "--since", "2026-09-23T00:00:00Z", "--now", "2026-09-23T10:30:00Z"}, extra...)
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("nova-test %s exits %d, want 0; stderr: %s", strings.Join(args, " "), code, errb.String())
	}
	runs := map[string]string{}
	var summary string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		switch {
		case strings.HasPrefix(line, "STATUS RUN "):
			id := field(line, "id")
			if _, dup := runs[id]; dup {
				t.Fatalf("run %s listed twice:\n%s", id, out.String())
			}
			runs[id] = line
		case strings.HasPrefix(line, "STATUS OK "):
			summary = line
		default:
			t.Fatalf("unexpected line %q in:\n%s", line, out.String())
		}
	}
	return runs, summary
}

// field returns the value of key=value in a one-line record, or "" if absent.
func field(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

func wantField(t *testing.T, line, key, want string) {
	t.Helper()
	if got := field(line, key); got != want {
		t.Errorf("%s=%q, want %q in:\n%s", key, got, want, line)
	}
}

// 15. status --since surveys recent runs: only runs queued at or after the
// boundary are listed, in queue order, and the summary counts what was left out.
func TestStatusSinceSurveysRecentRuns(t *testing.T) {
	runs, summary := status(t, seedStore(t))
	if _, listed := runs["old"]; listed {
		t.Errorf("run old was queued before --since and is listed:\n%s", runs["old"])
	}
	for _, id := range []string{"done", "cancel", "retry", "waiting"} {
		if _, listed := runs[id]; !listed {
			t.Errorf("run %s was queued after --since and is missing; listed: %v", id, keys(runs))
		}
	}
	wantField(t, summary, "runs", "4")
	wantField(t, summary, "older", "1")
	wantField(t, summary, "since", "2026-09-23T00:00:00Z")

	// A duration is a window back from the clock: 15m before 10:30 keeps only
	// the run queued at 10:20.
	runs, _ = status(t, seedStore(t), "--since", "15m")
	if len(runs) != 1 || runs["waiting"] == "" {
		t.Errorf("--since 15m listed %v, want only waiting", keys(runs))
	}
}

// 16. status reports queue and cancellation drain per run.
func TestStatusReportsQueueAndCancellationDrain(t *testing.T) {
	runs, _ := status(t, seedStore(t))
	wantField(t, runs["done"], "queue", "30s")
	wantField(t, runs["done"], "drain", "-")
	wantField(t, runs["cancel"], "queue", "5s")
	wantField(t, runs["cancel"], "drain", "45s")
	// Still queued: the wait so far, measured to the clock, marked open.
	wantField(t, runs["waiting"], "queue", "10m0s+")
	wantField(t, runs["waiting"], "state", "queued")
}

// 17. status reports execution and end-to-end latency per run.
func TestStatusReportsLatency(t *testing.T) {
	runs, _ := status(t, seedStore(t))
	wantField(t, runs["done"], "exec", "2m0s")
	wantField(t, runs["done"], "e2e", "2m30s")
	wantField(t, runs["retry"], "exec", "4m0s")
	wantField(t, runs["retry"], "e2e", "4m20s")
	// Cancelled: execution ends at the cancellation, not at a finish it never had.
	wantField(t, runs["cancel"], "exec", "1m40s")
	wantField(t, runs["cancel"], "e2e", "1m45s")
	// Never started: no execution to measure, and the end-to-end wait is open.
	wantField(t, runs["waiting"], "exec", "-")
	wantField(t, runs["waiting"], "e2e", "10m0s+")
}

// 18. attempt identities and prior failures are preserved: a run that passed
// on its second attempt still names both attempts and the failure before it.
func TestStatusPreservesAttemptsAndPriorFailures(t *testing.T) {
	runs, _ := status(t, seedStore(t))
	wantField(t, runs["retry"], "attempts", "retry.a1,retry.a2")
	wantField(t, runs["retry"], "prior_failures", "retry.a1:lint")
	wantField(t, runs["retry"], "state", "completed")
	wantField(t, runs["done"], "attempts", "done.a1")
	wantField(t, runs["done"], "prior_failures", "-")
	wantField(t, runs["waiting"], "attempts", "-")
}

// A run file that does not parse is named, not skipped: a survey that
// silently dropped a run would report a healthier queue than the one there is.
func TestStatusRefusesAnUnreadableRun(t *testing.T) {
	store := seedStore(t)
	if err := os.WriteFile(filepath.Join(store, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"status", "--store", store, "--since", "2026-09-23T00:00:00Z"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "broken.json") {
		t.Fatalf("exit %d stderr %q, want 2 naming broken.json", code, errb.String())
	}
}

// --store and --since are required: no default store, no default window.
func TestStatusRefusesMissingFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"status"}, &out, &errb); code != 2 {
		t.Fatalf("bare status exits %d, want 2", code)
	}
	for _, flag := range []string{"--store", "--since"} {
		if !strings.Contains(errb.String(), flag) {
			t.Errorf("refusal does not name %s: %s", flag, errb.String())
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
