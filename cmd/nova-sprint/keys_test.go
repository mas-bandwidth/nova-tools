package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// run --decide reads JEV_API_KEY from the seat login in process. The
// environment's value is not the key, and a missing name refuses naming it.
func TestDecisionKeyIsReadFromTheSeatNotTheEnvironment(t *testing.T) {
	t.Parallel()
	const seatKey = "seat-decision-not-env"
	const envKey = "env-decision-not-seat"

	dir := t.TempDir()
	la := newLoginApp(t, dir)
	login := "seat login --store " + filepath.Join(dir, "secrets") + " --as studio --key " + filepath.Join(dir, "studio.key") +
		" --sops /usr/local/bin/sops --secret NOVA_REDIS_COORDINATOR_PASSWORD --user coordinator --redis 127.0.0.1:6380"
	code, _, errs := la.do(login)
	require.Equal(t, 0, code, errs)

	la.env[decide.JevSecret] = envKey
	la.a.loginSecret = func(l secrets.Login) (secrets.Secret, error) {
		if l.Name == decide.JevSecret || (len(l.Names) == 1 && l.Names[0] == decide.JevSecret) {
			return secrets.NewSecret(seatKey), nil
		}
		return secrets.Secret{}, errors.New("seat " + l.As + " of store " + l.Store + " holds no " + l.Name)
	}
	got, err := la.a.decisionKey()
	require.NoError(t, err)
	assert.False(t, got == envKey, "the decision key was taken from the environment")
	assert.True(t, got == seatKey, "the decision key was not the seat's value")

	la.a.loginSecret = func(l secrets.Login) (secrets.Secret, error) {
		return secrets.Secret{}, errors.New("seat " + l.As + " of store " + l.Store + " holds no " + l.Name)
	}
	_, err = la.a.decisionKey()
	require.Error(t, err)
	require.Contains(t, err.Error(), decide.JevSecret)
	require.Contains(t, err.Error(), "run: nova-secrets seal")
	require.NotContains(t, err.Error(), seatKey)
	require.NotContains(t, err.Error(), envKey)

	bare := newLoginApp(t, t.TempDir())
	bare.env[decide.JevSecret] = envKey
	got, err = bare.a.decisionKey()
	require.NoError(t, err)
	assert.True(t, got == envKey, "with no seat login recorded, the decision key is the environment's")
}
