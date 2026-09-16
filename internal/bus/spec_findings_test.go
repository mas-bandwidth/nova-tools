package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-LOCAL.md Sources must not pin personal home paths (#462).
// A spec on a public repo should cite repo+sha, not /Users/<name>/….
func TestSpecLocalNoHomePaths(t *testing.T) {
	t.Parallel()
	dir := specDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "SPEC-LOCAL.md"))
	if err != nil {
		t.Fatalf("read SPEC-LOCAL.md: %v", err)
	}
	content := string(data)
	for _, needle := range []string{"/Users/glenn", "/Users/rowan"} {
		if strings.Contains(content, needle) {
			t.Errorf("SPEC-LOCAL.md contains pinned path %q; cite repo+sha instead", needle)
		}
	}
}

// SPEC-WAKE.md must acknowledge the third shape: nova-bus wait as an OS process outside the turn.
// The "two shapes / no third" claim on line 329 contradicts the table on line 35 that names
// nova-bus wait as the third shape (#462).
func TestSpecWakeThirdShape(t *testing.T) {
	t.Parallel()
	dir := specDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "SPEC-WAKE.md"))
	if err != nil {
		t.Fatalf("read SPEC-WAKE.md: %v", err)
	}
	content := string(data)
	// The document must mention nova-bus wait as the third shape.
	if !strings.Contains(content, "nova-bus wait") {
		t.Errorf("SPEC-WAKE.md does not mention nova-bus wait as the third shape")
	}
	// The "exactly two" and "There is no third" claims are contradictory once nova-bus wait is
	// named as the third shape in the lessons table. Replace the "two shapes" sentence
	// with one that allows the third shape.
	if strings.Contains(content, "The two shapes are exactly two") &&
		strings.Contains(content, "There is no\nthird") {
		t.Errorf("SPEC-WAKE.md says 'two shapes' and 'no third' but also names nova-bus wait as the third shape")
	}
}

// SPEC-WORK.md must name the five read-path indexes and keep six consistent (#462).
func TestSpecWorkIndexCount(t *testing.T) {
	t.Parallel()
	dir := specDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "SPEC-WORK.md"))
	if err != nil {
		t.Fatalf("read SPEC-WORK.md: %v", err)
	}
	content := string(data)
	// The document must name the five read-path indexes explicitly.
	for _, name := range []string{
		"id to node",
		"containment adjacency",
		"reverse dependency",
		"repository",
		"category",
	} {
		if !strings.Contains(content, name) {
			t.Errorf("SPEC-WORK.md does not name the five read-path index %q", name)
		}
	}
	// The sixth index (reverse roadmap) must be named.
	if !strings.Contains(content, "reverse roadmap") {
		t.Errorf("SPEC-WORK.md does not name reverse roadmap as the sixth index")
	}
}

func specDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatal("could not find repo root")
		}
		cwd = parent
	}
	docs := filepath.Join(cwd, "docs")
	if _, err := os.Stat(docs); err != nil {
		t.Fatalf("docs directory not found at %s: %v", docs, err)
	}
	return docs
}
