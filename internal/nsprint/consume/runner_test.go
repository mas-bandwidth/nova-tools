package consume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func newTestBus(t *testing.T) (*store.Store, *redis.Client, interface{}) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 200})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client, nil
}

func newTestPRToReadRule(st *store.Store, sprint string, out *bytes.Buffer) *PRToReadRule {
	var w io.Writer = io.Discard
	if out != nil {
		w = out
	}
	return &PRToReadRule{
		Store:    st,
		Sprint:   sprint,
		Consumer: "c1",
		Out:      w,
		Block:    -1, // do not block in tests
	}
}

func publishCheckRun(t *testing.T, client *redis.Client, repo, head, check, checkRunID, action, status, conclusion, at string) string {
	t.Helper()
	id, err := client.XAdd(context.Background(), &redis.XAddArgs{
		Stream: ghevent.Stream,
		Values: map[string]interface{}{
			"repo":         repo,
			"kind":         "check_run",
			"head":         head,
			"action":       action,
			"check":        check,
			"check_run_id": checkRunID,
			"status":       status,
			"conclusion":   conclusion,
			"at":           at,
		},
	}).Result()
	if err != nil {
		t.Fatalf("publish check_run: %v", err)
	}
	return id
}

func seedFriend(t *testing.T, client *redis.Client, sprint, name string, up bool, slots int) {
	t.Helper()
	ctx := context.Background()
	_ = client.SAdd(ctx, "friends", name).Err()
	if up {
		_ = client.HSet(ctx, "friend:"+name+":beat", "host", "h1").Err()
		_ = client.HSet(ctx, "friend:"+name+":desired", "slots", strconv.Itoa(slots), "paused", "0").Err()
	}
}

func seedPR(t *testing.T, client *redis.Client, sprint, repo string, pr int, head, base, author, state, draft string) {
	t.Helper()
	ctx := context.Background()
	prID := fmt.Sprintf("%s#%d", repo, pr)
	_ = client.SAdd(ctx, "s:"+sprint+":prs", prID).Err()
	prKey := fmt.Sprintf("s:%s:pr:%s:%d", sprint, repo, pr)
	_ = client.HSet(ctx, prKey,
		"head", head, "base", base, "author", author,
		"state", state, "draft", draft, "priority", "0",
	).Err()
}

