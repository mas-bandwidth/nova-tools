// Package store provides the Redis client shared by nova-sprint verbs.
package store

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Store owns a Redis connection. Mutating verbs use FCALL through Client;
// batches of independent reads use PipelineHMGet.
type Store struct {
	client *redis.Client
}

// Fleet authentication. The fleet Redis (space:6380, users.acl) has its
// default user off, so an unauthenticated verb fails NOAUTH. The password is
// never a flag: `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD` leaves it
// in the environment, where a ps cannot read it (the nova-pulse convention).
// UserEnv names the ACL user and turns authentication on; PasswordEnvEnv names
// the variable holding that user's password, DefaultPasswordEnv when unset.
// With UserEnv unset, Open connects as before, so a throwaway test Redis and a
// bench that exports the bench password for other tools are unaffected; only
// when that unauthenticated connection is refused NOAUTH while the password is
// already in the environment does Open name the missing variable and the pair
// (#3520) instead of passing the raw NOAUTH through.
const (
	UserEnv            = "NOVA_SPRINT_REDIS_USER"
	PasswordEnvEnv     = "NOVA_SPRINT_REDIS_PASSWORD_ENV"
	DefaultPasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"
)

func authFromEnv() (user, password string, err error) {
	user = os.Getenv(UserEnv)
	if user == "" {
		return "", "", nil
	}
	name := os.Getenv(PasswordEnvEnv)
	if name == "" {
		name = DefaultPasswordEnv
	}
	password = os.Getenv(name)
	if password == "" {
		return "", "", fmt.Errorf("%s=%s but %s is empty; run under nova-secrets exec --only %s", UserEnv, user, name, name)
	}
	return user, password, nil
}

// NoUserHint is the refusal the fleet Redis needs when the default user is off
// (#3520): the password is already in the environment but the ACL user is
// unset, so the verb connected as the default user and was refused NOAUTH. The
// line names the missing variable and the pair (user + password) in one line.
func NoUserHint() string {
	return UserEnv + " is unset but " + DefaultPasswordEnv + " is set; set the pair " +
		UserEnv + "=bench and " + DefaultPasswordEnv + " (password from nova-secrets exec --only " +
		DefaultPasswordEnv + ", never a flag)"
}

// isNoAuth reports a refusal for lack of authentication.
func isNoAuth(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOAUTH")
}

func Open(ctx context.Context, addr string) (*Store, error) {
	return open(ctx, addr, 0)
}

// OpenSingle is Open with a pool of exactly one connection, for a long-lived
// loop such as `nova-sprint bench beat` (#3372): every tick rides the one
// authenticated connection, and a broken one is redialed by the same client
// on the next command.
func OpenSingle(ctx context.Context, addr string) (*Store, error) {
	return open(ctx, addr, 1)
}

func open(ctx context.Context, addr string, poolSize int) (*Store, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis address is required")
	}
	user, password, err := authFromEnv()
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: password, PoolSize: poolSize})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		if user == "" && isNoAuth(err) && os.Getenv(DefaultPasswordEnv) != "" {
			return nil, fmt.Errorf("redis at %s: %w; %s", addr, err, NoUserHint())
		}
		return nil, fmt.Errorf("redis at %s: %w", addr, err)
	}
	return &Store{client: client}, nil
}

func New(client *redis.Client) *Store { return &Store{client: client} }

func (s *Store) Client() *redis.Client { return s.client }

func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// HashRead names one HMGET without issuing it yet.
type HashRead struct {
	Key    string
	Fields []string
}

// PipelineHMGet queues every read before Exec. Redis receives the full batch
// before reading any reply and returns replies in the same order. The buffered
// transport may split the batch into multiple writes, but it uses one exchange.
func (s *Store) PipelineHMGet(ctx context.Context, reads []HashRead) ([][]any, error) {
	if len(reads) == 0 {
		return [][]any{}, nil
	}
	pipe := s.client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(reads))
	for i, read := range reads {
		if read.Key == "" || len(read.Fields) == 0 {
			return nil, fmt.Errorf("read %d needs a key and fields", i)
		}
		cmds[i] = pipe.HMGet(ctx, read.Key, read.Fields...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("pipeline HMGET: %w", err)
	}
	out := make([][]any, len(cmds))
	for i, cmd := range cmds {
		values, err := cmd.Result()
		if err != nil {
			return nil, fmt.Errorf("HMGET %d: %w", i, err)
		}
		out[i] = values
	}
	return out, nil
}
