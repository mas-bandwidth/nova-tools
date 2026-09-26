// Package store provides the Redis client shared by nova-sprint verbs.
package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
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
// already in the environment does the refusal name the missing variable and
// the pair (#3520) instead of passing the raw NOAUTH through. Open sends
// nothing (#3277), so that refusal is the first command's error.
const (
	UserEnv            = redisauth.UserEnv
	PasswordEnvEnv     = redisauth.PasswordEnvEnv
	DefaultPasswordEnv = redisauth.DefaultPasswordEnv
)

func authFromEnv(sel *seatcred.Selection) (user, password string, err error) {
	// A seat given by --seat or NOVA_SEAT (nova-tools#4052) is read through
	// nova-secrets' library in this process: its login wins, and the password
	// goes to the client in memory, never into this process's environment.
	if c, ok, err := sel.Active(); ok {
		if err != nil {
			return "", "", err
		}
		_ = c.Password.Use(func(pw string) error { password = pw; return nil })
		return c.User, password, nil
	}
	return Auth("", "")
}

// Auth is the environment seat (internal/nsprint/redisauth, #3461): user is the
// ACL user, else UserEnv; passwordEnv the variable holding its password, else
// PasswordEnvEnv, else DefaultPasswordEnv.
func Auth(user, passwordEnv string) (string, string, error) { return redisauth.Auth(user, passwordEnv) }

// NoUserHint is the #3520 refusal: password in the environment, ACL user unset.
func NoUserHint() string { return redisauth.NoUserHint() }

// noUserHook adds NoUserHint to a NOAUTH refusal (#3520). Open sends nothing
// (#3277), so the refusal arrives on the caller's first command or batch; the
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

// noRetryWaits is set by NoRetryWaits and never cleared.
var noRetryWaits atomic.Bool

// NoRetryWaits makes every client Open and OpenSingle build from now on retry a
// refused dial or command WITHOUT WAITING between attempts. It is for test
// binaries: go-redis waits 100 ms between each of five dial attempts and backs
// off between four command attempts, so a verb pointed at a closed port spent
// 1.7 s of wall clock before it could refuse, and a test asserting that refusal
// waited it out (nova-tools#4328, Glenn 2026-09-26: unit tests under 2 s and
// never waiting on the wall clock). The number of attempts and the error are
// unchanged. Production never calls it.
func NoRetryWaits() { noRetryWaits.Store(true) }

func Open(ctx context.Context, addr string) (*Store, error) {
	return open(ctx, addr, 0, seatcred.Process())
}

// OpenSeat is Open as sel's seat instead of this process's (nova-tools#4330):
// a caller holding its own seatcred.Selection, such as a parallel test.
func OpenSeat(ctx context.Context, addr string, sel *seatcred.Selection) (*Store, error) {
	return open(ctx, addr, 0, sel)
}

// OpenSingle is Open with a pool of exactly one connection, for a long-lived
// loop such as `nova-sprint bench beat` (#3372): every tick rides the one
// authenticated connection, and a broken one is redialed by the same client
// on the next command.
func OpenSingle(ctx context.Context, addr string) (*Store, error) {
	return open(ctx, addr, 1, seatcred.Process())
}

// OpenProbe is Open for a one-shot health read (`nova-sprint doctor`): one
// connection, one dial attempt bounded by a second, and no command retries, so
// a store that is down or refuses the login answers on the first pipeline in
// well under a second instead of after go-redis's five dials and three
// retries.
func OpenProbe(ctx context.Context, addr string) (*Store, error) {
	return openWith(ctx, addr, seatcred.Process(), func(o *redis.Options) {
		o.PoolSize, o.MaxRetries, o.DialerRetries, o.DialTimeout = 1, -1, 1, time.Second
	})
}

// open dials as sel's seat, else the environment's. With no addr it dials the
// address sel's seat profile row names (nova-tools#4330).
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
	// No PING (#3277): go-redis dials on the first command, so the caller's
	// first pipeline is the probe and an unreachable store fails there.
	opts := &redis.Options{Addr: addr, Username: user, Password: password}
	tune(opts)
	if noRetryWaits.Load() {
		// The attempts are go-redis's own; only the waits between them go.
		opts.MinRetryBackoff, opts.MaxRetryBackoff = -1, -1
		opts.DialerRetryBackoff = func(int) time.Duration { return 0 }
	}
	client := redis.NewClient(opts)
	if user == "" && os.Getenv(DefaultPasswordEnv) != "" {
		client.AddHook(noUserHook{addr: addr})
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
