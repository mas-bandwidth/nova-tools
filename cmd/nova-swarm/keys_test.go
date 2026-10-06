package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// --pass names are read from the seat login in process. A child for one route
// is handed that route's key and not the other, and a missing name refuses
// naming it. Nothing here is a live store.
func TestMemberRouteKeyIsReadInProcessAndTheChildGetsOnlyThatOne(t *testing.T) {
	t.Parallel()
	const (
		route  = "seat-anthropic-route-key"
		other  = "seat-xai-other-key"
		jev    = "seat-jev-decision-key"
		poison = "env-poison-not-the-seat"
	)
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "nova-sprint")
	require.NoError(t, os.MkdirAll(cfg, 0o700))
	rec := []byte("{\"store\":\"/s\",\"as\":\"studio\",\"key\":\"/k\",\"sops\":\"/bin/sops\",\"secret\":\"PW\",\"redis\":\"127.0.0.1:6380\",\"user\":\"coordinator\"}\n")
	require.NoError(t, os.WriteFile(filepath.Join(cfg, "login.json"), rec, 0o600))
	read := func(l secrets.Login) (map[string]secrets.Secret, error) {
		out := map[string]secrets.Secret{}
		for _, n := range l.Names {
			switch n {
			case "ANTHROPIC_API_KEY":
				out[n] = secrets.NewSecret(route)
			case "XAI_API_KEY":
				out[n] = secrets.NewSecret(other)
			case decide.JevSecret:
				out[n] = secrets.NewSecret(jev)
			default:
				return nil, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, n, l.Store, l.As)
			}
		}
		return out, nil
	}
	getenv := func(string) string { return "" }
	homeOf := func() (string, error) { return home, nil }
	keys, err := loadMemberKeys([]string{"ANTHROPIC_API_KEY", "XAI_API_KEY"}, getenv, homeOf, read)
	require.NoError(t, err)
	require.Contains(t, keys, "ANTHROPIC_API_KEY")
	require.Contains(t, keys, decide.JevSecret)

	rn := &nativeRunner{
		keys:     keys,
		keyNames: []string{"ANTHROPIC_API_KEY", "XAI_API_KEY"},
		env:      []string{"ANTHROPIC_API_KEY=" + poison, "XAI_API_KEY=" + poison, "PATH=/bin", "HOME=/h"},
	}
	env, err := rn.childEnv("anthropic/claude-x")
	require.NoError(t, err)
	var names []string
	for _, kv := range env {
		n, v, _ := strings.Cut(kv, "=")
		names = append(names, n)
		switch n {
		case "ANTHROPIC_API_KEY":
			assert.True(t, v == route, "the child was not handed the seat's route key")
		case decide.JevSecret:
			assert.True(t, v == jev, "native was not handed the seat's decision key")
		default:
			assert.False(t, strings.Contains(v, poison), "the child environment carries the environment's value under "+n)
			assert.False(t, strings.Contains(v, other), "the child environment carries the other route key under "+n)
		}
	}
	assert.NotContains(t, names, "XAI_API_KEY")
	assert.Contains(t, names, "ANTHROPIC_API_KEY")
	assert.False(t, secrets.Leaks(strings.Join(env, "\n"), keys["XAI_API_KEY"]), "the child carries the other route key")

	_, err = loadMemberKeys([]string{"OPENAI_API_KEY"}, getenv, homeOf, read)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no OPENAI_API_KEY")
	assert.NotContains(t, err.Error(), route)

	_, err = loadMemberKeys([]string{"ANTHROPIC_API_KEY"}, getenv, func() (string, error) { return t.TempDir(), nil }, read)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no seat login is recorded")
	assert.Contains(t, err.Error(), "nova-sprint seat login")

	none, err := loadMemberKeys(nil, getenv, homeOf, read)
	require.NoError(t, err)
	assert.Nil(t, none)
}
