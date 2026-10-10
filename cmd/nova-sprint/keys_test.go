package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// The decision key's name is the decide loop's name. secrets does not import decide.
func TestTheDecisionKeyNameIsTheDecideLoops(t *testing.T) {
	t.Parallel()
	assert.Equal(t, decide.JevSecret, secrets.DecisionKey)
}

// run --keys reads the named secrets in this process from the seat login. The
// process environment is unchanged, a missing seat login refuses naming the
// names, and the recorded file holds names only. No value is printed.
func TestRunReadsNamedKeysInProcessNeverFromTheEnvironment(t *testing.T) {
	t.Parallel()
	const decision = "fixture-decision-not-a-real-key-0001"
	const route = "fixture-route-not-a-real-key-0002"
	before := map[string]string{}
	for _, n := range []string{decide.JevSecret, "OPENROUTER_API_KEY"} {
		before[n] = os.Getenv(n)
	}

	bare := newApp(func(string) string { return "" })
	err := bare.holdNamedUnitKeys(decide.JevSecret)
	require.Error(t, err)
	assert.Contains(t, err.Error(), decide.JevSecret)
	assert.Contains(t, err.Error(), "seat login")
	assert.Contains(t, err.Error(), "; run: ")
	assert.False(t, secrets.Leaks(err.Error(), secrets.NewSecret(decision)))

	dir := t.TempDir()
	env := map[string]string{"XDG_CONFIG_HOME": dir, decide.JevSecret: "env-copy-not-the-seat"}
	a := newApp(func(k string) string { return env[k] })
	a.seatLoginOn()
	login := storeLogin{Redis: "mem:0", User: "coordinator", Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Secret: "NOVA_REDIS_COORDINATOR_PASSWORD"}
	b, err := json.Marshal(login)
	require.NoError(t, err)
	path, err := a.loginFile()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600))

	require.NoError(t, a.holdNamedUnitKeysFrom(decide.JevSecret+",OPENROUTER_API_KEY", func(u secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		assert.Equal(t, []string{"/s", "studio", "/k/studio.key", "/bin/sops"}, []string{u.Store, u.As, u.Key, u.Sops})
		return map[string]secrets.Secret{
			decide.JevSecret:     secrets.NewSecret(decision),
			"OPENROUTER_API_KEY": secrets.NewSecret(route),
		}, nil
	}))
	assert.True(t, heldMatches(secrets.NewSecret(decision), a.getenv(decide.JevSecret)), "run did not read the decision key the seat holds")
	assert.True(t, heldMatches(secrets.NewSecret(route), a.getenv("OPENROUTER_API_KEY")), "run did not read the provider key the seat holds")
	assert.False(t, a.getenv(decide.JevSecret) == "env-copy-not-the-seat", "a value already in the environment won over the seat")

	kb, err := os.ReadFile(filepath.Join(filepath.Dir(path), unitKeysFile))
	require.NoError(t, err)
	assert.Contains(t, string(kb), decide.JevSecret)
	assert.Contains(t, string(kb), "OPENROUTER_API_KEY")
	assert.False(t, secrets.Leaks(string(kb), secrets.NewSecret(decision)))
	assert.False(t, secrets.Leaks(string(kb), secrets.NewSecret(route)))

	quiet := newApp(func(string) string { return "" })
	require.NoError(t, quiet.holdNamedUnitKeysFrom("", func(secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		t.Fatal("an empty setting read the seat")
		return nil, nil
	}))
	for n, was := range before {
		assert.Equal(t, true, os.Getenv(n) == was, "reading %s changed the process environment", n)
	}
}

// A name the seat does not hold refuses naming that name, and the refusal carries no value.
func TestANamedKeyTheSeatDoesNotHoldRefusesNamingIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := newApp(func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	})
	a.seatLoginOn()
	login := storeLogin{Redis: "mem:0", User: "coordinator", Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Secret: "NOVA_REDIS_COORDINATOR_PASSWORD"}
	b, err := json.Marshal(login)
	require.NoError(t, err)
	path, err := a.loginFile()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600))

	err = a.holdNamedUnitKeysFrom("MISSING_KEY", func(secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		return nil, errNamedMissing("MISSING_KEY")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MISSING_KEY")
	assert.Contains(t, err.Error(), "; run: ")
	assert.False(t, secrets.Leaks(err.Error(), secrets.NewSecret("fixture-decision-not-a-real-key-0001")))
}

// keys.json beside the login is the setting when run is given no --keys.
func TestKeysFileBesideTheLoginIsTheSetting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := newApp(func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	})
	a.seatLoginOn()
	login := storeLogin{Redis: "mem:0", User: "coordinator", Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Secret: "NOVA_REDIS_COORDINATOR_PASSWORD"}
	b, err := json.Marshal(login)
	require.NoError(t, err)
	path, err := a.loginFile()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), unitKeysFile), []byte("{\"names\":[\""+decide.JevSecret+"\"]}\n"), 0o600))

	saw := false
	require.NoError(t, a.holdNamedUnitKeysFrom("", func(u secrets.UnitKeyLogin) (map[string]secrets.Secret, error) {
		saw = true
		assert.Equal(t, []string{decide.JevSecret}, u.Names)
		return map[string]secrets.Secret{decide.JevSecret: secrets.NewSecret("fixture-decision-not-a-real-key-0001")}, nil
	}))
	assert.True(t, saw, "keys.json was not read")
	assert.True(t, heldMatches(secrets.NewSecret("fixture-decision-not-a-real-key-0001"), a.getenv(decide.JevSecret)))
}

// A keys file that carries a value is refused, and the refusal does not quote the file.
func TestAKeysFileThatCarriesAValueIsRefused(t *testing.T) {
	t.Parallel()
	_, err := decodeKeyNames([]byte("{\"names\":[\"JEV_API_KEY\"],\"value\":\"not-printed\"}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "value")
	assert.Contains(t, err.Error(), "names only")
	assert.NotContains(t, err.Error(), "not-printed")
}

func heldMatches(s secrets.Secret, got string) bool {
	ok := false
	_ = s.Use(func(v string) error {
		ok = got == v && v != ""
		return nil
	})
	return ok
}

func errNamedMissing(name string) error {
	return fmt.Errorf("seat studio of store /s holds no %s; run: nova-secrets names --store /s --as studio", name)
}
