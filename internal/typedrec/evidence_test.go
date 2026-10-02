package typedrec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

func findRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root")
		}
		dir = parent
	}
}

// 1. TestSpecSwarmContractMatches verifies that docs/SPEC-SWARM.md matches
// typedrec.Contract.Markdown() byte for byte between the typedrec markers.
func TestSpecSwarmContractMatches(t *testing.T) {
	t.Parallel()

	root := findRoot(t)
	specPath := filepath.Join(root, "docs", "SPEC-SWARM.md")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read SPEC-SWARM.md: %v", err)
	}
	s := string(data)
	const beginMarker = "<!-- typedrec:begin -->\n"
	const endMarker = "<!-- typedrec:end -->"
	begin := strings.Index(s, beginMarker)
	if begin == -1 {
		t.Fatal("<!-- typedrec:begin --> not found in docs/SPEC-SWARM.md")
	}
	begin += len(beginMarker)
	end := strings.Index(s[begin:], endMarker)
	if end == -1 {
		t.Fatal("<!-- typedrec:end --> not found in docs/SPEC-SWARM.md")
	}
	got := s[begin : begin+end]
	want := typedrec.Contract.Markdown()
	if got != want {
		t.Fatalf("docs/SPEC-SWARM.md drift:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
	}
}
