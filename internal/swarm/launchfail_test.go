package swarm

import (
	"os"
	"testing"
	"time"
)

// TestProviderLaunchFailureReadsTheProviderTail: the one classifier both paths ask, over
// the tails a provider prints for a server error, and the ref it carries (issue #900).
func TestProviderLaunchFailureReadsTheProviderTail(t *testing.T) {
	cases := []struct {
		tail    string
		wantRef string
		wantOK  bool
	}{
		{"Unexpected server error: the provider answered 503\nref=err_abc123\n", "err_abc123", true},
		{"internal server error\n", "", true},
		{"the upstream said 502 bad gateway\n", "", true},
		{"provider is busy (529)\n", "", true},
		{"all good, findings: 2\n", "", false},
		{"a task with 5036 tokens\n", "", false},
	}
	for _, c := range cases {
		ref, ok := ProviderLaunchFailure([]byte(c.tail))
		if ok != c.wantOK || ref != c.wantRef {
			t.Errorf("ProviderLaunchFailure(%q) = (%q,%t), want (%q,%t)", c.tail, ref, ok, c.wantRef, c.wantOK)
		}
	}
}

// TestLaunchGraceDefaultAndOverride: the worker description's launch_grace wins, and a
// description naming none takes the fifteen-second default.
func TestLaunchGraceDefaultAndOverride(t *testing.T) {
	if got := LaunchGrace(Worker{}); got != DefaultLaunchGrace {
		t.Errorf("a description naming no grace takes the default %s, got %s", DefaultLaunchGrace, got)
	}
	if got := LaunchGrace(Worker{LaunchGrace: "2s"}); got != 2*time.Second {
		t.Errorf("launch_grace 2s reads as %s", got)
	}
	if got := LaunchGrace(Worker{LaunchGrace: "not-a-duration"}); got != DefaultLaunchGrace {
		t.Errorf("an unreadable grace falls back to the default %s, got %s", DefaultLaunchGrace, got)
	}
}

// TestProviderRetryDelayBands: 5-20s after the first fast failure, 30-60s after the second.
func TestProviderRetryDelayBands(t *testing.T) {
	if err := os.Unsetenv("NOVA_SWARM_PROVIDER_BACKOFF"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		failed int
		lo, hi time.Duration
	}{{1, 5 * time.Second, 20 * time.Second}, {2, 30 * time.Second, 60 * time.Second}} {
		for i := 0; i < 50; i++ {
			got := ProviderRetryDelay(c.failed)
			if got < c.lo || got > c.hi {
				t.Fatalf("the delay after failure %d is %s, want within [%s,%s]", c.failed, got, c.lo, c.hi)
			}
		}
	}
}
