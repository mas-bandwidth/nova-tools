package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// GitHubStub is an HTTP server standing in for GitHub in tests.
// The lander path (eval, serve, worker) reads only git remotes and Redis records;
// any HTTP call to GitHub on that path is forbidden (O6, L18).
// When contacted, the stub records the call and fails the test.
type GitHubStub struct {
	Server *httptest.Server
	URL    string
	// Env carries GITHUB_API_URL and GH_HOST for a caller to append to a
	// child's cmd.Env, so a child honoring the standard GitHub variables
	// targets the stub without this process changing its own environment.
	Env   []string
	calls atomic.Int64
}

// StartGitHubStub starts an httptest.Server that fails t on any HTTP request.
// It returns the environment a child needs to target the stub (GITHUB_API_URL
// and GH_HOST) in GitHubStub.Env, a per-test seam in place of t.Setenv, so the
// test can run in parallel.
func StartGitHubStub(t *testing.T) *GitHubStub {
	t.Helper()
	stub := &GitHubStub{}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.calls.Add(1)
		msg := fmt.Sprintf("github stub received forbidden HTTP call: %s %s (O6/L18: no GitHub API calls on landing path)", r.Method, r.URL.Path)
		t.Errorf("%s", msg)
		http.Error(w, msg, http.StatusForbidden)
	}))
	stub.URL = stub.Server.URL
	stub.Env = []string{
		"GITHUB_API_URL=" + stub.URL,
		"GH_HOST=" + stub.Server.Listener.Addr().String(),
	}
	t.Cleanup(func() {
		stub.Server.Close()
	})
	return stub
}

// Calls returns the count of HTTP requests received by the stub.
func (g *GitHubStub) Calls() int64 {
	return g.calls.Load()
}
