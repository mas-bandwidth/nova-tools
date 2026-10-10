//go:build functional

package seatcred_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
)

// TestResolveReadsTheSeatThroughTheSecretsLibrary is the resolution half of
// #4052: a real store, sealed by sops to a fresh age key, read from the
// default layout under HOME with nothing in the environment but HOME and PATH.
func TestResolveReadsTheSeatThroughTheSecretsLibrary(t *testing.T) {
	t.Parallel()

	const coord, bench = "coord-test-pw-0f3a9c", "bench-test-pw-7d21e4"
	home := seattest.Home(t, "studio", map[string]string{
		"NOVA_REDIS_COORDINATOR_PASSWORD": coord,
		"NOVA_REDIS_BENCH_PASSWORD":       bench,
		"GH_TOKEN":                        "gh-test-value-not-redis",
	})
	mockEnv := map[string]string{
		"HOME":           home,
		seatcred.SopsEnv: seattest.Sops(t),
	}
	getenv := func(k string) string { return mockEnv[k] }

	c, err := seatcred.Resolve("studio", getenv)
	require.NoError(t, err, "Resolve: %v", err)
	require.Equal(t, "studio", c.Seat, "Resolve = %v; want studio as coordinator with the coordinator password", c)
	require.Equal(t, "coordinator", c.User, "Resolve = %v; want studio as coordinator with the coordinator password", c)
	require.Equal(t, "NOVA_REDIS_COORDINATOR_PASSWORD", c.Key, "Resolve = %v; want studio as coordinator with the coordinator password", c)
	require.True(t, same(c, coord), "Resolve = %v; want studio as coordinator with the coordinator password", c)
	for _, s := range []string{fmt.Sprintf("%v %+v %#v %s %q %x", c, c, c, c, c, c)} {
		require.NotContains(t, s, coord, "a formatted Cred carries the password: %s", s)
		require.NotContains(t, s, coord[:8], "a formatted Cred carries the password: %s", s)
	}

	mockEnv[seatcred.UserEnv] = "bench"
	c, err = seatcred.Resolve("studio", getenv)
	require.NoError(t, err, "Resolve with %s=bench = %v, %v; want the bench login", seatcred.UserEnv, c, err)
	require.Equal(t, "bench", c.User, "Resolve with %s=bench = %v, %v; want the bench login", seatcred.UserEnv, c, err)
	require.True(t, same(c, bench), "Resolve with %s=bench = %v, %v; want the bench login", seatcred.UserEnv, c, err)
	mockEnv[seatcred.UserEnv] = "ghost"
	_, err = seatcred.Resolve("studio", getenv)
	require.Error(t, err, "Resolve for a user the seat has no password for = %v; want a refusal naming the key and the remedy", err)
	require.Contains(t, err.Error(), "NOVA_REDIS_GHOST_PASSWORD", "Resolve for a user the seat has no password for = %v; want a refusal naming the key and the remedy", err)
	require.Contains(t, err.Error(), "nova-secrets seal", "Resolve for a user the seat has no password for = %v; want a refusal naming the key and the remedy", err)
	mockEnv[seatcred.UserEnv] = ""

	_, err = seatcred.Resolve("nobody", getenv)
	require.Error(t, err, "Resolve of an absent seat = %v; want a refusal naming it", err)
	require.Contains(t, err.Error(), "seat nobody", "Resolve of an absent seat = %v; want a refusal naming it", err)
	_, err = seatcred.Resolve("../x", getenv)
	require.Error(t, err, "Resolve accepted a seat name with a path in it")
	for _, kv := range os.Environ() {
		require.NotContains(t, kv, coord, "a password entered this process's environment: %s", strings.SplitN(kv, "=", 2)[0])
		require.NotContains(t, kv, bench, "a password entered this process's environment: %s", strings.SplitN(kv, "=", 2)[0])
	}
}

func TestActiveResolvesOnceAndOnlyWhenSelected(t *testing.T) {
	t.Parallel()

	home := seattest.Home(t, "air", map[string]string{"NOVA_REDIS_BENCH_PASSWORD": "air-bench-test-pw-11"})
	mockEnv := map[string]string{
		"HOME":           home,
		seatcred.SopsEnv: seattest.Sops(t),
	}
	getenv := func(k string) string { return mockEnv[k] }
	var s seatcred.Selection
	_, ok, _ := s.Active()
	require.False(t, ok, "Active with no seat selected reported one")
	s.SelectWith("air", "", func(seat string) (seatcred.Cred, error) { return seatcred.Resolve(seat, getenv) })
	c, ok, err := s.Active()
	require.True(t, ok, "Active = %v %v %v; want air as bench", c, ok, err)
	require.NoError(t, err, "Active = %v %v %v; want air as bench", c, ok, err)
	require.Equal(t, "bench", c.User, "Active = %v %v %v; want air as bench", c, ok, err)
	require.True(t, same(c, "air-bench-test-pw-11"), "Active = %v %v %v; want air as bench", c, ok, err)
}

func same(c seatcred.Cred, want string) bool {
	eq := false
	_ = c.Password.Use(func(v string) error { eq = v == want; return nil })
	return eq
}
