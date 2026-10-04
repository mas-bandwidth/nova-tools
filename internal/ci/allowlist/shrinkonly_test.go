package allowlist

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
)

// Every top-level list under internal/ci/testdata is shrink-only except the
// deleted-tests log; the unit tier's two lists under internal/ci are; a shard of a
// counted ledger, a fixture beside the lists, a Go file and a file elsewhere are not.
func TestShrinkOnly(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		"internal/ci/testdata/dead_code_allowlist.txt":                true,
		"internal/ci/testdata/sharedtemp_allowlist.txt":               true,
		"internal/ci/testdata/deprecated-imports-allowlist.txt":       true,
		"internal/ci/testdata/unexecuted_examples.txt":                true,
		"internal/ci/testdata/generality_text_fixtures_allowlist.txt": true,
		"internal/ci/sleeps-skips_allowlist.txt":                      true,
		"internal/ci/slow-tests_allowlist.txt":                        true,
		"internal/ci/testdata/deleted-tests.txt":                      false,
		"internal/ci/testdata/generality/internal/ci/allowlist.txt":   false,
		"internal/ci/testdata/discarded/cmd.txt":                      false,
		"internal/ci/testdata/dead_code_class_test.go":                false,
		"internal/ci/testdata/fixture.txt":                            false,
		"internal/ci/allowlist/allowlist.go":                          false,
		"docs/SPEC-SPRINT.md":                                         false,
		"":                                                            false,
	} {
		assert.Equal(t, want, ShrinkOnly(p), p)
	}
	// the lists on the tree: every top-level class ledger in testdata (diffcheck.Ledger)
	// but the growing one, and nothing beside them
	entries, err := os.ReadDir(filepath.Join("..", "testdata"))
	require.NoError(t, err)
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := "internal/ci/testdata/" + e.Name()
		if diffcheck.Ledger(p) {
			n++
		}
		assert.Equal(t, diffcheck.Ledger(p) && e.Name() != "deleted-tests.txt", ShrinkOnly(p), p)
	}
	assert.Greater(t, n, 10, "the testdata lists were read")
}
