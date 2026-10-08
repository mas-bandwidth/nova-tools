package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// landRound runs one land over every stream with cards queued, as the sprint's
// coordinator, and prints what it did (test helper; the binary's loop runs landCycle
// and landAfter instead, so this is not on the binary's path).
func (a *app) landRound(ctx context.Context, addr string, more []string, stdout io.Writer) int {
	code, idle := a.landOnce(ctx, addr, more, stdout)
	if a.prune.due(idle, a.now()) {
		at := oneline.Field(a.now().Format("15:04:05"))
		for _, r := range a.flushPrune(ctx, false) {
			fmt.Fprintf(stdout, "%s %s\n", at, oneline.Escape(r.line(false)))
		}
	}
	a.promoteOnTick(ctx, stdout)
	return code
}
