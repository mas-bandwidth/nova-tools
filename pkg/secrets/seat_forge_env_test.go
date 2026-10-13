package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/stretchr/testify/require"
)

func invalidSeatForgeGetenv(root string) func(string) string {
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return root
		}
		return ""
	}
}

func writeInvalidSeatForgeEnvironment(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, seatcred.EnvironmentDir)
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, seatcred.EnvironmentFile), []byte(`{"version":1,"forge_identity":null}`), 0600))
}

func TestRunSealSurfacesInvalidSeatForgeEnvironment(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	f := newSealFixture(t, "OTHER: keepme\n")
	root := t.TempDir()
	writeInvalidSeatForgeEnvironment(t, root)
	opts := f.options(t, "TARGET", "value\n", false)
	opts.ResolveGHEnv = func() ([]string, error) {
		return seatcred.ForgeCommandEnv(nil, invalidSeatForgeGetenv(root))
	}
	_, err := RunSeal(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "seat environment is invalid")
	require.Empty(t, readMaybe(t, f.sopsArgs), "seat refusal must happen before helpers run")
	require.Empty(t, readMaybe(t, f.gitArgs), "seat refusal must happen before git runs")
	require.Empty(t, readMaybe(t, f.ghArgs), "seat refusal must happen before gh runs")
}

func TestRunSeatInjectSurfacesInvalidSeatForgeEnvironment(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	f := newInjectFixture(t)
	root := t.TempDir()
	writeInvalidSeatForgeEnvironment(t, root)
	opts := f.options("NOVA_REDIS_BENCH_PASSWORD", false)
	opts.ResolveGHEnv = func() ([]string, error) {
		return seatcred.ForgeCommandEnv(nil, invalidSeatForgeGetenv(root))
	}
	_, err := RunSeatInject(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "seat environment is invalid")
	require.Empty(t, readMaybe(t, f.sopsArgs), "seat refusal must happen before helpers run")
	require.Empty(t, readMaybe(t, f.gitArgs), "seat refusal must happen before git runs")
	require.Empty(t, readMaybe(t, f.ghArgs), "seat refusal must happen before gh runs")
}
