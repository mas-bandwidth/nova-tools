package main

import (
	"strings"
	"testing"
)

func TestFnRefusesWithoutRedis(t *testing.T) {
	t.Parallel()

	for _, sub := range []string{"load", "check"} {
		code, _, errOut := runSprint("fn", sub)
		if code != 2 || !strings.Contains(errOut, "--redis") {
			t.Errorf("fn %s without --redis: code=%d err=%q, want 2 naming --redis", sub, code, errOut)
		}
	}
	code, _, errOut := runSprint("fn")
	if code != 2 || !strings.Contains(errOut, "load or check") {
		t.Errorf("bare fn: code=%d err=%q", code, errOut)
	}
}
