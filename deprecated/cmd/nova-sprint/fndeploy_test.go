package main

import (
	"testing"
)

func TestFnDeployUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"fn", "deploy"},
		{"fn", "deploy", "--redis", "127.0.0.1:1", "--want", "short"},
		{"fn", "deploy", "--redis", "127.0.0.1:1", "extra"},
		{"fn", "sum", "--redis", "127.0.0.1:1"},
	} {
		if code, _, errOut := runSprint(args...); code != 2 || errOut == "" {
			t.Errorf("%v: code=%d err=%q, want 2 with a reason", args, code, errOut)
		}
	}
}
