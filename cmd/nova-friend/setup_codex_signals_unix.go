//go:build linux || darwin

package main

import (
	"context"
	"os/signal"
	"syscall"
)

// codexEnrollmentSignals turns lost SSH-terminal ownership into native setup
// cancellation and verified group cleanup (SPEC-CODEX-SUBSCRIPTION-SETUP).
func codexEnrollmentSignals(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, syscall.SIGHUP, syscall.SIGTERM)
}
