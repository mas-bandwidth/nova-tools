package sprinttable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoopsPlistTemplateSetsAbandonProcessGroup(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "fleet", "templates", "nova-loop.plist.j2")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v; fleet/loops.yml renders this template for every loop unit", path, err)
	}
	if !AbandonProcessGroup(string(raw)) {
		t.Fatalf("%s does not set AbandonProcessGroup true; a kickstart -k then SIGTERMs the unit's process group and kills the sprint table refresh", path)
	}
	play, err := os.ReadFile(filepath.Join("..", "..", "fleet", "loops.yml"))
	if err != nil {
		t.Fatalf("reading fleet/loops.yml: %v", err)
	}
	if !strings.Contains(string(play), "templates/nova-loop.plist.j2") {
		t.Fatal("fleet/loops.yml does not render templates/nova-loop.plist.j2")
	}
}

func TestAbandonProcessGroupRejectsAMissingOrFalseKey(t *testing.T) {
	t.Parallel()

	if AbandonProcessGroup("<key>KeepAlive</key>\n<true/>") {
		t.Fatal("a different key read as AbandonProcessGroup")
	}
	if AbandonProcessGroup("<key>AbandonProcessGroup</key>\n<false/>") {
		t.Fatal("false read as abandoning the group")
	}
	if !AbandonProcessGroup("<key>AbandonProcessGroup</key>\n<true/>") {
		t.Fatal("the key set true was not recognised")
	}
}
