package docs

import (
	"os"
	"strings"
	"testing"
)

// TestLessonsFileIsCappedAndReadByEveryCard guards nova-tools#2498 S9: the
// repository's reviewed lessons stay small enough to include in every card,
// and each card template tells the worker to consume them as data.
func TestLessonsFileIsCappedAndReadByEveryCard(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/LESSONS.md")
	if err != nil {
		t.Fatalf("docs/LESSONS.md: %v", err)
	}
	lines := strings.Count(string(raw), "\n")
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		lines++
	}
	if lines > 40 {
		t.Fatalf("docs/LESSONS.md has %d lines; cap is 40", lines)
	}
	for _, kind := range []string{"build", "fix", "read"} {
		path := "../nsprint/brief/tmpl/" + kind + ".tmpl"
		tmpl, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !strings.Contains(string(tmpl), "Read `docs/LESSONS.md`") {
			t.Errorf("%s does not require the card to read docs/LESSONS.md", path)
		}
	}
}
