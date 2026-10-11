package goenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoenvCoverWithoutSecretsDropsSecretNames pins
// WithoutSecrets: every variable whose NAME carries KEY, TOKEN, SECRET,
// PASSWORD or PASSWD is removed, and every other variable is kept in order.
// The values are synthetic markers; nothing here reads a process environment
// or a real secret.
func TestGoenvCoverWithoutSecretsDropsSecretNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{
			name: "drops the three documented credential shapes",
			env: []string{
				"PATH=/usr/bin",
				"GH_TOKEN=a-forge-token",
				"GITHUB_TOKEN=a-forge-token",
				"DEEPSEEK_API_KEY=a-provider-key",
				"HOME=/home/user",
			},
			want: []string{"PATH=/usr/bin", "HOME=/home/user"},
		},
		{
			name: "drops password and passwd names mixed case",
			env: []string{
				"PATH=/usr/bin",
				"PGPASSWORD=not-a-real-secret-marker",
				"NOVA_REDIS_PASSWORD=not-a-real-secret-marker",
				"DB_PASSWD=not-a-real-secret-marker",
				"HOME=/home/user",
			},
			want: []string{"PATH=/usr/bin", "HOME=/home/user"},
		},
		{
			name: "keeps variables that carry secret-like substrings in values",
			env: []string{
				"PATH=/usr/bin",
				"NOTE=token-in-value",
				"CONFIG=see http://example.invalid/secret/key",
				"HOME=/home/user",
			},
			want: []string{
				"PATH=/usr/bin",
				"NOTE=token-in-value",
				"CONFIG=see http://example.invalid/secret/key",
				"HOME=/home/user",
			},
		},
		{
			name: "drops everything that is secret-named",
			env:  []string{"GH_TOKEN=x", "DEEPSEEK_API_KEY=y", "PGPASSWORD=z"},
			want: []string{},
		},
		{
			name: "empty input returns empty",
			env:  []string{},
			want: []string{},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := WithoutSecrets(tc.env)
			assert.Equal(t, tc.want, got, "WithoutSecrets = %q, want %q", got, tc.want)
		})
	}
}

// TestGoenvCoverWithoutSecretsKeepsEntriesWithoutEquals pins the refusal
// branch: an entry with no '=' cannot be split into name and value, so
// SecretName is never called on it and it is kept rather than dropped. This
// is the one refusal the main path does not cover, and a real os.Environ
// always carries malformed entries on some platform.
func TestGoenvCoverWithoutSecretsKeepsEntriesWithoutEquals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{
			name: "bare token is kept",
			env:  []string{"PATH=/usr/bin", "NOEQUALSIGN", "HOME=/home/user"},
			want: []string{"PATH=/usr/bin", "NOEQUALSIGN", "HOME=/home/user"},
		},
		{
			name: "empty entry is kept",
			env:  []string{"", "PATH=/usr/bin"},
			want: []string{"", "PATH=/usr/bin"},
		},
		{
			name: "only a bare token returns it alone",
			env:  []string{"NOEQUALSIGN"},
			want: []string{"NOEQUALSIGN"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := WithoutSecrets(tc.env)
			assert.Equal(t, tc.want, got, "WithoutSecrets = %q, want %q", got, tc.want)
		})
	}
}

// TestGoenvCoverWithoutSecretsDoesNotModifyInput pins the documented
// contract that WithoutSecrets copies its input: the caller keeps its own
// environment, so appending WithoutSecrets' result after Clean is safe.
func TestGoenvCoverWithoutSecretsDoesNotModifyInput(t *testing.T) {
	t.Parallel()

	env := []string{
		"PATH=/usr/bin",
		"GH_TOKEN=a-forge-token",
		"NOEQUALSIGN",
		"HOME=/home/user",
	}
	before := append([]string(nil), env...)
	_ = WithoutSecrets(env)
	require.Equal(t, before, env, "WithoutSecrets modified its input: %q", env)
}
