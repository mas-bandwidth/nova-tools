package main

import (
	"strings"
	"testing"
)

func TestStreamLifeUsage(t *testing.T) {
	t.Parallel()

	for _, sub := range []string{"open", "rebase", "pr", "close"} {
		if code, _, errOut := runSprint("stream", sub); code != 2 || strings.Count(errOut, "\n") != 1 {
			t.Errorf("stream %s with no flags: exit %d, stderr %q", sub, code, errOut)
		}
	}
	if code, _, errOut := runSprint("stream", "status", "--repo", "x"); code != 2 || strings.Count(errOut, "\n") != 1 {
		t.Errorf("stream status bad repo: exit %d %q", code, errOut)
	}
	if code, _, errOut := runSprint("stream", "bogus"); code != 2 || !strings.Contains(errOut, "open, rebase, pr, status or close") {
		t.Errorf("unknown subverb: %d %q", code, errOut)
	}
}
