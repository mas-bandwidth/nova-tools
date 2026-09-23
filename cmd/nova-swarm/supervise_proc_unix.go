//go:build unix

package main

import (
	"context"
	"os/signal"
	"syscall"
)

// superviseTerm is SIGTERM as a channel the supervisor's watch loop can select on, so
// a TERM reaps the harness group instead of killing only this process and leaving it.
func superviseTerm() (<-chan struct{}, func()) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	return ctx.Done(), stop
}
