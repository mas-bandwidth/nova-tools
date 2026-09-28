//go:build functional

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// TestUsageBannerExamplesRun runs the help banner's three examples against a
// throwaway server holding the fixture keyspace (the documented address is
// swapped for the throwaway one), and checks they print a table and leave the
// working directory empty: the table is written nowhere (#3326).
func TestUsageBannerExamplesRun(t *testing.T) {
	code, stdout, stderr := runSprint("help")
	if code != 0 {
		t.Fatalf("help exit %d; stderr %s", code, stderr)
	}
	examples, err := onboarding.ExampleLines(stdout, "nova-sprint")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 3 {
		t.Fatalf("example block has %d commands, want 3", len(examples))
	}
	addr := startThrowawayRedis(t)
	loadTableFunction(t, addr)
	seed(t, addr, table.DefectFixture())
	dir := t.TempDir()
	t.Chdir(dir)
	for _, ex := range examples {
		args := strings.Fields(strings.ReplaceAll(ex, "127.0.0.1:6379", addr))[1:]
		code, out, stderr := runSprint(args...)
		if code != 0 {
			t.Errorf("%s exit %d; stderr %s", ex, code, stderr)
		}
		if out == "" {
			t.Errorf("%s printed nothing", ex)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the examples left %d files in the working directory; the table is written nowhere", len(entries))
	}
}
