package scaffold

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScaffoldCoverCheck covers Check (internal/scaffold/scaffold.go:94), the
// dry-run counterpart of Write: it runs the same preflight over every planned
// file and returns the rels it would lay down, without writing anything. The
// defect this card fixes is that no unit test reached Check, which sat at 0.0%
// coverage in go tool cover -func.
func TestScaffoldCoverCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(t *testing.T, tree string)
		outs     []Planned
		wantRels []string
		wantErr  string
	}{
		{
			name: "dry_run_returns_rels_and_writes_nothing",
			outs: []Planned{
				{Rel: "pkg/a.txt", Data: []byte("hello a")},
				{Rel: "pkg/sub/b.txt", Data: []byte("hello b")},
			},
			wantRels: []string{"pkg/a.txt", "pkg/sub/b.txt"},
		},
		{
			name: "refuses_when_a_file_already_exists",
			setup: func(t *testing.T, tree string) {
				require.NoError(t, os.MkdirAll(filepath.Join(tree, "pkg"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(tree, "pkg", "a.txt"), []byte("old"), 0o644))
			},
			outs: []Planned{
				{Rel: "pkg/a.txt", Data: []byte("new")},
			},
			wantErr: "already exists",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tree := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, tree)
			}
			before := snapshotFiles(t, tree)

			rels, err := Check(tree, tc.outs)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Nil(t, rels)
				assert.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantRels, rels)
			}

			// Check is a dry run: it wrote nothing, so the tree's file
			// set is unchanged from before the call.
			assert.Equal(t, before, snapshotFiles(t, tree), "tree changed despite dry run")
		})
	}
}

// snapshotFiles walks tree and returns rel -> contents for every regular file,
// so a test can prove a dry-run function wrote nothing new.
func snapshotFiles(t *testing.T, tree string) map[string]string {
	t.Helper()
	got := map[string]string{}
	require.NoError(t, filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(tree, p)
		require.NoError(t, e)
		b, e := os.ReadFile(p)
		if e == nil {
			got[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	}))
	return got
}
