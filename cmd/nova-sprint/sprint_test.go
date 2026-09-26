package main

import (
	"strings"
	"testing"
)

func TestSprintRefusesABadName(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"sprint"},
		{"sprint", "reopen", "--sprint", "x"},
		{"sprint", "open", "--redis", "127.0.0.1:1"},
		{"sprint", "open", "--redis", "127.0.0.1:1", "--sprint", "Bad Name"},
	} {
		code, out, errOut := runSprint(args...)
		if code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-sprint sprint") {
			t.Errorf("%v: code=%d out=%q stderr=%q; want exit 2 and one refusal", args, code, out, errOut)
		}
	}
}

// ---- #2939 rev 6 fixtures ----
