package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// write lays down one fixture file, making its directory first.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// invokePulse runs one nova-pulse invocation in process at a fixed clock.
func invokePulse(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	exit := run(args, &out, &errs, time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	return exit, out.String(), errs.String()
}
