package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reader's findings on file.go: Path, Applied, Ownership and Close were
// the four functions no unit test reached (0.0% in the unit tier's
// per-function coverage table). These tests cover each one's main path and
// the one refusal beside it, all through the package's own seams: a real
// file under the test's own TempDir, no clock, no network, no store.

// TestFileCoverPathIsThePathTheStoreOpened: Path is the path OpenFile was
// given, before and after migrate makes the file. Path is a pure getter and
// has no refusal to pin.
func TestFileCoverPathIsThePathTheStoreOpened(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "try.json")
	f, err := OpenFile(path)
	require.NoError(t, err)
	assert.Equal(t, path, f.Path())
	_, _, _, err = f.Migrate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, path, f.Path(), "migrate does not rename the file")
}

// TestFileCoverAppliedListsTheLedgerOnceTheFileIsThere: the ledger is every
// migration's version once the file is there and none before, no error
// either way — a file store has no ledger to refuse from.
func TestFileCoverAppliedListsTheLedgerOnceTheFileIsThere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	all, err := Migrations()
	require.NoError(t, err)
	want := make([]int, 0, len(all))
	for _, m := range all {
		want = append(want, m.Version)
	}
	cases := []struct {
		name     string
		migrated bool
		want     []int
	}{
		{"before migrate the file is not there", false, nil},
		{"after migrate every version is applied", true, want},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := OpenFile(filepath.Join(t.TempDir(), "try.json"))
			require.NoError(t, err)
			if tc.migrated {
				_, _, _, err = f.Migrate(ctx)
				require.NoError(t, err)
			}
			applied, err := f.Applied(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, applied)
		})
	}
}

// TestFileCoverOwnershipIsTheZeroOwnership: a file has no roles and no
// owners, before and after migrate, so migrate's preflight has nothing to
// refuse; Ownership itself has no refusal to reach.
func TestFileCoverOwnershipIsTheZeroOwnership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	all, err := Migrations()
	require.NoError(t, err)
	cases := []struct {
		name     string
		migrated bool
	}{
		{"before migrate", false},
		{"after migrate", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := OpenFile(filepath.Join(t.TempDir(), "try.json"))
			require.NoError(t, err)
			if tc.migrated {
				_, _, _, err = f.Migrate(ctx)
				require.NoError(t, err)
			}
			ownership, err := f.Ownership(ctx)
			require.NoError(t, err)
			assert.Equal(t, Ownership{}, ownership, "a file has no roles and no owners")
			assert.Empty(t, MigrateGaps(ownership, Pending(all, 0)), "migrate's preflight has nothing to refuse")
		})
	}
}

// TestFileCoverCloseWritesNothing: Close returns nil, leaves the file's
// bytes as every write left them and makes no file where there is none; it
// has no refusal. A write before it is already saved, so a fresh open still
// reads the row.
func TestFileCoverCloseWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name     string
		migrated bool
	}{
		{"a store whose file is there", true},
		{"a store whose file is not there", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "try.json")
			f, err := OpenFile(path)
			require.NoError(t, err)
			if tc.migrated {
				_, _, _, err = f.Migrate(ctx)
				require.NoError(t, err)
				_, err = f.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{"user": "u", "seat": "s", "slots": "1"}}, "a1")
				require.NoError(t, err)
			}
			before, err := os.ReadFile(path)
			if tc.migrated {
				require.NoError(t, err)
			} else {
				require.Error(t, err, "no file yet")
			}
			require.NoError(t, f.Close())
			after, err := os.ReadFile(path)
			if tc.migrated {
				require.NoError(t, err)
				assert.Equal(t, before, after, "Close wrote nothing")
				kept, err := OpenFile(path)
				require.NoError(t, err)
				_, found, err := kept.Get(ctx, KindMachine, "m1")
				require.NoError(t, err)
				assert.True(t, found, "the write was saved as it landed, not at Close")
			} else {
				assert.Error(t, err, "Close makes no file")
			}
		})
	}
}
