package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redisRig is the machine the redis check reads: the stores the environment
// names, whether a dial answers, and what `nova-redis acl check` and
// `redis-server --version` print. Nothing real runs and no socket opens.
type redisRig struct {
	env    map[string]string // the environment the check reads
	dial   error             // the dial's answer; nil is reachable
	aclOut string            // `nova-redis acl check` stdout
	aclErr error             // its error (a check that found drift exits 1)
	server string            // `redis-server --version` stdout; "" is not on PATH
}

// runRedis runs only the redis check over the rig.
func runRedis(t *testing.T, r redisRig) Result {
	t.Helper()
	fe := fakeEnv{env: r.env, dial: func(string) error { return r.dial }}
	fe.exec = func(name string, args ...string) (string, error) {
		switch filepath.Base(name) {
		case "nova-redis":
			if len(args) >= 2 && args[0] == "acl" && args[1] == "check" {
				return r.aclOut, r.aclErr
			}
		case "redis-server":
			if r.server != "" {
				return r.server, nil
			}
			return "", errors.New("redis-server: not on PATH")
		}
		return "", errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
	}
	reg := NewRegistry()
	reg.Register(Default.checks["redis"])
	res, _, err := reg.Run(context.Background(), fe, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

// TestDoctorRedisCheckFindsAMissingACLUser pins the redis check: it reads the
// stores the environment names, and a store whose live ACL lacks an expected
// user is a fail naming the user and the `nova-redis acl apply` line that
// fixes it; a store that is right is ok, and a machine that names no store
// needs none (docs/SETUP.md, dep-redis-stores-b.w4).
func TestDoctorRedisCheckFindsAMissingACLUser(t *testing.T) {
	t.Parallel()

	const users = "ACL OK user=coordinator role=coordinator\n" +
		"ACL OK user=bench role=member\n" +
		"ACL OK user=ns-table role=table\n" +
		"ACL OK user=ns-friend role=friend\n" +
		"ACL CHECK OK users=4 library=abc store=127.0.0.1:6390\n"

	t.Run("a missing user is a fail naming it and the fix", func(t *testing.T) {
		t.Parallel()
		r := runRedis(t, redisRig{
			env: map[string]string{"NOVA_REDIS_ADDR": "127.0.0.1:6390"},
			aclOut: "ACL OK user=coordinator role=coordinator\n" +
				"ACL OK user=bench role=member\n" +
				"ACL OK user=ns-table role=table\n" +
				"ACL MISSING user=ns-friend role=friend\n" +
				"ACL CHECK DRIFT users=4 differ=1 library=abc store=127.0.0.1:6390 remedy=nova-redis acl apply\n",
			aclErr: errors.New("exit 1"),
			server: "server v=8.10.2 sha=abc bits=64 build=def\n",
		})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "ns-friend")
		assert.Contains(t, r.Evidence, "missing")
		assert.Contains(t, r.Fix, "nova-redis acl apply")
		assert.Contains(t, r.Fix, "--addr 127.0.0.1:6390")
		assert.Contains(t, r.Fix, "--password-env-for ns-friend=<VARIABLE>")
	})

	t.Run("a different user is a fail naming it", func(t *testing.T) {
		t.Parallel()
		r := runRedis(t, redisRig{
			env: map[string]string{"NOVA_REDIS_ADDR": "127.0.0.1:6390"},
			aclOut: "ACL OK user=coordinator role=coordinator\n" +
				"ACL DRIFT user=bench role=member drift=keys+=~friend:*\n" +
				"ACL CHECK DRIFT users=4 differ=1 library=abc store=127.0.0.1:6390\n",
			aclErr: errors.New("exit 1"),
			server: "server v=8.10.2 sha=abc bits=64 build=def\n",
		})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "bench")
		assert.Contains(t, r.Evidence, "different")
		assert.Contains(t, r.Fix, "nova-redis acl apply --addr 127.0.0.1:6390")
	})

	t.Run("every expected user is ok", func(t *testing.T) {
		t.Parallel()
		r := runRedis(t, redisRig{
			env:    map[string]string{"NOVA_REDIS_ADDR": "127.0.0.1:6390"},
			aclOut: users,
			server: "server v=8.10.2 sha=abc bits=64 build=def\n",
		})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "ns-friend")
		assert.Empty(t, r.Fix)
	})

	t.Run("a machine that names no store needs none", func(t *testing.T) {
		t.Parallel()
		r := runRedis(t, redisRig{})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "no Redis store")
		assert.Empty(t, r.Fix)
	})

	t.Run("an unreachable store is a fail naming nova-up", func(t *testing.T) {
		t.Parallel()
		r := runRedis(t, redisRig{
			env:    map[string]string{"NOVA_REDIS_ADDR": "127.0.0.1:6390"},
			dial:   errors.New("connection refused"),
			server: "server v=8.10.2 sha=abc bits=64 build=def\n",
		})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "127.0.0.1:6390")
		assert.Contains(t, r.Evidence, "not reachable")
		assert.Contains(t, r.Fix, "nova-up")
	})

	t.Run("the bus store is checked beside the sprint store", func(t *testing.T) {
		t.Parallel()
		r := runRedis(t, redisRig{
			env: map[string]string{
				"NOVA_REDIS_ADDR": "127.0.0.1:6390",
				"NOVA_BUS_REDIS":  "127.0.0.1:6391",
			},
			aclOut: "ACL MISSING user=ns-friend role=friend\nACL CHECK DRIFT users=4 differ=1\n",
			aclErr: errors.New("exit 1"),
			server: "server v=8.10.2 sha=abc bits=64 build=def\n",
		})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "bus")
		assert.Contains(t, r.Evidence, "ns-friend")
	})
}

// TestDoctorRedisMajorReadsTheServerVersion pins the version reader: the major
// number of a `redis-server --version` line, and no answer for a line without
// one.
func TestDoctorRedisMajorReadsTheServerVersion(t *testing.T) {
	t.Parallel()

	t.Run("the v= token is read", func(t *testing.T) {
		t.Parallel()
		major, ok := redisMajor("server v=8.10.2 sha=abc bits=64 build=def\n")
		assert.True(t, ok)
		assert.Equal(t, 8, major)
	})

	t.Run("a line with no version answers none", func(t *testing.T) {
		t.Parallel()
		_, ok := redisMajor("no version here\n")
		assert.False(t, ok)
	})
}
