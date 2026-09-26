package main

import (
	"strings"
	"testing"
)

func TestLandWriterUsage(t *testing.T) {
	t.Parallel()

	code, _, errOut := runSprint("land", "writer")
	if code != 2 || !strings.Contains(errOut, "needs --repo") {
		t.Fatalf("bare land writer: code=%d err=%q", code, errOut)
	}

	code, _, errOut = runSprint("land", "writer", "--repo", "o/n", "--base", "dev", "extra")
	if code != 2 || !strings.Contains(errOut, "takes flags") {
		t.Fatalf("positional arg: code=%d err=%q", code, errOut)
	}

	code, _, errOut = runSprint("land", "writer", "--repo", "o/n", "--base", "dev", "--writer", "invalid", "--redis", "127.0.0.1:6379")
	if code != 2 || !strings.Contains(errOut, "--writer must be old-loop or nova-sprint") {
		t.Fatalf("invalid --to: code=%d err=%q", code, errOut)
	}

	code, _, errOut = runSprint("land", "writer", "--repo", "o/n", "--base", "dev")
	if code != 2 || !strings.Contains(errOut, "--redis <addr> is required") {
		t.Fatalf("missing --redis: code=%d err=%q", code, errOut)
	}
}
