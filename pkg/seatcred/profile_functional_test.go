//go:build functional

package seatcred_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred/seattest"
)

// TestResolveProfileReadsTheRowsSecret opens a real store through the row:
// the row's user, with the password held under the row's secret env, from the
// seat file its key names -- nothing from the default layout.
func TestResolveProfileReadsTheRowsSecret(t *testing.T) {
	t.Parallel()

	const pw = "profile-test-pw-4330"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw, "NOVA_REDIS_BENCH_PASSWORD": "not-this-one"})
	// Nothing from the environment: the row names the store and key, and
	// sops is the one Home sealed with.
	noenv := sopsOnly(t)
	p := seatcred.Profile{
		Name: "coordinator", Addr: "h:1", User: "coordinator", SecretEnv: "NOVA_REDIS_COORDINATOR_PASSWORD",
		Store: filepath.Join(home, seatcred.DefaultStore), Key: filepath.Join(home, seatcred.DefaultKeyDir, "studio.key"),
	}
	c, err := seatcred.ResolveProfile(p, noenv)
	require.NoError(t, err, "ResolveProfile = %v %v; want coordinator with the coordinator password", c, err)
	require.Equal(t, "coordinator", c.Seat, "ResolveProfile = %v %v; want coordinator with the coordinator password", c, err)
	require.Equal(t, "coordinator", c.User, "ResolveProfile = %v %v; want coordinator with the coordinator password", c, err)
	require.Equal(t, p.SecretEnv, c.Key, "ResolveProfile = %v %v; want coordinator with the coordinator password", c, err)
	require.True(t, same(c, pw), "ResolveProfile = %v %v; want coordinator with the coordinator password", c, err)
	p.SecretEnv = "NOVA_REDIS_GHOST_PASSWORD"
	_, err = seatcred.ResolveProfile(p, noenv)
	require.Error(t, err, "absent secret env: %v; want a refusal naming the key and the seal remedy", err)
	require.Contains(t, err.Error(), "NOVA_REDIS_GHOST_PASSWORD", "absent secret env: %v; want a refusal naming the key and the seal remedy", err)
	require.Contains(t, err.Error(), "--as studio", "absent secret env: %v; want a refusal naming the key and the seal remedy", err)
	require.NotContains(t, err.Error(), pw, "absent secret env: %v; want a refusal naming the key and the seal remedy", err)
	p.Key = filepath.Join(home, "nowhere.key")
	_, err = seatcred.ResolveProfile(p, noenv)
	require.Error(t, err, "key for a seat the store lacks: %v", err)
	require.Contains(t, err.Error(), "seat coordinator", "key for a seat the store lacks: %v", err)
}

// TestResolveProfileReadsTheGitHubToken: the row's seventh column names the
// key the token is read from, in the same open of the seat's file; a file
// without it is a refusal kept for the GitHub verb that asks, not for the
// Redis login.
func TestResolveProfileReadsTheGitHubToken(t *testing.T) {
	t.Parallel()

	const pw, tok = "profile-gh-test-pw-4330", "profile-gh-test-token-4330"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw, "GH_GATE_TOKEN": tok})
	noenv := sopsOnly(t)
	p := seatcred.Profile{
		Name: "coordinator", Addr: "h:1", User: "coordinator", SecretEnv: "NOVA_REDIS_COORDINATOR_PASSWORD",
		Store: filepath.Join(home, seatcred.DefaultStore), Key: filepath.Join(home, seatcred.DefaultKeyDir, "studio.key"),
		GitHubEnv: "GH_GATE_TOKEN",
	}
	c, err := seatcred.ResolveProfile(p, noenv)
	require.NoError(t, err, "ResolveProfile = %v %v %v", c, err, c.GitHubErr)
	require.NoError(t, c.GitHubErr, "ResolveProfile = %v %v %v", c, err, c.GitHubErr)
	require.Equal(t, "GH_GATE_TOKEN", c.GitHubKey, "ResolveProfile = %v %v %v", c, err, c.GitHubErr)
	got := ""
	err = c.GitHub.Use(func(v string) error { got = v; return nil })
	require.NoError(t, err, "the GitHub token is not the one sealed under GH_GATE_TOKEN")
	require.Equal(t, tok, got, "the GitHub token is not the one sealed under GH_GATE_TOKEN")
	p.GitHubEnv = "GH_GHOST_TOKEN"
	c, err = seatcred.ResolveProfile(p, noenv)
	require.NoError(t, err, "absent token env: login err %v, GitHubErr %v; want the login and a GitHub refusal naming the key and the seal remedy", err, c.GitHubErr)
	require.Error(t, c.GitHubErr, "absent token env: login err %v, GitHubErr %v; want the login and a GitHub refusal naming the key and the seal remedy", err, c.GitHubErr)
	require.Contains(t, c.GitHubErr.Error(), "GH_GHOST_TOKEN", "absent token env: login err %v, GitHubErr %v; want the login and a GitHub refusal naming the key and the seal remedy", err, c.GitHubErr)
	require.Contains(t, c.GitHubErr.Error(), "--as studio", "absent token env: login err %v, GitHubErr %v; want the login and a GitHub refusal naming the key and the seal remedy", err, c.GitHubErr)
	require.NotContains(t, c.GitHubErr.Error(), tok, "absent token env: login err %v, GitHubErr %v; want the login and a GitHub refusal naming the key and the seal remedy", err, c.GitHubErr)
}

// sopsOnly is an environment holding nothing but seatcred.SopsEnv, answered
// with the sops seattest.Home sealed with, so a parallel test reads its fixture
// on a runner whose PATH names no Homebrew without touching the process's
// environment.
func sopsOnly(t *testing.T) func(string) string {
	t.Helper()
	sops := seattest.Sops(t)
	return func(k string) string {
		if k == seatcred.SopsEnv {
			return sops
		}
		return ""
	}
}
