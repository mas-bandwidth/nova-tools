package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// applied reports whether dsn is user at host with password pw (pw empty means
// none). The fact names no secret: a failure can be printed.
func applied(dsn, user, host, pw string) (bool, string) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return false, "unparsed"
	}
	gotHost := cfg.Host
	if cfg.Port != 0 {
		gotHost = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	}
	ok := cfg.User == user && gotHost == host && cfg.Database == "nova" && cfg.Password == pw
	return ok, fmt.Sprintf("user=%s host=%s db=%s password_ok=%t", cfg.User, gotHost, cfg.Database, cfg.Password == pw)
}

// config-login-built-in: nova-config <verb> typed bare reaches PostgreSQL with the
// recorded login. The password is read in process from a fake nova-secrets, never
// printed, put in an environment or written to a file. No real database, secrets
// store, Redis or forge is opened.
func TestABareVerbConnectsWithTheRecordedLogin(t *testing.T) {
	t.Parallel()
	const (
		recordedPassword = "Zm4qPw8nLt2vKc6rYb"
		envPassword      = "Hj7sBn3wQd5mXp8c"
		secretName       = "NOVA_PG_CONFIG_PASSWORD"
	)
	dir := t.TempDir()
	h := newHarness()
	h.dir = dir
	h.env["XDG_CONFIG_HOME"] = filepath.Join(dir, "config")
	var dsns []string
	var reads []secrets.Login
	read := func(l secrets.Login) (secrets.Secret, error) {
		reads = append(reads, l)
		if l.Name != secretName {
			return secrets.Secret{}, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, l.Name, l.Store, l.As)
		}
		return secrets.NewSecret(recordedPassword), nil
	}
	getenv := withLogin(func(k string) string { return h.env[k] }, read)
	t.Cleanup(func() {
		if p := getenv(loginFileKey); p != "" {
			loginReaders.Delete(p)
		}
	})
	d := h.deps()
	d.getenv = getenv
	inner := d.openStore
	d.openStore = func(ctx context.Context, got string) (pgStore, error) {
		dsns = append(dsns, got)
		return inner(ctx, got)
	}

	file := filepath.Join(dir, "config", "nova-config", "login.json")
	store := filepath.Join(dir, "secrets")
	key := filepath.Join(dir, "studio.key")
	const sops = "/usr/local/bin/sops"
	var said []string
	do := func(args ...string) (int, string, string) {
		t.Helper()
		code, out, errs := runCapture(args, d)
		said = append(said, out, errs)
		return code, out, errs
	}

	code, out, errs := do("login", "-h")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "--store")
	assert.Contains(t, out, "--secret")
	assert.Contains(t, out, "--dsn")
	assert.Contains(t, out, "--friend")
	code, out, errs = do("logout", "-h")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "removes the recorded login")
	assert.NoFileExists(t, file)
	assert.Empty(t, reads)
	assert.Empty(t, dsns)

	code, _, errs = do("friend", "list")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--pg is required")
	assert.Contains(t, errs, "nova-config login")
	assert.Empty(t, dsns)
	assert.Empty(t, reads)

	loginArgs := []string{"login", "--store", store, "--as", "studio", "--key", key, "--sops", sops,
		"--secret", secretName, "--dsn", dsn, "--friend", "rowan"}
	// --dry-run makes every check the login makes, the secret resolved, and records nothing
	code, out, errs = do(append(loginArgs, "--dry-run")...)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "LOGIN DRY-RUN file="+file)
	assert.Contains(t, out, "resolves=yes dry_run=true")
	assert.NoFileExists(t, file, "a dry run recorded the login")
	assert.Len(t, reads, 1, "a dry run resolves the secret")
	reads = nil
	code, out, errs = do("logout", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "was=none dry_run=true")
	code, out, errs = do(loginArgs...)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "LOGIN RECORDED file="+file)
	assert.Contains(t, out, "friend=rowan")
	assert.Contains(t, out, "resolves=yes")
	assert.Empty(t, dsns, "login records; it opens no store")
	require.NotEmpty(t, reads)
	assert.Equal(t, secrets.Login{Store: store, As: "studio", Key: key, Sops: sops, Name: secretName}, reads[0])

	b, err := os.ReadFile(file)
	require.NoError(t, err)
	var rec loginRecord
	require.NoError(t, json.Unmarshal(b, &rec))
	assert.Equal(t, loginRecord{DSN: dsn, Friend: "rowan", Store: store, As: "studio", Key: key, Sops: sops, Secret: secretName}, rec)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	for k := range raw {
		assert.NotContains(t, strings.ToLower(k), "password", "the record has a password field %q", k)
	}
	fi, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	code, out, errs = do("friend", "list")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG LIST kind=friend")
	ok, fact := applied(dsns[len(dsns)-1], "nova_config", "127.0.0.1:5432", recordedPassword)
	require.True(t, ok, "bare list: %s", fact)

	code, _, errs = do("friend", "add", "f1", "--slots", "4", "--tiers", "flash", "--roles", "builder")
	require.Equal(t, 0, code, errs)
	ok, fact = applied(dsns[len(dsns)-1], "nova_config", "127.0.0.1:5432", recordedPassword)
	require.True(t, ok, "bare add: %s", fact)
	code, out, errs = do("friend", "history", "f1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "actor=rowan")

	code, out, errs = do("login", "--check")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "LOGIN file="+file)
	assert.Contains(t, out, "dsn="+dsn)
	assert.Contains(t, out, "secret="+secretName+" resolves=yes")
	assert.NotContains(t, out, "dsn-wins=")
	assert.NotContains(t, out, "password-wins=")

	n := len(reads)
	code, _, errs = do("friend", "list", "--pg", "postgres://other@127.0.0.1:5432/nova")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, n, len(reads), "--pg reads no recorded secret")
	ok, fact = applied(dsns[len(dsns)-1], "other", "127.0.0.1:5432", "")
	require.True(t, ok, "--pg wins: %s", fact)

	h.env["NOVA_PG_DSN"] = "postgres://other@127.0.0.1:5432/nova"
	n = len(reads)
	code, _, errs = do("friend", "list")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, n, len(reads), "NOVA_PG_DSN reads no recorded secret")
	ok, fact = applied(dsns[len(dsns)-1], "other", "127.0.0.1:5432", "")
	require.True(t, ok, "NOVA_PG_DSN wins: %s", fact)
	delete(h.env, "NOVA_PG_DSN")

	h.env["NOVA_PG_PASSWORD_ENV"] = "NOVA_SECRET_PG"
	h.env["NOVA_SECRET_PG"] = envPassword
	n = len(reads)
	code, _, errs = do("friend", "list")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, n, len(reads), "NOVA_PG_PASSWORD_ENV reads no recorded secret")
	ok, fact = applied(dsns[len(dsns)-1], "nova_config", "127.0.0.1:5432", envPassword)
	require.True(t, ok, "NOVA_PG_PASSWORD_ENV wins: %s", fact)
	okRec, _ := applied(dsns[len(dsns)-1], "nova_config", "127.0.0.1:5432", recordedPassword)
	assert.False(t, okRec, "the recorded password was applied when the env prefix was set")
	delete(h.env, "NOVA_PG_PASSWORD_ENV")
	delete(h.env, "NOVA_SECRET_PG")

	n = len(reads)
	code, _, errs = do("migrate", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	code, _, errs = do("friend", "list", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, n, len(reads), "--file reads no recorded secret")
	assert.Equal(t, "file:try.json", dsns[len(dsns)-1])

	h.env["NOVA_FRIEND"] = "stella"
	h.env["NOVA_PG_DSN"] = "postgres://other@10.1.1.1:5432/nova"
	h.env["NOVA_PG_PASSWORD_ENV"] = "NOVA_SECRET_PG"
	code, out, errs = do("login", "--check")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "dsn-wins=env:")
	assert.Contains(t, out, "password-wins=env:NOVA_SECRET_PG")
	assert.Contains(t, out, "friend-wins=env:stella")
	assert.Contains(t, out, "resolves=yes")
	delete(h.env, "NOVA_PG_DSN")
	delete(h.env, "NOVA_PG_PASSWORD_ENV")
	code, _, errs = do("friend", "set", "f1", "--slots", "5")
	require.Equal(t, 0, code, errs)
	code, out, errs = do("friend", "history", "f1")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "actor=stella")
	delete(h.env, "NOVA_FRIEND")

	rec.Secret = "NOVA_PG_GONE"
	b, err = json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, append(b, '\n'), 0o600))
	nDial := len(dsns)
	code, _, errs = do("friend", "list")
	said = append(said, errs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "the login "+file)
	assert.Contains(t, errs, "NOVA_PG_GONE")
	assert.Contains(t, errs, "does not resolve")
	assert.Contains(t, errs, "run: nova-config login --check")
	assert.Len(t, dsns, nDial, "no store is opened without its password")
	code, out, errs = do("login", "--check")
	said = append(said, out, errs)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "resolves=no")
	assert.Contains(t, errs, "nova-secrets names")
	gone := append([]string{}, loginArgs...)
	for i, a := range gone {
		if a == secretName {
			gone[i] = "NOVA_PG_GONE"
		}
	}
	code, _, errs = do(gone...)
	said = append(said, errs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nothing was recorded")

	require.NoError(t, os.WriteFile(file, []byte("{"), 0o600))
	code, _, errs = do("friend", "list")
	said = append(said, errs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "is not a login")
	assert.Contains(t, errs, "run: nova-config logout")

	code, out, errs = do("logout", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "was=recorded dry_run=true")
	assert.FileExists(t, file, "a dry run removed the login")
	code, out, errs = do("logout")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "was=recorded")
	assert.NoFileExists(t, file)
	code, out, errs = do("logout")
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, out, "was=none")
	nDial = len(dsns)
	code, _, _ = do("friend", "list")
	assert.Equal(t, 2, code)
	assert.Len(t, dsns, nDial)

	pw := secrets.NewSecret(recordedPassword)
	envPw := secrets.NewSecret(envPassword)
	for _, s := range said {
		assert.False(t, secrets.Leaks(s, pw), "output leaks the recorded password")
		assert.False(t, secrets.Leaks(s, envPw), "output leaks the env password")
	}
	assert.False(t, secrets.Leaks(strings.Join(os.Environ(), "\n"), pw), "the process environment holds the recorded password")
	assert.False(t, secrets.Leaks(strings.Join(os.Environ(), "\n"), envPw), "the process environment holds the env password")
	require.NoError(t, filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		assert.False(t, secrets.Leaks(string(b), pw), "%s holds the recorded password", p)
		assert.False(t, secrets.Leaks(string(b), envPw), "%s holds the env password", p)
		return nil
	}))
}

// TestLoginToolHasNoProblems ensures the login tool meets the standard.
func TestLoginToolHasNoProblems(t *testing.T) {
	t.Parallel()
	assert.Empty(t, loginTool(deps{}).Problems())
}

// runCapture runs the tool and returns its streams. It is not h.run: the login
// test hands its own deps, with the recorded login on.
func runCapture(args []string, d deps) (int, string, string) {
	var out, errb strings.Builder
	code := run(args, &out, &errb, d)
	return code, out.String(), errb.String()
}
