package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// landPRFake is the smallest GitHub the verb calls: PR 12 read, and its
// merge at the head. Anything else (a check-runs or workflow-runs read,
// GraphQL) fails the test.
func landPRFake(t *testing.T) *httptest.Server {
	t.Helper()
	head := strings.Repeat("a", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/12":
			reply = map[string]any{"state": "open", "merged": false, "mergeable_state": "blocked", "title": "t", "head": map[string]any{"sha": head}}
		case r.Method == http.MethodPut && r.URL.Path == "/repos/o/r/pulls/12/merge":
			reply = map[string]any{"sha": strings.Repeat("b", 40), "merged": true}
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestLandPRVerb: with no GitHub leg in Redis the verb is WAITING at once
// (exit 3); once the webhook's record is green it merges by REST at the
// head with the receipt line (exit 0); red is exit 1 naming the run; a
// missing token is the typed refusal with nothing called (exit 2), and
// usage is exit 2.
func TestLandPRVerb(t *testing.T) {
	t.Setenv("GH_TOKEN", "t0k")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("NOVA_REDIS_ADDR", "")
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	key := "ci:r:" + strings.Repeat("a", 40) + ":gh"

	srv := landPRFake(t)
	code, out, errOut := runSprint("land", "pr", "12", "--repo", "o/r", "--redis", mr.Addr(), "--api", srv.URL)
	if code != 3 || !strings.Contains(out, "PR 12 WAITING "+key+" has gh=-") ||
		!strings.Contains(out, "LAND PR repo=o/r pr=#12 state=waiting head=aaaaaaaa ci=- merge=- failed=- rest_calls=1\n") {
		t.Fatalf("waiting: exit %d\n%s%s", code, out, errOut)
	}

	c.HSet(context.Background(), key, "gh", "green", "check:lint", "green 1 x")
	code, out, errOut = runSprint("land", "pr", "--repo", "o/r", "--redis", mr.Addr(), "--api", srv.URL, "#12")
	want := "PR 12 CHECKS green 1/1 head=aaaaaaaa\nPR 12 MERGED " + strings.Repeat("b", 40) + "\n" +
		"LAND PR repo=o/r pr=#12 state=merged head=aaaaaaaa ci=green merge=bbbbbbbb failed=- rest_calls=2\n"
	if code != 0 || out != want {
		t.Fatalf("green: exit %d\n%s\nwant:\n%s%s", code, out, want, errOut)
	}

	c.HSet(context.Background(), key, "gh", "red", "gh_fail", "check:lint", "check:lint", "red 1 x")
	code, out, _ = runSprint("land", "pr", "12", "--repo", "o/r", "--redis", mr.Addr(), "--api", srv.URL)
	if code != 1 || !strings.Contains(out, "PR 12 FAILED check:lint\nLAND PR repo=o/r pr=#12 state=failed head=aaaaaaaa ci=red merge=- failed=check:lint rest_calls=1\n") {
		t.Fatalf("red: exit %d\n%s", code, out)
	}

	prev := landStreamToken
	landStreamToken = func() (string, error) { return "", nil }
	t.Cleanup(func() { landStreamToken = prev })
	code, out, errOut = runSprint("land", "pr", "12", "--repo", "o/r", "--redis", mr.Addr(), "--api", srv.URL)
	if code != 2 || out != "" || !strings.Contains(errOut, "REFUSED no GitHub token remedy=name the seat's GitHub token env in its seats.tsv row") {
		t.Fatalf("no token: exit %d %q %q", code, out, errOut)
	}
	for _, args := range [][]string{{}, {"x"}, {"0"}, {"12", "13"}, {"12", "--repo", "norepo"}, {"12", "--timeout", "5m"}, {"12"}} {
		if code, _, errOut := runSprint(append([]string{"land", "pr"}, args...)...); code != 2 || !strings.HasPrefix(errOut, "nova-sprint land pr: ") {
			t.Fatalf("%v: exit %d %q, want usage", args, code, errOut)
		}
	}
}
