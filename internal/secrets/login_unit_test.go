package secrets

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sprint server's decision key and a member's route key are read in this
// process from a fake seat. Neither value enters the process environment, a
// child is handed only the route's key, and a missing name is a refusal that
// names it. No value is printed.
func TestEveryUnitKeyIsReadInProcessNeverFromTheEnvironment(t *testing.T) {
	t.Parallel()
	const decision = "fixture-decision-not-a-real-key-0001"
	const route = "fixture-route-not-a-real-key-0002"
	before := map[string]string{}
	for _, n := range []string{DecisionKey, "ANTHROPIC_API_KEY", "XAI_API_KEY"} {
		before[n] = os.Getenv(n)
	}
	u := UnitKeyLogin{Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Names: []string{DecisionKey, "ANTHROPIC_API_KEY", "XAI_API_KEY"}}
	opens := 0
	open := func(store, as, key, sops string) (SeatFile, error) {
		opens++
		assert.Equal(t, []string{"/s", "studio", "/k/studio.key", "/bin/sops"}, []string{store, as, key, sops})
		return SeatFile{Path: "/s/studio.yaml", Secrets: map[string]Secret{
			DecisionKey:         NewSecret(decision),
			"ANTHROPIC_API_KEY": NewSecret(route),
			"XAI_API_KEY":       NewSecret("fixture-other-not-a-real-key-0003"),
		}}, nil
	}
	held, err := readUnitKeys(u, open)
	require.NoError(t, err)
	assert.Equal(t, 1, opens, "the seat is opened once, in this process")
	require.Len(t, held, 3)
	for n, was := range before {
		assert.Equal(t, true, os.Getenv(n) == was, "reading %s changed the process environment", n)
	}
	for _, s := range held {
		require.False(t, envCarries(s), "a secret value is in the process environment")
		require.False(t, Leaks(u.String(), s), "the login prints a secret")
		require.False(t, Leaks(s.String(), s), "a Secret prints itself")
	}

	assert.Equal(t, "ANTHROPIC_API_KEY", RouteKey("anthropic/claude-x", u.Names))
	child, err := ChildWithOneKey([]string{"PATH=/bin", "HOME=/h", DecisionKey + "=from-the-unit", "XAI_API_KEY=from-the-unit"}, held, RouteKey("anthropic/claude-x", u.Names))
	require.NoError(t, err)
	got := map[string]string{}
	for _, kv := range child {
		n, v, _ := strings.Cut(kv, "=")
		_, dup := got[n]
		assert.False(t, dup, "the child environment names %s twice", n)
		got[n] = v
	}
	assert.NotContains(t, got, DecisionKey)
	assert.NotContains(t, got, "XAI_API_KEY")
	assert.Equal(t, "/bin", got["PATH"])
	matched := false
	require.NoError(t, held["ANTHROPIC_API_KEY"].Use(func(v string) error {
		matched = got["ANTHROPIC_API_KEY"] == v && v != ""
		return nil
	}))
	assert.True(t, matched, "the child was not handed the route key the store holds")
	require.False(t, Leaks(strings.Join(childNames(child), ","), held["ANTHROPIC_API_KEY"]), "a child environment's names carry the value")

	missing := u
	missing.Names = []string{DecisionKey, "MISSING_KEY"}
	_, err = readUnitKeys(missing, open)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no MISSING_KEY")
	assert.Contains(t, err.Error(), "run: nova-secrets names --store /s --as studio")
	for _, s := range held {
		assert.False(t, Leaks(err.Error(), s), "a refusal prints a secret")
	}

	_, err = ChildWithOneKey(nil, held, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "handed no key")
}

// envCarries reports whether any process environment entry contains s, without
// printing the value.
func envCarries(s Secret) bool {
	hit := false
	_ = s.Use(func(v string) error {
		if v == "" {
			return nil
		}
		for _, kv := range os.Environ() {
			if strings.Contains(kv, v) {
				hit = true
			}
		}
		return nil
	})
	return hit
}

func childNames(env []string) []string {
	out := make([]string, len(env))
	for i, kv := range env {
		out[i], _, _ = strings.Cut(kv, "=")
	}
	return out
}
