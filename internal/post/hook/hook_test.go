package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestWebhookEventsReachTheStream(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	emitter := NewRedisEmitter(rdb)
	rec := NewReceiver(emitter)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.ServeHTTP(w, r)
	}))
	defer srv.Close()

	events_to_send := []struct {
		eventType string
		payload   interface{}
	}{
		{
			eventType: "pull_request",
			payload: map[string]interface{}{
				"action": "opened",
				"pull_request": map[string]interface{}{
					"number": float64(42), "title": "webhooks into the stream",
					"head": map[string]interface{}{"sha": "abc123"},
				},
			},
		},
		{
			eventType: "pull_request_review",
			payload: map[string]interface{}{
				"action": "submitted",
				"review": map[string]interface{}{
					"state": "approved", "user": map[string]interface{}{"login": "glenn"},
				},
				"pull_request": map[string]interface{}{
					"number": float64(42), "head": map[string]interface{}{"sha": "abc123"},
				},
			},
		},
		{
			eventType: "check_suite",
			payload: map[string]interface{}{
				"action": "completed",
				"check_suite": map[string]interface{}{
					"conclusion": "success", "head_sha": "abc123",
					"pull_requests": []interface{}{
						map[string]interface{}{"number": float64(42)},
					},
				},
			},
		},
		{
			eventType: "issue_comment",
			payload: map[string]interface{}{
				"action": "created",
				"comment": map[string]interface{}{
					"body": "ship it",
					"user": map[string]interface{}{"login": "rowan"},
				},
				"issue": map[string]interface{}{"number": float64(42)},
			},
		},
	}

	ctx := context.Background()

	for _, ev := range events_to_send {
		body, err := json.Marshal(ev.payload)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", srv.URL+"/webhook", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", ev.eventType)
		req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("delivery-%s-1", ev.eventType))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("send %s: %v", ev.eventType, err)
		}
		resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status for %s = %d, want %d", ev.eventType, got, want)
		}
	}

	entries, err := rdb.XRange(ctx, Stream, "-", "+").Result()
	if err != nil {
		t.Fatalf("range %s: %v", Stream, err)
	}
	if got, want := len(entries), len(events_to_send); got != want {
		t.Fatalf("stream has %d events, want %d: each webhook must emit to %s", got, want, Stream)
	}
	for i, entry := range entries {
		want := events_to_send[i].eventType
		evType := valueString(entry.Values["event"])
		if evType != want {
			t.Errorf("entry %d event = %q, want %q", i, evType, want)
		}
	}

	group := "hook-fold"
	if err := rdb.XGroupCreateMkStream(ctx, Stream, group, "0").Err(); err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			t.Fatal(err)
		}
	}

	msgs, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: "consumer-1",
		Streams:  []string{Stream, ">"},
		Count:    int64(len(events_to_send)),
	}).Result()
	if err != nil {
		t.Fatalf("read group: %v", err)
	}
	got := msgs[0].Messages
	if len(got) != len(events_to_send) {
		t.Fatalf("read %d events, want %d", len(got), len(events_to_send))
	}

	if err := rdb.XAck(ctx, Stream, group, got[0].ID).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.XAck(ctx, Stream, group, got[1].ID).Err(); err != nil {
		t.Fatal(err)
	}

	pending, _, err := rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   Stream,
		Group:    group,
		Consumer: "consumer-2",
		MinIdle:  0,
		Start:    "0-0",
		Count:    10,
	}).Result()
	if err != nil {
		t.Fatalf("pending after simulated crash: %v", err)
	}
	if len(pending) != len(events_to_send)-2 {
		t.Fatalf("pending = %d, want %d: the fold count must equal the API's count after a crash-and-replay",
			len(pending), len(events_to_send)-2)
	}
}

func valueString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}
