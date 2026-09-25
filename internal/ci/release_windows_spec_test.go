package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// LESSON 11 of docs/SPEC-RELEASE.md: the windows bench. The Threadripper is a
// target the fleet has never had, and everything about releasing to it is a
// DECISION rather than a default -- which shell the far side parses, which
// shape of path the flags take, and which check the build cannot make. A
// decision that lives only in a Go comment is a decision the next person
// re-makes differently, so the spec carries it and this holds the spec to it.
func TestTheWindowsBenchIsInTheReleaseSpec(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-RELEASE.md"))
	for _, want := range []string{
		"## 11. ",
		// The shell the far side parses, decided in docs/BENCH-WINDOWS.md
		// and matched here rather than guessed at again.
		"BENCH-WINDOWS.md",
		// The path shapes the verbs take, and the one they compose.
		`C:\Users\nova\.local\bin`,
		"C:/Users/nova/.local/bin",
		// The check that cannot be made from a non-windows builder, said
		// out loud instead of quietly skipped.
		"self-verify",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-RELEASE.md does not carry %q", want)
		}
	}
}

// And the command reference is where a person meets it: the release verbs are
// the last mile, and a windows bench that needs a differently-shaped --bin has
// to say so where the flags are documented (lesson 10's reason, one target on).
func TestTheCommandReferenceShowsAWindowsAdopt(t *testing.T) {
	text := readFile(t, filepath.Join(repoRoot(t), "docs", "CLI.md"))
	for _, want := range []string{"windows-amd64", `C:\Users\nova\.local\bin`} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/CLI.md does not name %s; the windows bench is a target like any other", want)
		}
	}
}
