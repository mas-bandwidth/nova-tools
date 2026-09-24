package main

import (
	"os"
	"path/filepath"
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

func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	code, _, stderr := runSprint("table", "--refresh", "pending")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"flag provided but not defined: -refresh", "--redis <addr>", "--once", "run: nova-sprint help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in %s", want, stderr)
		}
	}
}

func TestThereIsNoQuickstartVerbAndTheCommandReferenceSaysWhy(t *testing.T) {
	code, _, stderr := runSprint("quickstart")
	if code != 2 {
		t.Fatalf("quickstart exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown verb") {
		t.Fatalf("stderr %q, want unknown verb", stderr)
	}
	cli, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cli), "no `quickstart`") {
		t.Error("docs/CLI.md does not say why there is no quickstart verb")
	}
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	runTranscript(t, string(raw), "docs/TESTS.md")
}

// TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints and its two helpers
// live in table_test.go now: docs/CLI.md's `### First run` transcript for
// `nova-sprint table` is the comparator test SPEC-TOOLWORK §7 rule 7 asks for
// (#2218), and it earns a file named for the verb it pins down.
