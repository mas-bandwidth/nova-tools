package redisconn

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// FirstError is the error of a pipeline, read so that no command's failure
// is missed. cmds and execErr are what the pipeline's Exec returned.
//
// Exec answers the first command that failed, and an absent value counts as
// a failure there: a pipeline whose first command read a field that is not
// set (redis.Nil) and whose later command was refused answers the Nil, the
// caller who expects absent fields lets it pass, and the refusal is never
// seen. FirstError walks every command instead: the first error that is not
// redis.Nil is the pipeline's error; when no command carries one, execErr
// is, unless it is redis.Nil; otherwise nil. So nil means every command
// either succeeded or found nothing, and an error is a real one, the
// earliest in the order the commands were queued.
func FirstError(cmds []redis.Cmder, execErr error) error {
	for _, cmd := range cmds {
		if err := cmd.Err(); err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
	}
	if execErr != nil && !errors.Is(execErr, redis.Nil) {
		return execErr
	}
	return nil
}

// Exec runs the pipeline and answers FirstError over what it returned: the
// one call a reader of values that may be absent makes in place of a bare
// Exec. It is one round trip. The commands keep their own results and
// errors for the caller to read afterwards.
func Exec(ctx context.Context, pipe redis.Pipeliner) error {
	cmds, err := pipe.Exec(ctx)
	return FirstError(cmds, err)
}
