package goenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateScrubbers are the two gate environment scrub paths: Clean builds a child
// `go` command's environment, and WithoutSecrets strips a gate's credentials
// from a process the gate did not write. The repair is one predicate,
// keyshape.SecretName, so the two cannot disagree about what a secret is.
func gateScrubbers() []struct {
	name string
	fn   func([]string) []string
} {
	return []struct {
		name string
		fn   func([]string) []string
	}{
		{"Clean", Clean},
		{"WithoutSecrets", WithoutSecrets},
	}
}

// TestGateEnvironmentsDropPasswordCredentials pins the leak: SecretName matched
// KEY, TOKEN and SECRET only, so Clean and WithoutSecrets preserved
// PGPASSWORD, NOVA_PG_PASSWORD and NOVA_REDIS_PASSWORD -- actual database
// credentials -- in a child running a pull request's code. The names are
// synthetic markers; nothing here reads a process environment or a real secret.
func TestGateEnvironmentsDropPasswordCredentials(t *testing.T) {
	t.Parallel()

	passwordNames := []string{
		"PGPASSWORD",
		"pgpassword",
		"PgPassword",
		"NOVA_PG_PASSWORD",
		"nova_pg_password",
		"NOVA_REDIS_PASSWORD",
		"nova_redis_password",
		"DB_PASSWD",
		"db_passwd",
		"MiXeD_PaSsWd",
	}

	t.Run("password and passwd names are removed mixed case", func(t *testing.T) {
		t.Parallel()
		for _, s := range gateScrubbers() {
			for _, name := range passwordNames {
				name, entry := name, name+"=not-a-real-secret-marker"
				t.Run(s.name+"/"+name, func(t *testing.T) {
					t.Parallel()
					got := s.fn([]string{"PATH=/usr/bin", entry, "HOME=/home/user"})
					assert.Equal(t, []string{"PATH=/usr/bin", "HOME=/home/user"}, got,
						"%s kept the credential %q", s.name, name)
				})
			}
		}
	})

	t.Run("ordinary variables keep exact order and values", func(t *testing.T) {
		t.Parallel()
		env := []string{
			"PATH=/usr/bin",
			"HOME=/home/user",
			"LANG=en_US.UTF-8",
			"TERM=xterm-256color",
			"NOVA_SWARM_JOB=a-job",
		}
		for _, s := range gateScrubbers() {
			t.Run(s.name, func(t *testing.T) {
				t.Parallel()
				got := s.fn(env)
				require.Equal(t, env, got, "%s = %q, want the input unchanged and in order", s.name, got)
			})
		}
	})

	t.Run("input is not modified", func(t *testing.T) {
		t.Parallel()
		for _, s := range gateScrubbers() {
			t.Run(s.name, func(t *testing.T) {
				t.Parallel()
				env := []string{"PATH=/usr/bin", "PGPASSWORD=not-a-real-secret-marker", "HOME=/home/user"}
				before := append([]string(nil), env...)
				_ = s.fn(env)
				require.Equal(t, before, env, "%s modified its input: %q", s.name, env)
			})
		}
	})

	t.Run("existing credentials still drop", func(t *testing.T) {
		t.Parallel()
		env := []string{
			"PATH=/usr/bin",
			"GH_TOKEN=a-forge-token",
			"GITHUB_TOKEN=a-forge-token",
			"DEEPSEEK_API_KEY=a-provider-key",
			"aws_secret_access_key=a-lower-case-secret",
			"HOME=/home/user",
		}
		want := []string{"PATH=/usr/bin", "HOME=/home/user"}
		for _, s := range gateScrubbers() {
			t.Run(s.name, func(t *testing.T) {
				t.Parallel()
				require.Equal(t, want, s.fn(env), "%s stopped dropping the existing credentials", s.name)
			})
		}
	})

	t.Run("GOFLAGS handling is unchanged", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, Clean([]string{"GOFLAGS=-json"}), "Clean stopped dropping GOFLAGS")
		require.Equal(t, []string{"GOFLAGS=-json"}, WithoutSecrets([]string{"GOFLAGS=-json"}),
			"WithoutSecrets changed the output-shape rule it is not")
	})
}

// TestRemovedNamesPasswordShapes holds the documented contract to the matcher:
// a reader pointed at Removed must see the names the matcher now drops.
func TestRemovedNamesPasswordShapes(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"PASSWORD", "PASSWD"} {
		assert.Contains(t, Removed, name, "Removed does not name %s", name)
	}
}
