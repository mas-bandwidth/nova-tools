package dshdesktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The desktop profile's patch layer as the harness writes it, with one user
// override; the fixture never reads the real ~/.dsh.
const patchFixture = `# Your patch layer for this dsh profile, applied after every bundle layer.
- id: ui-theme
  name: "@deepseek-ai/dsh-client-ui-theme"
  config:
    fontSize: 22
`

func profile(t *testing.T, patch string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profiles", "desktop")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	if patch != "" {
		require.NoError(t, os.WriteFile(PatchFile(dir), []byte(patch), 0o600))
	}
	return dir
}

func TestMountAppendsIncludeKeepingTheUsersLayer(t *testing.T) {
	t.Parallel()
	dir := profile(t, patchFixture)
	changed, err := Mount(dir, "/Users/ada/.dsh/ada-hook/hooks.json")
	require.NoError(t, err)
	assert.True(t, changed)
	data, err := os.ReadFile(PatchFile(dir))
	require.NoError(t, err)
	assert.Contains(t, string(data), patchFixture, "the user's entries and comments are kept byte for byte")
	assert.True(t, Mounted(dir, "/Users/ada/.dsh/ada-hook/hooks.json"))
	info, err := os.Stat(PatchFile(dir))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	changed, err = Mount(dir, "/Users/ada/.dsh/ada-hook/hooks.json")
	require.NoError(t, err)
	assert.False(t, changed, "an existing mount is idempotent and needs no restart")
	again, err := os.ReadFile(PatchFile(dir))
	require.NoError(t, err)
	assert.Equal(t, data, again)
}

func TestMountRecognisesAHandWrittenMountAndRepointsAMovedOne(t *testing.T) {
	t.Parallel()
	hand := patchFixture + `- insert:
    - id: nova-hook-dsh-include
      name: "cordis:include"
      config:
        path: "/Users/ada/.dsh/ada-hook/hooks.json"
`
	dir := profile(t, hand)
	changed, err := Mount(dir, "/Users/ada/.dsh/ada-hook/hooks.json")
	require.NoError(t, err)
	assert.False(t, changed)

	changed, err = Mount(dir, "/Users/ada/.dsh/bob-hook/hooks.json")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.True(t, Mounted(dir, "/Users/ada/.dsh/bob-hook/hooks.json"))
	assert.False(t, Mounted(dir, "/Users/ada/.dsh/ada-hook/hooks.json"), "repointed, never a second entry with the same id")
	var doc []map[string]any
	data, err := os.ReadFile(PatchFile(dir))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, &doc))
	assert.Len(t, doc, 2)
}

func TestMountEmptyAndMissingLayers(t *testing.T) {
	t.Parallel()
	for name, patch := range map[string]string{"missing": "", "flow empty": "# header\n[]\n"} {
		t.Run(name, func(t *testing.T) {
			dir := profile(t, patch)
			changed, err := Mount(dir, "/h/hooks.json")
			require.NoError(t, err)
			assert.True(t, changed)
			assert.True(t, Mounted(dir, "/h/hooks.json"))
		})
	}
}

func TestMountRefusals(t *testing.T) {
	t.Parallel()
	_, err := Mount(filepath.Join(t.TempDir(), "absent"), "/h/hooks.json")
	require.ErrorContains(t, err, "no DeepSeek Harness desktop profile")
	_, err = Mount(profile(t, "a: map\n"), "/h/hooks.json")
	require.ErrorContains(t, err, "not a top-level list")
	_, err = Mount(profile(t, "- [unclosed\n"), "/h/hooks.json")
	require.ErrorContains(t, err, "not YAML")
	_, err = Mount(profile(t, patchFixture), "relative/hooks.json")
	require.Error(t, err)
}
