package secrets

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The server's decision key and a member's route key are read from the seat in
// process. The environment is not a source of either, a child is handed only
// the route's one key, and a name the seat does not hold is refused by name
// even when the environment holds it.
func TestEveryUnitKeyIsReadInProcessNeverFromTheEnvironment(t *testing.T) {
	t.Parallel()
	const (
		decision  = "JEV_API_KEY"
		route     = "ANTHROPIC_API_KEY"
		other     = "XAI_API_KEY"
		missing   = "OPENAI_API_KEY"
		seatDec   = "seat-decision-key-value"
		seatRoute = "seat-route-key-value"
		seatOther = "seat-other-key-value"
		poison    = "env-poison-not-the-seat"
	)
	l := Login{Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Name: "NOVA_REDIS_COORDINATOR_PASSWORD",
		Names: []string{decision, route, other}}
	opened := 0
	open := func(store, as, key, sops string) (SeatFile, error) {
		opened++
		assert.Equal(t, []string{"/s", "studio", "/k/studio.key", "/bin/sops"}, []string{store, as, key, sops})
		return SeatFile{Path: "/s/studio.yaml", Secrets: map[string]Secret{
			decision: NewSecret(seatDec),
			route:    NewSecret(seatRoute),
			other:    NewSecret(seatOther),
			"EMPTY":  NewSecret(""),
		}}, nil
	}
	lookup := func(name string) string {
		switch name {
		case decision, route, other, missing, "EMPTY":
			return poison
		default:
			return ""
		}
	}

	got, err := readUnitKeys(l, open, lookup)
	require.NoError(t, err)
	assert.Equal(t, 1, opened, "the seat is opened once for the whole list")
	require.Contains(t, got, decision)
	require.Contains(t, got, route)
	assertNotEnv(t, got[decision], seatDec, poison)
	assertNotEnv(t, got[route], seatRoute, poison)
	assert.False(t, Leaks(l.String(), got[decision]), "the login line prints no value")
	assert.Contains(t, l.String(), "keys="+decision+","+route+","+other)

	base := []string{"PATH=/bin", "HOME=/h", decision + "=" + poison, route + "=" + poison, other + "=" + poison}
	env, err := OneRouteKey(base, got, route)
	require.NoError(t, err)
	var names []string
	for _, kv := range env {
		n, v, _ := strings.Cut(kv, "=")
		names = append(names, n)
		if n == route {
			if v != seatRoute {
				t.Fatal("the child was not handed the seat's route key")
			}
		} else if strings.Contains(v, poison) || strings.Contains(v, seatDec) || strings.Contains(v, seatOther) {
			t.Fatalf("the child environment carries another unit key under %s", n)
		}
	}
	assert.Equal(t, []string{"PATH", "HOME", route}, names)
	assert.False(t, Leaks(strings.Join(env, "\n"), got[decision]), "the child carries the decision key")
	assert.False(t, Leaks(strings.Join(env, "\n"), got[other]), "the child carries the other route key")

	name, err := RouteKeyName("anthropic", []string{other, route})
	require.NoError(t, err)
	assert.Equal(t, route, name)
	name, err = RouteKeyName("deepseek", []string{"DEEPSEEK_API_KEY"})
	require.NoError(t, err)
	assert.Equal(t, "DEEPSEEK_API_KEY", name, "one named key is the route's key")

	absent := l
	absent.Names = []string{decision, missing}
	_, err = readUnitKeys(absent, open, lookup)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no "+missing)
	assert.Contains(t, err.Error(), "environment is not where a unit reads it")
	assert.Contains(t, err.Error(), "run: nova-secrets names --store /s --as studio")
	assert.NotContains(t, err.Error(), poison)
	assert.NotContains(t, err.Error(), seatDec)

	empty := l
	empty.Names = []string{"EMPTY"}
	_, err = readUnitKeys(empty, open, lookup)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds EMPTY empty")
	assert.Contains(t, err.Error(), "run: nova-secrets seal --store /s --as studio --key /k/studio.key --sops /bin/sops --name EMPTY")

	_, err = readUnitKeys(l, func(string, string, string, string) (SeatFile, error) {
		return SeatFile{}, errors.New("key file /k/studio.key is absent")
	}, lookup)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat studio of store /s did not open")

	_, err = readUnitKeys(Login{Names: []string{decision}}, open, lookup)
	require.Error(t, err)
	assert.Equal(t, "the login names no --store <dir>, --as <seat>, --key <file>, --sops <path>", err.Error())

	_, err = OneRouteKey(base, got, missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the route needs "+missing)
}

// assertNotEnv fails when s is not the seat's value or is the environment's.
// The failure names neither value.
func assertNotEnv(t *testing.T, s Secret, seat, env string) {
	t.Helper()
	got := ""
	require.NoError(t, s.Use(func(v string) error { got = v; return nil }))
	if got == env {
		t.Fatal("the secret was read from the environment")
	}
	if got != seat {
		t.Fatal("the secret read in process is not the seat's value")
	}
}
