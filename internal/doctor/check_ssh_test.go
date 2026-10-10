package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sshRig is the fake world one ssh check runs against: the machines the
// `nova-config machine list` inventory holds, and the benches whose probe
// fails with ssh's own words. No socket is opened and no clock is read.
type sshRig struct {
	benches []string          // the machine names the inventory holds
	fail    map[string]string // bench -> what ssh prints when it cannot reach it
	invErr  error             // the inventory read's own failure, if any
	calls   *[][]string       // when set, every ssh argv is recorded here
}

// runSSH runs only the ssh check over the rig.
func runSSH(t *testing.T, r sshRig) Result {
	t.Helper()
	env := fakeEnv{
		env: map[string]string{"NOVA_SECRETS_SEAT": "coordinator"},
		exec: func(name string, args ...string) (string, error) {
			switch name {
			case "nova-config":
				if r.invErr != nil {
					return "", r.invErr
				}
				var b strings.Builder
				for _, m := range r.benches {
					fmt.Fprintf(&b, "MACHINE name=%s user=nova seat=%s slots=4 runners=0 width=- tla=false note=- os=- arch=- cores=4 memory_gb=- beat=none\n", m, m)
				}
				fmt.Fprintf(&b, "CONFIG LIST kind=machine rows=%d\n", len(r.benches))
				return b.String(), nil
			case "ssh":
				if r.calls != nil {
					*r.calls = append(*r.calls, append([]string(nil), args...))
				}
				bench := args[len(args)-2] // -o BatchMode=yes -o ConnectTimeout=5 -- <bench> true
				if reason, ok := r.fail[bench]; ok {
					return "", errors.New("ssh: " + reason)
				}
				return "", nil
			}
			return "", errors.New("unexpected command: " + name)
		},
	}
	reg := NewRegistry()
	reg.Register(Default.checks["ssh"])
	res, _, err := reg.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

// TestDoctorSSHCheckNamesTheBenchItCannotReach pins the card: the ssh check
// reads the inventory, probes each bench with
// `ssh -o BatchMode=yes -o ConnectTimeout=5 <bench> true`, passes when every
// bench answers, and on a failure names the bench and the reason (unknown host
// key, no key, timeout) with one fix line naming the documented step.
func TestDoctorSSHCheckNamesTheBenchItCannotReach(t *testing.T) {
	t.Parallel()

	t.Run("every bench answering is ok", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{benches: []string{"bench1", "bench2"}})
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, "2 bench")
		assert.Empty(t, r.Fix)
	})

	t.Run("one bench times out and is named with the reason", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{
			benches: []string{"bench1", "bench2"},
			fail:    map[string]string{"bench2": "connect to host bench2 port 22: Connection timed out"},
		})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "bench2")
		assert.Contains(t, r.Evidence, "timeout")
		assert.NotContains(t, r.Evidence, "bench1", "a bench that answered is not named")
		assert.Contains(t, r.Fix, "known_hosts")
		assert.Contains(t, r.Fix, "dep-ssh-b.w8")
	})

	t.Run("an unknown host key is named", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{
			benches: []string{"bench1"},
			fail:    map[string]string{"bench1": "Host key verification failed."},
		})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "bench1")
		assert.Contains(t, r.Evidence, "unknown host key")
	})

	t.Run("a missing key is named", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{
			benches: []string{"bench1"},
			fail:    map[string]string{"bench1": "Permission denied (publickey)."},
		})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "bench1")
		assert.Contains(t, r.Evidence, "no key")
	})

	t.Run("an inventory with no benches is ok", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{})
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, "no benches")
	})

	t.Run("a bad machine name is a finding and no ssh is run", func(t *testing.T) {
		t.Parallel()
		var calls [][]string
		r := runSSH(t, sshRig{benches: []string{"-oProxyCommand=touch"}, calls: &calls})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "-oProxyCommand=touch")
		assert.Contains(t, r.Evidence, "is not a host name")
		assert.Empty(t, calls, "an unvalidated name is never passed to ssh")
	})

	t.Run("the ssh argv puts -- before the host", func(t *testing.T) {
		t.Parallel()
		var calls [][]string
		r := runSSH(t, sshRig{benches: []string{"bench1"}, calls: &calls})
		assert.Equal(t, OK, r.Status)
		require.Len(t, calls, 1)
		assert.Equal(t, []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "--", "bench1", "true"}, calls[0])
	})

	t.Run("an unreadable inventory is a fail naming the seat", func(t *testing.T) {
		t.Parallel()
		r := runSSH(t, sshRig{invErr: errors.New("nova-config machine list: no store")})
		assert.Equal(t, Fail, r.Status)
		assert.Contains(t, r.Evidence, "inventory could not be read")
		assert.Contains(t, r.Fix, "seat")
	})
}
