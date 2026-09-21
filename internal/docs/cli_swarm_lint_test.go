package docs

import (
	"os"
	"strings"
	"testing"
)

// THE VERB THE REFERENCE DID NOT HAVE (#1855, Emma's item-4 dogfood).
//
// `nova-swarm lint --card` is the one verb a card writer runs before every launch, and
// `docs/CLI.md` -- the reference a person opens -- did not name it or any of its five
// flags. Emma found this by looking for it. A verb in the binary and in nobody's
// reference is a verb that gets re-derived from its own `--help` on every bench.
func TestCLIDocsNameTheSwarmLintVerbAndItsFlags(t *testing.T) {
	b, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("this class test reads docs/CLI.md: %v", err)
	}
	cli := string(b)
	for _, want := range []string{
		"nova-swarm lint",
		"--card <file>",
		"--rules",
		"--typed",
		"--trust <file>",
		"--max <n>",
	} {
		if !strings.Contains(cli, want) {
			t.Errorf("docs/CLI.md does not name %q; the lint is the verb a card writer runs before every launch", want)
		}
	}
	// The verdict a caller reads is the part that must not be left to guesswork: the
	// three lines and which of them changes the exit code.
	for _, want := range []string{"LINT OK", "LINT DRIFT", "LINT NOTE", "LINT SIZE", "advisory"} {
		if !strings.Contains(cli, want) {
			t.Errorf("docs/CLI.md does not name %q; a caller refuses on DRIFT and must be told NOTE is not one", want)
		}
	}
}
