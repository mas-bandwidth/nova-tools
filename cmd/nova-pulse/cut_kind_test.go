package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The #1852 reproducer through the verb: cut --kind fix writes a card whose typed
// header lint --card will read, not a SOURCE: line that trips the checks and then
// finds no KIND/PATHS/TEST.
func TestCutKindFixCLIWritesTypedHeaders(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "out-kind"), filepath.Join(dir, "queue-kind")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"cut", "--kind", "fix",
		"--repo", "mas-bandwidth/nova-tools",
		"--issue", "123",
		"--title", "fix",
		"--out", out,
		"--queue", queue,
	}, &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("cut --kind exit = %d, stderr=%s", code, stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, "card-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	card := string(raw)
	for _, want := range []string{
		"KIND: fix\n",
		"PATHS: none\n",
		"TEST: none\n",
		"SOURCE: mas-bandwidth/nova-tools#123\n",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "mas-bandwidth/nova-tools mas-bandwidth/nova-tools") {
		t.Errorf("SOURCE: repeats the repo:\n%s", card)
	}
}
