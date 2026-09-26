package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	t.Parallel()

	code, _, stderr := runSprint("table", "--refresh", "pending")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--refresh is not a flag of nova-sprint table", "--redis", "--once", "without it: nova-sprint table", "usage: nova-sprint table"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in %s", want, stderr)
		}
	}
}

func TestThereIsNoQuickstartVerbAndTheCommandReferenceSaysWhy(t *testing.T) {
	t.Parallel()

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
