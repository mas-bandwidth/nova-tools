package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2000StudioYamlNaming keeps the seat-file naming rule from
// nova-tools #2000: a store file without the shared prefix is requested
// under its actual name. The documentation states the rule without host names.
func TestIssue2000StudioYamlNaming(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/TESTS.md")
	if err != nil {
		t.Fatalf("docs/TESTS.md: %v", err)
	}
	content := strings.Join(strings.Fields(string(body)), " ")

	for _, want := range []string{
		"`swarm-` prefix",
		"must be asked for under that file's name",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/TESTS.md missing %q (nova-tools #2000)", want)
		}
	}
}
