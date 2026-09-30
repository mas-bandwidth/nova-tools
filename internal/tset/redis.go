package tset

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a caller-supplied Redis connection. Construction neither
// contacts a server nor loads a Function library.
type RedisStore struct {
	client redis.UniversalClient
}

func NewRedis(client redis.UniversalClient) *RedisStore {
	return &RedisStore{client: client}
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
	if r == nil || r.client == nil {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("no Redis client")}
	}
	// The standalone Function accepts zero KEYS. A cluster router cannot route
	// this call to the one store that owns the configured space.
	if _, ok := r.client.(*redis.ClusterClient); ok {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("standalone tset requires a single-node client")}
	}
	if _, ok := r.client.(*redis.Ring); ok {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("standalone tset requires a single-node client")}
	}
	// go-redis normalizes MaxRetries:-1 to the effective value 0. The default
	// effective value is 3, which could silently resend a write after a lost
	// response. A caller-supplied fake must expose the same Options proof.
	options, ok := r.client.(interface{ Options() *redis.Options })
	if !ok || options.Options() == nil || options.Options().MaxRetries != 0 {
		return &ClientError{Code: "UNSUPPORTEDSTORE", Cause: errors.New("tset client requires effective MaxRetries=0")}
	}
	return nil
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
	cmds := make([]*redis.Cmd, len(steps))
	for i, b := range raw {
		cmds[i] = pipe.FCall(ctx, "ns_tset_step", nil, Version, string(b))
	}
	// Exec flushes this one pipeline. Its aggregate error is not a substitute
	// for the per-command results: known replies remain known.
	_, execErr := pipe.Exec(ctx)
	result := make([]StepResult, len(steps))
	for i, cmd := range cmds {
		result[i].RawRequest = append([]byte(nil), raw[i]...)
		value, err := cmd.Text()
		if err != nil {
			if execErr != nil && errors.Is(err, redis.Nil) {
				err = execErr
			}
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
	if code := preexecutionServerCode(err); code != "" {
		return &ClientError{Code: code, Cause: err}
	}
	return &OutcomeUnknownError{RawRequest: append([]byte(nil), raw...), Cause: err}
}

func classifyReadError(err error) error {
	if err == nil {
		return nil
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
