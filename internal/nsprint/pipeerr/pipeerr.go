// Package pipeerr is the one reading of a go-redis pipeline result that
// misses no command's error (Glenn 2026-09-26: no verb fails silently).
package pipeerr

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// First is the error of a pipeline whose commands may answer redis.Nil
// (an HGET of an absent field). go-redis's Exec returns the FIRST failed
// command's error, and a Nil counts: a pipeline whose first absent field
// comes before a NOPERM, a WRONGTYPE or a lost connection on a later
// command reports the Nil, the caller ignores it, and the later failure is
// never seen (its Val() reads as empty or zero). First walks every command
// instead: the first error that is not redis.Nil is the pipeline's error;
// with none, a non-Nil execErr (the transport) is; else nil.
func First(cmds []redis.Cmder, execErr error) error {
	for _, c := range cmds {
		if err := c.Err(); err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
	}
	if execErr != nil && !errors.Is(execErr, redis.Nil) {
		return execErr
	}
	return nil
}

// Exec runs the pipeline and answers First over its commands: the one call
// a reader of absent-able fields makes in place of a bare Exec.
func Exec(ctx context.Context, p redis.Pipeliner) error {
	cmds, err := p.Exec(ctx)
	return First(cmds, err)
}
