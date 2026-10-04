package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLandCoverDefaultLandRootIsTheCacheDirectory covers defaultLandRoot
// (cmd/nova-sprint/land.go:1143, the finding's 0.0%). Its main path is the
// user cache directory's nova-sprint/land: the rows pin the join against
// os.UserCacheDir, the call the function makes, and the last two components.
// When the process has no cache directory (no HOME, no XDG_CACHE_HOME) the
// same call is the refusal branch, and the test pins that the fault is
// returned unchanged with no path; that branch cannot be forced here without
// mutating the process environment, which the unit tier forbids (no t.Setenv,
// no serial test), so this run takes the main path and the report names the
// refusal as not reached.
func TestLandCoverDefaultLandRootIsTheCacheDirectory(t *testing.T) {
	t.Parallel()
	cache, cacheErr := os.UserCacheDir()
	root, err := defaultLandRoot()
	if cacheErr != nil {
		require.Error(t, err)
		assert.Empty(t, root, "a cache directory that cannot be found names no root")
		return
	}
	require.NoError(t, err)
	cases := []struct {
		name string
		got  func() string
		want string
	}{
		{
			name: "the root is the cache directory's nova-sprint/land",
			got:  func() string { return root },
			want: filepath.Join(cache, "nova-sprint", "land"),
		},
		{
			name: "the last two components are nova-sprint/land",
			got:  func() string { return filepath.Join(filepath.Base(filepath.Dir(root)), filepath.Base(root)) },
			want: filepath.Join("nova-sprint", "land"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got())
		})
	}
}
