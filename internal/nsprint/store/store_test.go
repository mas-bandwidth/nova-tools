//go:build functional

package store_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync/atomic"
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

type countedConn struct {
	net.Conn
	writes      *atomic.Int64
	reads       *atomic.Int64
	interleaved *atomic.Int64
}

func (c countedConn) Write(p []byte) (int, error) {
	if c.reads.Load() != 0 {
		c.interleaved.Add(1)
	}
	c.writes.Add(1)
	return c.Conn.Write(p)
}

func (c countedConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.Conn.Read(p)
}

func TestPipelineThousandReadsOneRoundTrip(t *testing.T) {
	t.Parallel()

	addr := startRedis(t)
	var writes atomic.Int64
	var readCalls atomic.Int64
	var interleaved atomic.Int64
	client := redis.NewClient(&redis.Options{
		Addr: addr, PoolSize: 1,
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return countedConn{Conn: conn, writes: &writes, reads: &readCalls, interleaved: &interleaved}, nil
		},
	})
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		require.NoError(t, err, err)
	}
	seed := client.Pipeline()
	reads := make([]store.HashRead, 1000)
	for i := range reads {
		key := fmt.Sprintf("task:%d", i)
		seed.HSet(ctx, key, "state", "open", "owner", "stella")
		reads[i] = store.HashRead{Key: key, Fields: []string{"state", "owner"}}
	}
	if _, err := seed.Exec(ctx); err != nil {
		require.NoError(t, err, err)
	}
	writes.Store(0)
	readCalls.Store(0)
	interleaved.Store(0)
	got, err := store.New(client).PipelineHMGet(ctx, reads)
	if err != nil {
		require.NoError(t, err, err)
	}
	if writes.Load() == 0 || readCalls.Load() == 0 || interleaved.Load() != 0 {
		require.Failf(t, "assertion failed", "1000 HMGETs used %d writes, %d reads, %d writes after a reply; want one pipelined exchange", writes.Load(), readCalls.Load(), interleaved.Load())
	}
	if len(got) != len(reads) {
		require.Equal(t, len(reads), len(got), "got %d replies; want 1000", len(got))
	}
	for i, values := range got {
		if len(values) != 2 || values[0] != "open" || values[1] != "stella" {
			require.Failf(t, "assertion failed", "reply %d = %v", i, values)
		}
	}
}

// TestOpenAuthenticatesFromEnv is the fleet shape (users.acl on space:6380):
// the default user is off, so an unauthenticated Open fails NOAUTH, and every
// nova-sprint verb failed that way on 2026-09-23 (adoption receipt on #3009).
// The password comes from the environment nova-secrets exec leaves it in,
// never from a flag. Open sends nothing (#3277), so a refused login is the
// first command's error.
func TestOpenAuthenticatesFromEnv(t *testing.T) {
	t.Parallel()
	if inStoreTestChild(t) {
		addr := os.Getenv("NOVA_NSPRINT_STORE_TEST_ADDR")
		ctx := context.Background()
		switch os.Getenv("NOVA_NSPRINT_STORE_TEST_MODE") {
		case "missing-user":
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
			err := first()
			require.Error(t, err)
			require.Contains(t, err.Error(), "NOAUTH")
			require.Contains(t, err.Error(), store.UserEnv+" is unset")
			require.Contains(t, err.Error(), store.UserEnv+"=bench and "+store.DefaultPasswordEnv)
			st, err := store.Open(ctx, addr)
			require.NoError(t, err)
			defer st.Close()
			_, err = st.PipelineHMGet(ctx, []store.HashRead{{Key: "auth:probe", Fields: []string{"f"}}})
			require.Error(t, err)
			require.Contains(t, err.Error(), "NOAUTH")
			require.Contains(t, err.Error(), store.UserEnv+" is unset")
		case "valid-user":
			st, err := store.Open(ctx, addr)
			require.NoError(t, err)
			defer st.Close()
			require.NoError(t, st.Client().Set(ctx, "auth:probe", "1", 0).Err())
		case "empty-named-password":
			_, err := store.Open(ctx, addr)
			require.Error(t, err)
			require.Contains(t, err.Error(), "NOVA_REDIS_OTHER_SEAT is empty")
		case "wrong-password":
			st, err := store.Open(ctx, addr)
			require.NoError(t, err)
			defer st.Close()
			err = st.Client().Get(ctx, "auth:probe").Err()
			require.Error(t, err)
			require.Contains(t, err.Error(), "WRONGPASS")
		default:
			require.Fail(t, "unknown store test child mode")
		}
		return
	}

	addr := startRedis(t, "--user", "default", "off", "--user", "bench", "on", ">bench-secret", "~*", "&*", "+@all")
	baseEnv := []string{"NOVA_NSPRINT_STORE_TEST_ADDR=" + addr}
	for _, scenario := range []struct {
		mode string
		env  []string
	}{
		{"missing-user", []string{store.PasswordEnvEnv + "=", store.UserEnv + "=", store.DefaultPasswordEnv + "=bench-secret"}},
		{"valid-user", []string{store.PasswordEnvEnv + "=", store.UserEnv + "=bench", store.DefaultPasswordEnv + "=bench-secret"}},
		{"empty-named-password", []string{store.PasswordEnvEnv + "=NOVA_REDIS_OTHER_SEAT", store.UserEnv + "=bench", "NOVA_REDIS_OTHER_SEAT="}},
		{"wrong-password", []string{store.PasswordEnvEnv + "=NOVA_REDIS_OTHER_SEAT", store.UserEnv + "=bench", "NOVA_REDIS_OTHER_SEAT=wrong"}},
	} {
		env := append(append([]string{}, baseEnv...), "NOVA_NSPRINT_STORE_TEST_MODE="+scenario.mode)
		runStoreTestChild(t, append(env, scenario.env...)...)
	}
}
