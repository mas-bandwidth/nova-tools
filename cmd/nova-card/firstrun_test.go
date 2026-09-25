package main

import (
	"bytes"
	"testing"
)

func TestFirstRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"help"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("help exited %d", code)
	}
}
