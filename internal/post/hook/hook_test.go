package hook

// hook_test.go is the control of #2657's receiver: a signed GitHub delivery
// POSTed to the handler becomes exactly one XADD on ev:github with the fields
// internal/ghevent names, and a delivery whose X-Hub-Signature-256 does not
// verify is refused 401 with no entry. The bus is miniredis and the request is
// an httptest recorder: no socket to GitHub, no live Redis.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/redis/go-redis/v9"
)

const testSecret = "a-test-webhook-secret"

func newReceiver(t *testing.T) (*Handler, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	h, err := NewHandler(rdb, []byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return h, rdb
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func deliver(h http.Handler, event, sig, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GitHub-Event", event)
	r.Header.Set("X-GitHub-Delivery", "00000000-0000-0000-0000-000000000001")
	if sig != "" {
		r.Header.Set("X-Hub-Signature-256", sig)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func entries(t *testing.T, rdb *redis.Client) []map[string]string {
	t.Helper()
	msgs, err := rdb.XRange(context.Background(), ghevent.Stream, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE %s: %v", ghevent.Stream, err)
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

var fieldNames = []string{"repo", "kind", "number", "head", "action", "at", "sender", "comment_id"}

func TestWebhookToEvGithub(t *testing.T) {
	t.Parallel()
	if ghevent.Stream != "ev:github" {
		t.Fatalf("stream = %q, want ev:github", ghevent.Stream)
	}

	cases := []struct {
		event string
		body  string
		want  map[string]string
	}{
		{
			event: "pull_request",
			body: `{"action":"synchronize","number":42,
				"pull_request":{"number":42,"updated_at":"2026-09-22T16:45:01Z","head":{"sha":"1111111111111111111111111111111111111111"}},
				"repository":{"full_name":"mas-bandwidth/nova-tools"},"sender":{"login":"octocat"}}`,
			want: map[string]string{"repo": "mas-bandwidth/nova-tools", "kind": "pull_request", "number": "42",
				"head": "1111111111111111111111111111111111111111", "action": "synchronize",
				"at": "2026-09-22T16:45:01Z", "sender": "octocat", "comment_id": ""},
		},
		{
			event: "issue_comment",
			body: `{"action":"created","issue":{"number":42},
				"comment":{"id":9001,"created_at":"2026-09-22T16:47:03Z","updated_at":"2026-09-22T16:47:03Z",
				"body":"DISPOSITION who=stella head=3333333333333333333333333333333333333333 verdict=HOLD"},
				"repository":{"full_name":"mas-bandwidth/nova-tools"},"sender":{"login":"astra"}}`,
			want: map[string]string{"repo": "mas-bandwidth/nova-tools", "kind": "issue_comment", "number": "42",
				"head": "3333333333333333333333333333333333333333", "action": "created",
				"at": "2026-09-22T16:47:03Z", "sender": "astra", "comment_id": "9001"},
		},
		{
			event: "pull_request_review",
			body: `{"action":"submitted",
				"review":{"commit_id":"6666666666666666666666666666666666666666","submitted_at":"2026-09-22T16:50:06Z","body":"looks right"},
				"pull_request":{"number":42,"head":{"sha":"7777777777777777777777777777777777777777"}},
				"repository":{"full_name":"mas-bandwidth/nova-tools"},"sender":{"login":"rowan"}}`,
			want: map[string]string{"repo": "mas-bandwidth/nova-tools", "kind": "pull_request_review", "number": "42",
				"head": "6666666666666666666666666666666666666666", "action": "submitted",
				"at": "2026-09-22T16:50:06Z", "sender": "rowan", "comment_id": ""},
		},
		{
			event: "check_run",
			body: `{"action":"completed",
				"check_run":{"head_sha":"2222222222222222222222222222222222222222","started_at":"2026-09-22T16:40:00Z","completed_at":"2026-09-22T16:46:02Z",
				"pull_requests":[{"number":42,"head":{"sha":"2222222222222222222222222222222222222222"}},{"number":43}]},
				"repository":{"full_name":"mas-bandwidth/nova-tools"},"sender":{"login":"octocat"}}`,
			want: map[string]string{"repo": "mas-bandwidth/nova-tools", "kind": "check_run", "number": "42",
				"head": "2222222222222222222222222222222222222222", "action": "completed",
				"at": "2026-09-22T16:46:02Z", "sender": "octocat", "comment_id": ""},
		},
	}

	for _, tc := range cases {
		t.Run("signed "+tc.event+" is one entry", func(t *testing.T) {
			t.Parallel()
			h, rdb := newReceiver(t)
			w := deliver(h, tc.event, sign(testSecret, tc.body), tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
			}
			got := entries(t, rdb)
			if len(got) != 1 {
				t.Fatalf("ev:github has %d entries, want exactly 1", len(got))
			}
			e := got[0]
			if len(e) != len(fieldNames) {
				t.Errorf("entry has %d fields, want %d: %#v", len(e), len(fieldNames), e)
			}
			for _, k := range fieldNames {
				g, ok := e[k]
				if !ok {
					t.Errorf("missing field %s", k)
					continue
				}
				if g != tc.want[k] {
					t.Errorf("field %s = %q, want %q", k, g, tc.want[k])
				}
			}
		})
	}

	pr := cases[0].body
	refused := []struct {
		name string
		sig  string
	}{
		{"no signature", ""},
		{"signed with another secret", sign("not-the-secret", pr)},
		{"signature over another body", sign(testSecret, pr+" ")},
		{"sha1 scheme", "sha1=" + strings.TrimPrefix(sign(testSecret, pr), "sha256=")[:40]},
		{"not hex", "sha256=zz"},
		{"bare digest", strings.TrimPrefix(sign(testSecret, pr), "sha256=")},
	}
	for _, tc := range refused {
		t.Run("bad signature refused 401: "+tc.name, func(t *testing.T) {
			t.Parallel()
			h, rdb := newReceiver(t)
			w := deliver(h, "pull_request", tc.sig, pr)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
			}
			if got := entries(t, rdb); len(got) != 0 {
				t.Fatalf("ev:github has %d entries after a refused delivery, want 0", len(got))
			}
		})
	}

	t.Run("signed ping answers 200 and writes nothing", func(t *testing.T) {
		t.Parallel()
		h, rdb := newReceiver(t)
		body := `{"zen":"z","hook_id":1}`
		w := deliver(h, "ping", sign(testSecret, body), body)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
		}
		if got := entries(t, rdb); len(got) != 0 {
			t.Fatalf("ev:github has %d entries after ping, want 0", len(got))
		}
	})

	t.Run("signed event the stream does not carry writes nothing", func(t *testing.T) {
		t.Parallel()
		h, rdb := newReceiver(t)
		body := `{"ref":"refs/heads/dev","repository":{"full_name":"mas-bandwidth/nova-tools"}}`
		w := deliver(h, "push", sign(testSecret, body), body)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d, want 202: %s", w.Code, w.Body.String())
		}
		if got := entries(t, rdb); len(got) != 0 {
			t.Fatalf("ev:github has %d entries after push, want 0", len(got))
		}
	})

	t.Run("signed carried event with a bad payload is 400 and writes nothing", func(t *testing.T) {
		t.Parallel()
		h, rdb := newReceiver(t)
		body := `{"action":"opened"}`
		w := deliver(h, "pull_request", sign(testSecret, body), body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
		}
		if got := entries(t, rdb); len(got) != 0 {
			t.Fatalf("ev:github has %d entries, want 0", len(got))
		}
	})

	t.Run("GET is 405", func(t *testing.T) {
		t.Parallel()
		h, rdb := newReceiver(t)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/webhook", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status %d, want 405", w.Code)
		}
		if got := entries(t, rdb); len(got) != 0 {
			t.Fatalf("ev:github has %d entries, want 0", len(got))
		}
	})

	t.Run("an empty secret is refused at construction", func(t *testing.T) {
		t.Parallel()
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		if _, err := NewHandler(rdb, nil); err == nil {
			t.Fatal("NewHandler with no secret returned nil error")
		}
		if _, err := NewHandler(nil, []byte(testSecret)); err == nil {
			t.Fatal("NewHandler with no redis client returned nil error")
		}
	})
}
