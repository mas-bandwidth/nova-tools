package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2000StudioYamlNaming documents and checks that docs/TESTS.md names
// the Studio's secrets store as studio.yaml (without the swarm- prefix), which
// is the one bench whose store file does not follow the swarm-<name>.yaml
// convention. The darwin fill loop lost 80 cards (#2000) because the launcher
// asked for swarm-studio.yaml while the store held studio.yaml.
func TestIssue2000StudioYamlNaming(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/TESTS.md")
	if err != nil {
		t.Fatalf("docs/TESTS.md: %v", err)
	}
	content := string(body)

	for _, want := range []string{
		"studio.yaml",
		"swarm-studio.yaml",
		"swarm-",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/TESTS.md missing %q (nova-tools #2000)", want)
		}
	}
}
