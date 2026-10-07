package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLandCoverDefaultLandRootUnderTheUserCache runs defaultLandRoot's main
// path (land.go): land keeps its clones at nova-sprint/land in the user's
// cache directory, whatever the environment that directory is.
func TestLandCoverDefaultLandRootUnderTheUserCache(t *testing.T) {
	t.Parallel()
	cache, err := os.UserCacheDir()
	require.NoError(t, err)
	cases := []struct {
		name string
		want string
	}{
		{
			name: "nova-sprint/land under the user's cache directory",
			want: filepath.Join(cache, "nova-sprint", "land"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := defaultLandRoot()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestLandCoverAppLandRootSeamDefaultsToDefaultLandRoot pins the wiring a
// fresh app holds: the landRoot seam is defaultLandRoot itself, so a run with
// no --repo-dir keeps its clones in the cache directory until a test replaces
// the seam.
func TestLandCoverAppLandRootSeamDefaultsToDefaultLandRoot(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	require.NotNil(t, a.landRoot)
	assert.Equal(t,
		reflect.ValueOf(defaultLandRoot).Pointer(),
		reflect.ValueOf(a.landRoot).Pointer(),
		"a fresh app's landRoot seam is defaultLandRoot")
}
