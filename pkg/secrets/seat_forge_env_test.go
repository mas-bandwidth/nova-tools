package secrets

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunSealSurfacesForgeEnvironmentResolutionFailure(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	f := newSealFixture(t, "OTHER: keepme\n")
	opts := f.options(t, "TARGET", "value\n", false)
	opts.ResolveGHEnv = func() ([]string, error) {
		return nil, errors.New("seat environment is invalid")
	}
	_, err := RunSeal(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "seat environment is invalid")
	require.Empty(t, readMaybe(t, f.sopsArgs), "seat refusal must happen before helpers run")
	require.Empty(t, readMaybe(t, f.gitArgs), "seat refusal must happen before git runs")
	require.Empty(t, readMaybe(t, f.ghArgs), "seat refusal must happen before gh runs")
}

func TestRunSeatInjectSurfacesForgeEnvironmentResolutionFailure(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	f := newInjectFixture(t)
	opts := f.options("NOVA_REDIS_BENCH_PASSWORD", false)
	opts.ResolveGHEnv = func() ([]string, error) {
		return nil, errors.New("seat environment is invalid")
	}
	_, err := RunSeatInject(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "seat environment is invalid")
	require.Empty(t, readMaybe(t, f.sopsArgs), "seat refusal must happen before helpers run")
	require.Empty(t, readMaybe(t, f.gitArgs), "seat refusal must happen before git runs")
	require.Empty(t, readMaybe(t, f.ghArgs), "seat refusal must happen before gh runs")
}
