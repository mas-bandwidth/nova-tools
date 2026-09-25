package reconcile_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

type fakeGHJobClient struct {
	logs   map[int64]string
	reruns []int64
}

func (f *fakeGHJobClient) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	return f.logs[jobID], nil
}

func (f *fakeGHJobClient) RerunFailedJob(ctx context.Context, repo string, jobID int64) error {
	f.reruns = append(f.reruns, jobID)
	return nil
}

func TestCIDuty(t *testing.T) {
	ctx := context.Background()
	_, client := newSprint(t)
	st := store.New(client)

	client.SAdd(ctx, "sprints", "s1")
	client.HSet(ctx, "s:s1:pr:mas-bandwidth/nova-tools:42", "head", "abcdef")

	forge := &fakeGHJobClient{
		logs: map[int64]string{
			1: "some log with timeout budget spent in it",
			2: "some normal failure \n--- FAIL: TestXYZ",
			3: "exit code 2 with no test failure line",
			4: "toolchain/std mismatch",
			5: "signal: terminated",
			6: "runner lost",
		},
	}

	duty := &reconcile.CIDuty{
		Store:  st,
		GitHub: forge,
	}

	publishCheckRun := func(id int64, head string, name string) {
		payload := map[string]interface{}{
			"action": "completed",
			"repository": map[string]interface{}{
				"full_name": "mas-bandwidth/nova-tools",
			},
			"check_run": map[string]interface{}{
				"id":         id,
				"name":       name,
				"status":     "completed",
				"conclusion": "failure",
				"head_sha":   head,
				"pull_requests": []map[string]interface{}{
					{"number": 42},
				},
			},
		}
		b, _ := json.Marshal(payload)
		client.XAdd(ctx, &redis.XAddArgs{
			Stream: ghevent.Stream,
			Values: map[string]interface{}{
				"event":   "check_run",
				"payload": string(b),
			},
		})
	}

	publishCheckRun(1, "abcdef", "build1")
	publishCheckRun(2, "abcdef", "test")
	publishCheckRun(3, "abcdef", "build3")
	publishCheckRun(4, "abcdef", "build4")
	publishCheckRun(5, "abcdef", "build5")
	publishCheckRun(6, "abcdef", "build6")

	_, lines, err := duty.Pass(ctx, "test-instance")
	if err != nil {
		t.Fatalf("pass: %v", err)
	}

	if len(forge.reruns) != 5 {
		t.Fatalf("expected 5 reruns, got %v", forge.reruns)
	}

	if len(lines) != 5 || !strings.Contains(lines[0], "ci:: rerun=1 reason=timeout budget spent") {
		t.Fatalf("expected lines containing ci:: rerun=1 reason=..., got %v", lines)
	}

	// second failure is red (no rerun)
	publishCheckRun(1, "abcdef", "build1")
	_, lines, _ = duty.Pass(ctx, "test-instance")
	if len(forge.reruns) != 5 {
		t.Fatalf("expected no new rerun, got %v", forge.reruns)
	}
	if len(lines) != 0 {
		t.Fatalf("expected no lines, got %v", lines)
	}
}
