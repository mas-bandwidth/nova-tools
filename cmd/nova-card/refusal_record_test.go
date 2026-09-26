package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelpLeavesNoRefusal: a run that refuses nothing writes no refusal
// record (the version verb names no card).
func TestHelpLeavesNoRefusal(t *testing.T) {
	t.Parallel()

	results := t.TempDir()
	env := map[string]string{"NOVA_CARD_RESULTS": results}
	var out, errb strings.Builder
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb, func(k string) string { return env[k] }); code != 0 {
		t.Fatalf("version exits %d", code)
	}
	if _, err := os.Stat(filepath.Join(results, "refused")); err == nil {
		t.Fatal("version wrote a refusal record")
	}
}
