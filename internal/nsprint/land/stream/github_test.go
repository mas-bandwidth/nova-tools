package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// exists422 is GitHub's reply to POST /pulls when an open PR already has the
// head branch.
const exists422 = `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for o:stream/x."}]}`

// forge422 answers POST /pulls with exists422 and GET /pulls?head=&state=open
// with list; it records every call.
type forge422 struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
}

func newForge422(t *testing.T, list string) *forge422 {
	t.Helper()
	f := &forge422{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/"+repo+"/pulls":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(exists422))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+repo+"/pulls":
			_, _ = w.Write([]byte(list))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *forge422) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// TestLandStreamAdoptsTheOpenPROn422: the stream PR create answers 422 "A
// pull request already exists" and the open list has #91 on the stream
// branch, so the land step records pr=91, state open, and returns no error.
func TestLandStreamAdoptsTheOpenPROn422(t *testing.T) {
	c := newRedis(t)
	ctx := context.Background()
	f := newFixture(t)
	seed(t, c, 1, f.Head[1], 100, score("emma", f.Head[1], 10))
	gh := newForge422(t, `[{"number":91}]`)
	rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, Base: "dev", Remote: f.URL,
		Workdir: filepath.Join(t.TempDir(), "clone"), Test: "true", TestTimeout: time.Minute, MinScore: -1,
		By: "test", GH: &GitHub{API: gh.srv.URL}})
	if err != nil {
		t.Fatalf("land stream: %v (calls %v)", err, gh.Calls())
	}
	if rep.PR != 91 || rep.State != "open" {
		t.Fatalf("report pr=%d state=%s, want 91 open", rep.PR, rep.State)
	}
	l, ok, err := LoadLanding(ctx, c, repo, rep.Slug)
	if err != nil || !ok || l.PR != 91 || l.State != "open" {
		t.Fatalf("land record %+v ok=%v err=%v, want pr=91 state=open", l, ok, err)
	}
	if got, _ := c.HGet(ctx, "land:"+repo+":"+rep.Slug, "pr").Result(); got != "91" {
		t.Fatalf("land:%s:%s pr=%q, want 91", repo, rep.Slug, got)
	}
	calls := gh.Calls()
	owner := strings.SplitN(repo, "/", 2)[0]
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "POST /repos/"+repo+"/pulls") ||
		!strings.HasPrefix(calls[1], "GET /repos/"+repo+"/pulls?") ||
		!strings.Contains(calls[1], "head="+owner+"%3Astream%2Flanding-streams-lander") || !strings.Contains(calls[1], "state=open") {
		t.Fatalf("REST calls %v", calls)
	}
}

// TestGitHubErrorSaysWhy: a 422 with no open PR to adopt is an error that
// carries GitHub's message and every errors[].message (or code and field
// when the message is empty).
func TestGitHubErrorSaysWhy(t *testing.T) {
	gh := newForge422(t, `[]`)
	_, err := (&GitHub{API: gh.srv.URL}).OpenPR(context.Background(), repo, "stream/x", "dev", "t", "b")
	if err == nil {
		t.Fatal("OpenPR with no open PR to adopt returned no error")
	}
	want := "HTTP 422: Validation Failed: A pull request already exists for o:stream/x."
	if !strings.Contains(err.Error(), "A pull request already exists") || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want it to contain %q", err, want)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"base","code":"invalid"},{"message":"No commits between dev and x"}]}`))
	}))
	t.Cleanup(srv.Close)
	err = (&GitHub{API: srv.URL}).Close(context.Background(), repo, 5)
	if err == nil || !strings.Contains(err.Error(), "HTTP 422: Validation Failed: ") ||
		!strings.Contains(err.Error(), "field=base code=invalid") || !strings.Contains(err.Error(), "No commits between dev and x") {
		t.Fatalf("error %v, want message, code+field and errors[].message", err)
	}
}
