package docs

import (
	"os"
	"strings"
	"testing"
)

func TestSlowThing(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/TESTS.md")
	if err != nil {
		t.Fatalf("docs/TESTS.md: %v", err)
	}
	content := string(body)

	novaCISection, found := cutSection(content, "nova-ci")
	if !found {
		t.Fatal("docs/TESTS.md has no `## nova-ci` section; the slowtests verb is described there")
	}

	for _, want := range []struct{ phrase, why string }{
		{"newline-delimited `go test -json`", "the spec must say that the input is a newline-delimited go test -json stream"},
		{"Each package's total is its package-level `Elapsed`", "the spec must explain that a package's total elapsed is its package-level Elapsed field"},
		{"`slowest=`", "the spec must name the slowest= list"},
		{"`CI-SLOW OK packages=0 slowest=none`", "the spec must describe the empty-stdin case"},
		{"`--budget`", "the spec must describe the --budget flag"},
	} {
		if !strings.Contains(novaCISection, want.phrase) {
			t.Errorf("docs/TESTS.md's `## nova-ci` section missing %q: %s", want.phrase, want.why)
		}
	}
}

func cutSection(content, section string) (string, bool) {
	header := "\n## " + section
	i := strings.Index(content, header)
	if i < 0 {
		return "", false
	}
	rest := content[i+len(header):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest, true
}
