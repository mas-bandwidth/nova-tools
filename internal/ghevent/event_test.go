package ghevent

// event_test.go is the contract of the ev:github decoder (#2685, #2657): one
// webhook delivery becomes one stream entry. The bus is miniredis. No test
// opens a socket to GitHub or requires a live Redis.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newBus(t *testing.T) (*redis.Client, context.Context) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, context.Background()
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readStream(t *testing.T, rdb *redis.Client, ctx context.Context) []map[string]string {
	t.Helper()
	msgs, err := rdb.XRange(ctx, Stream, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE %s: %v", Stream, err)
	}
	out := make([]map[string]string, 0, len(msgs))
	for _, m := range msgs {
		got := make(map[string]string, len(m.Values))
		for k, v := range m.Values {
			s, ok := v.(string)
			if !ok {
				t.Fatalf("field %s is %T, want string", k, v)
			}
			got[k] = s
		}
		out = append(out, got)
	}
	return out
}

func equalFields(t *testing.T, got, want map[string]string) {
	t.Helper()
	if got == nil {
		t.Fatalf("no entry with the expected kind, want %#v", want)
	}
	if len(got) != len(want) {
		t.Errorf("field count %d, want %d: %#v", len(got), len(want), got)
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing field %s", k)
			continue
		}
		if g != w {
			t.Errorf("field %s = %q, want %q", k, g, w)
		}
	}
	for k, g := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected field %s=%q", k, g)
		}
	}
}

// The three deliveries #2685 asks to see on the stream: a pull_request, a
// check_run (two pull requests on the payload, still one entry), and an
// issue_comment whose body is a typed DISPOSITION line. The comment's sender
// is not the who= on the line, and its head is the sha the line names because
// the payload has no pull-request head. at is the event time, not an earlier
// created_at or started_at sitting beside it.
func TestPullRequestCheckRunAndDispositionEachBecomeOneEntry(t *testing.T) {
	t.Parallel()
	if Stream != "ev:github" {
		t.Fatalf("stream = %q, want ev:github", Stream)
	}
	rdb, ctx := newBus(t)
	cases := []struct {
		event string
		file  string
		want  map[string]string
	}{
		{
			event: "pull_request",
			file:  "pull_request.json",
			want: map[string]string{
				"repo":       "mas-bandwidth/nova-tools",
				"kind":       "pull_request",
				"number":     "42",
				"head":       "1111111111111111111111111111111111111111",
				"action":     "synchronize",
				"at":         "2026-09-22T16:45:01Z",
				"sender":     "octocat",
				"comment_id": "",
			},
		},
		{
			event: "check_run",
			file:  "check_run.json",
			want: map[string]string{
				"repo":       "mas-bandwidth/nova-tools",
				"kind":       "check_run",
				"number":     "42",
				"head":       "2222222222222222222222222222222222222222",
				"action":     "completed",
				"at":         "2026-09-22T16:46:02Z",
				"sender":     "octocat",
				"comment_id": "",
			},
		},
		{
			event: "issue_comment",
			file:  "issue_comment_disposition.json",
			want: map[string]string{
				"repo":       "mas-bandwidth/nova-tools",
				"kind":       "issue_comment",
				"number":     "42",
				"head":       "3333333333333333333333333333333333333333",
				"action":     "created",
				"at":         "2026-09-22T16:47:03Z",
				"sender":     "astra",
				"comment_id": "9001",
			},
		},
	}
	for _, tc := range cases {
		id, err := Accept(ctx, rdb, tc.event, fixture(t, tc.file))
		if err != nil {
			t.Fatalf("%s: %v", tc.event, err)
		}
		if id == "" {
			t.Fatalf("%s: empty stream id", tc.event)
		}
	}
	got := readStream(t, rdb, ctx)
	if len(got) != len(cases) {
		t.Fatalf("ev:github has %d entries, want %d (one per delivery)", len(got), len(cases))
	}
	byKind := make(map[string]map[string]string, len(got))
	for _, e := range got {
		k := e["kind"]
		if _, ok := byKind[k]; ok {
			t.Fatalf("two entries for kind %s", k)
		}
		byKind[k] = e
	}
	for _, tc := range cases {
		equalFields(t, byKind[tc.want["kind"]], tc.want)
	}
}

