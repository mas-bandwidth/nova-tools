package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryCardPushPathLintsOneInvariant is the class test of #4396: every
// card push and card cut path must validate the card's invariants using
// ValidateOneInvariant (or LintOneInvariant) before storing or pushing the card.
func TestEveryCardPushPathLintsOneInvariant(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)

	// Every entry point where cards are cut or pushed must validate one invariant.
	pushPaths := []string{
		"internal/nsprint/card/cut.go",
		"internal/nsprint/card/push.go",
		"internal/nsprint/file/file.go",
		"deprecated/cmd/nova-sprint/card_cut_from.go",
		"deprecated/cmd/nova-sprint/task_card.go",
	}

	checked := 0
	for _, rel := range pushPaths {
		p := filepath.Join(tree.Root, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("could not read card push path %s: %v", rel, err)
		}
		checked++
		src := string(b)
		if !strings.Contains(src, "ValidateOneInvariant") && !strings.Contains(src, "LintOneInvariant") {
			t.Errorf("%s does not call ValidateOneInvariant or LintOneInvariant; all card push and cut paths must lint one invariant", rel)
		}
	}

	if checked != len(pushPaths) {
		t.Fatalf("checked %d paths, expected %d", checked, len(pushPaths))
	}
}