type storedRunnerRow struct {
	Gen        int    `json:"gen"`
	CheckRunID int64  `json:"check_run_id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RereqID    int64  `json:"rereq_id"`
	RereqAt    string `json:"rereq_at"`
	Source     string `json:"source"`
	At         string `json:"at"`
}

func getRunnerRow(t *testing.T, client *redis.Client, repo, sha, row string) (storedRunnerRow, bool) {
	t.Helper()
	raw, err := client.HGet(context.Background(), "ci:"+repo+":"+sha, "runner:"+row).Result()
	if errors.Is(err, redis.Nil) || raw == "" {
		return storedRunnerRow{}, false
	}
	if err != nil {
		t.Fatalf("HGET runner:%s: %v", row, err)
	}
	var res storedRunnerRow
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("unmarshal runner:%s: %v", row, err)
	}
	return res, true
}

func getCIFields(t *testing.T, client *redis.Client, repo, sha string) map[string]string {
	t.Helper()
	m, err := client.HGetAll(context.Background(), "ci:"+repo+":"+sha).Result()
	if err != nil {
		t.Fatalf("HGETALL ci:%s:%s: %v", repo, sha, err)
	}
	return m
}

type failTransport struct{}

func (f *failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("http requests not allowed")
}

func TestControl33PrToRead(t *testing.T) {
	const (
		repo = "mas-bandwidth/nova-tools"
		sha1 = "2222222222222222222222222222222222222222"
		t1   = "2026-09-22T16:40:00Z"
		t2   = "2026-09-22T16:45:00Z"
		t3   = "2026-09-22T16:50:00Z"
		t4   = "2026-09-22T16:55:00Z"
		t5   = "2026-09-22T17:00:00Z"
	)

	t.Run("windows_green_one_key", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass: %v", err)
		}

		row, ok := getRunnerRow(t, client, repo, sha1, "windows")
		if !ok {
			t.Fatal("expected runner:windows to be stored")
		}
		if row.Gen != 0 || row.CheckRunID != 100 || row.Status != "completed" || row.Conclusion != "success" {
			t.Fatalf("unexpected runner row: %+v", row)
		}

		ci := getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"windows"})
		if !ready || len(missing) != 0 {
			t.Fatalf("RunnerReady = (%v, %v), want (true, nil)", ready, missing)
		}
	})

	t.Run("cancelled_row_missing", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "s390x").Err()

		publishCheckRun(t, client, repo, sha1, "s390x", "100", "completed", "completed", "cancelled", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass: %v", err)
		}

		ci := getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"s390x"})
		if ready || len(missing) != 1 || missing[0] != "s390x" {
			t.Fatalf("RunnerReady = (%v, %v), want (false, [s390x])", ready, missing)
		}
	})

	t.Run("unnamed_row_not_copied", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "linux", "100", "completed", "completed", "success", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass: %v", err)
		}

		_, ok := getRunnerRow(t, client, repo, sha1, "linux")
		if ok {
			t.Fatal("expected runner:linux to not be copied")
		}
	})

	t.Run("success_then_cancelled_replaces", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 1: %v", err)
		}

		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "101", "completed", "completed", "cancelled", t2)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 2: %v", err)
		}

		if !strings.Contains(buf.String(), "REPLACED") {
			t.Fatalf("expected output to contain REPLACED, got: %s", buf.String())
		}

		row, ok := getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.CheckRunID != 101 || row.Conclusion != "cancelled" {
			t.Fatalf("unexpected row: %+v", row)
		}

		ci := getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("RunnerReady = (%v, %v), want (false, [windows])", ready, missing)
		}
	})

	t.Run("success_then_skipped_replaces", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 1: %v", err)
		}

		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "101", "completed", "completed", "skipped", t2)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 2: %v", err)
		}

		if !strings.Contains(buf.String(), "REPLACED") {
			t.Fatalf("expected output to contain REPLACED, got: %s", buf.String())
		}

		row, ok := getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.CheckRunID != 101 || row.Conclusion != "skipped" {
			t.Fatalf("unexpected row: %+v", row)
		}

		ci := getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("RunnerReady = (%v, %v), want (false, [windows])", ready, missing)
		}
	})

	t.Run("rerun_queued_replaces", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 1: %v", err)
		}

		publishCheckRun(t, client, repo, sha1, "windows", "101", "created", "queued", "", t2)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 2: %v", err)
		}

		row, ok := getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.CheckRunID != 101 || row.Status != "queued" {
			t.Fatalf("unexpected row after queued: %+v", row)
		}

		ci := getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("RunnerReady while queued = (%v, %v), want (false, [windows])", ready, missing)
		}

		publishCheckRun(t, client, repo, sha1, "windows", "101", "completed", "completed", "success", t3)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 3: %v", err)
		}

		ci = getCIFields(t, client, repo, sha1)
		ready, missing = RunnerReady(ci, []string{"windows"})
		if !ready || len(missing) != 0 {
			t.Fatalf("RunnerReady after completed success = (%v, %v), want (true, nil)", ready, missing)
		}
	})

	t.Run("older_attempt_kept", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "windows", "101", "completed", "completed", "cancelled", t2)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 1: %v", err)
		}

		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 2: %v", err)
		}

		if !strings.Contains(buf.String(), "KEPT") {
			t.Fatalf("expected output to contain KEPT, got: %s", buf.String())
		}

		row, ok := getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.CheckRunID != 101 || row.Conclusion != "cancelled" {
			t.Fatalf("unexpected row after late entry: %+v", row)
		}

		ci := getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("RunnerReady = (%v, %v), want (false, [windows])", ready, missing)
		}
	})

	t.Run("rerequested_same_id_old_payload", func(t *testing.T) {
		// Variant 1
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		// 1. Windows attempt 100 completed/success at T1 is ready.
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 1: %v", err)
		}
		ci := getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"windows"})
		if !ready {
			t.Fatalf("expected ready at T1")
		}

		// 2. A rerequested entry for id 100 arrives carrying the old completed/success payload at T1.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "rerequested", "completed", "success", t1)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 2: %v", err)
		}
		if !strings.Contains(buf.String(), "RERUN") {
			t.Fatalf("expected RERUN in output, got: %s", buf.String())
		}
		row, ok := getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.Gen != 1 || row.Status != "rerequested" {
			t.Fatalf("expected gen=1 status=rerequested, got %+v", row)
		}
		ci = getCIFields(t, client, repo, sha1)
		ready, missing = RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("expected missing=[windows], got ready=%v missing=%v", ready, missing)
		}

		// 3. A redelivery of that same rerequested entry is KEPT, with gen still 1.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "rerequested", "completed", "success", t1)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 3: %v", err)
		}
		if !strings.Contains(buf.String(), "KEPT") {
			t.Fatalf("expected KEPT in output on redelivery, got: %s", buf.String())
		}
		row, _ = getRunnerRow(t, client, repo, sha1, "windows")
		if row.Gen != 1 {
			t.Fatalf("expected gen=1, got %d", row.Gen)
		}

		// 4. A late copy of the old completed/success at T1 is KEPT, and the row stays MISSING.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 4: %v", err)
		}
		if !strings.Contains(buf.String(), "KEPT") {
			t.Fatalf("expected KEPT for late old payload, got: %s", buf.String())
		}
		ci = getCIFields(t, client, repo, sha1)
		ready, missing = RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("expected missing=[windows], got ready=%v missing=%v", ready, missing)
		}

		// 5. Id 100 completed/success at T2 > T1 makes the row ready (gen=1).
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t2)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 5: %v", err)
		}
		row, _ = getRunnerRow(t, client, repo, sha1, "windows")
		if row.Gen != 1 || row.Status != "completed" || row.Conclusion != "success" {
			t.Fatalf("expected gen=1 completed/success, got %+v", row)
		}
		ci = getCIFields(t, client, repo, sha1)
		ready, missing = RunnerReady(ci, []string{"windows"})
		if !ready || len(missing) != 0 {
			t.Fatalf("expected ready, got ready=%v missing=%v", ready, missing)
		}

		// Variant 2: new attempt arriving as id 101 created/queued (MISSING) and then completed/success (ready)
		{
			st2, client2, _ := newTestBus(t)
			_ = client2.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()
			publishCheckRun(t, client2, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
			rule2 := newTestPRToReadRule(st2, sprint, nil)
			_ = rule2.Start(ctx)
			_, _ = rule2.Pass(ctx)

			// rerequest id 100 at T1
			publishCheckRun(t, client2, repo, sha1, "windows", "100", "rerequested", "completed", "success", t1)
			_, _ = rule2.Pass(ctx)

			// new attempt id 101 created/queued at T2 > T1
			publishCheckRun(t, client2, repo, sha1, "windows", "101", "created", "queued", "", t2)
			_, _ = rule2.Pass(ctx)
			ci2 := getCIFields(t, client2, repo, sha1)
			ready2, missing2 := RunnerReady(ci2, []string{"windows"})
			if ready2 || len(missing2) != 1 || missing2[0] != "windows" {
				t.Fatalf("variant 2: expected missing, got ready=%v missing=%v", ready2, missing2)
			}

			// id 101 completed/success at T3
			publishCheckRun(t, client2, repo, sha1, "windows", "101", "completed", "completed", "success", t3)
			_, _ = rule2.Pass(ctx)
			ci2 = getCIFields(t, client2, repo, sha1)
			ready2, missing2 = RunnerReady(ci2, []string{"windows"})
			if !ready2 || len(missing2) != 0 {
				t.Fatalf("variant 2: expected ready, got ready=%v missing=%v", ready2, missing2)
			}
		}

		// Variant 3: ends the new generation completed/failure, which gives FAIL and never the old green
		{
			st3, client3, _ := newTestBus(t)
			_ = client3.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()
			publishCheckRun(t, client3, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
			rule3 := newTestPRToReadRule(st3, sprint, nil)
			_ = rule3.Start(ctx)
			_, _ = rule3.Pass(ctx)

			// rerequest id 100 at T1
			publishCheckRun(t, client3, repo, sha1, "windows", "100", "rerequested", "completed", "success", t1)
			_, _ = rule3.Pass(ctx)

			// new attempt completed/failure at T2 > T1
			publishCheckRun(t, client3, repo, sha1, "windows", "100", "completed", "completed", "failure", t2)
			_, _ = rule3.Pass(ctx)
			ci3 := getCIFields(t, client3, repo, sha1)
			ready3, missing3 := RunnerReady(ci3, []string{"windows"})
			if ready3 || len(missing3) != 1 || missing3[0] != "windows" {
				t.Fatalf("variant 3: expected FAIL missing=[windows], got ready=%v missing=%v", ready3, missing3)
			}
		}
	})

	t.Run("rerequested_then_queued_then_completed_same_id", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// 1. Id 100 completed/success at T1 is ready.
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 1: %v", err)
		}
		ci := getCIFields(t, client, repo, sha1)
		ready, _ := RunnerReady(ci, []string{"windows"})
		if !ready {
			t.Fatalf("step 1: expected ready")
		}

		// 2. rerequested id 100 arrives with the old payload at T1. The line says RERUN, the field is gen=1, status=rerequested, and the row is MISSING.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "rerequested", "completed", "success", t1)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 2: %v", err)
		}
		if !strings.Contains(buf.String(), "RERUN") {
			t.Fatalf("step 2: expected RERUN, got: %s", buf.String())
		}
		row, ok := getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.Gen != 1 || row.Status != "rerequested" {
			t.Fatalf("step 2: expected gen=1 status=rerequested, got %+v", row)
		}
		ci = getCIFields(t, client, repo, sha1)
		ready, missing := RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("step 2: expected missing, got ready=%v missing=%v", ready, missing)
		}

		// 3. Id 100 created/queued arrives at T2 > T1. The line says REPLACED, the field is gen=1, status=queued, and the row is MISSING.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "created", "queued", "", t2)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 3: %v", err)
		}
		if !strings.Contains(buf.String(), "REPLACED") {
			t.Fatalf("step 3: expected REPLACED, got: %s", buf.String())
		}
		row, ok = getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.Gen != 1 || row.Status != "queued" {
			t.Fatalf("step 3: expected gen=1 status=queued, got %+v", row)
		}
		ci = getCIFields(t, client, repo, sha1)
		ready, missing = RunnerReady(ci, []string{"windows"})
		if ready || len(missing) != 1 || missing[0] != "windows" {
			t.Fatalf("step 3: expected missing, got ready=%v missing=%v", ready, missing)
		}

		// 4. Id 100 completed/success arrives at T3 > T2. The line says REPLACED and the row is ready.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t3)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 4: %v", err)
		}
		if !strings.Contains(buf.String(), "REPLACED") {
			t.Fatalf("step 4: expected REPLACED, got: %s", buf.String())
		}
		ci = getCIFields(t, client, repo, sha1)
		ready, missing = RunnerReady(ci, []string{"windows"})
		if !ready || len(missing) != 0 {
			t.Fatalf("step 4: expected ready, got ready=%v missing=%v", ready, missing)
		}

		// 5. Id 101 completed/success arrives at T4. The line says REPLACED and the field's check_run_id is 101.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "101", "completed", "completed", "success", t4)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 5: %v", err)
		}
		if !strings.Contains(buf.String(), "REPLACED") {
			t.Fatalf("step 5: expected REPLACED, got: %s", buf.String())
		}
		row, ok = getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.CheckRunID != 101 {
			t.Fatalf("step 5: expected check_run_id=101, got %+v", row)
		}

		// 6. A non-bumping rerequested for id 100 arrives at T5. The line says KEPT, the field is unchanged (gen=1, id 101), and the row is still ready.
		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "rerequested", "completed", "success", t5)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 6: %v", err)
		}
		if !strings.Contains(buf.String(), "KEPT") {
			t.Fatalf("step 6: expected KEPT, got: %s", buf.String())
		}
		row, ok = getRunnerRow(t, client, repo, sha1, "windows")
		if !ok || row.Gen != 1 || row.CheckRunID != 101 {
			t.Fatalf("step 6: expected gen=1 id=101, got %+v", row)
		}
		ci = getCIFields(t, client, repo, sha1)
		ready, missing = RunnerReady(ci, []string{"windows"})
		if !ready || len(missing) != 0 {
			t.Fatalf("step 6: expected ready, got ready=%v missing=%v", ready, missing)
		}
	})

	t.Run("redelivery_noop", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		_ = rule.Start(ctx)
		_, _ = rule.Pass(ctx)

		buf.Reset()
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
		_, _ = rule.Pass(ctx)

		if !strings.Contains(buf.String(), "KEPT") {
			t.Fatalf("expected KEPT for redelivery, got: %s", buf.String())
		}
	})

	t.Run("drain_one_pipeline", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// Publish 5 entries: 3 windows check_runs, 1 linux check_run (not in runner_rows), 1 pull_request
		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)
		publishCheckRun(t, client, repo, sha1, "windows", "101", "completed", "completed", "success", t2)
		publishCheckRun(t, client, repo, sha1, "linux", "200", "completed", "completed", "success", t3)
		_, _ = client.XAdd(ctx, &redis.XAddArgs{
			Stream: ghevent.Stream,
			Values: map[string]interface{}{"repo": repo, "kind": "pull_request", "action": "opened", "number": "42"},
		}).Result()
		publishCheckRun(t, client, repo, sha1, "windows", "102", "completed", "completed", "success", t4)

		type rtTracker struct {
			recording bool
			events    []string
		}
		tracker := &rtTracker{}

		trackClient := client.WithTimeout(30 * time.Second)
		trackClient.AddHook(&roundTripHook{
			onCmd: func(cmd redis.Cmder) {
				name := strings.ToLower(cmd.Name())
				if name == "xreadgroup" {
					if xcmd, ok := cmd.(*redis.XStreamSliceCmd); ok {
						val := xcmd.Val()
						if len(val) > 0 && len(val[0].Messages) > 0 {
							tracker.recording = true
							tracker.events = append(tracker.events, fmt.Sprintf("xreadgroup:%d", len(val[0].Messages)))
							return
						}
					}
				}
				if tracker.recording {
					tracker.events = append(tracker.events, name)
					if name == "xreadgroup" {
						tracker.recording = false
					}
				}
			},
			onPipe: func(cmds []redis.Cmder) {
				if tracker.recording {
					tracker.events = append(tracker.events, fmt.Sprintf("pipeline:%d", len(cmds)))
				}
			},
		})

		trackStore := store.New(trackClient)
		rule.Store = trackStore

		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass: %v", err)
		}

		// Verify round trips:
		// Between xreadgroup:5 and xreadgroup:0, there must be exactly 2 round trips:
		// "pipeline:3" and "xack"
		startIdx := -1
		endIdx := -1
		for i, ev := range tracker.events {
			if strings.HasPrefix(ev, "xreadgroup:") && ev != "xreadgroup" {
				if startIdx == -1 {
					startIdx = i
				} else {
					endIdx = i
					break
				}
			} else if ev == "xreadgroup" && startIdx != -1 {
				endIdx = i
				break
			}
		}
		if startIdx == -1 || endIdx == -1 {
			t.Fatalf("could not find both xreadgroup events in tracker events: %v", tracker.events)
		}
		between := tracker.events[startIdx+1 : endIdx]
		if len(between) != 2 || between[0] != "pipeline:3" || between[1] != "xack" {
			t.Fatalf("between XREADGROUP calls: got %v, want [pipeline:3, xack]", between)
		}

		// Verify pending count is 0
		pend, err := client.XPending(ctx, ghevent.Stream, rule.group()).Result()
		if err != nil {
			t.Fatalf("XPending: %v", err)
		}
		if pend.Count != 0 {
			t.Fatalf("pending count = %d, want 0", pend.Count)
		}
	})

	t.Run("adopt_once", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "readers", "2").Err()

		seedFriend(t, client, sprint, "ctl-a", true, 2)
		seedFriend(t, client, sprint, "ctl-b", true, 2)
		seedFriend(t, client, sprint, "author1", true, 2)

		seedPR(t, client, sprint, repo, 42, sha1, "dev", "author1", "reading", "false")

		var cuts atomic.Int64
		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		rule.CICut = func(ctx context.Context, c CICut) error {
			cuts.Add(1)
			return nil
		}

		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// Pass 1: adopts
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 1: %v", err)
		}
		if cuts.Load() != 1 {
			t.Fatalf("cuts = %d, want 1", cuts.Load())
		}
		if !strings.Contains(buf.String(), "ADOPT") || !strings.Contains(buf.String(), "cut=1") {
			t.Fatalf("expected ADOPT in output, got: %s", buf.String())
		}

		// Verify review tasks
		taskA := fmt.Sprintf("read-%s-%d-%s-ctl-a", repo, 42, sha1[:12])
		taskB := fmt.Sprintf("read-%s-%d-%s-ctl-b", repo, 42, sha1[:12])
		valA, errA := client.HGet(ctx, "s:"+sprint+":task:"+taskA, "state").Result()
		valB, errB := client.HGet(ctx, "s:"+sprint+":task:"+taskB, "state").Result()
		if errA != nil || valA != "open" || errB != nil || valB != "open" {
			t.Fatalf("expected tasks open, got taskA=%q (%v), taskB=%q (%v)", valA, errA, valB, errB)
		}

		// Pass 2: adds nothing
		buf.Reset()
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass 2: %v", err)
		}
		if cuts.Load() != 1 {
			t.Fatalf("cuts = %d on second pass, want 1", cuts.Load())
		}
		if !strings.Contains(buf.String(), "SKIP") || !strings.Contains(buf.String(), "adopted") {
			t.Fatalf("expected SKIP ... adopted in pass 2, got: %s", buf.String())
		}
	})

	t.Run("author_and_jev_excluded", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "readers", "1").Err()

		// Only author and jev are UP
		seedFriend(t, client, sprint, "author1", true, 2)
		seedFriend(t, client, sprint, "jev", true, 2)

		seedPR(t, client, sprint, repo, 42, sha1, "dev", "author1", "reading", "false")

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		_ = rule.Start(ctx)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "WAIT") || !strings.Contains(out, "no readers") {
			t.Fatalf("expected WAIT no readers in output, got: %s", out)
		}

		// No review task created
		openTasks, err := client.SMembers(ctx, "s:"+sprint+":idx:task:open").Result()
		if err != nil {
			t.Fatalf("SMembers: %v", err)
		}
		if len(openTasks) != 0 {
			t.Fatalf("open tasks = %v, want none", openTasks)
		}
	})

	t.Run("card_and_hold_skipped", func(t *testing.T) {
		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "readers", "1").Err()
		seedFriend(t, client, sprint, "ctl-a", true, 2)

		// PR 42 has a card
		seedPR(t, client, sprint, repo, 42, sha1, "dev", "author1", "reading", "false")
		_ = client.HSet(ctx, "s:"+sprint+":prcard", fmt.Sprintf("%s#42", repo), "card-42").Err()

		// PR 43 has an open hold
		seedPR(t, client, sprint, repo, 43, sha1, "dev", "author1", "reading", "false")
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:hold:%s:43", sprint, repo), "h1", `{"released_by":""}`).Err()

		var cuts atomic.Int64
		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		rule.CICut = func(ctx context.Context, c CICut) error {
			cuts.Add(1)
			return nil
		}

		_ = rule.Start(ctx)
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "SKIP mas-bandwidth/nova-tools#42 card") {
			t.Errorf("expected SKIP card, got: %s", out)
		}
		if !strings.Contains(out, "SKIP mas-bandwidth/nova-tools#43 hold") {
			t.Errorf("expected SKIP hold, got: %s", out)
		}
		if cuts.Load() != 0 {
			t.Fatalf("cuts = %d, want 0", cuts.Load())
		}
	})

	t.Run("no_http", func(t *testing.T) {
		orig := http.DefaultTransport
		http.DefaultTransport = &failTransport{}
		defer func() { http.DefaultTransport = orig }()

		st, client, _ := newTestBus(t)
		ctx := context.Background()
		sprint := "s1"
		_ = client.HSet(ctx, "s:"+sprint+":policy", "runner_rows", "windows").Err()

		publishCheckRun(t, client, repo, sha1, "windows", "100", "completed", "completed", "success", t1)

		var buf bytes.Buffer
		rule := newTestPRToReadRule(st, sprint, &buf)
		if err := rule.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := rule.Pass(ctx); err != nil {
			t.Fatalf("Pass: %v", err)
		}
	})
}

type roundTripHook struct {
	onCmd  func(redis.Cmder)
	onPipe func([]redis.Cmder)
}

func (h *roundTripHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *roundTripHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if h.onCmd != nil {
			h.onCmd(cmd)
		}
		return err
	}
}

func (h *roundTripHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		if h.onPipe != nil {
			h.onPipe(cmds)
		}
		return err
	}
}
