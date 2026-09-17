package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestNativeRunWritesTimeline: a native run timestamps the harness's own per-turn and
// per-tool report lines into <job>/timeline.tsv -- one row per model turn and per tool
// call, in the order the harness reported them, with the six columns the profile verb
// reads. A tool row carries no tokens (the harness gave none there), a turn row does.
func TestNativeRunWritesTimeline(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "timeline"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-TIMELINE\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("native run exits 0, got %d:\n%s", code, errOut.String())
	}

	path := filepath.Join(slot, "jobs", label, swarm.TimelineFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the native run wrote no timeline at %s: %v", path, err)
	}
	head := strings.SplitN(string(raw), "\n", 2)[0]
	if head != strings.Join(swarm.TimelineColumns, "\t") {
		t.Fatalf("timeline header = %q, want %q", head, strings.Join(swarm.TimelineColumns, "\t"))
	}

	rows, err := swarm.ReadTimeline(path)
	if err != nil {
		t.Fatalf("read the timeline back: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("%d timeline rows, want 6 (one turn, five tool calls):\n%s", len(rows), raw)
	}
	if rows[0].Tool != "model" || rows[0].InputTokens != "1200" || rows[0].OutputTokens != "340" {
		t.Errorf("turn row = %+v, want tool=model in=1200 out=340", rows[0])
	}
	// The tool calls, in report order, each with no tokens of its own.
	wants := []string{"git clone", "cat ", "go test", "go test", "result.md"}
	for i, want := range wants {
		r := rows[i+1]
		if !strings.Contains(strings.ToLower(r.Tool), want) {
			t.Errorf("tool row %d = %q, want it to name %q", i, r.Tool, want)
		}
		if r.InputTokens != "" || r.OutputTokens != "" {
			t.Errorf("tool row %d carries tokens %q/%q, want both empty (the harness gave none)", i, r.InputTokens, r.OutputTokens)
		}
	}
	// Every span is a real, non-negative duration.
	for i, r := range rows {
		if r.End.Before(r.Start) {
			t.Errorf("row %d ends before it starts: %s < %s", i, r.End, r.Start)
		}
	}
}

// timelineFile is a job's hand-written timeline: the columns the native run writes, with
// known phase durations so the profile line's arithmetic is exact.
func timelineFile(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, swarm.TimelineFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProfilePrintsPhases: `nova-swarm profile --jobs <glob>` prints one PROFILE line per
// job and one summary line, with every phase inferred from the tool call's command and its
// seconds, including a test run after a failing test run counted as retry.
func TestProfilePrintsPhases(t *testing.T) {
	root := t.TempDir()
	// job-a: clone, read, edit, a failing test, a passing test (retry), result and deps.
	timelineFile(t, filepath.Join(root, "job-a"),
		"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n"+
			"2026-09-17T00:00:00Z\t2026-09-17T00:00:10Z\tmodel\t10000\t100\t20\n"+
			"2026-09-17T00:00:10Z\t2026-09-17T00:00:35Z\tbash git clone https://github.com/x/y\t25000\t\t\n"+
			"2026-09-17T00:00:35Z\t2026-09-17T00:00:40Z\tRead internal/swarm/usage.go\t5000\t\t\n"+
			"2026-09-17T00:00:40Z\t2026-09-17T00:00:45Z\tEdit internal/swarm/usage.go\t5000\t\t\n"+
			"2026-09-17T00:00:45Z\t2026-09-17T00:00:55Z\tbash go test ./... rc=1\t10000\t\t\n"+
			"2026-09-17T00:00:55Z\t2026-09-17T00:01:02Z\tbash go test ./... rc=0\t7000\t\t\n"+
			"2026-09-17T00:01:02Z\t2026-09-17T00:01:05Z\tedit Write RESULT.md\t3000\t\t\n"+
			"2026-09-17T00:01:05Z\t2026-09-17T00:01:09Z\tbash go mod tidy\t4000\t\t\n")
	// job-b: one clone only.
	timelineFile(t, filepath.Join(root, "job-b"),
		"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n"+
			"2026-09-17T01:00:00Z\t2026-09-17T01:00:20Z\tbash git fetch origin\t20000\t\t\n")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"profile", "--jobs", filepath.Join(root, "job-*")},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("profile exits 0, got %d:\n%s", rc, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"PROFILE job=job-a", "turns=1", "tools=7",
		"clone=25.0", "deps=4.0", "read=5.0", "edit=5.0", "test=10.0", "retry=7.0", "result=3.0",
		"PROFILE job=job-b",
		"PROFILE SUMMARY jobs=2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("profile output does not name %q:\n%s", want, out)
		}
	}
}
