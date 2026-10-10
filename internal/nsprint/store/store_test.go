//go:build functional

package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// storeChildModeEnv picks which behavior a re-entered scenario test asserts,
// one child per behavior instead of one process changing its environment.
const storeChildModeEnv = "NOVA_NSPRINT_STORE_TEST_MODE"

func startRedis(t *testing.T, extra ...string) string {
	t.Helper()
	return testutil.Start(t, extra...)
}

func TestFunctionLibraryLoadsFromFiles(t *testing.T) {
	t.Parallel()

	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()
	source, err := fn.Source()
	if err != nil {
		require.NoError(t, err, err)
	}
	if !strings.HasPrefix(source, "#!lua name=nova_sprint\n") ||
		!strings.Contains(source, "redis.register_function('ns_ping'") ||
		!strings.Contains(source, "redis.register_function('ns_oset_move'") {
		require.Failf(t, "assertion failed", "library did not concatenate embedded verb files: %q", source)
	}
	if err := fn.Load(ctx, client); err != nil {
		require.NoError(t, err, err)
	}
	result, err := client.FCall(ctx, "ns_ping", []string{}).Text()
	if err != nil || result != "PONG" {
		require.Failf(t, "assertion failed", "loaded ns_ping = %q, %v; want PONG", result, err)
	}
	// An updated binary may load the same library again without a gap.
	if err := fn.Load(ctx, client); err != nil {
		require.NoError(t, err, "reload: %v", err)
	}
}

// TestOpenAuthenticatesFromEnv is the fleet shape (users.acl on space:6380):
// the default user is off, so an unauthenticated Open fails NOAUTH, and every
// nova-sprint verb failed that way on 2026-09-23 (adoption receipt on #3009).
// The password comes from the environment nova-secrets exec leaves it in,
// never from a flag. Open sends nothing (#3277), so a refused login is the
// first command's error.
//
// The four environments the behaviors need are the child's cmd.Env, the
// serial allowlist's per-test seam: each scenario re-enters this test in a
// child whose whole environment is the scenario's list, so no t.Setenv
// mutates this process beside the other tests.
func TestOpenAuthenticatesFromEnv(t *testing.T) {
	t.Parallel()
	if inStoreTestChild(t) {
		addr := os.Getenv(storeChildAddrEnv)
		ctx := context.Background()
		first := func() error {
			st, err := store.Open(ctx, addr)
			if err != nil {
				return err
			}
			defer st.Close()
			return st.Client().Get(ctx, "auth:probe").Err()
		}
		switch os.Getenv(storeChildModeEnv) {
		case "missing-user":
			// #3520 DONE-WHEN: a password without a user refuses with the line naming
			// the missing variable and the pair (user + password), not a bare NOAUTH.
			err := first()
			if err == nil ||
				!strings.Contains(err.Error(), "NOAUTH") ||
				!strings.Contains(err.Error(), store.UserEnv+" is unset") ||
				!strings.Contains(err.Error(), store.UserEnv+"=bench and "+store.DefaultPasswordEnv) {
				require.Failf(t, "assertion failed", "first command with %s but no %s = %v; want NOAUTH and a refusal naming the missing variable and the pair", store.DefaultPasswordEnv, store.UserEnv, err)
			}
			pipe := func() error {
				st, err := store.Open(ctx, addr)
				if err != nil {
					return err
				}
				defer st.Close()
				_, err = st.PipelineHMGet(ctx, []store.HashRead{{Key: "auth:probe", Fields: []string{"f"}}})
				return err
			}
			err = pipe()
			if err == nil || !strings.Contains(err.Error(), "NOAUTH") || !strings.Contains(err.Error(), store.UserEnv+" is unset") {
				require.Failf(t, "assertion failed", "first batch with %s but no %s = %v; want NOAUTH and the named refusal", store.DefaultPasswordEnv, store.UserEnv, err)
			}
		case "valid-user":
			st, err := store.Open(ctx, addr)
			require.NoError(t, err, "Open as bench with %s: %v", store.DefaultPasswordEnv, err)
			require.NoError(t, st.Client().Set(ctx, "auth:probe", "1", 0).Err(), "authenticated write")
			require.NoError(t, st.Close())
		case "empty-named-password":
			if _, err := store.Open(ctx, addr); err == nil || !strings.Contains(err.Error(), "NOVA_REDIS_OTHER_SEAT is empty") {
				require.Failf(t, "assertion failed", "Open with an empty named password variable = %v; want a refusal naming it", err)
			}
		case "wrong-password":
			if err := first(); err == nil || !strings.Contains(err.Error(), "WRONGPASS") {
				require.Failf(t, "assertion failed", "first command with the wrong password = %v; want WRONGPASS", err)
			}
		default:
			require.Failf(t, "assertion failed", "unknown store test child mode %q", os.Getenv(storeChildModeEnv))
		}
		return
	}

	addr := startRedis(t, "--user", "default", "off", "--user", "bench", "on", ">bench-secret", "~*", "&*", "+@all")
	for _, scenario := range []struct {
		mode string
		env  []string
	}{
		// An inherited NOVA_SPRINT_REDIS_PASSWORD_ENV would redirect the default path
		// below to another seat's variable; the scenarios clear it so each is
		// deterministic.
		{"missing-user", []string{store.PasswordEnvEnv + "=", store.UserEnv + "=", store.DefaultPasswordEnv + "=bench-secret"}},
		{"valid-user", []string{store.PasswordEnvEnv + "=", store.UserEnv + "=bench", store.DefaultPasswordEnv + "=bench-secret"}},
		{"empty-named-password", []string{store.PasswordEnvEnv + "=NOVA_REDIS_OTHER_SEAT", store.UserEnv + "=bench", store.DefaultPasswordEnv + "=bench-secret", "NOVA_REDIS_OTHER_SEAT="}},
		{"wrong-password", []string{store.PasswordEnvEnv + "=NOVA_REDIS_OTHER_SEAT", store.UserEnv + "=bench", store.DefaultPasswordEnv + "=bench-secret", "NOVA_REDIS_OTHER_SEAT=wrong"}},
	} {
		runStoreTestChild(t, append([]string{storeChildAddrEnv + "=" + addr, storeChildModeEnv + "=" + scenario.mode}, scenario.env...)...)
	}
}
