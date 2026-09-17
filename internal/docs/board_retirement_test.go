package docs

import (
	"os"
	"strings"
	"testing"
)

// TestNovaBoardRetainedAsInputAdapter596 pins the first bounded part of
// nova-tools #596. nova-board derives the owed list by folding GitHub and bus
// events; nova-work holds the same state as truth with leases, attempts and
// evidence, and `who` and `check` are its views, so a second inferred source
// can disagree with the first. The issue's own order is that the tool and
// SPEC-BOARD are retired only AFTER nova-work's views and event adapter replace
// them and are dogfooded, and that the fold is kept until then as the input
// adapter. This test holds that condition in the two pages a reader meets
// first: docs/SPEC-BOARD.md must carry the status and the pointer, and
// docs/TERMINOLOGY.md must give the board its own entry saying the same. The
// tool itself, cmd/nova-board and internal/board, must still exist, because a
// retirement that removes the fold before the replacement is dogfooded is the
// bug the issue exists to prevent.
func TestNovaBoardRetainedAsInputAdapter596(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-BOARD.md")
	if err != nil {
		t.Fatalf("docs/SPEC-BOARD.md: %v", err)
	}
	content := string(spec)
	for _, want := range []string{
		"## Status: the input adapter, not yet retired",
		"retired only after",
		"`who`",
		"`check`",
		"dogfooded",
		"input adapter",
		"SPEC-WORK.md",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC-BOARD.md missing %q (nova-tools #596)", want)
		}
	}

	term, err := os.ReadFile("../../docs/TERMINOLOGY.md")
	if err != nil {
		t.Fatalf("docs/TERMINOLOGY.md: %v", err)
	}
	terms := string(term)
	for _, want := range []string{
		"- **board** —",
		"input adapter",
		"SPEC-WORK.md",
		"SPEC-BOARD.md",
	} {
		if !strings.Contains(terms, want) {
			t.Errorf("docs/TERMINOLOGY.md missing %q (nova-tools #596)", want)
		}
	}

	// The fold is kept: the binary and the package that fold the events are
	// still here, because the issue retires them only after the replacement is
	// dogfooded.
	for _, path := range []string{
		"../../cmd/nova-board/main.go",
		"../../internal/board/derive.go",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s must remain until the replacement is dogfooded: %v (nova-tools #596)", path, err)
		}
	}
}
