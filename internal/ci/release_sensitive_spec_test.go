package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/release"
)

// sensitiveMarker is the HTML comment docs/SPEC-RELEASE.md puts immediately
// above the fenced block holding the list. It is a comment rather than a
// heading because a heading is prose somebody will reword, and this test has to
// find the same block in a year.
const sensitiveMarker = "<!-- release-sensitive-paths -->"

// TestTheSensitivePathListIsTheSameInTheCodeAndInTheSpec.
//
// Johnny's decision 1 on SPEC-RELEASE (#1337) is that a cut whose range touched
// certain paths cannot be tagged without his read, and that the list of those
// paths lives in ONE place in code -- internal/release/sensitive.go -- and is
// written out in the spec so a person can read what the gate covers without
// reading Go. Two copies of a security list drift, and the copy that drifts is
// always the one nobody is running. So they are held together here, in the
// package whose charter is exactly a check about the repo read as text: the
// spec's block and release.SensitivePaths must be the same list, in the same
// order, byte for byte.
func TestTheSensitivePathListIsTheSameInTheCodeAndInTheSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-RELEASE.md"))
	inSpec := fencedBlockAfter(t, spec, sensitiveMarker)
	inCode := release.SensitivePaths
	if len(inSpec) != len(inCode) {
		t.Fatalf("the spec lists %d paths and the code lists %d:\nspec: %v\ncode: %v", len(inSpec), len(inCode), inSpec, inCode)
	}
	for i := range inCode {
		if inSpec[i] != inCode[i] {
			t.Errorf("entry %d differs: the spec says %q, the code says %q", i+1, inSpec[i], inCode[i])
		}
	}
	// Every entry is a directory prefix. A list entry that is not one is a
	// list entry that classifies by accident: `internal/secrets` without the
	// slash would also catch `internal/secretsanta/`.
	for _, p := range inCode {
		if !strings.HasSuffix(p, "/") || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			t.Errorf("%q is not a directory prefix", p)
		}
	}
	// And the spec says what the gate DOES, not merely what it covers: the
	// refusal, the flag that gets past it, and the receipt line.
	for _, want := range []string{"--security-read", "RELEASE CUT SENSITIVE", "internal/release/sensitive.go"} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-RELEASE.md does not carry %q", want)
		}
	}
}

// fencedBlockAfter returns the lines of the first ``` block that follows marker.
func fencedBlockAfter(t *testing.T, text, marker string) []string {
	t.Helper()
	_, after, found := strings.Cut(text, marker)
	if !found {
		t.Fatalf("docs/SPEC-RELEASE.md carries no %s marker, so the list cannot be found", marker)
	}
	_, after, found = strings.Cut(after, "```")
	if !found {
		t.Fatalf("no fenced block follows %s", marker)
	}
	// The fence may carry a language tag; the block starts at the next line.
	_, after, _ = strings.Cut(after, "\n")
	block, _, found := strings.Cut(after, "```")
	if !found {
		t.Fatalf("the fenced block after %s is never closed", marker)
	}
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("the fenced block after %s is empty", marker)
	}
	return lines
}
