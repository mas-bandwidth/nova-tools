package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// seat-secrets-for-every-key: run --keys reads the decide lane's key in process from the
// store the seat login names, so the loop's unit carries no key in its environment; a key
// the seat cannot give refuses the run at its start, naming the key and the remedy.
func TestRunReadsItsKeysThroughTheSeatLoginAndRefusesOneThatDoesNotResolve(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	la := newLoginApp(t, dir)
	const jev = "jevKq83LmZx0Pw2Rt5"
	held := map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": loginPW, decide.JevSecret: jev}
	la.a.loginSecret = func(l secrets.Login) (secrets.Secret, error) {
		la.reads = append(la.reads, l)
		v, ok := held[l.Name]
		if !ok {
			return secrets.Secret{}, errors.New("seat " + l.As + " of store " + l.Store + " holds no " + l.Name)
		}
		return secrets.NewSecret(v), nil
	}
	var said []string
	do := func(line string) (int, string, string) {
		code, out, errs := la.do(line)
		said = append(said, out, errs)
		return code, out, errs
	}

	// no login recorded: the keys have nowhere to be read from, and the run says how to give one
	code, _, errs := do("run --keys JEV_API_KEY --redis 127.0.0.1:6380")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--keys JEV_API_KEY are read from the store the seat login names, and no seat login is recorded; run: nova-sprint seat login")

	code, _, errs = do("seat login --store " + filepath.Join(dir, "secrets") + " --as studio --key " + filepath.Join(dir, "studio.key") +
		" --sops /usr/local/bin/sops --secret NOVA_REDIS_COORDINATOR_PASSWORD --user coordinator --redis 127.0.0.1:6380")
	require.Equal(t, 0, code, errs)
	code, _, errs = do("init --readers reader-a --members m1")
	require.Equal(t, 0, code, errs)

	// a key the seat does not hold refuses the run before its loop starts
	code, out, errs := do("run --keys JEV_API_KEY,OPENROUTER_API_KEY --decide " + filepath.Join(dir, "decide"))
	assert.Equal(t, 2, code)
	assert.NotContains(t, out, "RUN ticking", "the loop never started")
	assert.Contains(t, errs, "the key OPENROUTER_API_KEY does not resolve: seat studio of store "+filepath.Join(dir, "secrets")+" holds no OPENROUTER_API_KEY")
	assert.Contains(t, errs, "run: nova-secrets names --store "+filepath.Join(dir, "secrets")+" --as studio")
	code, _, errs = do("run --keys jev")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, `--keys: the key "jev" is not a secret's name`)

	// the key named is read through the seat login's seat, in process, and answered by the
	// getenv the decide lane reads JEV_API_KEY through; the environment never holds it
	require.Empty(t, la.env[decide.JevSecret])
	require.Empty(t, os.Getenv(decide.JevSecret))
	require.NoError(t, la.a.keysOn("JEV_API_KEY"))
	last := la.reads[len(la.reads)-1]
	assert.Equal(t, secrets.Login{Store: filepath.Join(dir, "secrets"), As: "studio", Key: filepath.Join(dir, "studio.key"), Sops: "/usr/local/bin/sops", Name: decide.JevSecret}, last)
	assert.Equal(t, jev, la.a.getenv(decide.JevSecret), "the decide lane's key, read in process")
	assert.Empty(t, os.Getenv(decide.JevSecret), "and in no environment")
	assert.Empty(t, la.env[decide.JevSecret])
	assert.Equal(t, "coordinator", la.a.getenv("NOVA_SPRINT_ACTOR"), "every other name is the environment's")
	require.NoError(t, la.a.keysOn(""), "no keys read nothing")

	for _, s := range said {
		assert.False(t, secrets.Leaks(s, secrets.NewSecret(jev)), "no output shows the key")
		assert.False(t, secrets.Leaks(s, secrets.NewSecret(loginPW)), "no output shows the password")
	}
	assert.NotContains(t, strings.Join(os.Environ(), "\n"), jev)
}
