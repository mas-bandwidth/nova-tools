// The sprint verb (#2939) opens, closes and reads a sprint's status. It
// registers itself through the registry (registry.go), so main.go is unchanged.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "sprint",
		Summary: "open, close and status: s:<S> status and the sprints set",
		Run:     runSprintVerb,
	})
}

func runSprintVerb(ctx context.Context, args []string, out, errOut io.Writer) int {
	const want = "want open, close or status --sprint <S> [--redis host:port]"
	if len(args) == 0 {
		return refuse(errOut, "sprint", want)
	}
	sub := args[0]
	switch sub {
	case "open", "close", "status":
	default:
		return refuse(errOut, "sprint", fmt.Sprintf("unknown subverb %s; %s", sub, want))
	}
	verb := "sprint " + sub
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	name := fs.String("sprint", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if !sprint.ValidName(*name) {
		return refuse(errOut, verb, "needs --sprint <name> ([a-z0-9-]{1,40})")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	var status sprint.Status
	switch sub {
	case "open":
		status, err = sprint.SetOpen(ctx, st, *name, time.Now())
	case "close":
		status, err = sprint.SetClosed(ctx, st, *name)
	default:
		status, err = sprint.Get(ctx, st, *name)
	}
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintln(out, sprint.Line(*name, status))
	return 0
}
