package land_test

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// b3Tip is the fixture base tip every unit's parent points at.
const b3Tip = "1111111111111111111111111111111111111111"

// addUnit writes a unit at head and makes it landable (tier 0).
func addUnit(t *testing.T, f *landTestFixture, n int, head, files, parent, author string) string {
	t.Helper()
	unit := fmt.Sprintf("gh/mas-bandwidth/nova-tools/%d", n)
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: fmt.Sprintf("card-%d", n), Head: head, BaseSHA: b3Tip,
		Files: files, StackParent: parent, Author: author,
	}); err != nil {
		t.Fatalf("unit head %d: %v", n, err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval %d: %v", n, err)
	}
	return unit
}

func sha(n int) string { return fmt.Sprintf("%040d", n) }
