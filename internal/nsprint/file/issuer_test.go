package file

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// handlerTransport serves each request from h in process: no socket.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}

// TestIssuerPostsAndReadsBack is the writer card cut --from files through
// (nova-tools#4340): one Post is one create and one read-back; a stored body
// that differs is an error naming the issue.
func TestIssuerPostsAndReadsBack(t *testing.T) {
	t.Parallel()
	fake := newFake()
	d := Deps{API: "http://github.test", HTTP: &http.Client{Transport: handlerTransport{fake}},
		Token: func() (string, error) { return "test-token", nil }}
	is, err := NewIssuer(d)
	if err != nil {
		t.Fatal(err)
	}
	n, url, err := is.Post(context.Background(), "o/r", "a title", "short body")
	if err != nil || n != 100 || !strings.HasSuffix(url, "/issues/100") {
		t.Fatalf("Post = %d %q %v; want 100, its url, nil", n, url, err)
	}
	if fake.issues[100] != "short body" || fake.lastAuth != "Bearer test-token" {
		t.Fatalf("stored %q auth %q", fake.issues[100], fake.lastAuth)
	}
	fake.literal = "@/tmp/body.md"
	if _, _, err := is.Post(context.Background(), "o/r", "b", "real body"); err == nil || !strings.Contains(err.Error(), "READBACK DIFFERS o/r#101") {
		t.Fatalf("differing read-back: %v; want READBACK DIFFERS o/r#101", err)
	}
	if _, err := NewIssuer(Deps{Token: func() (string, error) { return " ", nil }}); err == nil {
		t.Fatal("an empty token was accepted")
	}
}
