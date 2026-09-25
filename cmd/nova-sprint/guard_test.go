package main

import (
	"bytes"
	"testing"
	"time"
)

func TestNovaSprintVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdVersion(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if stdout.Len() == 0 {
		t.Fatal("expected version output")
	}
}

func TestRunBenchGuard(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBench("t", "unknown", time.Second, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for unknown loop, got %d", code)
	}
}
