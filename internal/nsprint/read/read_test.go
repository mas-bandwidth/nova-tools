package read_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
)

func TestOutsidePaths(t *testing.T) {
	t.Parallel()

	got := read.OutsidePaths("internal/x, docs/CLI.md", []string{"internal/x/a.go", "internal/xy/b.go", "docs/CLI.md", "docs/other.md"})
	if strings.Join(got, ",") != "internal/xy/b.go,docs/other.md" {
		t.Fatalf("outside %q", got)
	}
	if got := read.OutsidePaths("", []string{"a"}); len(got) != 1 {
		t.Fatalf("empty PATHS covers nothing, got %q", got)
	}
}
