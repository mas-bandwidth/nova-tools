package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// internal/secrets reads seat login secrets in process: it holds no writer of this
	// package's stream and prints nothing; secret values are protected and never leak.
	swarmAudit.Imports = append(swarmAudit.Imports, `"github.com/mas-bandwidth/nova-tools/internal/secrets"`)
}

func TestMemberSecretsReadInProcessFromSeatLogin(t *testing.T) {
	t.Parallel()
	const (
		routeKey = "DEEPSEEK_API_KEY"
		routeVal = "deepseek-proc-val-123"
	)
	assert.Empty(t, os.Getenv(routeKey), "route key must not be in environment")

	seat := "seat-" + t.Name()
	cleanup1 := registerMemberLoginLoader(seat, func(s string) (secrets.Login, bool, error) {
		return secrets.Login{
			Store:   "/secrets",
			As:      seat,
			Secrets: []string{routeKey},
		}, true, nil
	})
	t.Cleanup(cleanup1)

	cleanup2 := registerMemberSecretsReader(seat, func(l secrets.Login, names ...string) (map[string]secrets.Secret, error) {
		assert.Equal(t, "/secrets", l.Store)
		assert.Equal(t, seat, l.As)
		assert.Equal(t, []string{routeKey}, names)
		return map[string]secrets.Secret{
			routeKey: secrets.NewSecret(routeVal),
		}, nil
	})
	t.Cleanup(cleanup2)

	secs, err := loadAndCheckMemberSecrets(seat)
	require.NoError(t, err)
	require.Contains(t, secs, routeKey)

	var got string
	require.NoError(t, secs[routeKey].Use(func(v string) error { got = v; return nil }))
	assert.Equal(t, routeVal, got)
}

func TestMemberSecretsRefusesMissingNamedSecretAtStart(t *testing.T) {
	t.Parallel()
	seat := "seat-" + t.Name()
	cleanup1 := registerMemberLoginLoader(seat, func(s string) (secrets.Login, bool, error) {
		return secrets.Login{
			Store:   "/secrets",
			As:      seat,
			Secrets: []string{"MISSING_KEY"},
		}, true, nil
	})
	t.Cleanup(cleanup1)

	cleanup2 := registerMemberSecretsReader(seat, func(l secrets.Login, names ...string) (map[string]secrets.Secret, error) {
		return nil, fmt.Errorf("seat %s of store %s holds no %s; the names it holds: run: nova-secrets names --store %s --as %s", l.As, l.Store, names[0], l.Store, l.As)
	})
	t.Cleanup(cleanup2)

	var stdout, stderr bytes.Buffer
	args := []string{"--as", seat, "--server", "127.0.0.1:6399", "--harness", "/bin/true", "--root", t.TempDir(), "--once"}
	code := cmdMember(args, &stdout, &stderr, noServer)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "seat "+seat+" of store /secrets holds no MISSING_KEY")
	assert.Contains(t, stderr.String(), "run: nova-secrets names --store /secrets --as "+seat)
}

func TestChildEnvForRouteContainsOnlyRouteKeyNeverWholeSet(t *testing.T) {
	t.Parallel()
	const (
		deepseekKey  = "DEEPSEEK_API_KEY"
		deepseekVal  = "ds-secret-999"
		anthropicKey = "ANTHROPIC_API_KEY"
		anthropicVal = "ant-secret-888"
		jevKey       = "JEV_API_KEY"
		jevVal       = "jev-secret-777"
	)

	rn := &nativeRunner{
		env: []string{
			"PATH=/usr/bin:/bin",
			"HOME=/home/test",
			"TMPDIR=/tmp/test",
			jevKey + "=" + jevVal, // member's environment carries JEV_API_KEY
		},
		secrets: map[string]secrets.Secret{
			deepseekKey:  secrets.NewSecret(deepseekVal),
			anthropicKey: secrets.NewSecret(anthropicVal),
		},
	}

	// Route: deepseek/deepseek-chat
	childEnv := rn.childEnvFor("deepseek/deepseek-chat")

	// Child environment must contain ONLY DEEPSEEK_API_KEY
	assert.Contains(t, childEnv, deepseekKey+"="+deepseekVal)

	// Neither other provider keys nor JEV_API_KEY
	for _, envVar := range childEnv {
		name, _, _ := strings.Cut(envVar, "=")
		assert.NotEqual(t, anthropicKey, name, "child must not receive other provider keys")
		assert.NotEqual(t, jevKey, name, "child must not receive JEV_API_KEY")
	}

	// For local route ollama/llama3, no provider keys are passed
	localEnv := rn.childEnvFor("ollama/llama3")
	for _, envVar := range localEnv {
		name, _, _ := strings.Cut(envVar, "=")
		assert.NotEqual(t, deepseekKey, name)
		assert.NotEqual(t, anthropicKey, name)
		assert.NotEqual(t, jevKey, name)
	}
}

func TestMemberPassNoteSuppressedWhenSecretsHeld(t *testing.T) {
	t.Parallel()
	// passNote with effectivePass containing keys read from seat
	effectivePass := []string{"DEEPSEEK_API_KEY"}
	note := passNote("deepseek/deepseek-chat", effectivePass, "")
	assert.Empty(t, note, "passNote must be suppressed when seat holds the route key")
}
