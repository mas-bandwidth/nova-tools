package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerDecisionKeyReadsInProcessFromSeatLogin(t *testing.T) {
	t.Parallel()
	const jevSecretVal = "jev-unit-test-val-987"
	assert.Empty(t, os.Getenv(decide.JevSecret))

	dir := t.TempDir()
	loginPath := filepath.Join(dir, "login.json")
	require.NoError(t, os.WriteFile(loginPath, []byte(`{
		"redis": "127.0.0.1:6399",
		"user": "coordinator",
		"store": "/s",
		"as": "bench-a",
		"key": "/k/bench-a.key",
		"sops": "/bin/sops",
		"secret": "NOVA_REDIS_COORDINATOR_PASSWORD",
		"secrets": ["`+decide.JevSecret+`"]
	}`), 0o600))

	a := newApp(func(k string) string { return "" })
	a.loginFile = func() (string, error) { return loginPath, nil }
	a.seatLoginOn()
	a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return store.NewMem(), nil }
	a.loginSecret = func(l secrets.Login) (secrets.Secret, error) {
		assert.Equal(t, "/s", l.Store)
		assert.Equal(t, "bench-a", l.As)
		if l.Name == decide.JevSecret {
			return secrets.NewSecret(jevSecretVal), nil
		}
		if l.Name == "NOVA_REDIS_COORDINATOR_PASSWORD" {
			return secrets.NewSecret("redis-pw"), nil
		}
		return secrets.Secret{}, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, l.Name, l.Store, l.As)
	}

	key, err := a.serverDecisionKey()
	require.NoError(t, err)
	assert.Equal(t, jevSecretVal, key)

	require.NoError(t, a.checkServerLoginSecrets())
}

func TestServerDecisionKeyRefusesWhenMissingFromSeat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	loginPath := filepath.Join(dir, "login.json")
	require.NoError(t, os.WriteFile(loginPath, []byte(`{
		"redis": "127.0.0.1:6399",
		"user": "coordinator",
		"store": "/s",
		"as": "bench-a",
		"key": "/k/bench-a.key",
		"sops": "/bin/sops",
		"secret": "NOVA_REDIS_COORDINATOR_PASSWORD"
	}`), 0o600))

	a := newApp(func(k string) string { return "" })
	a.loginFile = func() (string, error) { return loginPath, nil }
	a.seatLoginOn()
	a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return store.NewMem(), nil }
	a.loginSecret = func(l secrets.Login) (secrets.Secret, error) {
		if l.Name == "NOVA_REDIS_COORDINATOR_PASSWORD" {
			return secrets.NewSecret("redis-pw"), nil
		}
		return secrets.Secret{}, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, l.Name, l.Store, l.As)
	}

	_, err := a.serverDecisionKey()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat bench-a of store /s holds no JEV_API_KEY")
	assert.Contains(t, err.Error(), "run: nova-secrets names --store /s --as bench-a")

	var stdout, stderr bytes.Buffer
	code := a.cmdRun([]string{"--decide", t.TempDir()}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "seat bench-a of store /s holds no JEV_API_KEY")
	assert.Contains(t, stderr.String(), "run: nova-secrets names --store /s --as bench-a")
}

func TestServerLoginSecretsRefusesMissingNamedSecretAtStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	loginPath := filepath.Join(dir, "login.json")
	require.NoError(t, os.WriteFile(loginPath, []byte(`{
		"redis": "127.0.0.1:6399",
		"user": "coordinator",
		"store": "/s",
		"as": "bench-a",
		"key": "/k/bench-a.key",
		"sops": "/bin/sops",
		"secret": "NOVA_REDIS_COORDINATOR_PASSWORD",
		"secrets": ["EXTRA_KEY"]
	}`), 0o600))

	a := newApp(func(k string) string { return "" })
	a.loginFile = func() (string, error) { return loginPath, nil }
	a.seatLoginOn()
	a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return store.NewMem(), nil }
	a.loginSecret = func(l secrets.Login) (secrets.Secret, error) {
		if l.Name == "NOVA_REDIS_COORDINATOR_PASSWORD" {
			return secrets.NewSecret("redis-pw"), nil
		}
		return secrets.Secret{}, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, l.Name, l.Store, l.As)
	}

	var stdout, stderr bytes.Buffer
	code := a.cmdRun([]string{"--answer-rules=false", "--idle-alarm=false"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "seat bench-a of store /s holds no EXTRA_KEY")
	assert.Contains(t, stderr.String(), "run: nova-secrets names --store /s --as bench-a")
}
