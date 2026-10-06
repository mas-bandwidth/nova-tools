package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSprintPackagesAreGone pins the 2026-10-04 split (ideas#850): nova-sprint,
// nova-card and nova-work, and the packages only they used, live in
// mas-bandwidth/nova-sprint. nova-tools holds the building blocks and none of
// the sprint's own policy, so none of these directories may come back.
func TestSprintPackagesAreGone(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	for _, dir := range []string{
		"cmd/nova-sprint", "cmd/nova-card", "cmd/nova-work",
		"internal/sprint", "internal/sprintdash", "internal/cardgen",
		"internal/provbalance", "internal/workfile", "internal/workgh",
		"internal/worklang", "tools/sprintsize",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir)))
		assert.True(t, os.IsNotExist(err), "%s moved to mas-bandwidth/nova-sprint and must not return", dir)
	}
}
