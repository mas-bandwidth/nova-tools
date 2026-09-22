package presence

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// PasswordEnv is the one environment variable the heartbeat reads its password
// from, and it is the same one `bench-row` hands redis-cli: the password is
// never a flag, never a file this tool opens and never a word in a log. It
// reaches this process through `nova-secrets exec --only
// NOVA_REDIS_BENCH_PASSWORD` and no other way.
const PasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"

// DefaultUser is the ACL user the beat and the line authenticate as. `~friend:*`
// was added to its rules for #2610; it can touch nothing else on the store.
const DefaultUser = "bench"

// Redis is the live store: a Store over one go-redis client.
type Redis struct{ rdb *redis.Client }

// Open dials addr as user, with the password from the environment when there is
// one. A store with no ACL -- a test's miniredis, a local Redis -- is dialled
// with no credentials at all rather than with an empty password, because an
// empty AUTH is a failed login and not an anonymous one.
//
// The address is host:port, the spelling every other verb uses; a `redis://`
// prefix is accepted because people paste one. A bare host is refused rather
// than completed, because the fleet store's port is 6380 and the default is
// 6379, and a tool that guesses that wrong reports a friend as away.
func Open(ctx context.Context, addr, user string) (*Redis, error) {
	a, err := Addr(addr)
	if err != nil {
		return nil, err
	}
	opts := &redis.Options{Addr: a}
	if pw := os.Getenv(PasswordEnv); pw != "" {
		if strings.TrimSpace(user) == "" {
			user = DefaultUser
		}
		opts.Username, opts.Password = user, pw
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		// The error carries the address and the diagnosis, never the
		// credential: redis returns "NOAUTH"/"WRONGPASS" and go-redis
		// does not echo the password back.
		if os.Getenv(PasswordEnv) == "" && isAuthError(err) {
			return nil, fmt.Errorf("store %s: %w; no %s in this environment -- run this under `nova-secrets exec --only %s`", a, err, PasswordEnv, PasswordEnv)
		}
		return nil, fmt.Errorf("store %s: %w", a, err)
	}
	return &Redis{rdb: rdb}, nil
}

// Addr normalizes what a caller spelled into host:port, or says why it cannot.
func Addr(addr string) (string, error) {
	a := strings.TrimSpace(addr)
	a = strings.TrimPrefix(strings.TrimPrefix(a, "redis://"), "rediss://")
	a = strings.TrimSuffix(a, "/")
	if a == "" {
		return "", fmt.Errorf("--store is empty")
	}
	if !strings.Contains(a, ":") {
		return "", fmt.Errorf("--store %s names no port; the fleet store is host:6380", a)
	}
	return a, nil
}

func isAuthError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "NOAUTH") || strings.Contains(s, "WRONGPASS") || strings.Contains(s, "NOPERM")
}

// Close releases the connection pool.
func (r *Redis) Close() error {
	if r == nil || r.rdb == nil {
		return nil
	}
	return r.rdb.Close()
}

// Set is SET key value [EX ttl]; a ttl of 0 writes no expiry.
func (r *Redis) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.rdb.Set(ctx, key, value, ttl).Err()
}

// MGet is one MGET over every key, absent keys coming back as "".
func (r *Redis) MGet(ctx context.Context, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		if s, ok := v.(string); ok {
			out[i] = s
		}
	}
	return out, nil
}
