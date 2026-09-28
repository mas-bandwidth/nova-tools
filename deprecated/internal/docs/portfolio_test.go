package docs

import (
	"os"
	"strings"
	"testing"
)

// TestPortfolioTenToolFamilies verifies that the coordinator-token improvement
// portfolio exists in docs/ and carries the ten priority tool families table.
// Issue #249: Glenn asked for a collation of the improvement inventory; the
// portfolio is the durable home for that ranking, so a missing file or a table
// with fewer than ten rows is a bug.
func TestPortfolioTenToolFamilies(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/PORTFOLIO.md")
	if err != nil {
		t.Fatalf("docs/PORTFOLIO.md: %v", err)
	}

	content := string(body)

	// The heading must be present so a reader can find it.
	if !strings.Contains(content, "# Coordinator-token improvement portfolio") {
		t.Error("missing heading: # Coordinator-token improvement portfolio")
	}

	// The ten-row table is the core of the portfolio; fewer than ten tool rows
	// means the collation is incomplete. Count the markdown table rows that
	// carry a rank number in the first column (| 1 | through | 10 |).
	rows := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "| 1 ") || strings.HasPrefix(trimmed, "| 2 ") ||
			strings.HasPrefix(trimmed, "| 3 ") || strings.HasPrefix(trimmed, "| 4 ") ||
			strings.HasPrefix(trimmed, "| 5 ") || strings.HasPrefix(trimmed, "| 6 ") ||
			strings.HasPrefix(trimmed, "| 7 ") || strings.HasPrefix(trimmed, "| 8 ") ||
			strings.HasPrefix(trimmed, "| 9 ") || strings.HasPrefix(trimmed, "| 10 ") {
			rows++
		}
	}
	if rows < 10 {
		t.Errorf("the ten priority tool families table has %d rows, want 10", rows)
	}
}
