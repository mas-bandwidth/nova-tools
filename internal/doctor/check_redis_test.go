package doctor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redisVersionOK is what the fake redis-server prints: the repository's own
// version, so the class rule that holds every place to one Redis version reads
// it as the same. A lower version is built at run time, never written here.
const redisVersionOK = "Redis server v=8.10.2 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=0"

// redisRun runs only the redis check over env, as the frame would.
func redisRun(t *testing.T, env Env) Result {
	t.Helper()
	r := NewRegistry()
	r.Register(Default.checks["redis"])
	res, _, err := r.Run(context.Background(), env, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

// redisRender is a build's rendering: one pasteable ACL SETUSER line per user
// and the summary.
var redisRender = strings.Join([]string{
	"ACL FAMILY name=tables keys=table:*",
	"ACL SETUSER coordinator on clearselectors resetkeys resetchannels ~*",
	"ACL SETUSER bench on clearselectors resetkeys resetchannels ~table:*",
	"ACL SETUSER ns-table on clearselectors resetkeys resetchannels %R~table:*",
	"ACL SETUSER ns-friend on clearselectors resetkeys resetchannels ~table:*",
	"ACL RENDER OK users=4 functions=12 library=0123456789abcdef",
}, "\n")

// redisGood is a store whose every user is the rendering.
var redisGood = strings.Join([]string{
	"ACL OK user=coordinator role=coordinator",
	"ACL OK user=bench role=member",
	"ACL OK user=ns-table role=table",
	"ACL OK user=ns-friend role=friend",
	"ACL CHECK OK users=4 library=0123456789abcdef store=127.0.0.1:6390",
}, "\n")

// redisBad names one missing user and one that differs, with the tool's own
// remedy line.
var redisBad = strings.Join([]string{
	"ACL OK user=coordinator role=coordinator",
	"ACL DRIFT user=bench role=member keys-=~legacy:*",
	"ACL MISSING user=ns-table role=table",
	"ACL OK user=ns-friend role=friend",
	`ACL CHECK DRIFT users=4 differ=2 library=0123456789abcdef store=127.0.0.1:6390 remedy="nova-redis acl apply --addr 127.0.0.1:6390 --user coordinator --password-env NOVA_REDIS_COORDINATOR_PASSWORD sets the users that differ"`,
}, "\n")

// redisEnv is a fake Env of one Redis store: the address, the login user and
// the password's variable, with the password's value present so a check that
// printed it would show. The exec answers redis-server's version, the build's
// render and the store's check from a table; nothing real runs.
func redisEnv(t *testing.T, vars map[string]string, version, check string, dial error) Env {
	t.Helper()
	return fakeEnv{
		env: vars,
		exec: func(name string, args ...string) (string, error) {
			switch {
			case name == "redis-server":
				return version, nil
			case name == "nova-redis" && len(args) >= 2 && args[0] == "acl" && args[1] == "render":
				return redisRender, nil
			case name == "nova-redis" && len(args) >= 2 && args[0] == "acl" && args[1] == "check":
				assert.NotContains(t, strings.Join(args, " "), vars["NOVA_REDIS_COORDINATOR_PASSWORD"], "a password is never an argument")
				return check, nil
			}
			return "", fmt.Errorf("unexpected exec %s %v", name, args)
		},
		dial: func(string) error { return dial },
	}
}

// redisVars is the coordinator's seat naming one machine's Redis.
func redisVars() map[string]string {
	return map[string]string{
		"NOVA_REDIS_ADDR":                 "127.0.0.1:6390",
		"NOVA_REDIS_USER":                 "coordinator",
		"NOVA_REDIS_PASSWORD_ENV":         "NOVA_REDIS_COORDINATOR_PASSWORD",
		"NOVA_REDIS_COORDINATOR_PASSWORD": "s3cret-value",
	}
}

// TestDoctorRedisCheckFindsAMissingACLUser pins the redis check: it reads this
// build's rendering, asks each Redis store the inventory names for its live
// ACL, and fails naming a user that is missing or different, with the
// `nova-redis acl apply` line that fixes it, never printing a password. A
// store whose every user is the rendering is ok; an unreachable store, no
// store named, and a redis-server below the floor are each reported.
func TestDoctorRedisCheckFindsAMissingACLUser(t *testing.T) {
	t.Parallel()

	t.Run("a missing or different user is a fail naming it", func(t *testing.T) {
		t.Parallel()
		r := redisRun(t, redisEnv(t, redisVars(), redisVersionOK, redisBad, nil))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "ns-table", "the missing user is named")
		assert.Contains(t, r.Evidence, "bench", "the differing user is named")
		assert.Contains(t, r.Fix, "nova-redis acl apply", "the fix is the verb that sets the users")
		assert.Contains(t, r.Fix, "127.0.0.1:6390", "the fix names the store")
		assert.NotContains(t, r.Evidence+r.Fix, "s3cret-value", "a password is never printed")
	})

	t.Run("every user right is ok", func(t *testing.T) {
		t.Parallel()
		r := redisRun(t, redisEnv(t, redisVars(), redisVersionOK, redisGood, nil))
		assert.Equal(t, OK, r.Status, r)
		assert.Empty(t, r.Fix)
		assert.Contains(t, r.Evidence, "ns-table", "the evidence names the users checked")
	})

	t.Run("an unreachable store is a fail", func(t *testing.T) {
		t.Parallel()
		r := redisRun(t, redisEnv(t, redisVars(), redisVersionOK, redisGood, fmt.Errorf("connection refused")))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "unreachable")
		assert.NotEmpty(t, r.Fix)
	})

	t.Run("no store named is a warn with the setup step", func(t *testing.T) {
		t.Parallel()
		r := redisRun(t, redisEnv(t, map[string]string{}, redisVersionOK, redisGood, nil))
		assert.Equal(t, Warn, r.Status, r)
		assert.NotEmpty(t, r.Fix)
	})

	t.Run("a redis-server below the floor is a fail", func(t *testing.T) {
		t.Parallel()
		old := "Redis server v=" + strings.Join([]string{"6", "2", "0"}, ".") + " sha=0 malloc=jemalloc bits=64 build=0"
		r := redisRun(t, redisEnv(t, redisVars(), old, redisGood, nil))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "older")
		assert.NotEmpty(t, r.Fix)
	})
}
