package seatcred

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeatEnvironmentPathUsesTheVersionedXDGFile(t *testing.T) {
	t.Parallel()

	path, err := SeatEnvironmentPath(func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return "/tmp/config"
		}
		return "/home/user"
	})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/config/nova-sprint/seat-environment.json", path)
}

func TestLoadSeatEnvironmentRefusesAWorldWritableParentDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permission bits are not enforced by Windows")
	}

	dir := filepath.Join(t.TempDir(), "nova-sprint")
	require.NoError(t, os.Mkdir(dir, 0o777))
	require.NoError(t, os.Chmod(dir, 0o777))
	path := filepath.Join(dir, EnvironmentFile)
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1}`), 0o600))
	_, err := LoadSeatEnvironment(path)
	assert.Error(t, err, "LoadSeatEnvironment accepted a seat file under a world-writable directory")
}

func TestLoadSeatEnvironmentValidatesAndReturnsForgeReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	forge := filepath.Join(root, "gh-config")
	require.NoError(t, os.Mkdir(forge, 0o700))
	path := filepath.Join(root, EnvironmentFile)
	body := `{"version":1,"environment":{"NOVA_SPRINT_REDIS":"127.0.0.1:6381"},"forge_identity":"` + forge + `"}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	record, err := LoadSeatEnvironment(path)
	require.NoError(t, err)
	assert.Equal(t, 1, record.Version)
	assert.Equal(t, forge, record.ForgeIdentity)
	assert.Equal(t, "127.0.0.1:6381", record.Environment["NOVA_SPRINT_REDIS"])
}

func TestLoadSeatEnvironmentRejectsDuplicateUnknownAndCredentialValues(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), EnvironmentFile)
	for _, body := range []string{
		`{"version":1,"version":1}`,
		`{"Version":1}`,
		`{"version":1,"unknown":true}`,
		`{"version":1,"environment":{"nova_sprint_redis":"127.0.0.1:6381"}}`,
		`{"version":1,"environment":{"NOVA_SPRINT_REDIS":"redis://user:password@host"}}`,
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		_, err := LoadSeatEnvironment(path)
		assert.Error(t, err, "LoadSeatEnvironment(%s) accepted invalid data", body)
	}
}

func TestForgeCommandEnvUsesSeatDefaultButKeepsExplicitOverrides(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	forge := filepath.Join(root, "gh-config")
	require.NoError(t, os.Mkdir(forge, 0o700))
	path := filepath.Join(root, EnvironmentFile)
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"forge_identity":"`+forge+`"}`), 0o600))
	getenv := func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return root
		}
		if key == "HOME" {
			return root
		}
		return ""
	}

	got, err := ForgeCommandEnv([]string{"PATH=/bin"}, getenv)
	require.NoError(t, err)
	assert.Contains(t, got, ForgeConfigEnv+"="+forge)

	got, err = ForgeCommandEnv([]string{"PATH=/bin", ForgeConfigEnv + "=/explicit"}, getenv)
	require.NoError(t, err)
	assert.Contains(t, got, ForgeConfigEnv+"=/explicit")
	assert.NotContains(t, got, ForgeConfigEnv+"="+forge)

	got, err = ForgeCommandEnv([]string{"PATH=/bin", ForgeConfigEnv + "=/first", ForgeConfigEnv + "=/last"}, getenv)
	require.NoError(t, err)
	assert.Equal(t, []string{"PATH=/bin", ForgeConfigEnv + "=/last"}, got)

	process := func(key string) string {
		if key == ForgeConfigEnv {
			return "/process"
		}
		return getenv(key)
	}
	got, err = ForgeCommandEnv([]string{"PATH=/bin", ForgeConfigEnv + "=/child"}, process)
	require.NoError(t, err)
	assert.Equal(t, []string{"PATH=/bin", ForgeConfigEnv + "=/process"}, got)
}
