package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// seat-secrets-for-every-key: member --keys reads its keys in process from its seat's
// store, so its unit carries none in its environment; each launch is handed its route's one
// provider key (and the decide key native keeps from its harness), never another
// provider's; a key the seat cannot give refuses the member at its start.
func TestAMemberReadsItsKeysFromItsSeatAndHandsALaunchItsRoutesKeyAlone(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"OPENROUTER_API_KEY": "orVb61NcYq9Hd4Ws7e",
		"ANTHROPIC_API_KEY":  "anTg28JkMu5Xe3Lq0p",
		decide.JevSecret:     "jevKq83LmZx0Pw2Rt5",
	}
	held := map[string]secrets.Secret{}
	for n, v := range values {
		held[n] = secrets.NewSecret(v)
	}
	var logins []secrets.Login
	read := func(l secrets.Login, names []string) (secrets.Keys, error) {
		return secrets.ReadKeysWith(l, names, func(one secrets.Login) (secrets.Secret, error) {
			logins = append(logins, one)
			if s, ok := held[one.Name]; ok {
				return s, nil
			}
			return secrets.Secret{}, errors.New("seat " + one.As + " of store " + one.Store + " holds no " + one.Name)
		})
	}
	getenv := func(n string) string {
		return map[string]string{"HOME": "/home/m1", "NOVA_SECRETS_SOPS": "/usr/bin/sops"}[n]
	}
	leaked := func(env []string) []string {
		var out []string
		for n, s := range held {
			if secrets.Leaks(strings.Join(env, "\n"), s) {
				out = append(out, n)
			}
		}
		return out
	}

	keys, err := memberKeys("OPENROUTER_API_KEY,ANTHROPIC_API_KEY,JEV_API_KEY", "m1", getenv, read)
	require.NoError(t, err)
	require.Len(t, logins, 3)
	assert.Equal(t, secrets.Login{Store: "/home/m1/nova-bench/secrets", As: "m1", Key: "/home/m1/.config/nova-secrets/m1.key", Sops: "/usr/bin/sops", Name: "OPENROUTER_API_KEY"}, logins[0],
		"the keys are read from the seat's file in the fleet layout, as its Redis login is")

	// a launch on an openrouter route: its route's key and the decide key native keeps
	environ := append(os.Environ(), "PATH=/usr/bin", "HOME=/home/m1")
	env := append(childEnviron(environ, nil), launchKeys(keys, "openrouter/qwen/qwen3-coder")...)
	assert.ElementsMatch(t, []string{"OPENROUTER_API_KEY", decide.JevSecret}, leaked(env))
	// and the harness native starts from it holds the route's key alone
	harness := nativeChildEnvFrom(env, "/d", "/j", "/t", "", "", "", "", nil)
	assert.Equal(t, []string{"OPENROUTER_API_KEY"}, leaked(harness), "the harness is handed its route's one key")
	// another route, another key; a local route none but native's
	assert.ElementsMatch(t, []string{"ANTHROPIC_API_KEY", decide.JevSecret}, leaked(launchKeys(keys, "anthropic/claude")))
	assert.Equal(t, []string{decide.JevSecret}, leaked(launchKeys(keys, "ollama/qwen")))
	assert.Empty(t, leaked(nativeChildEnvFrom(launchKeys(keys, "ollama/qwen"), "/d", "/j", "/t", "", "", "", "", nil)))

	// the member's own attempt decision reads the decide key through the keys, and the
	// process's environment holds none of them
	assert.Equal(t, values[decide.JevSecret], keys.Getenv(os.Getenv)(decide.JevSecret))
	assert.NotNil(t, workAttempt(false, keys.Getenv(os.Getenv)))
	assert.Empty(t, leaked(os.Environ()))

	// with no keys named, nothing is read and a launch is handed nothing
	none, err := memberKeys("", "", getenv, read)
	require.NoError(t, err)
	assert.Empty(t, launchKeys(none, "openrouter/x"))

	// refusals at start: no seat, and a key the seat does not hold
	_, err = memberKeys("OPENROUTER_API_KEY", "", getenv, read)
	require.EqualError(t, err, "--keys OPENROUTER_API_KEY are read from the seat's store, and no seat is named; run: nova-swarm --seat <name> member ... (or set NOVA_SEAT)")
	_, err = memberKeys("OPENROUTER_API_KEY,OPENAI_API_KEY", "m1", getenv, read)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the key OPENAI_API_KEY does not resolve: seat m1 of store /home/m1/nova-bench/secrets holds no OPENAI_API_KEY")
	assert.Contains(t, err.Error(), "run: nova-secrets names --store /home/m1/nova-bench/secrets --as m1")
	assert.Empty(t, leaked([]string{err.Error()}))
}
