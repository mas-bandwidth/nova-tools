package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seat --wrapper prints the coordinator's seat wrapper for the calling actor, and
// --install writes that file. The text names variables and the server address; a
// secret in the environment is not copied into it.
func TestSeatPrintsAndInstallsTheWrapper(t *testing.T) {
	t.Parallel()
	const (
		server = "127.0.0.1:6390"
		secret = "s3cr3t-value-not-in-wrapper"
	)
	env := map[string]string{
		"NOVA_SPRINT_SERVER":         server,
		"NOVA_SPRINT_ACTOR":          "ada",
		"NOVA_SPRINT_REDIS_PASSWORD": secret,
		"NOVA_REDIS_BENCH_PASSWORD":  secret,
	}
	a := newApp(func(k string) string { return env[k] })
	do := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, errs := do("seat", "--wrapper")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, seatWrapperWant("ada", server), out)
	assert.NotContains(t, out, secret)

	env["NOVA_SPRINT_ACTOR"] = "bo"
	code, out, errs = do("seat", "--wrapper")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, seatWrapperWant("bo", server), out)
	assert.NotContains(t, out, "NOVA_SPRINT_ACTOR='ada'")

	env["NOVA_SPRINT_ACTOR"] = "ada"
	dir := t.TempDir()
	code, _, errs = do("seat", "--wrapper", "--install", dir)
	require.Equal(t, 0, code, errs)
	path := filepath.Join(dir, "ns.sh")
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), st.Mode().Perm())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, seatWrapperWant("ada", server), string(got))
	assert.NotContains(t, string(got), secret)

	other := []byte("not the wrapper\n")
	require.NoError(t, os.WriteFile(path, other, 0o644))
	require.NoError(t, os.Chmod(path, 0o644)) // WriteFile leaves an existing file's mode
	code, _, errs = do("seat", "--wrapper", "--install", dir)
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "REFUSED")
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(other), string(got), "a different file is left untouched")
	st, err = os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), st.Mode().Perm(), "a different file's mode is left untouched")

	code, _, errs = do("seat", "--wrapper", "--install", dir, "--replace")
	require.Equal(t, 0, code, errs)
	st, err = os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), st.Mode().Perm())
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, seatWrapperWant("ada", server), string(got))
	assert.NotContains(t, string(got), secret)
}

// seatWrapperWant is the wrapper text for actor at server: variable names and that
// address, never a secret value.
func seatWrapperWant(actor, server string) string {
	return "#!/bin/sh\n" +
		"# Coordinator seat wrapper. Written by nova-sprint seat --wrapper.\n" +
		"# No secret value is in this file: names of variables, and the server address.\n" +
		"export NOVA_SPRINT_SERVER='" + server + "'\n" +
		"export NOVA_SPRINT_ACTOR='" + actor + "'\n" +
		"exec nova-secrets exec \\\n" +
		"  --store \"${NOVA_SECRETS_STORE}\" \\\n" +
		"  --as \"${NOVA_SECRETS_SEAT}\" \\\n" +
		"  --key \"${NOVA_SECRETS_KEY}\" \\\n" +
		"  --sops \"$(command -v sops)\" \\\n" +
		"  --only \"${NOVA_SPRINT_REDIS_PASSWORD_ENV}\" \\\n" +
		"  --require \"${NOVA_SPRINT_REDIS_PASSWORD_ENV}\" \\\n" +
		"  -- env \\\n" +
		"  NOVA_SPRINT_SERVER=\"${NOVA_SPRINT_SERVER}\" \\\n" +
		"  NOVA_SPRINT_ACTOR=\"${NOVA_SPRINT_ACTOR}\" \\\n" +
		"  NOVA_SPRINT_REDIS=\"${NOVA_SPRINT_REDIS}\" \\\n" +
		"  NOVA_SPRINT_REDIS_USER=\"${NOVA_SPRINT_REDIS_USER}\" \\\n" +
		"  NOVA_SPRINT_REDIS_PASSWORD_ENV=\"${NOVA_SPRINT_REDIS_PASSWORD_ENV}\" \\\n" +
		"  nova-sprint \"$@\"\n"
}
