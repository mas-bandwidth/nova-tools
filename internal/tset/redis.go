package tset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// RedisStore owns a standalone Redis client. Construction neither contacts a
// server nor loads a Function library.
type RedisStore struct {
	client redis.UniversalClient
	close  func() error
	closed atomic.Bool
}

// RedisOption is sealed so callers can select supported connection metadata
// without supplying raw go-redis options, hooks, or a client.
type RedisOption interface {
	applyRedisOption(*redis.Options)
}

type redisOptionFunc func(*redis.Options)

func (option redisOptionFunc) applyRedisOption(options *redis.Options) { option(options) }

// WithClientName sets Redis's CLIENT SETNAME metadata for operational
// correlation. It cannot change retry behavior or install hooks.
func WithClientName(name string) RedisOption {
	return redisOptionFunc(func(options *redis.Options) { options.ClientName = name })
}

// NewRedis creates and owns a standalone go-redis client. The password is
// obtained from passwordEnvVar so callers never pass or retain the secret as
// a constructor argument. Pass an empty passwordEnvVar for an unauthenticated
// server. The returned store must be closed by its owner.
func NewRedis(address, username, passwordEnvVar string, options ...RedisOption) (*RedisStore, error) {
	client, err := newOwnedRedisClient(address, username, passwordEnvVar, options...)
	if err != nil {
		return nil, err
	}
	return &RedisStore{client: client, close: client.Close}, nil
}

// newRedisWithClient is limited to package-local tests. Production callers
// cannot inject a client whose hooks or retry behavior tset cannot control.
func newRedisWithClient(client redis.UniversalClient) *RedisStore {
	return &RedisStore{client: client}
}

func newOwnedRedisClient(address, username, passwordEnvVar string, redisOptions ...RedisOption) (*redis.Client, error) {
	return newOwnedRedisClientWithLookup(address, username, passwordEnvVar, os.LookupEnv, redisOptions...)
}

func redisPasswordFromEnv(name string, lookup func(string) (string, bool)) (string, error) {
	if name == "" {
		return "", nil
	}
	value, ok := lookup(name)
	if !ok {
		return "", fmt.Errorf("tset: password environment variable %q is not set", name)
	}
	return value, nil
}

func newOwnedRedisClientWithLookup(address, username, passwordEnvVar string, lookup func(string) (string, bool), redisOptions ...RedisOption) (*redis.Client, error) {
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("tset: Redis address is required")
	}
	password, err := redisPasswordFromEnv(passwordEnvVar, lookup)
	if err != nil {
		return nil, err
	}
	// go-redis v9 uses -1 as the input sentinel for effective MaxRetries=0.
	// A zero input selects its default retry count (currently 3).
	options := &redis.Options{
		Addr:       address,
		Username:   username,
		Password:   password,
		MaxRetries: -1,
	}
	for _, option := range redisOptions {
		if option != nil {
			option.applyRedisOption(options)
		}
	}
	client := redis.NewClient(options)
	return client, nil
}

// Close releases the internally owned go-redis client. The package-private
// test seam has no owned client and therefore Close is a no-op there.
func (r *RedisStore) Close() error {
	if r == nil || r.close == nil {
		return nil
	}
	r.closed.Store(true)
	return r.close()
}

type ClientError struct {
	Code  string
	Cause error
}

func (e *ClientError) Error() string {
	if e.Cause == nil {
		return "tset: " + e.Code
	}
	return "tset: " + e.Code + ": " + e.Cause.Error()
}

func (e *ClientError) Unwrap() error { return e.Cause }

// OutcomeUnknownError retains the exact encoded request for a done lookup or
// transport retry with the same bytes. Error text never includes request data.
type OutcomeUnknownError struct {
	RawRequest []byte
	Cause      error
}

func (e *OutcomeUnknownError) Error() string {
	if e.Cause == nil {
		return ErrOutcomeUnknown.Error()
	}
	return ErrOutcomeUnknown.Error() + ": " + e.Cause.Error()
}

func (e *OutcomeUnknownError) Unwrap() error        { return e.Cause }
func (e *OutcomeUnknownError) Is(target error) bool { return target == ErrOutcomeUnknown }

func (r *RedisStore) preflight() error {
	if r == nil || nilInterface(r.client) {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("no Redis client")}
	}
	if r.closed.Load() {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("Redis store is closed")}
	}
	// The standalone Function accepts zero KEYS. A cluster router cannot route
	// this call to the one store that owns the configured namespace.
	if _, ok := r.client.(*redis.ClusterClient); ok {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("standalone tset requires a single-node client")}
	}
	if _, ok := r.client.(*redis.Ring); ok {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("standalone tset requires a single-node client")}
	}
	// The production constructor owns a client with no installed hooks and
	// effective MaxRetries=0. Keep this check as a guard around the package-local
	// test seam and to reject unsupported routers there.
	options, ok := r.client.(interface{ Options() *redis.Options })
	if !ok {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("tset client requires effective MaxRetries=0")}
	}
	configured := options.Options()
	if configured == nil || configured.MaxRetries != 0 {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("tset client requires effective MaxRetries=0")}
	}
	return nil
}

