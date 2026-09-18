package main

// Rule (4) of 2026-09-18: `dogfood record` began checking the verb's spelling against a
// list, so it requires --cli or --tools -- and its help still printed the signature from
// before, which has neither. A first-time caller typed exactly what the help showed and got
// exit 2. Help that is out of date with the refusal is worse than no help: it spends the
// reader's run to teach them what the binary already knew.

import (
	"os"
	"strings"
	"testing"
)

// TestDogfoodRecordHelpNamesTheVerbListFlags holds the three sub-verbs' signatures against
// what they actually refuse without.
func TestDogfoodRecordHelpNamesTheVerbListFlags(t *testing.T) {
	code, stdout, stderr := dogfoodRun(t, "help")
	if code != 0 {
		t.Fatalf("help exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, verb := range []string{"ledger", "record", "gate"} {
		line := helpLine(stdout, "nova-check dogfood "+verb)
		if line == "" {
			t.Fatalf("help prints no signature for dogfood %s:\n%s", verb, stdout)
		}
		if !strings.Contains(line, "--cli") || !strings.Contains(line, "--tools") {
			t.Errorf("dogfood %s's help does not name the verb list it refuses without: %q", verb, line)
		}
	}
	// And the refusal and the help agree: what record says it wants is what it wants.
	_, _, refused := dogfoodRun(t, "dogfood", "record", "--tool", "t", "--verb", "v",
		"--by", "b", "--notes", "n", "--ok", "--receipts", t.TempDir())
	for _, want := range []string{"--cli", "--tools"} {
		if !strings.Contains(refused, want) {
			t.Errorf("the refusal does not name %s: %q", want, refused)
		}
	}
}

// helpLine is the usage line naming this verb: the first line that holds the prefix.
func helpLine(help, prefix string) string {
	for _, l := range strings.Split(help, "\n") {
		if strings.Contains(l, prefix) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// TestCLIReferenceDogfoodRecordExampleRuns: docs/CLI.md's own example is what a reader
// copies. It showed a `record` with no verb list at all, which is exit 2 today.
func TestCLIReferenceDogfoodRecordExampleRuns(t *testing.T) {
	raw, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Skipf("the command reference is not beside this test: %v", err)
	}
	body := string(raw)
	i := strings.Index(body, "$ nova-check dogfood record")
	if i < 0 {
		t.Fatal("docs/CLI.md carries no dogfood record example")
	}
	example := body[i:]
	if j := strings.Index(example, "\nDOGFOOD"); j > 0 {
		example = example[:j]
	}
	if !strings.Contains(example, "--cli") && !strings.Contains(example, "--tools") {
		t.Errorf("the reference's own example names no verb list, so copying it is exit 2:\n%s", example)
	}
}
