package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorSecretsCheckFindsAKeyTheSeatNeeds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("pass when all prerequisites are met", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create a key file with public key comment
		keyPath := filepath.Join(tmpDir, "test.key")
		keyContent := "# public key: age1testkey1234567890abcdefghijklmnopqrstuvwxyz\nprivate key data"
		require.NoError(t, os.WriteFile(keyPath, []byte(keyContent), 0600))

		// Create a fake store with .git
		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(filepath.Join(storePath, ".git"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(storePath, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644))

		// Create seat file
		seatFile := filepath.Join(storePath, "testseat.yaml")
		require.NoError(t, os.WriteFile(seatFile, []byte("sops:\n  metadata:\n    recipients: [age1testkey]"), 0644))

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh\necho sops"), 0755))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":   keyPath,
				"NOVA_SECRETS_STORE": storePath,
				"NOVA_SECRETS_SEAT":  "testseat",
				"PATH":               tmpDir + string(os.PathListSeparator) + "/usr/bin",
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, OK, result.Status)
		assert.Contains(t, result.Evidence, "sops")
		assert.Contains(t, result.Evidence, "testseat")
	})

	t.Run("fail when sops is not on PATH", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		keyPath := filepath.Join(tmpDir, "test.key")
		require.NoError(t, os.WriteFile(keyPath, []byte("# public key: age1test\n"), 0600))

		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(filepath.Join(storePath, ".git"), 0755))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":   keyPath,
				"NOVA_SECRETS_STORE": storePath,
				"NOVA_SECRETS_SEAT":  "testseat",
				"PATH":               "/usr/bin", // no sops here
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "sops")
		assert.Contains(t, result.Fix, "install sops")
	})

	t.Run("fail when key file does not exist", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(filepath.Join(storePath, ".git"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(storePath, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":   filepath.Join(tmpDir, "nonexistent.key"),
				"NOVA_SECRETS_STORE": storePath,
				"NOVA_SECRETS_SEAT":  "testseat",
				"PATH":               tmpDir,
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "not readable")
	})

	t.Run("fail when key file lacks public key comment", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		keyPath := filepath.Join(tmpDir, "test.key")
		require.NoError(t, os.WriteFile(keyPath, []byte("just private key data"), 0600))

		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(filepath.Join(storePath, ".git"), 0755))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":   keyPath,
				"NOVA_SECRETS_STORE": storePath,
				"NOVA_SECRETS_SEAT":  "testseat",
				"PATH":               tmpDir,
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "lacks the public key comment")
	})

	t.Run("fail when store is not a git working copy", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		keyPath := filepath.Join(tmpDir, "test.key")
		require.NoError(t, os.WriteFile(keyPath, []byte("# public key: age1test\n"), 0600))

		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(storePath, 0755))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":   keyPath,
				"NOVA_SECRETS_STORE": storePath,
				"NOVA_SECRETS_SEAT":  "testseat",
				"PATH":               tmpDir,
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "not a git working copy")
	})

	t.Run("fail when seat file does not exist", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		keyPath := filepath.Join(tmpDir, "test.key")
		require.NoError(t, os.WriteFile(keyPath, []byte("# public key: age1test\n"), 0600))

		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(filepath.Join(storePath, ".git"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(storePath, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":   keyPath,
				"NOVA_SECRETS_STORE": storePath,
				"NOVA_SECRETS_SEAT":  "nonexistentseat",
				"PATH":               tmpDir,
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "not found")
	})

	t.Run("fail when seat name is not configured", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		keyPath := filepath.Join(tmpDir, "test.key")
		require.NoError(t, os.WriteFile(keyPath, []byte("# public key: age1test\n"), 0600))

		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(filepath.Join(storePath, ".git"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(storePath, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":   keyPath,
				"NOVA_SECRETS_STORE": storePath,
				"PATH":               tmpDir,
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "no seat name configured")
	})

	t.Run("fail when NOVA_SECRETS_KEY is not set", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		storePath := filepath.Join(tmpDir, "secrets")
		require.NoError(t, os.MkdirAll(filepath.Join(storePath, ".git"), 0755))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_STORE": storePath,
				"NOVA_SECRETS_SEAT":  "testseat",
				"PATH":               tmpDir,
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "no age key file path configured")
	})

	t.Run("fail when NOVA_SECRETS_STORE is not set", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()

		// Create fake sops
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		keyPath := filepath.Join(tmpDir, "test.key")
		require.NoError(t, os.WriteFile(keyPath, []byte("# public key: age1test\n"), 0600))

		env := fakeEnv{
			env: map[string]string{
				"NOVA_SECRETS_KEY":  keyPath,
				"NOVA_SECRETS_SEAT": "testseat",
				"PATH":               tmpDir,
			},
			root: tmpDir,
		}

		result := checkSecrets(ctx, env)
		assert.Equal(t, Fail, result.Status)
		assert.Contains(t, result.Evidence, "no secrets store path configured")
	})
}

func TestSopsOnPath(t *testing.T) {
	t.Parallel()

	t.Run("finds sops on PATH", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		sopsPath := filepath.Join(tmpDir, "sops")
		require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh"), 0755))

		env := fakeEnv{
			env:  map[string]string{"PATH": tmpDir},
			root: tmpDir,
		}

		result := sopsOnPath(env)
		assert.Equal(t, sopsPath, result)
	})

	t.Run("returns empty when sops not found", func(t *testing.T) {
		t.Parallel()
		env := fakeEnv{
			env: map[string]string{"PATH": "/usr/bin"},
		}

		result := sopsOnPath(env)
		assert.Equal(t, "", result)
	})
}