// UniversalClient is an interface, so a typed nil *redis.Client is not equal
// to nil. Check nil-able dynamic values before calling Options or any method.
func nilInterface(value interface{}) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// settledRedisCmd remembers when go-redis has parsed this command's RESP
// reply. v9.22 may subsequently stamp a pipeline-level transport error onto
// successful commands whose Err was nil. Preserve the per-command result in
// that case; a nil bulk reply (redis.Nil) and a Redis error reply are also
// complete, known RESP replies and must not inherit a sibling's failure.
type settledRedisCmd struct {
	*redis.Cmd
	settled bool
}

var _ redis.Cmder = (*settledRedisCmd)(nil)

func (cmd *settledRedisCmd) SetErr(err error) {
	if !cmd.settled {
		if err == nil || errors.Is(err, redis.Nil) {
			cmd.settled = true
		} else {
			var server redis.Error
			if errors.As(err, &server) {
				cmd.settled = true
			}
		}
		if cmd.settled {
			// Remember the complete RESP result, including nil bulk/array and
			// per-command Redis error replies.
			cmd.Cmd.SetErr(err)
			return
		}
	}
	// Once a RESP result is settled, ignore later aggregate errors from
	// go-redis. Before that point, retain the transport failure.
	if cmd.settled {
		return
	}
	cmd.Cmd.SetErr(err)
}

func (r *RedisStore) Step(ctx context.Context, step Step) (Reply, error) {
	raw, err := EncodeStep(step)
	if err != nil {
		return Reply{}, err
	}
	if err := r.preflight(); err != nil {
		return Reply{}, err
	}
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	text, err := r.client.FCall(ctx, "ns_tset_step", nil, Version, string(raw)).Text()
	if err != nil {
		return Reply{}, classifyWriteError(err, raw)
	}
	reply, err := DecodeReply([]byte(text))
	if err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			return Reply{}, refusal
		}
		return Reply{}, &OutcomeUnknownError{RawRequest: append([]byte(nil), raw...), Cause: err}
	}
	return reply, nil
}

func (r *RedisStore) Steps(ctx context.Context, steps []Step) ([]StepResult, error) {
	if len(steps) == 0 {
		return []StepResult{}, nil
	}
	// Encode every member before opening a pipeline. An error here proves that
	// none of the batch was dispatched.
	raw := make([][]byte, len(steps))
	for i, step := range steps {
		b, err := EncodeStep(step)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		raw[i] = b
	}
	if err := r.preflight(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pipe := r.client.Pipeline()
	cmds := make([]*settledRedisCmd, len(steps))
	for i, b := range raw {
		cmd := redis.NewCmd(ctx, "fcall", "ns_tset_step", 0, Version, string(b))
		cmds[i] = &settledRedisCmd{Cmd: cmd}
		if err := pipe.Process(ctx, cmds[i]); err != nil {
			// Process queues a command on go-redis's pipeline. A returned error
			// here is an enqueue failure, before Exec flushes the batch.
			return nil, err
		}
	}
	// Exec flushes this one pipeline. Its aggregate error is not a substitute
	// for the per-command results: known replies remain known.
	// ignored: each command keeps its own result (see the comment above); the aggregate is read per reply below
	_, _ = pipe.Exec(ctx)
	result := make([]StepResult, len(steps))
	for i, cmd := range cmds {
		result[i].RawRequest = append([]byte(nil), raw[i]...)
		value, err := cmd.Text()
		if err != nil {
			result[i].Err = classifyWriteError(err, raw[i])
			continue
		}
		reply, err := DecodeReply([]byte(value))
		if err != nil {
			var refusal *Refusal
			if errors.As(err, &refusal) {
				result[i].Err = refusal
			} else {
				result[i].Err = &OutcomeUnknownError{RawRequest: append([]byte(nil), raw[i]...), Cause: err}
			}
			continue
		}
		result[i].Reply = reply
	}
	return result, nil
}

func (r *RedisStore) Read(ctx context.Context, plan ReadPlan) (ReadReply, error) {
	raw, err := EncodeReadPlan(plan)
	if err != nil {
		return ReadReply{}, err
	}
	if err := r.preflight(); err != nil {
		return ReadReply{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReadReply{}, err
	}
	text, err := r.client.FCallRO(ctx, "ns_tset_read", nil, Version, string(raw)).Text()
	if err != nil {
		return ReadReply{}, classifyReadError(err)
	}
	return DecodeReadReply([]byte(text))
}

func classifyWriteError(err error, raw []byte) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, redis.ErrClosed) {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: err}
	}
	if code := preexecutionServerCode(err); code != "" {
		return &ClientError{Code: code, Cause: err}
	}
	return &OutcomeUnknownError{RawRequest: append([]byte(nil), raw...), Cause: err}
}

func classifyReadError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, redis.ErrClosed) {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: err}
	}
	if code := preexecutionServerCode(err); code != "" {
		return &ClientError{Code: code, Cause: err}
	}
	return err
}

// These are server errors emitted before FCALL enters the function. A script
// error can contain the same words after a write has begun, and a transport
// error can quote arbitrary text, so neither substring nor untyped text is
// evidence of a known write outcome.
func preexecutionServerCode(err error) string {
	var server redis.Error
	if !errors.As(err, &server) {
		return ""
	}
	message := server.Error()
	if message == "ERR Function not found" || message == "NOSUCHFUNCTION No matching function" {
		return "FUNCTIONMISSING"
	}
	if message == "OOM command not allowed when used memory > 'maxmemory'" || message == "OOM command not allowed when used memory > 'maxmemory'." {
		return "OOMSTART"
	}
	return ""
}