// The other events #2657 names are the same contract: one entry, the same fields.
func TestCheckSuiteReviewAndMergeGroupEachBecomeOneEntry(t *testing.T) {
	t.Parallel()
	rdb, ctx := newBus(t)
	cases := []struct {
		event   string
		payload string
		want    map[string]string
	}{
		{
			event: "check_suite",
			payload: `{
				"action": "completed",
				"check_suite": {
					"head_sha": "5555555555555555555555555555555555555555",
					"created_at": "2026-09-22T16:40:00Z",
					"updated_at": "2026-09-22T16:49:05Z",
					"pull_requests": [{"number": 42}]
				},
				"repository": {"full_name": "mas-bandwidth/nova-tools"},
				"sender": {"login": "octocat"}
			}`,
			want: map[string]string{
				"repo":       "mas-bandwidth/nova-tools",
				"kind":       "check_suite",
				"number":     "42",
				"head":       "5555555555555555555555555555555555555555",
				"action":     "completed",
				"at":         "2026-09-22T16:49:05Z",
				"sender":     "octocat",
				"comment_id": "",
			},
		},
		{
			event: "pull_request_review",
			payload: `{
				"action": "submitted",
				"review": {
					"commit_id": "6666666666666666666666666666666666666666",
					"submitted_at": "2026-09-22T16:50:06Z",
					"body": "DISPOSITION who=rowan head=3333333333333333333333333333333333333333 verdict=APPROVE"
				},
				"pull_request": {
					"number": 42,
					"head": {"sha": "7777777777777777777777777777777777777777"}
				},
				"repository": {"full_name": "mas-bandwidth/nova-tools"},
				"sender": {"login": "rowan"}
			}`,
			want: map[string]string{
				"repo":       "mas-bandwidth/nova-tools",
				"kind":       "pull_request_review",
				"number":     "42",
				"head":       "6666666666666666666666666666666666666666",
				"action":     "submitted",
				"at":         "2026-09-22T16:50:06Z",
				"sender":     "rowan",
				"comment_id": "",
			},
		},
		{
			event: "merge_group",
			payload: `{
				"action": "checks_requested",
				"merge_group": {
					"head_sha": "4444444444444444444444444444444444444444",
					"head_ref": "refs/heads/gh-readonly-queue/dev/pr-42-4444444444444444444444444444444444444444",
					"head_commit": {"timestamp": "2026-09-22T16:48:04Z"}
				},
				"repository": {"full_name": "mas-bandwidth/nova-tools"},
				"sender": {"login": "github-merge-queue[bot]"}
			}`,
			want: map[string]string{
				"repo":       "mas-bandwidth/nova-tools",
				"kind":       "merge_group",
				"number":     "42",
				"head":       "4444444444444444444444444444444444444444",
				"action":     "checks_requested",
				"at":         "2026-09-22T16:48:04Z",
				"sender":     "github-merge-queue[bot]",
				"comment_id": "",
			},
		},
	}
	for _, tc := range cases {
		if _, err := Accept(ctx, rdb, tc.event, []byte(tc.payload)); err != nil {
			t.Fatalf("%s: %v", tc.event, err)
		}
	}
	got := readStream(t, rdb, ctx)
	if len(got) != len(cases) {
		t.Fatalf("ev:github has %d entries, want %d", len(got), len(cases))
	}
	byKind := make(map[string]map[string]string, len(got))
	for _, e := range got {
		k := e["kind"]
		if _, ok := byKind[k]; ok {
			t.Fatalf("two entries for kind %s", k)
		}
		byKind[k] = e
	}
	for _, tc := range cases {
		equalFields(t, byKind[tc.want["kind"]], tc.want)
	}
}

func TestAQuotedDispositionDoesNotSupplyTheHead(t *testing.T) {
	t.Parallel()
	rdb, ctx := newBus(t)
	payload := []byte(`{
		"action": "created",
		"issue": {"number": 42},
		"comment": {
			"id": 9002,
			"created_at": "2026-09-22T16:51:07Z",
			"updated_at": "2026-09-22T16:51:07Z",
			"body": "> DISPOSITION who=stella head=3333333333333333333333333333333333333333 verdict=HOLD\nno typed line\n"
		},
		"repository": {"full_name": "mas-bandwidth/nova-tools"},
		"sender": {"login": "astra"}
	}`)
	if _, err := Accept(ctx, rdb, "issue_comment", payload); err != nil {
		t.Fatal(err)
	}
	got := readStream(t, rdb, ctx)
	if len(got) != 1 {
		t.Fatalf("ev:github has %d entries, want 1", len(got))
	}
	equalFields(t, got[0], map[string]string{
		"repo":       "mas-bandwidth/nova-tools",
		"kind":       "issue_comment",
		"number":     "42",
		"head":       "",
		"action":     "created",
		"at":         "2026-09-22T16:51:07Z",
		"sender":     "astra",
		"comment_id": "9002",
	})
}

func TestAPingWritesNothing(t *testing.T) {
	t.Parallel()
	rdb, ctx := newBus(t)
	id, err := Accept(ctx, rdb, "ping", []byte(`{"zen":"keep it logically awesome"}`))
	if !errors.Is(err, ErrNotCarried) {
		t.Fatalf("ping: err=%v, want ErrNotCarried", err)
	}
	if id != "" {
		t.Fatalf("ping wrote id %q", id)
	}
	n, err := rdb.XLen(ctx, Stream).Result()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("ev:github len = %d, want 0", n)
	}
}

func TestACarriedEventWithBadJSONWritesNothing(t *testing.T) {
	t.Parallel()
	rdb, ctx := newBus(t)
	id, err := Accept(ctx, rdb, "pull_request", []byte(`{`))
	if err == nil {
		t.Fatal("bad JSON returned nil error")
	}
	if errors.Is(err, ErrNotCarried) {
		t.Fatal("bad JSON was treated as an event the stream does not carry")
	}
	if id != "" {
		t.Fatalf("bad JSON wrote id %q", id)
	}
	n, err := rdb.XLen(ctx, Stream).Result()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("ev:github len = %d, want 0", n)
	}
}
