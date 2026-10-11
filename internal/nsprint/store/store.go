// Package store provides the Redis client shared by nova-sprint verbs.
package store

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

// Store owns a Redis connection. Mutating verbs use FCALL through Client;
// batches of independent reads use PipelineHMGet.
type Store struct {
	client *redis.Client
}

// Fleet authentication. The fleet Redis (users.acl) has its
// default user off, so an unauthenticated verb fails NOAUTH. The password is
// never a flag: `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD` leaves it
// in the environment, where a ps cannot read it (the nova-pulse convention).
// UserEnv names the ACL user and turns authentication on; PasswordEnvEnv names
// the variable holding that user's password, DefaultPasswordEnv when unset.
// With UserEnv unset, Open connects as before, so a throwaway test Redis and a
// bench that exports the bench password for other tools are unaffected; only
// when that unauthenticated connection is refused NOAUTH while the password is
// already in the environment does the refusal name the missing variable and
// the pair instead of passing the raw NOAUTH through. Open sends
// nothing, so that refusal is the first command's error.
const (
	UserEnv            = redisauth.UserEnv
	PasswordEnvEnv     = redisauth.PasswordEnvEnv
	DefaultPasswordEnv = redisauth.DefaultPasswordEnv
)

func authFromEnv(sel *seatcred.Selection) (user, password string, err error) {
	// A seat given by --seat or NOVA_SEAT is read through
	// nova-secrets' library in this process: its login wins, and the password
	// goes to the client in memory, never into this process's environment.
	if c, ok, err := sel.Active(); ok {
		if err != nil {
			return "", "", err
		}
		// ignored: Use fails only when its function is nil or fails, and this one does neither
		_ = c.Password.Use(func(pw string) error { password = pw; return nil })
		return c.User, password, nil
	}
	return Auth("", "")
}

// Auth is the environment seat (pkg/nsprint/redisauth): user is the
// ACL user, else UserEnv; passwordEnv the variable holding its password, else
// PasswordEnvEnv, else DefaultPasswordEnv.
func Auth(user, passwordEnv string) (string, string, error) { return redisauth.Auth(user, passwordEnv) }

// NoUserHint is the NOAUTH refusal: password in the environment, ACL user unset.
func NoUserHint() string { return redisauth.NoUserHint() }

// noUserHook adds NoUserHint to a NOAUTH refusal. Open sends nothing
// so the refusal arrives on the caller's first command or batch; the
// hook costs no round trip and is installed only when the user is unset while
// the bench password is in the environment.
type noUserHook struct{ addr string }

func (h noUserHook) wrap(err error) error {
	return fmt.Errorf("redis at %s: %w; %s", h.addr, err, NoUserHint())
}

func (noUserHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h noUserHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if isNoAuth(err) {
			err = h.wrap(err)
			cmd.SetErr(err)
		}
		return err
	}
}

func (h noUserHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		for _, cmd := range cmds {
			if isNoAuth(cmd.Err()) {
				cmd.SetErr(h.wrap(cmd.Err()))
			}
		}
		if isNoAuth(err) {
			err = h.wrap(err)
		}
		return err
	}
}

// isNoAuth reports a refusal for lack of authentication.
func isNoAuth(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOAUTH")
}

func Open(ctx context.Context, addr string) (*Store, error) {
	return open(ctx, addr, 0, seatcred.Process())
}

// open dials as sel's seat, else the environment's. With no addr it dials the
// address sel's seat profile row names.
func open(ctx context.Context, addr string, poolSize int, sel *seatcred.Selection) (*Store, error) {
	return openWith(ctx, addr, sel, func(o *redis.Options) { o.PoolSize = poolSize })
}

func openWith(ctx context.Context, addr string, sel *seatcred.Selection, tune func(*redis.Options)) (*Store, error) {
	if addr == "" {
		addr = sel.Addr()
	}
	if addr == "" {
		return nil, fmt.Errorf("redis address is required")
	}
	user, password, err := authFromEnv(sel)
	if err != nil {
		return nil, err
	}
	// No PING: go-redis dials on the first command, so the caller's
	// first pipeline is the probe and an unreachable store fails there.
	// No CLIENT SETINFO either: Redis commands must be batched, and
	// go-redis sends the library name and version in a
	// round trip of its own after HELLO, and the store is 128 ms away, so
	// every one-shot verb paid it for nothing. No CLIENT MAINT_NOTIFICATIONS
	// either: go-redis v9.22.0 sends it after HELLO 3 unless told not to,
	// and Redis 8.10.2 refuses it (errorstat_ERR), a second trip for
	// nothing; the same setting as redisconn.Open. The connect is HELLO alone.
	opts := &redis.Options{Addr: addr, Username: user, Password: password, DisableIdentity: true,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode:         maintnotifications.ModeDisabled,
			EndpointType: maintnotifications.EndpointTypeNone,
		},
	}
	tune(opts)
	client := redis.NewClient(opts)
	if user == "" && os.Getenv(DefaultPasswordEnv) != "" {
		client.AddHook(noUserHook{addr: addr})
	}
	return &Store{client: client}, nil
}

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
