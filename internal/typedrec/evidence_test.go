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

func makeDoc(kind, status, check string, fields map[string]string, evidence string) string {
	var b strings.Builder
	b.WriteString("RESULT CARD-100 sha=1234567890ab repo/name: description\n")
	if status == "DONE" {
		b.WriteString("DONE\n")
	} else {
		b.WriteString(status + "\n")
	}
	b.WriteString("SCHEMA: v2\n")
	b.WriteString("KIND: " + kind + "\n")
	b.WriteString("ATTEMPT: 1\n")
	b.WriteString("CHECK: " + check + "\n")
	b.WriteString("REPO: mas-bandwidth/nova-tools\n")

	for k, v := range fields {
		if k == "SCHEMA" || k == "KIND" || k == "ATTEMPT" || k == "CHECK" || k == "REPO" {
			continue
		}
		b.WriteString(k + ": " + v + "\n")
	}

	if evidence != "" {
		b.WriteString(evidence)
	}
	return b.String()
}

func findDefault(k string) string {
	switch k {
	case "REPO":
		return "mas-bandwidth/nova-tools"
	case "ATTEMPT":
		return "1"
	case "CHECK":
		return "pass"
	}
	return ""
}
