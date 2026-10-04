// nova-friend scopes presence to a foreground harness invocation.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(friendTool(ctx).Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// friendTool uses the shared onboarding and output shape (STANDARD sections 2 and 3).
func friendTool(ctx context.Context) *tool.Tool {
	v := watchVerb(ctx)
	v.Example = `help
watch -h
watch --server unused:1 --friend reader --argv '["true"]' --dry-run`
	return &tool.Tool{Name: "nova-friend", What: "scopes sprint presence to a foreground harness invocation", Stamp: version,
		Stage:     "nova-friend is pre-alpha: not ready for production use.",
		How:       "A direct child command owns the foreground wait.\nThe explicit sprint server stores presence leases.\nCancellation, parent exit or an owned lifetime pipe ends presence.\nfirst run: inspect the plan; a real wait needs a sprint server and identity.",
		ExitTable: "0 the owned wait completed, 1 the wait or presence beat failed, 2 invalid invocation.", Verbs: []tool.Verb{v}}
}
