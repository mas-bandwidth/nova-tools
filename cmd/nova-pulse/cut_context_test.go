package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cut with --index pointing at a directory never built is refused, not silently bare.
func TestCutRefusesAnUnbuiltIndex(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"cut", "--kind", "fix", "--repo", "mas-bandwidth/nova-tools", "--issue", "1",
		"--title", "x", "--index", filepath.Join(dir, "nope"), "--out", filepath.Join(queue, "pending"), "--queue", queue},
		&out, &errb, time.Now().UTC())
	if code != 2 || !strings.Contains(errb.String(), "CUT REFUSED: --index") {
		t.Fatalf("exit = %d, stderr=%q; want 2 and a CUT REFUSED naming --index", code, errb.String())
	}
}
