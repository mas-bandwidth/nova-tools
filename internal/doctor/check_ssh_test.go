package doctor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sshRig is the test fixture for the ssh check: a fake inventory and fake ssh answers.
type sshRig struct {
	inv       string      // inventory YAML content
	benches   []string    // bench names to return
	failBench string      // bench that fails, or "" for all ok
	failReason string     // reason for failure
}

// runSSH runs only the ssh check over the rig.
func runSSH(t *testing.T, r sshRig) Result {
	t.Helper()
	root := t.TempDir()

	// Write inventory file
	invPath := root + "/seat_inv.yaml"
	require.NoError(t, os.WriteFile(invPath, []byte(r.inv), 0o644))

	env := fakeEnv{
		env:  map[string]string{"NOVA_SECRETS_STORE": root, "NOVA_SECRETS_SEAT": "seat"},
		root: root,
	}
	env.exec = func(name string, args ...string) (string, error) {
		if name == "ssh" {
			// Extract bench name from args
			var bench string
			for i, arg := range args {
				if arg != "-o" && arg != "BatchMode=yes" && arg != "ConnectTimeout=5" {
					bench = arg
					_ = i
					break
				}
			}
			if r.failBench != "" && bench == r.failBench {
				return "", errors.New("ssh: " + r.failReason)
			}
			return "success", nil
		}
		return "", errors.New("unexpected command: " + name)
	}

	reg := NewRegistry()
	reg.Register(Default.checks["ssh"])
	res, _, err := reg.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

func TestDoctorSSHCheckNamesTheBenchItCannotReach(t *testing.T) {
	t.Parallel()

	t.Run("all benches reachable is ok", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{
			inv:     "benches:\n  - bench1\n  - bench2\n",
			benches: []string{"bench1", "bench2"},
		})
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, "reachable")
		assert.Empty(t, r.Fix)
	})

	t.Run("one bench fails with timeout", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{
			inv:        "benches:\n  - bench1\n  - bench2\n",
			benches:    []string{"bench1", "bench2"},
			failBench:  "bench2",
			failReason: "timeout",
		})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "bench2")
		assert.Contains(t, r.Evidence, "timeout")
		assert.Contains(t, r.Fix, "known_hosts")
	})

	t.Run("one bench fails with host key error", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{
			inv:        "benches:\n  - bench1\n  - bench2\n",
			benches:    []string{"bench1", "bench2"},
			failBench:  "bench2",
			failReason: "host key verification failed",
		})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "bench2")
		assert.Contains(t, r.Evidence, "host key")
	})

	t.Run("one bench fails with permission denied", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{
			inv:        "benches:\n  - bench1\n  - bench2\n",
			benches:    []string{"bench1", "bench2"},
			failBench:  "bench2",
			failReason: "permission denied (publickey)",
		})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "bench2")
		assert.Contains(t, r.Evidence, "no key")
	})

	t.Run("no inventory is a fail", func(t *testing.T) {
		t.Parallel()
		reg := NewRegistry()
		env := fakeEnv{
			env:  map[string]string{"NOVA_SECRETS_STORE": "/nonexistent", "NOVA_SECRETS_SEAT": "seat"},
			root: t.TempDir(),
		}
		reg.Register(Default.checks["ssh"])
		res, _, err := reg.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, Fail, res[0].Status)
		assert.Contains(t, res[0].Evidence, "cannot read inventory")
	})
}
