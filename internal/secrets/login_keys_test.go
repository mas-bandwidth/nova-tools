package secrets

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seat-secrets-for-every-key: every secret a sprint unit needs (the server's decide key,
// each provider key a member hands a child) is read in process from the seat its login
// names, by name, never from the environment; a child is handed its route's one key and
// no other; a name the seat cannot give refuses at start, naming it and the remedy.
func TestEveryUnitKeyIsReadInProcessNeverFromTheEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"JEV_API_KEY":        "jevKq83LmZx0Pw2Rt5",
		"OPENROUTER_API_KEY": "orVb61NcYq9Hd4Ws7e",
		"ANTHROPIC_API_KEY":  "anTg28JkMu5Xe3Lq0p",
		"EMPTY_API_KEY":      "",
	}
	held := map[string]Secret{}
	for n, v := range values {
		held[n] = NewSecret(v)
	}
	var opened []string
	open := func(store, as, key, sops string) (SeatFile, error) {
		opened = append(opened, strings.Join([]string{store, as, key, sops}, " "))
		return SeatFile{Path: "/s/studio.yaml", Secrets: held}, nil
	}
	read := func(l Login) (Secret, error) { return readLogin(l, open) }
	l := Login{Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops"}
	leaked := func(text string) []string {
		var out []string
		for n, s := range held {
			if Leaks(text, s) {
				out = append(out, n)
			}
		}
		return out
	}
	environ := strings.Join(os.Environ(), "\n")
	for n := range values {
		require.Empty(t, os.Getenv(n), "the test's own environment holds no %s", n)
	}

	// the setting is a list of names, each a secret's name and none twice
	names, err := KeyNames(" JEV_API_KEY, OPENROUTER_API_KEY ,ANTHROPIC_API_KEY,")
	require.NoError(t, err)
	assert.Equal(t, []string{"JEV_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY"}, names)
	_, err = KeyNames("JEV_API_KEY,jev_api_key")
	require.EqualError(t, err, `the key "jev_api_key" is not a secret's name (A-Z, 0-9, _, a letter first)`)
	_, err = KeyNames("JEV_API_KEY,JEV_API_KEY")
	require.EqualError(t, err, "the key JEV_API_KEY is named twice")

	keys, err := ReadKeysWith(l, names, read)
	require.NoError(t, err)
	assert.Equal(t, []string{"/s studio /k/studio.key /bin/sops", "/s studio /k/studio.key /bin/sops", "/s studio /k/studio.key /bin/sops"}, opened,
		"each key is read through the login's seat, the one path exec takes")
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "JEV_API_KEY", "OPENROUTER_API_KEY"}, keys.Names())

	// the server's decide key: answered in process by the getenv the binary uses, while
	// the process's environment holds nothing
	getenv := keys.Getenv(func(n string) string {
		if n == "HOME" {
			return "/home/rowan"
		}
		return os.Getenv(n)
	})
	assert.Equal(t, values["JEV_API_KEY"], getenv("JEV_API_KEY"))
	assert.Equal(t, "/home/rowan", getenv("HOME"), "every other name is the next getenv's")
	for n := range values {
		assert.Empty(t, os.Getenv(n), "reading %s set nothing in the environment", n)
	}
	assert.Empty(t, leaked(strings.Join(os.Environ(), "\n")), "no value reached this process's environment")
	assert.Equal(t, environ, strings.Join(os.Environ(), "\n"), "the environment is as it was")

	// a member's route key: a child on an openrouter route is handed OPENROUTER_API_KEY
	// and nothing else, never the decide key or another provider's
	assert.Equal(t, "OPENROUTER_API_KEY", ProviderKey("openrouter"))
	assert.Equal(t, "LM_STUDIO_API_KEY", ProviderKey("lm-studio"))
	child := keys.ChildEnv("openrouter")
	require.Len(t, child, 1)
	assert.True(t, strings.HasPrefix(child[0], "OPENROUTER_API_KEY="))
	assert.Equal(t, []string{"OPENROUTER_API_KEY"}, leaked(strings.Join(child, "\n")), "the child holds its route's key alone")
	assert.Len(t, keys.ChildEnv("anthropic"), 1)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, leaked(strings.Join(keys.ChildEnv("anthropic"), "\n")))
	assert.Empty(t, leaked(strings.Join(keys.ChildEnv("ollama"), "\n")), "a route whose key is not named is handed none")
	assert.Len(t, keys.ChildEnv("ollama"), 0)
	assert.Len(t, keys.ChildEnv("openai"), 0)

	// the keys print their names and never a value
	for _, text := range []string{fmt.Sprintf("%v %s %+v %#v", keys, keys, keys, keys), fmt.Sprint(keys)} {
		assert.Empty(t, leaked(text), "printing the keys shows no value: %s", text)
		assert.Contains(t, text, "JEV_API_KEY")
	}

	// a name the seat cannot give refuses, naming it and the next thing to run
	_, err = ReadKeysWith(l, []string{"JEV_API_KEY", "OPENAI_API_KEY"}, read)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the key OPENAI_API_KEY does not resolve: seat studio of store /s holds no OPENAI_API_KEY")
	assert.Contains(t, err.Error(), "run: nova-secrets names --store /s --as studio")
	_, err = ReadKeysWith(l, []string{"EMPTY_API_KEY"}, read)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the key EMPTY_API_KEY does not resolve")
	_, err = ReadKeysWith(l, []string{"JEV_API_KEY"}, func(Login) (Secret, error) { return Secret{}, errors.New("key file /k/studio.key is absent") })
	require.EqualError(t, err, "the key JEV_API_KEY does not resolve: key file /k/studio.key is absent")
	_, err = ReadKeysWith(Login{Store: "/s"}, []string{"JEV_API_KEY"}, read)
	require.EqualError(t, err, "the keys JEV_API_KEY are read through a login that names no --as <seat>, --key <file>, --sops <path>")
	_, err = ReadKeysWith(l, []string{"bad name"}, read)
	require.Error(t, err, "a name that is no secret's name is refused before the seat is opened")
	none, err := ReadKeysWith(Login{}, nil, read)
	require.NoError(t, err, "no names reads nothing and needs no login")
	assert.Empty(t, none.Names())
	assert.Equal(t, "HOME", none.Getenv(func(n string) string { return n })("HOME"))
	assert.Empty(t, none.ChildEnv("openrouter"))
}
