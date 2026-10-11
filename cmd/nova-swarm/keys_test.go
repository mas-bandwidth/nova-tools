package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// A child is handed the decision key and the one route key read in this process,
// never the other provider key, and the process environment is unchanged.
func TestAChildIsHandedOnlyTheRouteKeyReadInProcess(t *testing.T) {
	t.Parallel()
	const decision = "fixture-decision-not-a-real-key-0001"
	const route = "fixture-route-not-a-real-key-0002"
	const other = "fixture-other-not-a-real-key-0003"
	before := map[string]string{}
	for _, n := range []string{decide.JevSecret, "ANTHROPIC_API_KEY", "XAI_API_KEY"} {
		before[n] = os.Getenv(n)
	}
	r := &nativeRunner{
		pass: []string{decide.JevSecret, "ANTHROPIC_API_KEY", "XAI_API_KEY"},
		held: map[string]secrets.Secret{
			decide.JevSecret:    secrets.NewSecret(decision),
			"ANTHROPIC_API_KEY": secrets.NewSecret(route),
			"XAI_API_KEY":       secrets.NewSecret(other),
		},
		env: []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_API_KEY=from-the-unit", "XAI_API_KEY=from-the-unit", decide.JevSecret + "=from-the-unit"},
	}
	got := r.childEnv("anthropic/claude-x")
	counts := map[string]int{}
	vals := map[string]string{}
	var names []string
	for _, kv := range got {
		n, v, _ := strings.Cut(kv, "=")
		counts[n]++
		vals[n] = v
		names = append(names, n)
	}
	assert.Equal(t, 1, counts["ANTHROPIC_API_KEY"])
	assert.Equal(t, 1, counts[decide.JevSecret])
	assert.Zero(t, counts["XAI_API_KEY"])
	assert.True(t, secretEquals(r.held["ANTHROPIC_API_KEY"], vals["ANTHROPIC_API_KEY"]), "the child was not handed the route key")
	assert.True(t, secretEquals(r.held[decide.JevSecret], vals[decide.JevSecret]), "native was not handed the decision key")
	assert.True(t, secretEquals(r.held[decide.JevSecret], r.getenv(decide.JevSecret)), "the member did not read the decision key in process")
	for _, s := range r.held {
		assert.False(t, secrets.Leaks(strings.Join(names, ","), s), "a child environment's names carry a value")
	}
	for n, was := range before {
		assert.Equal(t, true, os.Getenv(n) == was, "the process environment changed for %s", n)
	}
}

// One name is the route's key even when the model names another provider, so a
// single --pass name is still handed. Two names and neither the route's are not.
func TestOnePassNameIsHandedAndTwoUnmatchedAreNot(t *testing.T) {
	t.Parallel()
	one := &nativeRunner{pass: []string{"PROBE_API_KEY"}, env: []string{"PROBE_API_KEY=passed", "FOO=bar", "PATH=/bin"}}
	got := one.childEnv("fake/claude-x")
	hasProbe, hasFoo := false, false
	for _, kv := range got {
		n, v, _ := strings.Cut(kv, "=")
		if n == "PROBE_API_KEY" && v == "passed" {
			hasProbe = true
		}
		if n == "FOO" {
			hasFoo = true
		}
	}
	assert.True(t, hasProbe, "the one named key was not handed")
	assert.False(t, hasFoo, "a name outside the allowlist was handed")

	two := &nativeRunner{pass: []string{"ANTHROPIC_API_KEY", "XAI_API_KEY"}, env: []string{"ANTHROPIC_API_KEY=a", "XAI_API_KEY=b", "PATH=/bin"}}
	for _, kv := range two.childEnv("fake/claude-x") {
		n, _, _ := strings.Cut(kv, "=")
		assert.NotEqual(t, "ANTHROPIC_API_KEY", n)
		assert.NotEqual(t, "XAI_API_KEY", n)
	}
}

// A name absent from the environment is read in process from the seat. A name
// the environment already holds opens no seat. No seat at all refuses naming
// the missing name. No value is printed.
func TestAMissingPassNameIsReadInProcessFromTheSeat(t *testing.T) {
	t.Parallel()
	const route = "fixture-route-not-a-real-key-0002"
	read := func(u secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		assert.Equal(t, []string{"/s", "studio", "/k/studio.key", "/bin/sops"}, []string{u.Store, u.As, u.Key, u.Sops})
		assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, u.Names)
		return map[string]secrets.Secret{"ANTHROPIC_API_KEY": secrets.NewSecret(route)}, nil
	}
	getenv := func(k string) string {
		switch k {
		case seatcred.SeatEnv:
			return "studio"
		case seatcred.StoreEnv:
			return "/s"
		case seatcred.KeyEnv:
			return "/k/studio.key"
		case seatcred.SopsEnv:
			return "/bin/sops"
		case "XAI_API_KEY":
			return "already-in-the-environment"
		default:
			return ""
		}
	}
	held, extra, err := prepareMemberKeysFrom([]string{"ANTHROPIC_API_KEY", "XAI_API_KEY"}, getenv, read)
	require.NoError(t, err)
	assert.Empty(t, extra)
	require.Len(t, held, 1)
	assert.True(t, secretEquals(held["ANTHROPIC_API_KEY"], route))
	for _, s := range held {
		assert.False(t, secrets.Leaks(s.String(), s))
	}

	called := false
	held, _, err = prepareMemberKeysFrom([]string{"ANTHROPIC_API_KEY"}, func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "present"
		}
		return ""
	}, func(secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		called = true
		return nil, nil
	})
	require.NoError(t, err)
	assert.Nil(t, held)
	assert.False(t, called, "a name the environment holds opened the seat")

	_, _, err = prepareMemberKeysFrom([]string{"ANTHROPIC_API_KEY"}, func(string) string { return "" }, func(secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		t.Fatal("no seat opened the store")
		return nil, nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY")
	assert.Contains(t, err.Error(), "NOVA_SEAT")
	assert.Contains(t, err.Error(), "; run: ")
	assert.False(t, secrets.Leaks(err.Error(), secrets.NewSecret(route)))
}

// NOVA_SWARM_KEYS names the seat and extra names. A value field is refused and not quoted.
func TestSwarmKeysFileNamesTheSeatAndRefusesAValue(t *testing.T) {
	t.Parallel()
	const route = "fixture-route-not-a-real-key-0002"
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	require.NoError(t, os.WriteFile(path, []byte("{\"store\":\"/s\",\"as\":\"studio\",\"key\":\"/k/studio.key\",\"sops\":\"/bin/sops\",\"names\":[\"ANTHROPIC_API_KEY\"]}\n"), 0o600))
	held, extra, err := prepareMemberKeysFrom(nil, func(k string) string {
		if k == swarmKeysEnv {
			return path
		}
		return ""
	}, func(u secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		assert.Equal(t, "studio", u.As)
		assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, u.Names)
		return map[string]secrets.Secret{"ANTHROPIC_API_KEY": secrets.NewSecret(route)}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, extra)
	assert.True(t, secretEquals(held["ANTHROPIC_API_KEY"], route))

	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{\"store\":\"/s\",\"value\":\"not-printed\"}\n"), 0o600))
	_, _, err = prepareMemberKeysFrom(nil, func(k string) string {
		if k == swarmKeysEnv {
			return bad
		}
		return ""
	}, func(secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		t.Fatal("a value field opened the store")
		return nil, nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "value")
	assert.NotContains(t, err.Error(), "not-printed")
}

func secretEquals(s secrets.Secret, got string) bool {
	ok := false
	_ = s.Use(func(v string) error {
		ok = got == v && v != ""
		return nil
	})
	return ok
}
