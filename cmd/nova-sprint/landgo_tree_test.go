package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tree package the clone holds only as a directory of data is not run by the gate: on
// 2026-10-05 the nova-sprint repo's internal/ci held testdata and no .go file, `go test
// ./internal/ci/` failed with "no Go files", and every batch onto that repo's main was
// refused as a red base.
func TestTheTreeGateRunsOnlyTreePackagesThatHoldGoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x\n"), 0o644))
	}
	assert.Empty(t, treePackages(dir), "neither directory exists")

	write("internal/ci/testdata/ledger.txt")
	assert.Empty(t, treePackages(dir), "internal/ci holds data only")

	write("internal/docs/docs_test.go")
	assert.Equal(t, []string{"internal/docs"}, treePackages(dir))

	write("internal/ci/ci.go")
	assert.Equal(t, []string{"internal/docs", "internal/ci"}, treePackages(dir))
}
