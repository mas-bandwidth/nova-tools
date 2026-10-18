package ci

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 2026-10-17 (Glenn, restated): no real sockets in unit tests, ever. The
// unit-sockets ledger was the burn-down of the files that still opened one; the
// sweep empties it, and from then on any row is red: a socket test is a
// functional test (//go:build functional) or a test through a seam.
func TestTheUnitSocketsLedgerHoldsNoRow(t *testing.T) {
	t.Parallel()
	var rows []string
	err := filepath.WalkDir(unitSocketsLedgerPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				rows = append(rows, l)
			}
		}
		return nil
	})
	if !errors.Is(err, fs.ErrNotExist) {
		require.NoError(t, err)
	}
	assert.Empty(t, rows, "the unit-sockets ledger still allows %d files a real socket:\n%s", len(rows), strings.Join(rows, "\n"))
}
