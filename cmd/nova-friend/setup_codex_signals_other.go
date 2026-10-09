//go:build !linux && !darwin

package main

import "context"

// Other systems have no supported controlling-terminal enrollment route (spec).
func codexEnrollmentSignals(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
