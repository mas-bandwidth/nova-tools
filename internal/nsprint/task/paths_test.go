package task_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// TestPathsParseAndIntersect pins the title grammar and the check-cut.py match
// rule the push lint shares.
func TestPathsParseAndIntersect(t *testing.T) {
	t.Parallel()

	paths, deps := task.ParseTitle("t | DONE-WHEN: a | PATHS: `cmd/a.go`, internal/b; rowan-tools: bin/c | DEPENDS-ON: #2929 (landed), build-x", "nova-tools")
	want := []string{"nova-tools:cmd/a.go", "nova-tools:internal/b", "rowan-tools:bin/c"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("paths = %q; want %q", paths, want)
	}
	if strings.Join(deps, " ") != "#2929 build-x" {
		t.Fatalf("deps = %q; want #2929 build-x", deps)
	}
	for _, c := range []struct {
		a, b string
		hit  bool
	}{
		{"r:cmd/a.go", "r:cmd/a.go", true},
		{"r:internal/b", "r:internal/b/c.go", true},
		{"r:internal/b/", "r:internal/b/c.go", true},
		{"r:internal/b/**", "r:internal/b/c/d.go", true},
		{"r:internal/b/*", "r:internal/b/c.go", true},
		{"r:internal/b/*", "r:internal/b/c/d.go", false},
		{"r:internal/*.go", "r:internal/x.go", true},
		{"r:internal/bc", "r:internal/b", false},
		{"r:cmd/a.go", "s:cmd/a.go", false},
		{":cmd/a.go", "s:cmd/a.go", true},
	} {
		if got := task.PathsIntersect(c.a, c.b); got != c.hit {
			t.Errorf("PathsIntersect(%q, %q) = %v; want %v", c.a, c.b, got, c.hit)
		}
		if got := task.PathsIntersect(c.b, c.a); got != c.hit {
			t.Errorf("PathsIntersect(%q, %q) = %v; want %v", c.b, c.a, got, c.hit)
		}
	}
}
