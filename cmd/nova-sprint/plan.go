// The plan verb (#2380) applies one sprint plan file in one atomic step and
// shows it against the store. It registers itself through the registry
// (registry.go), so main.go is unchanged. Who may apply is the Redis ACL of the
// connecting seat, checked inside ns_sprint_plan; this file makes no authority
// check of its own. show uses plain reads, so any seat can run it.
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
		Name:    "plan",
		Summary: "apply or show a sprint plan: policy and desired slots from one file, atomically",
		Run:     runPlanVerb,
	})
}

func runPlanVerb(ctx context.Context, args []string, out, errOut io.Writer) int {
	const want = "want apply --sprint <S> --plan <file.tsv> or show --sprint <S> [--redis host:port]"
	if len(args) == 0 {
		return refuse(errOut, "plan", want)
	}
	sub := args[0]
	switch sub {
	case "apply", "show":
	default:
		return refuse(errOut, "plan", fmt.Sprintf("unknown subverb %s; %s", sub, want))
	}
	verb := "plan " + sub
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	name := fs.String("sprint", "", "")
	file := fs.String("plan", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if !sprint.ValidName(*name) {
		return refuse(errOut, verb, "needs --sprint <name> ([a-z0-9-]{1,40})")
	}
	var body []byte
	if sub == "apply" {
		if *file == "" {
			return refuse(errOut, verb, "needs --plan <file.tsv>")
		}
		info, err := os.Stat(*file)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		if info.Size() > sprint.PlanMaxBytes {
			fmt.Fprintf(errOut, "PLAN-REFUSED line 1: plan is %d bytes; the limit is %d\n", info.Size(), sprint.PlanMaxBytes)
			return 1
		}
		if body, err = os.ReadFile(*file); err != nil {
			return refuse(errOut, verb, err.Error())
		}
	} else if *file != "" {
		return refuse(errOut, verb, "show reads the applied plan from the store; drop --plan")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	if sub == "show" {
		return sprint.Show(ctx, st, *name, out, errOut)
	}
	user := os.Getenv(store.UserEnv)
	if user == "" {
		user = "default"
	}
	return sprint.Applier{Store: st, User: user}.Apply(ctx, *name, body, out, errOut)
}
