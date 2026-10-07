package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// secretsRig is the machine the secrets check reads: a PATH holding sops (or
// not), a key file under t.TempDir(), and a fake nova-secrets names listing.
// Nothing real runs and no value is printed.
type secretsRig struct {
	noSops   bool
	noKey    bool        // the key file is absent
	keyMode  os.FileMode // key file mode; 0 means 0600
	dirMode  os.FileMode // keys directory mode; 0 means 0700
	noPubKey bool        // the key file carries no public key comment line
	names    []string    // names `nova-secrets names` lists; nil means it refuses
	extraKey string      // sentinel private line planted in the key file
}

func runSecretsCheck(t *testing.T, r secretsRig) Result {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	if !r.noSops {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "sops"), []byte("#!/bin/sh\n"), 0o755))
	}
	keyMode := r.keyMode
	if keyMode == 0 {
		keyMode = 0o600
	}
	dirMode := r.dirMode
	if dirMode == 0 {
		dirMode = 0o700
	}
	keys := filepath.Join(root, "keys")
	require.NoError(t, os.MkdirAll(keys, 0o755))
	keyContent := "# public key: age1testkeyqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq\nAGE-SECRET-KEY-TESTONLY\n"
	if r.noPubKey {
		keyContent = "AGE-SECRET-KEY-TESTONLY\n"
	}
	if r.extraKey != "" {
		keyContent += r.extraKey + "\n"
	}
	if !r.noKey {
		require.NoError(t, os.WriteFile(filepath.Join(keys, "coordinator.key"), []byte(keyContent), 0o600))
		require.NoError(t, os.Chmod(filepath.Join(keys, "coordinator.key"), keyMode))
	}
	require.NoError(t, os.Chmod(keys, dirMode))
	env := fakeEnv{
		env: map[string]string{
			"PATH":               "bin",
			"NOVA_SECRETS_KEY":   "keys/coordinator.key",
			"NOVA_SECRETS_STORE": "secrets",
			"NOVA_SECRETS_SEAT":  "coordinator",
		},
		root: root,
		exec: func(name string, args ...string) (string, error) {
			if filepath.Base(name) != "nova-secrets" {
				return "", errors.New("unexpected command: " + name)
			}
			if r.names == nil {
				return "SECRETS NAMES REFUSED: no such seat; run: nova-secrets names -h", errors.New("exit 2")
			}
			var b strings.Builder
			for _, n := range r.names {
				b.WriteString("SECRETS NAME key=" + n + " clear=false\n")
			}
			fmt.Fprintf(&b, "SECRETS NAMES OK as=coordinator keys=%d shown=%d sealed=%d clear=0\n", len(r.names), len(r.names), len(r.names))
			return b.String(), nil
		},
	}
	return checkSecrets(context.Background(), env)
}

func fullSeatNames() []string { return requiredSecrets() }

// TestDoctorSecretsCheckFindsAKeyTheSeatNeeds pins the secrets check
// (docs/SETUP.md, dep-secrets-bb.w3): sops on PATH, the machine's key file
// readable with safe permissions, and every secret name the seat and the loop
// records require present by nova-secrets' own listing, names only.
func TestDoctorSecretsCheckFindsAKeyTheSeatNeeds(t *testing.T) {
	t.Parallel()

	t.Run("pass when sops, the key and every required name are there", func(t *testing.T) {
		t.Parallel()
		r := runSecretsCheck(t, secretsRig{names: fullSeatNames(), extraKey: "SENTINEL-PRIVATE-9f8e"})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "coordinator")
		assert.Empty(t, r.Fix)
		assert.NotContains(t, r.Evidence+r.Fix, "SENTINEL-PRIVATE-9f8e", "no key material reaches the output")
	})

	t.Run("fail when sops is not on PATH", func(t *testing.T) {
		t.Parallel()
		r := runSecretsCheck(t, secretsRig{noSops: true, names: fullSeatNames()})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "sops")
		assert.Contains(t, r.Fix, "sops")
	})

	t.Run("fail when the key file is missing", func(t *testing.T) {
		t.Parallel()
		r := runSecretsCheck(t, secretsRig{noKey: true, names: fullSeatNames()})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "not readable")
		assert.Contains(t, r.Fix, "nova-secrets keygen")
	})

	t.Run("fail when the key file is not mode 0600", func(t *testing.T) {
		t.Parallel()
		r := runSecretsCheck(t, secretsRig{keyMode: 0o644, names: fullSeatNames()})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "0600")
		assert.Contains(t, r.Fix, "chmod 600")
	})

	t.Run("fail when the key directory is not mode 0700", func(t *testing.T) {
		t.Parallel()
		r := runSecretsCheck(t, secretsRig{dirMode: 0o755, names: fullSeatNames()})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "0700")
		assert.Contains(t, r.Fix, "chmod 700")
	})

	t.Run("fail when the key file carries no public key line", func(t *testing.T) {
		t.Parallel()
		r := runSecretsCheck(t, secretsRig{noPubKey: true, names: fullSeatNames()})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "public key")
		assert.Contains(t, r.Fix, "age-keygen -y")
	})

	t.Run("fail naming the required key the seat lacks", func(t *testing.T) {
		t.Parallel()
		names := fullSeatNames()[:3]
		r := runSecretsCheck(t, secretsRig{names: names})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, requiredSecrets()[3])
		assert.Contains(t, r.Fix, "nova-secrets seal")
	})

	t.Run("fail when the names listing refuses", func(t *testing.T) {
		t.Parallel()
		r := runSecretsCheck(t, secretsRig{names: nil})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "nova-secrets names")
		assert.Contains(t, r.Fix, "nova-secrets names")
	})
}
