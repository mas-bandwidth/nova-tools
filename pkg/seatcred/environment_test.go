package seatcred

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidSeatEndpointSharesTheSeatEnvironmentRules(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value string
		valid bool
	}{
		{value: "127.0.0.1:6381", valid: true},
		{value: "redis.example:6379", valid: true},
		{value: "mem:6379", valid: false},
		{value: "redis://user:password@host", valid: false},
		{value: "redis.example:not-a-port", valid: false},
	} {
		assert.Equal(t, tc.valid, ValidSeatEndpoint(tc.value), "ValidSeatEndpoint(%q)", tc.value)
	}
}

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
	private := filepath.Join(root, "private")
	require.NoError(t, os.Mkdir(private, 0o700))
	path := filepath.Join(private, EnvironmentFile)
	body, err := json.Marshal(SeatEnvironment{
		Version:       1,
		Environment:   map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6381"},
		ForgeIdentity: forge,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))

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
		`{"version":1,"forge_identity":null}`,
		`{"version":1,"environment":null}`,
		`{"version":1,"login":null}`,
		`{"version":1,"environment":{"nova_sprint_redis":"127.0.0.1:6381"}}`,
		`{"version":1,"environment":{"NOVA_SPRINT_REDIS":"redis.example"}}`,
		`{"version":1,"environment":{"NOVA_SPRINT_REDIS":"redis.example:not-a-port"}}`,
		`{"version":1,"environment":{"NOVA_SPRINT_REDIS":"redis.example:65536"}}`,
		`{"version":1,"environment":{"NOVA_SPRINT_REDIS":"redis://user:password@host"}}`,
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		_, err := LoadSeatEnvironment(path)
		assert.Error(t, err, "LoadSeatEnvironment(%s) accepted invalid data", body)
	}
}

func TestLoadSeatEnvironmentPreservesSafeInputFailureCause(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), EnvironmentFile)
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "duplicate key", body: `{"version":1,"version":1}`, want: "duplicate key"},
		{name: "unknown key", body: `{"version":1,"unexpected":true}`, want: "unknown key"},
		{name: "null value", body: `{"version":1,"login":{"redis":null}}`, want: "null value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
			_, err := LoadSeatEnvironment(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), tc.body, "validation errors must not echo file contents")
		})
	}
}

func TestReadSeatEnvironmentTreatsMissingAbsoluteConfigHomeAsAbsent(t *testing.T) {
	t.Parallel()

	getenv := func(string) string { return "" }
	record, ok, err := ReadSeatEnvironment(getenv)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, record)

	env := []string{"PATH=/bin"}
	got, err := ForgeCommandEnv(env, getenv)
	require.NoError(t, err)
	assert.Equal(t, env, got)
}

func TestForgeCommandEnvUsesSeatDefaultButKeepsExplicitOverrides(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	forge := filepath.Join(root, "gh-config")
	require.NoError(t, os.Mkdir(forge, 0o700))
	path := filepath.Join(root, EnvironmentDir, EnvironmentFile)
	require.NoError(t, os.Mkdir(filepath.Dir(path), 0o700))
	body, err := json.Marshal(SeatEnvironment{Version: 1, ForgeIdentity: forge})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
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
