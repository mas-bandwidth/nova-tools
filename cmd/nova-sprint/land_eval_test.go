package main

import (
	"strings"
	"testing"
)

func TestLandEvalMissingRepoRefuses(t *testing.T) {
	t.Parallel()

	code, _, stderr := runSprint("land", "eval")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "needs --repo") {
		t.Fatalf("stderr want 'needs --repo', got %q", stderr)
	}
}

func TestLandEvalBadRedisExits6(t *testing.T) {
	t.Parallel()

	code, _, stderr := runSprint("land", "eval", "--repo", "nova-tools", "--redis", "127.0.0.1:99999")
	if code != 6 {
		t.Fatalf("exit %d, want 6", code)
	}
	if !strings.Contains(stderr, "connect redis") {
		t.Fatalf("stderr want 'connect redis', got %q", stderr)
	}
}
