package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestLandEvalMissingRepoRefuses(t *testing.T) {
	code, _, stderr := runSprint("land", "eval")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "needs --repo") {
		t.Fatalf("stderr want 'needs --repo', got %q", stderr)
	}
}

func TestLandEvalBadRedisExits6(t *testing.T) {
	code, _, stderr := runSprint("land", "eval", "--repo", "nova-tools", "--redis", "127.0.0.1:99999")
	if code != 6 {
		t.Fatalf("exit %d, want 6", code)
	}
	if !strings.Contains(stderr, "connect redis") {
		t.Fatalf("stderr want 'connect redis', got %q", stderr)
	}
}

func TestLandEvalPass(t *testing.T) {
	mr := miniredis.RunT(t)
	code, stdout, stderr := runSprint("land", "eval", "--repo", "nova-tools", "--redis", mr.Addr(), "--sprint", "sprint-eval")
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

	mr := miniredis.RunT(t)
	code, _, stderr := runSprint("land", "eval", "--repo", "nova-tools", "--redis", mr.Addr(), "--sprint", "sprint-test")
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
