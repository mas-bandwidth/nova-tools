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

// secretsRig is a fake machine for the secrets check: a key file with a mode,
// a store that is a git working copy with the seat's rules, the names
// `nova-secrets names` lists, and the seat's loop rows.
type secretsRig struct {
	keyMode    os.FileMode // the key file's mode; 0 makes no key file
	keyComment bool        // the key carries the `# public key:` line
	dirMode    os.FileMode // the key file's directory mode
	sops       bool        // a fake sops is on PATH
	git        bool        // the store is a git working copy
	rules      bool        // the store holds .sops.yaml
	names      []string    // the names `nova-secrets names` lists
	loops      string      // the seat's loop keys, comma separated; "" for none
	loopsErr   bool        // `nova-config loop list --json` fails
}

// runSecrets runs only the secrets check over a rig rooted in a t.TempDir().
func runSecrets(t *testing.T, rig secretsRig) Result {
	t.Helper()
	root := t.TempDir()

	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	if rig.sops {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "sops"), []byte("#!/bin/sh\n"), 0o755))
	}

	keyDir := filepath.Join(root, "keys")
	require.NoError(t, os.MkdirAll(keyDir, rig.dirMode))
	keyPath := filepath.Join(keyDir, "coordinator.key")
	if rig.keyMode != 0 {
		body := "AGE-SECRET-KEY-1TEST\n"
		if rig.keyComment {
			body = "# public key: age1test\n" + body
		}
		require.NoError(t, os.WriteFile(keyPath, []byte(body), rig.keyMode))
		require.NoError(t, os.Chmod(keyPath, rig.keyMode))
	}

	store := filepath.Join(root, "secrets")
	require.NoError(t, os.MkdirAll(store, 0o755))
	if rig.git {
		require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(store, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	}
	if rig.rules {
		require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules: []\n"), 0o644))
	}

	const seat = "coordinator"
	env := fakeEnv{
		env: map[string]string{
			"PATH":               bin,
			"NOVA_SECRETS_KEY":   keyPath,
			"NOVA_SECRETS_STORE": store,
			"NOVA_SECRETS_SEAT":  seat,
		},
		exec: func(name string, args ...string) (string, error) {
			switch {
			case name == "nova-config" && strings.Join(args, " ") == "loop list --json":
				if rig.loopsErr {
					return "", errors.New("nova-config: not found")
				}
				return loopListJSON(seat, rig.loops), nil
			case name == "nova-secrets" && len(args) > 0 && args[0] == "names":
				return namesLines(seat, rig.names), nil
			}
			return "", errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
		},
	}
	reg := NewRegistry()
	reg.Register(Check{Name: "secrets", Dependency: "the secrets store", Run: checkSecrets})
	res, _, err := reg.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

// loopListJSON is `nova-config loop list --json` with one loop row for seat,
// requiring keys (empty makes no rows).
func loopListJSON(seat, keys string) string {
	if keys == "" {
		return `{"items":[]}`
	}
	return fmt.Sprintf(`{"items":[{"kind":"loop","fields":{"seat":%q,"keys":%q,"enabled":"true"}}]}`, seat, keys)
}

// namesLines is the `nova-secrets names` output for names: one NAME line each
// and the OK tally, the shape the verb prints (names only, never a value).
func namesLines(seat string, names []string) string {
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "SECRETS NAME key=%s clear=false\n", name)
	}
	fmt.Fprintf(&b, "SECRETS NAMES OK as=%s keys=%d shown=%d sealed=%d clear=0\n", seat, len(names), len(names), len(names))
	return b.String()
}

// The secrets check holds sops, this machine's key at safe modes, the store,
// and every name the seat's loop records require, listed by name only: it
// fails while any of those is missing or wrong and passes when they are right
// (docs/SPEC-DOCTOR.md, the checks; docs/SETUP.md, dep-secrets-bb.w4).
func TestDoctorSecretsCheckFindsAKeyTheSeatNeeds(t *testing.T) {
	t.Parallel()

	// right is a machine with everything as the check wants it.
	right := func() secretsRig {
		return secretsRig{keyMode: 0o600, keyComment: true, dirMode: 0o700, sops: true, git: true, rules: true,
			names: []string{"GH_TOKEN"}, loops: "GH_TOKEN"}
	}

	t.Run("ok when sops, the key, the store and the required names are right", func(t *testing.T) {
		t.Parallel()
		r := runSecrets(t, right())
		assert.Equal(t, OK, r.Status, r)
		assert.Empty(t, r.Fix)
		assert.Contains(t, r.Evidence, "1 required present")
	})

	t.Run("ok when the seat's loop records require no secret", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.loops = ""
		r := runSecrets(t, rig)
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "0 required present")
	})

	t.Run("fail when sops is not on PATH", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.sops = false
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "sops is not on PATH")
		assert.Contains(t, r.Fix, "install sops")
	})

	t.Run("fail when the key file is not there", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.keyMode = 0
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "not readable")
		assert.Contains(t, r.Fix, "nova-secrets keygen")
	})

	t.Run("fail when the key carries no public half", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.keyComment = false
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "public key")
		assert.Contains(t, r.Fix, "age-keygen -y")
	})

	t.Run("fail when the key file is not mode 0600", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.keyMode = 0o644
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "0600")
		assert.Contains(t, r.Fix, "chmod 600")
	})

	t.Run("fail when the key's directory is not mode 0700", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.dirMode = 0o755
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "0700")
		assert.Contains(t, r.Fix, "chmod 700")
	})

	t.Run("fail when the store is not a git working copy", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.git = false
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "git working copy")
		assert.Contains(t, r.Fix, "git clone")
	})

	t.Run("fail when the store holds no sops rules", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.rules = false
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, ".sops.yaml")
	})

	t.Run("fail when a name the seat's loops require is not in the store", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.names = []string{"SOME_OTHER_KEY"}
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "GH_TOKEN")
		assert.Contains(t, r.Evidence, "not in the store")
		assert.Contains(t, r.Fix, "nova-secrets seal")
		assert.Contains(t, r.Fix, "GH_TOKEN")
	})

	t.Run("fail when the seat's loop records cannot be read", func(t *testing.T) {
		t.Parallel()
		rig := right()
		rig.loopsErr = true
		r := runSecrets(t, rig)
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "loop records could not be read")
		assert.Contains(t, r.Fix, "nova-config loop list")
	})
}
