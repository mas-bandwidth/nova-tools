package merge

// nova-tools#2185: Emit merge-queue enqueue and group events.
//
// docs/SPEC-LOGS.md, "The merge queue": "`enqueue` with the PR and head sha,
// `group_start` with the group name and the run id, `group_verdict` with the
// conclusion, the failing test and the poison verdict, and `park` when a PR is
// set aside with the reason and the age."
//
// This test drives Enqueue and one merge-group run through a verdict over a
// temp lane and asserts one JSON line per event carrying the spec's own nouns,
// beside the durable store (queue.json) rather than instead of it.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestIssue2185 is the card anchor for nova-tools#2185; the scenario itself is
// TestMergeEnqueueAndGroupEvents, the test the issue asks for.
func TestIssue2185(t *testing.T) {
	mergeEnqueueAndGroupEvents(t)
}

// TestMergeEnqueueAndGroupEvents drives Enqueue and one group through a
// verdict over a temp repo and asserts the four event lines.
func TestMergeEnqueueAndGroupEvents(t *testing.T) {
	mergeEnqueueAndGroupEvents(t)
}

func mergeEnqueueAndGroupEvents(t *testing.T) {
	t.Helper()

	lane := t.TempDir()
	if err := Init(lane, LaneConfig{
		Repo:       "example.invalid/oak/repo",
		Base:       "dev",
		LaneBranch: "refs/heads/nova-merge/lane",
	}); err != nil {
		t.Fatalf("Init(lane): %v", err)
	}

	var buf bytes.Buffer
	ev := &Events{Sink: &buf}

	// One admission through the one door.
	host := newFakeEnqueueHost()
	host.ids[1341] = "PR_integration6"
	head := strings.Repeat("a", 40)
	receipt := "BATCH OK name=integration-6 base=" + strings.Repeat("d", 40) + " head=" + head + " members=1341 dropped=none"
	door := NewEnqueuer(host)
	door.Events = ev
	if err := door.Enqueue(context.Background(),
		EnqueuePR{Number: 1341, HeadRef: "rowan/integration-6", HeadSHA: head, Receipt: receipt}, true); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// One merge-group run through a verdict, read off the fake host the way
	// `classify` reads it.
	forge := NewFakeHost()
	forge.MergeGroupRuns[int64(42)] = MergeRun{
		ID: 42, Event: "merge_group", PR: 77,
		Jobs: []RunJob{{
			Name: "ci-ok", Runner: "ubuntu-latest", Package: "internal/pulse",
			Tests: []string{"TestRefillCounts"}, Changed: false,
		}},
	}
	run, err := forge.MergeGroupRun(42)
	if err != nil {
		t.Fatalf("MergeGroupRun(42): %v", err)
	}
	now := time.Date(2026, 9, 22, 23, 0, 0, 0, time.UTC)
	got, err := RecordGroupVerdict(lane, "dev", run,
		[]Failure{{Test: "TestRefillCounts", Package: "internal/pulse", Count: 2}},
		map[string]bool{"internal/pulse": true},
		ClassOwnChange, now, ev)
	if err != nil {
		t.Fatalf("RecordGroupVerdict: %v", err)
	}
	if !got.Parked {
		t.Fatalf("an armed own-change failure should park PR 77, got %+v", got)
	}

	// Four lines, one JSON object each, in emission order.
	raw := strings.TrimRight(buf.String(), "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 event lines, got %d:\n%s", len(lines), raw)
	}
	parsed := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("line %d is blank", i)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i, err, line)
		}
		parsed = append(parsed, m)
	}

	// `enqueue` with the PR and head sha.
	if parsed[0]["event"] != "enqueue" {
		t.Errorf("line 0 event = %v, want enqueue", parsed[0]["event"])
	}
	if parsed[0]["pr"] != float64(1341) {
		t.Errorf("enqueue pr = %v, want 1341", parsed[0]["pr"])
	}
	if parsed[0]["head"] != head {
		t.Errorf("enqueue head = %v, want the head sha", parsed[0]["head"])
	}

	// `group_start` with the group name and the run id.
	if parsed[1]["event"] != "group_start" {
		t.Errorf("line 1 event = %v, want group_start", parsed[1]["event"])
	}
	if parsed[1]["group"] != "dev" {
		t.Errorf("group_start group = %v, want dev", parsed[1]["group"])
	}
	if parsed[1]["run"] != float64(42) {
		t.Errorf("group_start run = %v, want 42", parsed[1]["run"])
	}

	// `group_verdict` with the conclusion, the failing test and the poison verdict.
	if parsed[2]["event"] != "group_verdict" {
		t.Errorf("line 2 event = %v, want group_verdict", parsed[2]["event"])
	}
	if parsed[2]["conclusion"] != "failure" {
		t.Errorf("group_verdict conclusion = %v, want failure", parsed[2]["conclusion"])
	}
	if parsed[2]["failing_test"] != "TestRefillCounts" {
		t.Errorf("group_verdict failing_test = %v, want TestRefillCounts", parsed[2]["failing_test"])
	}
	if parsed[2]["poison_verdict"] != ClassOwnChange {
		t.Errorf("group_verdict poison_verdict = %v, want %s", parsed[2]["poison_verdict"], ClassOwnChange)
	}

	// `park` with the reason and the age.
	if parsed[3]["event"] != "park" {
		t.Errorf("line 3 event = %v, want park", parsed[3]["event"])
	}
	reason, _ := parsed[3]["reason"].(string)
	if !strings.Contains(reason, "TestRefillCounts") {
		t.Errorf("park reason = %q, want it to name the failing test", reason)
	}
	age, _ := parsed[3]["age"].(string)
	if strings.TrimSpace(age) == "" {
		t.Errorf("park age is missing: %v", parsed[3])
	}

	// The durable store stays the record: the park is in queue.json.
	st, err := Load(lane)
	if err != nil {
		t.Fatalf("Load(lane): %v", err)
	}
	q, err := LoadQueue(lane, st)
	if err != nil {
		t.Fatalf("LoadQueue: %v", err)
	}
	if !q.IsParked(77) {
		t.Fatalf("PR 77 is not parked in queue.json after the verdict: %+v", q.Parked)
	}
	var rec *Park
	for i := range q.Parked {
		if q.Parked[i].PR == 77 {
			rec = &q.Parked[i]
		}
	}
	if rec == nil || rec.Test != "TestRefillCounts" || rec.Package != "internal/pulse" || rec.Runs != 2 {
		t.Fatalf("queue.json park record is not the verdict's: %+v", q.Parked)
	}
}
