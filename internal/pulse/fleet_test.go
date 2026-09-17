package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-PULSE ## Fleet: the section names the benches file, the fleet rule,
// and every fleet verb. A verb renamed in the spec and not here (or vice
// versa) is red.
func TestFleetSectionListsVerbs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(doc), "\n## Fleet\n")
	if !ok {
		t.Fatal("the spec has no ## Fleet section")
	}
	end := strings.Index(after, "\n## ")
	section := after
	if end >= 0 {
		section = after[:end]
	}
	for _, want := range []string{
		"fleet survey",
		"fleet reboot",
		"fleet secrets",
		"fleet suspend",
		"fleet wake",
		"fleet standard",
		"FLEET ",
		"--ssh",
		"studio",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the ## Fleet section does not name %q", want)
		}
	}
}
