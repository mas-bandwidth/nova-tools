//go:build functional

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// landEvalRedis is a throwaway redis-server with the nova_sprint library
// loaded: land eval drains ev:github and beats through its functions.
func landEvalRedis(t *testing.T) string {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatalf("load fn library: %v", err)
	}
	return addr
}

func TestLandEvalPass(t *testing.T) {
	t.Parallel()

	addr := landEvalRedis(t)
	code, stdout, stderr := runSprint("land", "eval", "--repo", "nova-tools", "--redis", addr, "--sprint", "sprint-eval", "--mirror", "none")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "EVAL repo=nova-tools sprint=sprint-eval") {
		t.Fatalf("stdout want EVAL summary, got %s", stdout)
	}
}

func TestNoStateAPI(t *testing.T) {
	// Guard against any HTTP call from land eval
	callAttempted := false
	originalTransport := http.DefaultTransport
	http.DefaultTransport = &failingTransport{
		onCall: func(req *http.Request) {
			callAttempted = true
		},
	}
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})

	addr := landEvalRedis(t)
	code, _, stderr := runSprint("land", "eval", "--repo", "nova-tools", "--redis", addr, "--sprint", "sprint-test", "--mirror", "none")
	if code != 0 {
		t.Fatalf("land eval failed: %d: %s", code, stderr)
	}
	if callAttempted {
		t.Fatalf("TestNoStateAPI: land eval attempted an HTTP call to external API")
	}
}

type failingTransport struct {
	onCall func(req *http.Request)
}

func (f *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.onCall != nil {
		f.onCall(req)
	}
	return nil, fmt.Errorf("TestNoStateAPI: external HTTP call strictly forbidden: %s", req.URL.String())
}

// TestLandEvalPolicySyncFailureIsNamed (FG-A fix 1, land_eval.go policy
// sync): a policy record the store refuses to write (WRONGTYPE) stops the
// pass naming the base, the key and the store's error, and evaluates
// nothing; it is not a silent pass over an unwritten policy.
func TestLandEvalPolicySyncFailureIsNamed(t *testing.T) {
	t.Parallel()

	addr := landEvalRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	pkey := land.PolicyKey("nova-tools", "dev")
	if err := c.Set(context.Background(), pkey, "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(t.TempDir(), "nova-tools.yml")
	if err := os.WriteFile(pol, []byte("repo: nova-tools\nbases:\n  dev:\n    required_steps: [build]\n    land_bar: 10\n    readers: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runSprint("land", "eval", "--repo", "nova-tools", "--redis", addr, "--sprint", "sprint-sync", "--mirror", "none", "--policy", pol)
	if code != 2 {
		t.Fatalf("exit %d, want 2; stdout %q stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"policy sync base dev", pkey, "WRONGTYPE", "nothing evaluated"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr %q does not name %q", stderr, want)
		}
	}
	if strings.Contains(stdout, "EVAL repo=") {
		t.Fatalf("a failed policy sync still printed a pass summary: %q", stdout)
	}
}
