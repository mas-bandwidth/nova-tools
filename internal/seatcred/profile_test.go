package seatcred_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
)

func TestProfilePathIsXDGThenHome(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	p, err := seatcred.ProfilePath("nova-sprint", env(map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/h"}))
	require.NoError(t, err, "with XDG_CONFIG_HOME: %q %v", p, err)
	require.Equal(t, "/x/nova-sprint/seats.tsv", p, "with XDG_CONFIG_HOME: %q %v", p, err)
	{
		p, err := seatcred.ProfilePath("nova-sprint", env(map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/h"}))
		require.NoError(t, err, "relative XDG_CONFIG_HOME is ignored per the XDG spec: %q %v", p, err)
		require.Equal(t, "/h/.config/nova-sprint/seats.tsv", p, "relative XDG_CONFIG_HOME is ignored per the XDG spec: %q %v", p, err)
	}
	_, err = seatcred.ProfilePath("nova-sprint", env(nil))
	require.Error(t, err, "no HOME: %v; want a refusal naming seats.tsv", err)
	require.Contains(t, err.Error(), "seats.tsv", "no HOME: %v; want a refusal naming seats.tsv", err)
}

// TestLoadProfileNamesTheFile is #4330's DONE-WHEN clause: a missing seat
// row names the file, and so does every other refusal about it.
func TestLoadProfileNamesTheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "seats.tsv")
	_, err := seatcred.LoadProfile(path, "coordinator", "/h")
	require.ErrorIs(t, err, seatcred.ErrNoProfileRow, "missing file: %v; want ErrNoProfileRow naming %s and the columns", err, path)
	require.Contains(t, err.Error(), path, "missing file: %v; want ErrNoProfileRow naming %s and the columns", err, path)
	require.Contains(t, err.Error(), seatcred.ProfileColumns, "missing file: %v; want ErrNoProfileRow naming %s and the columns", err, path)
	good := "# fleet play\n\ncoordinator\tredis.invalid:6380\tcoordinator\tNOVA_REDIS_COORDINATOR_PASSWORD\t~/nova-bench/secrets\t~/.config/nova-secrets/studio.key\n" +
		"bench\tredis.invalid:6380\tbench\tNOVA_REDIS_BENCH_PASSWORD\t/s\t/k/air.key\r\n"
	require.NoError(t, os.WriteFile(path, []byte(good), 0o600))
	p, err := seatcred.LoadProfile(path, "coordinator", "/h")
	want := seatcred.Profile{Name: "coordinator", Addr: "redis.invalid:6380", User: "coordinator", SecretEnv: "NOVA_REDIS_COORDINATOR_PASSWORD", Store: "/h/nova-bench/secrets", Key: "/h/.config/nova-secrets/studio.key"}
	require.NoError(t, err, "LoadProfile = %+v %v; want %+v as studio", p, err, want)
	require.Equal(t, want, p, "LoadProfile = %+v %v; want %+v as studio", p, err, want)
	require.Equal(t, "studio", p.AsName(), "LoadProfile = %+v %v; want %+v as studio", p, err, want)
	{
		p, err := seatcred.LoadProfile(path, "bench", "/h")
		require.NoError(t, err, "CRLF row: %+v %v", p, err)
		require.Equal(t, "/k/air.key", p.Key, "CRLF row: %+v %v", p, err)
		require.Equal(t, "air", p.AsName(), "CRLF row: %+v %v", p, err)
	}
	_, err = seatcred.LoadProfile(path, "ghost", "/h")
	require.ErrorIs(t, err, seatcred.ErrNoProfileRow, "missing row: %v; want ErrNoProfileRow naming the seat and %s", err, path)
	require.Contains(t, err.Error(), "seat ghost", "missing row: %v; want ErrNoProfileRow naming the seat and %s", err, path)
	require.Contains(t, err.Error(), path, "missing row: %v; want ErrNoProfileRow naming the seat and %s", err, path)

	for _, c := range []struct{ row, why string }{
		{"coordinator\tredis.invalid:6380\tcoordinator", "3 columns"},
		{"bad/name\th:1\tu\tK\t/s\t/k/a.key", "name"},
		{"c\tnoport\tu\tK\t/s\t/k/a.key", "redis addr"},
		{"c\th:1\t\tK\t/s\t/k/a.key", "redis user"},
		{"c\th:1\tu\tlower\t/s\t/k/a.key", "secret env"},
		{"c\th:1\tu\tK\trel/s\t/k/a.key", "store"},
		{"c\th:1\tu\tK\t/s\t/k/a.pem", "key"},
		{"c\th:1\tu\tK\t/s\t/k/a.key\tlower", "github token env"},
		{"c\th:1\tu\tK\t/s\t/k/a.key\tGH\textra", "8 columns"},
	} {
		require.NoError(t, os.WriteFile(path, []byte("# x\n"+c.row+"\n"), 0o600))
		_, err := seatcred.LoadProfile(path, "anything", "/h")
		require.Error(t, err, "row %q: %v; want a refusal naming %s:2 and %q", c.row, err, path, c.why)
		require.NotErrorIs(t, err, seatcred.ErrNoProfileRow, "row %q: %v; want a refusal naming %s:2 and %q", c.row, err, path, c.why)
		require.Contains(t, err.Error(), path+":2", "row %q: %v; want a refusal naming %s:2 and %q", c.row, err, path, c.why)
		require.Contains(t, err.Error(), c.why, "row %q: %v; want a refusal naming %s:2 and %q", c.row, err, path, c.why)
	}
	dup := "c\th:1\tu\tK\t/s\t/k/a.key\nc\th:2\tu\tK\t/s\t/k/a.key\n"
	require.NoError(t, os.WriteFile(path, []byte(dup), 0o600))
	_, err = seatcred.LoadProfile(path, "c", "/h")
	require.Error(t, err, "duplicate row: %v", err)
	require.Contains(t, err.Error(), path+":2", "duplicate row: %v", err)
	require.Contains(t, err.Error(), "second row", "duplicate row: %v", err)
}

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

func TestSelectWithResolvesThroughTheGivenFunc(t *testing.T) {
	t.Parallel()

	var sel seatcred.Selection
	calls := 0
	sel.SelectWith("coordinator", "h:1", func(s string) (seatcred.Cred, error) {
		calls++
		return seatcred.Cred{Seat: s, User: "u"}, nil
	})
	for i := 0; i < 2; i++ {
		c, ok, err := sel.Active()
		require.True(t, ok, "Active = %v %v %v", c, ok, err)
		require.NoError(t, err, "Active = %v %v %v", c, ok, err)
		require.Equal(t, "u", c.User, "Active = %v %v %v", c, ok, err)
	}
	require.Equal(t, 1, calls, "resolved %d times with addr %q seat %q, want once with h:1 as coordinator", calls, sel.Addr(), sel.Selected())
	require.Equal(t, "h:1", sel.Addr(), "resolved %d times with addr %q seat %q, want once with h:1 as coordinator", calls, sel.Addr(), sel.Selected())
	require.Equal(t, "coordinator", sel.Selected(), "resolved %d times with addr %q seat %q, want once with h:1 as coordinator", calls, sel.Addr(), sel.Selected())
	sel.Select("")
	_, ok, _ := sel.Active()
	require.False(t, ok, "Select(\"\") left a seat or its address selected")
	require.Empty(t, sel.Addr(), "Select(\"\") left a seat or its address selected")
}

// TestLoadProfileSeventhColumnIsTheGitHubTokenEnv is #4330's second gap: a
// row may name the key of the seat's file that holds its GitHub token; a
// six-column row is still a row, with no token env.
func TestLoadProfileSeventhColumnIsTheGitHubTokenEnv(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "seats.tsv")
	body := "coordinator\th:1\tcoordinator\tNOVA_REDIS_COORDINATOR_PASSWORD\t/s\t/k/studio.key\tGH_GATE_TOKEN\n" +
		"bench\th:1\tbench\tNOVA_REDIS_BENCH_PASSWORD\t/s\t/k/air.key\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	p, err := seatcred.LoadProfile(path, "coordinator", "/h")
	require.NoError(t, err, "seven columns: %+v %v; want GitHubEnv GH_GATE_TOKEN", p, err)
	require.Equal(t, "GH_GATE_TOKEN", p.GitHubEnv, "seven columns: %+v %v; want GitHubEnv GH_GATE_TOKEN", p, err)
	{
		p, err := seatcred.LoadProfile(path, "bench", "/h")
		require.NoError(t, err, "six columns: %+v %v; want no GitHubEnv", p, err)
		require.Empty(t, p.GitHubEnv, "six columns: %+v %v; want no GitHubEnv", p, err)
	}
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
