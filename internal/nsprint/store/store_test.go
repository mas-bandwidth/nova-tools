//go:build functional

package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

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
func TestOpenAuthenticatesFromEnv(t *testing.T) {
	addr := startRedis(t, "--user", "default", "off", "--user", "bench", "on", ">bench-secret", "~*", "&*", "+@all")
	ctx := context.Background()

	// An inherited NOVA_SPRINT_REDIS_PASSWORD_ENV would redirect the default path
	// below to another seat's variable; clear it so the test is deterministic.
	t.Setenv(store.PasswordEnvEnv, "")
	t.Setenv(store.UserEnv, "")
	t.Setenv(store.DefaultPasswordEnv, "bench-secret")
	first := func() error {
		st, err := store.Open(ctx, addr)
		if err != nil {
			return err
		}
		defer st.Close()
		return st.Client().Get(ctx, "auth:probe").Err()
	}
	// #3520 DONE-WHEN: a password without a user refuses with the line naming
	// the missing variable and the pair (user + password), not a bare NOAUTH.
	if err := first(); err == nil ||
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
	if err := pipe(); err == nil || !strings.Contains(err.Error(), "NOAUTH") || !strings.Contains(err.Error(), store.UserEnv+" is unset") {
		require.Failf(t, "assertion failed", "first batch with %s but no %s = %v; want NOAUTH and the named refusal", store.DefaultPasswordEnv, store.UserEnv, err)
	}

	t.Setenv(store.UserEnv, "bench")
	st, err := store.Open(ctx, addr)
	if err != nil {
		require.NoError(t, err, "Open as bench with %s: %v", store.DefaultPasswordEnv, err)
	}
	if err := st.Client().Set(ctx, "auth:probe", "1", 0).Err(); err != nil {
		require.NoError(t, err, "authenticated write: %v", err)
	}
	_ = st.Close()

	t.Setenv(store.PasswordEnvEnv, "NOVA_REDIS_OTHER_SEAT")
	t.Setenv("NOVA_REDIS_OTHER_SEAT", "")
	if _, err := store.Open(ctx, addr); err == nil || !strings.Contains(err.Error(), "NOVA_REDIS_OTHER_SEAT is empty") {
		require.Failf(t, "assertion failed", "Open with an empty named password variable = %v; want a refusal naming it", err)
	}
	t.Setenv("NOVA_REDIS_OTHER_SEAT", "wrong")
	if err := first(); err == nil || !strings.Contains(err.Error(), "WRONGPASS") {
		require.Failf(t, "assertion failed", "first command with the wrong password = %v; want WRONGPASS", err)
	}
}
