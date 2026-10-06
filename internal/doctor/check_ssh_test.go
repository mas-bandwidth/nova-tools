package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reachRig is a fake Env for the ssh check: a fake exec that answers
// `nova-config inventory --list` with an inventory and each ssh probe from a
// table, and an injected clock. Nothing real runs and no socket is opened.
type reachRig struct {
	t        *testing.T
	fakeEnv  fakeEnv
	benches  []string // the inventory's benches group, in order
	local    string   // the bench the inventory marks ansible_connection=local
	invErr   error    // the inventory command fails with this
	reachErr map[string]error
}

func (r reachRig) exec(name string, args ...string) (string, error) {
	switch name {
	case "nova-config":
		require.Equal(r.t, []string{"inventory", "--list"}, args)
		if r.invErr != nil {
			return "", r.invErr
		}
		return r.inventory(), nil
	case "ssh":
		bench := args[len(args)-2]
		require.Equal(r.t, []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", bench, "true"}, args)
		if err := r.reachErr[bench]; err != nil {
			return "", err
		}
		return "", nil
	}
	return "", errors.New("unexpected command: " + name)
}

// inventory is `nova-config inventory --list` as the tool prints it: the
// benches group and each host's ansible_connection.
func (r reachRig) inventory() string {
	hostvars := map[string]map[string]any{}
	for _, b := range r.benches {
		hostvars[b] = map[string]any{}
	}
	if r.local != "" {
		hostvars[r.local] = map[string]any{"ansible_connection": "local"}
	}
	b, err := json.Marshal(map[string]any{
		"_meta":   map[string]any{"hostvars": hostvars},
		"benches": map[string]any{"hosts": r.benches},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// sshRefused is the *exec.ExitError the real exec seam returns for an ssh that
// failed, carrying the line ssh printed on its stderr.
func sshRefused(stderr string) error { return &exec.ExitError{Stderr: []byte(stderr)} }

// runReach runs only the ssh check over the rig.
func runReach(t *testing.T, r reachRig) Result {
	t.Helper()
	r.t = t
	r.fakeEnv = fakeEnv{env: map[string]string{"NOVA_MACHINE": "coordinator"}, exec: r.exec,
		clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	reg := NewRegistry()
	reg.Register(Default.checks["ssh"])
	res, _, err := reg.Run(context.Background(), r.fakeEnv, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

// TestDoctorSSHCheckNamesTheBenchItCannotReach pins the ssh check: the check
// runs `ssh -o BatchMode=yes -o ConnectTimeout=5 <bench> true` for each bench
// the inventory names and fails naming the one it cannot reach, with the
// documented step as its fix (docs/SETUP.md, dep-ssh-b.w3). Every bench that
// answers is ok.
func TestDoctorSSHCheckNamesTheBenchItCannotReach(t *testing.T) {
	t.Parallel()

	t.Run("every bench answers is ok", func(t *testing.T) {
		t.Parallel()
		r := runReach(t, reachRig{benches: []string{"bench-a", "bench-b"}, reachErr: map[string]error{}})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "2 bench")
		assert.Empty(t, r.Fix)
	})

	t.Run("a bench with no key is named with the authorize step", func(t *testing.T) {
		t.Parallel()
		r := runReach(t, reachRig{benches: []string{"bench-a", "bench-b"},
			reachErr: map[string]error{"bench-b": sshRefused("bench-b: Permission denied (publickey).")}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "bench-b")
		assert.Contains(t, r.Evidence, "no key")
		assert.NotContains(t, r.Evidence, "bench-a:")
		assert.Contains(t, r.Fix, "authorized_keys")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-ssh-b.w3")
	})

	t.Run("an unknown host key is named with the known_hosts step", func(t *testing.T) {
		t.Parallel()
		r := runReach(t, reachRig{benches: []string{"bench-a"},
			reachErr: map[string]error{"bench-a": sshRefused("Host key verification failed.")}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "bench-a")
		assert.Contains(t, r.Evidence, "unknown host key")
		assert.Contains(t, r.Fix, "known_hosts")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-ssh-b.w3")
	})

	t.Run("a bench that times out is named with the reachability step", func(t *testing.T) {
		t.Parallel()
		r := runReach(t, reachRig{benches: []string{"bench-a"},
			reachErr: map[string]error{"bench-a": sshRefused("ssh: connect to host bench-a port 22: Connection timed out")}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "bench-a")
		assert.Contains(t, r.Evidence, "timeout")
		assert.Contains(t, r.Fix, "tailnet")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-ssh-b.w3")
	})

	t.Run("the machine running the check is not probed", func(t *testing.T) {
		t.Parallel()
		r := runReach(t, reachRig{benches: []string{"coordinator", "bench-a"}, local: "coordinator",
			reachErr: map[string]error{}})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "1 bench")
	})

	t.Run("an inventory that cannot be read is a fail with its verb", func(t *testing.T) {
		t.Parallel()
		r := runReach(t, reachRig{invErr: errors.New("NOVA_SPRINT_REDIS is not set")})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "inventory")
		assert.Contains(t, r.Fix, "nova-config inventory --list")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-ssh-b.w3")
	})

	t.Run("an inventory with no bench but this machine is ok", func(t *testing.T) {
		t.Parallel()
		r := runReach(t, reachRig{benches: []string{"coordinator"}, local: "coordinator"})
		assert.Equal(t, OK, r.Status, r)
		assert.Empty(t, r.Fix)
	})
}
